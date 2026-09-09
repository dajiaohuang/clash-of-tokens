// Package majorweb contains small, source-backed clients for web products
// which expose a private chat endpoint rather than a public model API.
//
// The clients deliberately accept one user text message per request.  Web
// products in this package create a fresh temporary turn, so silently
// discarding history, roles, tools, or attachments would change the request's
// meaning.  Those inputs are rejected instead.
package majorweb

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
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
	AdapterClaude       = "claude-web"
	AdapterGrokWeb      = "grok-web"
	AdapterGrokConsole  = "grok-console"
	AdapterGrokBuild    = "grok-build"
	AdapterGenspark     = "genspark-web"
	AdapterGensparkChat = "genspark"
	AdapterGeminiWeb    = "gemini-web"
	AdapterGigaChatWeb  = "gigachat-web"

	defaultClaudeBase      = "https://claude.ai"
	defaultGrokBase        = "https://grok.com"
	defaultGrokBuildBase   = "https://cli-chat-proxy.grok.com/v1"
	defaultGrokConsoleBase = "https://console.x.ai"
	defaultGensparkBase    = "https://www.genspark.ai"
	defaultGeminiBase      = "https://gemini.google.com"
	maxEventBytes          = 16 << 20
	maxResponseBytes       = 64 << 20
)

// Aliases are kept exported so config/gateway registration can avoid copying
// string literals.  The aliases are accepted by New/Do as well.
var aliases = map[string]string{
	"monica":         "monica",
	"raycast":        "raycast",
	"merlin":         "merlin",
	"sider":          "sider",
	"tinycms-web":    "tinycms-web",
	"arena":          "arena",
	"duckduckgo-web": "duckduckgo-web",
	"meta-ai":        "meta-ai",
	"poe-web":        "poe-web",
	"claude":         AdapterClaude, "claude-web": AdapterClaude,
	"grok": AdapterGrokWeb, "grok-web": AdapterGrokWeb,
	"grok-console": AdapterGrokConsole, "grok-build": AdapterGrokBuild,
	"genspark": AdapterGenspark, "genspark-web": AdapterGenspark,
	"gemini": AdapterGeminiWeb, "gemini-web": AdapterGeminiWeb,
	"gigachat": AdapterGigaChatWeb, "gigachat-web": AdapterGigaChatWeb,
	"zenmux-web": "zenmux-web",
	"blackbox":   "blackbox",
	"conol-web":  "conol-web",
	"adapta-web": "adapta-web",
	"pi":         "pi", "reka-web": "reka-web",
	"huggingchat":    "huggingchat",
	"hyperagent":     "hyperagent",
	"inner-ai":       "inner-ai",
	"uc-web":         "uc-web",
	"easemate":       "easemate",
	"copilot-web":    "copilot-web",
	"perplexity-web": "perplexity-web",
	"t3-web":         "t3-web",
	"you":            "you",
}

var (
	ErrCredential     = errors.New("major web adapter: source credential environment variable is not set")
	ErrUnsupported    = errors.New("major web adapter: unsupported protocol or request field")
	ErrTruncated      = errors.New("major web adapter: truncated upstream stream")
	errStreamComplete = errors.New("major web adapter: upstream stream complete")
)

// HTTPError preserves an upstream status for prerequisite requests.  A caller
// can use HTTPStatus when mapping an error to a gateway response.
type HTTPError struct {
	Status int
	What   string
}

func (e *HTTPError) Error() string {
	if e.What == "" {
		return fmt.Sprintf("major web adapter: upstream status %d", e.Status)
	}
	return fmt.Sprintf("major web adapter: %s (status %d)", e.What, e.Status)
}
func (e *HTTPError) HTTPStatus() int { return e.Status }

type requestError struct{ message string }

func (e *requestError) Error() string   { return e.message }
func (e *requestError) HTTPStatus() int { return http.StatusUnprocessableEntity }

// Client implements the gateway's provider client lifecycle.
type Client struct {
	source  config.Source
	http    *http.Client
	browser config.Browser

	mu         sync.Mutex
	closed     bool
	adaptaGate providerutil.Gate
	adaptaAuth adaptaAuth
}

