package codingmore

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"time"
)

type eventFrame struct {
	headers map[string]string
	payload map[string]json.RawMessage
}

type eventReader struct {
	r       io.Reader
	bytes   int64
	frames  int
	maxByte int64
}

func newEventReader(r io.Reader) *eventReader {
	return &eventReader{r: r, maxByte: maxEventBytes}
}

func (r *eventReader) Next() (eventFrame, error) {
	var prelude [12]byte
	n, err := io.ReadFull(r.r, prelude[:])
	if err != nil {
		if err == io.EOF && n == 0 {
			return eventFrame{}, io.EOF
		}
		return eventFrame{}, ErrTruncated
	}
	total := binary.BigEndian.Uint32(prelude[0:4])
	headerLength := binary.BigEndian.Uint32(prelude[4:8])
	if total < 16 || total > maxEventFrame || headerLength > total-16 {
		return eventFrame{}, errors.New("coding more adapter: invalid EventStream frame length")
	}
	if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
		return eventFrame{}, errors.New("coding more adapter: EventStream prelude CRC mismatch")
	}
	rest := make([]byte, int(total)-12)
	if _, err := io.ReadFull(r.r, rest); err != nil {
		return eventFrame{}, ErrTruncated
	}
	frame := append(prelude[:], rest...)
	if crc32.ChecksumIEEE(frame[:len(frame)-4]) != binary.BigEndian.Uint32(frame[len(frame)-4:]) {
		return eventFrame{}, errors.New("coding more adapter: EventStream message CRC mismatch")
	}
	r.frames++
	r.bytes += int64(len(frame))
	if r.bytes > r.maxByte {
		return eventFrame{}, errors.New("coding more adapter: EventStream exceeds byte limit")
	}
	headers, err := parseEventHeaders(frame[12 : 12+headerLength])
	if err != nil {
		return eventFrame{}, err
	}
	payloadBytes := frame[12+headerLength : len(frame)-4]
	var payload map[string]json.RawMessage
	if len(payloadBytes) > 0 {
		if json.Unmarshal(payloadBytes, &payload) != nil || payload == nil {
			return eventFrame{}, errors.New("coding more adapter: EventStream payload is not a JSON object")
		}
	} else {
		payload = map[string]json.RawMessage{}
	}
	return eventFrame{headers: headers, payload: payload}, nil
}

func parseEventHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	for len(data) > 0 {
		nameLength := int(data[0])
		data = data[1:]
		if nameLength == 0 || len(data) < nameLength+1 {
			return nil, errors.New("coding more adapter: invalid EventStream header")
		}
		name := string(data[:nameLength])
		data = data[nameLength:]
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
				return nil, errors.New("coding more adapter: truncated byte header")
			}
			value = fmt.Sprintf("%d", data[0])
			data = data[1:]
		case 3:
			if len(data) < 2 {
				return nil, errors.New("coding more adapter: truncated short header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
		case 4:
			if len(data) < 4 {
				return nil, errors.New("coding more adapter: truncated int header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint32(data[:4]))
			data = data[4:]
		case 5, 8:
			if len(data) < 8 {
				return nil, errors.New("coding more adapter: truncated long header")
			}
			value = fmt.Sprintf("%d", binary.BigEndian.Uint64(data[:8]))
			data = data[8:]
		case 6, 7:
			if len(data) < 2 {
				return nil, errors.New("coding more adapter: truncated string header")
			}
			length := int(binary.BigEndian.Uint16(data[:2]))
			data = data[2:]
			if len(data) < length {
				return nil, errors.New("coding more adapter: truncated string header value")
			}
			value = string(data[:length])
			data = data[length:]
		case 9:
			if len(data) < 16 {
				return nil, errors.New("coding more adapter: truncated UUID header")
			}
			value = base64.RawURLEncoding.EncodeToString(data[:16])
			data = data[16:]
		default:
			return nil, errors.New("coding more adapter: unsupported EventStream header type")
		}
		if _, exists := headers[name]; exists {
			return nil, errors.New("coding more adapter: duplicate EventStream header")
		}
		headers[name] = value
	}
	return headers, nil
}

type amazonQEvent struct {
	typ       string
	content   string
	reasoning string
	id        string
	terminal  bool
	usage     usage
}

type usage struct {
	prompt     int
	completion int
	cacheRead  int
	cacheWrite int
}

func decodeAmazonQEvent(frame eventFrame) (amazonQEvent, error) {
	eventType := frame.headers[":event-type"]
	if eventType == "" {
		eventType = frame.headers["event-type"]
	}
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ":", "").Replace(eventType))
	messageType := strings.ToLower(frame.headers[":message-type"])
	if messageType == "exception" || messageType == "error" || strings.Contains(normalized, "error") || strings.Contains(normalized, "exception") {
		return amazonQEvent{}, errors.New("coding more adapter: Amazon Q EventStream reported an upstream error")
	}
	switch normalized {
	case "assistantresponseevent", "codeevent", "reasoningcontentevent", "metadataevent", "contextusageevent", "meteringevent", "metricsevent", "messagestopevent":
	default:
		return amazonQEvent{}, errors.New("coding more adapter: unsupported Amazon Q event")
	}
	if raw := frame.payload["error"]; len(raw) != 0 {
		return amazonQEvent{}, errors.New("coding more adapter: Amazon Q payload reported an upstream error")
	}
	// Some captured fixtures retain the event name as a payload envelope. The
	// service's current endpoint sends the fields at the top level, but accepting
	// both forms keeps the parser faithful to the product protocol.
	object := frame.payload
	for _, key := range []string{"assistantResponseEvent", "assistant_response_event", "codeEvent", "reasoningContentEvent", "metadataEvent", "contextUsageEvent", "meteringEvent"} {
		var nested map[string]json.RawMessage
		if raw := frame.payload[key]; len(raw) != 0 && json.Unmarshal(raw, &nested) == nil && nested != nil {
			object = nested
			break
		}
	}
	result := amazonQEvent{typ: normalized}
	result.content = rawString(object, "content")
	if (normalized == "assistantresponseevent" || normalized == "codeevent") && len(object["content"]) != 0 {
		var content string
		if json.Unmarshal(object["content"], &content) != nil {
			return amazonQEvent{}, errors.New("coding more adapter: Amazon Q event content is not text")
		}
	}
	result.id = firstString(object, "messageId", "conversationId", "id")
	if normalized == "reasoningcontentevent" {
		if raw := object["reasoningText"]; len(raw) != 0 {
			var value struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(raw, &value) == nil {
				result.reasoning = value.Text
			}
			if result.reasoning == "" {
				result.reasoning = rawString(object, "reasoningText")
			}
		}
		if result.reasoning == "" {
			result.reasoning = rawString(object, "text")
		}
	}
	status := strings.ToLower(firstString(object, "messageStatus", "status"))
	result.terminal = normalized == "messagestopevent" || status == "completed" || status == "complete" || status == "success" || status == "end_turn"
	if status == "error" || status == "failed" {
		return amazonQEvent{}, errors.New("coding more adapter: Amazon Q generation failed")
	}
	if normalized == "metadataevent" || normalized == "metricsevent" {
		if raw := object["metricsEvent"]; len(raw) != 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) == nil {
				result.usage = parseUsage(nested)
			}
		}
		if raw := object["usage"]; len(raw) != 0 {
			var nested map[string]json.RawMessage
			if json.Unmarshal(raw, &nested) == nil {
				result.usage = parseUsage(nested)
			}
		}
		if result.usage == (usage{}) {
			result.usage = parseUsage(object)
		}
	}
	return result, nil
}

func rawString(object map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(object[key], &value)
	return value
}

func firstString(object map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := rawString(object, key); value != "" {
			return value
		}
	}
	return ""
}

func parseUsage(object map[string]json.RawMessage) usage {
	result := usage{}
	result.prompt = firstInt(object, "inputTokens", "prompt_tokens", "input_tokens")
	result.completion = firstInt(object, "outputTokens", "completion_tokens", "output_tokens")
	result.cacheRead = firstInt(object, "cacheReadInputTokens", "cacheReadTokens", "cache_read_input_tokens")
	result.cacheWrite = firstInt(object, "cacheWriteInputTokens", "cacheCreationTokens", "cache_creation_input_tokens")
	return result
}

func firstInt(object map[string]json.RawMessage, keys ...string) int {
	for _, key := range keys {
		var value int
		if json.Unmarshal(object[key], &value) == nil && value >= 0 {
			return value
		}
	}
	return 0
}

type amazonQStream struct {
	body     io.ReadCloser
	reader   *eventReader
	ctx      context.Context
	model    string
	id       string
	created  int64
	pending  []byte
	sawFrame bool
	started  bool
	terminal bool
	finished bool
	usage    usage
}

func newAmazonQStream(ctx context.Context, body io.ReadCloser, model string) *amazonQStream {
	return &amazonQStream{body: body, reader: newEventReader(body), ctx: ctx, model: model, created: timeNowUnix()}
}

