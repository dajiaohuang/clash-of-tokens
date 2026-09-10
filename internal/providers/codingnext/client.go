// Package codingnext contains native adapters for the coding-oriented sources
// whose private HTTP contracts are pinned in .clash-tokens/reference/omniroute.
//
// The package is deliberately not registered here. Callers must opt in with a
// config.Source and provide credentials through Source.KeyEnv. No adapter
// starts a helper process, follows redirects, or forwards caller credentials.
package codingnext

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
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
	AdapterFreebuff    = "freebuff"
	AdapterZedHosted   = "zed-hosted"
	AdapterCodeBuddy   = "codebuddy-cn"
	AdapterCodeBuddyCN = AdapterCodeBuddy
	AdapterZed         = AdapterZedHosted
	maxEventBytes      = 16 << 20
	maxResponseBytes   = 64 << 20
	maxSessions        = 4096
	maxHeaderValueLen  = 4096
)

// Supports reports whether this package has a native adapter for adapter.
func Supports(adapter string) bool {
	switch adapter {
	case AdapterFreebuff, AdapterZedHosted, AdapterCodeBuddy:
		return true
	default:
		return false
	}
}

type unsupportedError string

func (e unsupportedError) Error() string   { return string(e) }
func (e unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

var (
	ErrUnsupported  = unsupportedError("coding next adapter: unsupported protocol or request semantics")
	ErrCredential   = errors.New("coding next adapter: source credential is missing or invalid")
	ErrTruncated    = errors.New("coding next adapter: truncated upstream response")
	ErrSessionLimit = errors.New("coding next adapter: session capacity exceeded")
)

// HTTPError preserves an upstream status for prerequisite requests such as a
// Freebuff session or agent-run allocation.
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("coding next adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("coding next adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

// Client implements the internal/upstream adapter contract.
type Client struct {
	source   config.Source
	http     *http.Client
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

type session struct {
	requestMu providerutil.Gate
	threadID  string
}

type responseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *responseBody) unlock() { b.once.Do(b.release) }
func (b *responseBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	if e != nil {
		b.unlock()
	}
	return n, e
}
func (b *responseBody) Close() error {
	e := b.ReadCloser.Close()
	b.unlock()
	return e
}

// New creates a client. It does not read credentials or perform network I/O.
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
		source: source,
		http: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
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

// Do sends one Chat Completions request through the selected coding adapter.
// Responses and Anthropic Messages are rejected because these private
// contracts are only translated from an OpenAI Chat request in this package.
func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if proto != "chat" {
		return nil, ErrUnsupported
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("%w: model is required", ErrUnsupported)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("coding next adapter: client is closed")
	}
	token, err := c.credential()
	if err != nil {
		return nil, err
	}
	request, err := decodeOpenAIRequest(body)
	if err != nil {
		return nil, err
	}
	state, err := c.session(headers.Get("X-COT-Session"))
	if err != nil {
		return nil, err
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

	var response *http.Response
	switch c.source.Adapter {
	case AdapterFreebuff:
		response, err = c.doFreebuff(ctx, token, model, stream, request)
	case AdapterCodeBuddy:
		response, err = c.doCodeBuddy(ctx, token, model, stream, request)
	case AdapterZedHosted:
		response, err = c.doZedHosted(ctx, token, model, stream, request, state)
	default:
		return nil, fmt.Errorf("%w: adapter %q", ErrUnsupported, c.source.Adapter)
	}
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("coding next adapter: empty upstream response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}

	var finish func()
	if run, ok := response.Body.(*freebuffRunBody); ok {
		finish = run.finish
	}
	if c.source.Adapter == AdapterFreebuff || c.source.Adapter == AdapterZedHosted || c.source.Adapter == AdapterCodeBuddy {
		converted, err := c.convertOpenAIResponse(ctx, response, model, stream, c.source.Adapter)
		if err != nil {
			return nil, err
		}
		response = converted
	}
	if finish != nil {
		if stream {
			response.Body = &completedRunBody{ReadCloser: response.Body, finish: finish}
		} else {
			go finish()
		}
	}
	if stream {
		locked = false
		response.Body = &responseBody{ReadCloser: response.Body, release: state.requestMu.Unlock}
	}
	return response, nil
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	token := strings.TrimSpace(c.source.CredentialValue())
	if token == "" || len(token) > maxHeaderValueLen || strings.ContainsAny(token, "\r\n") {
		return "", ErrCredential
	}
	return token, nil
}

func (c *Client) session(key string) (*session, error) {
	if key == "" {
		return &session{threadID: uuid()}, nil
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
	s := &session{threadID: uuid()}
	c.sessions[key] = s
	return s, nil
}

func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(b[:8], uint64(n))
		binary.LittleEndian.PutUint64(b[8:], uint64(n>>7))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(b[:4]), binary.BigEndian.Uint16(b[4:6]), binary.BigEndian.Uint16(b[6:8]), binary.BigEndian.Uint16(b[8:10]), b[10:])
}

func endpoint(source config.Source, fallback, suffix string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("coding next adapter: invalid base_url")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && (host == "localhost" || (ip != nil && ip.IsLoopback()))) {
		return "", errors.New("coding next adapter: base_url must use HTTPS outside loopback")
	}
	if suffix == "" || strings.HasSuffix(u.Path, suffix) {
		return base, nil
	}
	return base + suffix, nil
}

func postJSON(ctx context.Context, hc *http.Client, target, token string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("coding next adapter: invalid upstream request")
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	req.Header = cloneHeaders(headers)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding next adapter: upstream transport failed")
	}
	return resp, nil
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		for _, value := range values {
			out.Add(key, value)
		}
	}
	return out
}

func readLimited(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("coding next adapter: upstream response exceeds byte limit")
	}
	return data, nil
}
