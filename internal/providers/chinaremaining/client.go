// Package chinaremaining contains source-driven adapters for Chinese web
// contracts. These are private web endpoints, not official APIs;
// callers must provide their own session credentials through Source.KeyEnv.
// The package is intentionally standalone and is not part of the built-in
// provider registry.
package chinaremaining

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/protocol"
	"clash-of-tokens/internal/providerutil"
)

const (
	AdapterEmohaa = "emohaa"
	AdapterSpark  = "spark-web"
	AdapterQwenCN = "qwen-web-cn"
	AdapterMetaso = "metaso"

	defaultEmohaaBase = "https://ai-role.cn"
	defaultSparkBase  = "https://xinghuo.xfyun.cn"
	defaultQwenBase   = "https://api.tongyi.com"
	defaultMetasoBase = "https://metaso.cn"
	emohaaOrigin      = "https://echo.turing-world.com"
	sparkOrigin       = "https://xinghuo.xfyun.cn"

	maxEventBytes      = 16 << 20
	maxResponseBytes   = 64 << 20
	maxCredentialBytes = 64 << 10
)

var (
	ErrUnsupported = unsupportedError{}
	ErrCredential  = errors.New("china remaining web adapter: source credential is missing or invalid")
	ErrTruncated   = errors.New("china remaining web adapter: truncated upstream response")
	// ErrUpstreamEvent indicates that the source reported an application-level
	// failure inside an otherwise successful HTTP/SSE response.  The source's
	// error text is deliberately not copied into the assistant message.
	ErrUpstreamEvent = errors.New("china remaining web adapter: upstream reported an error event")
	// ErrRevision is returned for a cumulative Qwen snapshot that cannot be
	// represented as a forward-only streaming delta.
	ErrRevision = errors.New("china remaining web adapter: upstream cumulative text was revised")
)

type unsupportedError struct{}

func (unsupportedError) Error() string {
	return "china remaining web adapter: unsupported protocol or request semantics"
}
func (unsupportedError) HTTPStatus() int { return http.StatusUnprocessableEntity }

