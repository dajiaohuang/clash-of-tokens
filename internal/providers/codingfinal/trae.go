package codingfinal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	traeDefaultBase = "https://core-normal.trae.ai/api/remote/v1"
	traeUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"
	traeMaxSSELine  = 1 << 20
)

// EncodeTraeRequest builds the first-turn request used by the pinned Trae
// solo_agent_remote executor. The caller supplies an explicit account value
// through Source.AccountIDEnv when Trae requires a web identity.
func EncodeTraeRequest(req chatRequest, model, account string) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return nil, fmt.Errorf("%w: Trae tool execution is not implemented", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return nil, fmt.Errorf("%w: Trae request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return nil, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	for _, message := range req.Messages {
		if message.Role == "tool" || len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("%w: Trae tool history is not implemented", ErrUnsupported)
		}
	}

	mode, strategy, modelName := traeModelSelection(model)
	common := map[string]any{
		"language":        "en-us",
		"app_language":    "en",
		"quality":         "stable",
		"app_version":     "1.0.0.1229",
		"web_id":          account,
		"user_identity":   "Free",
		"is_freshman":     "0",
		"biz_user_id":     "",
		"user_unique_id":  "",
		"scope":           "marscode-us",
		"tenant":          "marscode",
		"region":          "US-East",
		"aiRegion":        "US-East",
		"is_privacy_mode": 0,
		"privacy_mode":    "off",
		"solo_chat_mode":  mode,
	}
	commonJSON, err := json.Marshal(common)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode Trae common parameters", ErrUnsupported)
	}
	query := traeQuery(req.Messages)
	payload := map[string]any{
		"mode":           mode,
		"environment_id": "default",
		"initial_message": map[string]any{
			"chat_session_id":          "",
			"content":                  []any{},
			"query":                    query,
			"model_name":               modelName,
			"agent_type":               "solo_agent_remote",
			"model_selection_strategy": strategy,
			"common_params":            string(commonJSON),
		},
		"env":                 "remote",
		"auto_create_project": false,
		"origin":              "web",
	}
	return json.Marshal(payload)
}

func traeModelSelection(model string) (mode, strategy, modelName string) {
	m := strings.TrimSpace(model)
	lower := strings.ToLower(strings.TrimPrefix(m, "trae/"))
	if lower == "work" || lower == "auto-work" || lower == "solo-work" {
		return "work", "auto", ""
	}
	if lower == "" || lower == "auto" {
		return "code", "auto", ""
	}
	return "code", "manual", m
}

func traeQuery(messages []chatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := message.Content
		switch message.Role {
		case "system":
			parts = append(parts, "[System]\n"+text)
		case "assistant":
			parts = append(parts, "[Assistant]\n"+text)
		default:
			parts = append(parts, text)
		}
	}
	data, _ := json.Marshal([]map[string]any{{"type": "text", "data": map[string]string{"content": strings.Join(parts, "\n\n")}}})
	return string(data)
}

func traeBase(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = traeDefaultBase
	}
	root, err := endpointBaseSource(base)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(root, "/chat_sessions"), nil
}

// endpointBaseSource keeps the default Trae URL separate from the config
// helper's Source-shaped API while preserving its HTTPS and loopback rules.
func endpointBaseSource(base string) (string, error) {
	return endpointBase(config.Source{BaseURL: base})
}

func traeHeaders(token string) http.Header {
	return http.Header{
		"Accept":                 []string{"application/json"},
		"Authorization":          []string{"Cloud-IDE-JWT " + token},
		"Content-Type":           []string{"application/json"},
		"Referer":                []string{"https://solo.trae.ai/"},
		"User-Agent":             []string{traeUserAgent},
		"X-Preferenced-Language": []string{"en"},
		"X-Trae-Client-Type":     []string{"web"},
		"x-user-region":          []string{"US"},
	}
}