func New(source config.Source, browsers ...config.Browser) *Client {
	max := source.MaxInflight
	if max < 1 {
		max = 8
	}
	if max > 64 {
		max = 64
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
	b := config.Default().Browser
	if len(browsers) > 0 {
		b = browsers[0]
	}
	return &Client{
		source:  source,
		browser: b,
		http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
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

func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("major web adapter: nil client")
	}
	if ctx == nil {
		return nil, errors.New("major web adapter: nil request context")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, errors.New("major web adapter: client is closed")
	}
	adapter := aliases[strings.ToLower(strings.TrimSpace(c.source.Adapter))]
	if adapter == "" {
		return nil, fmt.Errorf("major web adapter: unsupported adapter %q", c.source.Adapter)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if adapter == "easemate" {
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"EaseMate currently creates a fresh page turn"}
		}
		return c.doEaseMate(ctx, protocol, model, stream, body)
	}
	if adapter == AdapterGeminiWeb {
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Gemini Web currently creates an independent browser turn"}
		}
		return c.doGeminiWeb(ctx, protocol, model, stream, body)
	}
	if adapter == AdapterGigaChatWeb {
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"GigaChat Web currently creates an independent browser turn"}
		}
		return c.doGigaChatWeb(ctx, protocol, model, stream, body)
	}
	if adapter == "duckduckgo-web" {
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Duck.ai currently creates a fresh browser turn"}
		}
		return c.doDuck(ctx, protocol, model, stream, body)
	}
	credential, err := readCredential(c.source)
	if err != nil {
		return nil, err
	}
	switch adapter {
	case "arena":
		return c.doArena(ctx, protocol, model, stream, body, credential)
	case "tinycms-web":
		return c.doTinyCMS(ctx, protocol, model, stream, body, credential)
	case "merlin":
		return c.doMerlin(ctx, protocol, model, stream, body, credential)
	case "sider":
		return c.doSider(ctx, protocol, model, stream, body, credential)
	case "raycast":
		return c.doRaycast(ctx, protocol, model, stream, body, credential)
	case "monica":
		return c.doMonica(ctx, protocol, model, stream, body, credential)
	case "meta-ai":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Meta AI currently creates an independent turn"}
		}
		return c.doMeta(ctx, protocol, model, stream, body, credential)
	case "poe-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Poe currently creates a fresh chat"}
		}
		return c.doPoe(ctx, protocol, model, stream, body, credential)
	case "you":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"You currently creates an independent turn"}
		}
		return c.doYou(ctx, protocol, model, stream, body, credential)
	case "t3-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"T3 currently creates an independent turn"}
		}
		return c.doT3(ctx, protocol, model, stream, body, credential)
	case AdapterClaude:
		return c.doClaude(ctx, protocol, model, stream, body, credential)
	case AdapterGrokWeb:
		return c.doGrokWeb(ctx, protocol, model, stream, body, credential)
	case AdapterGrokConsole:
		return c.doGrokResponses(ctx, protocol, model, stream, body, credential, false)
	case AdapterGrokBuild:
		return c.doGrokResponses(ctx, protocol, model, stream, body, credential, true)
	case AdapterGenspark:
		return c.doGenspark(ctx, protocol, model, stream, body, credential)
	case "zenmux-web":
		return c.doZenmux(ctx, protocol, model, stream, body, credential)
	case "blackbox":
		return c.doBlackbox(ctx, protocol, model, stream, body, credential)
	case "conol-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Conol currently creates a fresh session per request"}
		}
		return c.doConol(ctx, protocol, model, stream, body, credential)
	case "adapta-web":
		return c.doAdapta(ctx, protocol, model, stream, body, credential)
	case "pi", "reka-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Pi/Reka currently create independent turns"}
		}
		return c.doPiReka(ctx, protocol, model, stream, body, credential, adapter == "pi")
	case "huggingchat":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"HuggingChat currently creates a fresh conversation"}
		}
		return c.doHuggingChat(ctx, protocol, model, stream, body, credential)
	case "hyperagent":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"HyperAgent currently creates a fresh thread"}
		}
		return c.doHyperAgent(ctx, protocol, model, stream, body, credential)
	case "inner-ai":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Inner.ai currently creates a fresh session"}
		}
		return c.doInnerAI(ctx, protocol, model, stream, body, credential)
	case "uc-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"UC currently creates an independent turn"}
		}
		return c.doUC(ctx, protocol, model, stream, body, credential)
	case "copilot-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Copilot Web currently creates a fresh conversation"}
		}
		return c.doCopilotWeb(ctx, protocol, model, stream, body, credential)
	case "perplexity-web":
		if clientHeaders.Get("X-COT-Session") != "" {
			return nil, &requestError{"Perplexity currently creates a fresh query"}
		}
		return c.doPerplexity(ctx, protocol, model, stream, body, credential)
	default:
		return nil, fmt.Errorf("major web adapter: unsupported adapter %q", c.source.Adapter)
	}
}