type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("china remaining web adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("china remaining web adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type Client struct {
	source  config.Source
	http    *http.Client
	browser config.Browser
	gate    providerutil.Gate
	mu      sync.Mutex
	closed  bool
}

type credential struct {
	SSOSessionID string `json:"sso_session_id"`
	GtToken      string `json:"gt_token"`
}

// The value is the fixed webpage field present in the pinned Spark source.
// It is not generated or refreshed by this adapter. A caller may provide a
// current value explicitly in the KeyEnv JSON credential when the webpage
// rotates it.

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
	return &Client{source: source, browser: b, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
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
	switch strings.ToLower(strings.TrimSpace(adapter)) {
	case AdapterEmohaa, AdapterSpark, AdapterQwenCN, AdapterMetaso:
		return true
	default:
		return false
	}
}

type chatRequest struct {
	Messages []chatMessage `json:"messages"`
}
type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("china remaining web adapter: nil client")
	}
	if ctx == nil {
		return nil, errors.New("china remaining web adapter: nil request context")
	}
	if proto != "chat" || strings.TrimSpace(model) == "" {
		return nil, ErrUnsupported
	}
	if !supportedModel(c.source.Adapter, model) {
		return nil, fmt.Errorf("%w: model %q is not supported by adapter %q", ErrUnsupported, model, c.source.Adapter)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("china remaining web adapter: client is closed")
	}
	if !Supports(c.source.Adapter) {
		return nil, fmt.Errorf("%w: adapter %q", ErrUnsupported, c.source.Adapter)
	}
	if clientHeaders != nil && strings.TrimSpace(clientHeaders.Get("X-COT-Session")) != "" {
		return nil, fmt.Errorf("%w: web source creates a temporary conversation per turn", ErrUnsupported)
	}
	req, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	if c.source.Adapter == AdapterMetaso && (len(req.Messages) != 1 || req.Messages[0].Role != "user") {
		return nil, fmt.Errorf("%w: Metaso source accepts one user message per temporary search", ErrUnsupported)
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
	var cred credential
	if c.source.Adapter == AdapterEmohaa || c.source.Adapter == AdapterSpark {
		cred, err = c.credential()
		if err != nil {
			return nil, err
		}
	}
	var upstream *http.Response
	switch c.source.Adapter {
	case AdapterEmohaa:
		upstream, err = c.doEmohaa(ctx, cred.SSOSessionID, req)
	case AdapterSpark:
		upstream, err = c.doSpark(ctx, cred, req)
	case AdapterQwenCN:
		upstream, err = c.doQwenCN(ctx, model, req)
	case AdapterMetaso:
		upstream, err = c.doMetaso(ctx, model, req)
	default:
		err = fmt.Errorf("%w: adapter %q", ErrUnsupported, c.source.Adapter)
	}
	if err != nil {
		return nil, err
	}
	if upstream == nil {
		return nil, errors.New("china remaining web adapter: empty upstream response")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	converted, err := c.convert(ctx, upstream, model, c.source.Adapter, stream)
	if err != nil {
		return nil, err
	}
	if stream {
		locked = false
		converted.Body = &releaseBody{ReadCloser: converted.Body, release: c.gate.Unlock}
	}
	return converted, nil
}

func supportedModel(adapter, model string) bool {
	switch adapter {
	case AdapterEmohaa:
		return model == AdapterEmohaa
	case AdapterSpark:
		return model == "spark"
	case AdapterMetaso:
		mode, suffix := metasoMode(model)
		return (mode == "concise" || mode == "detail" || mode == "research") && (suffix == "" || suffix == "scholar")
	default:
		return strings.TrimSpace(model) != ""
	}
}

func (c *Client) credential() (credential, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return credential{}, ErrCredential
	}
	raw := c.source.CredentialValue()
	if len(raw) == 0 || len(raw) > maxCredentialBytes || strings.ContainsAny(raw, "\r\n\x00") {
		return credential{}, ErrCredential
	}
	raw = strings.TrimSpace(raw)
	var cred credential
	if strings.HasPrefix(raw, "{") {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cred); err != nil {
			return credential{}, ErrCredential
		}
		var trailing any
		if dec.Decode(&trailing) != io.EOF {
			return credential{}, ErrCredential
		}
	} else {
		if c.source.Adapter == AdapterEmohaa {
			// The source route accepts a comma-separated token pool. Keep the
			// first caller-supplied token deterministic in this standalone client.
			if comma := strings.IndexByte(raw, ','); comma >= 0 {
				raw = strings.TrimSpace(raw[:comma])
			}
		}
		if len(raw) >= len("Bearer ") && strings.EqualFold(raw[:len("Bearer ")], "Bearer ") {
			raw = strings.TrimSpace(raw[len("Bearer "):])
		}
		cred.SSOSessionID = raw
	}
	if strings.TrimSpace(cred.SSOSessionID) == "" || strings.ContainsAny(cred.SSOSessionID, ";\r\n\x00") || len(cred.SSOSessionID) > 4096 {
		return credential{}, ErrCredential
	}
	if c.source.Adapter == AdapterSpark && strings.TrimSpace(cred.GtToken) == "" {
		return credential{}, ErrCredential
	}
	if len(cred.GtToken) > maxCredentialBytes || strings.ContainsAny(cred.GtToken, "\r\n\x00") {
		return credential{}, ErrCredential
	}
	return cred, nil
}

func decodeChatRequest(body []byte) (chatRequest, error) {
	var req chatRequest
	if len(body) == 0 || len(body) > maxResponseBytes {
		return req, fmt.Errorf("%w: invalid request body", ErrUnsupported)
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&fields); err != nil || fields == nil {
		return req, fmt.Errorf("%w: request must be a JSON object", ErrUnsupported)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return req, fmt.Errorf("%w: request must contain one JSON value", ErrUnsupported)
	}
	for field := range fields {
		if field != "model" && field != "messages" && field != "stream" {
			return req, fmt.Errorf("%w: unsupported field %q", ErrUnsupported, field)
		}
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
		return req, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(fields["messages"], &rawMessages); err != nil || len(rawMessages) == 0 {
		return req, fmt.Errorf("%w: messages must be a non-empty array", ErrUnsupported)
	}
	for _, raw := range rawMessages {
		var m chatMessage
		md := json.NewDecoder(bytes.NewReader(raw))
		md.DisallowUnknownFields()
		if err := md.Decode(&m); err != nil || strings.TrimSpace(m.Role) == "" {
			return req, fmt.Errorf("%w: each message needs a role and text content", ErrUnsupported)
		}
		if md.Decode(&trailing) != io.EOF {
			return req, fmt.Errorf("%w: each message must be one object", ErrUnsupported)
		}
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return req, fmt.Errorf("%w: message role %q is unsupported", ErrUnsupported, m.Role)
		}
		if _, err := textContent(m.Content); err != nil {
			return req, err
		}
	}
	return req, nil
}

