// Package chinamore contains standalone adapters for the MiniMax Agent,
// Xiaomi Mimo AI Studio, and StepChat web contracts.  These are private,
// changeable web endpoints and are deliberately not part of the built-in
// provider registry.  Credentials are read only from the source KeyEnv.
package chinamore

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	AdapterMiniMax = "minimax-web"
	AdapterMimo    = "mimo"
	AdapterStep    = "stepchat"

	defaultMiniMaxBase = "https://agent.minimaxi.com"
	defaultMimoBase    = "https://aistudio.xiaomimimo.com"
	defaultStepBase    = "https://stepchat.cn"

	maxEventBytes = 16 << 20
	maxSessions   = 4096
	maxPolls      = 240
)

type unsupportedError string

func (e unsupportedError) Error() string   { return string(e) }
func (e unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

var (
	ErrUnsupported  = unsupportedError("china more web adapter: unsupported protocol or request")
	ErrCredential   = errors.New("china more web adapter: source credential is missing or invalid")
	ErrSessionLimit = errors.New("china more web adapter: session capacity exceeded")
	ErrTruncated    = errors.New("china more web adapter: truncated upstream response")
)

// HTTPError retains a failed prerequisite's HTTP status for callers which do
// not receive an upstream response body (for example token exchange).
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("china more web adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("china more web adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type Client struct {
	source config.Source
	http   *http.Client

	mu       sync.Mutex
	sessions map[string]*session
	closed   bool

	deviceMu sync.Mutex
	devices  map[string]miniDevice
	stepMu   sync.Mutex
	step     map[string]stepAccess
}

type session struct {
	gate providerutil.Gate
	mu   sync.Mutex
	// MiniMax has an explicit chat id which can be reused for a session.
	miniChatID string
}

// sessionResponseBody holds a same-session generation lock until a converted
// stream reaches EOF or the caller closes it.
type sessionResponseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

// cancelableResponseBody ties the lifetime of a converted streaming response
// to the context used by its upstream poller.  Closing a downstream response
// must stop the goroutine and its network request instead of leaving it alive
// until the request timeout.
type cancelableResponseBody struct {
	io.ReadCloser
	once   sync.Once
	cancel context.CancelFunc
}

func (b *cancelableResponseBody) cancelStream() { b.once.Do(b.cancel) }
func (b *cancelableResponseBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.cancelStream()
	}
	return n, e
}
func (b *cancelableResponseBody) Close() error {
	e := b.ReadCloser.Close()
	b.cancelStream()
	return e
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

func New(source config.Source) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	tr := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           max(4, min(source.MaxInflight, 256)),
		MaxIdleConnsPerHost:    max(4, min(source.MaxInflight, 256)),
		MaxConnsPerHost:        maxConn,
		IdleConnTimeout:        60 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  60 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     true,
	}
	return &Client{
		source:   source,
		http:     &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		sessions: make(map[string]*session),
		devices:  make(map[string]miniDevice),
		step:     make(map[string]stepAccess),
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
		return nil, errors.New("china more web adapter: model is required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if strings.TrimSpace(clientHeaders.Get("X-COT-Session")) != "" {
		return nil, fmt.Errorf("%w: these adapters only support independent single turns", ErrUnsupported)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("china more web adapter: client is closed")
	}
	req, e := decodeChatRequest(body)
	if e != nil {
		return nil, e
	}
	// The protocol argument is authoritative; a stale body model must not
	// change the selected source target.
	req.Model = model
	credential, e := c.credential()
	if e != nil {
		return nil, e
	}
	s, e := c.session(clientHeaders.Get("X-COT-Session"))
	if e != nil {
		return nil, e
	}
	if e = s.gate.Lock(ctx); e != nil {
		return nil, e
	}
	locked := true
	defer func() {
		if locked {
			s.gate.Unlock()
		}
	}()

	requestCtx := ctx
	var streamCancel context.CancelFunc
	streamOwned := false
	if stream {
		requestCtx, streamCancel = context.WithCancel(ctx)
		defer func() {
			if !streamOwned {
				streamCancel()
			}
		}()
	}
	var response *http.Response
	switch c.source.Adapter {
	case AdapterMiniMax, "minimax":
		response, e = c.doMiniMax(requestCtx, credential, req, stream, s)
	case AdapterMimo, "mimo-web":
		response, e = c.doMimo(requestCtx, credential, req, stream, s)
	case AdapterStep, "step-chat":
		response, e = c.doStep(requestCtx, credential, req, stream, s)
	default:
		return nil, fmt.Errorf("china more web adapter: unsupported adapter %q", c.source.Adapter)
	}
	if e != nil {
		return nil, e
	}
	if response == nil {
		return nil, errors.New("china more web adapter: empty upstream response")
	}
	if stream {
		if response.Body == nil {
			return nil, errors.New("china more web adapter: streaming response has no body")
		}
		locked = false
		response.Body = &sessionResponseBody{ReadCloser: &cancelableResponseBody{ReadCloser: response.Body, cancel: streamCancel}, release: s.gate.Unlock}
		streamOwned = true
	}
	return response, nil
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	v := strings.TrimSpace(c.source.CredentialValue())
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
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
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
		return r, errors.New("china more web adapter: invalid JSON request")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(body, &fields); e != nil || fields == nil {
		return r, errors.New("china more web adapter: request must be an object")
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "web_search": true, "reasoning_effort": true, "enable_thinking": true}
	for field := range fields {
		if !allowed[field] {
			return r, fmt.Errorf("%w: unsupported top-level field %q", ErrUnsupported, field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if e := decoder.Decode(&r); e != nil {
		return r, errors.New("china more web adapter: invalid chat request")
	}
	var trailing json.RawMessage
	if e := decoder.Decode(&trailing); e != io.EOF {
		return r, errors.New("china more web adapter: invalid chat request")
	}
	if len(r.Messages) != 1 {
		return r, fmt.Errorf("%w: exactly one user message is supported; web history cannot be silently dropped", ErrUnsupported)
	}
	for _, raw := range []json.RawMessage{fields["messages"]} {
		var raws []json.RawMessage
		if e := json.Unmarshal(raw, &raws); e != nil || len(raws) != 1 {
			return r, fmt.Errorf("%w: messages must be a one-element array", ErrUnsupported)
		}
		for _, rawMessage := range raws {
			var message chatMessage
			md := json.NewDecoder(bytes.NewReader(rawMessage))
			md.DisallowUnknownFields()
			if e := md.Decode(&message); e != nil || message.Role != "user" {
				return r, fmt.Errorf("%w: only a user message is supported", ErrUnsupported)
			}
			if e := md.Decode(&trailing); e != io.EOF {
				return r, fmt.Errorf("%w: message must be one JSON object", ErrUnsupported)
			}
			if e := validateMessageContent(message.Content); e != nil {
				return r, e
			}
		}
	}
	if _, ok := fields["web_search"]; ok {
		return r, fmt.Errorf("%w: web_search is not exposed by the pinned MiniMax/Mimo/StepChat contracts", ErrUnsupported)
	}
	if _, ok := fields["enable_thinking"]; ok {
		return r, fmt.Errorf("%w: enable_thinking is not exposed by these web contracts", ErrUnsupported)
	}
	if _, ok := fields["reasoning_effort"]; ok {
		return r, fmt.Errorf("%w: reasoning_effort is not exposed by these web contracts", ErrUnsupported)
	}
	return r, nil
}

func validateMessageContent(raw json.RawMessage) error {
	t := bytes.TrimSpace(raw)
	var text string
	if len(t) == 0 || bytes.Equal(t, []byte("null")) || json.Unmarshal(t, &text) != nil || strings.TrimSpace(text) == "" {
		return fmt.Errorf("%w: only non-empty text content is supported", ErrUnsupported)
	}
	return nil
}

func contentText(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, e := url.Parse(base)
	if e != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("china more web adapter: invalid base_url")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("china more web adapter: invalid base_url")
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

func postJSON(ctx context.Context, hc *http.Client, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("china more web adapter: invalid upstream request")
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header = cloneHeaders(headers)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, e := hc.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("china more web adapter: upstream transport failed")
	}
	return resp, nil
}

func cloneHeaders(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, values := range h {
		for _, value := range values {
			out.Add(k, value)
		}
	}
	return out
}

func readLimitedJSON(resp *http.Response, limit int64, out any) error {
	if resp == nil || resp.Body == nil {
		return errors.New("china more web adapter: upstream response has no body")
	}
	defer resp.Body.Close()
	if limit < 0 {
		return errors.New("china more web adapter: invalid JSON byte limit")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("%w: upstream JSON exceeds byte limit", ErrTruncated)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("china more web adapter: trailing JSON data")
	}
	return nil
}

func browserHeaders(origin string) http.Header {
	h := http.Header{}
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", origin)
	h.Set("Pragma", "no-cache")
	h.Set("Referer", strings.TrimRight(origin, "/")+"/")
	h.Set("Sec-Fetch-Dest", "empty")
	h.Set("Sec-Fetch-Mode", "cors")
	h.Set("Sec-Fetch-Site", "same-origin")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/145.0.0.0 Safari/537.36")
	return h
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

func uuidNoHyphen() string { return strings.ReplaceAll(uuid(), "-", "") }

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func encodeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '!' || b == '~' || b == '*' || b == '\'' || b == '(' || b == ')' {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&0x0f])
	}
	return out.String()
}

func jsonNumber(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}

func rawMapString(v map[string]any, key string) string {
	if value, ok := v[key].(string); ok {
		return value
	}
	return ""
}

func sessionID(h http.Header) string { return h.Get("X-COT-Session") }

func bodyResponse(status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d", status), Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func copyResponseBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("china more web adapter: upstream response has no body")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxEventBytes))
	resp.Body.Close()
	return b, e
}
