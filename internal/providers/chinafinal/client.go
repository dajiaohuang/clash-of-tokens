// Package chinafinal contains the source-driven adapters for inventory
// entries #38-#42.  Tencent AI Studio has an OpenAI-shaped web contract in
// the pinned local references.  Metaso, Emohaa, and Spark remain explicit
// unsupported entries because their source contracts need browser/session
// behaviour that this package does not implement.
package chinafinal

import (
	"bytes"
	"context"
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
	AdapterMetaso          = "metaso"
	AdapterEmohaa          = "emohaa"
	AdapterTencentAIStudio = "tencent-aistudio-web"
	AdapterSpark           = "spark-web"

	defaultTencentAIStudio = "https://aistudio.tencent.ai"
	maxCookieBytes         = 16 << 10
	maxRequestBytes        = 16 << 20
	maxResponseBytes       = 64 << 20
)

type unsupportedError string

func (e unsupportedError) Error() string   { return string(e) }
func (e unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

var (
	ErrUnsupported error = unsupportedError("china final web adapter: unsupported protocol, source, or request")
	ErrCredential        = errors.New("china final web adapter: source credential is missing or invalid")
)

// Client is a deliberately standalone adapter. It does not register a source
// globally and it reads credentials only from the configured source KeyEnv.
type Client struct {
	source config.Source
	http   *http.Client

	mu     sync.Mutex
	closed bool
	gate   providerutil.Gate
}

// Supports reports whether a pinned reference contains enough runtime detail
// for this package to issue a request. The other inventory entries are kept
// visible to callers but fail closed in Do.
func Supports(adapter string) bool {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterTencentAIStudio, "tasw":
		return true
	default:
		return false
	}
}

// New constructs a client without reading credentials or making a request.
func New(source config.Source) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
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
		source: source,
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Close prevents future requests and closes idle transport connections.
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

// Do sends the Tencent AI Studio web request described by the pinned
// OmniRoute executor. The executor's registry marks the provider as OpenAI
// format, while its private endpoint emits SSE even when the caller asks for
// a non-streaming completion. This adapter therefore converts that known
// OpenAI SSE shape into a completion for stream=false and converts a JSON
// completion into SSE for stream=true. Caller headers are ignored except for
// rejecting X-COT-Session: Tencent's source contract has no conversation key
// to which this package could safely bind it.
func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("china final web adapter: nil client")
	}
	if ctx == nil {
		return nil, errors.New("china final web adapter: nil request context")
	}
	if proto != "chat" || strings.TrimSpace(model) == "" {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("china final web adapter: client is closed")
	}
	if !Supports(c.source.Adapter) {
		return nil, fmt.Errorf("%w: %s has no pinned runtime protocol; see chinafinal documentation", ErrUnsupported, c.source.Adapter)
	}
	if clientHeaders != nil && strings.TrimSpace(clientHeaders.Get("X-COT-Session")) != "" {
		return nil, fmt.Errorf("%w: Tencent AI Studio has no supported X-COT-Session contract", ErrUnsupported)
	}
	request, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	if err := c.gate.Lock(ctx); err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			c.gate.Unlock()
		}
	}()
	credential, err := c.credential()
	if err != nil {
		return nil, err
	}
	targetModel, err := tencentModelID(model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultTencentAIStudio)
	if err != nil {
		return nil, err
	}
	upstreamBody, err := json.Marshal(struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}{Model: targetModel, Messages: request.Messages})
	if err != nil || len(upstreamBody) > maxRequestBytes {
		return nil, fmt.Errorf("%w: cannot encode request body", ErrUnsupported)
	}
	endpoint := base + "/api/chat/" + url.PathEscape(targetModel)
	headers := browserHeaders(base)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "*/*")
	headers.Set("Cookie", credential)

	streamCtx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(streamCtx, http.MethodPost, endpoint, bytes.NewReader(upstreamBody))
	if err != nil {
		cancel()
		return nil, errors.New("china final web adapter: invalid upstream request")
	}
	req.ContentLength = int64(len(upstreamBody))
	req.GetBody = nil
	req.Header = headers
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("china final web adapter: upstream transport failed")
	}
	if resp.Body == nil {
		cancel()
		return nil, errors.New("china final web adapter: upstream response has no body")
	}
	resp.Body = &cancelableBody{ReadCloser: resp.Body, cancel: cancel}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Error bodies are deliberately preserved for the gateway's status and
		// diagnostics handling. They must not hold the source-wide gate.
		locked = false
		c.gate.Unlock()
		return resp, nil
	}
	if stream {
		if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
			if err := convertJSONResponseToSSE(resp, model); err != nil {
				return nil, err
			}
		} else {
			resp.Body = validatedTencentSSE(ctx, resp.Body)
			resp.ContentLength = -1
			resp.Header.Del("Content-Length")
			resp.Header.Set("Content-Type", "text/event-stream")
		}
		locked = false
		resp.Body = &gatedBody{ReadCloser: resp.Body, release: c.gate.Unlock}
		return resp, nil
	}
	if err := normalizeNonStreamingResponse(resp, model); err != nil {
		return nil, err
	}
	locked = false
	c.gate.Unlock()
	return resp, nil
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func decodeChatRequest(body []byte) (chatRequest, error) {
	if len(body) == 0 || len(body) > maxRequestBytes {
		return chatRequest{}, fmt.Errorf("%w: invalid request body size", ErrUnsupported)
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return chatRequest{}, fmt.Errorf("%w: request must be a JSON object", ErrUnsupported)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return chatRequest{}, fmt.Errorf("%w: request must contain one JSON value", ErrUnsupported)
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return chatRequest{}, fmt.Errorf("%w: unsupported top-level field %q", ErrUnsupported, key)
		}
	}
	if raw, ok := fields["model"]; ok {
		var requestModel string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &requestModel) != nil {
			return chatRequest{}, fmt.Errorf("%w: model must be a string", ErrUnsupported)
		}
	}
	if raw, ok := fields["stream"]; ok {
		var requestStream bool
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &requestStream) != nil {
			return chatRequest{}, fmt.Errorf("%w: stream must be boolean", ErrUnsupported)
		}
	}
	var request chatRequest
	if err := json.Unmarshal(body, &request); err != nil || len(request.Messages) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(fields["messages"], &rawMessages); err != nil || len(rawMessages) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages must be a non-empty array", ErrUnsupported)
	}
	for _, raw := range rawMessages {
		var message chatMessage
		md := json.NewDecoder(bytes.NewReader(raw))
		md.DisallowUnknownFields()
		if err := md.Decode(&message); err != nil || strings.TrimSpace(message.Role) == "" {
			return chatRequest{}, fmt.Errorf("%w: each message needs a role and text content", ErrUnsupported)
		}
		if err := md.Decode(&trailing); err != io.EOF {
			return chatRequest{}, fmt.Errorf("%w: each message must be one JSON object", ErrUnsupported)
		}
		var content string
		if json.Unmarshal(message.Content, &content) != nil || strings.TrimSpace(content) == "" {
			return chatRequest{}, fmt.Errorf("%w: only non-empty text content is supported", ErrUnsupported)
		}
		switch message.Role {
		case "system", "user", "assistant":
			// These roles are represented directly by Tencent's OpenAI-shaped
			// request. Tool/function/developer roles are not represented by the
			// pinned executor and must not be silently downgraded.
		default:
			return chatRequest{}, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, message.Role)
		}
	}
	return request, nil
}

