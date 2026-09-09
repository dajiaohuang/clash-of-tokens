package codingfinal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	cursorRunPath      = "/agent.v1.AgentService/Run"
	cursorAgentVersion = "2026.07.08-0c04a8a"
)

func cursorEndpoint(sourceBase string) (string, error) {
	base, err := endpointBase(config.Source{BaseURL: sourceBase})
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(base, cursorRunPath) {
		return base, nil
	}
	return base + cursorRunPath, nil
}

// endpointBase validates operator supplied URLs; caller code supplies the
// complete config.Source to the shared helper.

// cursorRequestBody is deliberately built from the pinned AgentService wire
// field table. Cursor's agent endpoint accepts one user action per Run; prior
// messages are represented in the user text exactly as cursor-agent's
// flattenMessages helper does. The gateway rejects X-COT-Session because this
// direct adapter does not retain the upstream bidirectional stream needed for
// genuine continuation.
func EncodeCursorRequest(req chatRequest, model, conversation string) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return nil, fmt.Errorf("%w: Cursor tool execution is not implemented", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return nil, fmt.Errorf("%w: Cursor request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return nil, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	for _, message := range req.Messages {
		if message.Role == "tool" || len(message.ToolCalls) > 0 {
			return nil, fmt.Errorf("%w: Cursor tool history is not implemented", ErrUnsupported)
		}
	}
	text := flattenCursorMessages(req.Messages)
	if text == "" {
		return nil, fmt.Errorf("%w: message text is empty", ErrUnsupported)
	}
	resolved, params := resolveCursorModel(model)
	user := encodeMessageField(1,
		encodeStringField(1, text), encodeStringField(2, randomUUID()),
		encodeMessageField(3), encodeVarintField(4, 1))
	action := encodeMessageField(2, encodeMessageField(1, user))
	modelDetails := encodeMessageField(3, encodeStringField(1, resolved), encodeStringField(3, resolved), encodeStringField(4, resolved))
	requestedParts := []byte(encodeStringField(1, resolved))
	for _, p := range params {
		requestedParts = append(requestedParts, encodeMessageField(3, encodeStringField(1, p.id), encodeStringField(2, p.value))...)
	}
	requested := encodeMessageField(9, requestedParts)
	run := encodeMessageField(1,
		encodeMessageField(1), action, modelDetails,
		encodeStringField(5, conversation), requested, encodeVarintField(12, 0), encodeStringField(16, conversation))
	return connectFrame(run, 0)
}

// BuildCursorRequest validates an OpenAI Chat request and returns the exact
// Connect framed AgentService request body. It is useful to wire-level tests
// and callers that need to inspect the request without sending it.
func BuildCursorRequest(body []byte, model, conversation string) ([]byte, error) {
	req, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	return EncodeCursorRequest(req, model, conversation)
}

type cursorParameter struct{ id, value string }

// resolveCursorModel mirrors the pinned agent client's finite model spelling
// and out-of-band effort parameters. Unknown model ids pass through unchanged.
func resolveCursorModel(model string) (string, []cursorParameter) {
	id := strings.TrimSpace(strings.TrimPrefix(model, "cursor/"))
	switch strings.ToLower(id) {
	case "", "composer-2-5", "composer-latest":
		return "composer-2.5", nil
	case "composer-2-5-fast", "composer-2.5-sdk-fast", "composer-latest-fast":
		return "composer-2.5", []cursorParameter{{"fast", "true"}}
	case "composer-2.5-sdk":
		return "composer-2.5", nil
	case "auto":
		return "default", nil
	}
	for _, level := range []string{"cost", "balance", "intelligence"} {
		if id == "auto-"+level {
			return "default", []cursorParameter{{"optimization", level}}
		}
	}
	if strings.HasPrefix(id, "composer-") && strings.HasSuffix(id, "-fast") {
		return strings.TrimSuffix(id, "-fast"), []cursorParameter{{"fast", "true"}}
	}
	for _, suffix := range []string{"low", "medium", "high", "xhigh"} {
		marker := "-" + suffix
		if strings.HasPrefix(id, "claude-") && strings.HasSuffix(id, marker) && len(id) > len("claude-")+len(marker) {
			return strings.TrimSuffix(id, marker), []cursorParameter{{"effort", suffix}}
		}
		if strings.HasPrefix(id, "gpt-") && strings.HasSuffix(id, marker) && len(id) > len("gpt-")+len(marker) {
			return strings.TrimSuffix(id, marker), []cursorParameter{{"reasoning", suffix}}
		}
	}
	return id, nil
}