type credentials struct {
	signatureSecret string
	clientIP        string
	recaptcha       string
	lsd             string
	dtsg            string
	formkey         string
	value           string
	cookie          string
	orgID           string
	userID          string
	validated       string
	email           string
	deviceID        string
	sessionID       string
}

// readCredential accepts a plain product cookie/session token and a bounded
// JSON object for account-bound products.  JSON is useful when an organization
// or user ID is issued alongside the secret, but no file or caller header is
// consulted.
func readCredential(source config.Source) (credentials, error) {
	if strings.TrimSpace(source.KeyEnv) == "" {
		return credentials{}, ErrCredential
	}
	raw := strings.TrimSpace(os.Getenv(source.KeyEnv))
	if raw == "" {
		return credentials{}, ErrCredential
	}
	out := credentials{value: raw, cookie: raw}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &object) == nil && object != nil {
		stringField := func(names ...string) string {
			for _, name := range names {
				var value string
				if json.Unmarshal(object[name], &value) == nil && strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
			return ""
		}
		for _, name := range []string{"sessionKey", "session_key", "access_token", "accessToken", "token", "key", "cookie"} {
			if value := stringField(name); value != "" {
				out.value, out.cookie = value, value
				break
			}
		}
		out.orgID = stringField("org_id", "orgID", "organization_id", "organizationID")
		out.formkey = stringField("formkey")
		out.recaptcha = stringField("recaptchaV3Token")
		out.clientIP = stringField("client_ip")
		out.signatureSecret = stringField("signature_secret")
		out.lsd = stringField("lsd")
		out.dtsg = stringField("fb_dtsg", "dtsg")
		out.userID = stringField("user_id", "userID", "uid")
		out.validated = stringField("validated")
		out.email = stringField("email")
		out.deviceID = stringField("device_id")
		out.sessionID = stringField("session_id", "sid")
		if out.value == raw {
			return credentials{}, errors.New("major web adapter: credential JSON has no supported secret field")
		}
	}
	if value := strings.TrimSpace(os.Getenv(source.AccountIDEnv)); value != "" {
		out.orgID = value
		if out.userID == "" {
			out.userID = value
		}
	}
	return out, nil
}

func baseURL(source config.Source, fallback string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		base = fallback
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("major web adapter: invalid base_url")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("major web adapter: base_url must use HTTPS outside loopback")
	}
	return base, nil
}

func endpoint(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	suffix = "/" + strings.TrimLeft(suffix, "/")
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func request(ctx context.Context, hc *http.Client, method, endpoint string, body []byte, headers http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("major web adapter: invalid upstream request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(body))
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("major web adapter: upstream transport failed")
	}
	return resp, nil
}

func readBounded(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("major web adapter: upstream response exceeds limit")
	}
	return data, nil
}

func setBody(resp *http.Response, body io.ReadCloser, contentType string) {
	resp.Body = body
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
}

func randomID(prefix string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return prefix + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(raw[:])
}

func randomUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("00000000-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffffffff)
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

type chatInput struct {
	Prompt string
	Model  string
}

func parseChatInput(body []byte, model string) (chatInput, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return chatInput{}, &requestError{"major web adapter: request must be a JSON object"}
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return chatInput{}, &requestError{"major web adapter: unsupported request field: " + key}
		}
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(fields["messages"], &messages) != nil || len(messages) != 1 {
		return chatInput{}, &requestError{"major web adapter: exactly one user message is required; history is unsupported"}
	}
	message := messages[0]
	for key := range message {
		if key != "role" && key != "content" {
			return chatInput{}, &requestError{"major web adapter: unsupported message field: " + key}
		}
	}
	var role string
	if json.Unmarshal(message["role"], &role) != nil || role != "user" {
		return chatInput{}, &requestError{"major web adapter: only a single user message is supported"}
	}
	var prompt string
	if json.Unmarshal(message["content"], &prompt) != nil || strings.TrimSpace(prompt) == "" {
		return chatInput{}, &requestError{"major web adapter: message content must be non-empty text"}
	}
	return chatInput{Prompt: prompt, Model: model}, nil
}

