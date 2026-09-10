// Package codingfinal contains opt-in, direct wire adapters for the coding
// sources whose protocol is present in the pinned strict93 inventory.
//
// The package is intentionally independent from the source registry.  A
// caller supplies an explicit config.Source and HTTP adapters read only its
// KeyEnv. The local ZCode adapter uses the selected product's app-server
// profile and does not read a gateway credential. No caller credential headers
// are forwarded.
package codingfinal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerutil"
)

const (
	AdapterCursor   = "cursor"
	AdapterWindsurf = "windsurf"
	AdapterQoder    = "qoder"
	AdapterTrae     = "trae"
	AdapterV0       = "v0-web"
	AdapterV0Vercel = "v0-vercel"
	AdapterWarp     = "warp"
	AdapterZCode    = "zcode"
	// Devin Desktop is the product identity used by the pinned Windsurf
	// transport. Both names select the same direct Connect/protobuf wire.
	AdapterDevinDesktop       = "devin-desktop"
	maxRequestBytes     int64 = 16 << 20
	maxEventBytes       int64 = 64 << 20
	maxOutputBytes      int64 = 64 << 20
	maxHeaderValue            = 4096
	maxSessions               = 4096
)

type unsupportedError string

func (e unsupportedError) Error() string   { return string(e) }
func (e unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

var (
	ErrUnsupported = unsupportedError("coding final adapter: unsupported protocol or request semantics")
	ErrCredential  = errors.New("coding final adapter: source credential is missing or invalid")
	ErrTruncated   = errors.New("coding final adapter: truncated upstream response")
	ErrClosed      = errors.New("coding final adapter: client is closed")
)

type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("coding final adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("coding final adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type Client struct {
	source   config.Source
	http     *http.Client
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}
type session struct {
	gate         providerutil.Gate
	conversation string
}

type responseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

// cursorRequestResponse keeps Cursor's bidirectional Connect request open
// after the initial Run frame. Closing the response closes the request side
// too, which lets cancellation and ordinary body lifecycle tear down both
// directions without a subprocess or a leaked pipe.
type cursorRequestResponse struct {
	io.ReadCloser
	writer *io.PipeWriter
	once   sync.Once
}

func (b *cursorRequestResponse) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { _ = b.writer.Close() })
	return err
}

func (b *responseBody) unlock() { b.once.Do(b.release) }
func (b *responseBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.unlock()
	}
	return n, e
}
func (b *responseBody) Close() error { e := b.ReadCloser.Close(); b.unlock(); return e }

// generatedBody performs conversion on first read.  Conversion is buffered so
// a malformed/truncated wire response can never be mistaken for a successful
// partial completion.  The upstream body is still closed on every path.
type generatedBody struct {
	upstream io.ReadCloser
	ctx      context.Context
	generate func(context.Context, io.Reader) ([]byte, error)
	once     sync.Once
	mu       sync.Mutex
	data     []byte
	err      error
	closed   bool
}

func (b *generatedBody) init() {
	b.once.Do(func() {
		defer b.upstream.Close()
		if b.ctx != nil {
			if err := b.ctx.Err(); err != nil {
				b.err = err
				return
			}
		}
		b.data, b.err = b.generate(b.ctx, b.upstream)
	})
}
func (b *generatedBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, io.EOF
	}
	b.mu.Unlock()
	b.init()
	if b.err != nil {
		err := b.err
		b.err = nil
		return 0, err
	}
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}
func (b *generatedBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return b.upstream.Close()
}

// New does not read credentials or perform network I/O.
func New(source config.Source) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	tr := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: max(4, min(source.MaxInflight, 256)), MaxIdleConnsPerHost: max(4, min(source.MaxInflight, 256)),
		MaxConnsPerHost: maxConn, IdleConnTimeout: 60 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: time.Second, MaxResponseHeaderBytes: 64 << 10,
		DisableCompression: true,
	}
	return &Client{source: source, http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, sessions: make(map[string]*session)}
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

