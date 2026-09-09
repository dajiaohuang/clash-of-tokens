package majorweb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func (c *Client) doPerplexity(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || !huggingID.MatchString(model) {
		return nil, &requestError{"Perplexity requires single user text and an exact model_preference value"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://www.perplexity.ai")
	if err != nil {
		return nil, err
	}
	rid := randomUUID()
	params := map[string]any{"attachments": []any{}, "language": "en-US", "timezone": "UTC", "search_focus": "internet", "sources": []string{"web"}, "frontend_uuid": rid, "mode": "copilot", "model_preference": model, "frontend_context_uuid": randomUUID(), "prompt_source": "user", "query_source": "home", "client_coordinates": nil, "mentions": []any{}, "dsl_query": input.Prompt, "source": "default", "client_search_results_cache_key": rid, "supported_features": []string{"browser_agent_permission_banner_v1.1"}, "version": "2.18", "rum_session_id": randomUUID()}
	for _, key := range []string{"is_incognito", "use_schematized_api", "skip_search_enabled", "should_ask_for_mcp_tool_confirmation", "supports_tool_approval_modal"} {
		params[key] = true
	}
	for _, key := range []string{"is_related_query", "is_sponsored", "local_search_enabled", "send_back_text_in_streaming_api", "is_nav_suggestions_disabled", "always_search_override", "override_no_search", "browser_agent_allow_once_from_toggle", "force_enable_browser_agent", "extended_context"} {
		params[key] = false
	}
	params["supported_block_use_cases"] = strings.Split("answer_modes media_items knowledge_cards inline_entity_cards place_widgets finance_widgets sports_widgets news_widgets shopping_widgets jobs_widgets search_result_widgets inline_images inline_assets placeholder_cards diff_blocks inline_knowledge_cards entity_group_v2 refinement_filters canvas_mode maps_preview answer_tabs price_comparison_widgets preserve_latex generic_onboarding_widgets in_context_suggestions pending_followups inline_claims unified_assets workflow_steps workflow_widgets navigation_results background_agents", " ")
	data, _ := json.Marshal(map[string]any{"query_str": input.Prompt, "params": params})
	h := http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}, "Origin": {base}, "Referer": {base + "/"}, "Cookie": {cookieHeader("__Secure-next-auth.session-token", cred.cookie)}, "User-Agent": {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:148.0) Gecko/20100101 Firefox/148.0"}}
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/rest/sse/perplexity_ask"), data, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	dec := newSSEDecoder(resp.Body)
	dec.emptyEvents = true
	patchSlots, wireBytes := 0, 0
	tracks := map[string]any{}
	finalText := ""
	done := false
	for !done {
		event, data, ok, e := dec.next()
		wireBytes += len(data)
		if wireBytes > 16<<20 {
			return nil, errors.New("Perplexity response exceeds limit")
		}
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, ErrTruncated
		}
		if event == "end_of_stream" || data == "[DONE]" {
			done = true
			break
		}
		var frame struct {
			Status       string                       `json:"status"`
			Final        bool                         `json:"final"`
			Text         string                       `json:"text"`
			Display      string                       `json:"display_model"`
			Error        json.RawMessage              `json:"error"`
			ErrorCode    json.RawMessage              `json:"error_code"`
			ErrorMessage json.RawMessage              `json:"error_message"`
			Upsell       json.RawMessage              `json:"upsell_information"`
			Blocks       []map[string]json.RawMessage `json:"blocks"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			return nil, errors.New("Perplexity invalid SSE event")
		}
		if frame.Status == "FAILED" || frame.Status == "ERROR" || pplxNonempty(frame.Error) || pplxNonempty(frame.ErrorCode) || pplxNonempty(frame.ErrorMessage) || pplxNonempty(frame.Upsell) {
			return nil, errors.New("Perplexity upstream rejected query")
		}
		if frame.Display != "" && frame.Display != model {
			return nil, errors.New("Perplexity selected a different model preference")
		}
		for _, block := range frame.Blocks {
			var usage string
			json.Unmarshal(block["intended_usage"], &usage)
			for _, field := range []string{"markdown_block", "workflow_block"} {
				if raw, ok := block[field]; ok {
					if len(tracks) > 128 {
						return nil, errors.New("Perplexity too many answer tracks")
					}
					var v any
					if json.Unmarshal(raw, &v) != nil {
						return nil, errors.New("Perplexity invalid materialized block")
					}
					tracks[usage+":"+field] = v
				}
			}
			if raw, ok := block["diff_block"]; ok {
				var diff struct {
					Field   string `json:"field"`
					Patches []struct {
						Op    string `json:"op"`
						Path  string `json:"path"`
						Value any    `json:"value"`
					} `json:"patches"`
				}
				if json.Unmarshal(raw, &diff) != nil {
					return nil, errors.New("Perplexity invalid diff")
				}
				if diff.Field != "markdown_block" && diff.Field != "workflow_block" {
					continue
				}
				if len(tracks) > 128 || len(diff.Patches) > 4096 {
					return nil, errors.New("Perplexity diff exceeds limits")
				}
				key := usage + ":" + diff.Field
				for _, patch := range diff.Patches {
					path := []string{}
					if patch.Path != "" {
						if !strings.HasPrefix(patch.Path, "/") {
							return nil, errors.New("Perplexity invalid patch path")
						}
						path = strings.Split(patch.Path[1:], "/")
					}
					next, e := pplxPatch(tracks[key], path, patch.Op, patch.Value, 0, &patchSlots)
					if e != nil {
						return nil, e
					}
					tracks[key] = next
				}
			}
		}
		if frame.Status == "COMPLETED" || frame.Final {
			if frame.Text != "" {
				finalText = pplxFinalText(frame.Text)
			}
			done = true
		}
	}
	answer := finalText
	if answer == "" {
		for key, track := range tracks {
			var candidate string
			if strings.HasSuffix(key, ":workflow_block") {
				candidate = pplxWorkflowAnswer(track)
			} else if strings.Contains(key, "ask_text") || strings.Contains(key, "markdown") {
				candidate = pplxText(track, "answer")
			}
			if len(candidate) > len(answer) {
				answer = candidate
			}
		}
	}
	if strings.TrimSpace(answer) == "" {
		return nil, errors.New("Perplexity completed without answer")
	}
	return bufferedProductResponse(model, answer, "", stream), nil
}

func pplxNonempty(v json.RawMessage) bool {
	s := strings.TrimSpace(string(v))
	return s != "" && s != "null" && s != `""` && s != "{}" && s != "false"
}

func pplxPatch(node any, path []string, op string, value any, depth int, slots *int) (any, error) {
	bad := func() (any, error) { return nil, errors.New("Perplexity unsupported or excessive patch") }
	if depth > 16 || len(path) > 16 {
		return bad()
	}
	if op != "add" && op != "replace" && op != "remove" {
		return bad()
	}
	if len(path) == 0 {
		if op == "remove" {
			return nil, nil
		}
		return value, nil
	}
	part := strings.ReplaceAll(strings.ReplaceAll(path[0], "~1", "/"), "~0", "~")
	if node == nil {
		if _, e := strconv.Atoi(part); e == nil || part == "-" {
			node = []any{}
		} else {
			node = map[string]any{}
		}
	}
	switch v := node.(type) {
	case map[string]any:
		if len(v) > 4096 {
			return bad()
		}
		if len(path) == 1 && op == "remove" {
			delete(v, part)
			return v, nil
		}
		if _, exists := v[part]; !exists {
			*slots++
			if *slots > 65536 {
				return bad()
			}
		}
		next, e := pplxPatch(v[part], path[1:], op, value, depth+1, slots)
		if e != nil {
			return nil, e
		}
		v[part] = next
		return v, nil
	case []any:
		idx, e := strconv.Atoi(part)
		if part == "-" {
			idx = len(v)
			e = nil
		}
		if e != nil || idx < 0 || idx > 16384 {
			return bad()
		}
		if len(path) == 1 && op == "remove" {
			if idx >= len(v) {
				return bad()
			}
			return append(v[:idx], v[idx+1:]...), nil
		}
		for len(v) <= idx {
			*slots++
			if *slots > 65536 {
				return bad()
			}
			v = append(v, nil)
		}
		next, e := pplxPatch(v[idx], path[1:], op, value, depth+1, slots)
		if e != nil {
			return nil, e
		}
		v[idx] = next
		return v, nil
	default:
		return bad()
	}
}

func pplxText(raw any, fallback string) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	if chunks, ok := m["chunks"].([]any); ok && len(chunks) > 0 {
		var b strings.Builder
		for _, v := range chunks {
			if s, ok := v.(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	s, _ := m[fallback].(string)
	return s
}
func pplxWorkflowAnswer(raw any) string {
	m, _ := raw.(map[string]any)
	steps, _ := m["steps"].([]any)
	var out strings.Builder
	for _, step := range steps {
		s, _ := step.(map[string]any)
		items, _ := s["items"].([]any)
		for _, item := range items {
			i, _ := item.(map[string]any)
			p, _ := i["payload"].(map[string]any)
			tp, _ := p["text_payload"].(map[string]any)
			variant, _ := tp["variant"].(string)
			if variant == "" {
				variant, _ = i["variant"].(string)
			}
			if variant == "answer" {
				out.WriteString(pplxText(tp, "text"))
			}
		}
	}
	return out.String()
}
func pplxFinalText(raw string) string {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") && !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return raw
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return ""
	}
	steps, ok := value.([]any)
	if !ok {
		steps = []any{value}
	}
	for _, step := range steps {
		m, _ := step.(map[string]any)
		typ, _ := m["step_type"].(string)
		if typ != "" && typ != "FINAL" {
			continue
		}
		content := m["content"]
		var answer string
		if s, ok := content.(string); ok {
			answer = s
		} else {
			c, _ := content.(map[string]any)
			answer, _ = c["answer"].(string)
		}
		if answer == "" {
			answer, _ = m["answer"].(string)
		}
		if answer != "" {
			var inner map[string]any
			if json.Unmarshal([]byte(answer), &inner) == nil {
				if s := pplxText(inner, "answer"); s != "" {
					return s
				}
			} else {
				return answer
			}
		}
	}
	return ""
}
