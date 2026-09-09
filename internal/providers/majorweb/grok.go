package majorweb

import (
	"bytes"
	"context"
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

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func (c *Client) doGrokResponses(ctx context.Context, protocol, model string, stream bool, body []byte, credential credentials, build bool) (*http.Response, error) {
	var input chatInput
	var err error
	switch protocol {
	case "responses":
		input, err = parseResponsesInput(body, model)
	case "chat":
		input, err = parseChatInput(body, model)
	default:
		return nil, &requestError{"major web adapter: Grok Console/Build supports chat and Responses protocols only"}
	}
	if err != nil {
		return nil, err
	}
	fallback := defaultGrokConsoleBase
	if build {
		fallback = defaultGrokBuildBase
	}
	base, err := baseURL(c.source, fallback)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"model": input.Model, "input": input.Prompt, "stream": stream}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("major web adapter: cannot encode Grok Responses request")
	}
	// Console's documented API is rooted at /v1, while callers may configure
	// either the host root or an already versioned base URL.  Build's default
	// already includes /v1 and follows the same helper, so both forms remain
	// deterministic without allowing an arbitrary path to replace the API
	// operation.
	endpoint := grokResponsesEndpoint(base, build)
	accept := "application/json"
	if stream {
		accept = "text/event-stream"
	}
	headers := http.Header{
		"Accept":        []string{accept},
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer " + credential.value},
		"User-Agent":    []string{"grok2api-majorweb/1"},
	}
	if credential.userID != "" {
		headers.Set("x-userid", credential.userID)
	}
	var upstream *http.Response
	if build {
		upstream, err = request(ctx, c.http, http.MethodPost, endpoint, data, headers)
	} else {
		// Console's web API requires a DPoP-bound access token. A bearer token
		// copied from the browser cookie is rejected even when the SSO cookie is
		// valid, so mint an ephemeral key/token pair for this request.
		upstream, err = c.consoleDPoPRequest(ctx, base, endpoint, data, stream, credential)
	}
	if err != nil {
		return nil, err
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	id := randomID("chatcmpl-")
	if protocol == "responses" {
		if stream {
			setBody(upstream, newValidatedResponsesStreamBody(upstream.Body), "text/event-stream")
			return upstream, nil
		}
		return responseFromNativeResponses(upstream, id, model)
	}
	if stream {
		converted := newProviderStreamBody(upstream.Body, id, model, responsesEventWithIdentity(id, model))
		setBody(upstream, converted, "text/event-stream")
		return upstream, nil
	}
	return responseFromResponses(upstream, id, model)
}

func responseFromNativeResponses(resp *http.Response, id, model string) (*http.Response, error) {
	_ = id
	_ = model
	data, err := readBounded(resp.Body, maxResponseBytes)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil, errors.New("major web adapter: invalid Grok Responses JSON")
	}
	if errorValue, ok := value["error"].(map[string]any); ok {
		_ = errorValue
		return nil, errors.New("major web adapter: Grok Responses upstream request failed")
	}
	if object, _ := value["object"].(string); object != "response" {
		return nil, errors.New("major web adapter: Grok Responses returned a non-Responses object")
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	resp.ContentLength = int64(len(data))
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func newValidatedResponsesStreamBody(source io.ReadCloser) io.ReadCloser {
	decoder := newSSEDecoder(source)
	completed := false
	return &transformBody{next: func() ([]byte, error) {
		if completed {
			return nil, io.EOF
		}
		event, data, ok, err := decoder.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrTruncated
		}
		trimmed := strings.TrimSpace(data)
		if trimmed == "[DONE]" {
			return nil, ErrTruncated
		}
		var value map[string]any
		if json.Unmarshal([]byte(data), &value) != nil {
			return nil, errors.New("major web adapter: invalid Grok Responses SSE event")
		}
		typeName, _ := value["type"].(string)
		if typeName == "error" || strings.HasSuffix(typeName, ".failed") {
			return nil, errors.New("major web adapter: Grok Responses upstream request failed")
		}
		if typeName == "response.completed" || typeName == "response.done" {
			completed = true
		}
		var out strings.Builder
		if event != "" {
			out.WriteString("event: ")
			out.WriteString(event)
			out.WriteString("\n")
		}
		out.WriteString("data: ")
		out.WriteString(strings.ReplaceAll(data, "\n", "\ndata: "))
		out.WriteString("\n\n")
		return []byte(out.String()), nil
	}, closeFn: source.Close}
}

