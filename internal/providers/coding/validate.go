package coding

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func strictObject(data []byte, label string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be a JSON object", label)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%s must contain one JSON object", label)
	}
	return object, nil
}

func rejectUnknown(object map[string]json.RawMessage, allowed map[string]struct{}, label string) error {
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s field %q is unsupported", label, key)
		}
	}
	return nil
}

func validateOptionalString(object map[string]json.RawMessage, key, label string) error {
	if raw, ok := object[key]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("%s field %q must be a string", label, key)
		}
	}
	return nil
}

func hasJSON(object map[string]json.RawMessage, key string) bool {
	raw, ok := object[key]
	return ok && len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func validateMessages(raw json.RawMessage, protocol string) error {
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) == 0 {
		return errors.New("messages must be a non-empty array")
	}
	for index, rawMessage := range messages {
		message, err := rawObject(rawMessage)
		if err != nil {
			return fmt.Errorf("message %d must be an object", index)
		}
		role := stringField(message, "role")
		if role == "" {
			return fmt.Errorf("message %d role is required", index)
		}
		if !hasJSON(message, "content") && !(protocol == "chat" && role == "assistant" && hasJSON(message, "tool_calls")) {
			return fmt.Errorf("message %d content is required", index)
		}
		switch protocol {
		case "messages":
			if err := rejectUnknown(message, map[string]struct{}{"role": {}, "content": {}}, fmt.Sprintf("message %d", index)); err != nil {
				return err
			}
			if role != "user" && role != "assistant" {
				return fmt.Errorf("message %d role %q is unsupported for messages", index, role)
			}
			if err := validateAnthropicContent(message["content"], fmt.Sprintf("message %d content", index)); err != nil {
				return err
			}
		case "chat":
			if err := rejectUnknown(message, map[string]struct{}{"role": {}, "content": {}, "tool_calls": {}, "tool_call_id": {}}, fmt.Sprintf("message %d", index)); err != nil {
				return err
			}
			if role != "system" && role != "user" && role != "assistant" && role != "tool" {
				return fmt.Errorf("message %d role %q is unsupported for chat", index, role)
			}
			if role == "tool" && !hasJSON(message, "tool_call_id") {
				return fmt.Errorf("message %d tool_call_id is required", index)
			}
			if role != "assistant" && hasJSON(message, "tool_calls") {
				return fmt.Errorf("message %d tool_calls are only valid on assistant messages", index)
			}
			if role != "tool" && hasJSON(message, "tool_call_id") {
				return fmt.Errorf("message %d tool_call_id is only valid on tool messages", index)
			}
			if !(role == "assistant" && hasJSON(message, "tool_calls") && bytes.Equal(bytes.TrimSpace(message["content"]), []byte("null"))) {
				if err := validateOpenAIContent(message["content"], fmt.Sprintf("message %d content", index)); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unsupported message protocol %q", protocol)
		}
	}
	return nil
}

func validateAnthropicSystem(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if text := stringContent(raw); text != "" {
		var value string
		if json.Unmarshal(raw, &value) == nil {
			return nil
		}
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return errors.New("system must be a string or text blocks")
	}
	for i, block := range blocks {
		if err := rejectUnknown(block, map[string]struct{}{"type": {}, "text": {}}, fmt.Sprintf("system block %d", i)); err != nil {
			return err
		}
		if stringField(block, "type") != "text" || stringField(block, "text") == "" {
			return fmt.Errorf("system block %d must be a non-empty text block", i)
		}
	}
	return nil
}

func validateAnthropicContent(raw json.RawMessage, label string) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return fmt.Errorf("%s must be a string or content blocks", label)
	}
	for i, block := range blocks {
		typ := stringField(block, "type")
		switch typ {
		case "text":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "text": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			if stringField(block, "text") == "" {
				return fmt.Errorf("%s block %d text is required", label, i)
			}
		case "image":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "source": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			source, err := rawObject(block["source"])
			if err != nil {
				return fmt.Errorf("%s image source must be an object", label)
			}
			if err := rejectUnknown(source, map[string]struct{}{"type": {}, "media_type": {}, "data": {}}, fmt.Sprintf("%s image source", label)); err != nil {
				return err
			}
			if stringField(source, "type") != "base64" || stringField(source, "data") == "" {
				return fmt.Errorf("%s image source must be base64 data", label)
			}
			if _, _, err := decodeKiroImageDataURL("data:" + stringField(source, "media_type") + ";base64," + stringField(source, "data")); err != nil {
				return fmt.Errorf("%s image source: %w", label, err)
			}
		case "tool_result":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "tool_use_id": {}, "content": {}, "is_error": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			if stringField(block, "tool_use_id") == "" {
				return fmt.Errorf("%s tool_result requires tool_use_id", label)
			}
		case "tool_use":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "id": {}, "name": {}, "input": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			if stringField(block, "id") == "" || stringField(block, "name") == "" || !hasJSON(block, "input") {
				return fmt.Errorf("%s tool_use requires id, name, and input", label)
			}
		default:
			return fmt.Errorf("%s block type %q is unsupported", label, typ)
		}
	}
	return nil
}

