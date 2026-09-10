// Package embedded contains native HTTP adapters for web-backed providers.
//
// The adapters intentionally keep credentials in explicit environment
// variables selected by config.Source.KeyEnv (and, where needed,
// AccountIDEnv). They do not forward credentials supplied by the caller.
package embedded

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
	AdapterFanzha      = "fanzha"
	AdapterTabbit      = "tabbit"
	AdapterNotion      = "notion-web"
	AdapterChatAIGPT   = "chataigpt"
	AdapterChatGPTFree = "chatgptfree"

	fanzhaDefaultBase = "https://xzfzznt.gaj.sh.gov.cn"
	tabbitDefaultBase = "https://web.tabbit.ai"
	notionDefaultBase = "https://www.notion.so"

	tabbitDefaultSignKey = "f8d0e6a73f8d4b1a9c3d2e1f9a4b7c6d"
	tabbitDefaultVersion = "1.1.39(10101039)"
	notionClientVersion  = "23.13.20251011.2037"
)

var sseDone = []byte("data: [DONE]\n\n")
var errStreamComplete = errors.New("upstream stream complete")

// Client implements the provider contract used by the gateway. New does not
// perform a network request; credentials are checked when Do is called.
type Client struct {
	http   *http.Client
	source config.Source

	mu          sync.Mutex
	tabbitKey   string
	tabbitSess  string
	tabbitReady bool
}

// New creates an embedded provider client for one source.
func New(source config.Source) *Client {
	max := source.MaxInflight
	if max < 1 {
		max = 8
	}
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           max,
		MaxIdleConnsPerHost:    max,
		MaxConnsPerHost:        max,
		IdleConnTimeout:        60 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  60 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     true,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		source:    source,
		tabbitKey: tabbitDefaultSignKey,
	}
}

// Close releases idle HTTP connections.
func (c *Client) Close() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Do converts the gateway protocol into the selected web provider protocol.
// Streaming responses are converted lazily so cancellation propagates to the
// provider request while the caller is reading the returned body.
func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("embedded provider is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSemantics(body, c.source.Adapter); err != nil {
		return nil, err
	}
	switch c.source.Adapter {
	case AdapterFanzha:
		return c.doFanzha(ctx, protocol, model, stream, body)
	case AdapterTabbit:
		return c.doTabbit(ctx, protocol, model, stream, body)
	case AdapterNotion:
		return c.doNotion(ctx, protocol, model, stream, body)
	case AdapterChatAIGPT, AdapterChatGPTFree:
		return c.doWordPress(ctx, protocol, model, stream, body)
	default:
		return nil, fmt.Errorf("embedded provider does not support adapter %q", c.source.Adapter)
	}
}

type requestError struct{ message string }

func (e *requestError) Error() string   { return e.message }
func (e *requestError) HTTPStatus() int { return 422 }
func validateSemantics(body []byte, adapter string) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return &requestError{"invalid chat JSON"}
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return &requestError{"unsupported embedded request field: " + key}
		}
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(fields["messages"], &messages) != nil || len(messages) == 0 {
		return &requestError{"non-empty messages required"}
	}
	if adapter != AdapterNotion && len(messages) != 1 {
		return &requestError{"this adapter currently requires one user message; history is unsupported"}
	}
	for i, m := range messages {
		for key := range m {
			if key != "role" && key != "content" {
				return &requestError{"unsupported message field: " + key}
			}
		}
		var role string
		_ = json.Unmarshal(m["role"], &role)
		if role != "user" && (adapter != AdapterNotion || role != "assistant") {
			return &requestError{"unsupported embedded message role"}
		}
		if i == len(messages)-1 && role != "user" {
			return &requestError{"last message must be from the user"}
		}
		if text, e := textContent(m["content"]); e != nil || text == "" {
			return &requestError{"nonempty text content required"}
		}
	}
	return nil
}

func (c *Client) credential() (string, error) {
	if c.source.KeyEnv == "" {
		return "", errors.New("embedded provider key_env is required")
	}
	value := strings.TrimSpace(c.source.CredentialValue())
	if value == "" {
		return "", errors.New("source credential environment variable is not set")
	}
	return value, nil
}