func grokResponsesEndpoint(base string, console bool) string {
	// Both product surfaces expose the Responses API under /v1. Build's
	// default already contains it, while a configured host root needs the same
	// normalization as Console. The bool is retained for source compatibility.
	_ = console
	base = ensureV1Base(base)
	return endpoint(base, "/responses")
}

func ensureV1Base(base string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func responsesEventWithIdentity(id, model string) func(string, string) ([]byte, bool, error) {
	textEmitted := false
	return func(_ string, data string) ([]byte, bool, error) {
		if strings.TrimSpace(data) == "[DONE]" {
			return nil, true, nil
		}
		if strings.TrimSpace(data) == "" {
			return nil, false, nil
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return nil, false, errors.New("major web adapter: invalid Grok Responses SSE event")
		}
		typeName, _ := value["type"].(string)
		if typeName == "error" || strings.HasSuffix(typeName, ".failed") {
			return nil, false, errors.New("major web adapter: Grok Responses upstream request failed")
		}
		if strings.HasSuffix(typeName, "output_text.delta") || strings.HasSuffix(typeName, "output_text.done") {
			if delta, _ := value["delta"].(string); delta != "" {
				textEmitted = true
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"content": delta}, nil), false, nil
			}
			if text, _ := value["text"].(string); text != "" && !textEmitted {
				textEmitted = true
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"content": text}, nil), false, nil
			}
		}
		if strings.Contains(typeName, "reasoning") && strings.HasSuffix(typeName, ".delta") {
			if delta, _ := value["delta"].(string); delta != "" {
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"reasoning_content": delta}, nil), false, nil
			}
		}
		if typeName == "response.completed" || typeName == "response.done" {
			return nil, true, nil
		}
		return nil, false, nil
	}
}

func responseFromResponses(resp *http.Response, id, model string) (*http.Response, error) {
	data, err := readBounded(resp.Body, maxResponseBytes)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(data)
	var content, reasoning string
	if bytes.HasPrefix(trimmed, []byte("data:")) || bytes.Contains(trimmed, []byte("\ndata:")) {
		content, reasoning, err = aggregateResponsesSSE(data)
	} else {
		content, reasoning, err = extractResponsesText(trimmed)
	}
	if err != nil {
		return nil, err
	}
	result := chatCompletion(id, model, content, reasoning, time.Now().Unix())
	resp.Body = io.NopCloser(bytes.NewReader(result))
	resp.ContentLength = int64(len(result))
	resp.Header.Set("Content-Type", "application/json")
	return resp, nil
}

func aggregateResponsesSSE(data []byte) (string, string, error) {
	decoder := newSSEDecoder(bytes.NewReader(data))
	var content, reasoning strings.Builder
	completed := false
	for {
		_, raw, ok, err := decoder.next()
		if err != nil {
			return "", "", err
		}
		if !ok {
			if !completed {
				return "", "", ErrTruncated
			}
			return content.String(), reasoning.String(), nil
		}
		if raw == "[DONE]" {
			completed = true
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(raw), &value) != nil {
			return "", "", errors.New("major web adapter: invalid Grok Responses SSE event")
		}
		typeName, _ := value["type"].(string)
		if strings.Contains(typeName, "reasoning") && strings.HasSuffix(typeName, ".delta") {
			if delta, _ := value["delta"].(string); delta != "" {
				reasoning.WriteString(delta)
			}
		} else if strings.HasSuffix(typeName, "output_text.delta") {
			if delta, _ := value["delta"].(string); delta != "" {
				content.WriteString(delta)
			}
		} else if typeName == "response.output_text.done" && content.Len() == 0 {
			if text, _ := value["text"].(string); text != "" {
				content.WriteString(text)
			}
		} else if typeName == "error" || strings.HasSuffix(typeName, ".failed") {
			return "", "", errors.New("major web adapter: Grok Responses upstream request failed")
		}
		if typeName == "response.completed" || typeName == "response.done" {
			completed = true
		}
	}
}

