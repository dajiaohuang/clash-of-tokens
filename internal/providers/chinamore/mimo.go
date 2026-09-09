package chinamore

import (
	"bytes"
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

const mimoChatPath = "/open-apis/bot/chat"

type mimoCredential struct {
	ServiceToken string `json:"service_token"`
	UserID       string `json:"user_id"`
	PHToken      string `json:"ph_token"`
}

func parseMimoCredential(raw string) (mimoCredential, error) {
	var c mimoCredential
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") || json.Unmarshal([]byte(raw), &c) != nil {
		return c, fmt.Errorf("%w: Mimo KeyEnv must be JSON with service_token, user_id, and ph_token", ErrCredential)
	}
	c.ServiceToken, c.UserID, c.PHToken = strings.TrimSpace(c.ServiceToken), strings.TrimSpace(c.UserID), strings.TrimSpace(c.PHToken)
	if c.ServiceToken == "" || c.UserID == "" || c.PHToken == "" {
		return mimoCredential{}, fmt.Errorf("%w: Mimo service_token, user_id, and ph_token are all required", ErrCredential)
	}
	return c, nil
}

func mimoModelID(model string) string {
	switch model {
	case "MiMo-V2.5-Pro":
		return "mimo-v2.5-pro"
	case "MiMo-V2.5":
		return "mimo-v2.5"
	case "MiMo-V2-Flash":
		return "mimo-v2-flash"
	default:
		return model
	}
}

func mimoHeaders(origin string, cred mimoCredential) http.Header {
	h := browserHeaders(origin)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("X-Timezone", "Asia/Shanghai")
	h.Set("Cookie", "serviceToken="+cred.ServiceToken+"; userId="+cred.UserID+"; xiaomichatbot_ph="+cred.PHToken)
	return h
}

func (c *Client) doMimo(ctx context.Context, raw string, req chatRequest, stream bool, _ *session) (*http.Response, error) {
	base, e := baseURL(c.source, defaultMimoBase)
	if e != nil {
		return nil, e
	}
	cred, e := parseMimoCredential(raw)
	if e != nil {
		return nil, e
	}
	conversationID := uuidNoHyphen()
	query := contentText(req.Messages[0].Content)
	saveBody, _ := json.Marshal(map[string]any{"conversationId": conversationID, "title": "新对话", "type": "chat"})
	saveCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	save, e := postJSON(saveCtx, c.http, base+"/open-apis/chat/conversation/save?xiaomichatbot_ph="+url.QueryEscape(cred.PHToken), saveBody, mimoHeaders(base, cred))
	if e != nil {
		cancel()
		return nil, e
	}
	if save.StatusCode < 200 || save.StatusCode >= 300 {
		cancel()
		return save, nil
	}
	var saveResult map[string]any
	if e = readLimitedJSON(save, 1<<20, &saveResult); e != nil {
		cancel()
		return nil, errors.New("china more web adapter: invalid Mimo save response")
	}
	cancel()
	if code := jsonNumber(saveResult["code"]); code != 0 {
		return nil, fmt.Errorf("china more web adapter: Mimo conversation save rejected (code %d)", code)
	}
	payload := map[string]any{
		"msgId": uuidNoHyphen(), "conversationId": conversationID, "query": query,
		"isEditedQuery": false,
		"modelConfig": map[string]any{
			"enableThinking": false, "webSearchStatus": "disabled",
			"model": mimoModelID(req.Model), "temperature": 0.8, "topP": 0.95,
		},
		"multiMedias": []any{},
	}
	b, _ := json.Marshal(payload)
	chatURL := base + mimoChatPath + "?xiaomichatbot_ph=" + url.QueryEscape(cred.PHToken)
	// Keep the caller context attached to the response body.  Canceling a
	// timeout context immediately after receiving headers would terminate the
	// SSE stream before its first complete event.
	upstream, e := postJSON(ctx, c.http, chatURL, b, mimoHeaders(base, cred))
	if e != nil {
		return nil, e
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		return upstream, nil
	}
	if upstream.Body == nil {
		return nil, errors.New("china more web adapter: Mimo response has no body")
	}
	if !stream {
		var out bytes.Buffer
		if e = convertMimo(ctx, upstream.Body, req.Model, conversationID, &out, false); e != nil {
			upstream.Body.Close()
			return nil, e
		}
		upstream.Body.Close()
		return bodyResponse(200, http.Header{"Content-Type": []string{"application/json"}}, out.Bytes()), nil
	}
	reader, writer := io.Pipe()
	go func() {
		e := convertMimo(ctx, upstream.Body, req.Model, conversationID, writer, true)
		upstream.Body.Close()
		if e != nil {
			_ = writer.CloseWithError(e)
		} else {
			_ = writer.Close()
		}
	}()
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}, nil
}
