package majorweb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// These reference protocols carry text records until a clean HTTP EOF, with
// no application-level terminal marker. Network truncation remains an error.
func textLineStream(source io.ReadCloser, id, model string, parse func(string) (string, error)) io.ReadCloser {
	reader := bufio.NewReaderSize(source, 32768)
	started, ended, sourceEOF, seen := false, false, false, false
	total := 0
	created := time.Now().Unix()
	return &transformBody{closeFn: source.Close, next: func() ([]byte, error) {
		if !started {
			started = true
			return chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil), nil
		}
		if ended {
			return nil, io.EOF
		}
		for {
			if sourceEOF {
				ended = true
				source.Close()
				if !seen {
					return nil, errors.New("web source completed without text")
				}
				return append(chatChunk(id, model, created, map[string]any{}, "stop"), []byte("data: [DONE]\n\n")...), nil
			}
			line, err := readBoundedLine(reader, 1<<20)
			total += len(line)
			if total > 16<<20 {
				return nil, errors.New("web source response exceeds limit")
			}
			if err != nil && err != io.EOF {
				return nil, err
			}
			if err == io.EOF {
				sourceEOF = true
			}
			if len(line) == 0 {
				continue
			}
			text, err := parse(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r"))
			if err != nil {
				return nil, err
			}
			if text != "" {
				seen = true
				return chatChunk(id, model, created, map[string]any{"content": text}, nil), nil
			}
		}
	}}
}

func (c *Client) doPiReka(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials, isPi bool) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 {
		return nil, &requestError{"Pi/Reka require a bounded text chat request"}
	}
	if isPi {
		if model != "pi" && model != "default" {
			return nil, &requestError{"Pi supports only pi/default"}
		}
	} else if model != "reka-core" && model != "default" {
		return nil, &requestError{"Reka reference supports only reka-core/default"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	fallback := "https://space.reka.ai"
	if isPi {
		fallback = "https://pi.ai"
	}
	base, err := baseURL(c.source, fallback)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	for k, v := range map[string]string{"Accept": "text/event-stream", "Content-Type": "application/json", "Origin": base, "Referer": base + "/", "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"} {
		headers.Set(k, v)
	}
	var payload map[string]any
	hc := c.http
	if isPi {
		if len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") || !strings.Contains(cred.cookie, "=") {
			return nil, ErrCredential
		}
		jar, _ := cookiejar.New(nil)
		u, _ := url.Parse(base)
		dummy := &http.Request{Header: http.Header{"Cookie": {cred.cookie}}}
		cookies := dummy.Cookies()
		if len(cookies) == 0 {
			return nil, ErrCredential
		}
		jar.SetCookies(u, cookies)
		clone := *c.http
		clone.Jar = jar
		hc = &clone
		headers.Set("X-Api-Version", "3")
		headers.Set("Accept", "application/json")
		resp, err := request(ctx, hc, http.MethodPost, endpoint(base, "/api/chat/start"), []byte("{}"), headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return resp, nil
		}
		data, err := readBounded(resp.Body, 1<<20)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var value struct {
			Conversations []struct {
				SID string `json:"sid"`
			} `json:"conversations"`
		}
		if json.Unmarshal(data, &value) != nil || len(value.Conversations) == 0 || value.Conversations[0].SID == "" {
			return nil, errors.New("Pi did not return a conversation")
		}
		headers.Del("X-Api-Version")
		headers.Set("Accept", "text/event-stream")
		payload = map[string]any{"text": input.Prompt, "conversation": value.Conversations[0].SID, "mode": "BASE"}
	} else {
		if cred.value == "" || len(cred.value) > 16384 || strings.ContainsAny(cred.value, "\r\n") {
			return nil, ErrCredential
		}
		headers.Set("Authorization", "Bearer "+cred.value)
		payload = map[string]any{"conversation_history": []any{map[string]any{"type": "human", "text": input.Prompt}}, "stream": true, "use_search_engine": false, "use_code_interpreter": false, "model_name": "reka-core", "random_seed": time.Now().UnixMilli()}
	}
	data, _ := json.Marshal(payload)
	resp, err := request(ctx, hc, http.MethodPost, endpoint(base, "/api/chat"), data, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	previous := ""
	id := randomID("chatcmpl-")
	converted := textLineStream(resp.Body, id, model, func(line string) (string, error) {
		if !strings.HasPrefix(line, "data:") {
			return "", nil
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" {
			return "", nil
		}
		var value map[string]any
		if json.Unmarshal([]byte(raw), &value) != nil || value == nil {
			return "", errors.New("Pi/Reka invalid text record")
		}
		if value["error"] != nil {
			return "", errors.New("Pi/Reka upstream error")
		}
		text, ok := value["text"].(string)
		if !ok {
			return "", nil
		}
		if isPi {
			return text, nil
		}
		if !strings.HasPrefix(text, previous) {
			return "", errors.New("Reka revised already emitted text")
		}
		delta := text[len(previous):]
		previous = text
		return delta, nil
	})
	return responseFromStream(resp, converted, stream, id, model)
}