func textContent(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("%w: message content is required", ErrUnsupported)
		}
		return s, nil
	}
	return "", fmt.Errorf("%w: only non-empty text content is supported", ErrUnsupported)
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("china remaining web adapter: invalid base_url")
	}
	if !strings.EqualFold(u.Scheme, "https") && !(strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname())) {
		return "", errors.New("china remaining web adapter: invalid base_url")
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

func (c *Client) doEmohaa(ctx context.Context, token string, req chatRequest) (*http.Response, error) {
	base, err := baseURL(c.source, defaultEmohaaBase)
	if err != nil {
		return nil, err
	}
	conv, err := c.createEmohaaConversation(ctx, base, token)
	if err != nil {
		return nil, err
	}
	params := xssParams()
	values := url.Values{"token": {token}, "cid": {conv}, "prompt": {prepareEmohaa(req.Messages)}, "role": {"echo"}, "xts": {params.ts}, "xid": {params.id}, "xreal": {params.real}}
	endpoint := base + "/echo-prod/chat?" + values.Encode()
	resp, err := c.do(ctx, http.MethodGet, endpoint, nil, emohaaHeaders(params, token, false))
	if err != nil {
		_ = c.deleteEmohaaConversation(context.Background(), base, token, conv)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = c.deleteEmohaaConversation(context.Background(), base, token, conv)
		return resp, nil
	}
	resp.Body = &cleanupBody{ReadCloser: resp.Body, cleanup: func() { _ = c.deleteEmohaaConversation(context.Background(), base, token, conv) }}
	return resp, nil
}

func (c *Client) createEmohaaConversation(ctx context.Context, base, token string) (string, error) {
	p := xssParams()
	resp, err := c.do(ctx, http.MethodGet, base+"/echo-prod/generate/id?create=true", nil, emohaaHeaders(p, token, false))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", readHTTPError(resp)
	}
	return parseID(resp.Body, "emohaa")
}

func (c *Client) deleteEmohaaConversation(ctx context.Context, base, token, conv string) error {
	p := xssParams()
	resp, err := c.do(ctx, http.MethodDelete, base+"/echo-prod/conv?cid="+url.QueryEscape(conv), nil, emohaaHeaders(p, token, false))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readHTTPError(resp)
	}
	return nil
}

type xss struct{ id, ts, real string }

func xssParams() xss {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		binary.BigEndian.PutUint64(random[:], uint64(time.Now().UnixNano()))
	}
	id := fmt.Sprintf("0.%016d", binary.BigEndian.Uint64(random[:])%10000000000000000)
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	sum := md5.Sum([]byte(ts + "-_-" + id))
	return xss{id: id, ts: ts, real: fmt.Sprintf("%x", sum)}
}

func emohaaHeaders(p xss, token string, stream bool) http.Header {
	h := http.Header{"Accept": {"application/json, text/plain, */*"}, "Accept-Encoding": {"gzip, deflate, br, zstd"}, "Accept-Language": {"zh-CN,zh;q=0.9,en;q=0.8"}, "Origin": {emohaaOrigin}, "Referer": {emohaaOrigin + "/"}, "Sec-Ch-Ua": {`"Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"`}, "Sec-Ch-Ua-Mobile": {"?0"}, "Sec-Ch-Ua-Platform": {`"Windows"`}, "Sec-Fetch-Dest": {"empty"}, "Sec-Fetch-Mode": {"cors"}, "Sec-Fetch-Site": {"same-origin"}, "User-Agent": {browserUA}}
	h.Set("Authorization", "Bearer "+token)
	h.Set("X-Xss-Id", p.id)
	h.Set("X-Xss-Ts", p.ts)
	h.Set("X-Xss-Real", p.real)
	if stream {
		h.Set("Accept", "text/event-stream")
	}
	return h
}

