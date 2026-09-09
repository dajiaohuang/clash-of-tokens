package codingfinal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	warpDefaultEndpoint = "https://app.warp.dev/ai/multi-agent"
	warpClientVersion   = "v0.2025.08.06.08.12.stable_02"
	warpOSVersion       = "11 (26100)"
)

func warpEndpoint(base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = warpDefaultEndpoint
	}
	root, err := endpointBase(config.Source{BaseURL: base})
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(root, "/ai/multi-agent") {
		return root, nil
	}
	return root + "/ai/multi-agent", nil
}

// EncodeWarpRequest follows the modern warp.multi_agent.v1.Request schema
// used by the pinned Warp client: Input.UserInputs.UserInput.UserQuery,
// Settings, and Metadata. Unsupported history/tool fields are rejected so a
// request cannot silently lose behavior at the protocol boundary.
func EncodeWarpRequest(req chatRequest, model, conversation string) ([]byte, error) {
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("%w: messages are required", ErrUnsupported)
	}
	if len(req.Tools) > 0 || req.ToolChoice != nil {
		return nil, fmt.Errorf("%w: Warp tool execution is not implemented", ErrUnsupported)
	}
	for key := range req.Raw {
		switch key {
		case "messages", "model", "stream":
		default:
			return nil, fmt.Errorf("%w: Warp request field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw, ok := req.Raw["model"]; ok {
		var requested string
		if json.Unmarshal(raw, &requested) != nil || (requested != "" && requested != model) {
			return nil, fmt.Errorf("%w: request model conflicts with selected model", ErrUnsupported)
		}
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		return nil, fmt.Errorf("%w: Warp direct mode accepts one user message per request", ErrUnsupported)
	}
	if len(req.Messages[0].ToolCalls) > 0 {
		return nil, fmt.Errorf("%w: Warp tool history is not implemented", ErrUnsupported)
	}
	query := flattenWarpMessages(req.Messages)
	if query == "" {
		return nil, fmt.Errorf("%w: message text is empty", ErrUnsupported)
	}
	modelBase := strings.ToLower(strings.TrimSpace(model))
	switch modelBase {
	case "auto", "warp-basic", "claude-4-sonnet", "claude-4-opus", "claude-4.1-opus", "gpt-5", "gpt-4o", "gpt-4.1", "o3", "o4-mini", "gemini-2.5-pro":
	default:
		return nil, fmt.Errorf("%w: Warp model %q is not in the pinned model set", ErrUnsupported, model)
	}
	inputQuery := encodeMessageField(1, encodeStringField(1, query))
	userInput := encodeMessageField(1, inputQuery)
	input := encodeMessageField(6, userInput)
	request := encodeMessageField(2, input)
	modelConfig := encodeMessageField(1, encodeStringField(1, modelBase), encodeStringField(2, "o3"), encodeStringField(3, "auto"))
	settings := append(modelConfig,
		bytes.Join([][]byte{
			encodeVarintField(2, 0), encodeVarintField(3, 0), encodeVarintField(4, 0),
			encodeVarintField(5, 0), encodeVarintField(6, 0), encodeVarintField(7, 0),
			encodeVarintField(8, 0), encodeVarintField(10, 0), encodeVarintField(11, 1),
			encodeVarintField(12, 0), encodeVarintField(13, 0)}, nil)...)
	request = append(request, encodeMessageField(3, settings)...)
	request = append(request, encodeMessageField(4, encodeStringField(1, conversation))...)
	if int64(len(request)) > maxRequestBytes {
		return nil, fmt.Errorf("%w: Warp request exceeds byte limit", ErrUnsupported)
	}
	return request, nil
}

func flattenWarpMessages(messages []chatMessage) string {
	if len(messages) != 1 {
		return ""
	}
	return messages[0].Content
}

func (c *Client) doWarp(ctx context.Context, token, model string, req chatRequest, s *session) (*http.Response, error) {
	endpoint, err := warpEndpoint(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	body, err := EncodeWarpRequest(req, model, s.conversation)
	if err != nil {
		return nil, err
	}
	h := http.Header{
		"Accept":                []string{"text/event-stream"},
		"Authorization":         []string{"Bearer " + token},
		"Content-Type":          []string{"application/x-protobuf"},
		"X-Warp-Client-Version": []string{warpClientVersion},
		"X-Warp-OS-Category":    []string{"Windows"},
		"X-Warp-OS-Name":        []string{"Windows"},
		"X-Warp-OS-Version":     []string{warpOSVersion},
	}
	return post(ctx, c.http, endpoint, body, h)
}

type warpUsage struct{ input, output, cacheRead, cacheWrite uint64 }

func warpToSSE(ctx context.Context, r io.Reader, model string) ([]byte, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 1<<20)
	var out bytes.Buffer
	var data strings.Builder
	var inputTotal int64
	id := "chatcmpl-warp-" + randomUUID()
	created := time.Now().Unix()
	role := false
	finished := false
	seenEvent := false
	var usage warpUsage
	usageSeen := false
	finishReason := "stop"
	process := func(encoded string) error {
		encoded = strings.TrimSpace(encoded)
		if encoded == "" || encoded == "[DONE]" {
			return nil
		}
		raw, err := decodeWarpPayload(encoded)
		if err != nil {
			return fmt.Errorf("%w: malformed Warp SSE payload", ErrTruncated)
		}
		fields, err := decodeProtoFields(raw)
		if err != nil {
			return fmt.Errorf("%w: Warp response protobuf: %v", ErrTruncated, err)
		}
		seenEvent = true
		for _, event := range fields {
			switch event.number {
			case 2:
				if event.wire != 2 {
					continue
				}
				if err := warpClientActions(&out, event.bytes, id, created, model, &role); err != nil {
					return err
				}
			case 3:
				if event.wire != 2 {
					continue
				}
				var reason string
				var u warpFinished
				reason, u, err = decodeWarpFinished(event.bytes)
				if err != nil {
					return err
				}
				if reason != "" {
					finishReason = reason
				}
				if u.seen {
					usage, usageSeen = u.usage, true
				}
				finished = true
			}
		}
		return nil
	}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSuffix(sc.Text(), "\r")
		inputTotal += int64(len(line) + 1)
		if inputTotal > maxEventBytes {
			return nil, fmt.Errorf("%w: Warp response exceeds byte limit", ErrUnsupported)
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			continue
		}
		if line != "" {
			continue
		}
		if data.Len() > 0 {
			encoded := data.String()
			data.Reset()
			if err := process(encoded); err != nil {
				return nil, err
			}
			if int64(out.Len()) > maxOutputBytes {
				return nil, fmt.Errorf("%w: Warp output exceeds byte limit", ErrUnsupported)
			}
		}
		if finished {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if data.Len() > 0 && !finished {
		if err := process(data.String()); err != nil {
			return nil, err
		}
	}
	if !seenEvent {
		return nil, fmt.Errorf("%w: Warp stream contained no events", ErrTruncated)
	}
	if !finished {
		return nil, fmt.Errorf("%w: Warp stream has no finished event", ErrTruncated)
	}
	if !role {
		return nil, fmt.Errorf("%w: Warp stream has no assistant output", ErrTruncated)
	}
	terminal := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason}}}
	if usageSeen {
		terminal["usage"] = map[string]any{"prompt_tokens": usage.input, "completion_tokens": usage.output, "total_tokens": usage.input + usage.output}
	}
	appendSSE(&out, terminal)
	out.WriteString("data: [DONE]\n\n")
	if int64(out.Len()) > maxOutputBytes {
		return nil, fmt.Errorf("%w: Warp output exceeds byte limit", ErrUnsupported)
	}
	return out.Bytes(), nil
}

