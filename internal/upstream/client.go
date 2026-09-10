// Package upstream contains native Go HTTP protocol adapters. It never launches
// third party proxy processes, follows redirects, or forwards client credentials.
package upstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/config"
)

type Client struct {
	initError error
	http      *http.Client
	source    config.Source
	token     *copilotToken
	web       *chatgptweb.Driver
	adapter   interface {
		Do(context.Context, string, string, bool, []byte, http.Header) (*http.Response, error)
		Close()
	}
}

func newHTTP(s config.Source) *Client {
	// Retain a bounded concurrent wave instead of closing most sockets after each burst.
	idle := max(4, min(s.MaxInflight, 256))
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, ForceAttemptHTTP2: true, MaxIdleConns: idle, MaxIdleConnsPerHost: idle, MaxConnsPerHost: s.MaxInflight, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second, ExpectContinueTimeout: time.Second, MaxResponseHeaderBytes: 64 << 10, DisableCompression: true}
	return &Client{http: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, source: s, token: &copilotToken{}}
}
func (c *Client) Close() {
	if c.adapter != nil {
		c.adapter.Close()
	}
	if c.http != nil {
		c.http.CloseIdleConnections()
	}
}
func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, clientHeaders http.Header) (*http.Response, error) {
	if c.initError != nil {
		return nil, c.initError
	}
	if c.adapter != nil {
		return c.adapter.Do(ctx, protocol, model, stream, body, clientHeaders)
	}
	if c.web != nil {
		return c.web.Do(ctx, protocol, model, body, clientHeaders)
	}
	key := c.source.CredentialValue()
	if !c.source.Local && key == "" {
		return nil, errors.New("source credential environment variable is not set")
	}
	base := strings.TrimRight(c.source.BaseURL, "/")
	path := ""
	switch protocol {
	case "chat":
		path = "/chat/completions"
	case "responses":
		path = "/responses"
	case "messages":
		path = "/messages"
	case "gemini":
		path = "/models/" + url.PathEscape(model) + ":generateContent"
		if stream {
			path = "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		}
	default:
		return nil, errors.New("unsupported protocol")
	}
	if c.source.Adapter == "copilot" {
		var e error
		key, base, e = c.token.get(ctx, c.http, key)
		if e != nil {
			return nil, e
		}
		if protocol == "messages" {
			path = "/v1/messages"
		}
	}
	if c.source.Adapter == "codex" && !stream {
		return nil, errors.New("codex subscription adapter requires streaming Responses requests")
	}
	if c.source.Adapter == "gemini-cli" {
		var e error
		body, e = cloudRequest(body, model, c.source.Project)
		if e != nil {
			return nil, e
		}
		path = ":generateContent"
		if stream {
			path = ":streamGenerateContent?alt=sse"
		}
	}
	payload := newPayload(body)
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, base+path, payload)
	if e != nil {
		payload.Close()
		return nil, errors.New("invalid upstream request")
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("User-Agent", "clash-tokens/0.1")
	switch c.source.Adapter {
	case "anthropic":
		req.Header.Set("x-api-key", key)
		version := clientHeaders.Get("anthropic-version")
		if version == "" {
			version = "2023-06-01"
		}
		req.Header.Set("anthropic-version", version)
		if b := clientHeaders.Get("anthropic-beta"); b != "" {
			req.Header.Set("anthropic-beta", b)
		}
	case "gemini":
		req.Header.Set("x-goog-api-key", key)
	case "openai":
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		if organization := strings.TrimSpace(c.source.Organization); organization != "" {
			req.Header.Set("OpenAI-Organization", organization)
		}
		if project := strings.TrimSpace(c.source.Project); project != "" {
			req.Header.Set("OpenAI-Project", project)
		}
	case "gemini-cli":
		req.Header.Set("Authorization", "Bearer "+key)
	case "claude-code":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	case "kimi-code":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Msh-Platform", "clash-tokens")
		req.Header.Set("X-Msh-Version", "0.1.0")
		if protocol == "messages" {
			req.Header.Set("anthropic-version", "2023-06-01")
		}
	case "copilot":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Editor-Version", "vscode/1.114.0")
		req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
		req.Header.Set("Copilot-Integration-Id", "vscode-chat")
		req.Header.Set("X-Initiator", "agent")
	case "codex":
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("originator", "codex_cli_rs")
		if account := os.Getenv(c.source.AccountIDEnv); account != "" {
			req.Header.Set("Chatgpt-Account-Id", account)
		}
	default:
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
	}
	// Avoid net/http replay of an ambiguous POST, including a stale keepalive
	// connection. A failed generation must never become a duplicate generation.
	req.GetBody = nil
	// Absence of GotConn proves this attempt never reached request writing.
	// Once a connection is handed to the transport, even a write error is
	// ambiguous: some bytes may already have reached the provider.
	var connected atomic.Bool
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}))
	resp, e := c.http.Do(req)
	// The transport may close request bodies asynchronously. A closeable reader
	// drops the input reference under a lock, so early responses/cancellation
	// cannot race a writeLoop still reading the request.
	payload.Close()
	if e != nil {
		_, standardTransport := c.http.Transport.(*http.Transport)
		return nil, &TransportError{BeforeSubmission: standardTransport && !connected.Load() && ctx.Err() == nil}
	}
	if c.source.Adapter == "gemini-cli" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body, e = cloudResponse(resp.Body, stream)
		if e != nil {
			return nil, e
		}
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
	}
	return resp, nil
}

