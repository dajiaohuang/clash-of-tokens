package coding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
)

func (c *Client) doKiro(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if protocol != "messages" && protocol != "chat" {
		return nil, ErrProtocol
	}
	requestBody, session, err := kiroRequest(protocol, model, body, cloneHeaderValue(headers, "X-COT-Session"))
	if err != nil {
		return nil, &requestError{err}
	}
	endpoint, err := baseURL(c.source, "/generateAssistantResponse")
	if err != nil {
		return nil, err
	}
	req, err := c.request(ctx, http.MethodPost, endpoint, requestBody)
	if err != nil {
		return nil, err
	}
	setBearer(req, credential(c.source))
	req.Header.Set("Accept", "application/vnd.amazon.eventstream")
	req.Header.Set("x-amzn-kiro-agent-mode", "spec")
	req.Header.Set("x-amz-user-agent", "aws-sdk-js/1.0.18 KiroIDE-0.2.13")
	req.Header.Set("User-Agent", "aws-sdk-js/1.0.18 ua/2.1 os/other lang/js api/codewhispererstreaming/1.0.18 m/E KiroIDE-0.2.13")
	resp, err := c.execute(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	if session != "" {
		resp.Header.Set("X-COT-Session", session)
	}
	if stream {
		setResponseBody(resp, &kiroStream{body: resp.Body, events: newEventReader(resp.Body), protocol: protocol, model: model}, "text/event-stream")
		return resp, nil
	}
	text, id, err := collectKiro(resp.Body)
	if err != nil {
		return nil, err
	}
	converted, err := kiroResponse(protocol, model, id, text)
	if err != nil {
		return nil, err
	}
	setResponseBody(resp, io.NopCloser(bytes.NewReader(converted)), "application/json")
	return resp, nil
}

func kiroRequest(protocol, model string, body []byte, session string) ([]byte, string, error) {
	root, err := strictObject(body, "Kiro request")
	if err != nil {
		return nil, "", err
	}
	// CodeWhisperer carries the prompt, images, model, and session inside its
	// conversationState envelope. Generation controls, system instructions, and
	// native tool round-trips are not implemented here; reject them explicitly.
	allowed := map[string]struct{}{"model": {}, "messages": {}, "stream": {}}
	if err := rejectUnknown(root, allowed, "Kiro request"); err != nil {
		return nil, "", err
	}
	if err := validateMessages(root["messages"], protocol); err != nil {
		return nil, "", fmt.Errorf("Kiro request: %w", err)
	}
	if protocol != "messages" {
		if err := validateKiroChatMessages(root["messages"]); err != nil {
			return nil, "", err
		}
	}
	if err := validateKiroFinalMessage(root["messages"]); err != nil {
		return nil, "", err
	}
	if err := validateKiroHistory(root["messages"]); err != nil {
		return nil, "", err
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(root["messages"], &messages); err != nil || len(messages) == 0 {
		return nil, "", errors.New("Kiro request requires a non-empty messages array")
	}
	if strings.TrimSpace(session) == "" {
		session = randomID("conv-")
	}
	conversation := make([]any, 0, len(messages))
	for _, raw := range messages {
		m, err := rawObject(raw)
		if err != nil {
			return nil, "", errors.New("Kiro message must be an object")
		}
		role := stringField(m, "role")
		content := stringContent(m["content"])
		if role == "assistant" {
			conversation = append(conversation, map[string]any{"assistantResponseMessage": map[string]any{"content": content, "toolUses": []any{}}})
		} else {
			conversation = append(conversation, map[string]any{"userInputMessage": map[string]any{"content": content, "modelId": model, "origin": "AI_EDITOR"}})
		}
	}
	last, err := rawObject(messages[len(messages)-1])
	if err != nil {
		return nil, "", errors.New("Kiro final message must be an object")
	}
	lastContent := stringContent(last["content"])
	images := kiroImages(last["content"])
	if strings.TrimSpace(lastContent) == "" && len(images) == 0 {
		return nil, "", errors.New("Kiro final user message must contain text or an image")
	}
	state := map[string]any{
		"agentContinuationId": randomID("agent-"),
		"agentTaskType":       "vibe",
		"chatTriggerType":     "MANUAL",
		"currentMessage": map[string]any{"userInputMessage": map[string]any{
			"userInputMessageContext": map[string]any{"tools": []any{}, "toolResults": []any{}},
			"content":                 lastContent,
			"modelId":                 model,
			"images":                  images,
			"origin":                  "AI_EDITOR",
		}},
		"conversationId": session,
		"history":        conversation[:max(0, len(conversation)-1)],
	}
	encoded, err := json.Marshal(map[string]any{"conversationState": state})
	return encoded, session, err
}

func kiroImages(raw json.RawMessage) []any {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return []any{}
	}
	out := make([]any, 0)
	for _, block := range blocks {
		typ := stringField(block, "type")
		if typ == "image_url" {
			imageURL, err := rawObject(block["image_url"])
			if err != nil {
				continue
			}
			media, data, err := decodeKiroImageDataURL(stringField(imageURL, "url"))
			if err != nil {
				continue
			}
			format := "png"
			if idx := strings.LastIndex(media, "/"); idx >= 0 && idx+1 < len(media) {
				format = media[idx+1:]
			}
			out = append(out, map[string]any{"format": format, "source": map[string]string{"bytes": data}})
			continue
		}
		if typ != "image" && typ != "input_image" {
			continue
		}
		source, _ := rawObject(block["source"])
		data := stringField(source, "data")
		if data == "" {
			data = stringField(source, "bytes")
		}
		if strings.Contains(data, ",") {
			data = strings.SplitN(data, ",", 2)[1]
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			continue
		}
		media := stringField(source, "media_type")
		if media == "" {
			media = stringField(source, "mediaType")
		}
		format := "png"
		if idx := strings.LastIndex(media, "/"); idx >= 0 && idx+1 < len(media) {
			format = media[idx+1:]
		}
		out = append(out, map[string]any{"format": format, "source": map[string]string{"bytes": data}})
	}
	return out
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errors.New("not an object")
	}
	return object, nil
}

