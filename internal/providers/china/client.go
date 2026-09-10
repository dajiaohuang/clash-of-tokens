// Package china contains standalone adapters for the web chat protocols used
// by Kimi, Qwen AI (international), GLM and Z.ai.
//
// These adapters deliberately live outside internal/upstream.  Web endpoints
// are private, changeable protocols and must not silently become part of the
// gateway's built-in provider registry.  Credentials are read only from the
// Source.KeyEnv named by the caller.
package china

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
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providerutil"
)

const (
	adapterKimi = "kimi-web"
	adapterQwen = "qwen-web-intl"
	adapterGLM  = "glm-web"
	adapterZAI  = "zai-web"

	defaultKimiBase = "https://www.kimi.com"
	defaultQwenBase = "https://chat.qwen.ai"
	defaultGLMBase  = "https://chatglm.cn/chatglm"
	defaultZAIBase  = "https://chat.z.ai"

	maxEventBytes = 16 << 20
	maxSessions   = 4096
)

var (
	ErrUnsupported  error = unsupportedError{}
	ErrCredential         = errors.New("china web adapter: source credential environment variable is not set")
	ErrSessionLimit       = errors.New("china web adapter: session capacity exceeded")
	ErrTruncated          = errors.New("china web adapter: truncated upstream response")
)

// HTTPError preserves an HTTP status for prerequisite requests (token refresh
// and web chat creation).  The gateway can map this to the upstream status.
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("china web adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("china web adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type unsupportedError struct{}

func (unsupportedError) Error() string {
	return "china web adapter: unsupported protocol or request semantics"
}
func (unsupportedError) HTTPStatus() int { return 422 }

// Client implements the same narrow contract as internal/upstream.Client.
// It is safe for concurrent calls.  Session state is scoped to this Client;
// two sources, or two clients using the same web account, never share chats.
type Client struct {
	source     config.Source
	http       *http.Client
	mu         sync.Mutex
	sessions   map[string]*session
	glmMu      sync.Mutex
	glmAccess  string
	glmRefresh string
	glmExpires time.Time
	closed     bool
}

type session struct {
	requestMu       providerutil.Gate
	mu              sync.Mutex
	kimiChatID      string
	kimiParentID    string
	qwenChatID      string
	qwenParentID    string
	glmConversation string
	zaiChatID       string
	zaiParentID     string
}

type sessionResponseBody struct {
	io.ReadCloser
	releaseOnce sync.Once
	release     func()
}

func (b *sessionResponseBody) unlock() { b.releaseOnce.Do(b.release) }

func (b *sessionResponseBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.unlock()
	}
	return n, e
}

func (b *sessionResponseBody) Close() error {
	e := b.ReadCloser.Close()
	b.unlock()
	return e
}

