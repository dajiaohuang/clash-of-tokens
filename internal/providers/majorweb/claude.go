package majorweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	claudeZeroParent   = "00000000-0000-4000-8000-000000000000"
	claudeDefaultModel = "default"
)

func (c *Client) doClaude(ctx context.Context, protocol, model string, stream bool, body []byte, credential credentials) (*http.Response, error) {
	if protocol != "chat" {
		return nil, &requestError{"major web adapter: Claude Web supports only chat protocol"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultClaudeBase)
	if err != nil {
		return nil, err
	}
	orgID := credential.orgID
	if orgID == "" {
		orgID, err = c.claudeOrganization(ctx, base, credential.cookie)
		if err != nil {
			return nil, err
		}
	}
	conversationID, resp, err := c.claudeCreateConversation(ctx, base, orgID, input.Model, credential.cookie)
	if err != nil {
		return nil, err
	}
	if resp != nil {
		return resp, nil
	}

	payload := map[string]any{
		"prompt":              input.Prompt,
		"parent_message_uuid": claudeZeroParent,
		"attachments":         []any{},
		"files":               []any{},
		"sync_sources":        []any{},
		"rendering_mode":      "messages",
		"timezone":            "America/Los_Angeles",
		"personalized_styles": []any{},
		"tools":               []any{},
	}
	if input.Model != "" && input.Model != claudeDefaultModel {
		payload["model"] = input.Model
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.New("major web adapter: cannot encode Claude request")
	}
	endpoint := base + "/api/organizations/" + url.PathEscape(orgID) + "/chat_conversations/" + url.PathEscape(conversationID) + "/completion"
	headers := claudeHeaders(credential.cookie, base, base+"/chat/"+conversationID, true)
	upstream, err := request(ctx, c.http, http.MethodPost, endpoint, data, headers)
	if err != nil {
		return nil, err
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	id := randomID("chatcmpl-")
	streamBody := newProviderStreamBody(upstream.Body, id, model, claudeEventWithIdentity(id, model))
	return responseFromStream(upstream, streamBody, stream, id, model)
}

func claudeHeaders(cookie, base, referer string, completion bool) http.Header {
	accept := "text/event-stream"
	if !completion {
		accept = "application/json"
	}
	return http.Header{
		"Accept":                    []string{accept},
		"Accept-Language":           []string{"zh-CN,zh;q=0.9,en;q=0.8"},
		"Content-Type":              []string{"application/json"},
		"Cookie":                    []string{cookieHeader("sessionKey", cookie)},
		"Origin":                    []string{base},
		"Referer":                   []string{referer},
		"anthropic-client-platform": []string{"web_claude_ai"},
		"Cache-Control":             []string{"no-cache"},
		"User-Agent":                []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
	}
}

func (c *Client) claudeOrganization(ctx context.Context, base, cookie string) (string, error) {
	endpoint := base + "/api/organizations"
	headers := claudeHeaders(cookie, base, base+"/new", false)
	resp, err := request(ctx, c.http, http.MethodGet, endpoint, nil, headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &HTTPError{Status: resp.StatusCode, What: "Claude organization lookup failed"}
	}
	data, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return "", err
	}
	var orgs []struct {
		UUID          string `json:"uuid"`
		RateLimitTier string `json:"rate_limit_tier"`
	}
	if err := json.Unmarshal(data, &orgs); err != nil || len(orgs) == 0 {
		return "", errors.New("major web adapter: Claude organization response is invalid")
	}
	if len(orgs) == 1 && strings.TrimSpace(orgs[0].UUID) != "" {
		return orgs[0].UUID, nil
	}
	for _, org := range orgs {
		if strings.TrimSpace(org.UUID) != "" && (org.RateLimitTier == "default_claude_ai" || org.RateLimitTier == "default_claude_max_20x" || org.RateLimitTier == "default_raven_enterprise") {
			return org.UUID, nil
		}
	}
	return "", errors.New("major web adapter: Claude organization response has no usable organization")
}

func (c *Client) claudeCreateConversation(ctx context.Context, base, orgID, model, cookie string) (string, *http.Response, error) {
	payload := map[string]any{
		"uuid":                             randomUUID(),
		"name":                             "",
		"include_conversation_preferences": true,
	}
	// The explicit product-default alias omits model selection. Versioned
	// model requests are sent unchanged and may be rejected by the provider.
	if model != "" && model != claudeDefaultModel {
		payload["model"] = model
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.New("major web adapter: cannot encode Claude conversation request")
	}
	endpoint := base + "/api/organizations/" + url.PathEscape(orgID) + "/chat_conversations"
	resp, err := request(ctx, c.http, http.MethodPost, endpoint, data, claudeHeaders(cookie, base, base+"/new", false))
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", resp, nil
	}
	defer resp.Body.Close()
	data, err = readBounded(resp.Body, 1<<20)
	if err != nil {
		return "", nil, err
	}
	var result struct {
		UUID string `json:"uuid"`
	}
	if json.Unmarshal(data, &result) != nil || strings.TrimSpace(result.UUID) == "" {
		return "", nil, errors.New("major web adapter: Claude conversation response has no UUID")
	}
	return result.UUID, nil, nil
}

// claudeEventWithIdentity adapts Claude events while keeping one stable
// response ID/model for all emitted chunks.
func claudeEventWithIdentity(id, model string) func(string, string) ([]byte, bool, error) {
	return func(event, data string) ([]byte, bool, error) {
		if data == "[DONE]" || event == "message_stop" {
			return nil, true, nil
		}
		if strings.TrimSpace(data) == "" {
			return nil, false, nil
		}
		var value struct {
			Type  string `json:"type"`
			Delta struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"delta"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &value); err != nil {
			return nil, false, errors.New("major web adapter: invalid Claude SSE event")
		}
		if value.Type == "error" || value.Error.Message != "" {
			return nil, false, errors.New("major web adapter: Claude upstream request failed")
		}
		switch value.Delta.Type {
		case "text_delta":
			if value.Delta.Text != "" {
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"content": value.Delta.Text}, nil), false, nil
			}
		case "thinking_delta":
			if value.Delta.Thinking != "" {
				return chatChunk(id, model, time.Now().Unix(), map[string]any{"reasoning_content": value.Delta.Thinking}, nil), false, nil
			}
		}
		return nil, false, nil
	}
}
