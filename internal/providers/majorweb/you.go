package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *Client) doYou(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 || strings.TrimSpace(model) == "" || len(model) > 128 || model == "agent" || strings.HasPrefix(model, "dall-e") {
		return nil, &requestError{"You requires bounded text chat and an exact text model ID"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if cred.cookie == "" || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://you.com")
	if err != nil {
		return nil, err
	}
	mode := "custom"
	if model == "gpt-4o-mini" {
		mode = "default"
	}
	q := url.Values{"userFiles": {""}, "q": {in.Prompt}, "domain": {"youchat"}, "selectedChatMode": {mode}, "conversationTurnId": {randomUUID()}, "chatId": {randomUUID()}}
	if mode == "custom" {
		q.Set("selectedAiModel", strings.ReplaceAll(model, "-", "_"))
	}
	h := http.Header{"Accept": {"text/event-stream"}, "Referer": {base + "/api/streamingSearch"}, "Cookie": {cred.cookie}}
	resp, err := request(ctx, c.http, http.MethodGet, base+"/api/streamingSearch?"+q.Encode(), nil, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, What: "You chat request failed"}
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		return nil, errors.New("You expected SSE response")
	}
	answer, err := readYou(resp.Body)
	if err != nil {
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var out []byte
	if stream {
		out = chatChunk(id, model, created, map[string]any{"role": "assistant", "content": answer}, nil)
		out = append(out, chatChunk(id, model, created, map[string]any{}, "stop")...)
		out = append(out, []byte("data: [DONE]\n\n")...)
	} else {
		out = chatCompletion(id, model, answer, "", created)
	}
	resp.Header = make(http.Header)
	resp.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		resp.Header.Set("Content-Type", "text/event-stream")
	} else {
		resp.Header.Set("Content-Type", "application/json")
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	return resp, nil
}

func readYou(r io.Reader) (string, error) {
	d := newSSEDecoder(r)
	var b strings.Builder
	total := 0
	for {
		event, data, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		total += len(event) + len(data)
		if total > 8<<20 {
			return "", errors.New("You response exceeds limit")
		}
		if event == "error" {
			return "", errors.New("You upstream error")
		}
		if data == "[DONE]" {
			break
		}
		if event != "youChatToken" && event != "youChatUpdate" {
			continue
		}
		var f map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &f) != nil {
			return "", errors.New("You invalid text event")
		}
		key := "youChatToken"
		if event == "youChatUpdate" {
			key = "t"
		}
		if raw, ok := f[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				return "", errors.New("You invalid text payload")
			}
			if strings.HasPrefix(s, "#### You've hit your free quota") {
				return "", &HTTPError{Status: 429, What: "You model quota exceeded"}
			}
			b.WriteString(s)
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", errors.New("You empty answer")
	}
	// The pinned source terminates at clean HTTP EOF and defines no end event.
	// Transport errors are propagated by the SSE decoder instead of accepted.
	return b.String(), nil
}
