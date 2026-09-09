package embedded

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var notionModelMap = map[string]string{
	"claude-sonnet-4.5": "anthropic-sonnet-alt",
	"gpt-5":             "openai-turbo",
	"claude-opus-4.1":   "anthropic-opus-4.1",
	"gemini-2.5-flash":  "vertex-gemini-2.5-flash",
	"gemini-2.5-pro":    "vertex-gemini-2.5-pro",
	"gpt-4.1":           "openai-gpt-4.1",
}

func (c *Client) doNotion(ctx context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" {
		return nil, errors.New("notion-web adapter supports only chat protocol")
	}
	cookie, err := c.credential()
	if err != nil {
		return nil, err
	}
	userEnv := strings.TrimSpace(c.source.AccountIDEnv)
	if userEnv == "" {
		return nil, errors.New("notion-web account_id_env is required for the user id")
	}
	userID := strings.TrimSpace(os.Getenv(userEnv))
	if userID == "" {
		return nil, errors.New("notion-web user id environment variable is not set")
	}
	spaceID := strings.TrimSpace(c.source.Project)
	if spaceID == "" {
		return nil, errors.New("notion-web project is required for the space id")
	}
	base, err := c.baseURL(notionDefaultBase)
	if err != nil {
		return nil, err
	}
	req, err := parseChatRequest(body)
	if err != nil {
		return nil, err
	}
	mapped := notionModel(model)
	threadType := "workflow"
	if strings.HasPrefix(mapped, "vertex-") {
		threadType = "markdown-chat"
	}
	threadID := newUUID()
	headers := c.notionHeaders(base, cookie, spaceID, userID)
	threadPayload, err := notionThreadPayload(spaceID, userID, threadID, threadType)
	if err != nil {
		return nil, errors.New("cannot encode notion thread request")
	}
	threadResp, err := c.request(ctx, http.MethodPost, base+"/api/v3/saveTransactionsFanout", threadPayload, headers)
	if err != nil {
		return nil, err
	}
	if threadResp.StatusCode < 200 || threadResp.StatusCode >= 300 {
		return threadResp, nil
	}
	_ = threadResp.Body.Close()
	inferencePayload, err := notionInferencePayload(req.Messages, spaceID, userID, threadID, mapped, threadType)
	if err != nil {
		return nil, errors.New("cannot encode notion inference request")
	}
	resp, err := c.request(ctx, http.MethodPost, base+"/api/v3/runInferenceTranscript", inferencePayload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	id := chatID()
	streamBody := c.newNotionStreamBody(resp.Body, id, model)
	return c.responseFromStream(resp, streamBody, stream, id, model)
}

func notionModel(model string) string {
	if mapped, ok := notionModelMap[strings.ToLower(model)]; ok {
		return mapped
	}
	if model == "" {
		return "anthropic-sonnet-alt"
	}
	return model
}

func (c *Client) notionHeaders(base, cookie, spaceID, userID string) http.Header {
	cookieHeader := cookie
	if !strings.Contains(cookieHeader, "=") {
		cookieHeader = "token_v2=" + cookieHeader
	}
	return http.Header{
		"Content-Type":                []string{"application/json"},
		"Accept":                      []string{"application/x-ndjson"},
		"Cookie":                      []string{cookieHeader},
		"x-notion-space-id":           []string{spaceID},
		"x-notion-active-user-header": []string{userID},
		"x-notion-client-version":     []string{notionClientVersion},
		"notion-audit-log-platform":   []string{"web"},
		"Origin":                      []string{base},
		"Referer":                     []string{base + "/"},
		"User-Agent":                  []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"},
	}
}

func notionThreadPayload(spaceID, userID, threadID, threadType string) ([]byte, error) {
	now := time.Now().UnixMilli()
	value := map[string]any{
		"requestId": newUUID(),
		"transactions": []any{map[string]any{
			"id":      newUUID(),
			"spaceId": spaceID,
			"operations": []any{map[string]any{
				"pointer": map[string]any{"table": "thread", "id": threadID, "spaceId": spaceID},
				"path":    []any{},
				"command": "set",
				"args": map[string]any{
					"id": threadID, "version": 1, "parent_id": spaceID,
					"parent_table": "space", "space_id": spaceID, "created_time": now,
					"created_by_id": userID, "created_by_table": "notion_user",
					"messages": []any{}, "data": map[string]any{}, "alive": true, "type": threadType,
				},
			}},
		}},
	}
	return json.Marshal(value)
}