func (c *Client) doSpark(ctx context.Context, cred credential, req chatRequest) (*http.Response, error) {
	base, err := baseURL(c.source, defaultSparkBase)
	if err != nil {
		return nil, err
	}
	conv, err := c.createSparkConversation(ctx, base, cred.SSOSessionID)
	if err != nil {
		return nil, err
	}
	body, contentType, err := sparkMultipart(req.Messages, conv, cred.GtToken)
	if err != nil {
		_ = c.deleteSparkConversation(context.Background(), base, cred.SSOSessionID, conv)
		return nil, err
	}
	h := sparkHeaders(cred.SSOSessionID)
	h.Set("Content-Type", contentType)
	h.Set("Accept", "text/event-stream")
	h.Set("Botweb", "0")
	h.Set("Challenge", "undefined")
	h.Set("Seccode", "")
	h.Set("Validate", "undefined")
	resp, err := c.do(ctx, http.MethodPost, base+"/iflygpt-chat/u/chat_message/chat", body, h)
	if err != nil {
		_ = c.deleteSparkConversation(context.Background(), base, cred.SSOSessionID, conv)
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = c.deleteSparkConversation(context.Background(), base, cred.SSOSessionID, conv)
		return resp, nil
	}
	resp.Body = &cleanupBody{ReadCloser: resp.Body, cleanup: func() { _ = c.deleteSparkConversation(context.Background(), base, cred.SSOSessionID, conv) }}
	return resp, nil
}

func (c *Client) createSparkConversation(ctx context.Context, base, sso string) (string, error) {
	h := sparkHeaders(sso)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	resp, err := c.do(ctx, http.MethodPost, base+"/iflygpt/u/chat-list/v1/create-chat-list", []byte("{}"), h)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", readHTTPError(resp)
	}
	return parseID(resp.Body, "spark")
}

func (c *Client) deleteSparkConversation(ctx context.Context, base, sso, conv string) error {
	h := sparkHeaders(sso)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	p, _ := json.Marshal(map[string]string{"chatListId": conv})
	resp, err := c.do(ctx, http.MethodPost, base+"/iflygpt/u/chat-list/v1/del-chat-list", p, h)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readHTTPError(resp)
	}
	return nil
}

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

func sparkHeaders(sso string) http.Header {
	h := http.Header{"Accept": {"application/json, text/plain, */*"}, "Accept-Encoding": {"identity"}, "Accept-Language": {"zh-CN,zh;q=0.9"}, "Cache-Control": {"no-cache"}, "Origin": {sparkOrigin}, "Pragma": {"no-cache"}, "Referer": {sparkOrigin + "/"}, "Sec-Ch-Ua": {`"Chromium";v="122", "Not(A:Brand";v="24", "Google Chrome";v="122"`}, "Sec-Ch-Ua-Mobile": {"?0"}, "Sec-Ch-Ua-Platform": {`"Windows"`}, "Sec-Fetch-Dest": {"empty"}, "Sec-Fetch-Mode": {"cors"}, "Sec-Fetch-Site": {"same-site"}, "User-Agent": {browserUA}}
	h.Set("Clienttype", "1")
	h.Set("Lang-Code", "zh")
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("Cookie", sparkCookie(sso))
	return h
}

func sparkCookie(sso string) string {
	return strings.Join([]string{"di_c_mti=" + uuid(), "d_d_app_ver=1.4.0", "daas_st=" + url.QueryEscape(`{"sdk_ver":"1.3.9","status":"0"}`), "appid=150b4dfebe", "d_d_ci=" + uuid(), "ssoSessionId=" + sso}, "; ")
}