func stringField(object map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(object[key], &value)
	return value
}

func stringContent(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var out strings.Builder
	for _, block := range blocks {
		if text := stringField(block, "text"); text != "" {
			out.WriteString(text)
		}
		if text := stringField(block, "content"); text != "" && stringField(block, "type") == "text" {
			out.WriteString(text)
		}
	}
	return out.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func kiroResponse(protocol, model, id, text string) ([]byte, error) {
	if id == "" {
		id = randomID("msg-")
	}
	if protocol == "messages" {
		return json.Marshal(map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model,
			"content": []map[string]string{{"type": "text", "text": text}}, "stop_reason": "end_turn", "stop_sequence": nil,
		})
	}
	return json.Marshal(map[string]any{
		"id": id, "object": "chat.completion", "model": model,
		"choices": []map[string]any{{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}},
	})
}

type kiroStream struct {
	body     io.ReadCloser
	events   *eventReader
	protocol string
	model    string
	pending  []byte
	id       string
	started  bool
	terminal bool
	sawFrame bool
	done     bool
}

func (s *kiroStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 && !s.done {
		frame, err := s.events.Next()
		if err != nil {
			if err == io.EOF {
				if !s.sawFrame {
					return 0, errors.New("Kiro returned an empty event stream")
				}
				if !s.terminal {
					s.appendTerminal()
				}
				s.done = true
				continue
			}
			return 0, err
		}
		s.sawFrame = true
		text, id, terminal, decodeErr := decodeKiroEvent(frame)
		if decodeErr != nil {
			return 0, decodeErr
		}
		if id != "" && !s.started {
			s.id = id
		}
		if !s.started {
			s.appendStart()
		}
		if text != "" {
			s.appendText(text)
		}
		if terminal && !s.terminal {
			s.appendTerminal()
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.done {
		return 0, io.EOF
	}
	return n, nil
}

func (s *kiroStream) appendStart() {
	s.started = true
	if s.id == "" {
		s.id = randomID("msg-")
	}
	id, _ := json.Marshal(s.id)
	model, _ := json.Marshal(s.model)
	if s.protocol == "messages" {
		s.pending = append(s.pending, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":"...)
		s.pending = append(s.pending, id...)
		s.pending = append(s.pending, ",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":"...)
		s.pending = append(s.pending, model...)
		s.pending = append(s.pending, "}}\n\n"...)
		s.pending = append(s.pending, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"...)
		return
	}
	s.pending = append(s.pending, "data: {\"id\":"...)
	s.pending = append(s.pending, id...)
	s.pending = append(s.pending, ",\"object\":\"chat.completion.chunk\",\"model\":"...)
	s.pending = append(s.pending, model...)
	s.pending = append(s.pending, ",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"...)
}

func (s *kiroStream) appendText(text string) {
	if s.protocol == "messages" {
		value, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}})
		s.pending = append(s.pending, "event: content_block_delta\ndata: "...)
		s.pending = append(s.pending, value...)
		s.pending = append(s.pending, '\n', '\n')
		return
	}
	value, _ := json.Marshal(map[string]any{"id": s.id, "object": "chat.completion.chunk", "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": nil}}})
	s.pending = append(s.pending, "data: "...)
	s.pending = append(s.pending, value...)
	s.pending = append(s.pending, '\n', '\n')
}

