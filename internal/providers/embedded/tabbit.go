package embedded

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var tabbitUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

var tabbitModelMap = map[string]string{
	"best":                   "最佳",
	"default":                "Default",
	"kimi-k3":                "Kimi-K3",
	"longcat-2-0":            "LongCat-2.0",
	"glm-5-2":                "GLM-5.2",
	"qwen3-7-max":            "Qwen3.7-Max",
	"kimi-k2-7-code":         "Kimi-K2.7-Code",
	"deepseek-v4-pro":        "DeepSeek-V4-Pro",
	"deepseek-v4-flash":      "DeepSeek-V4-Flash",
	"doubao-seed-2-1-pro":    "Doubao-Seed-2.1-Pro",
	"doubao-seed-2-1-turbo":  "Doubao-Seed-2.1-Turbo",
	"minimax-m3":             "MiniMax-M3",
	"glm-5-1":                "GLM-5.1",
	"glm-5v-turbo":           "GLM-5V-Turbo",
	"kimi-k2-6":              "Kimi-K2.6",
	"kimi-k2-5":              "Kimi-K2.5",
	"minimax-m2-7":           "MiniMax-M2.7",
	"doubao-seed-2-0-lite":   "Doubao-Seed-2.0-lite",
	"qwen3-5-plus":           "Qwen3.5-Plus",
	"longcat-flash-chat":     "LongCat-Flash-Chat",
	"longcat-flash-thinking": "LongCat-Flash-Thinking",
}

func (c *Client) doTabbit(ctx context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" {
		return nil, errors.New("tabbit adapter supports only chat protocol")
	}
	cookie, err := c.credential()
	if err != nil {
		return nil, err
	}
	base, err := c.baseURL(tabbitDefaultBase)
	if err != nil {
		return nil, err
	}
	req, err := parseChatRequest(body)
	if err != nil {
		return nil, err
	}
	content, err := messageContent(req.Messages)
	if err != nil {
		return nil, err
	}
	session, err := c.tabbitSession(ctx, base, cookie)
	if err != nil {
		return nil, err
	}
	selected := tabbitModel(model)
	payload := tabbitPayload(session, content, selected, true)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("cannot encode tabbit request")
	}
	resp, err := c.tabbitChat(ctx, base, cookie, "/api/v2/chat/completion", payloadBytes)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// tabbit-toy and tabbit2api at the pinned commits use v1, while the
		// reverse-engineering document records v2 as the current endpoint.
		// Keep v2 as the first choice and retain a bounded compatibility retry.
		_ = resp.Body.Close()
		payload = tabbitPayload(session, content, selected, false)
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return nil, errors.New("cannot encode tabbit compatibility request")
		}
		resp, err = c.tabbitChat(ctx, base, cookie, "/api/v1/chat/completion", payloadBytes)
		if err != nil {
			return nil, err
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if stream && resp.Header.Get("Content-Type") != "" && !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		_ = resp.Body.Close()
		return nil, errors.New("tabbit upstream did not return an SSE stream")
	}
	id := chatID()
	created := time.Now().Unix()
	streamBody := c.newStreamBody(resp.Body, id, model, func(event, data string) ([]byte, error) {
		return tabbitEvent(event, data, id, model, created)
	})
	return c.responseFromStream(resp, streamBody, stream, id, model)
}

func tabbitModel(model string) string {
	if value, ok := tabbitModelMap[strings.ToLower(model)]; ok {
		return value
	}
	if model == "" {
		return "Default"
	}
	return model
}

func tabbitPayload(session, content, model string, v2 bool) map[string]any {
	entityHash := md5.Sum(nil)
	payload := map[string]any{
		"chat_session_id":   session,
		"message_id":        nil,
		"content":           content,
		"selected_model":    model,
		"parallel_group_id": nil,
		"task_name":         "chat",
		"agent_mode":        false,
		"metadatas":         map[string]any{"html_content": "<p>" + html.EscapeString(content) + "</p>"},
		"references":        []any{},
		"entity":            map[string]any{"key": hex.EncodeToString(entityHash[:]), "extras": map[string]any{"type": "tab", "url": ""}},
	}
	if v2 {
		payload["client_turn_id"] = newUUID()
		payload["stream_mode"] = "sse"
		payload["force_execute"] = false
		payload["task_name"] = "CHAT"
	}
	return payload
}