func decodeWarpPayload(encoded string) ([]byte, error) {
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, encoded)
	if compact == "" {
		return nil, io.ErrUnexpectedEOF
	}
	if decoded, err := hex.DecodeString(compact); err == nil {
		return decoded, nil
	}
	padding := strings.Repeat("=", (4-len(compact)%4)%4)
	if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(compact, "=")); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(compact + padding)
}

func warpClientActions(out *bytes.Buffer, data []byte, id string, created int64, model string, role *bool) error {
	fields, err := decodeProtoFields(data)
	if err != nil {
		return fmt.Errorf("%w: Warp client actions: %v", ErrTruncated, err)
	}
	for _, list := range fields {
		if list.number != 1 || list.wire != 2 {
			continue
		}
		actions, err := decodeProtoFields(list.bytes)
		if err != nil {
			return fmt.Errorf("%w: Warp client action: %v", ErrTruncated, err)
		}
		for _, action := range actions {
			if action.wire != 2 || (action.number != 3 && action.number != 5) {
				continue
			}
			if err := warpActionMessage(out, action.bytes, action.number, id, created, model, role); err != nil {
				return err
			}
		}
	}
	return nil
}

func warpActionMessage(out *bytes.Buffer, data []byte, actionNumber int, id string, created int64, model string, role *bool) error {
	fields, err := decodeProtoFields(data)
	if err != nil {
		return fmt.Errorf("%w: Warp action message: %v", ErrTruncated, err)
	}
	messageNumber := 1
	if actionNumber == 3 {
		messageNumber = 2
	}
	for _, field := range fields {
		if field.number != messageNumber || field.wire != 2 {
			continue
		}
		variants, err := decodeProtoFields(field.bytes)
		if err != nil {
			return fmt.Errorf("%w: malformed Warp message", ErrTruncated)
		}
		for _, variant := range variants {
			if variant.number == 4 {
				return fmt.Errorf("%w: Warp requested unsupported tool execution", ErrUnsupported)
			}
			if variant.number != 3 || variant.wire != 2 {
				continue
			}
			output, err := decodeProtoFields(variant.bytes)
			if err != nil {
				return fmt.Errorf("%w: malformed Warp output", ErrTruncated)
			}
			for _, text := range output {
				if text.wire != 2 || (text.number != 1 && text.number != 2) || len(text.bytes) == 0 {
					continue
				}
				if !*role {
					appendCursorRole(out, id, created, model)
					*role = true
				}
				key := "content"
				if text.number == 2 {
					key = "reasoning_content"
				}
				appendCursorField(out, id, created, model, key, string(text.bytes))
			}
		}
	}
	return nil
}

