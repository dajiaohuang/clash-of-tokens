package majorweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var huggingID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// HuggingChat uses its product conversation endpoints, not an inference API.
func (c *Client) doHuggingChat(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" || model == "default" {
		return nil, &requestError{"HuggingChat requires bounded text chat and an explicit product model ID"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if len(cred.cookie) > 16384 || !strings.Contains(cred.cookie, "=") || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://huggingface.co")
	if err != nil {
		return nil, err
	}
	h := http.Header{"Cookie": {cred.cookie}, "Content-Type": {"application/json"}, "Accept": {"application/json"}, "Origin": {base}, "Referer": {base + "/chat/"}}
	fetch := func(method, path string, payload []byte) (map[string]json.RawMessage, error) {
		r, e := request(ctx, c.http, method, endpoint(base, path), payload, h)
		if e != nil {
			return nil, e
		}
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			return nil, &HTTPError{Status: r.StatusCode, What: "HuggingChat conversation request failed"}
		}
		b, e := readBounded(r.Body, 1<<20)
		if e != nil {
			return nil, e
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(b, &obj) != nil || obj == nil {
			return nil, errors.New("HuggingChat invalid conversation response")
		}
		return obj, nil
	}
	payload, _ := json.Marshal(map[string]string{"model": model})
	obj, err := fetch(http.MethodPost, "/chat/conversation", payload)
	if err != nil {
		return nil, err
	}
	var cid string
	if json.Unmarshal(obj["conversationId"], &cid) != nil || !huggingID.MatchString(cid) {
		return nil, errors.New("HuggingChat invalid conversation ID")
	}
	obj, err = fetch(http.MethodGet, "/chat/api/v2/conversations/"+cid, nil)
	if err != nil {
		return nil, err
	}
	var conversation struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if json.Unmarshal(obj["json"], &conversation) != nil {
		return nil, errors.New("HuggingChat invalid message response")
	}
	var messageID any
	if len(conversation.Messages) > 0 {
		v := conversation.Messages[len(conversation.Messages)-1].ID
		if !huggingID.MatchString(v) {
			return nil, errors.New("HuggingChat invalid message ID")
		}
		messageID = v
	}
	settings, _ := json.Marshal(map[string]any{"inputs": input.Prompt, "id": messageID, "is_retry": false, "is_continue": false, "web_search": false, "tools": []any{}})
	var buffer bytes.Buffer
	mw := multipart.NewWriter(&buffer)
	part, err := mw.CreateFormField("data")
	if err != nil {
		return nil, err
	}
	part.Write(settings)
	mw.Close()
	h.Set("Content-Type", mw.FormDataContentType())
	h.Set("Accept", "*/*")
	h.Set("Referer", base+"/chat/conversation/"+cid)
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/chat/conversation/"+cid), buffer.Bytes(), h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	id := randomID("chatcmpl-")
	converted := huggingChatStream(resp.Body, id, model)
	return responseFromStream(resp, converted, stream, id, model)
}

func huggingChatStream(source io.ReadCloser, id, model string) io.ReadCloser {
	reader := bufio.NewReaderSize(source, 32768)
	started, done, seen := false, false, false
	total, output := 0, 0
	created := time.Now().Unix()
	return &transformBody{closeFn: source.Close, next: func() ([]byte, error) {
		if !started {
			started = true
			return chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil), nil
		}
		if done {
			return nil, io.EOF
		}
		for {
			line, e := readBoundedLine(reader, 1<<20)
			total += len(line)
			if total > 16<<20 {
				return nil, errors.New("HuggingChat response exceeds limit")
			}
			if e != nil && e != io.EOF {
				return nil, e
			}
			if len(bytes.TrimSpace(line)) == 0 {
				if e == io.EOF {
					return nil, ErrTruncated
				}
				continue
			}
			var frame struct {
				Type  string          `json:"type"`
				Token string          `json:"token"`
				Error json.RawMessage `json:"error"`
			}
			if json.Unmarshal(line, &frame) != nil || frame.Type == "" {
				return nil, errors.New("HuggingChat invalid response record")
			}
			if frame.Type == "error" || (len(frame.Error) > 0 && string(frame.Error) != "null") {
				return nil, errors.New("HuggingChat upstream error")
			}
			switch frame.Type {
			case "stream":
				token := strings.ReplaceAll(frame.Token, "\x00", "")
				if token != "" {
					seen = true
					b := chatChunk(id, model, created, map[string]any{"content": token}, nil)
					output += len(b)
					if output > 32<<20 {
						return nil, errors.New("HuggingChat output exceeds limit")
					}
					return b, nil
				}
			case "finalAnswer":
				done = true
				source.Close()
				if !seen {
					return nil, errors.New("HuggingChat completed without text")
				}
				return append(chatChunk(id, model, created, map[string]any{}, "stop"), []byte("data: [DONE]\n\n")...), nil
			case "file":
				return nil, errors.New("HuggingChat returned unsupported media")
			}
			if e == io.EOF {
				return nil, ErrTruncated
			}
		}
	}}
}