func validateOpenAIContent(raw json.RawMessage, label string) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return nil
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return fmt.Errorf("%s must be a string or content blocks", label)
	}
	for i, block := range blocks {
		typ := stringField(block, "type")
		switch typ {
		case "text":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "text": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			if stringField(block, "text") == "" {
				return fmt.Errorf("%s block %d text is required", label, i)
			}
		case "image_url":
			if err := rejectUnknown(block, map[string]struct{}{"type": {}, "image_url": {}}, fmt.Sprintf("%s block %d", label, i)); err != nil {
				return err
			}
			imageURL, err := rawObject(block["image_url"])
			if err != nil {
				return fmt.Errorf("%s block %d image_url must be an object", label, i)
			}
			if err := rejectUnknown(imageURL, map[string]struct{}{"url": {}, "detail": {}}, fmt.Sprintf("%s block %d image_url", label, i)); err != nil {
				return err
			}
			if stringField(imageURL, "url") == "" {
				return fmt.Errorf("%s block %d image_url.url is required", label, i)
			}
			if err := validateOptionalString(imageURL, "detail", fmt.Sprintf("%s block %d image_url", label, i)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s block type %q is unsupported", label, typ)
		}
	}
	return nil
}

// validateKiroChatImages rejects remote OpenAI image URLs because resolving a
// caller-supplied URL would turn a request conversion into an implicit server
// side fetch. Data URLs can be represented by Kiro's native image field.
func validateKiroChatImages(raw json.RawMessage) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil
	}
	for i, rawMessage := range messages {
		message, err := rawObject(rawMessage)
		if err != nil {
			return fmt.Errorf("Kiro message %d must be an object", i)
		}
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			continue
		}
		for j, block := range blocks {
			if stringField(block, "type") != "image_url" {
				continue
			}
			imageURL, err := rawObject(block["image_url"])
			if err != nil {
				return fmt.Errorf("Kiro message %d content block %d image_url must be an object", i, j)
			}
			if _, _, err := decodeKiroImageDataURL(stringField(imageURL, "url")); err != nil {
				return fmt.Errorf("Kiro message %d content block %d: %w", i, j, err)
			}
		}
	}
	return nil
}

func decodeKiroImageDataURL(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "data:") {
		return "", "", errors.New("Kiro only supports data URL images")
	}
	comma := strings.IndexByte(value, ',')
	if comma <= len("data:") {
		return "", "", errors.New("Kiro image data URL is malformed")
	}
	metadata := strings.Split(value[len("data:"):comma], ";")
	mediaType := strings.TrimSpace(metadata[0])
	supported := map[string]struct{}{
		"image/jpeg": {}, "image/png": {}, "image/gif": {}, "image/webp": {}, "image/bmp": {},
	}
	if _, ok := supported[mediaType]; !ok {
		return "", "", errors.New("Kiro image media type is unsupported")
	}
	hasBase64 := false
	for _, part := range metadata[1:] {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			hasBase64 = true
			break
		}
	}
	if !hasBase64 {
		return "", "", errors.New("Kiro image data URL must be base64 encoded")
	}
	encoded := value[comma+1:]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return "", "", errors.New("Kiro image data URL has invalid base64 data")
	}
	if len(decoded) > 20<<20 {
		return "", "", errors.New("Kiro image exceeds byte limit")
	}
	return mediaType, encoded, nil
}

func validateAnthropicTools(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var tools []map[string]json.RawMessage
	if json.Unmarshal(raw, &tools) != nil {
		return errors.New("tools must be an array")
	}
	for i, tool := range tools {
		if err := rejectUnknown(tool, map[string]struct{}{"name": {}, "description": {}, "input_schema": {}}, fmt.Sprintf("tool %d", i)); err != nil {
			return err
		}
		if stringField(tool, "name") == "" || !hasJSON(tool, "input_schema") {
			return fmt.Errorf("tool %d requires name and input_schema", i)
		}
		if _, err := rawObject(tool["input_schema"]); err != nil {
			return fmt.Errorf("tool %d input_schema must be an object", i)
		}
	}
	return nil
}

func validateKiroChatMessages(raw json.RawMessage) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return errors.New("Kiro chat messages must be a non-empty array")
	}
	for i, rawMessage := range messages {
		message, err := rawObject(rawMessage)
		if err != nil {
			return fmt.Errorf("Kiro message %d must be an object", i)
		}
		role := stringField(message, "role")
		if role == "tool" || role == "system" {
			return fmt.Errorf("Kiro message %d role %q is unsupported", i, role)
		}
		if hasJSON(message, "tool_calls") || hasJSON(message, "tool_call_id") {
			return fmt.Errorf("Kiro message %d tool call fields are unsupported", i)
		}
	}
	if err := validateKiroChatImages(raw); err != nil {
		return err
	}
	if stringFieldMust(messages[len(messages)-1], "role") != "user" {
		return errors.New("Kiro request must end with a user message")
	}
	return nil
}

func validateKiroFinalMessage(raw json.RawMessage) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 || stringFieldMust(messages[len(messages)-1], "role") != "user" {
		return errors.New("Kiro request must end with a user message")
	}
	return nil
}