func parseResponsesInput(body []byte, model string) (chatInput, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return chatInput{}, &requestError{"major web adapter: Responses request must be a JSON object"}
	}
	for key := range fields {
		if key != "model" && key != "input" && key != "stream" {
			return chatInput{}, &requestError{"major web adapter: unsupported Responses field: " + key}
		}
	}
	var prompt string
	if json.Unmarshal(fields["input"], &prompt) != nil {
		var input []map[string]json.RawMessage
		if json.Unmarshal(fields["input"], &input) != nil || len(input) != 1 {
			return chatInput{}, &requestError{"major web adapter: Responses input must contain one user item"}
		}
		for key := range input[0] {
			if key != "role" && key != "content" {
				return chatInput{}, &requestError{"major web adapter: unsupported Responses input field: " + key}
			}
		}
		var role string
		if json.Unmarshal(input[0]["role"], &role) != nil || role != "user" {
			return chatInput{}, &requestError{"major web adapter: Responses input supports only one user item"}
		}
		if json.Unmarshal(input[0]["content"], &prompt) != nil {
			return chatInput{}, &requestError{"major web adapter: Responses content must be text"}
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return chatInput{}, &requestError{"major web adapter: Responses input must be non-empty text"}
	}
	return chatInput{Prompt: prompt, Model: model}, nil
}

// newProviderStreamBody converts one source SSE stream into the gateway's
// OpenAI-compatible Chat Completions stream.  The parser owns protocol
// correlation and returns done=true only after the source reports completion;
// an EOF before that point is treated as truncation rather than a successful
// empty answer.
func newProviderStreamBody(source io.ReadCloser, id, model string, parse func(string, string) ([]byte, bool, error)) io.ReadCloser {
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
					return nil, ErrTruncated
				}
				out, done, err := parse(event, data)
				if err != nil {
					return nil, err
				}
				if done {
					ended = true
					finish := chatChunk(id, model, created, map[string]any{}, "stop")
					out = append(out, finish...)
					return append(out, []byte("data: [DONE]\n\n")...), nil
				}
				if len(out) > 0 {
					return out, nil
				}
			}
		},
		closeFn: source.Close,
	}
}

func cookieHeader(name, value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "=") || strings.Contains(value, ";") {
		return value
	}
	return name + "=" + value
}

func sseData(value any) []byte {
	b, _ := json.Marshal(value)
	return append([]byte("data: "), append(b, '\n', '\n')...)
}

func chatChunk(id, model string, created int64, delta map[string]any, finish any) []byte {
	return sseData(map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
}

func chatCompletion(id, model, content, reasoning string, created int64) []byte {
	message := map[string]any{"role": "assistant", "content": content}
	if reasoning != "" {
		message["reasoning_content"] = reasoning
	}
	b, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}, "usage": nil})
	return b
}

type transformBody struct {
	mu         sync.Mutex
	next       func() ([]byte, error)
	closeFn    func() error
	pending    []byte
	produced   int
	done       bool
	terminal   error
	terminalOn bool
	closeOnce  sync.Once
}

func (b *transformBody) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	for {
		b.mu.Lock()
		if len(b.pending) > 0 {
			n := copy(dst, b.pending)
			b.pending = b.pending[n:]
			b.mu.Unlock()
			return n, nil
		}
		if b.done {
			if b.terminal != nil && !b.terminalOn {
				b.terminalOn = true
				err := b.terminal
				b.mu.Unlock()
				return 0, err
			}
			b.mu.Unlock()
			return 0, io.EOF
		}
		next := b.next
		b.mu.Unlock()

		// Do not hold b.mu while waiting for the upstream. Close must be able
		// to call closeFn and unblock a network read when the downstream
		// cancels a request.
		out, err := next()
		b.mu.Lock()
		if b.done {
			b.mu.Unlock()
			return 0, io.EOF
		}
		if len(out) > maxResponseBytes-b.produced {
			b.done = true
			b.mu.Unlock()
			_ = b.closeSource()
			return 0, errors.New("major web adapter: converted output exceeds limit")
		}
		b.produced += len(out)
		if len(out) > 0 {
			b.pending = out
			if err != nil {
				b.terminal = err
				b.done = true
			}
			b.mu.Unlock()
			if err != nil {
				_ = b.closeSource()
			}
			continue
		}
		if err != nil {
			b.terminal = err
			b.done = true
			b.terminalOn = true
			b.mu.Unlock()
			_ = b.closeSource()
			return 0, err
		}
		b.mu.Unlock()
	}
}

func (b *transformBody) Close() error {
	b.mu.Lock()
	b.done = true
	b.pending = nil
	b.mu.Unlock()
	return b.closeSource()
}

func (b *transformBody) closeSource() error {
	var err error
	b.closeOnce.Do(func() { err = b.closeFn() })
	return err
}

