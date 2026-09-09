package majorweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// doT3 ports the executable /api/chat path in the pinned reference. It does
// not claim to discover deployment-specific TanStack server function hashes.
func (c *Client) doT3(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" || len(model) > 128 {
		return nil, &requestError{"T3 requires bounded text chat and an exact product model ID"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	cookie := cred.cookie
	if cookie == "" {
		cookie = cred.value
	}
	if len(cookie) > 16384 || strings.ContainsAny(cookie, "\r\n") {
		return nil, ErrCredential
	}
	valid := false
	for _, v := range (&http.Request{Header: http.Header{"Cookie": {cookie}}}).Cookies() {
		if v.Name == "convex-session-id" && v.Value != "" {
			valid = true
		}
	}
	if !valid {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://t3.chat")
	if err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "messages": []map[string]string{{"role": "user", "content": in.Prompt}}, "stream": true})
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/x-ndjson, text/event-stream, application/json"}, "Origin": {base}, "Referer": {base + "/"}, "Cookie": {cookie}}
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/api/chat"), payload, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, What: "T3 chat endpoint failed"}
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	var answer string
	if strings.Contains(ct, "application/json") && !strings.Contains(ct, "ndjson") {
		raw, e := readBounded(resp.Body, 4<<20)
		if e != nil {
			return nil, e
		}
		var f map[string]any
		if json.Unmarshal(raw, &f) != nil {
			return nil, errors.New("T3 invalid JSON response")
		}
		answer, _, err = t3Text(f)
	} else if strings.Contains(ct, "ndjson") || strings.Contains(ct, "text/event-stream") {
		answer, err = readT3(resp.Body)
	} else {
		return nil, errors.New("T3 unsupported response framing; deployment-specific server functions are not implemented")
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(answer) == "" {
		return nil, errors.New("T3 empty answer")
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

func t3Text(f map[string]any) (string, bool, error) {
	if e, ok := f["error"]; ok && e != nil && e != false && e != "" {
		return "", false, errors.New("T3 upstream error")
	}
	done := f["type"] == "done" || f["done"] == true || f["status"] == "complete" || f["finish_reason"] == "stop"
	for _, k := range []string{"text", "delta", "content"} {
		if s, ok := f[k].(string); ok {
			return s, done, nil
		}
	}
	if p, ok := f["p"].(map[string]any); ok {
		keys, kok := p["k"].([]any)
		vals, vok := p["v"].([]any)
		if !kok || !vok || len(keys) != len(vals) {
			return "", false, errors.New("T3 invalid typed object")
		}
		for i, k := range keys {
			if k == "content" || k == "text" || k == "delta" {
				if s, ok := vals[i].(string); ok {
					return s, done, nil
				}
				if v, ok := vals[i].(map[string]any); ok && v["t"] == float64(2) {
					if s, ok := v["s"].(string); ok {
						return s, done, nil
					}
				}
			}
		}
	}
	if f["t"] == float64(2) {
		if s, ok := f["s"].(string); ok {
			return s, done, nil
		}
	}
	if m, ok := f["message"].(map[string]any); ok {
		if s, ok := m["content"].(string); ok {
			return s, done, nil
		}
	}
	return "", done, nil
}

func readT3(r io.Reader) (string, error) {
	reader := bufio.NewReader(r)
	var b strings.Builder
	total := 0
	for {
		line, err := readBoundedLine(reader, 1<<20)
		if err != nil && err != io.EOF {
			return "", err
		}
		total += len(line)
		if total > 8<<20 {
			return "", errors.New("T3 response exceeds limit")
		}
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimSpace(line[5:])
		}
		if string(line) == "[DONE]" {
			return b.String(), nil
		}
		if len(line) > 0 && !bytes.HasPrefix(line, []byte(":")) && !bytes.HasPrefix(line, []byte("event:")) {
			var f map[string]any
			if json.Unmarshal(line, &f) != nil {
				return "", errors.New("T3 invalid stream record")
			}
			text, done, e := t3Text(f)
			if e != nil {
				return "", e
			}
			b.WriteString(text)
			if done {
				return b.String(), nil
			}
		}
		if err == io.EOF {
			return "", ErrTruncated
		}
	}
}