func tencentModelID(model string) (string, error) {
	switch model {
	case "hy3-g", "hunyuan-default":
		return "HunyuanDefault", nil
	case "hunyuan-3d":
		return "Hunyuan3D", nil
	default:
		return "", fmt.Errorf("%w: Tencent AI Studio model %q is not in the pinned model set", ErrUnsupported, model)
	}
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	value := strings.TrimSpace(c.source.CredentialValue())
	if len(value) >= len("Cookie:") && strings.EqualFold(value[:len("Cookie:")], "Cookie:") {
		value = strings.TrimSpace(value[len("Cookie:"):])
	}
	if len(value) > maxCookieBytes || strings.ContainsAny(value, "\r\n") || value == "" || !strings.Contains(value, "=") {
		return "", ErrCredential
	}
	return value, nil
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return "", errors.New("china final web adapter: invalid base_url")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("china final web adapter: invalid base_url")
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

func browserHeaders(origin string) http.Header {
	h := http.Header{}
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", origin)
	h.Set("Pragma", "no-cache")
	h.Set("Referer", strings.TrimRight(origin, "/")+"/")
	h.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36")
	return h
}

type cancelableBody struct {
	io.ReadCloser
	once   sync.Once
	cancel context.CancelFunc
}

func (b *cancelableBody) cancelStream() { b.once.Do(b.cancel) }

func (b *cancelableBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.cancelStream()
	}
	return n, err
}

func (b *cancelableBody) Close() error {
	b.cancelStream()
	return b.ReadCloser.Close()
}

// gatedBody keeps the source-wide generation gate held until the caller has
// drained or closed a successful streaming response. This mirrors the
// lifetime of Tencent's cookie-backed web session and prevents overlapping
// turns from racing through one account's private endpoint.
type gatedBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *gatedBody) releaseGate() { b.once.Do(b.release) }

func (b *gatedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.releaseGate()
	}
	return n, err
}

func (b *gatedBody) Close() error {
	err := b.ReadCloser.Close()
	b.releaseGate()
	return err
}
