package majorweb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) doMeta(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || model != "meta-ai" || len(body) > 64<<10 {
		return nil, &requestError{"Meta AI requires model meta-ai and one user text message"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	for _, s := range []string{cred.cookie, cred.lsd, cred.dtsg} {
		if s == "" || len(s) > 16384 || strings.ContainsAny(s, "\r\n") {
			return nil, ErrCredential
		}
	}
	base, err := baseURL(c.source, "https://www.meta.ai")
	if err != nil {
		return nil, err
	}
	var entropy [4]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return nil, err
	}
	threadID := (uint64(time.Now().UnixMilli()) << 22) | (uint64(binary.BigEndian.Uint32(entropy[:])) & ((1 << 22) - 1))
	vars, _ := json.Marshal(map[string]any{"message": map[string]string{"sensitive_string_value": in.Prompt}, "externalConversationId": randomUUID(), "offlineThreadingId": strconv.FormatUint(threadID, 10), "suggestedPromptIndex": nil, "flashVideoRecapInput": map[string]any{"images": []any{}}, "flashPreviewInput": nil, "promptPrefix": nil, "entrypoint": "ABRA__CHAT__TEXT", "icebreaker_type": "TEXT", "__relay_internal__pv__AbraDebugDevOnlyrelayprovider": false, "__relay_internal__pv__WebPixelRatiorelayprovider": 1})
	payload := url.Values{"lsd": {cred.lsd}, "fb_dtsg": {cred.dtsg}, "fb_api_caller_class": {"RelayModern"}, "fb_api_req_friendly_name": {"useAbraSendMessageMutation"}, "variables": {string(vars)}, "server_timestamps": {"true"}, "doc_id": {"7783822248314888"}}
	h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Cookie": {cred.cookie}, "Origin": {base}, "Referer": {base + "/"}, "x-asbd-id": {"129477"}, "x-fb-friendly-name": {"useAbraSendMessageMutation"}, "x-fb-lsd": {cred.lsd}}
	r, err := request(ctx, c.http, http.MethodPost, base+"/api/graphql/", []byte(payload.Encode()), h)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, &HTTPError{Status: r.StatusCode, What: "Meta AI request failed"}
	}
	answer, err := readMeta(r.Body)
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
	r.Header = make(http.Header)
	r.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		r.Header.Set("Content-Type", "text/event-stream")
	} else {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Body = io.NopCloser(bytes.NewReader(out))
	r.ContentLength = int64(len(out))
	return r, nil
}

func readMeta(r io.Reader) (string, error) {
	d := newNDJSONDecoder(r)
	total := 0
	for {
		f, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrTruncated
		}
		raw, _ := json.Marshal(f)
		total += len(raw)
		if total > 8<<20 {
			return "", errors.New("Meta AI response exceeds limit")
		}
		if errs, ok := f["errors"].([]any); ok && len(errs) > 0 {
			return "", errors.New("Meta AI GraphQL error")
		}
		data, _ := f["data"].(map[string]any)
		node, _ := data["node"].(map[string]any)
		message, _ := node["bot_response_message"].(map[string]any)
		if message == nil {
			continue
		}
		state, _ := message["streaming_state"].(string)
		if state != "STREAMING" && state != "OVERALL_DONE" {
			return "", errors.New("Meta AI unrecognized generation state")
		}
		if card := message["imagine_card"]; card != nil {
			return "", errors.New("Meta AI media response is unsupported")
		}
		text, ok := message["snippet"].(string)
		if !ok {
			return "", errors.New("Meta AI missing answer text")
		}
		// Snapshot text may be revised while generating; only the authoritative
		// terminal snapshot is returned by this buffered adapter.
		if state == "OVERALL_DONE" {
			if strings.TrimSpace(text) == "" {
				return "", errors.New("Meta AI empty answer")
			}
			return text, nil
		}
	}
}
