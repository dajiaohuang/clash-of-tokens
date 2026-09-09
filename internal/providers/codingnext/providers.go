package codingnext

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var freebuffAgents = map[string]string{
	"deepseek/deepseek-v4-flash":      "base2-free-deepseek-flash",
	"deepseek/deepseek-v4-pro":        "base2-free-deepseek",
	"openai/gpt-5.6-luna":             "base2-free-luna",
	"minimax/minimax-m3":              "base2-free-minimax-m3",
	"mimo/mimo-v2.5":                  "base2-free-mimo",
	"z-ai/glm-5.2":                    "base2-free-glm",
	"crof/kimi-k3-eco":                "base2-free-kimi-k3-eco",
	"anthropic/claude-fable-5":        "base2-free-fable",
	"meta/muse-spark-1.2-contributor": "base2-free-muse-spark",
}

func (c *Client) doFreebuff(ctx context.Context, token, model string, stream bool, request openAIRequest) (*http.Response, error) {
	requestedModel := strings.TrimPrefix(model, "freebuff/")
	agentID := freebuffAgents[requestedModel]
	if agentID == "" {
		agentID = "base2-free"
	}
	sessionURL, err := endpoint(c.source, "https://www.codebuff.com/api/v1", "/freebuff/session")
	if err != nil {
		return nil, err
	}
	common := http.Header{}
	common.Set("User-Agent", "codebuff/0.1.0 (darwin-arm64)")
	sessionHeaders := cloneHeaders(common)
	sessionHeaders.Set("x-freebuff-model", requestedModel)
	sessionResponse, err := postJSON(ctx, c.http, sessionURL, token, []byte(`{}`), sessionHeaders)
	if err != nil {
		return nil, err
	}
	instance, err := decodeFreebuffSession(sessionResponse)
	if err != nil {
		return nil, err
	}

	runURL, err := endpoint(c.source, "https://www.codebuff.com/api/v1", "/agent-runs")
	if err != nil {
		return nil, err
	}
	runPayload, _ := json.Marshal(map[string]any{"action": "START", "agentId": agentID})
	runResponse, runErr := postJSON(ctx, c.http, runURL, token, runPayload, common)
	runID := ""
	if runErr == nil {
		runID = decodeFreebuffRunID(runResponse)
	}
	if runResponse != nil && runErr == nil {
		// A failed START is intentionally non-fatal in the pinned reference; the
		// completion endpoint can still service the request without a run ID.
		_ = runResponse.Body.Close()
	}

	payload := clonePayload(request.payload)
	payload["model"] = requestedModel
	payload["stream"] = stream
	payload["messages"] = withBuffyPrompt(request.payload["messages"])
	metadata := map[string]any{}
	metadata["run_id"] = runID
	metadata["cost_mode"] = "free"
	metadata["client_id"] = freebuffClientID()
	metadata["freebuff_instance_id"] = instance
	if existing, ok := request.payload["codebuff_metadata"].(map[string]any); ok && existing != nil {
		// Match the pinned executor: caller metadata is carried through after
		// the defaults are populated, including provider-specific extensions.
		for key, value := range existing {
			if _, reserved := metadata[key]; !reserved {
				metadata[key] = value
			}
		}
	}
	payload["codebuff_metadata"] = metadata
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, err
	}
	completionURL, err := endpoint(c.source, "https://www.codebuff.com/api/v1", "/chat/completions")
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("User-Agent", "ai-sdk/openai-compatible/1.0.25/codebuff")
	headers.Set("Accept", "application/json, text/event-stream")
	headers.Set("x-freebuff-instance-id", instance)
	headers.Set("x-codebuff-agent-id", agentID)
	if runID != "" {
		headers.Set("x-codebuff-run-id", runID)
	}
	response, err := postJSON(ctx, c.http, completionURL, token, encoded, headers)
	if err != nil {
		return nil, err
	}
	if runID != "" {
		response.Body = &freebuffRunBody{ReadCloser: response.Body, finish: func() { c.finishFreebuff(runURL, token, runID) }}
	}
	return response, nil
}