// TransportError deliberately excludes transport error text, which may contain
// credentials or sensitive endpoint details. BeforeSubmission is conservative;
// false means the caller must not replay this request.
type TransportError struct {
	BeforeSubmission bool
}

func (*TransportError) Error() string { return "upstream transport failed" }

type payload struct {
	mu sync.Mutex
	r  *bytes.Reader
}

func newPayload(b []byte) *payload { return &payload{r: bytes.NewReader(b)} }
func (p *payload) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.r == nil {
		return 0, io.EOF
	}
	return p.r.Read(b)
}
func (p *payload) Close() error { p.mu.Lock(); p.r = nil; p.mu.Unlock(); return nil }

type copilotToken struct {
	mu          sync.Mutex
	value, base string
	expires     time.Time
	refreshing  chan struct{}
}

func (t *copilotToken) get(ctx context.Context, c *http.Client, githubToken string) (string, string, error) {
	for {
		t.mu.Lock()
		if t.value != "" && time.Now().Before(t.expires) {
			v, b := t.value, t.base
			t.mu.Unlock()
			return v, b, nil
		}
		if wait := t.refreshing; wait != nil {
			t.mu.Unlock()
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-wait:
			}
			continue
		}
		t.refreshing = make(chan struct{})
		t.mu.Unlock()
		v, b, expiry, e := exchange(ctx, c, githubToken)
		t.mu.Lock()
		if e == nil {
			t.value, t.base, t.expires = v, b, expiry
		}
		close(t.refreshing)
		t.refreshing = nil
		t.mu.Unlock()
		return v, b, e
	}
}
func exchange(ctx context.Context, c *http.Client, key string) (string, string, time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/copilot_internal/v2/token", nil)
	req.Header.Set("Authorization", "token "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.114.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	resp, e := c.Do(req)
	if e != nil {
		return "", "", time.Time{}, errors.New("copilot credential exchange failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", time.Time{}, fmt.Errorf("copilot credential exchange status %d", resp.StatusCode)
	}
	var p struct {
		Token     string            `json:"token"`
		Refresh   int64             `json:"refresh_in"`
		Endpoints map[string]string `json:"endpoints"`
	}
	e = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&p)
	if e != nil || p.Token == "" {
		return "", "", time.Time{}, errors.New("invalid copilot credential exchange response")
	}
	base := strings.TrimRight(p.Endpoints["api"], "/")
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || (u.Hostname() != "api.githubcopilot.com" && !strings.HasSuffix(u.Hostname(), ".githubcopilot.com")) {
		return "", "", time.Time{}, errors.New("untrusted copilot endpoint")
	}
	return p.Token, base, time.Now().Add(time.Duration(max(1, min(p.Refresh-60, 3600))) * time.Second), nil
}