func (c *Client) doTrae(ctx context.Context, token, model string, req chatRequest, _ *session) (*http.Response, error) {
	base, err := traeBase(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	account := ""
	if strings.TrimSpace(c.source.AccountIDEnv) != "" {
		account = strings.TrimSpace(os.Getenv(c.source.AccountIDEnv))
		if len(account) > maxHeaderValue || strings.ContainsAny(account, "\r\n\x00") {
			return nil, fmt.Errorf("%w: Trae account identity is invalid", ErrCredential)
		}
	}
	body, err := EncodeTraeRequest(req, model, account)
	if err != nil {
		return nil, err
	}
	create, err := post(ctx, c.http, base+"/chat_sessions", body, traeHeaders(token))
	if err != nil {
		return nil, err
	}
	if create.StatusCode < 200 || create.StatusCode >= 300 {
		return create, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(create.Body, maxEventBytes+1))
	_ = create.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if int64(len(data)) > maxEventBytes {
		return nil, fmt.Errorf("%w: Trae session response exceeds byte limit", ErrUnsupported)
	}
	var created struct {
		Code int `json:"code"`
		Data struct {
			SessionID string `json:"chat_session_id"`
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &created) != nil || created.Code != 0 || created.Data.SessionID == "" || created.Data.MessageID == "" {
		return nil, fmt.Errorf("%w: Trae session response is invalid", ErrTruncated)
	}

	eventsURL := base + "/chat_sessions/" + url.PathEscape(created.Data.SessionID) + "/events?reply_to_message_id=" + url.QueryEscape(created.Data.MessageID)
	headers := traeHeaders(token)
	headers.Set("Accept", "text/event-stream")
	eventReq, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid Trae events request", ErrUnsupported)
	}
	eventReq.Header = headers
	events, err := c.http.Do(eventReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding final adapter: Trae events transport failed")
	}
	return events, nil
}

type traeUsage struct {
	prompt, completion, total uint64
}

func traeToSSE(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), traeMaxSSELine)
	var out bytes.Buffer
	id := "chatcmpl-trae-" + randomUUID()
	created := time.Now().Unix()
	thoughts := map[string]string{}
	order := make([]string, 0, 8)
	sent := 0
	role := false
	sawEvent := false
	doneSeen := false
	usageSeen := false
	var usage traeUsage
	eventName := ""
	eventHasData := false
	var parseErr error

	process := func(name, payload string) {
		if parseErr != nil || strings.TrimSpace(payload) == "" && name != "done" {
			return
		}
		name = strings.TrimSpace(name)
		if doneSeen {
			parseErr = fmt.Errorf("%w: Trae data follows done event", ErrTruncated)
			return
		}
		if name == "done" {
			if doneSeen {
				parseErr = fmt.Errorf("%w: duplicate Trae done event", ErrTruncated)
				return
			}
			doneSeen = true
			return
		}
		var value map[string]any
		if json.Unmarshal([]byte(payload), &value) != nil || value == nil {
			parseErr = fmt.Errorf("%w: malformed Trae SSE event", ErrTruncated)
			return
		}
		sawEvent = true
		switch name {
		case "error":
			parseErr = fmt.Errorf("coding final adapter: upstream error: %s", streamErrorText(value))
		case "token_usage":
			usage.prompt = traeUint(value["prompt_tokens"])
			usage.completion = traeUint(value["completion_tokens"])
			usage.total = traeUint(value["total_tokens"])
			usageSeen = true
		case "plan_item":
			pid, _ := value["id"].(string)
			thought, _ := value["thought"].(string)
			if pid == "" || thought == "" {
				return
			}
			if previous, ok := thoughts[pid]; ok && len(thought) < len(previous) {
				return
			}
			if _, ok := thoughts[pid]; !ok {
				order = append(order, pid)
			}
			thoughts[pid] = thought
			full := ""
			for _, key := range order {
				full += thoughts[key]
			}
			if len(full) <= sent {
				return
			}
			piece := full[sent:]
			sent = len(full)
			if !role {
				appendCursorRole(&out, id, created, model)
				role = true
			}
			appendCursorContent(&out, id, created, model, piece)
		}
	}

	var inputTotal int64
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		inputTotal += int64(len(line) + 1)
		if inputTotal > maxEventBytes {
			return nil, fmt.Errorf("%w: Trae response exceeds byte limit", ErrUnsupported)
		}
		if line == "" {
			if eventName != "" && !eventHasData {
				process(eventName, "{}")
			}
			eventName = ""
			eventHasData = false
			if doneSeen {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			process(eventName, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			eventHasData = true
		}
		if parseErr != nil {
			return nil, parseErr
		}
		if int64(out.Len()) > maxOutputBytes {
			return nil, fmt.Errorf("%w: Trae output exceeds byte limit", ErrUnsupported)
		}
		if doneSeen {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}
	if eventName != "" && !eventHasData && !doneSeen {
		process(eventName, "{}")
	}
	if !sawEvent || !doneSeen {
		return nil, ErrTruncated
	}
	if !role {
		return nil, ErrTruncated
	}
	appendCursorFinish(&out, id, created, model, "stop")
	if usageSeen {
		// appendCursorFinish already emitted [DONE]; usage must precede it for a
		// valid OpenAI stream, so rebuild the final marker with a usage chunk.
		body := out.Bytes()
		marker := []byte("data: [DONE]\n\n")
		body = bytes.TrimSuffix(body, marker)
		var withUsage bytes.Buffer
		withUsage.Write(body)
		appendSSE(&withUsage, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{}, "usage": map[string]any{"prompt_tokens": usage.prompt, "completion_tokens": usage.completion, "total_tokens": usage.total}})
		withUsage.Write(marker)
		out = withUsage
	}
	if int64(out.Len()) > maxOutputBytes {
		return nil, fmt.Errorf("%w: Trae output exceeds byte limit", ErrUnsupported)
	}
	return out.Bytes(), nil
}

func traeUint(value any) uint64 {
	switch n := value.(type) {
	case float64:
		if n >= 0 && n <= float64(^uint64(0)) && n == float64(uint64(n)) {
			return uint64(n)
		}
	case json.Number:
		if parsed, err := n.Int64(); err == nil && parsed >= 0 {
			return uint64(parsed)
		}
	case int:
		if n >= 0 {
			return uint64(n)
		}
	}
	return 0
}
