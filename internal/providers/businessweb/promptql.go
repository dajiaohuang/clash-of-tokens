package businessweb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultPromptQLGraphQL = "https://data.prompt.ql.app/promptql/playground-v2-hge/v1/graphql"

const startThreadQuery = `mutation StartThread($message:String!,$projectId:String!,$timezone:String!,$llmConfigId:String!){start_thread(message:$message,projectId:$projectId,timezone:$timezone,llmConfigId:$llmConfigId,roomless:true,uploads:[],agentResponseConfig:"force_respond"){thread_id thread_events{thread_event_id event_data}}}`
const eventsQuery = `query Events($thread_id:uuid!,$after_event_id:bigint!){thread_events(where:{thread_id:{_eq:$thread_id},thread_event_id:{_gt:$after_event_id}},order_by:{thread_event_id:asc}){thread_event_id event_data created_at}}`

type graphResponse struct {
	Data   map[string]any   `json:"data"`
	Errors []map[string]any `json:"errors"`
}

func (c *Client) doPromptQL(ctx context.Context, req chatRequest) (*http.Response, error) {
	cred, err := credential(c.source)
	if err != nil {
		return nil, err
	}
	token := stripBearer(firstNonempty(cred["token"], cred["jwt"], cred["access_token"], cred["value"]))
	if token == "" {
		return nil, ErrCredential
	}
	project := firstNonempty(c.source.Project, cred["projectId"], cred["project_id"], jwtProject(token))
	if project == "" {
		return nil, errors.New("business web adapter: PromptQL project id is required")
	}
	if strings.TrimSpace(req.Model) != "web" {
		return nil, &unsupportedError{"business web adapter: PromptQL requires the model selector \"web\""}
	}
	llmConfigID := strings.TrimSpace(credOr(cred, "llm_config_id", ""))
	if llmConfigID == "" || len(llmConfigID) > 256 || strings.ContainsAny(llmConfigID, "\r\n\x00") {
		return nil, ErrCredential
	}
	endpoint, err := baseURL(c.source, defaultPromptQLGraphQL)
	if err != nil {
		return nil, err
	}
	latestUser := lastUser(req.Messages)
	if latestUser == "" {
		return nil, &unsupportedError{"business web adapter: PromptQL requires a user message"}
	}
	if strings.TrimSpace(req.ThreadID) != "" {
		return nil, &unsupportedError{"business web adapter: PromptQL thread continuation is unavailable until model and thread binding can be verified"}
	}
	thread := ""
	after := "0"
	seedAnswer := ""
	data, err := promptGQL(ctx, c.http, endpoint, token, startThreadQuery, map[string]any{"message": promptQLHistory(req.Messages), "projectId": project, "timezone": credOr(cred, "timezone", "UTC"), "llmConfigId": llmConfigID})
	if err != nil {
		return nil, err
	}
	obj, _ := data["start_thread"].(map[string]any)
	thread, _ = obj["thread_id"].(string)
	if thread == "" {
		return nil, errors.New("business web adapter: PromptQL returned no thread id")
	}
	if seed, ok := obj["thread_events"].([]any); ok && len(seed) > 0 {
		if e, ok := seed[len(seed)-1].(map[string]any); ok {
			if id := strings.TrimSpace(fmt.Sprint(e["thread_event_id"])); id != "" && id != "<nil>" {
				after = id
			}
		}
		seedTerminal := false
		for _, item := range seed {
			if e, ok := item.(map[string]any); ok {
				text, terminal := promptEventResult(e["event_data"])
				if text != "" {
					seedAnswer = text
				}
				seedTerminal = seedTerminal || terminal
			}
		}
		if !seedTerminal {
			seedAnswer = ""
		}
	}
	answer := seedAnswer
	if answer == "" {
		answer, err = c.pollPromptQL(ctx, endpoint, token, thread, after)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(answer) == "" {
		return nil, errors.New("business web adapter: PromptQL returned no text")
	}
	if req.Stream {
		return streamResponse(req.Model, answer, thread), nil
	}
	return jsonResponse(req.Model, answer, thread), nil
}

func promptGQL(ctx context.Context, hc *http.Client, endpoint, token, query string, vars map[string]any) (map[string]any, error) {
	b, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}, "Authorization": {"Bearer " + token}, "Origin": {"https://prompt.ql.app"}, "Referer": {"https://prompt.ql.app/"}, "User-Agent": {"clash-tokens/0.1"}}
	resp, err := request(ctx, hc, http.MethodPost, endpoint, b, h)
	if err != nil {
		return nil, err
	}
	raw, err := readBounded(resp.Body, maxResponseBytes)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode}
	}
	var out graphResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, errors.New("business web adapter: PromptQL returned invalid JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("business web adapter: PromptQL returned invalid JSON")
	}
	if len(out.Errors) > 0 {
		return nil, errors.New("business web adapter: PromptQL GraphQL error")
	}
	if out.Data == nil {
		return nil, errors.New("business web adapter: PromptQL GraphQL response has no data")
	}
	return out.Data, nil
}

