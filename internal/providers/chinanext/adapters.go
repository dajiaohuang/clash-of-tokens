package chinanext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	dolaBotID        = "7339470689562525703"
	yuanbaoAgentID   = "naQivTmsDa"
	deepSeekAPIPath  = "/api"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/145.0.0.0 Safari/537.36"
)

func browserHeaders(origin string) http.Header {
	h := http.Header{}
	h.Set("Accept", "*/*")
	h.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	h.Set("Cache-Control", "no-cache")
	h.Set("Origin", origin)
	h.Set("Pragma", "no-cache")
	h.Set("Referer", strings.TrimRight(origin, "/")+"/")
	h.Set("User-Agent", defaultUserAgent)
	return h
}

func endpoint(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return base + suffix
}

func cookieValue(raw, name string) string {
	for _, item := range strings.Split(strings.TrimSpace(strings.TrimPrefix(raw, "Cookie:")), ";") {
		pair := strings.SplitN(strings.TrimSpace(item), "=", 2)
		if len(pair) != 2 || pair[0] != name {
			continue
		}
		return strings.TrimSpace(pair[1])
	}
	return ""
}

func cookieHeader(raw string, names ...string) string {
	var values []string
	for _, name := range names {
		if value := cookieValue(raw, name); value != "" {
			values = append(values, name+"="+value)
		}
	}
	return strings.Join(values, "; ")
}

func dolaCookie(raw string) (string, error) {
	// Dola requires a logged-in session and a browser fingerprint. Forward only
	// the narrowly required cookie names; arbitrary caller cookies never leave
	// the gateway.
	selected := cookieHeader(raw, "sessionid", "ttwid", "s_v_web_id", "fp", "sessionid_ss", "sid_guard", "sid_tt")
	if cookieValue(selected, "sessionid") == "" {
		return "", ErrCredential
	}
	if cookieValue(selected, "s_v_web_id") == "" && cookieValue(selected, "fp") == "" {
		return "", ErrCredential
	}
	return selected, nil
}

func yuanbaoCookie(raw string) (string, error) {
	hyUser, hyToken := cookieValue(raw, "hy_user"), cookieValue(raw, "hy_token")
	if hyUser == "" || hyToken == "" {
		return "", ErrCredential
	}
	return "hy_source=web; hy_user=" + hyUser + "; hy_token=" + hyToken, nil
}

func uuidNoHyphen() string { return strings.ReplaceAll(uuid(), "-", "") }

func (c *Client) doDola(ctx context.Context, rawCookie string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultDolaBase)
	if e != nil {
		return nil, e
	}
	cookie, e := dolaCookie(rawCookie)
	if e != nil {
		return nil, e
	}
	localConversationID := ""
	s.mu.Lock()
	localConversationID = s.conversationID
	s.mu.Unlock()
	if localConversationID == "" {
		localConversationID = "local_" + uuidNoHyphen()
		s.mu.Lock()
		s.conversationID = localConversationID
		s.mu.Unlock()
	}
	prompt := contentText(req.Messages[0].Content)
	model := req.Model
	deepThink := 0
	if strings.Contains(strings.ToLower(model), "think") || strings.Contains(strings.ToLower(model), "reason") || boolField(req.EnableThinking, req.ReasoningEffort) {
		deepThink = 3
	}
	if req.EnableThinking != nil && !*req.EnableThinking {
		deepThink = 0
	}
	payload := map[string]any{
		"client_meta": map[string]any{
			"local_conversation_id": localConversationID,
			"conversation_id":       "", "bot_id": dolaBotID,
			"last_section_id": "", "last_message_index": nil,
		},
		"messages": []any{map[string]any{
			"local_message_id": uuid(),
			"content_block": []any{map[string]any{
				"block_type": 10000,
				"content":    map[string]any{"text_block": map[string]any{"text": prompt, "icon_url": "", "icon_url_dark": "", "summary": ""}, "pc_event_block": ""},
				"block_id":   uuid(), "parent_id": "", "meta_info": []any{}, "append_fields": []any{},
			}},
			"message_status": 0,
		}},
		"option": map[string]any{
			"send_message_scene": "", "create_time_ms": time.Now().UnixMilli(), "collect_id": "",
			"is_audio": false, "answer_with_suggest": false, "tts_switch": false,
			"need_deep_think": deepThink, "click_clear_context": false, "from_suggest": false,
			"is_regen": false, "is_replace": false, "is_from_click_option": false,
			"disable_sse_cache": false, "scene_type": 0, "unique_key": uuid(), "start_seq": 0,
			"need_create_conversation": true, "conversation_init_option": map[string]any{"need_ack_conversation": true},
			"sse_recv_event_options": map[string]any{"support_chunk_delta": true},
		},
		"user_context": []any{},
		"ext":          map[string]any{"use_deep_think": fmt.Sprintf("%d", deepThink), "fp": cookieValue(cookie, "s_v_web_id")},
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china next web adapter: cannot encode Doubao request")
	}
	h := browserHeaders(base)
	h.Set("Cookie", cookie)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("Agw-Js-Conv", "str")
	query := url.Values{"aid": {"495671"}, "real_aid": {"495671"}, "device_platform": {"web"}, "device_id": {uuidNoHyphen()}, "web_tab_id": {uuid()}, "version_code": {"20800"}, "language": {"en"}, "region": {"US"}}
	urlPath := endpoint(base, "/chat/completion") + "?" + query.Encode()
	return post(ctx, c.http, urlPath, "application/json", b, h)
}

