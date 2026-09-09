package chinaapps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// Wire contract: teng-lin/weread-omni at 3a7fee58995ef502f2044d93f11af7b76fdacd0e.
// This is book-scoped AI, not a general model endpoint. Project is the book ID.
func (c *Client) doWeRead(ctx context.Context, proto, model string, stream bool, body []byte, caller http.Header) (*http.Response, error) {
	unsupported := requestError("weread-ai: requires model web, source project as book ID, and one user text; history, sessions, tools and controls are unsupported")
	if proto != "chat" || model != "web" || strings.TrimSpace(c.source.Project) == "" || len(c.source.Project) > 256 || caller.Get("X-COT-Session") != "" || len(body) > 1<<20 {
		return nil, unsupported
	}
	var in struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if !utf8.Valid(body) || dec.Decode(&in) != nil || dec.Decode(new(any)) != io.EOF || len(in.Messages) != 1 || in.Messages[0].Role != "user" || strings.TrimSpace(in.Messages[0].Content) == "" {
		return nil, unsupported
	}
	var cred struct {
		VID         string `json:"vid"`
		AccessToken string `json:"access_token"`
	}
	dec = json.NewDecoder(strings.NewReader(os.Getenv(c.source.KeyEnv)))
	dec.DisallowUnknownFields()
	if dec.Decode(&cred) != nil || dec.Decode(new(any)) != io.EOF || cred.VID == "" || cred.AccessToken == "" || len(cred.AccessToken) > 64<<10 || len(cred.VID) > 256 || strings.ContainsAny(cred.VID+cred.AccessToken, "\r\n") {
		return nil, errors.New("weread-ai: source credential requires vid and access_token")
	}
	base := strings.TrimRight(c.source.BaseURL, "/")
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("weread-ai: invalid base URL")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, errors.New("weread-ai: HTTPS required")
	}
	query := in.Messages[0].Content
	payload := map[string]any{"accept_text_type": 1, "bookId": c.source.Project, "query": query, "scene": 1, "isPlugin": false, "intent": "", "weread_opt": map[string]string{"intent": "", "query_context": ""}, "chatid": "", "session_id": ""}
	answer := ""
	total := 0
	for poll := 0; poll < 80; poll++ {
		raw, _ := json.Marshal(payload)
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, base+"/ai/chatv2", bytes.NewReader(raw))
		if e != nil {
			return nil, e
		}
		req.GetBody = nil
		for k, v := range map[string]string{"vid": cred.VID, "accessToken": cred.AccessToken, "baseapi": "30", "appver": "2.1.2.10245900", "basever": "2.1.2.10245900", "osver": "11", "channelId": "900", "User-Agent": "WeRead/2.1.2 WRBrand/Onyx wr_eink Dalvik/2.1.0 (Linux; U; Android 11; BOOX Build/onyx)", "Content-Type": "application/json; charset=UTF-8"} {
			req.Header.Set(k, v)
		}
		resp, e := c.http.Do(req)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("weread-ai: upstream transport failed")
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp, nil
		}
		data, e := readJSON(resp.Body)
		if e != nil {
			return nil, e
		}
		total += len(data)
		if total > 16<<20 {
			return nil, errors.New("weread-ai: cumulative response limit exceeded")
		}
		var frame struct {
			ErrCode         int    `json:"errCode"`
			ChatID          string `json:"chatid"`
			SessionID       string `json:"session_id"`
			RequestInterval *int64 `json:"request_interval"`
			Result          *struct {
				Text    string `json:"text"`
				HasMore *int   `json:"has_more"`
			} `json:"result"`
			ExtraSections *struct {
				HasMore *bool `json:"has_more"`
			} `json:"extra_sections"`
		}
		if json.Unmarshal(data, &frame) != nil || frame.ErrCode != 0 {
			return nil, errors.New("weread-ai: upstream rejected or malformed poll")
		}
		if frame.ChatID != "" {
			if payload["chatid"] != "" && payload["chatid"] != frame.ChatID {
				return nil, errors.New("weread-ai: chat identity changed")
			}
			payload["chatid"] = frame.ChatID
		}
		if frame.SessionID != "" {
			if payload["session_id"] != "" && payload["session_id"] != frame.SessionID {
				return nil, errors.New("weread-ai: session identity changed")
			}
			payload["session_id"] = frame.SessionID
		}
		frameText := ""
		done := false
		if frame.Result != nil {
			frameText = frame.Result.Text
			if frameText != "" {
				answer = frameText
			}
			done = frame.Result.HasMore != nil && *frame.Result.HasMore == 0
		}
		if frame.ExtraSections != nil && frame.ExtraSections.HasMore != nil && !*frame.ExtraSections.HasMore && frameText != "" {
			done = true
		}
		if done {
			if answer == "" {
				return nil, errors.New("weread-ai: empty completed answer")
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			id, e := newID()
			if e != nil {
				return nil, e
			}
			return completion("chatcmpl-"+id, model, answer, stream), nil
		}
		if payload["chatid"] == "" || payload["session_id"] == "" {
			return nil, errors.New("weread-ai: continuation IDs missing")
		}
		delay := int64(200)
		if frame.RequestInterval != nil {
			delay = max(50, min(*frame.RequestInterval, 1500))
		}
		timer := time.NewTimer(time.Duration(delay) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, errors.New("weread-ai: generation did not complete within poll limit")
}
