// Package businessweb contains native, opt-in clients for the five private
// business and workspace sources whose wire contracts are documented in the
// pinned source inventory. It does not create accounts or refresh credentials.
package businessweb

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	AdapterGeminiBusiness     = "gemini-business"
	AdapterAIStudioBuild      = "aistudio-build"
	AdapterAIStudioPlayground = "aistudio-playground"
	AdapterCopilotM365        = "copilot-m365"
	AdapterPromptQL           = "promptql"

	maxRequestBytes  = 1 << 20
	maxEventBytes    = 1 << 20
	maxResponseBytes = 16 << 20
)

var (
	ErrUnsupported = errors.New("business web adapter: unsupported protocol or request field")
	ErrCredential  = errors.New("business web adapter: source credential is missing or invalid")
	ErrTruncated   = errors.New("business web adapter: truncated upstream response")
)

type unsupportedError struct{ message string }

func (e *unsupportedError) Error() string   { return e.message }
func (e *unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }
func (e *unsupportedError) Unwrap() error   { return ErrUnsupported }

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("business web adapter: upstream status %d", e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type Client struct {
	source  config.Source
	http    *http.Client
	browser config.Browser
	mu      sync.Mutex
	closed  bool
}

type chatMessage struct {
	Role    string
	Content any
}
type chatRequest struct {
	Model    string
	Messages []chatMessage
	Stream   bool
	ThreadID string
}

func Supports(adapter string) bool {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterGeminiBusiness, AdapterAIStudioBuild, AdapterAIStudioPlayground, AdapterCopilotM365, AdapterPromptQL:
		return true
	default:
		return false
	}
}

func New(source config.Source, browsers ...config.Browser) *Client {
	max := source.MaxInflight
	if max < 1 {
		max = 4
	}
	if max > 64 {
		max = 64
	}
	tr := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: max, MaxIdleConnsPerHost: max,
		MaxConnsPerHost: max, IdleConnTimeout: 60 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 90 * time.Second,
		MaxResponseHeaderBytes: 64 << 10, DisableCompression: true,
	}
	browser := config.Default().Browser
	if len(browsers) > 0 {
		browser = browsers[0]
	}
	return &Client{source: source, browser: browser, http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("business web adapter: nil client")
	}
	if ctx == nil {
		return nil, errors.New("business web adapter: nil request context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if protocol != "chat" {
		return nil, &unsupportedError{"business web adapter: only chat protocol is supported"}
	}
	if len(body) == 0 || len(body) > maxRequestBytes {
		return nil, &unsupportedError{"business web adapter: request exceeds limit"}
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("business web adapter: client is closed")
	}
	req, err := parseChat(body, model, stream)
	if err != nil {
		return nil, err
	}
	if c.source.Adapter != AdapterPromptQL && (req.ThreadID != "" || (headers != nil && headers.Get("X-PromptQL-Thread-Id") != "")) {
		return nil, &unsupportedError{"business web adapter: thread selector is only valid for PromptQL"}
	}
	if c.source.Adapter == AdapterPromptQL && headers != nil && req.ThreadID == "" {
		req.ThreadID = strings.TrimSpace(headers.Get("X-PromptQL-Thread-Id"))
	}
	if headers != nil && strings.TrimSpace(headers.Get("X-COT-Session")) != "" && c.source.Adapter != AdapterPromptQL {
		return nil, &unsupportedError{"business web adapter: source creates a temporary turn per request"}
	}
	switch strings.ToLower(strings.TrimSpace(c.source.Adapter)) {
	case AdapterGeminiBusiness:
		return c.doGeminiBusiness(ctx, req)
	case AdapterAIStudioBuild:
		return c.doAIStudioBuild(ctx, req)
	case AdapterAIStudioPlayground:
		return c.doAIStudioPlayground(ctx, req)
	case AdapterCopilotM365:
		return c.doCopilotM365(ctx, req)
	case AdapterPromptQL:
		return c.doPromptQL(ctx, req)
	default:
		return nil, &unsupportedError{"business web adapter: unknown adapter"}
	}
}

