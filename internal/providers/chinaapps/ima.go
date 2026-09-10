// Package chinaapps implements source-bound Chinese application assistants.
// IMA wire reference: 1icc0/ima2api, 3fc4bd7ed07bd26eccb17327661e62f4fe4b612e.
package chinaapps

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/protocol"
)

const maxBytes = 4 << 20

type requestError string

func (e requestError) Error() string   { return string(e) }
func (e requestError) HTTPStatus() int { return 422 }

const ErrUnsupported = requestError("ima: only a single user text message is supported; tools, history, sessions and generation controls are unavailable")

var ErrCredential = errors.New("ima: source key must contain the account's x-ima-cookie with IMA-TOKEN")
var ErrIncomplete = errors.New("ima: missing successful completion event")

type Client struct {
	source config.Source
	http   *http.Client
}

func New(s config.Source) *Client {
	t := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: max(4, min(s.MaxInflight, 256)), MaxIdleConnsPerHost: max(4, min(s.MaxInflight, 256)), MaxConnsPerHost: max(1, s.MaxInflight), IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, MaxResponseHeaderBytes: 64 << 10}
	return &Client{s, &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

type modelInfo struct {
	Type int    `json:"model_type"`
	ID   string `json:"model_id"`
}

var imaModels = map[string]modelInfo{
	"hy3-preview": {0, "official_0"}, "hy3-preview-think": {2, "official_2"},
	"deepseek-v4-flash": {3, "official_3"}, "deepseek-v4-flash-think": {1, "official_1"},
	"glm-5.2": {3000, "official_3000"}, "glm-5.2-think": {3001, "official_3001"},
}

func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, caller http.Header) (*http.Response, error) {
	if c.source.Adapter == "weread-ai" {
		return c.doWeRead(ctx, proto, model, stream, body, caller)
	}
	info, ok := imaModels[model]
	if proto != "chat" || !ok || caller.Get("X-COT-Session") != "" || len(body) > 1<<20 {
		return nil, ErrUnsupported
	}
	var input struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || len(input.Messages) != 1 || input.Messages[0].Role != "user" || strings.TrimSpace(input.Messages[0].Content) == "" {
		return nil, ErrUnsupported
	}
	if !utf8.Valid(body) {
		return nil, ErrUnsupported
	}
	cookie := strings.TrimSpace(c.source.CredentialValue())
	if strings.HasPrefix(cookie, "{") {
		var cred struct {
			Cookie string `json:"cookie"`
		}
		d := json.NewDecoder(strings.NewReader(cookie))
		d.DisallowUnknownFields()
		if d.Decode(&cred) != nil || d.Decode(new(any)) != io.EOF {
			return nil, ErrCredential
		}
		cookie = cred.Cookie
	}
	token := ""
	for _, p := range strings.Split(cookie, ";") {
		key, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && key == "IMA-TOKEN" {
			if token != "" {
				return nil, ErrCredential
			}
			token = v
		}
	}
	if token == "" || len(cookie) > 64<<10 || strings.ContainsAny(cookie, "\r\n") {
		return nil, ErrCredential
	}
	base := strings.TrimRight(c.source.BaseURL, "/")
	u, err := url.Parse(base)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" || u.Path != "" {
		return nil, errors.New("ima: invalid source base URL")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("ima: HTTPS required")
	}
	question := input.Messages[0].Content
	title := []rune(question)
	if len(title) > 50 {
		title = title[:50]
	}
	initPayload := map[string]any{"env_info": map[string]int{"interact_type": 2, "robot_type": 10000}, "name": string(title), "msgs_limit": 20}
	resp, err := c.post(ctx, base+"/cgi-bin/session_logic/init_session", base, cookie, token, initPayload, false)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	var initialized struct {
		Code      *int   `json:"code"`
		SessionID string `json:"session_id"`
	}
	raw, err := readJSON(resp.Body)
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(raw, &initialized) != nil || initialized.Code == nil || *initialized.Code != 0 || initialized.SessionID == "" || len(initialized.SessionID) > 1024 {
		return nil, errors.New("ima: session initialization rejected")
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"session_id": initialized.SessionID, "robot_type": 10000, "question": question, "question_type": 2, "command_info": map[string]any{"question_info": map[string]any{}}, "client_id": id, "model_info": info}
	resp, err = c.post(ctx, base+"/cgi-bin/assistant/qa", base, cookie, token, payload, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil, errors.New("ima: expected SSE response")
	}
	answer, err := decodeIMA(resp.Body)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return completion("chatcmpl-"+id, model, answer, stream), nil
}

