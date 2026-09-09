package webnext

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func (c *Client) doLangFast(ctx context.Context, cred credentials, req chatRequest) (*http.Response, error) {
	base, err := baseURL(c.source, defaultLangFastBase)
	if err != nil {
		return nil, err
	}
	if cred.SupabaseURL != "" {
		base, err = baseURL(configSource(cred.SupabaseURL), defaultLangFastBase)
		if err != nil {
			return nil, err
		}
	}
	userID := cred.UserID
	if userID == "" {
		userID = jwtSubject(cred.Token)
	}
	if userID == "" {
		return nil, ErrCredential
	}
	promptID := strings.TrimSpace(c.source.Project)
	if promptID == "" {
		return nil, errors.New("webnext adapter: LangFast project (prompt_id) is required")
	}
	socketURL := cred.SocketURL
	if socketURL == "" {
		socketURL = defaultLangFastWS
	}
	conn, rw, err := dialLangFast(ctx, socketURL, cred.Token)
	if err != nil {
		return nil, err
	}
	payload := langFastPayload(req, userID, promptID)
	headers := providerHeaders("https://langfa.st", "https://langfa.st/")
	headers.Set("apikey", cred.SupabaseAnonKey)
	headers.Set("Authorization", "Bearer "+cred.Token)
	resp, err := postJSON(ctx, c.http, joinPath(base, "/functions/v1/initiate-prompt-run"), payload, headers)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = conn.Close()
		return resp, nil
	}
	_ = resp.Body.Close()
	packetReader := &langFastPacketReader{conn: conn, rw: rw}
	if req.Stream {
		stream := newConvertedResponse(ctx, packetReader, req.Model, parseLangFast)
		headers := make(http.Header)
		headers.Set("Content-Type", "text/event-stream")
		headers.Set("X-COT-Delivery", "upstream")
		return &http.Response{StatusCode: 200, Header: headers, Body: stream, ContentLength: -1}, nil
	}
	return collectResponse(ctx, packetReader, req.Model, parseLangFast)
}