func parseChat(body []byte, selectedModel string, stream bool) (chatRequest, error) {
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return chatRequest{}, &unsupportedError{"business web adapter: request must be one JSON object"}
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return chatRequest{}, &unsupportedError{"business web adapter: request must contain one JSON value"}
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" && key != "promptql_thread_id" {
			return chatRequest{}, &unsupportedError{fmt.Sprintf("business web adapter: unsupported field %q", key)}
		}
	}
	var model string
	if raw := fields["model"]; len(raw) > 0 && json.Unmarshal(raw, &model) != nil {
		return chatRequest{}, &unsupportedError{"business web adapter: model must be text"}
	}
	if model != "" && selectedModel != "" && model != selectedModel {
		return chatRequest{}, &unsupportedError{"business web adapter: request model conflicts with selected model"}
	}
	if strings.TrimSpace(selectedModel) == "" {
		return chatRequest{}, &unsupportedError{"business web adapter: model is required"}
	}
	var rawMessages []json.RawMessage
	if json.Unmarshal(fields["messages"], &rawMessages) != nil || len(rawMessages) == 0 {
		return chatRequest{}, &unsupportedError{"business web adapter: messages must be a non-empty array"}
	}
	var messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}
	for _, raw := range rawMessages {
		var messageFields map[string]json.RawMessage
		if json.Unmarshal(raw, &messageFields) != nil || messageFields == nil {
			return chatRequest{}, &unsupportedError{"business web adapter: each message must be one object"}
		}
		for key := range messageFields {
			if key != "role" && key != "content" {
				return chatRequest{}, &unsupportedError{fmt.Sprintf("business web adapter: unsupported message field %q", key)}
			}
		}
		var message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		}
		if json.Unmarshal(raw, &message) != nil {
			return chatRequest{}, &unsupportedError{"business web adapter: invalid message"}
		}
		messages = append(messages, message)
	}
	out := chatRequest{Model: selectedModel, Stream: stream}
	for _, m := range messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return chatRequest{}, &unsupportedError{"business web adapter: unsupported message role"}
		}
		text, ok := textContent(m.Content)
		if !ok || strings.TrimSpace(text) == "" {
			return chatRequest{}, &unsupportedError{"business web adapter: only non-empty text content is supported"}
		}
		out.Messages = append(out.Messages, chatMessage{Role: m.Role, Content: text})
	}
	if raw := fields["stream"]; len(raw) > 0 {
		var v bool
		if json.Unmarshal(raw, &v) != nil || v != stream {
			return chatRequest{}, &unsupportedError{"business web adapter: stream flag conflicts with gateway selection"}
		}
	}
	if raw := fields["promptql_thread_id"]; len(raw) > 0 {
		if json.Unmarshal(raw, &out.ThreadID) != nil {
			return chatRequest{}, &unsupportedError{"business web adapter: promptql_thread_id must be text"}
		}
	}
	if out.Messages[len(out.Messages)-1].Role != "user" {
		return chatRequest{}, &unsupportedError{"business web adapter: last message must be a user message"}
	}
	return out, nil
}

func textContent(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	if parts, ok := v.([]any); ok {
		var b strings.Builder
		for _, p := range parts {
			if s, ok := p.(string); ok {
				b.WriteString(s)
				continue
			}
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String(), b.Len() > 0
	}
	return "", false
}

func credential(source config.Source) (map[string]string, error) {
	if strings.TrimSpace(source.KeyEnv) == "" {
		return nil, ErrCredential
	}
	raw := strings.TrimSpace(source.CredentialValue())
	if raw == "" || len(raw) > maxResponseBytes || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, ErrCredential
	}
	out := map[string]string{"value": raw}
	if strings.HasPrefix(raw, "{") {
		var fields map[string]any
		dec := json.NewDecoder(strings.NewReader(raw))
		if dec.Decode(&fields) != nil || fields == nil {
			return nil, ErrCredential
		}
		var trailing any
		if dec.Decode(&trailing) != io.EOF {
			return nil, ErrCredential
		}
		out["value"] = ""
		for k, v := range fields {
			s, ok := v.(string)
			if !ok {
				switch k {
				case "value", "authorization", "token", "access_token", "accessToken", "cookie", "session", "jwt",
					"x_goog_api_key", "visit_id", "authuser", "proof", "tier", "user_agent", "x_user_agent",
					"chathub_path", "chathubPath", "userTenant", "variants", "source", "product", "agent_host",
					"license_type", "is_edu", "agent", "scenario", "projectId", "project_id", "timezone":
					return nil, ErrCredential
				default:
					continue
				}
			}
			if ok && strings.TrimSpace(s) != "" {
				s = strings.TrimSpace(s)
				if len(s) > maxResponseBytes || strings.ContainsAny(s, "\r\n\x00") {
					return nil, ErrCredential
				}
				out[k] = s
			}
		}
		if out["value"] == "" {
			for _, k := range []string{"authorization", "token", "access_token", "accessToken", "cookie", "session", "jwt"} {
				if out[k] != "" {
					out["value"] = out[k]
					break
				}
			}
		}
	} else if strings.Contains(raw, "=") {
		// M365 captures are often pasted as `access_token=...; chathubPath=...`.
		// Keep the complete value too, while exposing only bounded key/value
		// fields to the adapter that needs them.
		for _, item := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == '\n' }) {
			key, value, ok := strings.Cut(strings.TrimSpace(item), "=")
			if ok && strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
				out[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		if strings.Contains(raw, "__Secure-1PSID=") || strings.Contains(raw, "SAPISID=") {
			out["cookie"] = raw
		}
	}
	return out, nil
}

