package codingmore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type chatRequest struct {
	messages    []chatMessage
	maxTokens   *int
	temperature *float64
	topP        *float64
}

type chatMessage struct {
	role    string
	content string
}

func decodeChatRequest(body []byte, model string) (chatRequest, error) {
	var root map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&root); err != nil || root == nil {
		return chatRequest{}, fmt.Errorf("%w: request must be a JSON object", ErrUnsupported)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return chatRequest{}, fmt.Errorf("%w: request must contain one JSON object", ErrUnsupported)
	}
	allowed := map[string]bool{
		"model": true, "messages": true, "stream": true,
		"max_tokens": true, "max_completion_tokens": true,
		"temperature": true, "top_p": true,
	}
	for key := range root {
		if !allowed[key] {
			return chatRequest{}, fmt.Errorf("%w: Amazon Q field %q is unsupported", ErrUnsupported, key)
		}
	}
	if raw := root["model"]; len(raw) != 0 {
		var supplied string
		if json.Unmarshal(raw, &supplied) != nil || strings.TrimSpace(supplied) == "" {
			return chatRequest{}, fmt.Errorf("%w: model must be a non-empty string", ErrUnsupported)
		}
		// The router supplies the selected upstream model separately. A body model
		// is accepted for OpenAI compatibility but cannot override that selection.
		_ = model
	}
	if raw := root["stream"]; len(raw) != 0 {
		var supplied bool
		if json.Unmarshal(raw, &supplied) != nil {
			return chatRequest{}, fmt.Errorf("%w: stream must be boolean", ErrUnsupported)
		}
	}
	var rawMessages []json.RawMessage
	if err := json.Unmarshal(root["messages"], &rawMessages); err != nil || len(rawMessages) == 0 {
		return chatRequest{}, fmt.Errorf("%w: messages must be a non-empty array", ErrUnsupported)
	}
	request := chatRequest{messages: make([]chatMessage, 0, len(rawMessages))}
	seenNonSystem := false
	lastRole := ""
	systemPrefix := make([]string, 0, 1)
	for i, raw := range rawMessages {
		var message map[string]json.RawMessage
		if json.Unmarshal(raw, &message) != nil || message == nil {
			return chatRequest{}, fmt.Errorf("%w: message %d must be an object", ErrUnsupported, i)
		}
		for key := range message {
			if key != "role" && key != "content" {
				return chatRequest{}, fmt.Errorf("%w: message %d field %q is unsupported", ErrUnsupported, i, key)
			}
		}
		var role string
		if json.Unmarshal(message["role"], &role) != nil || role == "" {
			return chatRequest{}, fmt.Errorf("%w: message %d role is required", ErrUnsupported, i)
		}
		var content string
		if json.Unmarshal(message["content"], &content) != nil {
			return chatRequest{}, fmt.Errorf("%w: message %d content must be text", ErrUnsupported, i)
		}
		switch role {
		case "system":
			if seenNonSystem {
				return chatRequest{}, fmt.Errorf("%w: system messages must precede conversation turns", ErrUnsupported)
			}
			if strings.TrimSpace(content) == "" {
				return chatRequest{}, fmt.Errorf("%w: system message %d is empty", ErrUnsupported, i)
			}
			systemPrefix = append(systemPrefix, content)
			continue
		case "user", "assistant":
			seenNonSystem = true
		default:
			return chatRequest{}, fmt.Errorf("%w: message %d role %q is unsupported", ErrUnsupported, i, role)
		}
		if lastRole == role {
			return chatRequest{}, fmt.Errorf("%w: consecutive %s messages are unsupported", ErrUnsupported, role)
		}
		lastRole = role
		request.messages = append(request.messages, chatMessage{role: role, content: content})
	}
	if len(request.messages) == 0 || request.messages[0].role != "user" {
		return chatRequest{}, fmt.Errorf("%w: conversation must begin with a user message", ErrUnsupported)
	}
	if request.messages[len(request.messages)-1].role != "user" {
		return chatRequest{}, fmt.Errorf("%w: Amazon Q request must end with a user message", ErrUnsupported)
	}
	if len(systemPrefix) > 0 {
		request.messages[0].content = "<system-reminder>\n" + strings.Join(systemPrefix, "\n\n") + "\n</system-reminder>\n\n" + request.messages[0].content
	}
	if strings.TrimSpace(request.messages[len(request.messages)-1].content) == "" {
		return chatRequest{}, fmt.Errorf("%w: final user message must contain text", ErrUnsupported)
	}

	if raw := root["max_tokens"]; len(raw) != 0 {
		value, err := boundedInt(raw, 1, 200000, "max_tokens")
		if err != nil {
			return chatRequest{}, err
		}
		request.maxTokens = &value
	}
	if raw := root["max_completion_tokens"]; len(raw) != 0 {
		value, err := boundedInt(raw, 1, 200000, "max_completion_tokens")
		if err != nil {
			return chatRequest{}, err
		}
		if request.maxTokens == nil {
			request.maxTokens = &value
		}
	}
	if raw := root["temperature"]; len(raw) != 0 {
		value, err := boundedFloat(raw, 0, 2, "temperature")
		if err != nil {
			return chatRequest{}, err
		}
		request.temperature = &value
	}
	if raw := root["top_p"]; len(raw) != 0 {
		value, err := boundedFloat(raw, 0, 1, "top_p")
		if err != nil {
			return chatRequest{}, err
		}
		request.topP = &value
	}
	return request, nil
}

