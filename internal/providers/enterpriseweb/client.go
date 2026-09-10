// Package enterpriseweb contains native, opt-in clients for private web
// protocols whose wire contracts are present in the pinned inventory.  It
// never creates accounts, refreshes anonymous quotas, or forwards caller
// credentials. Google AI Mode is the explicit CDP/browser exception.
package enterpriseweb

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
	AdapterMaxAI     = "maxai"
	AdapterNotionWeb = "notion-web"
	AdapterOperaAria = "opera-aria"
	AdapterGoogleAI  = "google-ai-mode"

	maxInputBytes    = 1 << 20
	maxEventBytes    = 1 << 20
	maxResponseBytes = 16 << 20
)

var (
	ErrUnsupported = unsupportedError("enterpriseweb adapter: unsupported protocol or request semantics")
	ErrCredential  = errors.New("enterpriseweb adapter: source credential is missing or invalid")
	ErrTruncated   = errors.New("enterpriseweb adapter: truncated upstream response")
	errSourceDone  = errors.New("enterpriseweb adapter: source stream complete")
)

type unsupportedError string

func (e unsupportedError) Error() string { return string(e) }
func (unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("enterpriseweb adapter: upstream status %d", e.Status)
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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string
	Messages []chatMessage
	Stream   bool
}

type credentialJSON struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
	UserID       string `json:"user_id"`
	HMACKey      string `json:"hmac_key"`
	AESKey       string `json:"aes_key"`
	ContextKey   string `json:"context_key"`
	AppVersion   string `json:"app_version"`
	Cookie       string `json:"cookie"`
	TokenV2      string `json:"token_v2"`
	SpaceID      string `json:"space_id"`
}

func Supports(adapter string) bool {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterMaxAI, AdapterNotionWeb, AdapterOperaAria, AdapterGoogleAI:
		return true
	default:
		return false
	}
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
		MaxIdleConns:      maxConn, MaxIdleConnsPerHost: maxConn, MaxConnsPerHost: maxConn,
		IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, MaxResponseHeaderBytes: 64 << 10,
		DisableCompression: true,
	}
	browser := config.Default().Browser
	if len(browsers) > 0 {
		browser = browsers[0]
	}
	return &Client{source: source, browser: browser, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) Do(ctx context.Context, protocolName, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if ctx == nil {
		return nil, errors.New("enterpriseweb adapter: nil request context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if protocolName != "chat" {
		return nil, ErrUnsupported
	}
	if len(body) == 0 || len(body) > maxInputBytes {
		return nil, fmt.Errorf("%w: request body exceeds limit", ErrUnsupported)
	}
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("%w: model is required", ErrUnsupported)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("enterpriseweb adapter: client is closed")
	}
	if headers != nil && strings.TrimSpace(headers.Get("X-COT-Session")) != "" {
		return nil, fmt.Errorf("%w: stateless web adapters reject X-COT-Session", ErrUnsupported)
	}
	req, err := parseChat(body, model, stream)
	if err != nil {
		return nil, err
	}
	if !Supports(c.source.Adapter) {
		return nil, ErrUnsupported
	}
	switch strings.ToLower(strings.TrimSpace(c.source.Adapter)) {
	case AdapterMaxAI:
		return c.doMaxAI(ctx, req)
	case AdapterNotionWeb:
		return c.doNotion(ctx, req)
	case AdapterOperaAria:
		return c.doOperaAria(ctx, req)
	case AdapterGoogleAI:
		return c.doGoogleAIMode(ctx, req)
	default:
		return nil, ErrUnsupported
	}
}