func Supports(adapter string) bool {
	switch canonicalAdapter(adapter) {
	case AdapterCursor, AdapterWindsurf, AdapterDevinDesktop, AdapterQoder, AdapterTrae, AdapterV0, AdapterWarp, AdapterZCode:
		return true
	default:
		return false
	}
}

func canonicalAdapter(adapter string) string {
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	if adapter == "v0" || adapter == AdapterV0Vercel {
		return AdapterV0
	}
	return adapter
}

// Do accepts the gateway's OpenAI Chat Completions request and translates it
// to the selected private wire protocol. Only text chat is exposed; the
// individual adapters reject fields they cannot represent without loss.
func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, errors.New("coding final adapter: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	adapter := canonicalAdapter(c.source.Adapter)
	if !Supports(adapter) {
		return nil, fmt.Errorf("%w: adapter %q", ErrUnsupported, c.source.Adapter)
	}
	if protocol != "chat" {
		return nil, fmt.Errorf("%w: chat protocol is required", ErrUnsupported)
	}
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 256 {
		return nil, fmt.Errorf("%w: model is required", ErrUnsupported)
	}
	if int64(len(body)) > maxRequestBytes {
		return nil, fmt.Errorf("%w: request body exceeds byte limit", ErrUnsupported)
	}
	token := ""
	var err error
	if adapter != AdapterZCode {
		token, err = c.credential()
		if err != nil {
			return nil, err
		}
	}
	req, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	key := headerValue(headers, "X-COT-Session")
	if key != "" && (adapter == AdapterCursor || adapter == AdapterWindsurf || adapter == AdapterDevinDesktop || adapter == AdapterTrae || adapter == AdapterV0 || adapter == AdapterWarp || adapter == AdapterZCode || adapter == AdapterQoder) {
		return nil, fmt.Errorf("%w: %s does not support gateway session continuation", ErrUnsupported, adapter)
	}
	s, err := c.session(key)
	if err != nil {
		return nil, err
	}
	if err = s.gate.Lock(ctx); err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			s.gate.Unlock()
		}
	}()
	var upstream *http.Response
	switch adapter {
	case AdapterCursor:
		upstream, err = c.doCursor(ctx, token, model, req, s)
	case AdapterWindsurf, AdapterDevinDesktop:
		upstream, err = c.doWindsurf(ctx, token, model, req, s)
	case AdapterQoder:
		upstream, err = c.doQoder(ctx, token, model, stream, body, s)
	case AdapterTrae:
		upstream, err = c.doTrae(ctx, token, model, req, s)
	case AdapterV0:
		upstream, err = c.doV0(ctx, token, model, stream, req)
	case AdapterWarp:
		upstream, err = c.doWarp(ctx, token, model, req, s)
	case AdapterZCode:
		upstream, err = c.doZCode(ctx, model, req, stream)
	}
	if err != nil {
		return nil, err
	}
	if upstream == nil {
		return nil, errors.New("coding final adapter: empty upstream response")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	generate := func(context.Context, io.Reader) ([]byte, error) { return nil, nil }
	switch adapter {
	case AdapterCursor:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return cursorToSSE(ctx, r, model) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) {
				sse, err := cursorToSSE(ctx, r, model)
				if err != nil {
					return nil, err
				}
				return collectOpenAISSE(ctx, bytes.NewReader(sse), model)
			}
		}
	case AdapterWindsurf, AdapterDevinDesktop:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return windsurfToSSE(ctx, r, model) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) {
				sse, err := windsurfToSSE(ctx, r, model)
				if err != nil {
					return nil, err
				}
				return collectOpenAISSE(ctx, bytes.NewReader(sse), model)
			}
		}
	case AdapterQoder:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return validateQoderSSE(ctx, r) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return qoderToJSON(ctx, r, model) }
		}
	case AdapterTrae:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return traeToSSE(ctx, r, model) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) {
				sse, err := traeToSSE(ctx, r, model)
				if err != nil {
					return nil, err
				}
				return collectOpenAISSE(ctx, bytes.NewReader(sse), model)
			}
		}
	case AdapterV0:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return validateV0SSE(ctx, r) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return v0ToJSON(ctx, r, model) }
		}
	case AdapterWarp:
		if stream {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return warpToSSE(ctx, r, model) }
		} else {
			generate = func(ctx context.Context, r io.Reader) ([]byte, error) {
				sse, err := warpToSSE(ctx, r, model)
				if err != nil {
					return nil, err
				}
				return collectOpenAISSE(ctx, bytes.NewReader(sse), model)
			}
		}
	case AdapterZCode:
		generate = func(ctx context.Context, r io.Reader) ([]byte, error) { return zcodeReadOutput(ctx, r) }
	}
	if stream {
		upstream.Body = &responseBody{ReadCloser: &generatedBody{upstream: upstream.Body, ctx: ctx, generate: generate}, release: s.gate.Unlock}
		locked = false
		upstream.ContentLength = -1
		upstream.Header.Del("Content-Length")
		if key != "" {
			upstream.Header.Set("X-COT-Session", key)
		}
		upstream.Header.Set("Content-Type", "text/event-stream")
		upstream.Header.Set("Cache-Control", "no-cache")
		upstream.Header.Set("X-COT-Delivery", "buffered")
		return upstream, nil
	}
	data, err := generate(ctx, upstream.Body)
	_ = upstream.Body.Close()
	if err != nil {
		return nil, err
	}
	upstream.Body = io.NopCloser(bytes.NewReader(data))
	upstream.ContentLength = int64(len(data))
	upstream.Header.Del("Content-Length")
	upstream.Header.Set("Content-Type", "application/json")
	if key != "" {
		upstream.Header.Set("X-COT-Session", key)
	}
	return upstream, nil
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	token := strings.TrimSpace(c.source.CredentialValue())
	if token == "" || len(token) > maxHeaderValue || strings.ContainsAny(token, "\r\n") {
		return "", ErrCredential
	}
	return token, nil
}
func (c *Client) session(key string) (*session, error) {
	if key == "" {
		return &session{conversation: randomUUID()}, nil
	}
	if len(key) > 256 {
		sum := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("%x", sum[:])
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if s := c.sessions[key]; s != nil {
		return s, nil
	}
	if len(c.sessions) >= maxSessions {
		return nil, fmt.Errorf("%w: session capacity exceeded", ErrUnsupported)
	}
	s := &session{conversation: randomUUID()}
	c.sessions[key] = s
	return s, nil
}
func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func randomHex(n int) string { b := make([]byte, n); _, _ = rand.Read(b); return fmt.Sprintf("%x", b) }
func headerValue(h http.Header, k string) string {
	if h == nil {
		return ""
	}
	if value := strings.TrimSpace(h.Get(k)); value != "" {
		return value
	}
	for name, values := range h {
		if strings.EqualFold(name, k) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

type chatMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []toolCall
}
type toolCall struct{ ID, Name, Arguments string }
type chatRequest struct {
	Messages   []chatMessage
	Tools      []toolDefinition
	ToolChoice any
	Parallel   *bool
	Raw        map[string]json.RawMessage
}
type toolDefinition struct {
	Name, Description string
	Parameters        any
	Strict            bool
}

func decodeChatRequest(body []byte) (chatRequest, error) {
	var root map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil || root == nil {
		return chatRequest{}, fmt.Errorf("%w: request must be one JSON object", ErrUnsupported)
	}
	if dec.Decode(new(any)) != io.EOF {
		return chatRequest{}, fmt.Errorf("%w: request must contain one JSON object", ErrUnsupported)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(root["messages"], &raw); err != nil || len(raw) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages must be a non-empty array", ErrUnsupported)
	}
	r := chatRequest{Raw: root}
	for i, b := range raw {
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) != nil || m == nil {
			return chatRequest{}, fmt.Errorf("%w: message %d must be an object", ErrUnsupported, i)
		}
		var role string
		if json.Unmarshal(m["role"], &role) != nil || role == "" {
			return chatRequest{}, fmt.Errorf("%w: message %d role is required", ErrUnsupported, i)
		}
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return chatRequest{}, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, role)
		}
		content, err := textContent(m["content"])
		if err != nil {
			return chatRequest{}, err
		}
		if (role == "user" || role == "system" || role == "developer" || role == "tool") && len(m["content"]) == 0 {
			return chatRequest{}, fmt.Errorf("%w: message %d content is required", ErrUnsupported, i)
		}
		msg := chatMessage{Role: role, Content: content}
		_ = json.Unmarshal(m["tool_call_id"], &msg.ToolCallID)
		if v, ok := m["tool_calls"]; ok {
			calls, err := decodeToolCalls(v)
			if err != nil {
				return chatRequest{}, fmt.Errorf("%w: message %d tool_calls malformed", ErrUnsupported, i)
			}
			msg.ToolCalls = calls
		}
		r.Messages = append(r.Messages, msg)
	}
	if v, ok := root["tools"]; ok {
		tools, err := decodeTools(v)
		if err != nil {
			return chatRequest{}, err
		}
		r.Tools = tools
	}
	if v, ok := root["tool_choice"]; ok {
		_ = json.Unmarshal(v, &r.ToolChoice)
	}
	if v, ok := root["parallel_tool_calls"]; ok {
		var x bool
		if json.Unmarshal(v, &x) != nil {
			return chatRequest{}, fmt.Errorf("%w: parallel_tool_calls must be boolean", ErrUnsupported)
		}
		r.Parallel = &x
	}
	return r, nil
}