func (s *kiroStream) appendTerminal() {
	s.terminal = true
	if s.protocol == "messages" {
		s.pending = append(s.pending, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"...)
		s.pending = append(s.pending, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"...)
		return
	}
	id, _ := json.Marshal(s.id)
	model, _ := json.Marshal(s.model)
	s.pending = append(s.pending, "data: {\"id\":"...)
	s.pending = append(s.pending, id...)
	s.pending = append(s.pending, ",\"object\":\"chat.completion.chunk\",\"model\":"...)
	s.pending = append(s.pending, model...)
	s.pending = append(s.pending, ",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"...)
}

func (s *kiroStream) Close() error { return s.body.Close() }

func collectKiro(body io.ReadCloser) (string, string, error) {
	defer body.Close()
	reader := newEventReader(body)
	var text strings.Builder
	id := ""
	saw := false
	for {
		frame, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				if !saw {
					return "", "", errors.New("Kiro returned an empty event stream")
				}
				return text.String(), id, nil
			}
			return "", "", err
		}
		saw = true
		chunk, chunkID, _, decodeErr := decodeKiroEvent(frame)
		if decodeErr != nil {
			return "", "", decodeErr
		}
		text.WriteString(chunk)
		if chunkID != "" {
			id = chunkID
		}
	}
}

