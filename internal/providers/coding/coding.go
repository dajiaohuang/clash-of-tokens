// Package coding contains native HTTP clients for coding products whose wire
// protocols are different from the ordinary OpenAI/Anthropic API surfaces.
//
// The clients deliberately receive credentials only through config.Source.KeyEnv.
// They do not inspect product credential files, refresh tokens, launch a proxy,
// or forward credentials supplied by the caller.
package coding

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

type Adapter string

type requestError struct{ err error }

func (e *requestError) Error() string   { return e.err.Error() }
func (e *requestError) Unwrap() error   { return e.err }
func (e *requestError) HTTPStatus() int { return 422 }

const (
	AdapterKiro        Adapter = "kiro"
	AdapterIFlow       Adapter = "iflow"
	AdapterAntigravity Adapter = "antigravity"
)

var (
	ErrCredential = errors.New("coding source credential environment variable is not set")
	ErrProtocol   = errors.New("coding adapter does not support protocol")
)

// Client is the common coding provider contract used by the gateway. A Client
// is safe for concurrent Do calls; each response body owns its stream parser.
type Client struct {
	source config.Source
	http   *http.Client
}

func New(source config.Source) *Client {
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           max(4, min(source.MaxInflight, 256)),
		MaxIdleConnsPerHost:    max(4, min(source.MaxInflight, 256)),
		MaxConnsPerHost:        max(1, source.MaxInflight),
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
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}

func (c *Client) Do(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("coding client is nil")
	}
	if ctx == nil {
		return nil, errors.New("coding request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.source.KeyEnv) == "" || os.Getenv(c.source.KeyEnv) == "" {
		return nil, ErrCredential
	}

	switch Adapter(strings.ToLower(strings.TrimSpace(c.source.Adapter))) {
	case AdapterKiro:
		return c.doKiro(ctx, protocol, model, stream, body, headers)
	case AdapterIFlow:
		return c.doIFlow(ctx, protocol, model, stream, body, headers)
	case AdapterAntigravity:
		return c.doAntigravity(ctx, protocol, model, stream, body, headers)
	default:
		return nil, errors.New("unknown coding adapter")
	}
}

func (c *Client) request(ctx context.Context, method, endpoint string, body []byte) (*http.Request, error) {
	payload := bytes.NewReader(body)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return nil, errors.New("invalid coding upstream request")
	}
	// A POST body must never be replayed after an ambiguous transport failure.
	req.GetBody = nil
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) execute(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, errors.New("coding upstream transport failed")
	}
	return resp, nil
}

func credential(source config.Source) string { return os.Getenv(source.KeyEnv) }

func baseURL(source config.Source, suffix string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(source.BaseURL), "/")
	if base == "" {
		return "", errors.New("coding source base_url is empty")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid coding source base_url")
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("coding source base_url must use HTTPS outside loopback")
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = suffix
	} else if !strings.HasSuffix(path, suffix) {
		path += suffix
	}
	u.Path = path
	u.RawPath = ""
	return u.String(), nil
}

func setBearer(req *http.Request, key string) {
	req.Header.Set("Authorization", "Bearer "+key)
}

func setResponseBody(resp *http.Response, body io.ReadCloser, contentType string) {
	resp.Body = body
	resp.ContentLength = -1
	resp.Header.Del("Content-Length")
	if contentType != "" {
		resp.Header.Set("Content-Type", contentType)
	}
}

func cloneHeaderValue(src http.Header, key string) string {
	if src == nil {
		return ""
	}
	return strings.TrimSpace(src.Get(key))
}

func randomID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failure is exceptionally rare; a time-free constant still
		// keeps the request well formed without touching any local credentials.
		return prefix + "unknown"
	}
	return prefix + hex.EncodeToString(b[:])
}