func validateKiroHistory(raw json.RawMessage) error {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return errors.New("Kiro messages must be a non-empty array")
	}
	for i, rawMessage := range messages {
		message, err := rawObject(rawMessage)
		if err != nil {
			return fmt.Errorf("Kiro message %d must be an object", i)
		}
		var blocks []map[string]json.RawMessage
		if json.Unmarshal(message["content"], &blocks) != nil {
			continue
		}
		for _, block := range blocks {
			typ := stringField(block, "type")
			if typ == "tool_use" || typ == "tool_result" {
				return fmt.Errorf("Kiro message %d %s blocks are unsupported", i, typ)
			}
			if i != len(messages)-1 && (typ == "image" || typ == "image_url") {
				return fmt.Errorf("Kiro historical message %d contains an unsupported %s block", i, typ)
			}
		}
	}
	return nil
}

func stringFieldMust(raw json.RawMessage, key string) string {
	object, _ := rawObject(raw)
	return stringField(object, key)
}

func validateAntigravityRoot(root map[string]json.RawMessage) error {
	const envelopeLabel = "Antigravity request"
	native := map[string]struct{}{
		"contents": {}, "tools": {}, "systemInstruction": {}, "generationConfig": {},
		"safetySettings": {}, "toolConfig": {}, "cachedContent": {}, "sessionId": {},
	}
	if requestRaw, ok := root["request"]; ok {
		if err := rejectUnknown(root, map[string]struct{}{
			"request": {}, "model": {}, "project": {}, "userAgent": {}, "requestType": {}, "requestId": {},
		}, envelopeLabel); err != nil {
			return err
		}
		request, err := rawObject(requestRaw)
		if err != nil {
			return errors.New("Antigravity request field must be an object")
		}
		if err := rejectUnknown(request, native, "Antigravity request field"); err != nil {
			return err
		}
		if err := validateGeminiRequest(request); err != nil {
			return err
		}
	} else if err := rejectUnknown(root, func() map[string]struct{} {
		allowed := map[string]struct{}{"model": {}, "project": {}, "userAgent": {}, "requestType": {}, "requestId": {}}
		for key := range native {
			allowed[key] = struct{}{}
		}
		return allowed
	}(), envelopeLabel); err != nil {
		return err
	}
	if _, ok := root["request"]; !ok {
		if err := validateGeminiRequest(root); err != nil {
			return err
		}
	}
	if requestType := stringField(root, "requestType"); requestType != "" && requestType != "agent" && requestType != "web_search" && requestType != "image_gen" {
		return errors.New("Antigravity requestType is unsupported")
	}
	for _, key := range []string{"model", "project", "userAgent", "requestType", "requestId"} {
		if err := validateOptionalString(root, key, envelopeLabel); err != nil {
			return err
		}
	}
	return nil
}

func validateGeminiRequest(request map[string]json.RawMessage) error {
	if contents := request["contents"]; len(contents) > 0 {
		if err := validateGeminiContents(contents, "Antigravity contents"); err != nil {
			return err
		}
	}
	if system := request["systemInstruction"]; len(system) > 0 {
		object, err := rawObject(system)
		if err != nil {
			return errors.New("Antigravity systemInstruction must be an object")
		}
		if err := rejectUnknown(object, map[string]struct{}{"role": {}, "parts": {}}, "Antigravity systemInstruction"); err != nil {
			return err
		}
		if parts := object["parts"]; len(parts) > 0 {
			if err := validateGeminiParts(parts, "Antigravity systemInstruction parts"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateGeminiContents(raw json.RawMessage, label string) error {
	var contents []map[string]json.RawMessage
	if json.Unmarshal(raw, &contents) != nil {
		return fmt.Errorf("%s must be an array", label)
	}
	for i, content := range contents {
		if err := rejectUnknown(content, map[string]struct{}{"role": {}, "parts": {}}, fmt.Sprintf("%s item %d", label, i)); err != nil {
			return err
		}
		role := stringField(content, "role")
		if role != "user" && role != "model" {
			return fmt.Errorf("%s item %d role %q is unsupported", label, i, role)
		}
		if err := validateGeminiParts(content["parts"], fmt.Sprintf("%s item %d parts", label, i)); err != nil {
			return err
		}
	}
	return nil
}

func validateGeminiParts(raw json.RawMessage, label string) error {
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return fmt.Errorf("%s must be an array", label)
	}
	allowed := map[string]struct{}{
		"text": {}, "inlineData": {}, "fileData": {}, "functionCall": {}, "functionResponse": {},
		"executableCode": {}, "codeExecutionResult": {}, "thought": {}, "thoughtSignature": {}, "videoMetadata": {},
	}
	for i, part := range parts {
		if err := rejectUnknown(part, allowed, fmt.Sprintf("%s item %d", label, i)); err != nil {
			return err
		}
		if len(part) == 0 {
			return fmt.Errorf("%s item %d is empty", label, i)
		}
	}
	return nil
}

func joinAnthropicText(raw json.RawMessage) string {
	if text := strings.TrimSpace(stringContent(raw)); text != "" {
		return text
	}
	return ""
}
