package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gobwas/ws"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) doUC(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" || len(model) > 128 {
		return nil, &requestError{"UC requires bounded text chat and a product persona model shortname"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !huggingID.MatchString(cred.userID) || !huggingID.MatchString(cred.sessionID) || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	hasClient := false
	for _, cookie := range (&http.Request{Header: http.Header{"Cookie": {cred.cookie}}}).Cookies() {
		if cookie.Name == "__client" && cookie.Value != "" {
			hasClient = true
		}
	}
	if !hasClient {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://internal-6.pubyar.com")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	h := http.Header{"Origin": {"https://uncensored.com"}, "Referer": {"https://uncensored.com/"}, "Cookie": {cred.cookie}, "Content-Type": {"application/x-www-form-urlencoded"}}
	resp, err := request(ctx, c.http, http.MethodPost, "https://clerk.uncensored.com/v1/client/sessions/"+cred.sessionID+"/tokens?_clerk_js_version=5.127.1", nil, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &HTTPError{Status: resp.StatusCode, What: "UC session token mint failed"}
	}
	raw, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	var auth struct {
		JWT   string `json:"jwt"`
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &auth) != nil {
		return nil, errors.New("UC invalid token response")
	}
	if auth.JWT == "" {
		auth.JWT = auth.Token
	}
	if auth.JWT == "" || len(auth.JWT) > 16384 || strings.ContainsAny(auth.JWT, "\r\n") {
		return nil, errors.New("UC missing session token")
	}
	u, _ := url.Parse(base)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/" + cred.userID
	u.RawQuery = url.Values{"token": {auth.JWT}, "_t": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}.Encode()
	dialer := ws.Dialer{Timeout: 15 * time.Second, Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": {"https://uncensored.com"}})}
	conn, prefetch, _, err := dialer.Dial(ctx, u.String())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("UC WebSocket connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	var reader io.Reader = conn
	if prefetch != nil {
		reader = io.MultiReader(prefetch, conn)
	}
	rw := &gatewayReadWriter{Reader: reader, Writer: conn}
	payload := map[string]any{"message_id": randomUUID(), "client_request_id": randomUUID(), "thread_id": randomUUID(), "app_version": "1.0.0-web", "model": model, "text": input.Prompt, "chat_history": []any{}, "chat_mode": "chat", "user_identifier": cred.userID, "no_media_in_chat": true, "media_blob_name": "", "media_content_type": "", "adapty_profile_id": nil}
	for _, k := range []string{"chat_history_truncated", "use_memory", "web_search_enabled", "perplexity_search_enabled", "is_smartify", "is_refresh", "is_suggested_input", "followups_enabled", "free_tier_model_selected"} {
		payload[k] = false
	}
	if writeGatewayJSON(conn, payload) != nil {
		return nil, errors.New("UC persona send failed")
	}
	content, reasoning, err := readUCTurn(rw)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var result []byte
	if stream {
		result = chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil)
		if reasoning != "" {
			result = append(result, chatChunk(id, model, created, map[string]any{"reasoning_content": reasoning}, nil)...)
		}
		result = append(result, chatChunk(id, model, created, map[string]any{"content": content}, nil)...)
		result = append(result, chatChunk(id, model, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, model, content, reasoning, created)
	}
	out := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}
	out.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		out.Header.Set("Content-Type", "text/event-stream")
	} else {
		out.Header.Set("Content-Type", "application/json")
	}
	return out, nil
}

func readUCTurn(rw io.ReadWriter) (string, string, error) {
	var text, reasoning strings.Builder
	total := 0
	for {
		raw, err := readBoundedServerText(rw, 1<<20)
		if err != nil {
			return "", "", ErrTruncated
		}
		total += len(raw)
		if total > 16<<20 {
			return "", "", errors.New("UC response exceeds limit")
		}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var f struct {
				Type string  `json:"type"`
				Code string  `json:"code"`
				Kind string  `json:"message_type"`
				Text string  `json:"text"`
				Raw  *string `json:"raw_text"`
				End  bool    `json:"end_of_stream"`
			}
			if json.Unmarshal(line, &f) != nil {
				return "", "", errors.New("UC invalid persona frame")
			}
			if f.Type == "error" || f.Kind == "generation_failed" {
				return "", "", errors.New("UC upstream error")
			}
			switch f.Code {
			case "message_limit_exceeded", "paywall_exceeded", "rate_limit_exceeded", "unauthorized", "forbidden":
				return "", "", errors.New("UC upstream rejected turn")
			}
			if f.Kind == "intermediary_message" {
				reasoning.WriteString(f.Text)
			}
			if f.Kind == "text" {
				if f.End {
					answer := text.String()
					if f.Raw != nil && *f.Raw != "" {
						answer = *f.Raw
					}
					if strings.TrimSpace(answer) == "" {
						return "", "", errors.New("UC completed without text")
					}
					return answer, reasoning.String(), nil
				}
				text.WriteString(f.Text)
			}
		}
	}
}
