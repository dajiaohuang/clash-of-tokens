// Package codingmore contains native adapters for coding-source contracts
// that have a directly documented HTTP protocol.
//
// Amazon Q Developer is implemented through its AWS CodeWhisperer streaming
// endpoint. Augment is implemented through its tenant chat-stream endpoint.
// Devin CLI is implemented through the official ACP stdio companion.
package codingmore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	AdapterAmazonQ  = "amazon-q"
	AdapterDevinCLI = "devin-cli"
	AdapterAugment  = "augment"

	// Descriptive aliases keep callers from spelling the source names again.
	AdapterAmazonQDeveloper = AdapterAmazonQ
	AdapterDevin            = AdapterDevinCLI
	AdapterAuggie           = AdapterAugment

	// These limits protect the adapter when it is used without the gateway's
	// outer body and output limits (for example, in a direct integration test).
	maxRequestBytes int64  = 16 << 20
	maxEventBytes   int64  = 64 << 20
	maxEventFrame   uint32 = 16 << 20
	maxHeaderValue         = 4096
	maxSessions            = 4096
)

type unsupportedError string

func (e unsupportedError) Error() string   { return string(e) }
func (e unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

var (
	ErrUnsupported = unsupportedError("coding more adapter: unsupported protocol or request semantics")
	ErrCredential  = errors.New("coding more adapter: source credential is missing or invalid")
	ErrTruncated   = errors.New("coding more adapter: truncated upstream response")
	ErrClosed      = errors.New("coding more adapter: client is closed")
)

// HTTPError preserves a useful upstream status while leaving the response body
// available to the caller, matching the internal/upstream contract.
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("coding more adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("coding more adapter: %s (status %d)", e.What, e.Status)
}

func (e *HTTPError) HTTPStatus() int { return e.Status }

// Client is safe for concurrent calls. Calls carrying the same X-COT-Session
// are serialized because CodeWhisperer conversation state is mutable.
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

func (b *responseBody) unlock() { b.once.Do(b.release) }

func (b *responseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		_ = b.ReadCloser.Close()
		b.unlock()
	}
	return n, err
}

func (b *responseBody) Close() error {
	err := b.ReadCloser.Close()
	b.unlock()
	return err
}

// New constructs a client without reading credentials or performing network
// I/O. Authentication is read from source.KeyEnv on each Do call.
func New(source config.Source) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	transport := &http.Transport{
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
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		sessions: make(map[string]*session),
	}
}

// Close releases idle connections and prevents new calls. An in-flight call
// remains owned by its context and response body.
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

// Supports reports the native surface of this package. The CLI-only sources
// remain visible as named adapters so callers can explain why they are not
// selected, but they do not claim an executable implementation.
func Supports(adapter string) bool {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterAmazonQ, AdapterDevinCLI, AdapterAugment:
		return true
	default:
		return false
	}
}

// Do sends an OpenAI Chat Completions request to the selected coding companion
// and returns a gateway-compatible response. Only text chat is supported. The
// original caller headers are not forwarded; X-COT-Session selects local
// conversation serialization for HTTP adapters and is never interpreted as a
// credential.
func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, errors.New("coding more adapter: nil context")
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
	adapter := strings.ToLower(strings.TrimSpace(c.source.Adapter))
	if !Supports(adapter) {
		return nil, fmt.Errorf("%w: adapter %q", ErrUnsupported, c.source.Adapter)
	}
	if protocol != "chat" {
		return nil, fmt.Errorf("%w: codingmore adapters support chat only", ErrUnsupported)
	}
	model = strings.TrimSpace(model)
	if adapter == AdapterAmazonQ {
		model = strings.TrimPrefix(model, "amazon-q/")
	}
	if model == "" || len(model) > 256 {
		return nil, fmt.Errorf("%w: model is required", ErrUnsupported)
	}
	if int64(len(body)) > maxRequestBytes {
		return nil, fmt.Errorf("%w: request body exceeds byte limit", ErrUnsupported)
	}
	if adapter == AdapterDevinCLI {
		if key := strings.TrimSpace(headerValue(headers, "X-COT-Session")); key != "" {
			return nil, fmt.Errorf("%w: Devin CLI does not support gateway session continuation", ErrUnsupported)
		}
		return c.doDevin(ctx, model, stream, body)
	}
	token, err := c.credential()
	if err != nil {
		return nil, err
	}
	if adapter == AdapterAugment {
		return c.doAugment(ctx, model, stream, body, headers, token)
	}
	request, err := decodeChatRequest(body, model)
	if err != nil {
		return nil, err
	}

	key := strings.TrimSpace(headerValue(headers, "X-COT-Session"))
	s, err := c.session(key)
	if err != nil {
		return nil, err
	}
	if err := s.gate.Lock(ctx); err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			s.gate.Unlock()
		}
	}()

	endpoint, err := amazonQEndpoint(c.source)
	if err != nil {
		return nil, err
	}
	payload, err := buildAmazonQPayload(request, model, s.conversation, c.profileARN())
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("coding more adapter: cannot encode Amazon Q request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("coding more adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(encoded))
	setAmazonQHeaders(req.Header, token)
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding more adapter: upstream transport failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}

	if stream {
		response.Header.Set("Content-Type", "text/event-stream")
		response.Header.Set("Cache-Control", "no-cache")
		response.ContentLength = -1
		response.Header.Del("Content-Length")
		response.Body = &responseBody{
			ReadCloser: newAmazonQStream(ctx, response.Body, model),
			release:    s.gate.Unlock,
		}
		locked = false
		if key != "" {
			response.Header.Set("X-COT-Session", key)
		}
		return response, nil
	}

	result, err := collectAmazonQ(ctx, response.Body, model)
	if err != nil {
		return nil, err
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("coding more adapter: cannot encode response")
	}
	response.Body = io.NopCloser(bytes.NewReader(encodedResult))
	response.ContentLength = int64(len(encodedResult))
	response.Header.Del("Content-Length")
	response.Header.Set("Content-Type", "application/json")
	if key != "" {
		response.Header.Set("X-COT-Session", key)
	}
	return response, nil
}

