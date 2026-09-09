package majorweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

func (c *Client) doHyperAgent(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	families := map[string]string{"fable-latest": "fable", "claude-fable-5": "fable", "opus-latest": "opus", "claude-opus-4-8": "opus", "sonnet-latest": "sonnet", "claude-sonnet-5": "sonnet"}
	family := families[model]
	if protocol != "chat" || len(body) > 1<<20 || family == "" {
		return nil, &requestError{"HyperAgent requires bounded text chat and an exact supported product wire model ID"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(cred.cookie, "=") || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://hyperagent.com")
	if err != nil {
		return nil, err
	}
	h := http.Header{"Cookie": {cred.cookie}, "Content-Type": {"application/json"}, "Accept": {"*/*"}, "Origin": {base}, "Referer": {base + "/"}, "User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"}}
	h.Set("X-Request-Id", randomUUID())
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/api/threads"), []byte("{}"), h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	raw, err := readBounded(resp.Body, 1<<20)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var value struct {
		ID          string `json:"id"`
		ThreadID    string `json:"threadId"`
		ThreadSnake string `json:"thread_id"`
		Thread      struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("HyperAgent invalid thread response")
	}
	tid := value.ID
	if tid == "" {
		tid = value.ThreadID
	}
	if tid == "" {
		tid = value.ThreadSnake
	}
	if tid == "" {
		tid = value.Thread.ID
	}
	if !huggingID.MatchString(tid) {
		return nil, errors.New("HyperAgent invalid thread ID")
	}
	h.Set("Referer", base+"/thread/"+tid)
	h.Set("X-Request-Id", randomUUID())
	data, _ := json.Marshal(map[string]any{"modelId": model, "defaultSubagentModel": family, "runtimeId": "claude-agents-sdk", "executionMode": "auto"})
	resp, err = request(ctx, c.http, http.MethodPatch, endpoint(base, "/api/threads/"+tid), data, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	_, err = readBounded(resp.Body, 1<<20)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"sessionId": nil, "unifiedStream": true, "searchMode": "exa", "content": input.Prompt, "enabledIntegrations": []any{}, "integrationMode": "open"}
	for _, k := range []string{"enablePersistentSandbox", "enableWebpage", "enableSlides", "tablesEnabled", "enableWebSearch", "enableBrowser", "enableImageGeneration", "enableVideoGeneration", "enableAudioGeneration", "enableTranscription", "enableAvatarVideo", "enableExaFindSimilar", "enableExaAnswer", "enableExaResearch", "enableExaWebsets", "enableGeoTools", "documentsEnabled", "enableThreadSearch", "solveCaptchasEnabled", "globalTablesEnabled"} {
		payload[k] = true
	}
	for _, k := range []string{"enableExecuteScript", "hyperAppsEnabled", "residentialProxyEnabled", "debug"} {
		payload[k] = false
	}
	data, _ = json.Marshal(payload)
	h.Set("X-Request-Id", randomUUID())
	h.Set("Accept", "text/event-stream")
	resp, err = request(ctx, c.http, http.MethodPost, endpoint(base, "/api/threads/"+tid+"/chat"), data, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	seen := false
	sourceBody := resp.Body
	converted := newProviderStreamBody(sourceBody, id, model, func(event, data string) ([]byte, bool, error) {
		if data == "[DONE]" {
			sourceBody.Close()
			if !seen {
				return nil, false, errors.New("HyperAgent completed without text")
			}
			return nil, true, nil
		}
		var frame struct {
			Type    string          `json:"type"`
			Content string          `json:"content"`
			ModelID string          `json:"modelId"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			return nil, false, errors.New("HyperAgent invalid event")
		}
		if frame.Type == "error" || frame.Type == "stream_error" || (len(frame.Error) > 0 && string(frame.Error) != "null") {
			return nil, false, errors.New("HyperAgent upstream error")
		}
		if frame.Type == "thread_runtime_latched" && frame.ModelID != "" && frame.ModelID != model {
			return nil, false, errors.New("HyperAgent selected a different product model")
		}
		if frame.Type == "done" {
			sourceBody.Close()
			if !seen {
				return nil, false, errors.New("HyperAgent completed without text")
			}
			return nil, true, nil
		}
		if frame.Type == "text" && frame.Content != "" {
			seen = true
			return chatChunk(id, model, created, map[string]any{"content": frame.Content}, nil), false, nil
		}
		return nil, false, nil
	})
	return responseFromStream(resp, converted, stream, id, model)
}
