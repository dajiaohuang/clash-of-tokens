package china

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	kimiScenarioK25 = "SCENARIO_K2D5"
	kimiScenarioK26 = "SCENARIO_K2D6"
	glmAssistantID  = "65940acff94777010aa6b796"
	glmSignSecret   = "8a1317a7468aa3ad86e997d08f3f31cb"
	zaiSignSecret   = "key-@@@@)))()((9))-xxxx&&&%%%%%"
	zaiFEVersion    = "prod-fe-1.1.37"
)

func (c *Client) doKimi(ctx context.Context, token string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultKimiBase)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	chatID, parentID := s.kimiChatID, s.kimiParentID
	s.mu.Unlock()
	model := req.Model
	scenario := kimiScenarioK25
	if strings.Contains(strings.ToLower(model), "k2.6") {
		scenario = kimiScenarioK26
	}
	thinking := boolField(req.EnableThinking, req.ReasoningEffort) || strings.Contains(strings.ToLower(model), "think") || strings.Contains(strings.ToLower(model), "r1")
	tools := []any{}
	if req.WebSearch || strings.Contains(strings.ToLower(model), "search") {
		tools = append(tools, map[string]any{"type": "TOOL_TYPE_SEARCH", "search": map[string]any{}})
	}
	payload := map[string]any{
		"scenario": scenario,
		"chat_id":  chatID,
		"tools":    tools,
		"message": map[string]any{
			"parent_id": parentID,
			"role":      "user",
			"blocks": []any{map[string]any{
				"message_id": "",
				"text":       map[string]any{"content": promptForKimi(req.Messages)},
			}},
			"scenario": scenario,
		},
		"options": map[string]any{"thinking": thinking},
	}
	jsonBody, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china web adapter: cannot encode Kimi request")
	}
	frame := make([]byte, 5+len(jsonBody))
	// Connect protocol unary frames are a flags byte followed by a big-endian
	// uint32 payload length and JSON payload.
	frame[0] = 0
	putUint32(frame[1:5], uint32(len(jsonBody)))
	copy(frame[5:], jsonBody)
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Content-Type", "application/connect+json")
	h.Set("Accept", "*/*")
	resp, e := post(ctx, c.http, base+"/apiv2/kimi.gateway.chat.v1.ChatService/Chat", token, "application/connect+json", frame, h)
	if e != nil {
		return nil, e
	}
	return resp, nil
}

func putUint32(dst []byte, n uint32) {
	dst[0] = byte(n >> 24)
	dst[1] = byte(n >> 16)
	dst[2] = byte(n >> 8)
	dst[3] = byte(n)
}