func notionInferencePayload(messages []chatMessage, spaceID, userID, threadID, model, threadType string) ([]byte, error) {
	contextValue := map[string]any{
		"timezone": "Asia/Shanghai", "spaceId": spaceID, "userId": userID,
		"currentDatetime": time.Now().Format(time.RFC3339Nano),
	}
	configValue := map[string]any{
		"type": threadType, "model": model, "useWebSearch": true,
	}
	if strings.HasPrefix(model, "vertex-") {
		contextValue["surface"] = "ai_module"
		configValue["enableAgentAutomations"] = false
		configValue["enableAgentIntegrations"] = false
		configValue["enableBackgroundAgents"] = false
		configValue["enableCodegenIntegration"] = false
		configValue["enableCustomAgents"] = false
		configValue["enableExperimentalIntegrations"] = false
		configValue["enableLinkedDatabases"] = false
		configValue["enableAgentViewVersionHistoryTool"] = false
		configValue["searchScopes"] = []any{map[string]any{"type": "everything"}}
		configValue["enableDatabaseAgents"] = false
		configValue["enableAgentComments"] = false
		configValue["enableAgentForms"] = false
		configValue["enableAgentMakesFormulas"] = false
		configValue["enableUserSessionContext"] = false
		configValue["modelFromUser"] = true
		configValue["isCustomAgent"] = false
	}
	transcript := []any{
		map[string]any{"id": newUUID(), "type": "config", "value": configValue},
		map[string]any{"id": newUUID(), "type": "context", "value": contextValue},
	}
	for _, msg := range messages {
		content, err := textContent(msg.Content)
		if err != nil {
			return nil, err
		}
		switch msg.Role {
		case "user":
			transcript = append(transcript, map[string]any{
				"id": newUUID(), "type": "user", "value": []any{[]any{content}},
				"userId": userID, "createdAt": time.Now().Format(time.RFC3339Nano),
			})
		case "assistant":
			transcript = append(transcript, map[string]any{"id": newUUID(), "type": "agent-inference", "value": []any{map[string]any{"type": "text", "content": content}}})
		case "system":
			// The reference provider only serializes user and assistant turns;
			// silently omitting system turns would be surprising, so preserve it
			// as a user-visible context turn.
			transcript = append(transcript, map[string]any{
				"id": newUUID(), "type": "user", "value": []any{[]any{"[System]\n" + content}},
				"userId": userID, "createdAt": time.Now().Format(time.RFC3339Nano),
			})
		}
	}
	value := map[string]any{
		"traceId": newUUID(), "spaceId": spaceID, "transcript": transcript,
		"threadId": threadID, "createThread": false, "isPartialTranscript": true,
		"asPatchResponse": true, "generateTitle": true, "saveAllThreadOperations": true,
		"threadType": threadType,
	}
	if strings.HasPrefix(model, "vertex-") {
		value["debugOverrides"] = map[string]any{
			"emitAgentSearchExtractedResults": true, "cachedInferences": map[string]any{},
			"annotationInferences": map[string]any{}, "emitInferences": false,
		}
	}
	return json.Marshal(value)
}

type notionPart struct {
	content string
	final   bool
}