func extractResponsesText(data []byte) (string, string, error) {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", "", errors.New("major web adapter: invalid Grok Responses JSON")
	}
	if value["object"] != "response" || value["status"] != "completed" {
		return "", "", errors.New("major web adapter: expected a completed Responses object")
	}
	if message, _ := value["error"].(map[string]any); message != nil {
		_ = message
		return "", "", errors.New("major web adapter: Grok Responses upstream request failed")
	}
	content := firstText(value, "output_text", "text")
	if content == "" {
		if output, _ := value["output"].([]any); output != nil {
			content = textFromItems(output)
		}
	}
	if content == "" {
		if choices, _ := value["choices"].([]any); len(choices) > 0 {
			if choice, _ := choices[0].(map[string]any); choice != nil {
				if message, _ := choice["message"].(map[string]any); message != nil {
					content = firstText(message, "content", "text")
				}
			}
		}
	}
	return content, firstText(value, "reasoning", "reasoning_content"), nil
}

func firstText(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, _ := value[key].(string); text != "" {
			return text
		}
	}
	return ""
}

func textFromItems(items []any) string {
	var out strings.Builder
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		if text := firstText(m, "text", "output_text", "content"); text != "" {
			out.WriteString(text)
		}
		if content, _ := m["content"].([]any); content != nil {
			out.WriteString(textFromItems(content))
		}
	}
	return out.String()
}

// doGrokWeb uses the current Grok Web gateway protocol: a temporary
// WebSocket session, a conversation.item.create, and a response.create.  The
// connection is request-local and is closed with the returned stream body.
func (c *Client) doGrokWeb(ctx context.Context, protocol, model string, stream bool, body []byte, credential credentials) (*http.Response, error) {
	if protocol != "chat" {
		return nil, &requestError{"major web adapter: Grok Web supports only chat protocol"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultGrokBase)
	if err != nil {
		return nil, err
	}
	if credential.userID == "" {
		return nil, errors.New("major web adapter: Grok Web account_id_env must provide user_id for the Gateway")
	}
	endpoint, err := grokGatewayEndpoint(base, credential.userID)
	if err != nil {
		return nil, err
	}
	origin := grokGatewayOrigin(base)
	headers := http.Header{
		"Origin":          []string{origin},
		"User-Agent":      []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
		"Accept-Language": []string{"zh-CN,zh;q=0.9,en;q=0.8"},
		"Cache-Control":   []string{"no-cache"},
		"Pragma":          []string{"no-cache"},
		"Cookie":          []string{grokCookieHeader(credential.cookie, credential.userID)},
	}
	dialer := ws.Dialer{Timeout: 15 * time.Second, Header: ws.HandshakeHeaderHTTP(headers)}
	conn, prefetch, _, err := dialer.Dial(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var reader io.Reader = conn
	if prefetch != nil {
		reader = io.MultiReader(prefetch, conn)
	}
	rw := &gatewayReadWriter{Reader: reader, Writer: conn}
	id := randomID("chatcmpl-")
	streamBody := newGrokWebStreamBody(ctx, conn, rw, input, id, model)
	if stream {
		resp, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: streamBody, Request: resp}, nil
	}
	data, err := readBounded(streamBody, maxResponseBytes)
	_ = streamBody.Close()
	if err != nil {
		return nil, err
	}
	content, reasoning, err := aggregateSSE(data)
	if err != nil {
		return nil, err
	}
	result := chatCompletion(id, model, content, reasoning, time.Now().Unix())
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}, nil
}