func sparkMultipart(messages []chatMessage, conv, gt string) ([]byte, string, error) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fields := [][2]string{{"fd", randomDigits(6)}, {"isBot", "0"}, {"clientType", "1"}, {"text", prepareSpark(messages)}, {"chatId", conv}, {"GtToken", gt}}
	for _, field := range fields {
		if err := mw.WriteField(field[0], field[1]); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return b.Bytes(), mw.FormDataContentType(), nil
}

func prepareEmohaa(messages []chatMessage) string {
	var b strings.Builder
	for _, m := range messages {
		text, _ := textContent(m.Content)
		b.WriteString(m.Role)
		b.WriteByte(':')
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return strings.Replace(b.String(), "assistant", "emohaa", 1)
}
func prepareSpark(messages []chatMessage) string {
	var b strings.Builder
	for i, m := range messages {
		if len(messages) > 2 && i == len(messages)-1 {
			b.WriteString("system:\u5173\u6ce8\u7528\u6237\u6700\u65b0\u7684\u6d88\u606f\n")
		}
		text, _ := textContent(m.Content)
		b.WriteString(m.Role)
		b.WriteByte(':')
		b.WriteString(text)
		b.WriteByte('\n')
	}
	b.WriteString("assistant:")
	return b.String()
}

func (c *Client) do(ctx context.Context, method, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("china remaining web adapter: invalid upstream request")
	}
	req.Header = headers
	if body != nil {
		req.ContentLength = int64(len(body))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("china remaining web adapter: upstream transport failed")
	}
	if resp.Body == nil {
		return nil, errors.New("china remaining web adapter: upstream response has no body")
	}
	return resp, nil
}

func parseID(body io.Reader, source string) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 1<<20 {
		return "", fmt.Errorf("%w: %s conversation response exceeds byte limit", ErrTruncated, source)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: %s returned an empty conversation id", ErrTruncated, source)
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", fmt.Errorf("%w: %s conversation response is not JSON", ErrUnsupported, source)
	}
	if obj, ok := value.(map[string]any); ok {
		if status, ok := obj["status"].(float64); ok && status >= 400 {
			return "", &HTTPError{Status: int(status)}
		}
		if code, ok := obj["code"].(float64); ok && code != 0 {
			return "", &HTTPError{Status: http.StatusBadGateway}
		}
		if code, ok := obj["errCode"].(float64); ok && code != 0 {
			return "", &HTTPError{Status: http.StatusBadGateway}
		}
	}
	if source == "emohaa" {
		if s, ok := value.(string); ok {
			return validConversationID(s, source)
		}
	}
	if id := findID(value); id != "" {
		return validConversationID(id, source)
	}
	return "", fmt.Errorf("%w: %s conversation id missing", ErrUnsupported, source)
}

func validConversationID(id, source string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n<>/\\\"'") {
		return "", fmt.Errorf("%w: invalid %s conversation id", ErrUnsupported, source)
	}
	for _, r := range id {
		if r < 0x20 || r > 0x7e {
			return "", fmt.Errorf("%w: invalid %s conversation id", ErrUnsupported, source)
		}
	}
	return id, nil
}
func findID(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%g", x)
	case map[string]any:
		for _, key := range []string{"id", "chatListId", "chat_id", "conversationId"} {
			if s, ok := x[key].(string); ok && s != "" {
				return s
			}
		}
		for _, key := range []string{"data", "biz_data"} {
			if nested, ok := x[key]; ok {
				if s := findID(nested); s != "" {
					return s
				}
			}
		}
	}
	return ""
}
func readHTTPError(resp *http.Response) error {
	_, _ = io.CopyN(io.Discard, resp.Body, 4097)
	return &HTTPError{Status: resp.StatusCode}
}

type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releaseBody) done() { b.once.Do(b.release) }
func (b *releaseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.done()
	}
	return n, err
}
func (b *releaseBody) Close() error { err := b.ReadCloser.Close(); b.done(); return err }

type cleanupBody struct {
	io.ReadCloser
	once    sync.Once
	cleanup func()
}

func (b *cleanupBody) done() { b.once.Do(b.cleanup) }
func (b *cleanupBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.done()
	}
	return n, err
}
func (b *cleanupBody) Close() error { err := b.ReadCloser.Close(); b.done(); return err }