func (s *amazonQStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 && !s.finished {
		frame, err := s.reader.Next()
		if err != nil {
			if s.ctx != nil && s.ctx.Err() != nil {
				return 0, s.ctx.Err()
			}
			if err != io.EOF {
				if err == ErrTruncated {
					return 0, err
				}
				return 0, err
			}
			if !s.sawFrame {
				return 0, errors.New("coding more adapter: Amazon Q returned an empty EventStream")
			}
			if !s.terminal {
				return 0, ErrTruncated
			}
			s.appendFinish()
			s.finished = true
			continue
		}
		s.sawFrame = true
		event, err := decodeAmazonQEvent(frame)
		if err != nil {
			return 0, err
		}
		if event.id != "" && s.id == "" {
			s.id = event.id
		}
		if !s.started {
			s.appendStart()
		}
		if event.content != "" {
			s.appendContent(event.content)
		}
		if event.reasoning != "" {
			s.appendReasoning(event.reasoning)
		}
		if event.usage != (usage{}) {
			s.usage = event.usage
		}
		if event.terminal {
			s.terminal = true
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.finished {
		return 0, io.EOF
	}
	return n, nil
}

func (s *amazonQStream) appendStart() {
	s.started = true
	if s.id == "" {
		s.id = randomID("chatcmpl-")
	}
	value := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil}}}
	s.appendSSE(value)
}

func (s *amazonQStream) appendContent(content string) {
	delta := map[string]any{"content": content}
	value := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
	s.appendSSE(value)
}

func (s *amazonQStream) appendReasoning(content string) {
	delta := map[string]any{"reasoning_content": content}
	value := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
	s.appendSSE(value)
}

func (s *amazonQStream) appendFinish() {
	value := map[string]any{"id": s.id, "object": "chat.completion.chunk", "created": s.created, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
	if s.usage != (usage{}) {
		value["usage"] = usageJSON(s.usage)
	}
	s.appendSSE(value)
	s.pending = append(s.pending, []byte("data: [DONE]\n\n")...)
}

func (s *amazonQStream) appendSSE(value any) {
	encoded, _ := json.Marshal(value)
	s.pending = append(s.pending, []byte("data: ")...)
	s.pending = append(s.pending, encoded...)
	s.pending = append(s.pending, '\n', '\n')
}

func (s *amazonQStream) Close() error { return s.body.Close() }

func timeNowUnix() int64 { return time.Now().Unix() }

type collectedAmazonQ struct {
	id        string
	model     string
	text      strings.Builder
	reasoning strings.Builder
	usage     usage
}

func collectAmazonQ(ctx context.Context, body io.ReadCloser, model string) (map[string]any, error) {
	defer body.Close()
	reader := newEventReader(body)
	result := collectedAmazonQ{model: model}
	sawFrame := false
	terminal := false
	for {
		frame, err := reader.Next()
		if err != nil {
			if ctx != nil && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == io.EOF {
				if !sawFrame {
					return nil, errors.New("coding more adapter: Amazon Q returned an empty EventStream")
				}
				if !terminal {
					return nil, ErrTruncated
				}
				break
			}
			return nil, err
		}
		sawFrame = true
		event, err := decodeAmazonQEvent(frame)
		if err != nil {
			return nil, err
		}
		if event.id != "" {
			result.id = event.id
		}
		result.text.WriteString(event.content)
		result.reasoning.WriteString(event.reasoning)
		if event.usage != (usage{}) {
			result.usage = event.usage
		}
		terminal = terminal || event.terminal
	}
	if result.id == "" {
		result.id = randomID("chatcmpl-")
	}
	choiceMessage := map[string]any{"role": "assistant", "content": result.text.String()}
	if result.reasoning.Len() > 0 {
		choiceMessage["reasoning_content"] = result.reasoning.String()
	}
	choice := map[string]any{"index": 0, "message": choiceMessage, "finish_reason": "stop"}
	response := map[string]any{"id": result.id, "object": "chat.completion", "created": timeNowUnix(), "model": model, "choices": []any{choice}}
	if result.usage.prompt > 0 || result.usage.completion > 0 || result.usage.cacheRead > 0 || result.usage.cacheWrite > 0 {
		response["usage"] = usageJSON(result.usage)
	}
	return response, nil
}

func usageJSON(value usage) map[string]any {
	result := map[string]any{
		"prompt_tokens":     value.prompt,
		"completion_tokens": value.completion,
		"total_tokens":      value.prompt + value.completion,
	}
	if value.cacheRead > 0 {
		result["cache_read_input_tokens"] = value.cacheRead
	}
	if value.cacheWrite > 0 {
		result["cache_creation_input_tokens"] = value.cacheWrite
	}
	return result
}