func notionParts(line []byte) ([]notionPart, error) {
	var value map[string]any
	if err := json.Unmarshal(line, &value); err != nil {
		return nil, fmt.Errorf("invalid notion NDJSON event: %w", err)
	}
	if value["type"] == "error" || value["error"] != nil {
		return nil, errors.New("notion upstream returned an error")
	}
	parts := []notionPart{}
	if value["type"] == "markdown-chat" {
		if content, ok := value["value"].(string); ok && content != "" {
			parts = append(parts, notionPart{content: cleanNotionContent(content), final: true})
		}
	}
	if value["type"] == "patch" {
		operations, ok := value["v"].([]any)
		if !ok {
			return parts, nil
		}
		for _, raw := range operations {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			opType, _ := op["o"].(string)
			path, _ := op["p"].(string)
			content, _ := op["v"].(string)
			if opType == "x" && (strings.Contains(path, "/s/") && strings.HasSuffix(path, "/value") || strings.Contains(path, "/value/")) && content != "" {
				parts = append(parts, notionPart{content: cleanNotionContent(content)})
				continue
			}
			if opType == "a" && strings.HasSuffix(path, "/s/-") {
				if nested, ok := op["v"].(map[string]any); ok && nested["type"] == "markdown-chat" {
					if full, ok := nested["value"].(string); ok && full != "" {
						parts = append(parts, notionPart{content: cleanNotionContent(full), final: true})
					}
				}
			}
			if opType == "a" && strings.HasSuffix(path, "/value/-") {
				if nested, ok := op["v"].(map[string]any); ok && nested["type"] == "text" {
					if full, ok := nested["content"].(string); ok && full != "" {
						parts = append(parts, notionPart{content: cleanNotionContent(full), final: true})
					}
				}
			}
		}
	}
	if value["type"] == "record-map" {
		if recordMap, ok := value["recordMap"].(map[string]any); ok {
			if messages, ok := recordMap["thread_message"].(map[string]any); ok {
				if len(messages) > 1 {
					return nil, errors.New("notion returned ambiguous message records")
				}
				for _, raw := range messages {
					message, ok := raw.(map[string]any)
					if !ok {
						continue
					}
					outer, _ := message["value"].(map[string]any)
					inner, _ := outer["value"].(map[string]any)
					step, _ := inner["step"].(map[string]any)
					switch step["type"] {
					case "markdown-chat":
						if content, ok := step["value"].(string); ok && content != "" {
							return []notionPart{{content: cleanNotionContent(content), final: true}}, nil
						}
					case "agent-inference":
						if values, ok := step["value"].([]any); ok {
							for _, item := range values {
								if text, ok := item.(map[string]any); ok && text["type"] == "text" {
									if content, ok := text["content"].(string); ok && content != "" {
										return []notionPart{{content: cleanNotionContent(content), final: true}}, nil
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return parts, nil
}

func cleanNotionContent(content string) string {
	for _, pair := range [][2]string{
		{`(?is)<lang primary="[^"]*"\s*/>\s*`, ""},
		{`(?is)<thinking>.*?</thinking>\s*`, ""},
		{`(?is)<thought>.*?</thought>\s*`, ""},
	} {
		content = notionRegexReplace(pair[0], pair[1], content)
	}
	return content
}

var notionRegexReplace = func(pattern, replacement, value string) string {
	return regexp.MustCompile(pattern).ReplaceAllString(value, replacement)
}

func (c *Client) newNotionStreamBody(source io.ReadCloser, id, model string) io.ReadCloser {
	reader := bufio.NewReaderSize(source, 32<<10)
	created := time.Now().Unix()
	started, ended, eof, sawIncremental := false, false, false, false
	sawContent := false
	return &transformBody{
		next: func() ([]byte, error) {
			if !started {
				started = true
				return chatChunk(id, model, created, map[string]any{"role": "assistant", "content": ""}, nil), nil
			}
			if ended {
				return nil, io.EOF
			}
			for {
				if eof {
					if !sawContent {
						return nil, io.ErrUnexpectedEOF
					}
					ended = true
					return append(chatChunk(id, model, created, map[string]any{}, "stop"), sseDone...), nil
				}
				line, err := boundedLine(reader)
				if len(line) > 0 {
					complete := strings.HasSuffix(line, "\n")
					line = strings.TrimSpace(line)
					if line != "" {
						parts, parseErr := notionParts([]byte(line))
						if parseErr != nil {
							return nil, parseErr
						}
						var output []byte
						for _, part := range parts {
							if part.content == "" {
								continue
							}
							sawContent = true
							if part.final && sawIncremental {
								continue
							}
							if !part.final {
								sawIncremental = true
							}
							output = append(output, chatChunk(id, model, created, map[string]any{"content": part.content}, nil)...)
						}
						if len(output) > 0 {
							if err == io.EOF {
								eof = true
							}
							return output, nil
						}
					}
					if err == io.EOF && !complete {
						// A final complete JSON record may omit the trailing newline.
						eof = true
					}
				}
				if err != nil {
					if err == io.EOF {
						eof = true
						continue
					}
					return nil, err
				}
			}
		},
		closeFn: source.Close,
	}
}
