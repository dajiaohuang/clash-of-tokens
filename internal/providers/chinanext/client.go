// Package chinanext contains standalone adapters for the Dola, Tencent Yuanbao,
// DeepSeek, and domestic Doubao web contracts. The package is intentionally
// outside the built-in provider registry: these are private, changeable web
// endpoints. Doubao uses the user's already authenticated Chrome page through
// CDP; the other adapters require explicit source credentials.
package chinanext

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerutil"
)

const (
	AdapterDola     = "dola-web"
	AdapterYuanbao  = "yuanbao"
	AdapterDeepSeek = "deepseek-web"
	AdapterDoubao   = "doubao"

	defaultDolaBase     = "https://www.dola.com"
	defaultYuanbaoBase  = "https://yuanbao.tencent.com"
	defaultDeepSeekBase = "https://chat.deepseek.com"

	maxEventBytes = 16 << 20
	maxSessions   = 4096
)

var (
	ErrUnsupported  error = unsupportedError{}
	ErrCredential         = errors.New("china next web adapter: source credential is missing or invalid")
	ErrSessionLimit       = errors.New("china next web adapter: session capacity exceeded")
	ErrTruncated          = errors.New("china next web adapter: truncated upstream response")
)

// HTTPError preserves a prerequisite HTTP status for the gateway.
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("china next web adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("china next web adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type unsupportedError struct{}

func (unsupportedError) Error() string {
	return "china next web adapter: unsupported request semantics"
}
func (unsupportedError) HTTPStatus() int { return 422 }

// Client implements the narrow internal/upstream.Client contract. A session
// is created only when the caller supplies X-COT-Session; requests without it
// receive a fresh provider conversation and cannot inherit another request's
// state.
type Client struct {
	source     config.Source
	http       *http.Client
	browser    config.Browser
	mu         sync.Mutex
	sessions   map[string]*session
	doubaoGate providerutil.Gate
	accessMu   sync.Mutex
	access     string
	accessFor  string
	expires    time.Time
	closed     bool
}

type session struct {
	requestMu      providerutil.Gate
	mu             sync.Mutex
	conversationID string
	deepSessionID  string
}

// sessionResponseBody releases the per-session generation lock when a stream
// reaches EOF or the caller closes it. This keeps same-session turns ordered
// through response completion while allowing unrelated sessions to proceed.
type sessionResponseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *sessionResponseBody) releaseLock() { b.once.Do(b.release) }
func (b *sessionResponseBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.releaseLock()
	}
	return n, e
}
func (b *sessionResponseBody) Close() error {
	e := b.ReadCloser.Close()
	b.releaseLock()
	return e
}

func New(source config.Source, browsers ...config.Browser) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
		MaxIdleConns:      max(4, min(source.MaxInflight, 256)), MaxIdleConnsPerHost: max(4, min(source.MaxInflight, 256)), MaxConnsPerHost: maxConn,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, ExpectContinueTimeout: time.Second,
		MaxResponseHeaderBytes: 64 << 10, DisableCompression: true,
	}
	b := config.Default().Browser
	if len(browsers) > 0 {
		b = browsers[0]
	}
	return &Client{
		source:   source,
		browser:  b,
		http:     &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		sessions: make(map[string]*session),
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if proto != "chat" {
		return nil, ErrUnsupported
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("china next web adapter: model is required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("china next web adapter: client is closed")
	}
	if c.source.Adapter == AdapterDoubao {
		return c.doDoubao(ctx, model, stream, body, clientHeaders)
	}
	request, e := decodeChatRequest(body)
	if e != nil {
		return nil, e
	}
	request.Model = model
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	if _, ok := fields["reasoning_effort"]; ok {
		return nil, fmt.Errorf("%w: use enable_thinking boolean where supported", ErrUnsupported)
	}
	if c.source.Adapter == AdapterDola || c.source.Adapter == "doubao-web" {
		if _, ok := fields["web_search"]; ok {
			return nil, fmt.Errorf("%w: Dola web_search is not implemented", ErrUnsupported)
		}
		if model != "dola-speed" && model != "dola-thinking" {
			return nil, fmt.Errorf("%w: Dola modes are dola-speed and dola-thinking", ErrUnsupported)
		}
	}
	if c.source.Adapter == AdapterYuanbao || c.source.Adapter == "yuanbao-web" {
		if _, ok := fields["enable_thinking"]; ok {
			return nil, fmt.Errorf("%w: select the Yuanbao model mode explicitly", ErrUnsupported)
		}
	}
	if clientHeaders.Get("X-COT-Session") != "" && (c.source.Adapter == AdapterDola || c.source.Adapter == "doubao-web" || c.source.Adapter == AdapterDeepSeek) {
		return nil, fmt.Errorf("%w: this adapter currently supports isolated turns only", ErrUnsupported)
	}
	key, e := c.credential()
	if e != nil {
		return nil, e
	}
	state, e := c.session(clientHeaders.Get("X-COT-Session"))
	if e != nil {
		return nil, e
	}
	if err := state.requestMu.Lock(ctx); err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			state.requestMu.Unlock()
		}
	}()

	var upstream *http.Response
	switch c.source.Adapter {
	case AdapterDola, "doubao-web":
		upstream, e = c.doDola(ctx, key, request, state)
	case AdapterYuanbao, "yuanbao-web":
		upstream, e = c.doYuanbao(ctx, key, request, state)
	case AdapterDeepSeek:
		upstream, e = c.doDeepSeek(ctx, key, request, state)
	default:
		return nil, fmt.Errorf("china next web adapter: unsupported adapter %q", c.source.Adapter)
	}
	if e != nil {
		return nil, e
	}
	if upstream == nil {
		return nil, errors.New("china next web adapter: empty upstream response")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	converted, e := c.convert(ctx, upstream, request.Model, c.source.Adapter, stream, state)
	if e != nil {
		return nil, e
	}
	if stream {
		locked = false
		converted.Body = &sessionResponseBody{ReadCloser: converted.Body, release: state.requestMu.Unlock}
	}
	return converted, nil
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	v := os.Getenv(c.source.KeyEnv)
	if strings.TrimSpace(v) == "" {
		return "", ErrCredential
	}
	return v, nil
}

