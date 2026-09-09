package enterpriseweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxAIBase = "https://api.maxai.me"

func (c *Client) doMaxAI(ctx context.Context, req chatRequest) (*http.Response, error) {
	cred, _, err := c.credential()
	if err != nil || !validMaxAICredential(cred) {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, maxAIBase)
	if err != nil {
		return nil, err
	}
	auth, err := maxAIAuthorization(cred, "/gpt/cwc/chat")
	if err != nil {
		return nil, err
	}
	body, err := buildMaxAIRequest(req, cred.AppVersion)
	if err != nil {
		return nil, err
	}
	headers := http.Header{
		"Accept": []string{"*/*"}, "Accept-Language": []string{"en-CA,en;q=0.9"},
		"Content-Type": []string{"application/json"}, "Origin": []string{"https://www.maxai.co"},
		"Referer": []string{"https://www.maxai.co/"}, "User-Agent": []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:150.0) Gecko/20100101 Firefox/150.0"},
		"Sec-Fetch-Dest": []string{"empty"}, "Sec-Fetch-Mode": []string{"cors"}, "Sec-Fetch-Site": []string{"cross-site"},
		"Authorization": []string{"Bearer " + cred.AccessToken}, "X-Authorization": []string{auth},
		"X-Browser-Name": []string{"Firefox"}, "X-Browser-Version": []string{"150.0"}, "X-Browser-Major": []string{"150"},
		"X-App-Version": []string{cred.AppVersion}, "X-App-Env": []string{"MaxAI-Browser-Extension"},
	}
	resp, err := postJSON(ctx, c.http, joinPath(base, "/gpt/cwc/chat"), body, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		_ = resp.Body.Close()
		return nil, errors.New("enterpriseweb adapter: MaxAI did not return an event stream")
	}
	if req.Stream {
		resp.Body = newConvertedResponse(ctx, resp.Body, req.Model, parseMaxAISSE)
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Set("X-COT-Delivery", "upstream")
		return resp, nil
	}
	return collectResponse(ctx, resp.Body, req.Model, parseMaxAISSE)
}

func buildMaxAIRequest(req chatRequest, appVersion string) ([]byte, error) {
	text, err := assembleMaxAIContext(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	payload := map[string]any{
		"chat_mode": "pro_chat", "conversation_id": randomID(""), "chat_history": []any{},
		"message_content":          []map[string]string{{"type": "text", "text": text}},
		"chrome_extension_version": appVersion, "model_name": req.Model, "prompt_id": "chat", "prompt_name": "chat",
		"prompt_inputs": map[string]string{"RELATED_QUESTION_CNT": "5", "AI_RESPONSE_LANGUAGE": "English"},
		"doc_list":      []any{}, "event_source": "web", "streaming": true, "prompt_type": "freestyle",
		"feature_name": "immersive_chat", "source_type": "NA", "platform_feature": "web_app",
	}
	return json.Marshal(payload)
}

func assembleMaxAIContext(messages []chatMessage) (string, error) {
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			last = i
			break
		}
	}
	if last < 0 {
		return "", errors.New("no user message")
	}
	var system, history []string
	for i, m := range messages {
		if i == last {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" {
			continue
		}
		if m.Role == "system" {
			system = append(system, text)
			continue
		}
		label := "User"
		if m.Role == "assistant" {
			label = "Assistant"
		}
		history = append(history, label+": "+text)
	}
	current := strings.TrimSpace(messages[last].Content)
	out := append([]string{}, system...)
	if len(history) > 0 {
		out = append(out, "=== Conversation so far (for context) ===\n\n"+strings.Join(history, "\n\n"))
		out = append(out, "=== Current request (respond to THIS) ===\n\n"+current)
	} else {
		out = append(out, current)
	}
	return strings.Join(out, "\n\n"), nil
}

func parseMaxAISSE(ctx context.Context, body io.Reader, emit func(string) error) error {
	seen := false
	err := parseSSELines(ctx, body, func(data []byte) error {
		if string(data) == "[DONE]" {
			return errSourceDone
		}
		var f map[string]any
		if json.Unmarshal(data, &f) != nil {
			return nil
		}
		if streamObjectHasError(f) {
			return errors.New("enterpriseweb adapter: MaxAI upstream stream error")
		}
		dataKey, _ := f["data_key"].(string)
		needMerge, _ := f["need_merge"].(bool)
		text, _ := f["text"].(string)
		if dataKey == "text" && needMerge && text != "" {
			seen = true
			return emit(text)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !seen {
		return errors.New("enterpriseweb adapter: MaxAI completed without answer")
	}
	return nil
}