func (c *Client) pollPromptQL(ctx context.Context, endpoint, token, thread, after string) (string, error) {
	pollCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var answer string
	sawFinal := false
	for {
		select {
		case <-pollCtx.Done():
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("business web adapter: PromptQL timed out before a terminal response")
		default:
		}
		data, err := promptGQL(pollCtx, c.http, endpoint, token, eventsQuery, map[string]any{"thread_id": thread, "after_event_id": after})
		if err != nil {
			if pollCtx.Err() != nil && ctx.Err() == nil {
				return "", errors.New("business web adapter: PromptQL timed out before a terminal response")
			}
			return "", err
		}
		events, _ := data["thread_events"].([]any)
		for _, raw := range events {
			e, _ := raw.(map[string]any)
			if e == nil {
				continue
			}
			after = fmt.Sprint(e["thread_event_id"])
			text, terminal := promptEventResult(e["event_data"])
			if text != "" {
				answer = text
			}
			if terminal {
				sawFinal = true
			}
		}
		if sawFinal && answer != "" {
			return answer, nil
		}
		timer := time.NewTimer(1200 * time.Millisecond)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("business web adapter: PromptQL timed out before a terminal response")
		case <-timer.C:
		}
	}
}

func promptQLHistory(messages []chatMessage) string {
	var b strings.Builder
	b.WriteString("<agent_mention /> ")
	for i, message := range messages {
		text, _ := textContent(message.Content)
		if i > 0 {
			b.WriteString("\n")
		}
		switch message.Role {
		case "system":
			b.WriteString("System: ")
		case "assistant":
			b.WriteString("Assistant: ")
		default:
			b.WriteString("User: ")
		}
		b.WriteString(text)
	}
	return b.String()
}

func promptEventResult(v any) (string, bool) {
	if raw, ok := v.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) != nil {
			return "", false
		}
		return promptEventResult(decoded)
	}
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		return "", false
	}
	for key, value := range m {
		if strings.EqualFold(key, "AgentMessage") {
			return promptEventResultInAgent(value)
		}
	}
	return "", false
}

func promptEventResultInAgent(v any) (string, bool) {
	if raw, ok := v.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			return promptEventResultInAgent(decoded)
		}
		return "", false
	}
	if m, ok := v.(map[string]any); ok {
		var text string
		terminal := false
		for key, value := range m {
			switch strings.ToLower(key) {
			case "final_response.message":
				if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
					text = s
					terminal = true
				}
			case "final_response":
				if child, ok := value.(map[string]any); ok {
					if s, ok := child["message"].(string); ok && strings.TrimSpace(s) != "" {
						text = s
						terminal = true
					}
				}
			case "agent_loop_action_result_type":
				if s, ok := value.(string); ok && strings.EqualFold(s, "final_response_sent") {
					terminal = true
				}
			case "response_text":
				if s, ok := value.(string); ok {
					lower := strings.ToLower(s)
					if i := strings.Index(lower, "<final_response>"); i >= 0 {
						if j := strings.Index(lower[i:], "</final_response>"); j > 0 {
							text = strings.TrimSpace(s[i+len("<final_response>") : i+j])
							terminal = text != ""
						}
					}
				}
			}
			childText, childTerminal := promptEventResultInAgent(value)
			if childText != "" {
				text = childText
			}
			terminal = terminal || childTerminal
		}
		return text, terminal
	}
	if a, ok := v.([]any); ok {
		var text string
		terminal := false
		for _, value := range a {
			childText, childTerminal := promptEventResultInAgent(value)
			if childText != "" {
				text = childText
			}
			terminal = terminal || childTerminal
		}
		return text, terminal
	}
	return "", false
}

func jwtProject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		b, err = base64.URLEncoding.DecodeString(parts[1])
	}
	if err != nil {
		return ""
	}
	var p map[string]any
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	if h, ok := p["https://promptql.hasura.io"].(map[string]any); ok {
		if s, _ := h["x-hasura-project-id"].(string); s != "" {
			return s
		}
	}
	if s, _ := p["project_id"].(string); s != "" {
		return s
	}
	if s, _ := p["projectId"].(string); s != "" {
		return s
	}
	if s, _ := p["aud"].(string); s != "" && strings.Contains(s, "-") {
		return s
	}
	return ""
}
func firstNonempty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
