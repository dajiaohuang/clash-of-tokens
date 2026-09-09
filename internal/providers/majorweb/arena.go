package majorweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Arena uses UUIDv7 identifiers for evaluation and message records.
func arenaID() string {
	id := strings.ReplaceAll(randomUUID(), "-", "")
	raw, _ := hex.DecodeString(id)
	now := uint64(time.Now().UnixMilli())
	for i := 5; i >= 0; i-- {
		raw[i] = byte(now)
		now >>= 8
	}
	raw[6] = (raw[6] & 15) | 0x70
	s := hex.EncodeToString(raw)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func (c *Client) doArena(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(model) != 36 || model[8] != '-' || model[13] != '-' || model[18] != '-' || model[23] != '-' {
		return nil, &requestError{"Arena requires an exact model UUID and one user text message"}
	}
	if v, e := hex.DecodeString(strings.ReplaceAll(model, "-", "")); e != nil || len(v) != 16 {
		return nil, ErrUnsupported
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if cred.cookie == "" || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") || len(cred.recaptcha) > 16384 {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://arena.ai")
	if err != nil {
		return nil, err
	}
	var captcha any
	if cred.recaptcha != "" {
		captcha = cred.recaptcha
	}
	payload, _ := json.Marshal(map[string]any{"id": arenaID(), "mode": "direct-battle", "modelAId": model, "userMessageId": arenaID(), "modelAMessageId": arenaID(), "userMessage": map[string]any{"content": in.Prompt, "experimental_attachments": []any{}, "metadata": map[string]any{}}, "modality": "chat", "recaptchaV3Token": captcha})
	r, err := request(ctx, c.http, http.MethodPost, base+"/nextjs-api/stream/create-evaluation", payload, http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}, "Cookie": {cred.cookie}, "Origin": {base}, "Referer": {base + "/"}})
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Arena request failed"}
	}
	answer, finish, err := readArena(r.Body)
	if err != nil {
		return nil, err
	}
	id, created := randomID("chatcmpl-"), time.Now().Unix()
	var out []byte
	if stream {
		out = chatChunk(id, model, created, map[string]any{"role": "assistant", "content": answer}, nil)
		out = append(out, chatChunk(id, model, created, map[string]any{}, finish)...)
		out = append(out, []byte("data: [DONE]\n\n")...)
	} else {
		out, _ = json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": finish}}})
	}
	r.Header = make(http.Header)
	r.Header.Set("X-COT-Delivery", "buffered")
	r.Header.Set("Content-Type", "application/json")
	if stream {
		r.Header.Set("Content-Type", "text/event-stream")
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	return r, nil
}

func readArena(r io.Reader) (string, string, error) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 1<<20)
	var answer strings.Builder
	total := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		total += len(line)
		if total > 8<<20 {
			return "", "", errors.New("Arena response exceeds limit")
		}
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		code, raw, ok := strings.Cut(line, ":")
		if !ok || !json.Valid([]byte(raw)) {
			return "", "", errors.New("invalid Arena frame")
		}
		// Direct battle requests select participant A. Never expose participant B.
		if len(code) == 2 && code[0] == 'b' {
			continue
		}
		if len(code) == 2 && code[0] == 'a' {
			code = code[1:]
			if code == "e" {
				return "", "", errors.New("Arena upstream error")
			}
		}
		switch code {
		case "0":
			var text string
			if json.Unmarshal([]byte(raw), &text) != nil {
				var v struct {
					Text  *string `json:"text"`
					Delta *string `json:"textDelta"`
				}
				if json.Unmarshal([]byte(raw), &v) != nil {
					return "", "", errors.New("invalid Arena text")
				}
				if v.Text != nil {
					text = *v.Text
				} else if v.Delta != nil {
					text = *v.Delta
				} else {
					return "", "", errors.New("missing Arena text")
				}
			}
			answer.WriteString(text)
		case "3":
			return "", "", errors.New("Arena upstream error")
		case "d":
			var v struct {
				Finish string `json:"finishReason"`
			}
			if json.Unmarshal([]byte(raw), &v) != nil || (v.Finish != "stop" && v.Finish != "length") {
				return "", "", errors.New("invalid Arena finish")
			}
			if answer.Len() == 0 {
				return "", "", errors.New("empty Arena response")
			}
			return answer.String(), v.Finish, nil
		case "e":
			var v map[string]any
			if json.Unmarshal([]byte(raw), &v) != nil || v["error"] != nil || v["finishReason"] == "error" {
				return "", "", errors.New("Arena step failed")
			}
		case "g", "2", "f": // Reasoning, heartbeat, step metadata.
		default:
			return "", "", errors.New("unsupported Arena frame")
		}
	}
	if s.Err() != nil {
		return "", "", errors.New("Arena response read failed")
	}
	return "", "", ErrTruncated
}