type warpFinished struct {
	seen  bool
	usage warpUsage
}

func decodeWarpFinished(data []byte) (string, warpFinished, error) {
	fields, err := decodeProtoFields(data)
	if err != nil {
		return "", warpFinished{}, fmt.Errorf("%w: Warp finished event: %v", ErrTruncated, err)
	}
	result := warpFinished{}
	reason := "stop"
	reasons := 0
	for _, field := range fields {
		if field.number >= 1 && field.number <= 7 {
			reasons++
		}
		switch field.number {
		case 1:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			reason = "stop"
		case 2:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			reason = "stop"
		case 3:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			reason = "length"
		case 4:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			return "", result, fmt.Errorf("Warp upstream quota exceeded")
		case 5:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			reason = "length"
		case 6:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished reason", ErrTruncated)
			}
			return "", result, fmt.Errorf("Warp upstream model unavailable")
		case 7:
			if field.wire != 2 {
				return "", result, fmt.Errorf("%w: malformed Warp finished error", ErrTruncated)
			}
			return "", result, fmt.Errorf("coding final adapter: upstream error: request failed")
		case 8:
			if field.wire != 2 {
				continue
			}
			usage, err := decodeWarpUsage(field.bytes)
			if err != nil {
				return "", result, err
			}
			result.usage.input += usage.input
			result.usage.output += usage.output
			result.usage.cacheRead += usage.cacheRead
			result.usage.cacheWrite += usage.cacheWrite
			result.seen = true
		}
	}
	if reasons != 1 {
		return "", result, fmt.Errorf("%w: Warp finished event requires one reason", ErrTruncated)
	}
	return reason, result, nil
}

func decodeWarpUsage(data []byte) (warpUsage, error) {
	fields, err := decodeProtoFields(data)
	if err != nil {
		return warpUsage{}, fmt.Errorf("%w: Warp token usage: %v", ErrTruncated, err)
	}
	var usage warpUsage
	for _, field := range fields {
		if field.wire != 0 {
			continue
		}
		switch field.number {
		case 2:
			usage.input = field.varint
		case 3:
			usage.output = field.varint
		case 4:
			usage.cacheRead = field.varint
		case 5:
			usage.cacheWrite = field.varint
		}
	}
	return usage, nil
}