func (c *Client) credential() (string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return "", ErrCredential
	}
	value := strings.TrimSpace(c.source.CredentialValue())
	if value == "" || len(value) > maxHeaderValue || strings.ContainsAny(value, "\r\n") {
		return "", ErrCredential
	}
	return value, nil
}

func (c *Client) profileARN() string {
	if strings.TrimSpace(c.source.AccountIDEnv) == "" {
		return ""
	}
	value := strings.TrimSpace(os.Getenv(c.source.AccountIDEnv))
	if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return ""
	}
	return value
}

func (c *Client) session(key string) (*session, error) {
	if key == "" {
		return &session{conversation: randomUUID()}, nil
	}
	if len(key) > 256 {
		sum := sha256.Sum256([]byte(key))
		key = hex.EncodeToString(sum[:])
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if existing := c.sessions[key]; existing != nil {
		return existing, nil
	}
	if len(c.sessions) >= maxSessions {
		return nil, errors.New("coding more adapter: session capacity exceeded")
	}
	s := &session{conversation: randomUUID()}
	c.sessions[key] = s
	return s, nil
}

func deterministicConversationID(value string) string {
	sum := sha256.Sum256([]byte("amazon-q:" + value))
	var b [16]byte
	copy(b[:], sum[:16])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return prefix + "unknown"
	}
	return prefix + hex.EncodeToString(b[:])
}

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func headerValue(headers http.Header, key string) string {
	if headers == nil {
		return ""
	}
	if value := headers.Get(key); value != "" {
		return strings.TrimSpace(value)
	}
	for name, values := range headers {
		if !strings.EqualFold(name, key) || len(values) == 0 {
			continue
		}
		return strings.TrimSpace(values[0])
	}
	return ""
}

func endpointBase(source config.Source) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		return "", errors.New("coding more adapter: base_url is required")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("coding more adapter: invalid base_url")
	}
	host := strings.ToLower(u.Hostname())
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && loopback) {
		return "", errors.New("coding more adapter: HTTPS required outside loopback")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}

func amazonQEndpoint(source config.Source) (string, error) {
	base, err := endpointBase(source)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(base, "/generateAssistantResponse") {
		return base, nil
	}
	return base + "/generateAssistantResponse", nil
}

func setAmazonQHeaders(h http.Header, token string) {
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/vnd.amazon.eventstream")
	h.Set("X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse")
	h.Set("User-Agent", "AWS-SDK-JS/3.0.0 kiro-ide/1.0.0")
	h.Set("X-Amz-User-Agent", "aws-sdk-js/3.0.0 kiro-ide/1.0.0")
	h.Set("Amz-Sdk-Request", "attempt=1; max=3")
	h.Set("Amz-Sdk-Invocation-Id", randomUUID())
	h.Set("x-amzn-bedrock-cache-control", "enable")
	h.Set("Authorization", "Bearer "+token)
}