func decodeToolCalls(raw json.RawMessage) ([]toolCall, error) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("tool calls are not an array")
	}
	out := make([]toolCall, 0, len(values))
	for _, value := range values {
		var outer map[string]json.RawMessage
		if json.Unmarshal(value, &outer) != nil || outer == nil {
			return nil, errors.New("tool call is not an object")
		}
		var typ string
		_ = json.Unmarshal(outer["type"], &typ)
		if typ != "function" {
			return nil, errors.New("tool call type is unsupported")
		}
		var id string
		if json.Unmarshal(outer["id"], &id) != nil || id == "" {
			return nil, errors.New("tool call id is required")
		}
		var fn map[string]json.RawMessage
		if json.Unmarshal(outer["function"], &fn) != nil || fn == nil {
			return nil, errors.New("tool function is malformed")
		}
		var name, args string
		if json.Unmarshal(fn["name"], &name) != nil || name == "" || json.Unmarshal(fn["arguments"], &args) != nil {
			return nil, errors.New("tool function fields are invalid")
		}
		out = append(out, toolCall{ID: id, Name: name, Arguments: args})
	}
	return out, nil
}

func decodeTools(raw json.RawMessage) ([]toolDefinition, error) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("%w: tools malformed", ErrUnsupported)
	}
	tools := make([]toolDefinition, 0, len(values))
	for i, value := range values {
		var outer map[string]json.RawMessage
		if json.Unmarshal(value, &outer) != nil || outer == nil {
			return nil, fmt.Errorf("%w: tool %d malformed", ErrUnsupported, i)
		}
		var typ string
		if json.Unmarshal(outer["type"], &typ) != nil || typ != "function" {
			return nil, fmt.Errorf("%w: tool %d must be a function", ErrUnsupported, i)
		}
		var fn map[string]json.RawMessage
		if json.Unmarshal(outer["function"], &fn) != nil || fn == nil {
			return nil, fmt.Errorf("%w: tool %d function is malformed", ErrUnsupported, i)
		}
		var name, description string
		if json.Unmarshal(fn["name"], &name) != nil || strings.TrimSpace(name) == "" || len(name) > 256 {
			return nil, fmt.Errorf("%w: tool %d name is invalid", ErrUnsupported, i)
		}
		_ = json.Unmarshal(fn["description"], &description)
		var params any
		if len(fn["parameters"]) != 0 {
			if json.Unmarshal(fn["parameters"], &params) != nil {
				return nil, fmt.Errorf("%w: tool %d parameters are malformed", ErrUnsupported, i)
			}
		} else {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		strict := false
		if len(fn["strict"]) != 0 && json.Unmarshal(fn["strict"], &strict) != nil {
			return nil, fmt.Errorf("%w: tool %d strict must be boolean", ErrUnsupported, i)
		}
		tools = append(tools, toolDefinition{Name: name, Description: description, Parameters: params, Strict: strict})
	}
	return tools, nil
}
func textContent(raw json.RawMessage) (string, error) {
	var s string
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return "", fmt.Errorf("%w: message content must be text", ErrUnsupported)
	}
	var out strings.Builder
	for _, b := range blocks {
		for key := range b {
			if key != "type" && key != "text" {
				return "", fmt.Errorf("%w: content block field %q is unsupported", ErrUnsupported, key)
			}
		}
		var typ string
		if json.Unmarshal(b["type"], &typ) != nil || typ != "text" {
			return "", fmt.Errorf("%w: only text content is supported", ErrUnsupported)
		}
		var x string
		if json.Unmarshal(b["text"], &x) != nil {
			return "", fmt.Errorf("%w: text content is malformed", ErrUnsupported)
		}
		out.WriteString(x)
	}
	return out.String(), nil
}