func (c *Client) doYuanbao(ctx context.Context, rawCookie string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultYuanbaoBase)
	if e != nil {
		return nil, e
	}
	cookie, e := yuanbaoCookie(rawCookie)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	conversationID := s.conversationID
	s.mu.Unlock()
	if conversationID == "" {
		conversationID, e = c.createYuanbaoConversation(ctx, base, cookie)
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		s.conversationID = conversationID
		s.mu.Unlock()
	}
	modelID := yuanbaoModel(req.Model)
	payload := map[string]any{
		"model": "gpt_175B_0404", "prompt": contentText(req.Messages[0].Content),
		"displayPrompt": contentText(req.Messages[0].Content), "displayPromptType": 1,
		"plugin": "Adaptive", "multimedia": []any{}, "agentId": yuanbaoAgentID,
		"supportHint": 1, "version": "v2", "chatModelId": modelID,
		"options": map[string]any{"imageIntention": map[string]any{"needIntentionModel": true, "backendUpdateFlag": 2, "intentionStatus": true}},
	}
	if strings.HasSuffix(strings.ToLower(req.Model), "-search") || req.WebSearch {
		payload["supportFunctions"] = []string{"supportInternetSearch"}
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china next web adapter: cannot encode Yuanbao request")
	}
	h := browserHeaders(base)
	h.Set("Cookie", cookie)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("X-Agentid", yuanbaoAgentID)
	return post(ctx, c.http, endpoint(base, "/api/chat/")+url.PathEscape(conversationID), "application/json", b, h)
}

func (c *Client) createYuanbaoConversation(ctx context.Context, base, cookie string) (string, error) {
	b := []byte(`{"agentId":"naQivTmsDa"}`)
	h := browserHeaders(base)
	h.Set("Cookie", cookie)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-Agentid", yuanbaoAgentID)
	resp, e := post(ctx, c.http, endpoint(base, "/api/user/agent/conversation/create"), "application/json", b, h)
	if e != nil {
		return "", e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", &HTTPError{Status: status, What: "Yuanbao conversation creation failed"}
	}
	data, e := boundedRead(resp.Body)
	resp.Body.Close()
	if e != nil {
		return "", e
	}
	var value struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &value) != nil || value.ID == "" {
		return "", errors.New("china next web adapter: Yuanbao returned no conversation id")
	}
	return value.ID, nil
}

func yuanbaoModel(model string) string {
	switch strings.ToLower(model) {
	case "deepseek-v3", "deepseek-v3-search":
		return "deep_seek_v3"
	case "deepseek-r1", "deepseek-r1-search":
		return "deep_seek"
	case "hunyuan", "hunyuan-search":
		return "hunyuan_gpt_175B_0404"
	case "hunyuan-t1", "hunyuan-t1-search":
		return "hunyuan_t1"
	default:
		return model
	}
}

func (c *Client) doDeepSeek(ctx context.Context, rawToken string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultDeepSeekBase)
	if e != nil {
		return nil, e
	}
	userToken, e := deepSeekUserToken(rawToken)
	if e != nil {
		return nil, e
	}
	accessToken, e := c.deepSeekAccessToken(ctx, base, userToken)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	sessionID := s.deepSessionID
	s.mu.Unlock()
	if sessionID == "" {
		sessionID, e = c.createDeepSeekSession(ctx, base, accessToken)
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		s.deepSessionID = sessionID
		s.mu.Unlock()
	}
	challenge, e := c.deepSeekChallenge(ctx, base, accessToken)
	if e != nil {
		return nil, e
	}
	nonce, e := solvePow(ctx, challenge)
	if e != nil {
		return nil, e
	}
	think := boolField(req.EnableThinking, req.ReasoningEffort) || deepSeekThinking(req.Model)
	if req.EnableThinking != nil {
		think = *req.EnableThinking
	}
	search := req.WebSearch || strings.Contains(strings.ToLower(req.Model), "search")
	payload := map[string]any{
		"chat_session_id": sessionID, "parent_message_id": nil, "model_type": deepSeekModelType(req.Model),
		"prompt": contentText(req.Messages[0].Content), "thinking_enabled": think, "search_enabled": search, "preempt": false,
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china next web adapter: cannot encode DeepSeek request")
	}
	h := browserHeaders(base)
	bearer(h, accessToken)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("X-Ds-Pow-Response", encodePowAnswer(challenge, nonce))
	_, offset := time.Now().Zone()
	h.Set("X-Client-Timezone-Offset", fmt.Sprintf("%d", -offset/60))
	return post(ctx, c.http, endpoint(base, deepSeekAPIPath+"/v0/chat/completion"), "application/json", b, h)
}