func grokGatewayOrigin(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return base
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

func grokCookieHeader(value, userID string) string {
	value = strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(strings.TrimSpace(value))
	if value == "" {
		return "; x-userid=" + userID
	}
	parts := strings.Split(value, ";")
	hasSSO, hasSSORW := false, false
	var ssoValue string
	for _, part := range parts {
		name, raw, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "sso":
			hasSSO = true
			ssoValue = strings.TrimSpace(raw)
		case "sso-rw":
			hasSSORW = true
		}
	}
	if !hasSSO {
		// A bare credential is the SSO token form used by the source client.
		if !strings.Contains(value, "=") {
			ssoValue = value
			hasSSO = true
		}
	}
	if hasSSO && ssoValue != "" {
		// Keep the source client's canonical SSO pair first, followed by any
		// additional browser cookies.  A conflicting sso-rw value is replaced
		// because it would represent a different account than sso.
		others := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			name, _, found := strings.Cut(trimmed, "=")
			if !found {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "sso", "sso-rw":
				continue
			default:
				others = append(others, trimmed)
			}
		}
		value = "sso=" + ssoValue + "; sso-rw=" + ssoValue
		if len(others) > 0 {
			value += "; " + strings.Join(others, "; ")
		}
	} else if hasSSORW {
		// This branch is intentionally unreachable for a well-formed cookie,
		// but keeps the function's output deterministic if only sso-rw was
		// supplied.
		value = strings.TrimSpace(value)
	}
	return value + "; x-userid=" + userID
}

func grokGatewayEndpoint(base, userID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", errors.New("major web adapter: invalid Grok Web base_url")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		return "", errors.New("major web adapter: Grok Web base_url must use HTTP(S)")
	}
	u.Path = "/ws/mgw/"
	u.RawQuery = url.Values{"uid": []string{userID}}.Encode()
	u.Fragment = ""
	return u.String(), nil
}

type gatewayReadWriter struct {
	io.Reader
	io.Writer
}

type grokWebStream struct {
	ctx       context.Context
	conn      net.Conn
	rw        io.ReadWriter
	input     chatInput
	id        string
	model     string
	started   bool
	turnSent  bool
	created   bool
	attached  bool
	sessionID string
	read      int64
	ended     bool
	closeOnce sync.Once
	done      chan struct{}
}

func newGrokWebStreamBody(ctx context.Context, conn net.Conn, rw io.ReadWriter, input chatInput, id, model string) io.ReadCloser {
	state := &grokWebStream{ctx: ctx, conn: conn, rw: rw, input: input, id: id, model: model, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-state.done:
		}
	}()
	return &transformBody{next: state.next, closeFn: state.Close}
}

func (g *grokWebStream) Close() error {
	g.closeOnce.Do(func() {
		close(g.done)
		_ = g.conn.Close()
	})
	return nil
}

func (g *grokWebStream) next() ([]byte, error) {
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	if g.ended {
		return nil, io.EOF
	}
	if !g.started {
		g.started = true
		initial := map[string]any{"event": map[string]any{"type": "session.create", "event_id": randomID("evt_init_"), "session": map[string]any{
			"model":  g.input.Model,
			"x_grok": map[string]any{"protocol_capabilities": []string{"conversation_attached", "custom_methods_v1"}, "use_chunk": true, "enable_side_by_side": true, "force_side_by_side": false, "enable_image_generation": true, "image_generation_count": 2, "disable_text_follow_ups": false, "disable_artifact": true, "force_concise": false, "is_temporary": true, "keep_context": false, "disable_memory": true},
		}}}
		if err := writeGatewayJSON(g.rw, initial); err != nil {
			return nil, err
		}
		return chatChunk(g.id, g.model, time.Now().Unix(), map[string]any{"role": "assistant", "content": ""}, nil), nil
	}
	for {
		data, err := readBoundedServerText(g.rw, maxEventBytes)
		if err != nil {
			if ctxErr := g.ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, ErrTruncated
		}
		if len(data) > maxEventBytes {
			return nil, errors.New("major web adapter: Grok Gateway event exceeds limit")
		}
		g.read += int64(len(data))
		if g.read > maxResponseBytes {
			return nil, errors.New("major web adapter: Grok Gateway stream exceeds limit")
		}
		var root map[string]any
		if json.Unmarshal(data, &root) != nil {
			continue
		}
		event, _ := root["event"].(map[string]any)
		if event == nil {
			event = root
		}
		typeName, _ := event["type"].(string)
		switch typeName {
		case "session.created":
			g.created = true
			if g.sessionID == "" {
				g.sessionID, _ = root["session_id"].(string)
			}
		case "conversation.attached":
			g.attached = true
			candidate := ""
			if conversation, _ := event["conversation"].(map[string]any); conversation != nil {
				candidate, _ = conversation["id"].(string)
			}
			if candidate == "" {
				candidate, _ = event["session_id"].(string)
			}
			if g.sessionID != "" && candidate != "" && g.sessionID != candidate {
				return nil, errors.New("major web adapter: Grok Gateway returned inconsistent session IDs")
			}
			if g.sessionID == "" {
				g.sessionID = candidate
			}
		case "error":
			return nil, errors.New("major web adapter: Grok Gateway upstream error")
		case "response.done":
			g.ended = true
			return append(chatChunk(g.id, g.model, time.Now().Unix(), map[string]any{}, "stop"), []byte("data: [DONE]\n\n")...), nil
		}
		if g.created && g.attached && !g.turnSent {
			g.turnSent = true
			if err := g.sendTurn(); err != nil {
				return nil, err
			}
		}
		if out := grokGatewayDelta(typeName, event, g.id, g.model); len(out) > 0 {
			return out, nil
		}
	}
}