func (c *Client) doQwen(ctx context.Context, token string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultQwenBase)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	chatID, parentID := s.qwenChatID, s.qwenParentID
	s.mu.Unlock()
	if chatID == "" {
		chatID, e = c.qwenCreateChat(ctx, base, token, req.Model)
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		if s.qwenChatID == "" {
			s.qwenChatID = chatID
		} else {
			chatID = s.qwenChatID
		}
		s.mu.Unlock()
	}
	childID := uuid()
	feature := map[string]any{
		"thinking_enabled": boolField(req.EnableThinking, req.ReasoningEffort) || strings.HasSuffix(strings.ToLower(req.Model), "-thinking"),
		"output_schema":    "phase",
		"research_mode":    "normal",
		"auto_thinking":    boolField(req.EnableThinking, req.ReasoningEffort) || strings.HasSuffix(strings.ToLower(req.Model), "-thinking"),
		"thinking_format":  "summary",
		"auto_search":      false,
	}
	if len(req.ReasoningEffort) > 0 && string(req.ReasoningEffort) != "null" {
		var n int
		if json.Unmarshal(req.ReasoningEffort, &n) == nil && n > 0 {
			feature["thinking_budget"] = n
		}
	}
	parent := any(nil)
	if parentID != "" {
		parent = parentID
	}
	message := map[string]any{
		"fid":            uuid(),
		"parentId":       nil,
		"childrenIds":    []string{childID},
		"role":           "user",
		"content":        contentText(req.Messages[0].Content),
		"user_action":    "chat",
		"files":          []any{},
		"timestamp":      time.Now().Unix(),
		"models":         []string{req.Model},
		"chat_type":      "t2t",
		"feature_config": feature,
		"extra":          map[string]any{"meta": map[string]any{"subChatType": "t2t"}},
		"sub_chat_type":  "t2t",
		"parent_id":      nil,
	}
	payload := map[string]any{
		"stream":             true,
		"version":            "2.1",
		"incremental_output": true,
		"chat_id":            chatID,
		"chat_mode":          "normal",
		"model":              req.Model,
		"parent_id":          parent,
		"messages":           []any{message},
		"timestamp":          time.Now().Unix() + 1,
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china web adapter: cannot encode Qwen request")
	}
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Accept", "text/event-stream")
	h.Set("Content-Type", "application/json")
	h.Set("source", "web")
	h.Set("Version", "0.2.7")
	h.Set("X-Request-Id", uuid())
	h.Set("Referer", base+"/c/"+url.PathEscape(chatID))
	resp, e := post(ctx, c.http, base+"/api/v2/chat/completions?chat_id="+url.QueryEscape(chatID), token, "application/json", b, h)
	if e == nil {
		s.mu.Lock()
		s.qwenParentID = childID
		s.mu.Unlock()
	}
	return resp, e
}

func (c *Client) qwenCreateChat(ctx context.Context, base, token, model string) (string, error) {
	payload := map[string]any{"title": "OpenAI_API_Chat", "models": []string{model}, "chat_mode": "normal", "chat_type": "t2t", "timestamp": time.Now().UnixMilli(), "project_id": ""}
	b, e := json.Marshal(payload)
	if e != nil {
		return "", errors.New("china web adapter: cannot encode Qwen chat creation")
	}
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("source", "web")
	h.Set("Version", "0.2.7")
	h.Set("X-Request-Id", uuid())
	resp, e := post(ctx, c.http, base+"/api/v2/chats/new", token, "application/json", b, h)
	if e != nil {
		return "", e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", &HTTPError{Status: status, What: "Qwen chat creation failed"}
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxEventBytes+1))
	if e != nil || len(data) > maxEventBytes {
		return "", errors.New("china web adapter: Qwen chat creation response failed")
	}
	var v struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &v) != nil || v.Data.ID == "" && v.ID == "" {
		return "", errors.New("china web adapter: Qwen chat creation returned no chat id")
	}
	if v.Data.ID != "" {
		return v.Data.ID, nil
	}
	return v.ID, nil
}

func (c *Client) doGLM(ctx context.Context, refreshToken string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultGLMBase)
	if e != nil {
		return nil, e
	}
	token, e := c.glmToken(ctx, base, refreshToken)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	conversationID := s.glmConversation
	s.mu.Unlock()
	chatMode := ""
	if hasReasoning(req.ReasoningEffort) || strings.Contains(strings.ToLower(req.Model), "think") || strings.Contains(strings.ToLower(req.Model), "zero") {
		chatMode = "zero"
	}
	if req.WebSearch {
		// The web protocol calls this networking, independently of chat mode.
	}
	assistantID := glmAssistantID
	if regexp.MustCompile(`^[a-z0-9]{24,}$`).MatchString(req.Model) {
		assistantID = req.Model
	}
	meta := map[string]any{
		"channel": "", "draft_id": "", "if_plus_model": true,
		"input_question_type": "xxxx", "is_networking": req.WebSearch,
		"is_test": false, "platform": "pc", "quote_log_id": "",
		"cogview": map[string]any{"rm_label_watermark": false},
	}
	if chatMode != "" {
		meta["chat_mode"] = chatMode
	}
	prepared := glmMessages(req.Messages)
	payload := map[string]any{
		"assistant_id":    assistantID,
		"conversation_id": conversationID,
		"project_id":      "",
		"chat_type":       "user_chat",
		"messages":        prepared,
		"meta_data":       meta,
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china web adapter: cannot encode GLM request")
	}
	sign := glmSign()
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Accept", "text/event-stream")
	h.Set("Content-Type", "application/json")
	h.Set("App-Name", "chatglm")
	h.Set("X-App-Fr", "browser_extension")
	h.Set("X-App-Platform", "pc")
	h.Set("X-App-Version", "0.0.1")
	h.Set("X-Lang", "zh")
	h.Set("X-Device-Id", uuid())
	h.Set("X-Request-Id", uuid())
	h.Set("X-Sign", sign.sign)
	h.Set("X-Timestamp", sign.timestamp)
	h.Set("X-Nonce", sign.nonce)
	return post(ctx, c.http, base+"/backend-api/assistant/stream", token, "application/json", b, h)
}