type sseDecoder struct {
	reader      *bufio.Reader
	event       string
	data        []string
	read        int64
	emptyEvents bool
}

func newSSEDecoder(body io.Reader) *sseDecoder {
	return &sseDecoder{reader: bufio.NewReaderSize(body, 32<<10)}
}

func (d *sseDecoder) next() (event, data string, ok bool, err error) {
	for {
		lineBytes, readErr := readBoundedLine(d.reader, maxEventBytes)
		d.read += int64(len(lineBytes))
		if d.read > maxResponseBytes {
			return "", "", false, errors.New("major web adapter: upstream stream exceeds limit")
		}
		line := string(lineBytes)
		if len(line) > 0 {
			if !strings.HasSuffix(line, "\n") {
				return "", "", false, ErrTruncated
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			switch {
			case line == "":
				if len(d.data) > 0 || (d.emptyEvents && d.event != "") {
					e, value := d.event, strings.Join(d.data, "\n")
					d.event, d.data = "", nil
					return e, value, true, nil
				}
				d.event = ""
			case strings.HasPrefix(line, ":"):
			case strings.HasPrefix(line, "event:"):
				d.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				if strings.HasPrefix(value, " ") {
					value = value[1:]
				}
				d.data = append(d.data, value)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				if len(d.data) > 0 || d.event != "" {
					return "", "", false, ErrTruncated
				}
				return "", "", false, nil
			}
			return "", "", false, readErr
		}
	}
}

type ndjsonDecoder struct {
	reader *bufio.Reader
	read   int64
}

func newNDJSONDecoder(body io.Reader) *ndjsonDecoder {
	return &ndjsonDecoder{reader: bufio.NewReaderSize(body, 32<<10)}
}

func (d *ndjsonDecoder) next() (map[string]any, bool, error) {
	for {
		lineBytes, err := readBoundedLine(d.reader, maxEventBytes)
		d.read += int64(len(lineBytes))
		if d.read > maxResponseBytes {
			return nil, false, errors.New("major web adapter: upstream stream exceeds limit")
		}
		line := string(lineBytes)
		if len(line) > maxEventBytes {
			return nil, false, errors.New("major web adapter: NDJSON event exceeds limit")
		}
		if len(line) > 0 && !strings.HasSuffix(line, "\n") {
			return nil, false, ErrTruncated
		}
		line = strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line != "" {
			var value map[string]any
			if json.Unmarshal([]byte(line), &value) != nil {
				return nil, false, errors.New("major web adapter: invalid NDJSON event")
			}
			return value, true, nil
		}
		if err != nil {
			if err == io.EOF {
				return nil, false, nil
			}
			return nil, false, err
		}
	}
}

// readBoundedLine avoids bufio.Reader.ReadString's unbounded allocation for a
// peer that withholds a newline. ReadSlice only returns the reader's fixed
// buffer-sized fragments, so a malicious line is rejected before it can grow
// beyond the protocol event limit.
func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	if limit < 1 {
		return nil, errors.New("major web adapter: invalid line limit")
	}
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > limit-len(line) {
			return nil, errors.New("major web adapter: protocol event exceeds limit")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

func responseFromStream(resp *http.Response, body io.ReadCloser, stream bool, id, model string) (*http.Response, error) {
	if stream {
		setBody(resp, body, "text/event-stream")
		return resp, nil
	}
	data, err := readBounded(body, maxResponseBytes)
	_ = body.Close()
	if err != nil {
		return nil, err
	}
	content, reasoning, err := aggregateSSE(data)
	if err != nil {
		return nil, err
	}
	result := chatCompletion(id, model, content, reasoning, time.Now().Unix())
	resp.Body = io.NopCloser(bytes.NewReader(result))
	resp.ContentLength = int64(len(result))
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func aggregateSSE(data []byte) (string, string, error) {
	d := newSSEDecoder(bytes.NewReader(data))
	var content, reasoning strings.Builder
	for {
		_, raw, ok, err := d.next()
		if err != nil {
			return "", "", err
		}
		if !ok {
			return content.String(), reasoning.String(), nil
		}
		if raw == "[DONE]" {
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(raw), &event) != nil {
			return "", "", errors.New("major web adapter: invalid converted stream event")
		}
		if len(event.Choices) > 0 {
			content.WriteString(event.Choices[0].Delta.Content)
			reasoning.WriteString(event.Choices[0].Delta.ReasoningContent)
		}
	}
}