func langFastPayload(req chatRequest, userID, promptID string) map[string]any {
	promptMeta := map[string]any{
		"model": req.Model, "messages": req.Messages, "response_format": "text", "stream": true,
		"temperature": 0.7, "top_p": 1, "frequency_penalty": 0, "presence_penalty": 0, "max_completion_tokens": 16384,
	}
	if req.Temperature != nil {
		promptMeta["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		promptMeta["top_p"] = *req.TopP
	}
	if req.FrequencyPenalty != nil {
		promptMeta["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		promptMeta["presence_penalty"] = *req.PresencePenalty
	}
	if req.MaxTokens != nil {
		promptMeta["max_completion_tokens"] = *req.MaxTokens
	}
	return map[string]any{
		"run_id":      randomID(""),
		"prompt_id":   promptID,
		"prompt_meta": promptMeta,
		"test_cases":  []any{},
		"created_by":  userID,
	}
}

func jwtSubject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return ""
	}
	var value struct {
		Subject string `json:"sub"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return ""
	}
	return value.Subject
}

func dialLangFast(ctx context.Context, rawURL, token string) (net.Conn, io.ReadWriter, error) {
	endpoint, err := socketEndpoint(rawURL)
	if err != nil {
		return nil, nil, err
	}
	dialer := ws.Dialer{Timeout: 15 * time.Second}
	conn, reader, _, err := dialer.Dial(ctx, endpoint)
	if err != nil {
		return nil, nil, errors.New("webnext adapter: LangFast Socket.IO connection failed")
	}
	var streamReader io.Reader = conn
	if reader != nil {
		streamReader = io.MultiReader(reader, conn)
	}
	conn = newLangFastContextConn(ctx, conn)
	rw := &langFastReadWriter{Reader: streamReader, Writer: conn}
	opened := false
	connected := false
	var handshakeBytes int
	// The WebSocket upgrade timeout does not cover the subsequent Engine.IO
	// open/connect packets. Bound that phase independently so a peer that
	// accepts the socket and then stays silent cannot hold a request forever.
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	for !connected {
		data, err := readLangFastText(rw, maxEventBytes)
		if err != nil {
			_ = conn.Close()
			return nil, nil, errors.New("webnext adapter: LangFast Socket.IO handshake failed")
		}
		handshakeBytes += len(data)
		if handshakeBytes > maxResponseBytes {
			_ = conn.Close()
			return nil, nil, errors.New("webnext adapter: LangFast handshake exceeds limit")
		}
		if len(data) == 0 {
			continue
		}
		switch data[0] {
		case '0':
			opened = true
			if err := wsutil.WriteClientText(rw, []byte(`40{"token":"`+jsonEscape(token)+`"}`)); err != nil {
				_ = conn.Close()
				return nil, nil, errors.New("webnext adapter: LangFast Socket.IO authentication failed")
			}
		case '2':
			if err := wsutil.WriteClientText(rw, []byte("3")); err != nil {
				_ = conn.Close()
				return nil, nil, errors.New("webnext adapter: LangFast Socket.IO heartbeat failed")
			}
		case '4':
			if bytes.HasPrefix(data, []byte("40")) {
				connected = true
			}
		}
		if !opened && connected {
			_ = conn.Close()
			return nil, nil, errors.New("webnext adapter: invalid LangFast Socket.IO handshake")
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	return conn, rw, nil
}

// langFastContextConn keeps cancellation tied to the connection until the
// response body has been consumed. The HTTP initiation request and the later
// event stream share one request context.
type langFastContextConn struct {
	net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func newLangFastContextConn(ctx context.Context, conn net.Conn) *langFastContextConn {
	c := &langFastContextConn{Conn: conn, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-c.done:
		}
	}()
	return c
}

func (c *langFastContextConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		err = c.Conn.Close()
	})
	return err
}

type langFastReadWriter struct {
	io.Reader
	io.Writer
}

func socketEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", errors.New("webnext adapter: invalid LangFast Socket.IO URL")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		if !isLoopback(u.Hostname()) {
			return "", errors.New("webnext adapter: LangFast Socket.IO URL must use HTTPS outside loopback")
		}
		u.Scheme = "ws"
	} else if u.Scheme == "ws" {
		if !isLoopback(u.Hostname()) {
			return "", errors.New("webnext adapter: LangFast Socket.IO URL must use WSS outside loopback")
		}
	} else if u.Scheme != "wss" {
		return "", errors.New("webnext adapter: LangFast Socket.IO URL must use HTTP(S) or WS(S)")
	}
	if !strings.Contains(u.Path, "socket.io") {
		u.Path = strings.TrimRight(u.Path, "/") + "/socket.io/"
	}
	q := u.Query()
	q.Set("EIO", "4")
	q.Set("transport", "websocket")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type langFastState struct {
	lastContent string
	seenContent bool
	completed   bool
	total       int
}

func parseLangFast(ctx context.Context, body io.Reader, emit func(string) error) error {
	return parseLangFastEvents(ctx, body, emit)
}

func parseLangFastEvents(ctx context.Context, body io.Reader, emit func(string) error) error {
	// body is a langFastReader below; use its packet reader through the generic
	// interface so collectResponse and streaming share one event parser.
	reader, ok := body.(*langFastPacketReader)
	if !ok {
		return errors.New("webnext adapter: invalid LangFast stream")
	}
	return reader.consume(ctx, emit)
}

type langFastPacketReader struct {
	conn  io.Closer
	rw    io.ReadWriter
	state langFastState
	read  int
}

func (r *langFastPacketReader) Read([]byte) (int, error) {
	return 0, errors.New("webnext adapter: LangFast packet reader is event based")
}
func (r *langFastPacketReader) Close() error { return r.conn.Close() }

func (r *langFastPacketReader) consume(ctx context.Context, emit func(string) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := readLangFastText(r.rw, maxEventBytes)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrTruncated
		}
		if len(data) == 0 {
			continue
		}
		r.read += len(data)
		if r.read > maxResponseBytes {
			return errors.New("webnext adapter: LangFast stream exceeds limit")
		}
		if data[0] == '2' {
			if err := wsutil.WriteClientText(r.rw, []byte("3")); err != nil {
				return ErrTruncated
			}
			continue
		}
		if data[0] != '4' || len(data) < 2 {
			continue
		}
		if data[1] == '1' {
			return ErrTruncated
		}
		if data[1] != '2' {
			continue
		}
		var packet []json.RawMessage
		if json.Unmarshal(data[2:], &packet) != nil || len(packet) != 2 {
			return errors.New("webnext adapter: malformed LangFast Socket.IO event")
		}
		var name string
		if json.Unmarshal(packet[0], &name) != nil {
			return errors.New("webnext adapter: malformed LangFast Socket.IO event name")
		}
		if name == "completion_finished" {
			if !r.state.seenContent {
				return errors.New("webnext adapter: LangFast completed without answer")
			}
			r.state.completed = true
			return nil
		}
		if name != "execution:chunk" {
			continue
		}
		var value any
		if json.Unmarshal(packet[1], &value) != nil {
			return errors.New("webnext adapter: malformed LangFast chunk")
		}
		if text, ok := value.(string); ok {
			var nested any
			if json.Unmarshal([]byte(text), &nested) == nil {
				value = nested
			} else {
				value = map[string]any{"content": text}
			}
		}
		if object, ok := value.(map[string]any); ok && object["error"] != nil {
			return ErrUpstream
		}
		content, status := langFastContent(value)
		if content == "" {
			if status == "completed" || status == "done" {
				if !r.state.seenContent {
					return errors.New("webnext adapter: LangFast completed without answer")
				}
				r.state.completed = true
				return nil
			}
			continue
		}
		if !strings.HasPrefix(content, r.state.lastContent) {
			return errors.New("webnext adapter: LangFast revised emitted text")
		}
		delta := content[len(r.state.lastContent):]
		r.state.lastContent = content
		r.state.seenContent = true
		r.state.total += len(delta)
		if r.state.total > maxResponseBytes {
			return errors.New("webnext adapter: upstream stream exceeds limit")
		}
		if delta != "" {
			if err := emit(delta); err != nil {
				return err
			}
		}
		if status == "completed" || status == "done" {
			r.state.completed = true
			return nil
		}
	}
}

func langFastContent(value any) (string, string) {
	m, ok := value.(map[string]any)
	if !ok || m == nil {
		return "", ""
	}
	status, _ := m["status"].(string)
	content, _ := m["content"].(string)
	return content, status
}

func readLangFastText(rw io.ReadWriter, limit int64) ([]byte, error) {
	reader := wsutil.NewReader(rw, ws.StateClientSide)
	reader.CheckUTF8 = true
	reader.MaxFrameSize = limit
	reader.OnIntermediate = wsutil.ControlFrameHandler(rw, ws.StateClientSide)
	for {
		header, err := reader.NextFrame()
		if err != nil {
			return nil, err
		}
		if header.OpCode.IsControl() {
			if !reader.State.Fragmented() {
				if err := wsutil.ControlFrameHandler(rw, ws.StateClientSide)(header, reader); err != nil {
					return nil, err
				}
			}
			continue
		}
		if header.OpCode != ws.OpText {
			return nil, errors.New("webnext adapter: LangFast returned non-text WebSocket data")
		}
		value, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(value)) > limit {
			return nil, errors.New("webnext adapter: LangFast event exceeds limit")
		}
		return value, nil
	}
}

func jsonEscape(value string) string {
	data, _ := json.Marshal(value)
	if len(data) >= 2 {
		return string(data[1 : len(data)-1])
	}
	return ""
}
