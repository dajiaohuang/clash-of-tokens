package enterpriseweb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const notionBase = "https://app.notion.com"

func (c *Client) doNotion(ctx context.Context, req chatRequest) (*http.Response, error) {
	cred, _, err := c.credential()
	if err != nil {
		return nil, ErrCredential
	}
	cookie := strings.TrimSpace(cred.Cookie)
	if cookie == "" {
		cookie = strings.TrimSpace(cred.TokenV2)
	}
	if cookie == "" || cred.UserID == "" {
		return nil, ErrCredential
	}
	if !strings.Contains(cookie, "=") {
		cookie = "token_v2=" + cookie
	}
	spaceID := strings.TrimSpace(cred.SpaceID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(c.source.Project)
	}
	if spaceID == "" {
		return nil, errors.New("enterpriseweb adapter: Notion space_id is required")
	}
	base, err := baseURL(c.source, notionBase)
	if err != nil {
		return nil, err
	}
	body, err := buildNotionRequest(req, spaceID, cred.UserID)
	if err != nil {
		return nil, err
	}
	headers := http.Header{
		"Content-Type": []string{"application/json"}, "Accept": []string{"application/x-ndjson"},
		"Cookie": []string{cookie}, "Origin": []string{"https://app.notion.com"}, "Referer": []string{"https://app.notion.com/ai"},
		"User-Agent":            []string{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"},
		"notion-client-version": []string{"23.13.20260720.1949"}, "notion-audit-log-platform": []string{"web"},
		"x-notion-space-id": []string{spaceID}, "x-notion-active-user-header": []string{cred.UserID},
	}
	resp, err := postJSON(ctx, c.http, joinPath(base, "/api/v3/runInferenceTranscript"), body, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if req.Stream {
		resp.Body = newConvertedResponse(ctx, resp.Body, req.Model, parseNotionResponse)
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Set("X-COT-Delivery", "buffered")
		return resp, nil
	}
	return collectResponse(ctx, resp.Body, req.Model, parseNotionResponse)
}

func buildNotionRequest(req chatRequest, spaceID, userID string) ([]byte, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	transcript := []map[string]any{
		{"id": notionUUID(), "type": "config", "value": map[string]any{"type": "workflow", "model": req.Model, "useWebSearch": true}},
		{"id": notionUUID(), "type": "context", "value": map[string]any{"timezone": "UTC", "surface": "ai_module", "currentDatetime": now, "spaceId": spaceID, "userId": userID}},
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			ctxValue := transcript[1]["value"].(map[string]any)
			prior, _ := ctxValue["instructions"].(string)
			if prior != "" {
				prior += "\n"
			}
			ctxValue["instructions"] = prior + m.Content
		case "assistant":
			transcript = append(transcript, map[string]any{"id": notionUUID(), "type": "agent-inference", "value": []map[string]string{{"type": "text", "content": m.Content}}})
		case "user":
			transcript = append(transcript, map[string]any{"id": notionUUID(), "type": "user", "value": [][]string{{m.Content}}, "createdAt": now, "userId": userID})
		}
	}
	payload := map[string]any{
		"traceId": notionUUID(), "spaceId": spaceID, "threadId": notionUUID(), "createThread": true,
		"generateTitle": true, "asPatchResponse": true, "patchResponseVersion": 2, "isPartialTranscript": false,
		"saveAllThreadOperations": true, "setUnreadState": true, "createdSource": "ai_module", "threadType": "workflow",
		"supportsCustomAgentNudgeTranscriptStep": true, "isUserInAnySalesAssistedSpace": false, "isSpaceSalesAssisted": false,
		"transcript": transcript, "threadParentPointer": map[string]string{"table": "space", "id": spaceID, "spaceId": spaceID},
		"debugOverrides": map[string]any{"annotationInferences": map[string]any{}, "cachedInferences": map[string]any{}, "emitAgentSearchExtractedResults": true, "emitInferences": false},
	}
	return json.Marshal(payload)
}

func parseNotionResponse(ctx context.Context, body io.Reader, emit func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrTruncated
	}
	if len(raw) > maxResponseBytes {
		return errors.New("enterpriseweb adapter: Notion response exceeds limit")
	}
	text, inBandError := parseNotionText(raw)
	if inBandError {
		return errors.New("enterpriseweb adapter: Notion reported an upstream error")
	}
	if text == "" {
		return errors.New("enterpriseweb adapter: Notion completed without answer")
	}
	return emit(text)
}

type notionStreamState struct {
	lastLegacy      string
	lastPatchFinal  string
	lastIncremental string
	lastRecordMap   string
}

func parseNotionText(raw []byte) (string, bool) {
	state := notionStreamState{}
	errorFound := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
		if line == "" || line == "[DONE]" {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(line), &value) != nil || value == nil {
			continue
		}
		if notionRecordHasError(value) {
			errorFound = true
		}
		applyNotionStreamRecord(value, &state)
	}
	candidates := []string{state.lastRecordMap, state.lastPatchFinal, state.lastIncremental, state.lastLegacy}
	best := ""
	for _, candidate := range candidates {
		candidate = sanitizeNotionText(candidate)
		if len(candidate) > len(best) {
			best = candidate
		}
	}
	return best, errorFound
}

