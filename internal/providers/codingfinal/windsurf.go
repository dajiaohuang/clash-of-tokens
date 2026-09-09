package codingfinal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	windsurfChatPath         = "/exa.api_server_pb.ApiServerService/GetChatMessage"
	windsurfAuthPath         = "/exa.auth_pb.AuthService/GetUserJwt"
	windsurfIDEVersion       = "3.6.27"
	windsurfExtensionVersion = "1.48.2"
)

func windsurfServiceBase(base string) (string, error) {
	base, err := endpointBase(config.Source{BaseURL: base})
	if err != nil {
		return "", err
	}
	for _, suffix := range []string{windsurfChatPath, windsurfAuthPath} {
		if strings.HasSuffix(base, suffix) {
			return strings.TrimSuffix(base, suffix), nil
		}
	}
	return base, nil
}

func encodeWindsurfMetadata(token, session, userJWT string) []byte {
	meta := []byte{}
	for _, field := range [][]byte{
		encodeStringField(1, "windsurf"),
		encodeStringField(2, windsurfExtensionVersion),
		encodeStringField(3, token),
		encodeStringField(4, "en-US"),
		encodeStringField(7, windsurfIDEVersion),
		encodeStringField(10, session),
		encodeStringField(12, "windsurf"),
		encodeStringField(21, userJWT),
	} {
		meta = append(meta, field...)
	}
	return meta
}

// EncodeWindsurfAuthRequest and EncodeWindsurfRequest expose the pinned
// Devin Desktop protobuf envelopes for deterministic wire-level tests.
func EncodeWindsurfAuthRequest(token, session string) []byte {
	return encodeMessageField(1, encodeWindsurfMetadata(token, session, ""))
}

func EncodeWindsurfRequest(req chatRequest, model, token, session, jwt string) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream", "tools", "tool_choice", "parallel_tool_calls":
		default:
			return nil, fmt.Errorf("%w: Windsurf request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return nil, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	var system strings.Builder
	var prompts [][]byte
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Content)
			continue
		}
		source := uint64(1)
		if m.Role == "assistant" {
			source = 2
		} else if m.Role == "tool" {
			source = 4
		}
		parts := []byte{}
		parts = append(parts, encodeStringField(1, randomUUID())...)
		if m.Role == "assistant" {
			parts = append(parts, encodeVarintField(2, source)...)
		} else {
			parts = append(parts, encodeVarintField(2, source)...)
		}
		parts = append(parts, encodeStringField(3, m.Content)...)
		for _, tc := range m.ToolCalls {
			parts = append(parts, encodeMessageField(6, encodeStringField(1, tc.ID), encodeStringField(2, tc.Name), encodeStringField(3, tc.Arguments))...)
		}
		parts = append(parts, encodeStringField(7, m.ToolCallID)...)
		prompts = append(prompts, encodeMessageField(3, parts))
	}
	metadata := encodeMessageField(1, encodeWindsurfMetadata(token, session, jwt))
	body := []byte{}
	body = append(body, metadata...)
	body = append(body, encodeStringField(2, system.String())...)
	for _, p := range prompts {
		body = append(body, p...)
	}
	body = append(body, encodeVarintField(7, 5)...)
	for _, tool := range req.Tools {
		schema, _ := json.Marshal(tool.Parameters)
		body = append(body, encodeMessageField(10, encodeStringField(1, tool.Name), encodeStringField(2, tool.Description), encodeStringField(3, string(schema)), encodeVarintField(12, btoi(tool.Strict)))...)
	}
	if req.Parallel != nil && !*req.Parallel {
		body = append(body, encodeVarintField(11, 1)...)
	}
	if choice := windsurfToolChoice(req.ToolChoice); choice != nil {
		body = append(body, encodeMessageField(12, choice)...)
	}
	body = append(body, encodeStringField(14, model)...)
	body = append(body, encodeStringField(16, session)...)
	body = append(body, encodeStringField(21, model)...)
	return connectFrame(body, 0)
}