func (c *Client) session(key string) (*session, error) {
	if key == "" {
		return &session{}, nil
	}
	if len(key) > 256 {
		sum := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("%x", sum[:])
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[key]; s != nil {
		return s, nil
	}
	if len(c.sessions) >= maxSessions {
		return nil, ErrSessionLimit
	}
	s := &session{}
	c.sessions[key] = s
	return s, nil
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Stream          bool            `json:"stream"`
	WebSearch       bool            `json:"web_search"`
	ReasoningEffort json.RawMessage `json:"reasoning_effort"`
	EnableThinking  *bool           `json:"enable_thinking"`
}
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func decodeChatRequest(body []byte) (chatRequest, error) {
	var r chatRequest
	if !json.Valid(body) {
		return r, errors.New("china next web adapter: invalid JSON request")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(body, &fields); e != nil || fields == nil {
		return r, errors.New("china next web adapter: request must be an object")
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "web_search": true, "reasoning_effort": true, "enable_thinking": true}
	for field := range fields {
		if !allowed[field] {
			return r, fmt.Errorf("%w: unsupported top-level field %q", ErrUnsupported, field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if e := decoder.Decode(&r); e != nil {
		return r, errors.New("china next web adapter: invalid chat request")
	}
	var trailing json.RawMessage
	if e := decoder.Decode(&trailing); e != io.EOF {
		return r, errors.New("china next web adapter: invalid chat request")
	}
	if len(r.Messages) == 0 {
		return r, errors.New("china next web adapter: messages are required")
	}
	var rawMessages []json.RawMessage
	if e := json.Unmarshal(fields["messages"], &rawMessages); e != nil {
		return r, errors.New("china next web adapter: messages must be an array")
	}
	for _, raw := range rawMessages {
		var message chatMessage
		messageDecoder := json.NewDecoder(bytes.NewReader(raw))
		messageDecoder.DisallowUnknownFields()
		if e := messageDecoder.Decode(&message); e != nil || message.Role == "" {
			return r, fmt.Errorf("%w: message has unsupported fields", ErrUnsupported)
		}
		if e := messageDecoder.Decode(&trailing); e != io.EOF {
			return r, fmt.Errorf("%w: message must be an object", ErrUnsupported)
		}
		if message.Role != "user" {
			return r, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, message.Role)
		}
		if e := validateMessageContent(message.Content); e != nil {
			return r, e
		}
	}
	if len(r.Messages) != 1 {
		return r, fmt.Errorf("%w: only one user message is supported; history must use X-COT-Session", ErrUnsupported)
	}
	return r, nil
}

func validateMessageContent(raw json.RawMessage) error {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return fmt.Errorf("%w: message content is required", ErrUnsupported)
	}
	var text string
	if json.Unmarshal(t, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("%w: message content is required", ErrUnsupported)
		}
		return nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(t, &parts) != nil || len(parts) == 0 {
		return fmt.Errorf("%w: only non-empty text content is supported", ErrUnsupported)
	}
	for _, rawPart := range parts {
		var part map[string]json.RawMessage
		if json.Unmarshal(rawPart, &part) != nil || part == nil || len(part) != 2 {
			return fmt.Errorf("%w: content part has unsupported fields", ErrUnsupported)
		}
		var kind, value string
		if e := json.Unmarshal(part["type"], &kind); e != nil || kind != "text" {
			return fmt.Errorf("%w: multimodal message content is unsupported", ErrUnsupported)
		}
		if e := json.Unmarshal(part["text"], &value); e != nil {
			return fmt.Errorf("%w: text content part must contain text", ErrUnsupported)
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: message content is required", ErrUnsupported)
		}
		for field := range part {
			if field != "type" && field != "text" {
				return fmt.Errorf("%w: content part field %q is unsupported", ErrUnsupported, field)
			}
		}
	}
	return nil
}

func contentText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []struct{ Type, Text string }
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, e := url.Parse(base)
	if e != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("china next web adapter: invalid base_url")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("china next web adapter: invalid base_url")
	}
	return base, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func post(ctx context.Context, hc *http.Client, endpoint, contentType string, payload []byte, headers http.Header) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if e != nil {
		return nil, errors.New("china next web adapter: invalid upstream request")
	}
	req.ContentLength = int64(len(payload))
	req.GetBody = nil
	req.Header = cloneHeaders(headers)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, e := hc.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("china next web adapter: upstream transport failed")
	}
	return resp, nil
}

func cloneHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, values := range h {
		for _, v := range values {
			out.Add(k, v)
		}
	}
	return out
}

func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		n := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(b[:8], uint64(n))
		binary.LittleEndian.PutUint64(b[8:], uint64(n>>7))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(b[:4]), binary.BigEndian.Uint16(b[4:6]), binary.BigEndian.Uint16(b[6:8]), binary.BigEndian.Uint16(b[8:10]), b[10:])
}

func bearer(h http.Header, token string) { h.Set("Authorization", "Bearer "+token) }
