// Package webnext contains small, native HTTP/WebSocket adapters for the
// long-tail web providers whose wire contracts are pinned in
// .clash-tokens/reference/longtail.  It does not launch a browser, create an
// account, refresh a quota, harvest a cookie, or forward caller credentials.
package webnext

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
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
	AdapterEaseMate = "easemate"
	AdapterFlowith  = "flowith"
	AdapterLangFast = "langfast"
	AdapterLiaoBots = "liaobots"

	maxHeaderValue   = 4096
	maxInputBytes    = 1 << 20
	maxEventBytes    = 1 << 20
	maxResponseBytes = 16 << 20
)

var (
	ErrUnsupported error = unsupportedError("webnext adapter: unsupported protocol or request semantics")
	ErrCredential        = errors.New("webnext adapter: source credential is missing or invalid")
	ErrTruncated         = errors.New("webnext adapter: truncated upstream response")
	ErrUpstream          = errors.New("webnext adapter: upstream request failed")
)

type unsupportedError string

func (e unsupportedError) Error() string { return string(e) }
func (unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

// HTTPError preserves an upstream status for the gateway without copying an
// upstream response body or message into an adapter error.
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("webnext adapter: upstream status %d", e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

// Client implements internal/upstream's native adapter contract.
type Client struct {
	source config.Source
	http   *http.Client
	mu     sync.Mutex
	closed bool
}

// credentials contains only values explicitly supplied through KeyEnv.  The
// fields are intentionally provider-specific; no browser profile or cookie
// store is consulted.
type credentials struct {
	Token           string
	Cookie          string
	DeviceUUID      string
	AuthCode        string
	SocketURL       string
	SupabaseURL     string
	SupabaseAnonKey string
	UserID          string
}

type chatRequest struct {
	Model            string
	Messages         []chatMessage
	Stream           bool
	Temperature      *float64
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	MaxTokens        *int
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type credentialJSON struct {
	Token           string `json:"token"`
	AccessToken     string `json:"access_token"`
	Cookie          string `json:"cookie"`
	DeviceUUID      string `json:"device_uuid"`
	DeviceUUIDAlt   string `json:"deviceUUID"`
	AuthCode        string `json:"authCode"`
	AuthCodeSnake   string `json:"auth_code"`
	SocketURL       string `json:"socket_url"`
	SupabaseURL     string `json:"supabase_url"`
	SupabaseAnonKey string `json:"supabase_anon_key"`
	UserID          string `json:"user_id"`
}

// Supports reports whether this package has a native adapter for adapter.
func Supports(adapter string) bool {
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterFlowith, AdapterLangFast, AdapterLiaoBots:
		return true
	default:
		return false
	}
}

// New creates a client. It performs no I/O and does not read KeyEnv until Do.
func New(source config.Source) *Client {
	maxConn := source.MaxInflight
	if maxConn < 1 {
		maxConn = 1
	}
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           maxConn,
		MaxIdleConnsPerHost:    maxConn,
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

func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}

// Do accepts only independent Chat Completions requests. The providers' private
// protocols are translated to bounded OpenAI Chat Completions output at this
// boundary; they expose no conversation handle for X-COT-Session continuity.
func (c *Client) Do(ctx context.Context, protocolName, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if ctx == nil {
		return nil, errors.New("webnext adapter: nil request context")
	}
	if protocolName != "chat" {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > maxInputBytes {
		return nil, fmt.Errorf("%w: request body exceeds limit", ErrUnsupported)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("%w: model is required", ErrUnsupported)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("webnext adapter: client is closed")
	}
	adapter := strings.ToLower(strings.TrimSpace(c.source.Adapter))
	if !Supports(adapter) {
		return nil, ErrUnsupported
	}
	if strings.TrimSpace(clientHeaders.Get("X-COT-Session")) != "" {
		return nil, fmt.Errorf("%w: stateless adapters reject X-COT-Session", ErrUnsupported)
	}
	req, err := parseChatRequest(body, adapter, model, stream)
	if err != nil {
		return nil, err
	}
	cred, err := c.credential()
	if err != nil {
		return nil, err
	}
	var resp *http.Response
	switch adapter {
	case AdapterFlowith:
		resp, err = c.doFlowith(ctx, cred, req)
	case AdapterLangFast:
		resp, err = c.doLangFast(ctx, cred, req)
	case AdapterLiaoBots:
		resp, err = c.doLiaoBots(ctx, cred, req)
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, errors.New("webnext adapter: empty upstream response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if stream {
		// These providers do not expose conversation handles. Every request is
		// independent, so no session gate is retained across response reads.
	}
	return resp, nil
}

func (c *Client) credential() (credentials, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return credentials{}, ErrCredential
	}
	raw := strings.TrimSpace(c.source.CredentialValue())
	if raw == "" || len(raw) > maxResponseBytes {
		return credentials{}, ErrCredential
	}
	var value credentialJSON
	if strings.HasPrefix(raw, "{") {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&value) != nil {
			return credentials{}, ErrCredential
		}
		// Decode once more so a credential cannot hide trailing JSON or
		// arbitrary bytes after an otherwise valid object.
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return credentials{}, ErrCredential
		}
	}
	cred := credentials{
		Token:           value.Token,
		Cookie:          value.Cookie,
		DeviceUUID:      value.DeviceUUID,
		AuthCode:        value.AuthCode,
		SocketURL:       value.SocketURL,
		SupabaseURL:     value.SupabaseURL,
		SupabaseAnonKey: value.SupabaseAnonKey,
		UserID:          value.UserID,
	}
	if cred.Token == "" {
		cred.Token = value.AccessToken
	}
	if cred.DeviceUUID == "" {
		cred.DeviceUUID = value.DeviceUUIDAlt
	}
	if cred.AuthCode == "" {
		cred.AuthCode = value.AuthCodeSnake
	}
	if !strings.HasPrefix(raw, "{") {
		switch strings.ToLower(strings.TrimSpace(c.source.Adapter)) {
		case AdapterEaseMate:
			cred.DeviceUUID = raw
		case AdapterFlowith:
			cred.Cookie = raw
		default:
			cred.Token = raw
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.source.Adapter)) {
	case AdapterEaseMate:
		if cred.DeviceUUID == "" || !validHeaderValue(cred.DeviceUUID) || (cred.Cookie != "" && !validHeaderValue(cred.Cookie)) {
			return credentials{}, ErrCredential
		}
	case AdapterFlowith:
		if cred.Cookie != "" && !validHeaderValue(cred.Cookie) {
			return credentials{}, ErrCredential
		}
		if (cred.SupabaseURL == "") != (cred.SupabaseAnonKey == "") || (cred.SupabaseAnonKey != "" && !validHeaderValue(cred.SupabaseAnonKey)) {
			return credentials{}, ErrCredential
		}
	case AdapterLangFast:
		if cred.Token == "" || !validHeaderValue(cred.Token) || cred.SupabaseAnonKey == "" || !validHeaderValue(cred.SupabaseAnonKey) || (cred.SocketURL != "" && !validHeaderValue(cred.SocketURL)) {
			return credentials{}, ErrCredential
		}
	case AdapterLiaoBots:
		// LiaoBots deliberately requires both values in an explicit JSON object.
		// A plain cookie or token must never trigger the reference's authcode
		// refresh path.
		if !strings.HasPrefix(raw, "{") || cred.AuthCode == "" || cred.Cookie == "" || !validHeaderValue(cred.AuthCode) || !validHeaderValue(cred.Cookie) {
			return credentials{}, ErrCredential
		}
	}
	return cred, nil
}

func validHeaderValue(value string) bool {
	return len(value) > 0 && len(value) <= maxHeaderValue && !strings.ContainsAny(value, "\r\n")
}

func parseChatRequest(body []byte, adapter, model string, stream bool) (chatRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return chatRequest{}, fmt.Errorf("%w: a JSON object is required", ErrUnsupported)
	}
	adapter = strings.ToLower(strings.TrimSpace(adapter))
	allowed := map[string]bool{"model": true, "messages": true, "stream": true}
	if adapter == AdapterLangFast {
		for _, key := range []string{"temperature", "top_p", "frequency_penalty", "presence_penalty", "max_tokens"} {
			allowed[key] = true
		}
	}
	for key := range fields {
		if !allowed[key] {
			return chatRequest{}, fmt.Errorf("%w: unsupported request field %q", ErrUnsupported, key)
		}
	}
	var raw struct {
		Model            string        `json:"model"`
		Messages         []chatMessage `json:"messages"`
		Stream           *bool         `json:"stream"`
		Temperature      *float64      `json:"temperature"`
		TopP             *float64      `json:"top_p"`
		FrequencyPenalty *float64      `json:"frequency_penalty"`
		PresencePenalty  *float64      `json:"presence_penalty"`
		MaxTokens        *int          `json:"max_tokens"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil || len(raw.Messages) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages must be a non-empty text array", ErrUnsupported)
	}
	if raw.Model != "" && strings.TrimSpace(raw.Model) != model {
		return chatRequest{}, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
	}
	for _, msg := range raw.Messages {
		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
			return chatRequest{}, fmt.Errorf("%w: unsupported message role", ErrUnsupported)
		}
		if msg.Content == "" {
			return chatRequest{}, fmt.Errorf("%w: message content must be non-empty text", ErrUnsupported)
		}
	}
	if raw.Messages[len(raw.Messages)-1].Role != "user" {
		return chatRequest{}, fmt.Errorf("%w: the last message must be a user message", ErrUnsupported)
	}
	if adapter == AdapterEaseMate && (len(raw.Messages) != 1 || raw.Messages[0].Role != "user") {
		return chatRequest{}, fmt.Errorf("%w: EaseMate supports one user turn", ErrUnsupported)
	}
	if raw.MaxTokens != nil && *raw.MaxTokens < 1 {
		return chatRequest{}, fmt.Errorf("%w: max_tokens must be positive", ErrUnsupported)
	}
	if raw.Temperature != nil && (*raw.Temperature < 0 || *raw.Temperature > 2) {
		return chatRequest{}, fmt.Errorf("%w: temperature is out of range", ErrUnsupported)
	}
	if raw.TopP != nil && (*raw.TopP < 0 || *raw.TopP > 1) {
		return chatRequest{}, fmt.Errorf("%w: top_p is out of range", ErrUnsupported)
	}
	if raw.Stream != nil && *raw.Stream != stream {
		return chatRequest{}, fmt.Errorf("%w: stream flag conflicts with gateway selection", ErrUnsupported)
	}
	return chatRequest{Model: model, Messages: raw.Messages, Stream: stream, Temperature: raw.Temperature, TopP: raw.TopP, FrequencyPenalty: raw.FrequencyPenalty, PresencePenalty: raw.PresencePenalty, MaxTokens: raw.MaxTokens}, nil
}

func baseURL(source config.Source, fallback string) (string, error) {
	value := strings.TrimSpace(source.BaseURL)
	if value == "" {
		value = fallback
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("webnext adapter: invalid base_url")
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("webnext adapter: base_url must use HTTP(S)")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "", errors.New("webnext adapter: base_url must use HTTPS outside loopback")
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

func postJSON(ctx context.Context, hc *http.Client, endpoint string, payload any, headers http.Header) (*http.Response, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("webnext adapter: cannot encode upstream request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("webnext adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(data))
	req.Header = headers.Clone()
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("webnext adapter: upstream transport failed")
	}
	return resp, nil
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:])
}