func cloneMap(in map[string]json.RawMessage) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		var x any
		if json.Unmarshal(v, &x) == nil {
			out[k] = x
		}
	}
	return out
}
func endpointBase(source config.Source) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		return "", errors.New("coding final adapter: base_url is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("coding final adapter: invalid base_url")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && (host == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return "", errors.New("coding final adapter: HTTPS required outside loopback")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}
func post(ctx context.Context, hc *http.Client, url string, body []byte, h http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("coding final adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(body))
	req.Header = h
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding final adapter: upstream transport failed")
	}
	return resp, nil
}

func parseStreamError(value any) error {
	if m, ok := value.(map[string]any); ok {
		if raw := m["error"]; raw != nil {
			return fmt.Errorf("coding final adapter: upstream error: %s", streamErrorText(raw))
		}
		if status, ok := m["statusCodeValue"].(float64); ok && status != 200 {
			return &HTTPError{Status: int(status), What: streamErrorText(m["body"])}
		}
	}
	return nil
}
func streamErrorText(v any) string {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"message", "detail", "title", "code"} {
			if s, ok := m[k].(string); ok && s != "" {
				return streamErrorText(s)
			}
		}
	}
	if s, ok := v.(string); ok && s != "" {
		s = strings.TrimSpace(s)
		lower := strings.ToLower(s)
		if len(s) > 256 || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") || strings.ContainsAny(s, "\r\n") || strings.Contains(lower, "bearer ") || strings.Contains(lower, "access_token") || strings.Contains(lower, "refresh_token") {
			return "request failed"
		}
		return s
	}
	// Error payloads can contain request headers, bearer tokens, or internal
	// paths. Never expose an unknown object by serializing it into the client
	// response.
	return "request failed"
}