func stripBearer(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 7 && strings.EqualFold(value[:7], "bearer ") {
		return strings.TrimSpace(value[7:])
	}
	return value
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("business web adapter: invalid base_url")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", errors.New("business web adapter: base_url must use HTTPS outside loopback")
	}
	return base, nil
}
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func request(ctx context.Context, hc *http.Client, method, endpoint string, payload []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("business web adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(payload))
	req.Header = headers.Clone()
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("business web adapter: upstream transport failed")
	}
	return resp, nil
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("business web adapter: upstream response exceeds limit")
	}
	return b, nil
}

func jsonResponse(model, content, thread string) *http.Response {
	id := randomID("chatcmpl-")
	v := map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}}
	if thread != "" {
		v["promptql_thread_id"] = thread
	}
	b, _ := json.Marshal(v)
	h := http.Header{"Content-Type": {"application/json"}, "X-COT-Delivery": {"buffered"}}
	if thread != "" {
		h.Set("X-PromptQL-Thread-Id", thread)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b))}
}

func streamResponse(model, content, thread string) *http.Response {
	id := randomID("chatcmpl-")
	now := time.Now().Unix()
	chunk := func(delta map[string]any, finish any) []byte {
		v := map[string]any{"id": id, "object": "chat.completion.chunk", "created": now, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		b, _ := json.Marshal(v)
		return append([]byte("data: "), append(b, []byte("\n\n")...)...)
	}
	out := chunk(map[string]any{"role": "assistant"}, nil)
	out = append(out, chunk(map[string]any{"content": content}, nil)...)
	out = append(out, chunk(map[string]any{}, "stop")...)
	out = append(out, []byte("data: [DONE]\n\n")...)
	h := http.Header{"Content-Type": {"text/event-stream"}, "X-COT-Delivery": {"buffered"}}
	if thread != "" {
		h.Set("X-PromptQL-Thread-Id", thread)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(out)), ContentLength: int64(len(out))}
}

func randomID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + fmt.Sprintf("%x", b[:])
}

func parseSSEText(ctx context.Context, body io.Reader) (string, error) {
	r := bufio.NewReaderSize(body, 8192)
	var out strings.Builder
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		line, err := r.ReadString('\n')
		total += len(line)
		if total > maxResponseBytes {
			return "", ErrTruncated
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if d == "[DONE]" {
				return out.String(), nil
			}
			var v any
			if json.Unmarshal([]byte(d), &v) == nil {
				if s := extractText(v); s != "" {
					out.WriteString(s)
				}
			}
		}
		if err == io.EOF {
			return out.String(), nil
		}
		if err != nil {
			return "", err
		}
	}
}

func extractText(v any) string {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"text", "content", "output_text", "message"} {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		if ch, ok := m["choices"].([]any); ok && len(ch) > 0 {
			if s := extractText(ch[0]); s != "" {
				return s
			}
		}
		for _, k := range []string{"delta", "message", "response", "data", "result"} {
			if x, ok := m[k]; ok {
				if s := extractText(x); s != "" {
					return s
				}
			}
		}
	}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s := extractText(x); s != "" {
				return s
			}
		}
	}
	return ""
}
