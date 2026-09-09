package codingmore

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"clash-of-tokens/internal/config"
)

const (
	augmentUserAgent = "Augment.vscode-augment/0.492.0 (darwin; arm64; 24.2.0) vscode/1.98.2"
	maxAugmentLine   = 1 << 20
)

var errAugmentBlocked = errors.New("coding more adapter: Augment rejected the request")

type augmentMessage struct {
	role    string
	content string
}

type augmentRequest struct {
	ChatHistory       []augmentChatHistory `json:"chat_history"`
	Message           string               `json:"message"`
	AgentMemories     string               `json:"agent_memories"`
	Mode              string               `json:"mode"`
	Prefix            string               `json:"prefix"`
	Suffix            string               `json:"suffix"`
	Lang              string               `json:"lang"`
	Path              string               `json:"path"`
	UserGuidelines    string               `json:"user_guidelines"`
	Blobs             augmentBlobs         `json:"blobs"`
	UserGuidedBlobs   []any                `json:"user_guided_blobs"`
	ExternalSourceIDs []any                `json:"external_source_ids"`
	FeatureDetection  augmentFeatureFlags  `json:"feature_detection_flags"`
	ToolDefinitions   []any                `json:"tool_definitions"`
	Nodes             []any                `json:"nodes"`
}

type augmentChatHistory struct {
	ResponseText   string `json:"response_text"`
	RequestMessage string `json:"request_message"`
	RequestID      string `json:"request_id"`
	RequestNodes   []any  `json:"request_nodes"`
	ResponseNodes  []any  `json:"response_nodes"`
}

type augmentBlobs struct {
	CheckpointID string `json:"checkpoint_id"`
	AddedBlobs   []any  `json:"added_blobs"`
	DeletedBlobs []any  `json:"deleted_blobs"`
}

type augmentFeatureFlags struct {
	SupportRawOutput bool `json:"support_raw_output"`
}

type augmentResponseLine struct {
	text string
	done bool
}