type freebuffRunBody struct {
	io.ReadCloser
	finish func()
}

// Only a validated completion can report a completed product run.
type completedRunBody struct {
	io.ReadCloser
	once   sync.Once
	finish func()
}

func (b *completedRunBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(func() { go b.finish() })
	}
	return n, err
}

func decodeFreebuffSession(response *http.Response) (string, error) {
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", upstreamStatus(response, "Freebuff session failed")
	}
	body, err := readLimited(response.Body, 1<<20)
	if err != nil {
		return "", err
	}
	var value struct {
		InstanceID string `json:"instanceId"`
	}
	if err := json.Unmarshal(body, &value); err != nil || strings.TrimSpace(value.InstanceID) == "" {
		return "", fmt.Errorf("coding next adapter: Freebuff session response has no instanceId")
	}
	return value.InstanceID, nil
}

func decodeFreebuffRunID(response *http.Response) string {
	if response == nil || response.Body == nil {
		return ""
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ""
	}
	body, err := readLimited(response.Body, 1<<20)
	if err != nil {
		return ""
	}
	var value struct {
		RunID string `json:"runId"`
	}
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	return value.RunID
}

func (c *Client) finishFreebuff(target, token, runID string) {
	payload, _ := json.Marshal(map[string]any{
		"action": "FINISH", "runId": runID, "status": "completed",
		"totalSteps": 1, "directCredits": 0, "totalCredits": 0,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := postJSON(ctx, c.http, target, token, payload, http.Header{"User-Agent": []string{"codebuff/0.1.0 (darwin-arm64)"}})
	if err == nil && response != nil && response.Body != nil {
		response.Body.Close()
	}
}

func upstreamStatus(response *http.Response, prefix string) error {
	return &HTTPError{Status: response.StatusCode, What: prefix}
}

func withBuffyPrompt(raw any) []any {
	messages, _ := raw.([]any)
	out := make([]any, 0, len(messages)+1)
	for _, value := range messages {
		out = append(out, value)
	}
	hasBuffy := false
	if len(messages) > 0 {
		if first, ok := messages[0].(map[string]any); ok && first["role"] == "system" {
			if content, ok := first["content"].(string); ok && strings.HasPrefix(strings.TrimSpace(content), "You are Buffy") {
				hasBuffy = true
			}
		}
	}
	if !hasBuffy {
		out = append([]any{map[string]any{"role": "system", "content": "You are Buffy, the strategic coding assistant."}}, out...)
	}
	return out
}

func freebuffClientID() string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	var out [13]byte
	var random [13]byte
	if _, err := rand.Read(random[:]); err != nil {
		return strings.Repeat("0", len(out))
	}
	for i := range out {
		out[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(out[:])
}

func (c *Client) doCodeBuddy(ctx context.Context, token, model string, stream bool, request openAIRequest) (*http.Response, error) {
	payload := clonePayload(request.payload)
	payload["model"] = model
	// CodeBuddy currently rejects non-stream requests. The caller's requested
	// delivery mode is restored by convertOpenAIResponse below.
	payload["stream"] = true
	reasoningOptIn(payload)
	encoded, err := encodePayload(payload)
	if err != nil {
		return nil, err
	}
	target, err := endpoint(c.source, "https://copilot.tencent.com", "/v2/chat/completions")
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("User-Agent", "CLI/2.108.1 CodeBuddy/2.108.1")
	headers.Set("X-Product", "SaaS")
	headers.Set("X-IDE-Type", "CLI")
	headers.Set("X-IDE-Name", "CLI")
	headers.Set("x-requested-with", "XMLHttpRequest")
	headers.Set("x-codebuddy-request", "1")
	headers.Set("Accept", "application/json, text/event-stream")
	return postJSON(ctx, c.http, target, token, encoded, headers)
}