func (c *Client) baseURL(defaultBase string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.source.BaseURL), "/")
	if base == "" {
		base = defaultBase
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid embedded provider base_url")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !(u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())) {
			return "", errors.New("embedded provider base_url must use HTTPS outside loopback")
		}
	}
	return base, nil
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	payload := bytes.NewReader(body)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, errors.New("invalid embedded provider request")
	}
	req.ContentLength = int64(len(body))
	req.GetBody = nil
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("embedded provider transport failed")
	}
	return resp, nil
}

func setResponseBody(resp *http.Response, body io.ReadCloser, contentType string) {
	resp.Body = body
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
}

func jsonObject(body []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		if err == nil {
			return errors.New("expected one JSON value")
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func textContent(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return text, nil
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
		return "", errors.New("only text content is supported")
	}
	var b strings.Builder
	for _, part := range parts {
		var kind, value string
		if len(part) != 2 || json.Unmarshal(part["type"], &kind) != nil || kind != "text" || json.Unmarshal(part["text"], &value) != nil {
			return "", errors.New("only text content parts are supported")
		}
		b.WriteString(value)
	}
	return b.String(), nil
}

func textValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			switch item := item.(type) {
			case string:
				parts = append(parts, item)
			case map[string]any:
				if text, ok := item["text"].(string); ok {
					parts = append(parts, text)
				} else if content, ok := item["content"].(string); ok {
					parts = append(parts, content)
				}
			}
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

func parseChatRequest(body []byte) (chatRequest, error) {
	var req chatRequest
	if err := jsonObject(body, &req); err != nil {
		return req, err
	}
	if len(req.Messages) == 0 {
		return req, errors.New("messages is required and must be non-empty")
	}
	return req, nil
}

func messageContent(messages []chatMessage) (string, error) {
	valid := make([]chatMessage, 0, len(messages))
	for _, msg := range messages {
		if len(msg.Content) != 0 && !bytes.Equal(bytes.TrimSpace(msg.Content), []byte("null")) {
			valid = append(valid, msg)
		}
	}
	if len(valid) == 0 {
		return "", errors.New("messages contain no content")
	}
	if len(valid) == 1 {
		return textContent(valid[0].Content)
	}
	labels := map[string]string{"assistant": "Assistant", "system": "System", "user": "User"}
	parts := make([]string, 0, len(valid))
	for _, msg := range valid {
		content, err := textContent(msg.Content)
		if err != nil {
			return "", err
		}
		label := labels[msg.Role]
		if label == "" {
			label = "User"
		}
		parts = append(parts, fmt.Sprintf("[%s]\n%s", label, content))
	}
	return strings.Join(parts, "\n\n"), nil
}

func chatID() string { return "chatcmpl-" + strings.ReplaceAll(newUUID(), "-", "") }

func sseData(value any) []byte {
	b, _ := json.Marshal(value)
	return append([]byte("data: "), append(b, '\n', '\n')...)
}

func chatChunk(id, model string, created int64, delta map[string]any, finish any) []byte {
	return sseData(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	})
}

func chatCompletion(id, model string, content string, created int64) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": id, "object": "chat.completion", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
	})
	return b
}

// transformBody turns provider records into OpenAI SSE records as they are
// read. The callback returns zero or more already encoded SSE records.
type transformBody struct {
	mu       sync.Mutex
	next     func() ([]byte, error)
	closeFn  func() error
	pending  []byte
	produced int
	done     bool
	closeOne sync.Once
}

func (b *transformBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if len(b.pending) > 0 {
			n := copy(dst, b.pending)
			b.pending = b.pending[n:]
			return n, nil
		}
		if b.done {
			return 0, io.EOF
		}
		out, err := b.next()
		if len(out) > (16<<20)-b.produced {
			b.done = true
			_ = b.closeFn()
			return 0, errors.New("provider stream exceeds output limit")
		}
		b.produced += len(out)
		if len(out) > 0 {
			b.pending = out
			if err != nil {
				b.done = true
			}
			continue
		}
		if err != nil {
			b.done = true
			_ = b.closeFn()
			return 0, err
		}
	}
}

func (b *transformBody) Close() error {
	var err error
	b.closeOne.Do(func() { err = b.closeFn() })
	b.mu.Lock()
	b.done = true
	b.mu.Unlock()
	return err
}

type sseDecoder struct {
	reader *bufio.Reader
	event  string
	data   []string
	size   int
}