func (c *Client) doAugment(ctx context.Context, model string, stream bool, body []byte, headers http.Header, token string) (*http.Response, error) {
	if model != "default" {
		return nil, fmt.Errorf("%w: Augment CHAT mode has no model selector; use default", ErrUnsupported)
	}
	request, err := decodeAugmentRequest(body, model)
	if err != nil {
		return nil, err
	}

	key := headerValue(headers, "X-COT-Session")
	s, err := c.session(key)
	if err != nil {
		return nil, err
	}
	if err := s.gate.Lock(ctx); err != nil {
		return nil, err
	}
	locked := true
	defer func() {
		if locked {
			s.gate.Unlock()
		}
	}()

	endpoint, err := augmentEndpoint(c.source)
	if err != nil {
		return nil, err
	}
	payload, err := buildAugmentRequest(request, s.conversation)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("coding more adapter: cannot encode Augment request")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("coding more adapter: invalid Augment request")
	}
	req.GetBody = nil
	req.ContentLength = int64(len(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", augmentUserAgent)
	req.Header.Set("x-api-version", "2")
	req.Header.Set("x-request-id", randomUUID())
	req.Header.Set("x-request-session-id", s.conversation)

	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("coding more adapter: Augment upstream transport failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, nil
	}

	if stream {
		response.Header.Set("Content-Type", "text/event-stream")
		response.Header.Set("Cache-Control", "no-cache")
		response.ContentLength = -1
		response.Header.Del("Content-Length")
		response.Body = &responseBody{
			ReadCloser: newAugmentStream(ctx, response.Body, model),
			release:    s.gate.Unlock,
		}
		locked = false
		if key != "" {
			response.Header.Set("X-COT-Session", key)
		}
		return response, nil
	}

	result, err := collectAugment(ctx, response.Body, model)
	if err != nil {
		return nil, err
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return nil, errors.New("coding more adapter: cannot encode Augment response")
	}
	response.Body = io.NopCloser(bytes.NewReader(encodedResult))
	response.ContentLength = int64(len(encodedResult))
	response.Header.Del("Content-Length")
	response.Header.Set("Content-Type", "application/json")
	if key != "" {
		response.Header.Set("X-COT-Session", key)
	}
	return response, nil
}

func augmentEndpoint(source config.Source) (string, error) {
	// endpointBase owns the HTTPS, host, and query validation shared by the
	// CodeWhisperer adapter. Augment stores a tenant root rather than a region
	// host, so preserve any configured path prefix before adding chat-stream.
	base, err := endpointBase(source)
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(base, "/chat-stream") {
		return base, nil
	}
	return base + "/chat-stream", nil
}

func decodeAugmentRequest(body []byte, model string) ([]augmentMessage, error) {
	var root map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&root); err != nil || root == nil {
		return nil, fmt.Errorf("%w: Augment request must be a JSON object", ErrUnsupported)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: Augment request must contain one JSON object", ErrUnsupported)
	}
	for key := range root {
		if key != "model" && key != "messages" && key != "stream" {
			return nil, fmt.Errorf("%w: Augment field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw := root["model"]; len(raw) != 0 {
		var supplied string
		if json.Unmarshal(raw, &supplied) != nil || strings.TrimSpace(supplied) == "" {
			return nil, fmt.Errorf("%w: model must be a non-empty string", ErrUnsupported)
		}
	}
	if raw := root["stream"]; len(raw) != 0 {
		var supplied bool
		if json.Unmarshal(raw, &supplied) != nil {
			return nil, fmt.Errorf("%w: stream must be boolean", ErrUnsupported)
		}
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(root["messages"], &rawMessages); err != nil || len(rawMessages) == 0 {
		return nil, fmt.Errorf("%w: Augment messages must be a non-empty array", ErrUnsupported)
	}
	messages := make([]augmentMessage, 0, len(rawMessages))
	for i, raw := range rawMessages {
		var message map[string]json.RawMessage
		if err := json.Unmarshal(raw, &message); err != nil || message == nil {
			return nil, fmt.Errorf("%w: Augment message %d must be an object", ErrUnsupported, i)
		}
		for key := range message {
			if key != "role" && key != "content" {
				return nil, fmt.Errorf("%w: Augment message %d field %q is unsupported", ErrUnsupported, i, key)
			}
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil || role == "" {
			return nil, fmt.Errorf("%w: Augment message %d role is required", ErrUnsupported, i)
		}
		if role != "user" && role != "assistant" {
			return nil, fmt.Errorf("%w: Augment message %d role %q is unsupported", ErrUnsupported, i, role)
		}
		content, err := augmentTextContent(message["content"], fmt.Sprintf("Augment message %d content", i))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("%w: Augment message %d content is empty", ErrUnsupported, i)
		}
		if len(messages) > 0 && messages[len(messages)-1].role == role {
			return nil, fmt.Errorf("%w: Augment messages must alternate user and assistant turns", ErrUnsupported)
		}
		messages = append(messages, augmentMessage{role: role, content: content})
	}
	if messages[0].role != "user" || messages[len(messages)-1].role != "user" {
		return nil, fmt.Errorf("%w: Augment conversation must begin and end with a user message", ErrUnsupported)
	}
	_ = model // model selects product mode in doAugment; it is not a body field.
	return messages, nil
}

func augmentTextContent(raw json.RawMessage, label string) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return "", fmt.Errorf("%w: %s must be text", ErrUnsupported, label)
	}
	var out strings.Builder
	for i, block := range blocks {
		for key := range block {
			if key != "type" && key != "text" {
				return "", fmt.Errorf("%w: %s block %d field %q is unsupported", ErrUnsupported, label, i, key)
			}
		}
		var typ string
		if json.Unmarshal(block["type"], &typ) != nil || typ != "text" {
			return "", fmt.Errorf("%w: %s block %d is not text", ErrUnsupported, label, i)
		}
		var value string
		if json.Unmarshal(block["text"], &value) != nil {
			return "", fmt.Errorf("%w: %s block %d text is required", ErrUnsupported, label, i)
		}
		out.WriteString(value)
	}
	return out.String(), nil
}

func buildAugmentRequest(messages []augmentMessage, conversation string) (augmentRequest, error) {
	if len(messages) == 0 || messages[len(messages)-1].role != "user" {
		return augmentRequest{}, fmt.Errorf("%w: Augment request has no final user turn", ErrUnsupported)
	}
	history := make([]augmentChatHistory, 0, (len(messages)-1)/2)
	for i := 0; i+1 < len(messages)-1; i += 2 {
		user, assistant := messages[i], messages[i+1]
		history = append(history, augmentChatHistory{
			RequestMessage: user.content,
			ResponseText:   assistant.content,
			RequestID:      randomUUID(),
			RequestNodes:   []any{},
			ResponseNodes:  []any{map[string]any{"id": 0, "type": 0, "content": assistant.content, "tool_use": map[string]any{"tool_use_id": "", "tool_name": "", "input_json": ""}, "agent_memory": map[string]any{"content": ""}}},
		})
	}
	last := messages[len(messages)-1]
	return augmentRequest{
		ChatHistory:       history,
		Message:           last.content,
		AgentMemories:     "",
		Mode:              "CHAT",
		Prefix:            "",
		Suffix:            "",
		Lang:              "",
		Path:              "",
		UserGuidelines:    "",
		Blobs:             augmentBlobs{CheckpointID: randomUUID(), AddedBlobs: []any{}, DeletedBlobs: []any{}},
		UserGuidedBlobs:   []any{},
		ExternalSourceIDs: []any{},
		FeatureDetection:  augmentFeatureFlags{SupportRawOutput: true},
		ToolDefinitions:   []any{},
		Nodes:             []any{},
	}, nil
}

type augmentStream struct {
	body     io.ReadCloser
	reader   *bufio.Reader
	ctx      context.Context
	model    string
	id       string
	created  int64
	pending  []byte
	total    int64
	started  bool
	done     bool
	sawEvent bool
}

func newAugmentStream(ctx context.Context, body io.ReadCloser, model string) *augmentStream {
	return &augmentStream{body: body, reader: bufio.NewReaderSize(body, maxAugmentLine), ctx: ctx, model: model, id: randomID("chatcmpl-"), created: timeNowUnix()}
}

func (s *augmentStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(s.pending) == 0 && !s.done {
		if s.ctx != nil && s.ctx.Err() != nil {
			return 0, s.ctx.Err()
		}
		event, err := nextAugmentLine(s.reader, &s.total)
		if err != nil {
			if s.ctx != nil && s.ctx.Err() != nil {
				return 0, s.ctx.Err()
			}
			if err == io.EOF {
				if !s.sawEvent || !s.done {
					return 0, ErrTruncated
				}
				s.done = true
				continue
			}
			return 0, err
		}
		s.sawEvent = true
		if !s.started {
			s.appendStart()
		}
		if event.text != "" {
			s.appendContent(event.text)
		}
		if event.done {
			s.appendFinish()
			s.done = true
			_ = s.body.Close()
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.done {
		return 0, io.EOF
	}
	return n, nil
}

func (s *augmentStream) appendStart() {
	s.started = true
	s.appendSSE(map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}})
}

func (s *augmentStream) appendContent(content string) {
	s.appendSSE(map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": content}, "finish_reason": nil}}})
}

func (s *augmentStream) appendFinish() {
	s.appendSSE(map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	s.pending = append(s.pending, []byte("data: [DONE]\n\n")...)
}

func (s *augmentStream) appendSSE(value any) {
	encoded, _ := json.Marshal(value)
	s.pending = append(s.pending, []byte("data: ")...)
	s.pending = append(s.pending, encoded...)
	s.pending = append(s.pending, '\n', '\n')
}

func (s *augmentStream) Close() error { return s.body.Close() }

func nextAugmentLine(reader *bufio.Reader, total *int64) (augmentResponseLine, error) {
	for {
		line, err := readAugmentLine(reader)
		*total += int64(len(line))
		if *total > maxEventBytes {
			return augmentResponseLine{}, fmt.Errorf("%w: Augment response exceeds byte limit", ErrUnsupported)
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			if err != nil {
				return augmentResponseLine{}, err
			}
			continue
		}
		if err != nil && err != io.EOF {
			return augmentResponseLine{}, err
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(trimmed, &root) != nil || root == nil {
			return augmentResponseLine{}, fmt.Errorf("%w: Augment response line is not JSON", ErrTruncated)
		}
		for key := range root {
			if key != "text" && key != "done" {
				return augmentResponseLine{}, fmt.Errorf("%w: Augment response field %q is unsupported", ErrUnsupported, key)
			}
		}
		event := augmentResponseLine{}
		if raw, ok := root["text"]; ok {
			if json.Unmarshal(raw, &event.text) != nil {
				return augmentResponseLine{}, fmt.Errorf("%w: Augment response text is not a string", ErrTruncated)
			}
		}
		if raw, ok := root["done"]; ok {
			if json.Unmarshal(raw, &event.done) != nil {
				return augmentResponseLine{}, fmt.Errorf("%w: Augment response done is not boolean", ErrTruncated)
			}
		}
		if _, textPresent := root["text"]; !textPresent {
			if _, donePresent := root["done"]; !donePresent {
				return augmentResponseLine{}, fmt.Errorf("%w: Augment response line is empty", ErrTruncated)
			}
		}
		if strings.Contains(event.text, "Request blocked. Please reach out to support@augmentcode.com") {
			return augmentResponseLine{}, errAugmentBlocked
		}
		return event, nil
	}
}

func readAugmentLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > maxAugmentLine-len(line) {
			return nil, errors.New("Augment response line exceeds byte limit")
		}
		line = append(line, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return line, err
	}
}

func collectAugment(ctx context.Context, body io.ReadCloser, model string) (map[string]any, error) {
	defer body.Close()
	reader := bufio.NewReaderSize(body, maxAugmentLine)
	var total int64
	var text strings.Builder
	sawEvent := false
	done := false
	for {
		event, err := nextAugmentLine(reader, &total)
		if err != nil {
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == io.EOF {
				if !sawEvent || !done {
					return nil, ErrTruncated
				}
				break
			}
			return nil, err
		}
		sawEvent = true
		text.WriteString(event.text)
		if event.done {
			done = true
			break
		}
	}
	return map[string]any{
		"id": sRandomAugmentID(), "object": "chat.completion", "created": timeNowUnix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text.String()}, "finish_reason": "stop"}},
	}, nil
}

func sRandomAugmentID() string { return randomID("chatcmpl-") }
