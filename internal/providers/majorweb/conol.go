package majorweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var conolSessionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (c *Client) doConol(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" {
		return nil, &requestError{"Conol requires a bounded chat request and explicit model"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://conol.ai")
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	headers := http.Header{}
	for k, v := range map[string]string{"Cookie": cookieHeader("__Secure-better-auth.session_token", cred.cookie), "Origin": base, "Referer": base + "/home", "Accept": "application/json", "Content-Type": "application/json", "Accept-Language": "en-US,en;q=0.9", "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"} {
		headers.Set(k, v)
	}
	post := func(path string, payload any, read bool) (map[string]any, error) {
		encoded, _ := json.Marshal(payload)
		resp, err := request(callCtx, c.http, http.MethodPost, base+path, encoded, headers)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &HTTPError{Status: resp.StatusCode, What: "Conol request failed"}
		}
		if !read {
			return nil, nil
		}
		data, err := readBounded(resp.Body, 1<<20)
		if err != nil {
			return nil, err
		}
		var value map[string]any
		if json.Unmarshal(data, &value) != nil || value == nil {
			return nil, errors.New("Conol invalid response")
		}
		return value, nil
	}
	created, err := post("/api/sessions", map[string]any{"source": map[string]any{"type": "home"}, "messages": []any{}, "timezone": "UTC"}, true)
	if err != nil {
		return nil, err
	}
	session, _ := created["sessionId"].(string)
	if !conolSessionID.MatchString(session) {
		return nil, errors.New("Conol invalid session identifier")
	}
	path := "/api/sessions/" + url.PathEscape(session)
	headers.Set("Referer", base+"/home?chat_session="+url.QueryEscape(session))
	for _, payload := range []any{map[string]any{"modelPreset": "pro", "hasImageHistory": false}, map[string]any{"agentModel": model, "agentEffort": nil}} {
		reply, err := post(path+"/model", payload, true)
		if err != nil {
			return nil, err
		}
		if reply["ok"] != true {
			return nil, errors.New("Conol model selection was not acknowledged")
		}
	}
	_, err = post(path+"/messages", map[string]any{"messages": []any{map[string]any{"type": "text", "content": input.Prompt}}, "timezone": "UTC"}, false)
	if err != nil {
		return nil, err
	}
	headers.Set("Accept", "text/event-stream, application/x-ndjson")
	resp, err := request(callCtx, c.http, http.MethodGet, base+path+"/messages?logDeltas=1", nil, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, What: "Conol message stream failed"}
	}
	text, err := collectConol(resp.Body)
	if err != nil {
		return nil, err
	}
	cancel()
	resp.Body.Close()
	id := "chatcmpl-conol-" + session
	now := time.Now().Unix()
	var data []byte
	if stream {
		data = chatChunk(id, model, now, map[string]any{"role": "assistant", "content": text}, nil)
		data = append(data, chatChunk(id, model, now, map[string]any{}, "stop")...)
		data = append(data, []byte("data: [DONE]\n\n")...)
	} else {
		data = chatCompletion(id, model, text, "", now)
	}
	result := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
	result.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		result.Header.Set("Content-Type", "text/event-stream")
	} else {
		result.Header.Set("Content-Type", "application/json")
	}
	return result, nil
}

func collectConol(body io.Reader) (string, error) {
	reader := bufio.NewReaderSize(body, 32768)
	var total int
	var final, preview string
	var deltas strings.Builder
	for {
		line, err := readBoundedLine(reader, 1<<20)
		total += len(line)
		if total > 16<<20 {
			return "", errors.New("Conol stream exceeds limit")
		}
		if err != nil && (err != io.EOF || len(line) == 0) {
			return "", ErrTruncated
		}
		raw := strings.TrimSpace(string(line))
		if raw == "" || strings.HasPrefix(raw, ":") || strings.HasPrefix(raw, "event:") {
			continue
		}
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "data:"))
		raw = strings.TrimPrefix(raw, "message\t")
		var event map[string]any
		if raw == "[DONE]" {
			event = map[string]any{"type": "done"}
		} else if json.Unmarshal([]byte(raw), &event) != nil {
			continue
		}
		typ, _ := event["type"].(string)
		if typ == "error" || event["error"] != nil {
			return "", errors.New("Conol stream failed")
		}
		if typ == "done" {
			if final != "" {
				return final, nil
			}
			if preview != "" {
				return preview, nil
			}
			if deltas.Len() > 0 {
				return deltas.String(), nil
			}
			return "", errors.New("Conol completed without answer")
		}
		if stages, ok := event["stages"].([]any); ok {
			for _, stage := range stages {
				if obj, ok := stage.(map[string]any); ok {
					for _, field := range []string{"logs", "preview"} {
						entries, _ := obj[field].([]any)
						for _, entry := range entries {
							msg, _ := entry.(map[string]any)
							if msg["role"] == "assistant" {
								text := conolText(msg["content"], 0)
								if text != "" {
									if field == "logs" {
										final = text
									} else {
										preview = text
									}
								}
							}
						}
					}
				}
			}
		}
		switch typ {
		case "assistant":
			if text := conolFirstText(event, []string{"content", "message", "text"}); text != "" {
				final = text
			}
		case "stream_event":
			deltas.WriteString(conolFirstText(event, []string{"delta", "content", "text"}))
		}
	}
}

func conolFirstText(event map[string]any, keys []string) string {
	for _, key := range keys {
		if event[key] != nil {
			return conolText(event[key], 0)
		}
	}
	return ""
}
func conolText(value any, depth int) string {
	if depth > 16 {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			part := conolText(item, depth+1)
			if part != "" {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(part)
			}
		}
		return b.String()
	case map[string]any:
		if typ, _ := v["type"].(string); typ == "image" || typ == "image_url" || typ == "input_image" {
			return ""
		}
		for _, key := range []string{"text", "content", "output", "result"} {
			if text := conolText(v[key], depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}