// BuildWindsurfRequest validates an OpenAI Chat request and returns the exact
// framed Devin Desktop/Windsurf request body.
func BuildWindsurfRequest(body []byte, model, token, session, jwt string) ([]byte, error) {
	req, err := decodeChatRequest(body)
	if err != nil {
		return nil, err
	}
	return EncodeWindsurfRequest(req, model, token, session, jwt)
}
func btoi(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}
func windsurfToolChoice(v any) []byte {
	switch x := v.(type) {
	case string:
		if x == "auto" || x == "none" || x == "required" {
			return encodeStringField(1, x)
		}
	case map[string]any:
		if f, ok := x["function"].(map[string]any); ok {
			if n, ok := f["name"].(string); ok && n != "" {
				return encodeStringField(2, n)
			}
		}
	}
	return nil
}

func (c *Client) doWindsurf(ctx context.Context, token, model string, req chatRequest, s *session) (*http.Response, error) {
	base, err := windsurfServiceBase(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	sessionID := s.conversation
	authHeaders := http.Header{"Accept": []string{"*/*"}, "Connect-Protocol-Version": []string{"1"}, "Content-Type": []string{"application/proto"}}
	auth, err := post(ctx, c.http, base+windsurfAuthPath, EncodeWindsurfAuthRequest(token, sessionID), authHeaders)
	if err != nil {
		return nil, err
	}
	if auth.StatusCode < 200 || auth.StatusCode >= 300 {
		return auth, nil
	}
	authBytes, err := io.ReadAll(io.LimitReader(auth.Body, maxEventBytes+1))
	_ = auth.Body.Close()
	if err != nil {
		return nil, err
	}
	if int64(len(authBytes)) > maxEventBytes {
		return nil, fmt.Errorf("%w: Windsurf auth response exceeds byte limit", ErrUnsupported)
	}
	fields, err := decodeProtoFields(authBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: Windsurf auth protobuf: %v", ErrTruncated, err)
	}
	jwt := ""
	if f, ok := firstField(fields, 1, 2); ok {
		jwt = string(f.bytes)
	}
	if jwt == "" {
		return nil, fmt.Errorf("%w: Windsurf authentication returned an empty user JWT", ErrCredential)
	}
	body, err := EncodeWindsurfRequest(req, model, token, sessionID, jwt)
	if err != nil {
		return nil, err
	}
	h := http.Header{"Accept": []string{"application/connect+proto"}, "Connect-Accept-Encoding": []string{"gzip"}, "Connect-Protocol-Version": []string{"1"}, "Content-Type": []string{"application/connect+proto"}, "User-Agent": []string{"windsurf/" + windsurfIDEVersion}}
	return post(ctx, c.http, base+windsurfChatPath, body, h)
}

type windsurfUsage struct{ input, output, cacheWrite, cacheRead uint64 }
type windsurfTool struct{ id, name, args string }

func windsurfToSSE(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	reader := &connectReader{reader: r}
	var out bytes.Buffer
	id := "chatcmpl-windsurf-" + randomUUID()
	created := time.Now().Unix()
	role := false
	var usage windsurfUsage
	usageSeen := false
	stop := uint64(0)
	tools := map[string]int{}
	toolCount := 0
	end := false
	total := int64(0)
	sawPayload := false
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
			return nil, fmt.Errorf("%w: Windsurf response exceeds byte limit", ErrUnsupported)
		}
		if flags&connectFlagEndStream != 0 {
			if end {
				return nil, fmt.Errorf("%w: duplicate Windsurf end-stream", ErrTruncated)
			}
			end = true
			if e := parseJSONError(payload); e != nil {
				return nil, e
			}
			break
		}
		if end {
			return nil, fmt.Errorf("%w: Windsurf data follows end-stream", ErrTruncated)
		}
		sawPayload = true
		fields, err := decodeProtoFields(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: Windsurf response protobuf: %v", ErrTruncated, err)
		}
		for _, f := range fields {
			switch {
			case f.number == 3 && f.wire == 2:
				v := string(f.bytes)
				if v != "" {
					if !role {
						appendCursorRole(&out, id, created, model)
						role = true
					}
					appendCursorContent(&out, id, created, model, v)
				}
			case f.number == 9 && f.wire == 2:
				v := string(f.bytes)
				if v != "" {
					if !role {
						appendCursorRole(&out, id, created, model)
						role = true
					}
					appendCursorField(&out, id, created, model, "reasoning_content", v)
				}
			case f.number == 5 && f.wire == 0:
				stop = f.varint
			case f.number == 6 && f.wire == 2:
				tc, e := decodeWindsurfTool(f.bytes)
				if e != nil {
					return nil, e
				}
				if tc.id == "" {
					continue
				}
				idx, ok := tools[tc.id]
				if !ok {
					idx = toolCount
					tools[tc.id] = idx
					toolCount++
				}
				delta := map[string]any{"index": idx, "function": map[string]any{}}
				fn := delta["function"].(map[string]any)
				if ok == false {
					delta["id"] = tc.id
					delta["type"] = "function"
				}
				if tc.name != "" {
					fn["name"] = tc.name
				}
				if tc.args != "" {
					fn["arguments"] = tc.args
				}
				appendSSE(&out, map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{delta}}, "finish_reason": nil}}})
			case f.number == 7 && f.wire == 2:
				u, e := decodeWindsurfUsage(f.bytes)
				if e != nil {
					return nil, e
				}
				usage = u
				usageSeen = true
			}
		}
		if int64(out.Len()) > maxOutputBytes {
			return nil, fmt.Errorf("%w: Windsurf output exceeds byte limit", ErrUnsupported)
		}
	}
	if !end {
		return nil, ErrTruncated
	}
	if (!sawPayload && stop == 0) || (!role && toolCount == 0) {
		return nil, ErrTruncated
	}
	reason := "stop"
	if toolCount > 0 || stop == 10 {
		reason = "tool_calls"
	} else if stop == 3 {
		reason = "length"
	} else if stop == 11 {
		reason = "content_filter"
	}
	terminal := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": reason}}}
	if usageSeen {
		terminal["usage"] = map[string]any{"prompt_tokens": usage.input, "completion_tokens": usage.output, "total_tokens": usage.input + usage.output, "prompt_tokens_details": map[string]any{"cached_tokens": usage.cacheRead}, "cache_write_tokens": usage.cacheWrite}
	}
	appendSSE(&out, terminal)
	out.WriteString("data: [DONE]\n\n")
	if int64(out.Len()) > maxOutputBytes {
		return nil, fmt.Errorf("%w: Windsurf output exceeds byte limit", ErrUnsupported)
	}
	return out.Bytes(), nil
}
func decodeWindsurfTool(data []byte) (windsurfTool, error) {
	f, e := decodeProtoFields(data)
	if e != nil {
		return windsurfTool{}, fmt.Errorf("%w: Windsurf tool call: %v", ErrTruncated, e)
	}
	t := windsurfTool{}
	for _, x := range f {
		if x.wire != 2 {
			continue
		}
		switch x.number {
		case 1:
			t.id = string(x.bytes)
		case 2:
			t.name = string(x.bytes)
		case 3:
			t.args = string(x.bytes)
		}
	}
	return t, nil
}
func decodeWindsurfUsage(data []byte) (windsurfUsage, error) {
	f, e := decodeProtoFields(data)
	if e != nil {
		return windsurfUsage{}, fmt.Errorf("%w: Windsurf usage: %v", ErrTruncated, e)
	}
	u := windsurfUsage{}
	for _, x := range f {
		if x.wire != 0 {
			continue
		}
		switch x.number {
		case 2:
			u.input = x.varint
		case 3:
			u.output = x.varint
		case 4:
			u.cacheWrite = x.varint
		case 5:
			u.cacheRead = x.varint
		}
	}
	return u, nil
}