func (c *Client) deepSeekAccessToken(ctx context.Context, base, userToken string) (string, error) {
	c.accessMu.Lock()
	if c.access != "" && c.accessFor == userToken && time.Now().Before(c.expires) {
		value := c.access
		c.accessMu.Unlock()
		return value, nil
	}
	c.accessMu.Unlock()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(base, "/api/v0/users/current"), nil)
	if e != nil {
		return "", e
	}
	h := browserHeaders(base)
	bearer(h, userToken)
	req.Header = h
	resp, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", errors.New("china next web adapter: DeepSeek credential request failed")
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		resp.Body.Close()
		return "", &HTTPError{Status: resp.StatusCode, What: "DeepSeek user token rejected"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", &HTTPError{Status: status, What: "DeepSeek credential request failed"}
	}
	data, e := boundedRead(resp.Body)
	resp.Body.Close()
	if e != nil {
		return "", e
	}
	var value struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			BizData struct {
				Token string `json:"token"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &value) != nil || value.Code != 0 || value.Data.BizData.Token == "" {
		return "", ErrCredential
	}
	c.accessMu.Lock()
	c.access, c.accessFor, c.expires = value.Data.BizData.Token, userToken, time.Now().Add(50*time.Minute)
	c.accessMu.Unlock()
	return value.Data.BizData.Token, nil
}

func (c *Client) createDeepSeekSession(ctx context.Context, base, token string) (string, error) {
	h := browserHeaders(base)
	bearer(h, token)
	h.Set("Content-Type", "application/json")
	resp, e := post(ctx, c.http, endpoint(base, "/api/v0/chat_session/create"), "application/json", []byte(`{}`), h)
	if e != nil {
		return "", e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", &HTTPError{Status: status, What: "DeepSeek session creation failed"}
	}
	data, e := boundedRead(resp.Body)
	resp.Body.Close()
	if e != nil {
		return "", e
	}
	var value struct {
		Data struct {
			BizData struct {
				ChatSession struct {
					ID string `json:"id"`
				} `json:"chat_session"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &value) != nil || value.Data.BizData.ChatSession.ID == "" {
		return "", errors.New("china next web adapter: DeepSeek returned no session id")
	}
	return value.Data.BizData.ChatSession.ID, nil
}

func (c *Client) deepSeekChallenge(ctx context.Context, base, token string) (powChallenge, error) {
	h := browserHeaders(base)
	bearer(h, token)
	h.Set("Content-Type", "application/json")
	resp, e := post(ctx, c.http, endpoint(base, "/api/v0/chat/create_pow_challenge"), "application/json", []byte(`{"target_path":"/api/v0/chat/completion"}`), h)
	if e != nil {
		return powChallenge{}, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return powChallenge{}, &HTTPError{Status: status, What: "DeepSeek PoW challenge failed"}
	}
	data, e := boundedRead(resp.Body)
	resp.Body.Close()
	if e != nil {
		return powChallenge{}, e
	}
	var value struct {
		Data struct {
			BizData struct {
				Challenge powChallenge `json:"challenge"`
			} `json:"biz_data"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &value) != nil || value.Data.BizData.Challenge.Challenge == "" {
		return powChallenge{}, errors.New("china next web adapter: DeepSeek returned no PoW challenge")
	}
	return value.Data.BizData.Challenge, nil
}

func deepSeekUserToken(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	var wrapped struct {
		Value string `json:"value"`
	}
	if strings.HasPrefix(raw, "{") && json.Unmarshal([]byte(raw), &wrapped) == nil && wrapped.Value != "" {
		return wrapped.Value, nil
	}
	if raw == "" {
		return "", ErrCredential
	}
	return raw, nil
}

func deepSeekThinking(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "r1") || strings.Contains(m, "think") || strings.Contains(m, "reason")
}
func deepSeekModelType(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "pro") || strings.Contains(m, "expert") {
		return "expert"
	}
	return "default"
}
func boolField(v *bool, raw json.RawMessage) bool {
	if v != nil {
		return *v
	}
	var b bool
	return len(raw) > 0 && json.Unmarshal(raw, &b) == nil && b
}

func boundedRead(body io.Reader) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(body, maxEventBytes+1))
	if e != nil {
		return nil, errors.New("china next web adapter: upstream response read failed")
	}
	if len(b) > maxEventBytes {
		return nil, errors.New("china next web adapter: upstream response exceeds byte limit")
	}
	return b, nil
}
