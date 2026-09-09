package businessweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	aiStudioBuildOrigin = "https://generativelanguage.googleapis.com"
	aiStudioBuildMaxRun = 125 * time.Second
	// Build renders user code in Google's SafeContentFrame sandbox. Keep the
	// selector tied to that source-backed iframe origin; the data attributes are
	// reserved for the local loopback fixture and are never accepted on a public
	// page by aiStudioBuildPreviewURLAllowed.
	aiStudioBuildPreviewSelector = `iframe[src*=".scf.usercontent.goog"],iframe[data-aistudio-preview="true"],iframe[data-cot-aistudio-preview="true"]`
)

var aiStudioBuildTerminalReasons = map[string]bool{
	"STOP": true,
}

type aiStudioBuildBrowserError struct {
	status  int
	message string
}

func (e *aiStudioBuildBrowserError) Error() string   { return e.message }
func (e *aiStudioBuildBrowserError) HTTPStatus() int { return e.status }

type aiStudioBuildFetchResult struct {
	Status      int    `json:"status"`
	StatusText  string `json:"status_text"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
	Error       string `json:"error"`
}

// doAIStudioBuild drives the operator-owned AI Studio Build app through the
// configured, already-authenticated Chrome session. The Build app's preview
// iframe is the origin that is allowed to make the authenticated Gemini
// fetch; the AI Studio shell itself is not a generic chat endpoint.
func (c *Client) doAIStudioBuild(ctx context.Context, req chatRequest) (*http.Response, error) {
	if !c.browser.Enabled || strings.TrimSpace(c.browser.CDPURL) == "" {
		return nil, &aiStudioBuildBrowserError{status: http.StatusServiceUnavailable, message: "AI Studio Build requires browser.enabled and a running configured Chrome"}
	}
	appURL, err := aiStudioBuildAppURL(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	path, body, err := aiStudioBuildRequest(req)
	if err != nil {
		return nil, err
	}
	result, err := c.aiStudioBuildFetch(ctx, appURL, path, body)
	if err != nil {
		return nil, err
	}
	if result.Error != "" {
		return nil, &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build browser request failed"}
	}
	if result.Status < 200 || result.Status >= 300 {
		return nil, &HTTPError{Status: result.Status}
	}
	text, err := parseAIStudioBuildResponse([]byte(result.Body), result.ContentType)
	if err != nil {
		return nil, err
	}
	if req.Stream {
		return streamResponse(req.Model, text, ""), nil
	}
	return jsonResponse(req.Model, text, ""), nil
}

func aiStudioBuildAppURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("business web adapter: AI Studio Build requires source base_url set to an operator-owned app URL")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("business web adapter: invalid AI Studio Build app URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", errors.New("business web adapter: AI Studio Build app URL must use HTTPS")
	}
	if !isLoopback(u.Hostname()) && !strings.EqualFold(u.Hostname(), "ai.studio") {
		return "", errors.New("business web adapter: AI Studio Build app URL must use ai.studio")
	}
	parts := strings.Split(strings.Trim(u.EscapedPath(), "/"), "/")
	if len(parts) != 2 || parts[0] != "apps" || parts[1] == "" || strings.Contains(parts[1], "%2f") {
		return "", errors.New("business web adapter: AI Studio Build app URL must be /apps/<app-id>")
	}
	return u.Scheme + "://" + u.Host + "/apps/" + parts[1], nil
}

func aiStudioBuildRequest(req chatRequest) (string, []byte, error) {
	model := strings.TrimSpace(strings.TrimPrefix(req.Model, "models/"))
	if model == "" || strings.ContainsAny(model, "/?#\r\n") {
		return "", nil, &unsupportedError{"business web adapter: AI Studio Build model is invalid"}
	}
	contents := make([]any, 0, len(req.Messages))
	var system strings.Builder
	for _, m := range req.Messages {
		text, _ := textContent(m.Content)
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteByte('\n')
			}
			system.WriteString(text)
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []any{map[string]any{"text": text}},
		})
	}
	if len(contents) == 0 {
		return "", nil, &unsupportedError{"business web adapter: AI Studio Build requires a user message"}
	}
	payload := map[string]any{"contents": contents}
	if system.Len() > 0 {
		payload["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": system.String()}}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, errors.New("business web adapter: AI Studio Build request encoding failed")
	}
	method := "generateContent"
	query := ""
	if req.Stream {
		method = "streamGenerateContent"
		query = "?alt=sse"
	}
	return "/v1beta/models/" + url.PathEscape(model) + ":" + method + query, body, nil
}

func (c *Client) aiStudioBuildFetch(parent context.Context, appURL, path string, body []byte) (aiStudioBuildFetchResult, error) {
	if err := parent.Err(); err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	allocator, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	tab, tabCancel := chromedp.NewContext(allocator)
	turn, turnCancel := context.WithCancel(tab)
	stop := context.AfterFunc(parent, turnCancel)
	defer func() {
		stop()
		turnCancel()
		tabCancel()
		allocatorCancel()
	}()
	if err := chromedp.Run(turn); err != nil {
		if parent.Err() != nil {
			return aiStudioBuildFetchResult{}, parent.Err()
		}
		return aiStudioBuildFetchResult{}, &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build browser connection failed"}
	}
	setup, setupCancel := context.WithTimeout(turn, aiStudioBuildMaxRun)
	defer setupCancel()
	if err := chromedp.Run(setup, chromedp.Navigate(appURL)); err != nil {
		if parent.Err() != nil {
			return aiStudioBuildFetchResult{}, parent.Err()
		}
		return aiStudioBuildFetchResult{}, &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build app navigation failed"}
	}
	if err := aiStudioBuildStartup(setup); err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	frame, err := aiStudioBuildPreviewFrame(setup)
	if err != nil {
		return aiStudioBuildFetchResult{}, err
	}
	var result aiStudioBuildFetchResult
	script := aiStudioBuildFetchScript(randomID("build-"), path, body)
	err = chromedp.Run(setup, chromedp.Poll(script, &result,
		chromedp.WithPollingInFrame(frame),
		chromedp.WithPollingInterval(100*time.Millisecond),
		chromedp.WithPollingTimeout(aiStudioBuildMaxRun),
	))
	if err != nil {
		if parent.Err() != nil {
			return aiStudioBuildFetchResult{}, parent.Err()
		}
		if errors.Is(err, chromedp.ErrPollingTimeout) || errors.Is(err, context.DeadlineExceeded) {
			return aiStudioBuildFetchResult{}, &aiStudioBuildBrowserError{status: http.StatusGatewayTimeout, message: "AI Studio Build browser request timed out"}
		}
		return aiStudioBuildFetchResult{}, &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build browser request failed"}
	}
	return result, nil
}

func aiStudioBuildStartup(ctx context.Context) error {
	const clickScript = `(()=>{const visible=e=>{const r=e.getBoundingClientRect();const s=getComputedStyle(e);return r.width>0&&r.height>0&&s.visibility!=='hidden'&&s.display!=='none'};const exact=['Continue to the app','Skip'];for(const b of document.querySelectorAll('button,[role="button"]')){if(!visible(b))continue;const t=(b.innerText||b.textContent||'').trim();if(exact.includes(t)||(t==='Launch'||t.startsWith('Launch '))){b.click();return t}}return ''})()`
	for i := 0; i < 30; i++ {
		var clicked string
		if err := chromedp.Run(ctx, chromedp.Evaluate(clickScript, &clicked)); err != nil {
			return &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build startup control lookup failed"}
		}
		if clicked != "" {
			// The shell may expose Continue, Skip, and Launch in sequence.
			// Let the next iteration observe the next control or preview frame.
		}
		var count int
		if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelectorAll('iframe[src*=".scf.usercontent.goog"],iframe[data-aistudio-preview="true"],iframe[data-cot-aistudio-preview="true"]').length`, &count)); err != nil {
			return &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build preview lookup failed"}
		}
		if count > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return &aiStudioBuildBrowserError{status: http.StatusGatewayTimeout, message: "AI Studio Build preview did not load"}
		case <-time.After(time.Second):
		}
	}
	return &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build preview frame was not found"}
}

