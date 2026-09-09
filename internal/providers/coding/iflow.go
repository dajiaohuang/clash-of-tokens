package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	wire "clash-of-tokens/internal/protocol"
)

// iFlow exposes the Open Platform chat-completions route documented for the
// iFlow CLI. It is kept as its own adapter because the product endpoint,
// authentication, user agent, and response conversion are part of the
// product contract rather than a generic provider alias.
func (c *Client) doIFlow(ctx context.Context, protocol, model string, stream bool, body []byte, _ http.Header) (*http.Response, error) {
	if protocol != "chat" && protocol != "messages" {
		return nil, ErrProtocol
	}
	requestBody, err := iFlowRequest(protocol, model, stream, body)
	if err != nil {
		return nil, err
	}
	endpoint, err := baseURL(c.source, "/chat/completions")
	if err != nil {
		return nil, err
	}
	req, err := c.request(ctx, http.MethodPost, endpoint, requestBody)
	if err != nil {
		return nil, err
	}
	key := credential(c.source)
	setBearer(req, key)
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("User-Agent", "iFlow-CLI/0.2.4")
	resp, err := c.execute(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	if protocol == "chat" {
		if stream {
			setResponseBody(resp, &iFlowChatStream{body: resp.Body, events: wire.NewSSEReader(resp.Body, 16<<20)}, "text/event-stream")
		} else {
			data, readErr := readJSONBody(resp.Body, 16<<20)
			if readErr != nil {
				return nil, readErr
			}
			setResponseBody(resp, io.NopCloser(bytes.NewReader(data)), "application/json")
		}
		return resp, nil
	}
	if stream {
		setResponseBody(resp, &iFlowMessagesStream{body: resp.Body, events: wire.NewSSEReader(resp.Body, 16<<20), model: model}, "text/event-stream")
		return resp, nil
	}
	data, readErr := readJSONBody(resp.Body, 16<<20)
	if readErr != nil {
		return nil, readErr
	}
	converted, convertErr := iFlowChatToMessages(data, model)
	if convertErr != nil {
		return nil, convertErr
	}
	setResponseBody(resp, io.NopCloser(bytes.NewReader(converted)), "application/json")
	return resp, nil
}

func iFlowRequest(protocol, model string, stream bool, body []byte) ([]byte, error) {
	root, err := strictObject(body, "iFlow request")
	if err != nil {
		return nil, err
	}
	allowed := map[string]struct{}{
		"model": {}, "messages": {}, "stream": {}, "max_tokens": {}, "max_completion_tokens": {},
		"temperature": {}, "top_p": {}, "top_k": {}, "n": {}, "stop": {}, "presence_penalty": {},
		"frequency_penalty": {}, "logit_bias": {}, "user": {}, "tools": {}, "tool_choice": {},
		"response_format": {}, "seed": {}, "parallel_tool_calls": {}, "reasoning_effort": {},
	}
	if protocol == "messages" {
		allowed = map[string]struct{}{
			"model": {}, "messages": {}, "max_tokens": {}, "max_tokens_to_sample": {}, "stream": {},
			"system": {}, "stop_sequences": {}, "temperature": {}, "top_p": {}, "top_k": {}, "tools": {},
			"tool_choice": {}, "metadata": {},
		}
	}
	if err := rejectUnknown(root, allowed, "iFlow request"); err != nil {
		return nil, err
	}
	if err := validateMessages(root["messages"], protocol); err != nil {
		return nil, fmt.Errorf("iFlow request: %w", err)
	}
	if protocol == "messages" {
		if err := validateIFlowAnthropicMessages(root["messages"]); err != nil {
			return nil, fmt.Errorf("iFlow request: %w", err)
		}
		if err := validateAnthropicSystem(root["system"]); err != nil {
			return nil, fmt.Errorf("iFlow request: %w", err)
		}
		if err := validateAnthropicTools(root["tools"]); err != nil {
			return nil, fmt.Errorf("iFlow request: %w", err)
		}
		root, err = anthropicToIFlow(root)
		if err != nil {
			return nil, err
		}
	}
	modelJSON, _ := json.Marshal(model)
	streamJSON, _ := json.Marshal(stream)
	root["model"] = modelJSON
	root["stream"] = streamJSON
	return json.Marshal(root)
}

func anthropicToIFlow(root map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	// Anthropic places system outside messages; iFlow follows OpenAI's system
	// message convention. Preserve all other fields, including tool schemas.
	if system, ok := root["system"]; ok {
		text := joinAnthropicText(system)
		if text == "" {
			return nil, errors.New("iFlow system content must contain text")
		}
		var messages []json.RawMessage
		if json.Unmarshal(root["messages"], &messages) != nil {
			return nil, errors.New("iFlow messages must be an array")
		}
		encodedMessage, _ := json.Marshal(map[string]any{"role": "system", "content": text})
		messages = append([]json.RawMessage{encodedMessage}, messages...)
		encodedMessages, err := json.Marshal(messages)
		if err != nil {
			return nil, errors.New("iFlow could not encode messages")
		}
		root["messages"] = encodedMessages
		delete(root, "system")
	}
	if maxTokens, ok := root["max_tokens_to_sample"]; ok {
		if _, exists := root["max_tokens"]; !exists {
			root["max_tokens"] = maxTokens
		}
		delete(root, "max_tokens_to_sample")
	}
	if stopSequences, ok := root["stop_sequences"]; ok {
		if _, exists := root["stop"]; !exists {
			root["stop"] = stopSequences
		}
		delete(root, "stop_sequences")
	}
	if tools := root["tools"]; len(tools) > 0 {
		converted, err := anthropicToolsToIFlow(tools)
		if err != nil {
			return nil, err
		}
		root["tools"] = converted
	}
	if choice := root["tool_choice"]; len(choice) > 0 {
		converted, err := anthropicToolChoiceToIFlow(choice)
		if err != nil {
			return nil, err
		}
		root["tool_choice"] = converted
	}
	return root, nil
}

func anthropicToolsToIFlow(raw json.RawMessage) (json.RawMessage, error) {
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return nil, errors.New("iFlow tools must be an array")
	}
	out := make([]map[string]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		name, description := tool["name"], tool["description"]
		fn := map[string]json.RawMessage{"name": name, "description": description, "parameters": tool["input_schema"]}
		encodedFn, _ := json.Marshal(fn)
		encodedType, _ := json.Marshal("function")
		out = append(out, map[string]json.RawMessage{"type": encodedType, "function": encodedFn})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, errors.New("iFlow could not encode tools")
	}
	return encoded, nil
}