func flattenCursorMessages(messages []chatMessage) string {
	if len(messages) == 0 {
		return ""
	}
	systemTexts := make([]string, 0, len(messages))
	turn := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			if m.Content != "" {
				systemTexts = append(systemTexts, m.Content)
			}
			continue
		}
		turn = append(turn, m)
	}
	// The pinned helper intentionally leaves a single plain user turn
	// unlabeled. This is the normal first-turn wire shape.
	if len(turn) == 1 && turn[0].Role == "user" && len(turn[0].ToolCalls) == 0 {
		return joinCursorSystemText(systemTexts, turn[0].Content)
	}
	parts := make([]string, 0, len(turn))
	for _, m := range turn {
		text := m.Content
		switch m.Role {
		case "user":
			if text != "" {
				parts = append(parts, "User: "+text)
			}
		case "assistant":
			if text != "" {
				parts = append(parts, "Assistant: "+text)
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, "Assistant called tool "+tc.Name+" ("+tc.ID+") with arguments: "+tc.Arguments)
			}
		case "tool":
			parts = append(parts, "Tool result ("+m.ToolCallID+"): "+text)
		default:
			if text != "" {
				parts = append(parts, m.Role+": "+text)
			}
		}
	}
	return joinCursorSystemText(systemTexts, strings.Join(parts, "\n\n"))
}

func joinCursorSystemText(systemTexts []string, body string) string {
	if len(systemTexts) == 0 {
		return body
	}
	return strings.Join(systemTexts, "\n\n") + "\n\n" + body
}

func (c *Client) doCursor(ctx context.Context, token, model string, req chatRequest, s *session) (*http.Response, error) {
	endpoint, err := cursorEndpoint(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	body, err := EncodeCursorRequest(req, model, s.conversation)
	if err != nil {
		return nil, err
	}
	trace := "00-" + randomHex(16) + "-" + randomHex(8) + "-01"
	requestID := randomUUID()
	if i := strings.Index(token, "::"); i >= 0 && i+2 < len(token) {
		token = token[i+2:]
	}
	h := http.Header{"Authorization": []string{"Bearer " + token}, "Backend-Traceparent": []string{trace}, "Connect-Accept-Encoding": []string{"gzip"}, "Connect-Protocol-Version": []string{"1"}, "Content-Type": []string{"application/connect+proto"}, "Traceparent": []string{trace}, "User-Agent": []string{"connect-es/1.6.1"}, "X-Cursor-Client-Type": []string{"cli"}, "X-Cursor-Client-Version": []string{"cli-" + cursorAgentVersion}, "X-Ghost-Mode": []string{"true"}, "X-Original-Request-Id": []string{requestID}, "X-Request-Id": []string{requestID}}
	// Plain HTTP is used only for loopback fixtures. HTTP/1 cannot carry the
	// half-open request body that Cursor's bidirectional Connect stream needs,
	// so use a finite request there; production HTTPS uses the pipe below.
	if strings.HasPrefix(endpoint, "http://") {
		return post(ctx, c.http, endpoint, body, h)
	}
	reader, writer := io.Pipe()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, reader)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("%w: invalid Cursor request", ErrUnsupported)
	}
	request.GetBody = nil
	request.ContentLength = -1
	request.Header = h
	go func() {
		_, writeErr := writer.Write(body)
		if writeErr != nil {
			_ = writer.CloseWithError(writeErr)
		}
	}()
	response, err := c.http.Do(request)
	if err != nil {
		_ = writer.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding final adapter: Cursor upstream transport failed")
	}
	response.Body = &cursorRequestResponse{ReadCloser: response.Body, writer: writer}
	return response, nil
}

func cursorToSSE(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	return cursorFramesToSSE(ctx, r, model, "chatcmpl-cursor-"+randomUUID(), time.Now().Unix(), false, false, false, false, 0, bytes.Buffer{})
}

