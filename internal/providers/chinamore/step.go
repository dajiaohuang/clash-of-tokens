package chinamore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	stepRegisterPath = "/passport/proto.api.passport.v1.PassportService/RegisterDevice"
	stepCreatePath   = "/api/proto.chat.v1.ChatService/CreateChat"
	stepDeletePath   = "/api/proto.chat.v1.ChatService/DelChat"
	stepSendPath     = "/api/proto.chat.v1.ChatMessageService/SendMessageStream"
)

type stepAccess struct {
	DeviceID  string
	Token     string
	ExpiresAt time.Time
}

func parseStepCredential(raw string) (string, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	if raw == "" || strings.ContainsAny(raw, ",;\r\n") {
		return "", fmt.Errorf("%w: StepChat requires one explicit Oasis-Token", ErrCredential)
	}
	return raw, nil
}

func stepHeaders(origin string, access stepAccess) http.Header {
	h := browserHeaders(origin)
	h.Set("Accept", "*/*")
	h.Set("Connect-Protocol-Version", "1")
	h.Set("Oasis-Appid", "10200")
	h.Set("Oasis-Platform", "web")
	h.Set("Oasis-Webid", access.DeviceID)
	h.Set("Cookie", "Oasis-Token="+access.Token+"; Oasis-Webid="+access.DeviceID)
	return h
}

func (c *Client) stepAccessToken(ctx context.Context, refreshToken, base string) (stepAccess, error) {
	c.stepMu.Lock()
	if token, ok := c.step[refreshToken]; ok && time.Now().Before(token.ExpiresAt) {
		c.stepMu.Unlock()
		return token, nil
	}
	c.stepMu.Unlock()
	body := []byte(`{}`)
	h := browserHeaders(base)
	h.Set("Cookie", "Oasis-Token="+refreshToken)
	h.Set("Connect-Protocol-Version", "1")
	h.Set("Oasis-Appid", "10200")
	h.Set("Oasis-Platform", "web")
	h.Set("Oasis-Webid", uuidNoHyphen())
	registerCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	resp, e := postJSON(registerCtx, c.http, base+stepRegisterPath, body, h)
	if e != nil {
		cancel()
		return stepAccess{}, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.Body != nil {
			resp.Body.Close()
		}
		cancel()
		return stepAccess{}, &HTTPError{Status: resp.StatusCode, What: "StepChat token exchange failed"}
	}
	var root map[string]any
	if e = readLimitedJSON(resp, 1<<20, &root); e != nil {
		cancel()
		return stepAccess{}, errors.New("china more web adapter: invalid StepChat token response")
	}
	cancel()
	accessObject, _ := root["accessToken"].(map[string]any)
	refreshObject, _ := root["refreshToken"].(map[string]any)
	deviceObject, _ := root["device"].(map[string]any)
	accessRaw := rawMapString(accessObject, "raw")
	refreshRaw := rawMapString(refreshObject, "raw")
	deviceID := rawMapString(deviceObject, "deviceID")
	if deviceID == "" {
		deviceID = rawMapString(deviceObject, "deviceId")
	}
	if accessRaw == "" || refreshRaw == "" || deviceID == "" {
		return stepAccess{}, errors.New("china more web adapter: StepChat token response omitted access, refresh, or device fields")
	}
	access := stepAccess{DeviceID: deviceID, Token: accessRaw + "..." + refreshRaw, ExpiresAt: time.Now().Add(15 * time.Minute)}
	c.stepMu.Lock()
	c.step[refreshToken] = access
	c.stepMu.Unlock()
	return access, nil
}

func (c *Client) stepRequest(ctx context.Context, base, path string, access stepAccess, body []byte) (*http.Response, error) {
	h := stepHeaders(base, access)
	h.Set("Content-Type", "application/json")
	return postJSON(ctx, c.http, base+path, body, h)
}

func (c *Client) doStep(ctx context.Context, raw string, req chatRequest, stream bool, _ *session) (*http.Response, error) {
	if req.Model != "default" {
		return nil, fmt.Errorf("%w: StepChat only exposes product-default model selection", ErrUnsupported)
	}
	base, e := baseURL(c.source, defaultStepBase)
	if e != nil {
		return nil, e
	}
	refresh, e := parseStepCredential(raw)
	if e != nil {
		return nil, e
	}
	access, e := c.stepAccessToken(ctx, refresh, base)
	if e != nil {
		return nil, e
	}
	createBody := []byte(`{"chatName":"新会话"}`)
	createCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	create, e := c.stepRequest(createCtx, base, stepCreatePath, access, createBody)
	if e != nil {
		cancel()
		return nil, e
	}
	if create.StatusCode < 200 || create.StatusCode >= 300 {
		cancel()
		return create, nil
	}
	var createRoot map[string]any
	if e = readLimitedJSON(create, 1<<20, &createRoot); e != nil {
		cancel()
		return nil, errors.New("china more web adapter: invalid StepChat CreateChat response")
	}
	cancel()
	chatID := rawMapString(createRoot, "chatId")
	if chatID == "" {
		chatID = rawMapString(createRoot, "chat_id")
	}
	if chatID == "" {
		return nil, errors.New("china more web adapter: StepChat CreateChat response omitted chatId")
	}
	message := map[string]any{
		"chatId": chatID,
		"messageInfo": map[string]any{
			"text": "user:" + contentText(req.Messages[0].Content) + "\nassistant:",
		},
	}
	jsonBody, _ := json.Marshal(message)
	framed := connectFrame(jsonBody)
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, base+stepSendPath, bytes.NewReader(framed))
	if e != nil {
		return nil, errors.New("china more web adapter: invalid StepChat stream request")
	}
	request.ContentLength = int64(len(framed))
	request.GetBody = nil
	request.Header = stepHeaders(base, access)
	request.Header.Set("Content-Type", "application/connect+json")
	request.Header.Set("Accept", "application/connect+json")
	upstream, e := c.http.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("china more web adapter: StepChat transport failed")
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	if stream {
		reader, writer := io.Pipe()
		go func() {
			e := convertStep(ctx, upstream.Body, req.Model, chatID, writer, true)
			upstream.Body.Close()
			if e == nil {
				go c.removeStepConversation(context.Background(), base, access, chatID)
			}
			if e != nil {
				_ = writer.CloseWithError(e)
			} else {
				_ = writer.Close()
			}
		}()
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
	}
	var out bytes.Buffer
	e = convertStep(ctx, upstream.Body, req.Model, chatID, &out, false)
	upstream.Body.Close()
	if e != nil {
		return nil, e
	}
	go c.removeStepConversation(context.Background(), base, access, chatID)
	return bodyResponse(200, http.Header{"Content-Type": []string{"application/json"}}, out.Bytes()), nil
}

func connectFrame(body []byte) []byte {
	frame := make([]byte, 5+len(body))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

// removeStepConversation is best effort and is called only after a successful
// generation.  It keeps the temporary web conversation out of the user's
// visible StepChat history without attempting account or challenge actions.
func (c *Client) removeStepConversation(ctx context.Context, base string, access stepAccess, chatID string) {
	body, _ := json.Marshal(map[string]any{"chatIds": []string{chatID}})
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, e := c.stepRequest(requestCtx, base, stepDeletePath, access, body)
	if e == nil && resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
}