func (c *Client) convert(ctx context.Context, upstream *http.Response, model, adapter string, stream bool) (*http.Response, error) {
	if !stream {
		var out bytes.Buffer
		err := decodeUpstream(ctx, upstream.Body, model, adapter, &out, false)
		_ = upstream.Body.Close()
		if err != nil {
			return nil, err
		}
		upstream.StatusCode = http.StatusOK
		upstream.Status = "200 OK"
		upstream.Header = cloneHeaders(upstream.Header)
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("Content-Length", fmt.Sprintf("%d", out.Len()))
		upstream.Body = io.NopCloser(bytes.NewReader(out.Bytes()))
		return upstream, nil
	}
	reader, writer := io.Pipe()
	upstream.Header = cloneHeaders(upstream.Header)
	upstream.Header.Set("Content-Type", "text/event-stream")
	upstream.Header.Del("Content-Length")
	go func() {
		defer upstream.Body.Close()
		if err := decodeUpstream(ctx, upstream.Body, model, adapter, writer, true); err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
	}()
	upstream.Body = reader
	return upstream, nil
}

func decodeUpstream(ctx context.Context, body io.Reader, model, adapter string, sink io.Writer, stream bool) error {
	reader := protocol.NewSSEReader(body, maxEventBytes)
	id := "china-remaining-" + uuid()
	created := time.Now().Unix()
	var content strings.Builder
	var contentBytes int
	var convertedBytes int
	done := false
	emittedRole := false
	writeConverted := func(b []byte) error {
		if len(b) > maxResponseBytes-convertedBytes {
			return errors.New("china remaining web adapter: converted stream exceeds byte limit")
		}
		convertedBytes += len(b)
		n, err := sink.Write(b)
		if err != nil {
			return err
		}
		if n != len(b) {
			return io.ErrShortWrite
		}
		return nil
	}
	emit := func(delta map[string]any) error {
		if !emittedRole {
			delta["role"] = "assistant"
			emittedRole = true
		}
		value := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
		b, _ := json.Marshal(value)
		frame := make([]byte, 0, len(b)+len("data: \n\n"))
		frame = append(frame, "data: "...)
		frame = append(frame, b...)
		frame = append(frame, '\n', '\n')
		return writeConverted(frame)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := reader.Next()
		if err == io.EOF {
			// Metaso's browser/direct implementations can close a complete
			// response without an explicit [DONE]. Treat clean EOF as a
			// terminal event only when real answer text was received. Other
			// sources require their documented terminal marker.
			if done || (adapter == AdapterMetaso && contentBytes > 0) {
				done = true
				break
			}
			return ErrTruncated
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrTruncated, err)
		}
		data := protocol.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		control := bytes.TrimSpace(data)
		if bytes.Equal(control, []byte("[DONE]")) || bytes.Equal(control, []byte("<end>")) {
			done = true
			break
		}
		if adapter == AdapterSpark && bytes.HasSuffix(control, []byte("<sid>")) {
			continue
		}
		decoded := data
		if adapter == AdapterSpark {
			var decodeErr error
			decoded, decodeErr = base64.StdEncoding.DecodeString(string(control))
			if decodeErr != nil {
				return fmt.Errorf("%w: malformed Spark base64 event", ErrTruncated)
			}
		}
		text, ok, reset, decodeErr := sourceEventText(adapter, decoded, content.String())
		if decodeErr != nil {
			return decodeErr
		}
		if !ok {
			continue
		}
		if reset && stream {
			// A client cannot retract text already sent downstream. Fail the
			// stream instead of emitting the revised full snapshot as a
			// duplicate answer.
			return ErrRevision
		}
		if reset {
			content.Reset()
			contentBytes = 0
		}
		if text == "" {
			continue
		}
		if len(text) > maxResponseBytes-contentBytes {
			return errors.New("china remaining web adapter: upstream content exceeds byte limit")
		}
		content.WriteString(text)
		contentBytes += len(text)
		if stream {
			if err := emit(map[string]any{"content": text}); err != nil {
				return err
			}
		}
	}
	if !done {
		return ErrTruncated
	}
	if contentBytes == 0 {
		return ErrTruncated
	}
	if stream {
		if !emittedRole {
			if err := emit(map[string]any{}); err != nil {
				return err
			}
		}
		finish := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
		b, _ := json.Marshal(finish)
		terminal := make([]byte, 0, len(b)+len("data: \n\ndata: [DONE]\n\n"))
		terminal = append(terminal, "data: "...)
		terminal = append(terminal, b...)
		terminal = append(terminal, '\n', '\n')
		terminal = append(terminal, "data: [DONE]\n\n"...)
		if err := writeConverted(terminal); err != nil {
			return err
		}
		return nil
	}
	result := map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content.String()}, "finish_reason": "stop"}}}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if len(b) > maxResponseBytes {
		return errors.New("china remaining web adapter: converted response exceeds byte limit")
	}
	_, err = sink.Write(b)
	return err
}