func boundedLine(r *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		part, err := r.ReadSlice('\n')
		if b.Len()+len(part) > 1<<20 {
			return "", errors.New("upstream line exceeds 1 MiB")
		}
		b.Write(part)
		if err != bufio.ErrBufferFull {
			return b.String(), err
		}
	}
}

func newSSEDecoder(body io.Reader) *sseDecoder {
	return &sseDecoder{reader: bufio.NewReaderSize(body, 32<<10)}
}

func (d *sseDecoder) next() (event, data string, ok bool, err error) {
	for {
		line, readErr := boundedLine(d.reader)
		if len(line) > 0 {
			if !strings.HasSuffix(line, "\n") {
				return "", "", false, io.ErrUnexpectedEOF
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			switch {
			case line == "":
				if len(d.data) > 0 || d.event != "" {
					outEvent, outData := d.event, strings.Join(d.data, "\n")
					d.event, d.data = "", nil
					d.size = 0
					return outEvent, outData, true, nil
				}
				d.event = ""
			case strings.HasPrefix(line, ":"):
				// SSE comment/heartbeat.
			case strings.HasPrefix(line, "event:"):
				d.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				if strings.HasPrefix(value, " ") {
					value = value[1:]
				}
				d.data = append(d.data, value)
				d.size += len(value)
				if d.size > 1<<20 {
					return "", "", false, errors.New("upstream event exceeds 1 MiB")
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if len(d.data) > 0 || d.event != "" {
					return "", "", false, io.ErrUnexpectedEOF
				}
				return "", "", false, nil
			}
			return "", "", false, readErr
		}
	}
}

func newUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is unrecoverable for a request identifier; the
		// timestamp keeps this fallback unique enough for local diagnostics.
		n := time.Now().UnixNano()
		for i := range raw {
			raw[i] = byte(n >> (i % 8 * 8))
		}
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func (c *Client) newStreamBody(source io.ReadCloser, id, model string, parse func(string, string) ([]byte, error)) io.ReadCloser {
	decoder := newSSEDecoder(source)
	created := time.Now().Unix()
	started := false
	ended := false
	return &transformBody{
		next: func() ([]byte, error) {
			if !started {
				started = true
				return chatChunk(id, model, created, map[string]any{"role": "assistant", "content": ""}, nil), nil
			}
			if ended {
				return nil, io.EOF
			}
			for {
				event, data, ok, err := decoder.next()
				if err != nil {
					return nil, err
				}
				if !ok {
					return nil, io.ErrUnexpectedEOF
				}
				out, err := parse(event, data)
				if errors.Is(err, errStreamComplete) {
					ended = true
					out = append(out, chatChunk(id, model, created, map[string]any{}, "stop")...)
					return append(out, sseDone...), nil
				}
				if err != nil {
					return nil, err
				}
				if len(out) > 0 {
					return out, nil
				}
			}
		},
		closeFn: source.Close,
	}
}

func aggregateChatSSE(body []byte) (string, error) {
	decoder := newSSEDecoder(bytes.NewReader(body))
	var content strings.Builder
	for {
		_, data, ok, err := decoder.next()
		if err != nil {
			return "", err
		}
		if !ok {
			return content.String(), nil
		}
		if data == "[DONE]" {
			continue
		}
		var value struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return "", err
		}
		if len(value.Choices) > 0 {
			content.WriteString(value.Choices[0].Delta.Content)
		}
	}
}

func (c *Client) responseFromStream(resp *http.Response, streamBody io.ReadCloser, stream bool, id, model string) (*http.Response, error) {
	if stream {
		setResponseBody(resp, streamBody, "text/event-stream")
		return resp, nil
	}
	data, err := io.ReadAll(io.LimitReader(streamBody, (16<<20)+1))
	_ = streamBody.Close()
	if len(data) > 16<<20 {
		return nil, errors.New("upstream response exceeds 16 MiB")
	}
	if err != nil {
		return nil, err
	}
	content, err := aggregateChatSSE(data)
	if err != nil {
		return nil, err
	}
	completion := chatCompletion(id, model, content, time.Now().Unix())
	resp.Body = io.NopCloser(bytes.NewReader(completion))
	resp.ContentLength = int64(len(completion))
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}