func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}
func bkn(token string) string {
	h := uint32(5381)
	for _, v := range utf16.Encode([]rune(token)) {
		h = h*33 + uint32(v)
	}
	return strconv.FormatUint(uint64(h&0x7fffffff), 10)
}
func (c *Client) post(ctx context.Context, endpoint, origin, cookie, token string, payload any, sse bool) (*http.Response, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("from_browser_ima", "1")
	req.Header.Set("x-ima-cookie", cookie)
	req.Header.Set("x-ima-bkn", bkn(token))
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin)
	req.Header.Set("User-Agent", "okhttp/4.12.0")
	if sse {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("ima: upstream transport failed")
	}
	return resp, nil
}
func readJSON(body io.ReadCloser) ([]byte, error) {
	defer body.Close()
	b, e := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > maxBytes || !json.Valid(b) {
		return nil, errors.New("chinaapps: invalid or oversized JSON response")
	}
	return b, nil
}

func decodeIMA(body io.Reader) (string, error) {
	limited := &io.LimitedReader{R: body, N: maxBytes + 1}
	sse := protocol.NewSSEReader(limited, 1<<20)
	var out strings.Builder
	complete := false
	for {
		frame, err := sse.Next()
		if limited.N <= 0 {
			return "", errors.New("ima: response exceeds byte limit")
		}
		if err == io.EOF {
			if complete && out.Len() > 0 {
				return out.String(), nil
			}
			return "", ErrIncomplete
		}
		if err != nil {
			return "", err
		}
		event := ""
		for _, line := range bytes.Split(frame, []byte{'\n'}) {
			line = bytes.TrimSuffix(line, []byte{'\r'})
			if bytes.HasPrefix(line, []byte("event:")) {
				event = strings.TrimSpace(string(line[6:]))
			}
		}
		switch event {
		case "INNER_EXCEPTION", "ERROR", "FAILED":
			return "", errors.New("ima: upstream generation failed")
		case "COMPLETED":
			data := protocol.SSEData(frame)
			if len(data) > 0 {
				var control map[string]any
				if json.Unmarshal(data, &control) != nil || control == nil {
					return "", errors.New("ima: malformed completion event")
				}
				if code, exists := control["code"]; exists && code != float64(0) {
					return "", errors.New("ima: rejected completion event")
				}
			}
			complete = true
			continue
		case "CLOSE":
			if !complete {
				return "", ErrIncomplete
			}
			continue
		}
		data := protocol.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if complete {
			return "", errors.New("ima: data after completion")
		}
		var value any
		if !utf8.Valid(data) || json.Unmarshal(data, &value) != nil {
			return "", errors.New("ima: invalid event JSON")
		}
		text := ""
		switch v := value.(type) {
		case string:
			text = v
		case map[string]any:
			if code, exists := v["code"]; exists && code != float64(0) {
				return "", errors.New("ima: upstream event rejected")
			}
			for _, key := range []string{"Text", "text", "Content", "content", "Delta", "delta", "Msg", "msg", "reply", "Reply", "answer", "Answer"} {
				if s, ok := v[key].(string); ok && s != "" {
					text = s
					break
				}
			}
		}
		if out.Len()+len(text) > maxBytes {
			return "", errors.New("ima: answer exceeds byte limit")
		}
		out.WriteString(text)
	}
}
func completion(id, model, text string, stream bool) *http.Response {
	created := time.Now().Unix()
	var data []byte
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("X-COT-Delivery", "buffered")
	if stream {
		h.Set("Content-Type", "text/event-stream")
		var out bytes.Buffer
		for _, choice := range []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": text}, "finish_reason": nil}, map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}} {
			b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{choice}})
			fmt.Fprintf(&out, "data: %s\n\n", b)
		}
		out.WriteString("data: [DONE]\n\n")
		data = out.Bytes()
	} else {
		data, _ = json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": nil})
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}