func (c *Client) glmToken(ctx context.Context, base, refreshToken string) (string, error) {
	c.glmMu.Lock()
	if c.glmAccess != "" && c.glmRefresh == refreshToken && time.Now().Before(c.glmExpires) {
		v := c.glmAccess
		c.glmMu.Unlock()
		return v, nil
	}
	c.glmMu.Unlock()
	payload := []byte(`{}`)
	sign := glmSign()
	h := browserHeaders(base)
	setBearer(h, refreshToken)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-Device-Id", uuid())
	h.Set("X-Request-Id", uuid())
	h.Set("X-Sign", sign.sign)
	h.Set("X-Timestamp", sign.timestamp)
	h.Set("X-Nonce", sign.nonce)
	resp, e := post(ctx, c.http, base+"/user-api/user/refresh", refreshToken, "application/json", payload, h)
	if e != nil {
		return "", e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", &HTTPError{Status: status, What: "GLM token refresh failed"}
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, maxEventBytes+1))
	if e != nil || len(b) > maxEventBytes {
		return "", errors.New("china web adapter: GLM token response failed")
	}
	var v struct {
		Code   int `json:"code"`
		Status int `json:"status"`
		Result struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &v) != nil || v.Result.Access == "" || v.Code != 0 && v.Status != 0 {
		return "", errors.New("china web adapter: GLM token refresh returned no access token")
	}
	c.glmMu.Lock()
	c.glmAccess = v.Result.Access
	// Keep the cache keyed by the explicit environment value.  A rotated
	// refresh token is deliberately not persisted by this standalone adapter.
	c.glmRefresh = refreshToken
	c.glmExpires = time.Now().Add(50 * time.Minute)
	c.glmMu.Unlock()
	return v.Result.Access, nil
}

func glmMessages(messages []chatMessage) []any {
	if len(messages) != 1 {
		return nil
	}
	return []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": contentText(messages[0].Content)}}}}
}

func glmSign() struct{ timestamp, nonce, sign string } {
	ms := time.Now().UnixMilli()
	digits := fmt.Sprintf("%d", ms)
	sum := 0
	for _, ch := range digits {
		sum += int(ch - '0')
	}
	if len(digits) >= 2 {
		sum -= int(digits[len(digits)-2] - '0')
	}
	if len(digits) >= 2 {
		digits = digits[:len(digits)-2] + fmt.Sprintf("%d", ((sum%10)+10)%10) + digits[len(digits)-1:]
	}
	nonce := uuid()
	h := md5.Sum([]byte(digits + "-" + nonce + "-" + glmSignSecret))
	return struct{ timestamp, nonce, sign string }{digits, nonce, hex.EncodeToString(h[:])}
}