func cursorFramesToSSE(ctx context.Context, r io.Reader, model, id string, created int64, role, textSeen, terminal, endStream bool, total int64, out bytes.Buffer) ([]byte, error) {
	reader := &connectReader{reader: r}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		flags, payload, err := reader.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		total += int64(len(payload))
		if total > maxEventBytes {
			return nil, fmt.Errorf("%w: Cursor response exceeds byte limit", ErrUnsupported)
		}
		if flags&connectFlagEndStream != 0 {
			if endStream {
				return nil, fmt.Errorf("%w: duplicate Cursor end-stream", ErrTruncated)
			}
			endStream = true
			if e := parseJSONError(payload); e != nil {
				return nil, e
			}
			if textSeen {
				appendCursorFinish(&out, id, created, model, "stop")
				if int64(out.Len()) > maxOutputBytes {
					return nil, fmt.Errorf("%w: Cursor output exceeds byte limit", ErrUnsupported)
				}
				return out.Bytes(), nil
			}
			return nil, ErrTruncated
		}
		if endStream {
			return nil, fmt.Errorf("%w: Cursor data follows end-stream", ErrTruncated)
		}
		fields, err := decodeProtoFields(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: Cursor protobuf: %v", ErrTruncated, err)
		}
		for _, top := range fields {
			if top.number == 4 && top.wire == 2 {
				if textSeen {
					terminal = true
				}
				continue
			}
			if top.number != 1 || top.wire != 2 {
				continue
			}
			updates, err := decodeProtoFields(top.bytes)
			if err != nil {
				return nil, fmt.Errorf("%w: Cursor interaction: %v", ErrTruncated, err)
			}
			for _, u := range updates {
				switch u.number {
				case 1:
					if u.wire != 2 {
						continue
					}
					inner, e := decodeProtoFields(u.bytes)
					if e != nil {
						return nil, fmt.Errorf("%w: Cursor text delta: %v", ErrTruncated, e)
					}
					if f, ok := firstField(inner, 1, 2); ok {
						if !role {
							appendCursorRole(&out, id, created, model)
							role = true
						}
						v, e := nestedCursorText(f.bytes)
						if e != nil {
							return nil, fmt.Errorf("%w: Cursor text value: %v", ErrTruncated, e)
						}
						if v != "" {
							textSeen = true
							appendCursorContent(&out, id, created, model, v)
						}
					}
				case 4:
					if u.wire != 2 {
						continue
					}
					inner, e := decodeProtoFields(u.bytes)
					if e != nil {
						return nil, fmt.Errorf("%w: Cursor thinking delta: %v", ErrTruncated, e)
					}
					if f, ok := firstField(inner, 1, 2); ok {
						if !role {
							appendCursorRole(&out, id, created, model)
							role = true
						}
						v, e := nestedCursorText(f.bytes)
						if e != nil {
							return nil, fmt.Errorf("%w: Cursor thinking value: %v", ErrTruncated, e)
						}
						if v != "" {
							textSeen = true
							appendCursorField(&out, id, created, model, "reasoning_content", v)
						}
					}
				case 14:
					terminal = true
				}
			}
		}
		if int64(out.Len()) > maxOutputBytes {
			return nil, fmt.Errorf("%w: Cursor output exceeds byte limit", ErrUnsupported)
		}
		if terminal {
			if !textSeen {
				return nil, ErrTruncated
			}
			appendCursorFinish(&out, id, created, model, "stop")
			if int64(out.Len()) > maxOutputBytes {
				return nil, fmt.Errorf("%w: Cursor output exceeds byte limit", ErrUnsupported)
			}
			return out.Bytes(), nil
		}
	}
	if !terminal && !endStream {
		return nil, ErrTruncated
	}
	if !textSeen {
		return nil, ErrTruncated
	}
	appendCursorFinish(&out, id, created, model, "stop")
	if int64(out.Len()) > maxOutputBytes {
		return nil, fmt.Errorf("%w: Cursor output exceeds byte limit", ErrUnsupported)
	}
	return out.Bytes(), nil
}
func nestedCursorText(data []byte) (string, error) {
	fields, err := decodeProtoFields(data)
	if err != nil {
		return "", err
	}
	f, ok := firstField(fields, 1, 2)
	if !ok {
		return "", fmt.Errorf("text field is missing")
	}
	return string(f.bytes), nil
}
func appendCursorRole(b *bytes.Buffer, id string, created int64, model string) {
	appendSSE(b, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}})
}
func appendCursorContent(b *bytes.Buffer, id string, created int64, model, text string) {
	appendCursorField(b, id, created, model, "content", text)
}
func appendCursorField(b *bytes.Buffer, id string, created int64, model, key, value string) {
	appendSSE(b, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{key: value}, "finish_reason": nil}}})
}
func appendCursorFinish(b *bytes.Buffer, id string, created int64, model, reason string) {
	appendSSE(b, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}}})
	b.WriteString("data: [DONE]\n\n")
}
func appendSSE(b *bytes.Buffer, v any) {
	x, _ := json.Marshal(v)
	b.WriteString("data: ")
	b.Write(x)
	b.WriteString("\n\n")
}
