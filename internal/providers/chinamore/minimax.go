package chinamore

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	minimaxRegisterPath = "/v1/api/user/device/register"
	minimaxSendPath     = "/matrix/api/v1/chat/send_msg"
	minimaxDetailPath   = "/matrix/api/v1/chat/get_chat_detail"
	minimaxPollDelay    = 250 * time.Millisecond
)

type miniCredential struct {
	JWT      string
	RealUser string
}

type miniDevice struct {
	DeviceID  string
	RealUser  string
	JWT       string
	ExpiresAt time.Time
}

func parseMiniCredential(raw string) (miniCredential, error) {
	var out miniCredential
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	if raw == "" {
		return out, ErrCredential
	}
	if strings.HasPrefix(raw, "{") {
		var v struct {
			Token      string `json:"token"`
			JWT        string `json:"jwt_token"`
			RealUserID string `json:"realUserID"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil {
			return out, ErrCredential
		}
		out.JWT = strings.TrimSpace(v.Token)
		if out.JWT == "" {
			out.JWT = strings.TrimSpace(v.JWT)
		}
		out.RealUser = strings.TrimSpace(v.RealUserID)
	} else if i := strings.IndexByte(raw, '+'); i > 0 {
		out.RealUser = strings.TrimSpace(raw[:i])
		out.JWT = strings.TrimSpace(raw[i+1:])
	} else {
		out.JWT = raw
	}
	if out.JWT == "" {
		return miniCredential{}, ErrCredential
	}
	if out.RealUser == "" {
		out.RealUser = jwtUserID(out.JWT)
	}
	if out.RealUser == "" {
		return miniCredential{}, fmt.Errorf("%w: MiniMax realUserID is required or must be present in JWT user.id", ErrCredential)
	}
	return out, nil
}

func jwtUserID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return ""
	}
	var v struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		UserID string `json:"user_id"`
	}
	if json.Unmarshal(payload, &v) != nil {
		return ""
	}
	if v.User.ID != "" {
		return v.User.ID
	}
	return v.UserID
}

// minimaxQuery mirrors the ordered browser query used by Chat2API's Agent
// adapter.  Values are intentionally explicit; no caller headers are reused.
func minimaxQuery(userID, jwt, uuid, deviceID string) string {
	values := []string{
		"device_platform=web", "biz_id=3", "app_id=3001", "version_code=22201",
		"uuid=" + uuid,
	}
	if deviceID != "" {
		values = append(values, "device_id="+deviceID)
	} else {
		// FAKE_USER_DATA initializes device_id to null for the source's
		// registration request; subsequent requests replace it with a device ID.
		values = append(values, "device_id=null")
	}
	values = append(values,
		"os_name=Mac", "browser_name=chrome", "device_memory=8", "cpu_core_num=11",
		"browser_language=zh-CN", "browser_platform=MacIntel", "user_id="+userID,
		"screen_width=1920", "screen_height=1080", "unix="+fmt.Sprint(time.Now().UnixMilli()),
		"lang=zh", "token="+jwt, "timezone_offset=28800", "sys_language=zh", "client=web",
	)
	return strings.Join(values, "&")
}

func (c *Client) miniDeviceInfo(ctx context.Context, cred miniCredential, base string) (miniDevice, error) {
	cacheKey := cred.RealUser + "\x00" + cred.JWT
	c.deviceMu.Lock()
	if cached, ok := c.devices[cacheKey]; ok && time.Now().Before(cached.ExpiresAt) {
		c.deviceMu.Unlock()
		return cached, nil
	}
	c.deviceMu.Unlock()

	deviceUUID := uuid()
	payload := []byte(`{"uuid":"` + deviceUUID + `"}`)
	query := minimaxQuery(cred.RealUser, cred.JWT, deviceUUID, "")
	uri := minimaxRegisterPath + "?" + query
	timestamp := time.Now().Unix()
	// The source signs timestamp + JWT + JSON and computes yy over the URI,
	// payload, millisecond nonce, and the literal suffix used by the web app.
	unix := queryValue(query, "unix")
	yy := md5Hex(encodeURIComponent(uri) + "_" + string(payload) + md5Hex(unix) + "ooui")
	signature := md5Hex(fmt.Sprintf("%d%s%s", timestamp, cred.JWT, payload))
	h := browserHeaders(base)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json, text/plain, */*")
	h.Set("Referer", strings.TrimRight(base, "/")+"/")
	h.Set("token", cred.JWT)
	h.Set("x-timestamp", fmt.Sprint(timestamp))
	h.Set("x-signature", signature)
	h.Set("yy", yy)
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, e := postJSON(requestCtx, c.http, base+uri, payload, h)
	if e != nil {
		return miniDevice{}, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.Body != nil {
			resp.Body.Close()
		}
		return miniDevice{}, &HTTPError{Status: resp.StatusCode, What: "MiniMax device registration failed"}
	}
	var root map[string]any
	if e = readLimitedJSON(resp, 1<<20, &root); e != nil {
		return miniDevice{}, errors.New("china more web adapter: invalid MiniMax device response")
	}
	statusInfo, _ := root["statusInfo"].(map[string]any)
	if code := jsonNumber(statusInfo["code"]); code != 0 {
		return miniDevice{}, fmt.Errorf("china more web adapter: MiniMax device registration rejected (code %d)", code)
	}
	data, _ := root["data"].(map[string]any)
	deviceID := rawMapString(data, "deviceIDStr")
	if deviceID == "" {
		deviceID = rawMapString(data, "device_id")
	}
	if deviceID == "" {
		return miniDevice{}, errors.New("china more web adapter: MiniMax device response omitted device ID")
	}
	realUser := rawMapString(data, "realUserID")
	if realUser == "" {
		realUser = cred.RealUser
	}
	device := miniDevice{DeviceID: deviceID, RealUser: realUser, JWT: cred.JWT, ExpiresAt: time.Now().Add(3 * time.Hour)}
	c.deviceMu.Lock()
	c.devices[cacheKey] = device
	c.deviceMu.Unlock()
	return device, nil
}

func queryValue(query, wanted string) string {
	for _, field := range strings.Split(query, "&") {
		parts := strings.SplitN(field, "=", 2)
		if len(parts) == 2 && parts[0] == wanted {
			return parts[1]
		}
	}
	return ""
}

func (c *Client) miniRequest(ctx context.Context, base string, cred miniDevice, path string, payload []byte, accept string) (*http.Response, error) {
	deviceUUID := cred.RealUser
	query := minimaxQuery(cred.RealUser, cred.JWT, deviceUUID, cred.DeviceID)
	uri := path + "?" + query
	timestamp := time.Now().Unix()
	yy := md5Hex(encodeURIComponent(uri) + "_" + string(payload) + md5Hex(queryValue(query, "unix")) + "ooui")
	signature := md5Hex(fmt.Sprintf("%d%s%s", timestamp, cred.JWT, payload))
	h := browserHeaders(base)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", accept)
	h.Set("Referer", strings.TrimRight(base, "/")+"/")
	h.Set("token", cred.JWT)
	h.Set("x-timestamp", fmt.Sprint(timestamp))
	h.Set("x-signature", signature)
	h.Set("yy", yy)
	return postJSON(ctx, c.http, base+uri, payload, h)
}

func (c *Client) doMiniMax(ctx context.Context, raw string, req chatRequest, stream bool, s *session) (*http.Response, error) {
	if req.Model != "default" {
		return nil, fmt.Errorf("%w: MiniMax only exposes product-default model selection", ErrUnsupported)
	}
	base, e := baseURL(c.source, defaultMiniMaxBase)
	if e != nil {
		return nil, e
	}
	credential, e := parseMiniCredential(raw)
	if e != nil {
		return nil, e
	}
	device, e := c.miniDeviceInfo(ctx, credential, base)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	chatID := s.miniChatID
	s.mu.Unlock()
	body := map[string]any{
		"msg_type":  1,
		"text":      "user:" + contentText(req.Messages[0].Content) + "\n",
		"chat_type": 1, "attachments": []any{}, "selected_mcp_tools": []any{},
		"backend_config": map[string]any{}, "sub_agent_ids": []any{},
	}
	if chatID != "" {
		body["chat_id"] = chatID
	}
	payload, _ := json.Marshal(body)
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, e := c.miniRequest(requestCtx, base, device, minimaxSendPath, payload, "application/json, text/plain, */*")
	if e != nil {
		return nil, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	var root map[string]any
	if e = readLimitedJSON(resp, 1<<20, &root); e != nil {
		return nil, errors.New("china more web adapter: invalid MiniMax send response")
	}
	baseResp, _ := root["base_resp"].(map[string]any)
	if code := jsonNumber(baseResp["status_code"]); code != 0 {
		return nil, fmt.Errorf("china more web adapter: MiniMax send rejected (code %d)", code)
	}
	// The send result's msg_id identifies the submitted user message, not
	// necessarily the generated assistant message. Fresh chat_id isolation is
	// sufficient for this single-turn adapter; continuation remains rejected.
	messageID := ""
	if chatID == "" {
		chatID = jsonID(root["chat_id"])
		if chatID == "" {
			chatID = jsonID(root["chatID"])
		}
		if chatID == "" {
			return nil, errors.New("china more web adapter: MiniMax send response omitted chat id")
		}
		s.mu.Lock()
		s.miniChatID = chatID
		s.mu.Unlock()
	}
	if stream {
		reader, writer := io.Pipe()
		go func() {
			e := c.pollMiniMaxWithMessageID(ctx, base, device, chatID, messageID, req.Model, writer)
			if e != nil {
				_ = writer.CloseWithError(e)
			} else {
				_ = writer.Close()
			}
		}()
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
	}
	var out bytes.Buffer
	if e = c.pollMiniMaxToWithMessageID(ctx, base, device, chatID, messageID, req.Model, &out, false); e != nil {
		return nil, e
	}
	return bodyResponse(200, http.Header{"Content-Type": []string{"application/json"}}, out.Bytes()), nil
}

func jsonID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case json.Number:
		return v.String()
	}
	return ""
}

func (c *Client) pollMiniMax(ctx context.Context, base string, device miniDevice, chatID, model string, sink io.Writer) error {
	return c.pollMiniMaxWithMessageID(ctx, base, device, chatID, "", model, sink)
}

func (c *Client) pollMiniMaxTo(ctx context.Context, base string, device miniDevice, chatID, model string, sink io.Writer, stream bool) error {
	return c.pollMiniMaxToWithMessageID(ctx, base, device, chatID, "", model, sink, stream)
}

func (c *Client) pollMiniMaxWithMessageID(ctx context.Context, base string, device miniDevice, chatID, messageID, model string, sink io.Writer) error {
	return c.pollMiniMaxToWithMessageID(ctx, base, device, chatID, messageID, model, sink, true)
}

func (c *Client) pollMiniMaxToWithMessageID(ctx context.Context, base string, device miniDevice, chatID, expectedMessageID, model string, sink io.Writer, stream bool) error {
	var lastContent, lastThinking, lastMsg string
	var emitter *streamEmitter
	if stream {
		emitter = newEmitter(sink, chatID, model)
	}
	for poll := 0; poll < maxPolls; poll++ {
		if poll > 0 {
			t := time.NewTimer(minimaxPollDelay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
		body, _ := json.Marshal(map[string]any{"chat_id": chatID})
		requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		resp, e := c.miniRequest(requestCtx, base, device, minimaxDetailPath, body, "application/json, text/plain, */*")
		if e != nil {
			cancel()
			return e
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.Body != nil {
				resp.Body.Close()
			}
			cancel()
			return &HTTPError{Status: resp.StatusCode, What: "MiniMax detail request failed"}
		}
		var root map[string]any
		if e = readLimitedJSON(resp, 4<<20, &root); e != nil {
			cancel()
			return errors.New("china more web adapter: invalid MiniMax detail response")
		}
		cancel()
		baseResp, _ := root["base_resp"].(map[string]any)
		if code := jsonNumber(baseResp["status_code"]); code != 0 {
			return fmt.Errorf("china more web adapter: MiniMax detail rejected (code %d)", code)
		}
		messages, _ := root["messages"].([]any)
		var ai map[string]any
		for _, raw := range messages {
			if m, ok := raw.(map[string]any); ok && jsonNumber(m["msg_type"]) == 2 {
				if expectedMessageID == "" || jsonID(m["msg_id"]) == expectedMessageID {
					ai = m
				}
			}
		}
		if ai == nil {
			continue
		}
		content := rawMapString(ai, "msg_content")
		thinking := ""
		if extra, ok := ai["extra_info"].(map[string]any); ok {
			thinking = rawMapString(extra, "thinking_content")
		}
		msgID := jsonID(ai["msg_id"])
		if msgID != "" && msgID != lastMsg && lastMsg != "" {
			return errors.New("china more web adapter: MiniMax changed assistant message during generation")
		}
		if stream {
			if !strings.HasPrefix(thinking, lastThinking) || !strings.HasPrefix(content, lastContent) {
				return errors.New("china more web adapter: MiniMax revised emitted content")
			}
			if len(thinking) > len(lastThinking) {
				if e = emitter.chunk(map[string]any{"reasoning_content": thinking[len(lastThinking):]}); e != nil {
					return e
				}
				lastThinking = thinking
			}
			if len(content) > len(lastContent) {
				if e = emitter.chunk(map[string]any{"content": content[len(lastContent):]}); e != nil {
					return e
				}
				lastContent = content
			}
		} else {
			lastContent, lastThinking = content, thinking
		}
		lastMsg = msgID
		chat, _ := root["chat"].(map[string]any)
		if jsonNumber(chat["chat_status"]) == 2 {
			if stream {
				if e = emitter.finish("stop"); e != nil {
					return e
				}
			} else {
				result := completionJSON(model, chatID, lastContent, lastThinking)
				_, e = sink.Write(result)
			}
			return e
		}
	}
	return fmt.Errorf("%w: MiniMax generation exceeded bounded polling window", ErrTruncated)
}