func parseChat(body []byte, model string, stream bool) (chatRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return chatRequest{}, fmt.Errorf("%w: a JSON object is required", ErrUnsupported)
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return chatRequest{}, fmt.Errorf("%w: unsupported request field %q", ErrUnsupported, key)
		}
	}
	var raw struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
		Stream   *bool         `json:"stream"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil || len(raw.Messages) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages must be a non-empty text array", ErrUnsupported)
	}
	if raw.Model != "" && strings.TrimSpace(raw.Model) != model {
		return chatRequest{}, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
	}
	for _, m := range raw.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return chatRequest{}, fmt.Errorf("%w: unsupported message role", ErrUnsupported)
		}
		if m.Content == "" {
			return chatRequest{}, fmt.Errorf("%w: message content must be non-empty text", ErrUnsupported)
		}
	}
	if raw.Messages[len(raw.Messages)-1].Role != "user" {
		return chatRequest{}, fmt.Errorf("%w: the last message must be a user message", ErrUnsupported)
	}
	if raw.Stream != nil && *raw.Stream != stream {
		return chatRequest{}, fmt.Errorf("%w: stream flag conflicts with gateway selection", ErrUnsupported)
	}
	return chatRequest{Model: model, Messages: raw.Messages, Stream: stream}, nil
}

func (c *Client) credential() (credentialJSON, string, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return credentialJSON{}, "", ErrCredential
	}
	raw := strings.TrimSpace(c.source.CredentialValue())
	if raw == "" || len(raw) > maxResponseBytes {
		return credentialJSON{}, "", ErrCredential
	}
	var cred credentialJSON
	if strings.HasPrefix(raw, "{") {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cred); err != nil {
			return credentialJSON{}, "", ErrCredential
		}
	} else if strings.EqualFold(c.source.Adapter, AdapterOperaAria) {
		cred.RefreshToken = raw
	} else if strings.EqualFold(c.source.Adapter, AdapterNotionWeb) {
		cred.Cookie = raw
	} else {
		return credentialJSON{}, "", ErrCredential
	}
	for _, v := range []string{cred.AccessToken, cred.RefreshToken, cred.DeviceID, cred.UserID, cred.HMACKey, cred.AESKey, cred.ContextKey, cred.AppVersion, cred.Cookie, cred.TokenV2, cred.SpaceID} {
		if strings.ContainsAny(v, "\r\n") || len(v) > maxResponseBytes {
			return credentialJSON{}, "", ErrCredential
		}
	}
	return cred, raw, nil
}

func baseURL(source config.Source, fallback string) (string, error) {
	v := strings.TrimSpace(source.BaseURL)
	if v == "" {
		v = fallback
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enterpriseweb adapter: invalid base_url")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", errors.New("enterpriseweb adapter: base_url must use HTTPS outside loopback")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func joinPath(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func postJSON(ctx context.Context, hc *http.Client, endpoint string, payload []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("enterpriseweb adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(payload))
	req.Header = headers.Clone()
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("enterpriseweb adapter: upstream transport failed")
	}
	return resp, nil
}

type eventParser func(context.Context, io.Reader, func(string) error) error

type convertedBody struct {
	*io.PipeReader
	writer   *io.PipeWriter
	upstream io.Closer
	done     chan struct{}
	once     sync.Once
	doneOnce sync.Once
	id       string
}

func (b *convertedBody) signalDone() { b.doneOnce.Do(func() { close(b.done) }) }
func (b *convertedBody) finish(err error) {
	if err != nil {
		_ = b.writer.CloseWithError(err)
	} else {
		_ = b.writer.Close()
	}
}
func (b *convertedBody) Close() error {
	b.once.Do(func() { b.signalDone(); _ = b.upstream.Close() })
	return b.PipeReader.Close()
}

func newConvertedResponse(ctx context.Context, upstream io.ReadCloser, model string, parser eventParser) io.ReadCloser {
	r, w := io.Pipe()
	b := &convertedBody{PipeReader: r, writer: w, upstream: upstream, done: make(chan struct{}), id: randomID("chatcmpl-")}
	go func() {
		select {
		case <-ctx.Done():
			_ = b.Close()
		case <-b.done:
		}
	}()
	go func() {
		defer upstream.Close()
		defer b.signalDone()
		bw := &boundedWriter{Writer: w, limit: maxResponseBytes}
		if err := writeChunk(bw, b.id, model, map[string]any{"role": "assistant"}, nil); err != nil {
			b.finish(err)
			return
		}
		seen := false
		err := parser(ctx, upstream, func(text string) error {
			if text == "" {
				return nil
			}
			seen = true
			return writeChunk(bw, b.id, model, map[string]any{"content": text}, nil)
		})
		if err == nil && !seen {
			err = errors.New("enterpriseweb adapter: upstream completed without answer")
		}
		if err == nil {
			err = writeChunk(bw, b.id, model, map[string]any{}, "stop")
			if err == nil {
				_, err = io.WriteString(bw, "data: [DONE]\n\n")
			}
		}
		b.finish(err)
	}()
	return b
}

type boundedWriter struct {
	io.Writer
	limit, total int
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.total {
		return 0, errors.New("enterpriseweb adapter: converted output exceeds limit")
	}
	n, e := w.Writer.Write(p)
	w.total += n
	if e == nil && n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, e
}

func collectResponse(ctx context.Context, upstream io.ReadCloser, model string, parser eventParser) (*http.Response, error) {
	defer upstream.Close()
	var content strings.Builder
	err := parser(ctx, upstream, func(s string) error {
		if content.Len()+len(s) > maxResponseBytes {
			return errors.New("enterpriseweb adapter: upstream response exceeds limit")
		}
		content.WriteString(s)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if content.Len() == 0 {
		return nil, errors.New("enterpriseweb adapter: upstream completed without answer")
	}
	data, _ := json.Marshal(map[string]any{"id": randomID("chatcmpl-"), "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": content.String()}, "finish_reason": "stop"}}, "usage": nil})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-COT-Delivery": []string{"buffered"}}, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}, nil
}

func writeChunk(w io.Writer, id, model string, delta map[string]any, finish any) error {
	data, e := json.Marshal(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(w, "data: %s\n\n", data)
	return e
}

func parseSSELines(ctx context.Context, body io.Reader, onData func([]byte) error) error {
	return parseSSELinesWithEvents(ctx, body, nil, onData)
}

func parseSSELinesWithEvents(ctx context.Context, body io.Reader, onEvent func(string), onData func([]byte) error) error {
	r := bufio.NewReaderSize(body, 8192)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := r.ReadString('\n')
		total += len(line)
		if total > maxResponseBytes {
			return errors.New("enterpriseweb adapter: upstream stream exceeds limit")
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(line, "event:") {
			if onEvent != nil {
				onEvent(strings.TrimSpace(strings.TrimPrefix(line, "event:")))
			}
		} else if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if len(data) > maxEventBytes {
				return errors.New("enterpriseweb adapter: upstream event exceeds limit")
			}
			if len(data) > 0 {
				if e := onData([]byte(data)); e != nil {
					if errors.Is(e, errSourceDone) {
						return nil
					}
					return e
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// streamObjectHasError recognizes the small set of terminal error envelopes
// used by the web streams.  The caller deliberately maps these to a generic
// provider error instead of exposing upstream text or credentials.
func streamObjectHasError(value map[string]any) bool {
	if value == nil {
		return false
	}
	if typ, _ := value["type"].(string); strings.EqualFold(typ, "error") {
		return true
	}
	if key, _ := value["data_key"].(string); strings.Contains(strings.ToLower(key), "error") {
		return true
	}
	if status, _ := value["status"].(string); strings.Contains(strings.ToLower(status), "error") || strings.Contains(strings.ToLower(status), "fail") {
		return true
	}
	if raw, ok := value["error"]; ok && raw != nil {
		return true
	}
	if nested, ok := value["response"].(map[string]any); ok && streamObjectHasError(nested) {
		return true
	}
	if nested, ok := value["data"].(map[string]any); ok && streamObjectHasError(nested) {
		return true
	}
	return false
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	var out strings.Builder
	out.Grow(22)
	out.WriteString(prefix)
	for _, v := range b {
		out.WriteByte(alphabet[int(v)%len(alphabet)])
	}
	return out.String()
}