// sourceEventText converts the two cumulative/event JSON formats used by the
// newer Chinese web sources into a text delta.  `reset` is reported when a
// Qwen cumulative snapshot is not a prefix of the prior snapshot.
func sourceEventText(adapter string, data []byte, previous string) (text string, ok, reset bool, err error) {
	switch adapter {
	case AdapterQwenCN:
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil || raw == nil {
			return "", false, false, fmt.Errorf("%w: malformed Qwen event", ErrTruncated)
		}
		// Qwen can send application errors/status objects with HTTP 200. Do
		// not silently turn those frames into a successful empty completion.
		for _, key := range []string{"error", "errors", "status", "errCode", "errMsg"} {
			if _, present := raw[key]; present {
				return "", false, false, ErrUpstreamEvent
			}
		}
		if code, present := raw["code"]; present {
			var numeric float64
			if json.Unmarshal(code, &numeric) == nil && numeric != 0 {
				return "", false, false, ErrUpstreamEvent
			}
		}
		contentsRaw, present := raw["contents"]
		if !present {
			return "", false, false, fmt.Errorf("%w: Qwen event has no contents", ErrUpstreamEvent)
		}
		var event struct {
			Contents []struct {
				Content     string          `json:"content"`
				ContentType string          `json:"contentType"`
				Status      json.RawMessage `json:"status"`
				Error       json.RawMessage `json:"error"`
			} `json:"contents"`
		}
		if json.Unmarshal(contentsRaw, &event.Contents) != nil {
			return "", false, false, fmt.Errorf("%w: malformed Qwen contents", ErrTruncated)
		}
		for i := len(event.Contents) - 1; i >= 0; i-- {
			if len(event.Contents[i].Status) != 0 || len(event.Contents[i].Error) != 0 || strings.EqualFold(event.Contents[i].ContentType, "status") || strings.EqualFold(event.Contents[i].ContentType, "error") {
				return "", false, false, ErrUpstreamEvent
			}
			if event.Contents[i].ContentType != "text" {
				continue
			}
			full := event.Contents[i].Content
			if strings.HasPrefix(full, previous) {
				return full[len(previous):], true, false, nil
			}
			return full, true, true, nil
		}
		return "", false, false, nil
	case AdapterMetaso:
		var event struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Code string `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(data, &event) != nil {
			return "", false, false, fmt.Errorf("%w: malformed Metaso event", ErrTruncated)
		}
		switch event.Type {
		case "append-text":
			return removeMetasoIndexLabels(event.Text), true, false, nil
		case "error":
			return "", false, false, ErrUpstreamEvent
		default:
			return "", false, false, nil
		}
	default:
		return string(data), true, false, nil
	}
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
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		n := time.Now().UnixNano()
		binary.LittleEndian.PutUint64(b[:8], uint64(n))
		binary.LittleEndian.PutUint64(b[8:], uint64(n>>7))
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", binary.BigEndian.Uint32(b[:4]), binary.BigEndian.Uint16(b[4:6]), binary.BigEndian.Uint16(b[6:8]), binary.BigEndian.Uint16(b[8:10]), b[10:])
}
func randomDigits(n int) string {
	b := make([]byte, n)
	for i := range b {
		var one [1]byte
		if _, err := rand.Read(one[:]); err != nil {
			b[i] = byte('0' + time.Now().Nanosecond()%10)
		} else {
			b[i] = '0' + one[0]%10
		}
	}
	return string(b)
}
