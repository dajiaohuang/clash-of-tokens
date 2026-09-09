package majorweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

var monicaBots = map[string]string{
	"gpt-5": "gpt_5", "gpt-4o": "gpt_4_o_chat", "gpt-4o-mini": "gpt_4_o_mini_chat", "gpt-4.1": "gpt_4_1", "gpt-4.1-mini": "gpt_4_1_mini", "gpt-4.1-nano": "gpt_4_1_nano", "gpt-4-5": "gpt_4_5_chat", "o1-preview": "openai_o_1", "o3": "o3", "o3-mini": "openai_o_3_mini", "o4-mini": "o4_mini",
	"claude-4-sonnet": "claude_4_sonnet", "claude-4-sonnet-thinking": "claude_4_sonnet_think", "claude-4-opus": "claude_4_opus", "claude-4-opus-thinking": "claude_4_opus_think", "claude-3-7-sonnet-thinking": "claude_3_7_sonnet_think", "claude-3-7-sonnet": "claude_3_7_sonnet", "claude-3-5-sonnet": "claude_3.5_sonnet", "claude-3-5-haiku": "claude_3.5_haiku",
	"gemini-2.5-pro": "gemini_2_5_pro", "gemini-2.5-flash": "gemini_2_5_flash", "gemini-2.0-flash": "gemini_2_0", "gemini-1": "gemini_1_5", "deepseek-reasoner": "deepseek_reasoner", "deepseek-chat": "deepseek_chat", "deepclaude": "deepclaude", "sonar": "sonar", "sonar-reasoning-pro": "sonar_reasoning_pro", "grok-3-beta": "grok_3_beta", "grok-4": "grok_4",
}

func (c *Client) doMonica(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 || monicaBots[model] == "" {
		return nil, &requestError{"Monica requires a supported model and one user text message"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if cred.cookie == "" || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://api.monica.im")
	if err != nil {
		return nil, err
	}
	conv, welcome, message := "conv:"+randomUUID(), "msg:"+randomUUID(), "msg:"+randomUUID()
	items := []any{
		map[string]any{"conversation_id": conv, "item_id": welcome, "item_type": "reply", "data": map[string]any{"type": "text", "content": "__RENDER_BOT_WELCOME_MSG__"}},
		map[string]any{"conversation_id": conv, "item_id": message, "parent_item_id": welcome, "item_type": "question", "data": map[string]any{"type": "text", "content": in.Prompt, "is_incognito": true}},
	}
	p, _ := json.Marshal(map[string]any{"task_uid": "task:" + randomUUID(), "bot_uid": monicaBots[model], "language": "auto", "task_type": "chat", "tool_data": map[string]any{"sys_skill_list": []any{}}, "data": map[string]any{"conversation_id": conv, "pre_parent_item_id": message, "items": items, "trigger_by": "auto", "is_incognito": true, "use_new_memory": false}})
	r, err := request(ctx, c.http, http.MethodPost, base+"/api/custom_bot/chat", p, http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream,application/json"}, "x-client-locale": {"zh_CN"}, "Cookie": {cred.cookie}})
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Monica request failed"}
	}
	answer, err := readMonica(r.Body)
	if err != nil {
		return nil, err
	}
	return appReply(model, answer, "", "stop", stream), nil
}

func readMonica(r io.Reader) (string, error) {
	d := newSSEDecoder(r)
	var text strings.Builder
	total := 0
	for {
		event, raw, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrTruncated
		}
		total += len(raw)
		if total > 4<<20 {
			return "", errors.New("Monica response exceeds limit")
		}
		if event == "error" {
			return "", errors.New("Monica upstream error")
		}
		if raw == "[DONE]" {
			if text.Len() == 0 {
				return "", errors.New("Monica empty response")
			}
			return text.String(), nil
		}
		var v struct {
			Text     string `json:"text"`
			Finished bool   `json:"finished"`
			Code     *int   `json:"code"`
			Error    any    `json:"error"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || v.Error != nil || (v.Code != nil && *v.Code != 0) {
			return "", errors.New("Monica invalid upstream event")
		}
		text.WriteString(v.Text)
		if v.Finished {
			if text.Len() == 0 {
				return "", errors.New("Monica empty response")
			}
			return text.String(), nil
		}
	}
}