func (c *Client) doZAI(ctx context.Context, token string, req chatRequest, s *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultZAIBase)
	if e != nil {
		return nil, e
	}
	userID := jwtUserID(token)
	s.mu.Lock()
	chatID, parentID := s.zaiChatID, s.zaiParentID
	s.mu.Unlock()
	if chatID == "" {
		chatID, parentID, e = c.zaiCreateChat(ctx, base, token, req.Model)
		if e != nil {
			return nil, e
		}
		s.mu.Lock()
		if s.zaiChatID == "" {
			s.zaiChatID, s.zaiParentID = chatID, parentID
		} else {
			chatID, parentID = s.zaiChatID, s.zaiParentID
		}
		s.mu.Unlock()
	}
	messages := zaiMessages(req.Messages)
	signaturePrompt := lastUserText(req.Messages)
	requestID := uuid()
	timestamp := time.Now().UnixMilli()
	messageID := uuid()
	model := zaiModel(req.Model)
	thinking := true
	if len(req.ReasoningEffort) > 0 {
		var v bool
		if json.Unmarshal(req.ReasoningEffort, &v) == nil {
			thinking = v
		}
	}
	features := map[string]any{
		"image_generation": false, "web_search": false, "auto_web_search": req.WebSearch,
		"preview_mode": true, "flags": []any{}, "vlm_tools_enable": false,
		"vlm_web_search_enable": false, "vlm_website_mode": false, "enable_thinking": thinking,
	}
	childParent := any(nil)
	if parentID != "" {
		childParent = parentID
	}
	payload := map[string]any{
		"stream": true, "model": model, "messages": messages,
		"signature_prompt": signaturePrompt, "params": map[string]any{}, "extra": map[string]any{},
		"features": features,
		"variables": map[string]any{
			"{{USER_NAME}}": "User", "{{USER_LOCATION}}": "Unknown", "{{CURRENT_DATETIME}}": time.Now().Format("2006-01-02 15:04:05"),
			"{{CURRENT_DATE}}": time.Now().Format("2006-01-02"), "{{CURRENT_TIME}}": time.Now().Format("15:04:05"),
			"{{CURRENT_TIMEZONE}}": "Asia/Shanghai", "{{USER_LANGUAGE}}": "zh-CN",
		},
		"chat_id": chatID, "id": requestID, "current_user_message_id": messageID,
		"current_user_message_parent_id": childParent,
		"background_tasks":               map[string]any{"title_generation": true, "tags_generation": true},
	}
	// captcha_verify_param is intentionally absent.  It is a short-lived value
	// minted by the official browser and this adapter neither generates nor
	// replays it.
	b, e := json.Marshal(payload)
	if e != nil {
		return nil, errors.New("china web adapter: cannot encode Z.ai request")
	}
	q := url.Values{}
	q.Set("timestamp", fmt.Sprintf("%d", timestamp))
	q.Set("requestId", requestID)
	q.Set("user_id", userID)
	q.Set("version", "0.0.1")
	q.Set("platform", "web")
	q.Set("token", token)
	q.Set("user_agent", "Mozilla/5.0")
	q.Set("language", "zh-CN")
	q.Set("languages", "zh-CN,zh")
	q.Set("timezone", "Asia/Shanghai")
	q.Set("cookie_enabled", "true")
	q.Set("current_url", base+"/c/"+chatID)
	q.Set("pathname", "/c/"+chatID)
	q.Set("host", "chat.z.ai")
	q.Set("hostname", "chat.z.ai")
	q.Set("protocol", "https:")
	q.Set("title", "Z.ai - Free AI Chatbot & Agent powered by GLM-5 & GLM-4.7")
	q.Set("timezone_offset", "-480")
	q.Set("signature_timestamp", fmt.Sprintf("%d", timestamp))
	signature := zaiSignature(signaturePrompt, requestID, timestamp, userID)
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("X-Signature", signature)
	h.Set("X-FE-Version", zaiFEVersion)
	h.Set("X-Region", "domestic")
	h.Set("Cookie", "token="+token)
	h.Set("Referer", base+"/c/"+url.PathEscape(chatID))
	resp, e := post(ctx, c.http, base+"/api/v2/chat/completions?"+q.Encode(), token, "application/json", b, h)
	return resp, e
}

