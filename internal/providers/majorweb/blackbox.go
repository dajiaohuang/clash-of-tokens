package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

func (c *Client) blackboxJSON(ctx context.Context, method, target string, payload any, headers http.Header) (map[string]any, error) {
	var data []byte
	if payload != nil {
		data, _ = json.Marshal(payload)
	}
	resp, err := request(ctx, c.http, method, target, data, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, What: "Blackbox account lookup failed"}
	}
	data, err = readBounded(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, errors.New("Blackbox account response is invalid")
	}
	return value, nil
}

func (c *Client) doBlackbox(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 1<<20 || strings.TrimSpace(model) == "" {
		return nil, &requestError{"Blackbox requires a bounded chat request and model"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if cred.validated == "" || len(cred.validated) > 4096 || len(cred.cookie) > 16384 || strings.ContainsAny(cred.cookie, "\r\n") {
		return nil, errors.New("Blackbox requires credential JSON with cookie and validated frontend token")
	}
	base, err := baseURL(c.source, "https://app.blackbox.ai")
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	for k, v := range map[string]string{"Cookie": cookieHeader("next-auth.session-token", cred.cookie), "Origin": base, "Accept": "application/json", "Content-Type": "application/json", "User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"} {
		headers.Set(k, v)
	}
	session, err := c.blackboxJSON(ctx, http.MethodGet, endpoint(base, "/api/auth/session"), nil, headers)
	if err != nil {
		return nil, err
	}
	user, _ := session["user"].(map[string]any)
	email, _ := user["email"].(string)
	if email == "" {
		return nil, errors.New("Blackbox account session has no user email")
	}
	sub, err := c.blackboxJSON(ctx, http.MethodPost, endpoint(base, "/api/check-subscription"), map[string]any{"email": email}, headers)
	if err != nil {
		return nil, err
	}
	premium, ok := sub["hasActiveSubscription"].(bool)
	if !ok {
		return nil, errors.New("Blackbox subscription response has no status")
	}
	status := "FREE"
	if premium {
		status = "PREMIUM"
	}
	cache := map[string]any{"status": status, "lastChecked": time.Now().UnixMilli(), "hasPaymentVerificationFailure": false, "verificationFailureTimestamp": nil, "requiresAuthentication": false}
	for _, key := range []string{"customerId", "expiryTimestamp", "provider"} {
		cache[key] = sub[key]
	}
	for _, key := range []string{"isTrialSubscription", "isTeam", "previouslySubscribed", "activeInsuffientCredits"} {
		value, _ := sub[key].(bool)
		cache[key] = value
	}
	cache["numSeats"] = 1
	if sub["numSeats"] != nil {
		cache["numSeats"] = sub["numSeats"]
	}
	chatID := randomUUID()[:7]
	payload := map[string]any{"messages": []any{map[string]any{"id": chatID, "role": "user", "content": input.Prompt}}, "id": chatID, "maxTokens": 1024, "userSelectedModel": model, "userSelectedAgent": "VscodeAgent", "validated": cred.validated, "session": session, "isPremium": premium, "teamAccount": email, "subscriptionCache": cache, "codeModelMode": true, "imageGenMode": "autoMode"}
	for _, key := range []string{"previewToken", "userId", "userSystemPrompt", "playgroundTopP", "playgroundTemperature", "domains", "selectedElement"} {
		payload[key] = nil
	}
	if cred.userID != "" {
		payload["userId"] = cred.userID
	}
	for _, key := range []string{"isMicMode", "isChromeExt", "clickedAnswer2", "clickedAnswer3", "clickedForceWebSearch", "visitFromDelta", "isMemoryEnabled", "mobileClient", "imageGenerationMode", "webSearchModePrompt", "deepSearchMode", "vscodeClient", "codeInterpreterMode", "beastMode", "reasoningMode", "designerMode", "asyncMode", "isTaskPersistent"} {
		payload[key] = false
	}
	for _, key := range []string{"githubToken", "promptSelection", "workspaceId"} {
		payload[key] = ""
	}
	payload["trendingAgentMode"] = map[string]any{}
	payload["integrations"] = map[string]any{}
	payload["customProfile"] = map[string]any{"name": "", "occupation": "", "traits": []any{}, "additionalInfo": "", "enableNewChats": false}
	payload["webSearchModeOption"] = map[string]any{"autoMode": true, "webMode": false, "offlineMode": false}
	encoded, _ := json.Marshal(payload)
	headers.Set("Accept", "text/plain, */*")
	headers.Set("Referer", base+"/chat/"+chatID)
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/api/chat"), encoded, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{Status: resp.StatusCode, What: "Blackbox chat failed"}
	}
	data, err := readBounded(resp.Body, 16<<20)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) || len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("Blackbox returned invalid text")
	}
	text := string(data)
	lower := strings.ToLower(text)
	for _, marker := range []string{"not upgraded", "upgrade to a premium plan", "upgrade.required", "please upgrade", "invalid validated", "invalid token", "rate limit", "too many requests", "please login", "login required", "authentication required"} {
		if strings.Contains(lower, marker) {
			return nil, errors.New("Blackbox rejected the request")
		}
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/html") {
		return nil, errors.New("Blackbox returned a web page instead of an answer")
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var result []byte
	if stream {
		result = chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil)
		result = append(result, chatChunk(id, model, created, map[string]any{"content": text}, nil)...)
		result = append(result, chatChunk(id, model, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, model, text, "", created)
	}
	out := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}
	out.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		out.Header.Set("Content-Type", "text/event-stream")
	} else {
		out.Header.Set("Content-Type", "application/json")
	}
	return out, nil
}