func New(source config.Source) *Client {
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           max(4, min(source.MaxInflight, 256)),
		MaxIdleConnsPerHost:    max(4, min(source.MaxInflight, 256)),
		MaxConnsPerHost:        max(1, source.MaxInflight),
		IdleConnTimeout:        60 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  60 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     true,
	}
	return &Client{
		source:   source,
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

// Do converts the provider's web response into OpenAI-compatible Chat
// Completions output.  Only protocol "chat" is supported; Responses,
// Messages and Gemini are rejected explicitly because their web payloads are
// different contracts.
func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if proto != "chat" {
		return nil, ErrUnsupported
	}
	if model == "" {
		return nil, errors.New("china web adapter: model is required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("china web adapter: client is closed")
	}
	key, e := c.credential()
	if e != nil {
		return nil, e
	}
	request, e := decodeChatRequest(body)
	if e != nil {
		return nil, e
	}
	request.Model = model
	state, e := c.session(clientHeaders.Get("X-COT-Session"))
	if e != nil {
		return nil, e
	}
	// A provider conversation has one mutable parent/continuation pointer.
	// Serialize requests for the same explicit session through response
	// completion so concurrent turns cannot reuse a stale parent id.
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
	case adapterKimi:
		upstream, e = c.doKimi(ctx, key, request, state)
	case adapterQwen:
		upstream, e = c.doQwen(ctx, key, request, state)
	case adapterGLM:
		upstream, e = c.doGLM(ctx, key, request, state)
	case adapterZAI:
		upstream, e = c.doZAI(ctx, key, request, state)
	default:
		return nil, fmt.Errorf("china web adapter: unsupported adapter %q", c.source.Adapter)
	}
	if e != nil {
		return nil, e
	}
	if upstream == nil {
		return nil, errors.New("china web adapter: empty upstream response")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		// Preserve the provider status and body.  This includes Z.ai's current
		// FRONTEND_CAPTCHA_REQUIRED response; no captcha is generated or replayed.
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
	v := c.source.CredentialValue()
	if v == "" {
		return "", ErrCredential
	}
	return v, nil
}

func (c *Client) session(key string) (*session, error) {
	if key == "" {
		return &session{}, nil
	}
	if len(key) > 256 {
		// Hash untrusted header values instead of truncating them.  Truncation
		// would let two distinct long headers share a provider conversation.
		sum := sha256.Sum256([]byte(key))
		key = fmt.Sprintf("%x", sum[:])
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[key]; s != nil {
		return s, nil
	}
	if len(c.sessions) >= maxSessions {
		// Evicting an arbitrary session would cause surprising cross-turn loss.
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
		return r, errors.New("china web adapter: invalid JSON request")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(body, &fields); e != nil {
		return r, errors.New("china web adapter: request must be an object")
	}
	if fields == nil {
		return r, errors.New("china web adapter: request must be an object")
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "web_search": true, "reasoning_effort": true, "enable_thinking": true}
	for field := range fields {
		if !allowed[field] {
			return r, fmt.Errorf("%w: unsupported top-level field %q", ErrUnsupported, field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if e := decoder.Decode(&r); e != nil {
		return r, errors.New("china web adapter: invalid chat request")
	}
	var trailing json.RawMessage
	if e := decoder.Decode(&trailing); e != io.EOF {
		return r, errors.New("china web adapter: invalid chat request")
	}
	if len(r.Messages) == 0 {
		return r, errors.New("china web adapter: messages are required")
	}
	// Decode every message with DisallowUnknownFields.  This rejects tool,
	// function and provider-specific metadata instead of dropping it.
	var rawMessages []json.RawMessage
	if e := json.Unmarshal(fields["messages"], &rawMessages); e != nil {
		return r, errors.New("china web adapter: messages must be an array")
	}
	for _, raw := range rawMessages {
		var message chatMessage
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if e := decoder.Decode(&message); e != nil || message.Role == "" {
			return r, fmt.Errorf("%w: message has unsupported fields", ErrUnsupported)
		}
		if e := decoder.Decode(&trailing); e != io.EOF {
			return r, fmt.Errorf("%w: message must be an object", ErrUnsupported)
		}
		switch message.Role {
		case "user":
		default:
			return r, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, message.Role)
		}
		if e := validateMessageContent(message.Content); e != nil {
			return r, e
		}
	}
	// The web payloads in this package have one current user turn.  Accepting
	// an arbitrary history here would make the adapters either drop messages
	// or downgrade a system instruction into ordinary user text.  Callers that
	// need continuity must send one new user turn with an explicit
	// X-COT-Session header so the provider conversation is isolated by session.
	if len(r.Messages) != 1 {
		return r, fmt.Errorf("%w: only one user message is supported; history must use X-COT-Session", ErrUnsupported)
	}
	return r, nil
}

func validateMessageContent(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("%w: message content is required", ErrUnsupported)
	}
	var text string
	if json.Unmarshal(trimmed, &text) == nil {
		return nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(trimmed, &parts) != nil {
		return fmt.Errorf("%w: only text message content is supported", ErrUnsupported)
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: message content is required", ErrUnsupported)
	}
	for _, rawPart := range parts {
		var fields map[string]json.RawMessage
		if json.Unmarshal(rawPart, &fields) != nil || fields == nil {
			return fmt.Errorf("%w: text content parts must be objects", ErrUnsupported)
		}
		if len(fields) != 2 {
			return fmt.Errorf("%w: content part has unsupported fields", ErrUnsupported)
		}
		var kind, value string
		if e := json.Unmarshal(fields["type"], &kind); e != nil || kind != "text" {
			return fmt.Errorf("%w: multimodal message content is unsupported", ErrUnsupported)
		}
		if e := json.Unmarshal(fields["text"], &value); e != nil {
			return fmt.Errorf("%w: text content part must contain text", ErrUnsupported)
		}
		for field := range fields {
			if field != "type" && field != "text" {
				return fmt.Errorf("%w: content part field %q is unsupported", ErrUnsupported, field)
			}
		}
	}
	return nil
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL any    `json:"image_url"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

func lastUserText(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return contentText(messages[i].Content)
		}
	}
	return ""
}

func promptForKimi(messages []chatMessage) string {
	if len(messages) != 1 {
		return ""
	}
	return contentText(messages[0].Content)
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, e := url.Parse(base)
	if e != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("china web adapter: invalid base_url")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("china web adapter: invalid base_url")
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

func post(ctx context.Context, hc *http.Client, endpoint, token, contentType string, payload []byte, headers http.Header) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if e != nil {
		return nil, errors.New("china web adapter: invalid upstream request")
	}
	req.ContentLength = int64(len(payload))
	// Do not let net/http replay this POST after a failed connection. A web
	// chat generation is not idempotent and replay could duplicate a turn.
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
		return nil, errors.New("china web adapter: upstream transport failed")
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

func browserHeaders(origin string) http.Header {
	h := http.Header{}
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", origin)
	h.Set("Pragma", "no-cache")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36")
	return h
}

func setBearer(h http.Header, token string) { h.Set("Authorization", "Bearer "+token) }

func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		// crypto/rand failure is exceptionally rare; a time-derived value still
		// keeps request IDs distinct enough for a single process.
		n := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(b[:8], uint64(n))
		binary.LittleEndian.PutUint64(b[8:], uint64(n>>7))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(b[:4]), binary.BigEndian.Uint16(b[4:6]), binary.BigEndian.Uint16(b[6:8]), binary.BigEndian.Uint16(b[8:10]), b[10:])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