// collectOpenAISSE aggregates a validated text/tool SSE stream for non-stream
// callers. It intentionally requires [DONE] or a finish_reason and preserves
// upstream error objects instead of returning a fabricated completion.
func collectOpenAISSE(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1<<20)
	var total int64
	var text strings.Builder
	var reasoning strings.Builder
	var id string
	var created int64
	var finish any
	var calls []map[string]any
	done := false
	seenEvent := false
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(sc.Text())
		total += int64(len(sc.Bytes()) + 1)
		if total > maxEventBytes {
			return nil, fmt.Errorf("%w: response exceeds byte limit", ErrUnsupported)
		}
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			done = true
			break
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(data), &chunk) != nil || chunk == nil {
			return nil, fmt.Errorf("%w: malformed SSE event", ErrTruncated)
		}
		if err := parseStreamError(chunk); err != nil {
			return nil, err
		}
		if s, _ := chunk["id"].(string); s != "" {
			id = s
		}
		if x, ok := chunk["created"].(float64); ok {
			created = int64(x)
		}
		rawChoices, present := chunk["choices"]
		if !present {
			continue
		}
		choices, ok := rawChoices.([]any)
		if !ok || rawChoices == nil {
			return nil, fmt.Errorf("%w: malformed choices", ErrTruncated)
		}
		if len(choices) == 0 {
			continue
		}
		seenEvent = true
		for _, cv := range choices {
			choice, ok := cv.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: malformed choice", ErrTruncated)
			}
			if x := choice["finish_reason"]; x != nil {
				finish = x
			}
			d, hasDelta := choice["delta"].(map[string]any)
			if rawDelta, present := choice["delta"]; present && rawDelta != nil && !hasDelta {
				return nil, fmt.Errorf("%w: malformed delta", ErrTruncated)
			}
			if s, _ := d["content"].(string); s != "" {
				text.WriteString(s)
			}
			if s, _ := d["reasoning_content"].(string); s != "" {
				reasoning.WriteString(s)
			}
			if raw, present := d["tool_calls"]; present && raw != nil {
				values, ok := raw.([]any)
				if !ok {
					return nil, fmt.Errorf("%w: malformed tool calls", ErrTruncated)
				}
				for _, value := range values {
					var err error
					calls, err = appendOpenAIToolDelta(calls, value)
					if err != nil {
						return nil, err
					}
				}
			}
			if raw, present := d["function_call"]; present && raw != nil {
				var err error
				calls, err = appendOpenAIToolDelta(calls, map[string]any{"index": 0, "function": raw})
				if err != nil {
					return nil, err
				}
			}
		}
		if streamOutputBytes(text.Len(), reasoning.Len(), calls) > maxOutputBytes {
			return nil, fmt.Errorf("%w: output exceeds byte limit", ErrUnsupported)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !seenEvent {
		return nil, ErrTruncated
	}
	if !done && finish == nil {
		return nil, ErrTruncated
	}
	if id == "" {
		id = "chatcmpl-" + randomUUID()
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	msg := map[string]any{"role": "assistant", "content": text.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	fr := finish
	if fr == nil {
		fr = "stop"
	}
	data, err := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": fr}}})
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxOutputBytes {
		return nil, fmt.Errorf("%w: output exceeds byte limit", ErrUnsupported)
	}
	return data, nil
}

const maxToolCallCount = 4096

func appendOpenAIToolDelta(existing []map[string]any, raw any) ([]map[string]any, error) {
	delta, ok := raw.(map[string]any)
	if !ok || delta == nil {
		return nil, fmt.Errorf("%w: malformed tool call", ErrTruncated)
	}
	index, err := openAIToolIndex(delta["index"])
	if err != nil {
		return nil, err
	}
	if rawID, present := delta["id"]; present && rawID != nil {
		if _, ok := rawID.(string); !ok {
			return nil, fmt.Errorf("%w: tool call id must be text", ErrTruncated)
		}
	}
	if rawType, present := delta["type"]; present && rawType != nil {
		if _, ok := rawType.(string); !ok {
			return nil, fmt.Errorf("%w: tool call type must be text", ErrTruncated)
		}
	}
	incoming, hasFunction := delta["function"].(map[string]any)
	if rawFunction, present := delta["function"]; present && rawFunction != nil && !hasFunction {
		return nil, fmt.Errorf("%w: malformed tool function", ErrTruncated)
	}
	if hasFunction {
		if value, present := incoming["name"]; present && value != nil {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("%w: tool call name must be text", ErrTruncated)
			}
		}
		if value, present := incoming["arguments"]; present && value != nil {
			if _, ok := value.(string); !ok {
				return nil, fmt.Errorf("%w: tool call arguments must be text", ErrTruncated)
			}
		}
	}
	for len(existing) <= index {
		existing = append(existing, map[string]any{
			"index": len(existing), "id": "", "type": "function",
			"function": map[string]any{"name": "", "arguments": ""},
		})
	}
	out := existing[index]
	if out == nil {
		return nil, fmt.Errorf("%w: malformed accumulated tool call", ErrTruncated)
	}
	if value, ok := delta["id"].(string); ok && value != "" {
		out["id"] = value
	}
	if value, ok := delta["type"].(string); ok && value != "" {
		out["type"] = value
	}
	fn, ok := out["function"].(map[string]any)
	if !ok || fn == nil {
		return nil, fmt.Errorf("%w: malformed accumulated tool function", ErrTruncated)
	}
	arguments, present := fn["arguments"]
	if !present || arguments == nil {
		arguments = ""
		fn["arguments"] = arguments
	}
	current, ok := arguments.(string)
	if !ok {
		return nil, fmt.Errorf("%w: accumulated tool arguments must be text", ErrTruncated)
	}
	if !hasFunction {
		return existing, nil
	}
	if value, ok := incoming["name"].(string); ok && value != "" {
		fn["name"] = value
	}
	if value, ok := incoming["arguments"].(string); ok {
		current += value
	}
	if value, present := incoming["input"]; present {
		fragment, err := openAIToolFragment(value)
		if err != nil {
			return nil, err
		}
		current += fragment
	}
	fn["arguments"] = current
	return existing, nil
}

func openAIToolIndex(value any) (int, error) {
	if value == nil {
		return 0, nil
	}
	var index int64
	switch n := value.(type) {
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.Trunc(n) != n || n > float64(maxToolCallCount-1) {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
		index = int64(n)
	case int:
		index = int64(n)
	case int64:
		index = n
	case json.Number:
		var err error
		index, err = n.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
		}
	default:
		return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
	}
	if index < 0 || index >= maxToolCallCount {
		return 0, fmt.Errorf("%w: invalid tool call index", ErrUnsupported)
	}
	return int(index), nil
}

func openAIToolFragment(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)

	if err != nil {
		return "", fmt.Errorf("%w: malformed tool input", ErrTruncated)
	}
	return string(data), nil
}