func anthropicToolChoiceToIFlow(raw json.RawMessage) (json.RawMessage, error) {
	var choice map[string]json.RawMessage
	if json.Unmarshal(raw, &choice) != nil {
		return nil, errors.New("iFlow tool_choice must be an object")
	}
	typ := stringField(choice, "type")
	switch typ {
	case "auto":
		if err := rejectUnknown(choice, map[string]struct{}{"type": {}}, "iFlow tool_choice"); err != nil {
			return nil, err
		}
		return json.RawMessage(`"auto"`), nil
	case "any":
		if err := rejectUnknown(choice, map[string]struct{}{"type": {}}, "iFlow tool_choice"); err != nil {
			return nil, err
		}
		return json.RawMessage(`"required"`), nil
	case "tool":
		if err := rejectUnknown(choice, map[string]struct{}{"type": {}, "name": {}}, "iFlow tool_choice"); err != nil {
			return nil, err
		}
		name := stringField(choice, "name")
		if name == "" {
			return nil, errors.New("iFlow tool_choice tool name is required")
		}
		return json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": name}})
	default:
		return nil, errors.New("iFlow tool_choice type is unsupported")
	}
}

func validateIFlowAnthropicMessages(raw json.RawMessage) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return errors.New("iFlow messages must be an array")
	}
	for i, rawMessage := range messages {
		message, _ := rawObject(rawMessage)
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			if stringField(block, "type") != "text" {
				return fmt.Errorf("message %d content block %q cannot be represented by iFlow", i, stringField(block, "type"))
			}
		}
	}
	return nil
}