func (c *Client) zaiCreateChat(ctx context.Context, base, token, model string) (string, string, error) {
	payload := map[string]any{"chat": map[string]any{
		"id": "", "title": "新聊天", "models": []string{zaiModel(model)}, "params": map[string]any{},
		"history": map[string]any{"messages": map[string]any{}, "currentId": ""}, "tags": []any{}, "flags": []any{},
		"features":    []any{map[string]any{"type": "tool_selector", "server": "tool_selector_h", "status": "hidden"}},
		"mcp_servers": []any{}, "enable_thinking": true, "auto_web_search": false, "message_version": 1,
		"extra": map[string]any{}, "timestamp": time.Now().UnixMilli(), "type": "default",
	}}
	b, e := json.Marshal(payload)
	if e != nil {
		return "", "", errors.New("china web adapter: cannot encode Z.ai chat creation")
	}
	h := browserHeaders(base)
	setBearer(h, token)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Cookie", "token="+token)
	h.Set("Referer", base+"/")
	resp, e := post(ctx, c.http, base+"/api/v1/chats/new", token, "application/json", b, h)
	if e != nil {
		return "", "", e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return "", "", &HTTPError{Status: status, What: "Z.ai chat creation failed"}
	}
	defer resp.Body.Close()
	data, e := io.ReadAll(io.LimitReader(resp.Body, maxEventBytes+1))
	if e != nil || len(data) > maxEventBytes {
		if e != nil {
			return "", "", e
		}
		return "", "", errors.New("china web adapter: Z.ai chat creation response exceeds byte limit")
	}
	var v struct {
		ID   string `json:"id"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &v) != nil {
		return "", "", errors.New("china web adapter: invalid Z.ai chat creation response")
	}
	if v.ID == "" {
		v.ID = v.Data.ID
	}
	if v.ID == "" {
		return "", "", errors.New("china web adapter: Z.ai chat creation returned no chat id")
	}
	// Chat creation contains no user message; the first completion therefore
	// has a null parent.  A subsequent assistant id is captured from SSE.
	return v.ID, "", nil
}

func zaiMessages(messages []chatMessage) []chatMessage {
	return messages
}

func zaiModel(model string) string {
	switch strings.ToLower(model) {
	case "glm-5.1":
		return "GLM-5.1"
	case "glm-5-turbo":
		return "GLM-5-Turbo"
	case "glm-5v-turbo":
		return "GLM-5v-Turbo"
	case "glm-5":
		return "glm-5"
	case "glm-4.7":
		return "glm-4.7"
	default:
		return model
	}
}

func jwtUserID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "guest"
	}
	p := parts[1]
	if n := len(p) % 4; n != 0 {
		p += strings.Repeat("=", 4-n)
	}
	b, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if e != nil {
		b, e = base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.ReplaceAll(p, "-", "+"), "_", "/"))
	}
	if e != nil {
		return "guest"
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		return "guest"
	}
	for _, key := range []string{"id", "user_id", "uid", "sub"} {
		if s, ok := v[key].(string); ok && s != "" {
			return s
		}
	}
	return "guest"
}

func zaiSignature(message, requestID string, timestamp int64, userID string) string {
	canonical := fmt.Sprintf("requestId,%s,timestamp,%d,user_id,%s|%s|%d", requestID, timestamp, userID, base64.StdEncoding.EncodeToString([]byte(message)), timestamp)
	window := fmt.Sprintf("%d", timestamp/(5*60*1000))
	h1 := hmac.New(sha256.New, []byte(zaiSignSecret))
	_, _ = h1.Write([]byte(window))
	key := hex.EncodeToString(h1.Sum(nil))
	h2 := hmac.New(sha256.New, []byte(key))
	_, _ = h2.Write([]byte(canonical))
	return hex.EncodeToString(h2.Sum(nil))
}

func boolField(v *bool, raw json.RawMessage) bool {
	if v != nil {
		return *v
	}
	return hasReasoning(raw)
}
func hasReasoning(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null" && string(raw) != "false" && string(raw) != `""`
}