func aiStudioBuildPreviewFrame(ctx context.Context) (*cdp.Node, error) {
	for i := 0; i < 30; i++ {
		var nodes []*cdp.Node
		if err := chromedp.Run(ctx, chromedp.Nodes(aiStudioBuildPreviewSelector, &nodes, chromedp.ByQueryAll)); err == nil && len(nodes) > 0 {
			var tree *page.FrameTree
			// GetFrameTree must run as an action. Calling .Do with an executor
			// extracted outside the action can race chromedp's task executor.
			treeErr := chromedp.Run(ctx, chromedp.ActionFunc(func(actionCtx context.Context) error {
				var err error
				tree, err = page.GetFrameTree().Do(actionCtx)
				return err
			}))
			if treeErr == nil {
				urls := make(map[cdp.FrameID]string)
				var visit func(*page.FrameTree)
				visit = func(t *page.FrameTree) {
					if t == nil || t.Frame == nil {
						return
					}
					urls[t.Frame.ID] = t.Frame.URL
					for _, child := range t.ChildFrames {
						visit(child)
					}
				}
				visit(tree)
				for _, n := range nodes {
					if n == nil || n.FrameID == "" {
						continue
					}
					if aiStudioBuildPreviewURLAllowed(urls[n.FrameID]) {
						return n, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil, &aiStudioBuildBrowserError{status: http.StatusGatewayTimeout, message: "AI Studio Build preview frame did not load"}
		case <-time.After(time.Second):
		}
	}
	return nil, &aiStudioBuildBrowserError{status: http.StatusBadGateway, message: "AI Studio Build preview frame has no loaded execution context"}
}

func aiStudioBuildPreviewURLAllowed(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme == "blob" {
		// SafeContentFrame replaces the initial .scf URL with a blob URL. The
		// blob's opaque component retains the sandbox origin.
		inner, innerErr := url.Parse(u.Opaque)
		if innerErr != nil {
			return false
		}
		u = inner
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if u.Scheme == "https" && strings.HasSuffix(host, ".scf.usercontent.goog") && host != "scf.usercontent.goog" {
		return true
	}
	// Loopback is only for the explicitly marked local fixture selector. The
	// caller never selects an unmarked loopback iframe.
	return u.Scheme == "http" && isLoopback(host)
}

func aiStudioBuildFetchScript(id, path string, body []byte) string {
	idJSON, _ := json.Marshal(id)
	pathJSON, _ := json.Marshal(aiStudioBuildOrigin + path)
	bodyJSON, _ := json.Marshal(string(body))
	return fmt.Sprintf(`(()=>{const id=%s;let s=window.__cotAIStudioBuild;if(!s||s.id!==id){s={id,done:false,status:0,status_text:'',content_type:'',body:'',error:''};window.__cotAIStudioBuild=s;(async()=>{const ctl=new AbortController();const timer=setTimeout(()=>ctl.abort(),120000);try{const r=await fetch(%s,{method:'POST',headers:{'Content-Type':'application/json','Accept':'application/json'},credentials:'include',body:%s,signal:ctl.signal});s.status=r.status;s.status_text=r.statusText||'';s.content_type=r.headers.get('content-type')||'';const reader=r.body&&r.body.getReader?r.body.getReader():null;const dec=new TextDecoder();let total=0;const chunks=[];if(reader){for(;;){const v=await reader.read();if(v.done)break;total+=v.value.byteLength;if(total>16777216){await reader.cancel();throw new Error('response limit');}chunks.push(dec.decode(v.value,{stream:true}))}chunks.push(dec.decode())}else{const t=await r.text();if(new TextEncoder().encode(t).byteLength>16777216)throw new Error('response limit');chunks.push(t)}s.body=chunks.join('')}catch(e){s.error=String(e&&e.name==='AbortError'?'timeout':e&&e.message||e)}finally{clearTimeout(timer);s.done=true}})()}return s.done?s:false})()`, string(idJSON), string(pathJSON), string(bodyJSON))
}

func parseAIStudioBuildResponse(raw []byte, contentType string) (string, error) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("data:")) {
		return parseAIStudioBuildSSE(raw)
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&value); err != nil {
		return "", errors.New("business web adapter: AI Studio Build response is not valid JSON")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return "", errors.New("business web adapter: AI Studio Build response has multiple root values")
	}
	return parseAIStudioBuildEnvelope(value)
}

func parseAIStudioBuildSSE(raw []byte) (string, error) {
	r := bufio.NewReader(bytes.NewReader(raw))
	var out strings.Builder
	sawEvent, sawFinish := false, false
	for {
		line, err := r.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" && data != "[DONE]" {
				if sawFinish {
					return "", errors.New("business web adapter: AI Studio Build stream has data after completion")
				}
				var value any
				if json.Unmarshal([]byte(data), &value) != nil {
					return "", errors.New("business web adapter: AI Studio Build stream event is not valid JSON")
				}
				text, finished, e := parseAIStudioBuildEnvelopeState(value)
				if e != nil {
					return "", e
				}
				out.WriteString(text)
				sawEvent = true
				if finished {
					sawFinish = true
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", errors.New("business web adapter: truncated AI Studio Build stream")
		}
	}
	if !sawEvent || !sawFinish || strings.TrimSpace(out.String()) == "" {
		return "", errors.New("business web adapter: AI Studio Build stream completed without text")
	}
	return out.String(), nil
}

func parseAIStudioBuildEnvelope(value any) (string, error) {
	text, finished, err := parseAIStudioBuildEnvelopeState(value)
	if err != nil {
		return "", err
	}
	if !finished || strings.TrimSpace(text) == "" {
		return "", errors.New("business web adapter: AI Studio Build response has no completed text")
	}
	return text, nil
}

func parseAIStudioBuildEnvelopeState(value any) (string, bool, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return "", false, errors.New("business web adapter: AI Studio Build response root is not an object")
	}
	if rawError, ok := root["error"]; ok && rawError != nil {
		return "", false, errors.New("business web adapter: AI Studio Build response contains an error")
	}
	if _, ok := root["promptFeedback"]; ok && root["candidates"] == nil {
		return "", false, errors.New("business web adapter: AI Studio Build prompt was rejected")
	}
	candidates, ok := root["candidates"].([]any)
	if !ok || len(candidates) != 1 {
		return "", false, errors.New("business web adapter: AI Studio Build response does not contain exactly one candidate")
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		return "", false, errors.New("business web adapter: AI Studio Build candidate is malformed")
	}
	finished := false
	if rawFinish, exists := candidate["finishReason"]; exists && rawFinish != nil {
		finish, ok := rawFinish.(string)
		if !ok {
			return "", false, errors.New("business web adapter: AI Studio Build finish reason is malformed")
		}
		finish = strings.TrimSpace(finish)
		if finish != "" {
			if !aiStudioBuildTerminalReasons[finish] {
				return "", false, errors.New("business web adapter: AI Studio Build finish reason is unsupported")
			}
			finished = true
		}
	}
	content, ok := candidate["content"].(map[string]any)
	if !ok {
		if finished {
			return "", true, nil
		}
		return "", finished, errors.New("business web adapter: AI Studio Build candidate content is malformed")
	}
	if role, _ := content["role"].(string); role != "model" {
		return "", finished, errors.New("business web adapter: AI Studio Build response role is not model")
	}
	parts, ok := content["parts"].([]any)
	if !ok {
		return "", finished, errors.New("business web adapter: AI Studio Build candidate parts are malformed")
	}
	var out strings.Builder
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			return "", finished, errors.New("business web adapter: AI Studio Build response part is malformed")
		}
		if text, exists := part["text"]; exists {
			s, ok := text.(string)
			if !ok {
				return "", finished, errors.New("business web adapter: AI Studio Build response text is malformed")
			}
			out.WriteString(s)
		}
	}
	return out.String(), finished, nil
}