// readBoundedServerText reads one complete WebSocket message without first
// allocating the unbounded payload used by wsutil.ReadServerText. MaxFrameSize
// rejects an oversized single frame before its payload is read; the explicit
// message counter also bounds a fragmented text message.
func readBoundedServerText(rw io.ReadWriter, limit int64) ([]byte, error) {
	if rw == nil || limit < 1 {
		return nil, errors.New("major web adapter: invalid WebSocket reader")
	}
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
			// NextFrame dispatches control frames itself while a fragmented
			// message is active. For an unfragmented control frame, dispatch it
			// here before looking for the next data message.
			if !reader.State.Fragmented() {
				if err := wsutil.ControlFrameHandler(rw, ws.StateClientSide)(header, reader); err != nil {
					return nil, err
				}
			}
			continue
		}
		if header.OpCode != ws.OpText {
			if err := reader.Discard(); err != nil {
				return nil, err
			}
			continue
		}

		message, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(message)) > limit {
			return nil, errors.New("major web adapter: Grok Gateway event exceeds limit")
		}
		return message, nil
	}
}

func (g *grokWebStream) sendTurn() error {
	now := time.Now().UnixMilli()
	item := map[string]any{"type": "message", "role": "user", "x_grok": map[string]any{"client_message_id": randomID(""), "input_chunks": []any{map[string]any{"text": map[string]any{"text": g.input.Prompt}}}}}
	if err := writeGatewayJSON(g.rw, map[string]any{"session_id": g.sessionID, "event": map[string]any{"type": "conversation.item.create", "event_id": fmt.Sprintf("evt_msg_%d", now), "item": item}}); err != nil {
		return err
	}
	return writeGatewayJSON(g.rw, map[string]any{"session_id": g.sessionID, "event": map[string]any{"type": "response.create", "event_id": fmt.Sprintf("evt_resp_%d", now)}})
}

func writeGatewayJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return wsutil.WriteClientText(w, data)
}

func grokGatewayDelta(typeName string, event map[string]any, id, model string) []byte {
	if strings.HasSuffix(typeName, "output_text.delta") {
		if delta, _ := event["delta"].(string); delta != "" {
			return chatChunk(id, model, time.Now().Unix(), map[string]any{"content": delta}, nil)
		}
	}
	if typeName == "response.chunk" {
		chunk, _ := event["chunk"].(map[string]any)
		textValue, _ := chunk["text"].(map[string]any)
		delta, _ := textValue["text"].(string)
		channel, _ := textValue["channel"].(string)
		if delta != "" {
			field := "content"
			if strings.Contains(strings.ToUpper(channel), "ANALYSIS") || strings.Contains(strings.ToUpper(channel), "REASONING") {
				field = "reasoning_content"
			}
			return chatChunk(id, model, time.Now().Unix(), map[string]any{field: delta}, nil)
		}
	}
	return nil
}