func decodeKiroEvent(frame eventFrame) (string, string, bool, error) {
	eventType := strings.ToLower(frame.headers[":event-type"])
	if eventType == "" {
		eventType = strings.ToLower(frame.headers["event-type"])
	}
	messageType := strings.ToLower(frame.headers[":message-type"])
	if strings.Contains(eventType, "tooluse") || strings.Contains(eventType, "tool_use") {
		return "", "", false, errors.New("Kiro returned unsupported tool output")
	}
	if messageType == "exception" || messageType == "error" || strings.Contains(eventType, "error") || strings.Contains(eventType, "exception") {
		return "", "", false, errors.New("Kiro event stream reported an upstream error")
	}
	if len(frame.payload) == 0 {
		return "", "", false, nil
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(frame.payload, &root) != nil || root == nil {
		return "", "", false, errors.New("Kiro event stream payload is not valid JSON")
	}
	if hasJSON(root, "error") {
		return "", "", false, errors.New("Kiro event stream reported an upstream error")
	}
	object := root
	for _, key := range []string{"assistantResponseEvent", "assistant_response_event"} {
		if nested, err := rawObject(root[key]); err == nil {
			object = nested
			break
		}
	}
	text := stringField(object, "content")
	id := firstNonEmpty(stringField(object, "messageId"), stringField(object, "conversationId"))
	status := strings.ToLower(firstNonEmpty(stringField(object, "messageStatus"), stringField(object, "status")))
	if status == "error" || status == "failed" {
		return "", "", false, errors.New("Kiro generation failed")
	}
	terminal := status == "completed" || status == "complete" || status == "success"
	return text, id, terminal, nil
}

// AWS Event Stream consists of a prelude (lengths + prelude CRC), headers,
// payload, and a message CRC. Every frame is checked before any data is exposed
// to the gateway, including frames received in fragmented network reads.
type eventReader struct {
	r       io.Reader
	frames  int
	bytes   int64
	maxByte int64
}

type eventFrame struct {
	headers map[string]string
	payload []byte
}

func newEventReader(r io.Reader) *eventReader { return &eventReader{r: r, maxByte: 64 << 20} }

func (r *eventReader) Next() (eventFrame, error) {
	var prelude [12]byte
	n, err := io.ReadFull(r.r, prelude[:])
	if err != nil {
		if err == io.EOF && n == 0 {
			return eventFrame{}, io.EOF
		}
		return eventFrame{}, errors.New("truncated Kiro event stream prelude")
	}
	total := binary.BigEndian.Uint32(prelude[0:4])
	headerLen := binary.BigEndian.Uint32(prelude[4:8])
	if total < 16 || total > 16<<20 || headerLen > total-16 {
		return eventFrame{}, errors.New("invalid Kiro event stream frame length")
	}
	if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
		return eventFrame{}, errors.New("Kiro event stream prelude CRC mismatch")
	}
	rest := make([]byte, int(total)-12)
	if _, err := io.ReadFull(r.r, rest); err != nil {
		return eventFrame{}, errors.New("truncated Kiro event stream frame")
	}
	frame := append(prelude[:], rest...)
	if crc32.ChecksumIEEE(frame[:len(frame)-4]) != binary.BigEndian.Uint32(frame[len(frame)-4:]) {
		return eventFrame{}, errors.New("Kiro event stream message CRC mismatch")
	}
	r.frames++
	r.bytes += int64(len(frame))
	if r.bytes > r.maxByte {
		return eventFrame{}, errors.New("Kiro event stream exceeds byte limit")
	}
	headers, err := parseEventHeaders(frame[12 : 12+headerLen])
	if err != nil {
		return eventFrame{}, err
	}
	payloadEnd := len(frame) - 4
	return eventFrame{headers: headers, payload: append([]byte(nil), frame[12+headerLen:payloadEnd]...)}, nil
}

func parseEventHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	for len(data) > 0 {
		nameLen := int(data[0])
		data = data[1:]
		if nameLen == 0 || len(data) < nameLen+1 {
			return nil, errors.New("invalid Kiro event stream header")
		}
		name := string(data[:nameLen])
		data = data[nameLen:]
		typ := data[0]
		data = data[1:]
		var value string
		switch typ {
		case 0:
			value = "true"
		case 1:
			value = "false"
		case 2:
			if len(data) < 1 {
				return nil, errors.New("invalid byte event header")
			}
			value = fmt.Sprintf("%d", data[0])
			data = data[1:]
		case 3:
			if len(data) < 2 {
				return nil, errors.New("invalid short event header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
		case 4:
			if len(data) < 4 {
				return nil, errors.New("invalid int event header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint32(data[:4]))
			data = data[4:]
		case 5, 8:
			if len(data) < 8 {
				return nil, errors.New("invalid long event header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint64(data[:8]))
			data = data[8:]
		case 6, 7:
			if len(data) < 2 {
				return nil, errors.New("invalid variable event header")
			}
			length := int(binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
			if len(data) < length {
				return nil, errors.New("truncated variable event header")
			}
			value = string(data[:length])
			data = data[length:]
		case 9:
			if len(data) < 16 {
				return nil, errors.New("invalid UUID event header")
			}
			value = base64.RawURLEncoding.EncodeToString(data[:16])
			data = data[16:]
		default:
			return nil, errors.New("unsupported Kiro event stream header type")
		}
		if _, exists := headers[name]; exists {
			return nil, errors.New("duplicate Kiro event stream header")
		}
		headers[name] = value
	}
	return headers, nil
}