func (c *Client) tabbitSession(ctx context.Context, base, cookie string) (string, error) {
	// Create a fresh server-side session for each stateless request. Never select
	// an unrelated existing conversation from the account's session list.
	headers := c.tabbitBaseHeaders(base, cookie)
	headers.Set("Accept", "application/json")
	headers.Set("Content-Type", "application/json")
	resp, err := c.request(ctx, http.MethodPost, base+"/panel/session", nil, headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tabbit session creation returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("invalid tabbit session response")
	}
	var v struct {
		ID string `json:"chat_session_id"`
	}
	if json.Unmarshal(data, &v) != nil || len(v.ID) != 36 || !tabbitUUIDPattern.MatchString(v.ID) {
		return "", errors.New("tabbit session creation returned no valid id")
	}
	initHeaders := c.tabbitBaseHeaders(base, cookie)
	initHeaders.Set("rsc", "1")
	initHeaders.Set("next-url", "/newtab")
	initHeaders.Set("next-router-state-tree", "%5B%22%22%2C%7B%22children%22%3A%5B%22newtab%22%2C%7B%22children%22%3A%5B%22__PAGE__%22%2C%7B%7D%2Cnull%2Cnull%5D%7D%2Cnull%2Cnull%5D%7D%2Cnull%2Cnull%2Ctrue%5D")
	init, err := c.request(ctx, http.MethodGet, base+"/session/"+v.ID+"?_rsc=kfw4t", nil, initHeaders)
	if err != nil {
		return "", err
	}
	defer init.Body.Close()
	if init.StatusCode != 200 {
		return "", fmt.Errorf("tabbit session initialization returned status %d", init.StatusCode)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(init.Body, 64<<10))
	if err != nil {
		return "", errors.New("tabbit session initialization failed")
	}
	return v.ID, nil
}

func (c *Client) tabbitBaseHeaders(base, cookie string) http.Header {
	version := strings.TrimSpace(c.source.Project)
	if version == "" {
		version = tabbitDefaultVersion
	}
	return http.Header{
		"Cookie":      []string{cookie},
		"trace-id":    []string{newUUID()},
		"User-Agent":  []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"},
		"Origin":      []string{base},
		"Referer":     []string{base + "/"},
		"x-req-ctx":   []string{base64.StdEncoding.EncodeToString([]byte(version))},
		"unique-uuid": []string{tabbitUniqueUUID(true)},
	}
}

func tabbitUniqueUUID(defaultBrowser bool) string {
	const markerPos = 5
	positions := [...]int{2, 7, 11, 14, 18, 21, 25, 28}
	seconds := fmt.Sprintf("%08x", time.Now().Unix())
	var raw [32]byte
	if _, err := crand.Read(raw[:]); err != nil {
		copy(raw[:], strings.Repeat("0", len(raw)))
	}
	for i := range raw {
		raw[i] = "0123456789abcdef"[int(raw[i])%16]
	}
	for i, position := range positions {
		raw[position] = seconds[i]
	}
	if defaultBrowser {
		raw[markerPos] = '1'
	}
	value := string(raw[:])
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:]
}

func (c *Client) tabbitChat(ctx context.Context, base, cookie, path string, body []byte) (*http.Response, error) {
	key := c.tabbitSignKey()
	headers := c.tabbitBaseHeaders(base, cookie)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	for name, value := range tabbitSignHeaders(body, key) {
		headers.Set(name, value)
	}
	resp, err := c.request(ctx, http.MethodPost, base+path, body, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 499 {
		_ = resp.Body.Close()
		if c.tabbitRefreshSignKey(ctx, base, cookie) {
			key = c.tabbitSignKey()
			for name, value := range tabbitSignHeaders(body, key) {
				headers.Set(name, value)
			}
			return c.request(ctx, http.MethodPost, base+path, body, headers)
		}
	}
	return resp, nil
}

func (c *Client) tabbitSignKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tabbitKey == "" {
		return tabbitDefaultSignKey
	}
	return c.tabbitKey
}

func tabbitSignHeaders(body []byte, key string) map[string]string {
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	signature := newUUID()
	digest := sha256.Sum256(body)
	message := timestamp + "." + signature + "." + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(message))
	return map[string]string{
		"x-timestamp": timestamp,
		"x-signature": signature,
		"x-nonce":     hex.EncodeToString(mac.Sum(nil)),
	}
}

func (c *Client) tabbitRefreshSignKey(ctx context.Context, base, cookie string) bool {
	resp, err := c.request(ctx, http.MethodGet, base+"/chat/sign-key", nil, c.tabbitBaseHeaders(base, cookie))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	if err != nil {
		return false
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return false
	}
	c.mu.Lock()
	c.tabbitKey = key
	c.mu.Unlock()
	return true
}

func tabbitEvent(event, data, id, model string, created int64) ([]byte, error) {
	if data == "[DONE]" || event == "message_finish" || event == "finish" || event == "close" {
		return nil, errStreamComplete
	}
	if data == "" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return nil, fmt.Errorf("invalid tabbit SSE event: %w", err)
	}
	if event == "error" {
		return nil, errors.New("tabbit API returned an error event")
	}
	if event == "message_finish" || event == "finish" || event == "close" {
		return nil, nil
	}
	if event == "" {
		event = "message_chunk"
	}
	if event != "message_chunk" {
		return nil, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("tabbit message_chunk is not an object")
	}
	content, ok := object["content"].(string)
	if !ok || content == "" {
		return nil, nil
	}
	return chatChunk(id, model, created, map[string]any{"content": content}, nil), nil
}