func boundedInt(raw json.RawMessage, min, max int, name string) (int, error) {
	var value int
	if err := json.Unmarshal(raw, &value); err != nil || value < min || value > max {
		return 0, fmt.Errorf("%w: %s is outside supported range", ErrUnsupported, name)
	}
	return value, nil
}

func boundedFloat(raw json.RawMessage, min, max float64, name string) (float64, error) {
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || value < min || value > max {
		return 0, fmt.Errorf("%w: %s is outside supported range", ErrUnsupported, name)
	}
	return value, nil
}

type amazonQPayload struct {
	ConversationState conversationState `json:"conversationState"`
	ProfileARN        string            `json:"profileArn,omitempty"`
	InferenceConfig   *inferenceConfig  `json:"inferenceConfig,omitempty"`
}

type conversationState struct {
	ChatTriggerType string             `json:"chatTriggerType"`
	ConversationID  string             `json:"conversationId"`
	CurrentMessage  userTurn           `json:"currentMessage"`
	History         []conversationTurn `json:"history"`
}

type userTurn struct {
	UserInputMessage userInputMessage `json:"userInputMessage"`
}

type userInputMessage struct {
	Content string `json:"content"`
	ModelID string `json:"modelId"`
	Origin  string `json:"origin"`
}

type conversationTurn struct {
	UserInputMessage         *userInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *assistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type assistantResponseMessage struct {
	Content  string `json:"content"`
	ToolUses []any  `json:"toolUses"`
}

type inferenceConfig struct {
	MaxTokens   *int     `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

func buildAmazonQPayload(request chatRequest, model, conversation, profileARN string) (amazonQPayload, error) {
	history := make([]conversationTurn, 0, len(request.messages)-1)
	for _, message := range request.messages[:len(request.messages)-1] {
		switch message.role {
		case "user":
			history = append(history, conversationTurn{UserInputMessage: &userInputMessage{
				Content: message.content, ModelID: model, Origin: "AI_EDITOR",
			}})
		case "assistant":
			history = append(history, conversationTurn{AssistantResponseMessage: &assistantResponseMessage{
				Content: message.content, ToolUses: []any{},
			}})
		default:
			return amazonQPayload{}, fmt.Errorf("%w: unsupported translated role", ErrUnsupported)
		}
	}
	last := request.messages[len(request.messages)-1]
	payload := amazonQPayload{
		ConversationState: conversationState{
			ChatTriggerType: "MANUAL",
			ConversationID:  conversation,
			CurrentMessage: userTurn{UserInputMessage: userInputMessage{
				Content: last.content, ModelID: model, Origin: "AI_EDITOR",
			}},
			History: history,
		},
		ProfileARN: profileARN,
	}
	if request.maxTokens != nil || request.temperature != nil || request.topP != nil {
		payload.InferenceConfig = &inferenceConfig{MaxTokens: request.maxTokens, Temperature: request.temperature, TopP: request.topP}
	}
	return payload, nil
}