func readJSONBody(body io.ReadCloser, limit int64) ([]byte, error) {
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit || !json.Valid(data) {
		return nil, errors.New("coding upstream returned an invalid JSON response")
	}
	return data, nil
}

type iFlowChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"delta"`
		Message struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage any `json:"usage,omitempty"`
}

func iFlowChatToMessages(data []byte, model string) ([]byte, error) {
	var src iFlowChatResponse
	if err := json.Unmarshal(data, &src); err != nil {
		return nil, errors.New("iFlow returned an invalid chat response")
	}
	if len(src.Choices) == 0 {
		return nil, errors.New("iFlow response has no choices")
	}
	choice := src.Choices[0]
	content := ""
	if text, ok := choice.Message.Content.(string); ok {
		content = text
	} else if choice.Message.Content != nil {
		encoded, _ := json.Marshal(choice.Message.Content)
		content = string(encoded)
	}
	stop := choice.FinishReason
	if stop == "" {
		stop = "end_turn"
	}
	if stop == "stop" {
		stop = "end_turn"
	}
	return json.Marshal(map[string]any{
		"id":            src.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         firstNonEmpty(src.Model, model),
		"content":       []map[string]string{{"type": "text", "text": content}},
		"stop_reason":   stop,
		"stop_sequence": nil,
		"usage":         src.Usage,
	})
}

type iFlowChatStream struct {
	body    io.ReadCloser
	events  *wire.SSEReader
	pending []byte
	done    bool
}

func (s *iFlowChatStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 && !s.done {
		frame, err := s.events.Next()
		if err != nil {
			if err == io.EOF {
				s.done = true
				return 0, io.EOF
			}
			return 0, err
		}
		data := wire.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			s.done = true
			continue
		}
		if !json.Valid(data) {
			return 0, errors.New("iFlow returned an invalid SSE JSON event")
		}
		s.pending = append(s.pending[:0], data...)
		s.pending = append(s.pending, '\n', '\n')
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.done {
		return 0, io.EOF
	}
	return n, nil
}

func (s *iFlowChatStream) Close() error { return s.body.Close() }

type iFlowMessagesStream struct {
	body    io.ReadCloser
	events  *wire.SSEReader
	model   string
	pending []byte
	started bool
	done    bool
}

func (s *iFlowMessagesStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 && !s.done {
		frame, err := s.events.Next()
		if err != nil {
			if err == io.EOF {
				s.done = true
				return 0, io.EOF
			}
			return 0, err
		}
		data := wire.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			s.pending = append(s.pending[:0], "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"...)
			s.done = true
			continue
		}
		var chunk iFlowChatResponse
		if !json.Valid(data) || json.Unmarshal(data, &chunk) != nil {
			return 0, errors.New("iFlow returned an invalid SSE JSON event")
		}
		if !s.started {
			id := chunk.ID
			if id == "" {
				id = "msg_iflow"
			}
			start := map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "content": []any{}, "model": firstNonEmpty(chunk.Model, s.model)}}
			encoded, _ := json.Marshal(start)
			s.pending = append(s.pending, "event: message_start\ndata: "...)
			s.pending = append(s.pending, encoded...)
			s.pending = append(s.pending, '\n', '\n')
			s.started = true
		}
		if len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Message.Content
			if content == nil {
				content = chunk.Choices[0].Delta.Content
			}
			text, _ := content.(string)
			if text == "" && content != nil {
				if encoded, marshalErr := json.Marshal(content); marshalErr == nil {
					var blocks []map[string]json.RawMessage
					if json.Unmarshal(encoded, &blocks) == nil {
						text = stringContent(encoded)
					}
				}
			}
			if text != "" {
				delta := map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}}
				encoded, _ := json.Marshal(delta)
				s.pending = append(s.pending, "event: content_block_delta\ndata: "...)
				s.pending = append(s.pending, encoded...)
				s.pending = append(s.pending, '\n', '\n')
			}
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.done {
		return 0, io.EOF
	}
	return n, nil
}

func (s *iFlowMessagesStream) Close() error { return s.body.Close() }

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