func sanitizeNotionText(text string) string {
	clean := strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	for {
		start := strings.Index(clean, "<lang")
		if start < 0 {
			break
		}
		end := strings.Index(clean[start:], ">")
		if end < 0 {
			if start == 0 {
				return ""
			}
			break
		}
		clean = clean[:start] + clean[start+end+1:]
	}
	clean = strings.ReplaceAll(clean, "</lang>", "")
	return strings.TrimSpace(clean)
}

func notionRecordHasError(value map[string]any) bool {
	typ, _ := value["type"].(string)
	if strings.EqualFold(typ, "error") {
		return true
	}
	if raw, ok := value["error"]; ok && raw != nil {
		return true
	}
	if subtype, _ := value["subType"].(string); strings.TrimSpace(subtype) != "" {
		return true
	}
	if retryable, _ := value["isRetryable"].(bool); retryable {
		message, _ := value["message"].(string)
		if strings.Contains(strings.ToLower(message), "went wrong") {
			return true
		}
	}
	if data, ok := value["data"].(map[string]any); ok {
		if notionStringMapSliceHasError(data["s"]) {
			return true
		}
	}
	return notionStringMapSliceHasError(value["s"])
}

func notionStringMapSliceHasError(raw any) bool {
	items, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if object, ok := item.(map[string]any); ok && notionRecordHasError(object) {
			return true
		}
	}
	return false
}

func applyNotionStreamRecord(value map[string]any, state *notionStreamState) {
	typ, _ := value["type"].(string)
	switch typ {
	case "markdown-chat":
		if text, _ := value["value"].(string); text != "" {
			state.lastPatchFinal = text
		}
		return
	case "agent-inference":
		if text := notionPartsText(value["value"]); text != "" {
			state.lastPatchFinal = text
		}
		return
	case "patch":
		if ops, ok := value["v"].([]any); ok {
			for _, raw := range ops {
				applyNotionPatchOp(raw, state)
			}
		}
		return
	case "record-map":
		if text := notionRecordMapText(value["recordMap"]); text != "" {
			state.lastRecordMap = text
		}
		return
	}
	if _, ok := value["recordMap"]; ok {
		if text := notionRecordMapText(value["recordMap"]); text != "" {
			state.lastRecordMap = text
		}
		return
	}
	if rich, ok := value["value"].([]any); ok {
		if text := notionRichText(rich); text != "" {
			state.lastLegacy = text
		}
	}
}

func applyNotionPatchOp(raw any, state *notionStreamState) {
	op, ok := raw.(map[string]any)
	if !ok {
		return
	}
	o, _ := op["o"].(string)
	p, _ := op["p"].(string)
	v := op["v"]
	if o == "a" && strings.HasSuffix(p, "/value/-") {
		if part, ok := v.(map[string]any); ok {
			if typ, _ := part["type"].(string); typ == "text" {
				if content, _ := part["content"].(string); content != "" {
					state.lastPatchFinal = content
				}
			} else if typ == "markdown-chat" {
				if text, _ := part["value"].(string); text != "" {
					state.lastPatchFinal = text
				}
			}
		}
		return
	}
	if o == "a" && strings.HasSuffix(p, "/s/-") {
		if part, ok := v.(map[string]any); ok {
			if typ, _ := part["type"].(string); typ == "markdown-chat" {
				if text, _ := part["value"].(string); text != "" {
					state.lastPatchFinal = text
				}
			} else if typ == "agent-inference" {
				if text := notionPartsText(part["value"]); text != "" {
					state.lastPatchFinal = text
				}
			}
		}
		return
	}
	if (o == "x" || o == "p") && strings.Contains(p, "/value") {
		if text, _ := v.(string); text != "" {
			state.lastIncremental += text
		}
	}
}

func notionRecordMapText(raw any) string {
	record, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	threads, ok := record["thread_message"].(map[string]any)
	if !ok {
		return ""
	}
	best := ""
	for _, rawMessage := range threads {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		v1, _ := message["value"].(map[string]any)
		v2, _ := v1["value"].(map[string]any)
		step, _ := v2["step"].(map[string]any)
		if text := notionRecordText(step); len(text) > len(best) {
			best = text
		}
	}
	return best
}

func notionRecordText(value map[string]any) string {
	if value == nil {
		return ""
	}
	typ, _ := value["type"].(string)
	if typ == "markdown-chat" {
		text, _ := value["value"].(string)
		return text
	}
	if typ == "agent-inference" {
		return notionPartsText(value["value"])
	}
	return ""
}

func notionRichText(rich []any) string {
	var b strings.Builder
	for _, item := range rich {
		segment, ok := item.([]any)
		if ok && len(segment) > 0 {
			if text, ok := segment[0].(string); ok {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

func notionPartsText(value any) string {
	var b strings.Builder
	if parts, ok := value.([]any); ok {
		for _, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := part["type"].(string); typ == "text" {
				if s, _ := part["content"].(string); s != "" {
					b.WriteString(s)
				}
			}
		}
	}
	return b.String()
}

func notionUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return randomID("")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