func streamOutputBytes(contentLen, reasoningLen int, calls []map[string]any) int64 {
	total := int64(contentLen + reasoningLen)
	for _, call := range calls {
		if function, ok := call["function"].(map[string]any); ok {
			if value, ok := function["name"].(string); ok {
				total += int64(len(value))
			}
			if value, ok := function["arguments"].(string); ok {
				total += int64(len(value))
			}
			if value, ok := function["input"].(string); ok {
				total += int64(len(value))
			}
		}
	}
	return total
}

func validateSSE(ctx context.Context, r io.Reader) ([]byte, error) {
	data, err := readOpenAISSE(ctx, r)
	if err != nil {
		return nil, err
	}
	if _, err := collectOpenAISSE(ctx, bytes.NewReader(data), "upstream"); err != nil {
		return nil, err
	}
	return data, nil
}

// readOpenAISSE preserves an upstream SSE stream while stopping as soon as
// its terminal marker arrives. Some compatible APIs leave the HTTP response
// open after [DONE]; waiting for EOF would otherwise hang the gateway and
// hold the per-session gate indefinitely.
func readOpenAISSE(ctx context.Context, r io.Reader) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1<<20)
	var out bytes.Buffer
	var total int64
	done := false
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := sc.Text()
		total += int64(len(line) + 1)
		if total > maxEventBytes {
			return nil, fmt.Errorf("%w: response exceeds byte limit", ErrUnsupported)
		}
		out.WriteString(line)
		out.WriteString("\n")
		if strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "data:")), "[done]") {
			done = true
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !done && out.Len() == 0 {
		return nil, ErrTruncated
	}
	return out.Bytes(), nil
}
