package majorweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const gensparkChatType = "COPILOT_MOA_CHAT"

func (c *Client) doGenspark(ctx context.Context, protocol, model string, stream bool, body []byte, credential credentials) (*http.Response, error) {
	if protocol != "chat" {
		return nil, &requestError{"major web adapter: Genspark supports only chat protocol"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultGensparkBase)
	if err != nil {
		return nil, err
	}
	modelName := input.Model
	requestModel := strings.TrimSuffix(modelName, "-search")
	if requestModel == "" {
		requestModel = modelName
	}
	payload := map[string]any{
		"type":                 gensparkChatType,
		"current_query_string": "type=" + gensparkChatType,
		"messages":             []any{map[string]any{"role": "user", "content": input.Prompt}},
		"action_params":        map[string]any{},
		"extra_data": map[string]any{
			"models":                 []string{requestModel},
			"run_with_another_model": false,
			"writingContent":         nil,
			"request_web_knowledge":  strings.HasSuffix(modelName, "-search"),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("major web adapter: cannot encode Genspark request")
	}
	endpoint := endpoint(base, "/api/copilot/ask")
	headers := http.Header{
		"Accept":        []string{"text/event-stream"},
		"Content-Type":  []string{"application/json"},
		"Cookie":        []string{credential.cookie},
		"Origin":        []string{base},
		"Referer":       []string{base + "/"},
		"User-Agent":    []string{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
		"Cache-Control": []string{"no-cache"},
	}
	upstream, err := request(ctx, c.http, http.MethodPost, endpoint, data, headers)
	if err != nil {
		return nil, err
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	id := randomID("chatcmpl-")
	streamBody := newProviderStreamBody(upstream.Body, id, model, gensparkEventWithIdentity(id, model))
	return responseFromStream(upstream, streamBody, stream, id, model)
}

func gensparkEventWithIdentity(id, model string) func(string, string) ([]byte, bool, error) {
	return func(_ string, data string) ([]byte, bool, error) {
		if data == "[DONE]" {
			return nil, true, nil
		}
		if strings.TrimSpace(data) == "" {
			return nil, false, nil
		}
		var value struct {
			Type      string `json:"type"`
			FieldName string `json:"field_name"`
			Content   string `json:"content"`
			Delta     string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return nil, false, errors.New("major web adapter: invalid Genspark SSE event")
		}
		switch value.Type {
		case "message_field_delta":
			if value.FieldName == "session_state.answerthink" && value.Delta != "" {
				return chatChunk(id, model, 0, map[string]any{"reasoning_content": value.Delta}, nil), false, nil
			}
		case "message_result":
			if value.Content == "" {
				return nil, false, nil
			}
			return chatChunk(id, model, 0, map[string]any{"content": value.Content}, nil), true, nil
		}
		return nil, false, nil
	}
}
