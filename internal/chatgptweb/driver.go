// Package chatgptweb implements ChatGPT's web conversation surface through a
// real, explicitly configured browser. It does not call the official model API,
// solve challenges, emulate tools, or share a user's existing browser tab.
package chatgptweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/input"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"clash-of-tokens/internal/config"
)

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string           { return e.Message }
func (e *Error) HTTPStatus() int         { return e.Code }
func problem(code int, msg string) error { return &Error{code, msg} }

type Driver struct {
	cfg     config.Browser
	source  string
	store   *sessionStore
	origin  string
	connect func() (context.Context, context.CancelFunc)
}

func New(c config.Browser, source string) *Driver {
	d := &Driver{cfg: c, source: source, store: newStore(c), origin: "https://chatgpt.com"}
	d.connect = func() (context.Context, context.CancelFunc) {
		allocator, acancel := chromedp.NewRemoteAllocator(context.Background(), c.CDPURL)
		tab, tcancel := chromedp.NewContext(allocator)
		return tab, func() { tcancel(); acancel() }
	}
	return d
}

type pageState struct {
	Account      string `json:"account"`
	Ready        bool   `json:"ready"`
	Draft        string `json:"draft"`
	Conversation string `json:"conversation"`
}
type turnState struct {
	Conversation string `json:"conversation"`
	Node         string `json:"node"`
	UserNode     string `json:"user_node"`
	Text         string `json:"text"`
	Done         bool   `json:"done"`
	Model        string `json:"model"`
	Pending      bool   `json:"pending"`
}
type scriptResult struct {
	Value json.RawMessage `json:"value"`
	Error string          `json:"error"`
}

func evaluate(ctx context.Context, code string, args any, out any) error {
	data, _ := json.Marshal(args)
	script := "(async()=>{try { const args=" + string(data) + ";" + pageHelpers + " const value=await(async()=>{" + code + "})();return {value};} catch(e){ return {error:String(e.message || 'page_error')}; }})()"
	var result scriptResult
	e := chromedp.Run(ctx, chromedp.Evaluate(script, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	if e != nil {
		return problem(502, "browser connection or page execution failed")
	}
	if result.Error != "" {
		code := 502
		switch result.Error {
		case "login_required", "http_401":
			code = 401
		case "http_403", "account_changed":
			code = 403
		case "http_429":
			code = 429
		case "response_too_large", "conversation_too_large":
			code = 413
		case "submitted_turn_does_not_match", "conversation_changed", "conversation_branch_changed":
			code = 409
		}
		return problem(code, "ChatGPT web: "+result.Error)
	}
	if out == nil {
		return nil
	}
	if e = json.Unmarshal(result.Value, out); e != nil {
		return problem(502, "ChatGPT page returned an invalid result")
	}
	return nil
}

var sessionKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)
var conversationKey = regexp.MustCompile(`^[a-zA-Z0-9-]{1,96}$`)

func (d *Driver) Do(ctx context.Context, protocol, model string, body []byte, headers http.Header) (*http.Response, error) {
	if !d.cfg.Enabled {
		return nil, problem(503, "browser adapter is disabled")
	}
	req, e := parseRequest(protocol, body)
	if e != nil {
		return nil, problem(422, e.Error())
	}
	prompt := req.Messages[len(req.Messages)-1].Content
	if len(prompt) > d.cfg.MaxPromptBytes {
		return nil, problem(413, "ChatGPT web prompt exceeds configured byte limit")
	}
	key := headers.Get("X-COT-Session")
	if key != "" && !sessionKey.MatchString(key) {
		return nil, problem(400, "invalid X-COT-Session")
	}
	session, exists, e := d.store.get(key, req.Previous)
	if e != nil {
		return nil, problem(409, e.Error())
	}
	if exists {
		if session.Dirty {
			return nil, problem(409, "previous web turn did not finish reliably; inspect the web conversation and start a new session")
		}
		if session.Source != d.source || session.Protocol != protocol || session.Model != model {
			return nil, problem(409, "session is bound to another source, protocol or model")
		}
		if !conversationKey.MatchString(session.Conversation) || session.LastNode == "" {
			return nil, problem(409, "invalid persisted conversation binding")
		}
		if protocol == "chat" && (len(req.Messages) != session.HistoryCount+1 || digest(req.Messages[:len(req.Messages)-1]) != session.HistoryHash) {
			return nil, problem(409, "conversation history does not match the saved session")
		}
		if protocol == "responses" && (len(req.Messages) != 1 || req.Previous != session.ResponseID) {
			return nil, problem(409, "continue Responses with the latest previous_response_id and one user message")
		}
	} else {
		if len(req.Messages) != 1 {
			return nil, problem(409, "start with one user message; continue Chat with X-COT-Session and the exact full history")
		}
		if key == "" {
			key = id("web_")
		}
		session = Session{ID: key, Source: d.source, Model: model, Protocol: protocol}
	}
	tab, closeTab := d.connect()
	turn, turnCancel := context.WithCancel(tab)
	stopCancel := context.AfterFunc(ctx, turnCancel)
	cleanup := func() { stopCancel(); turnCancel(); closeTab() }
	// chromedp binds target lifetime to its first Run context. Initialize on the
	// whole-turn context, not the shorter navigation timeout below.
	if e = chromedp.Run(turn); e != nil {
		cleanup()
		return nil, problem(503, "cannot connect to the configured browser; run browser-login first")
	}
	target := d.origin + "/?model=" + url.QueryEscape(model)
	if exists {
		target = d.origin + "/c/" + session.Conversation
	}
	setupCtx, setupCancel := context.WithTimeout(turn, 30*time.Second)
	if e = chromedp.Run(setupCtx, chromedp.Navigate(target)); e != nil {
		setupCancel()
		cleanup()
		return nil, problem(503, "cannot connect to the configured browser; run browser-login first")
	}
	var page pageState
	for {
		e = evaluate(setupCtx, inspectPage, nil, &page)
		if e != nil {
			break
		}
		if page.Ready {
			break
		}
		select {
		case <-setupCtx.Done():
			e = problem(401, "ChatGPT composer unavailable; sign in or resolve the browser challenge manually")
		case <-time.After(500 * time.Millisecond):
		}
		if e != nil {
			break
		}
	}
	setupCancel()
	if e != nil {
		cleanup()
		return nil, e
	}
	if exists && digest(page.Account) != session.Account {
		cleanup()
		return nil, problem(403, "browser account changed; the session remains bound to its original account")
	}
	session.Account = digest(page.Account)
	if page.Draft != "" {
		cleanup()
		return nil, problem(409, "composer contains a draft; refusing to overwrite it")
	}
	if exists {
		var before turnState
		e = evaluate(turn, readTurn, map[string]any{"account": page.Account, "conversation": session.Conversation, "parent": "", "before": true}, &before)
		if e != nil || before.Node != session.LastNode {
			cleanup()
			if e != nil {
				return nil, e
			}
			return nil, problem(409, "upstream conversation changed outside this gateway")
		}
	}
	if e = evaluate(turn, `const el=composer();if(!el)throw new Error('composer_missing');el.focus();return true;`, nil, nil); e == nil {
		e = chromedp.Run(turn, chromedp.ActionFunc(func(ctx context.Context) error { return input.InsertText(prompt).Do(ctx) }))
	}
	if e != nil {
		cleanup()
		return nil, problem(502, "cannot enter the web prompt")
	}
	var typed string
	if e = evaluate(turn, `const el=composer();return el?composerText(el):'';`, nil, &typed); e != nil {
		cleanup()
		return nil, e
	}
	normalized := strings.ReplaceAll(prompt, "\r\n", "\n")
	if typed != normalized && typed != normalized+"\n" {
		cleanup()
		return nil, problem(409, "composer text verification failed; nothing was submitted")
	}
	var button struct {
		Ready bool    `json:"ready"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	readyCtx, readyCancel := context.WithTimeout(turn, 10*time.Second)
	for {
		e = evaluate(readyCtx, `const el=composer();const form=el?.closest('form');if(!form)return {ready:false};const choices=[...form.querySelectorAll('button[data-testid="send-button"],button[aria-label*="Send" i],button[aria-label*="发送"],button[type="submit"]')].filter(b=>!b.disabled&&b.getClientRects().length&&b.dataset.testid!=='stop-button');const unique=[...new Set(choices)];if(unique.length!==1)return {ready:false};const r=unique[0].getBoundingClientRect();return {ready:true,x:r.x+r.width/2,y:r.y+r.height/2};`, nil, &button)
		if e != nil || button.Ready {
			break
		}
		select {
		case <-readyCtx.Done():
			e = problem(409, "send button did not become ready; nothing was submitted")
		case <-time.After(200 * time.Millisecond):
		}
		if e != nil {
			break
		}
	}
	readyCancel()
	if e != nil {
		cleanup()
		return nil, e
	}
	session.Dirty = true
	if e = d.store.put(session); e != nil {
		cleanup()
		return nil, problem(503, e.Error())
	}
	// Persist uncertainty BEFORE the one and only send click. An ambiguous failure
	// cannot be automatically replayed as a duplicate web generation.
	if e = evaluate(turn, `const form=composer()?.closest('form');const b=form?.querySelector('button[data-testid="send-button"]');if(!b||b.disabled||b.getAttribute('aria-disabled')==='true')throw new Error('send_button_unavailable');b.click();return true;`, nil, nil); e != nil {
		cleanup()
		return nil, problem(502, "web submission outcome unknown; session marked dirty")
	}
	// Observe a submission transition before waiting for generation. A no-op
	// click must not occupy the account until the full inference timeout.
	submitCtx, submitCancel := context.WithTimeout(turn, 10*time.Second)
	for {
		var submitted bool
		e = evaluate(submitCtx, `const el=composer();return !el || composerText(el)==='';`, nil, &submitted)
		if e != nil || submitted {
			break
		}
		select {
		case <-submitCtx.Done():
			e = problem(502, "web submission was not observed; inspect the dirty session before retrying")
		case <-time.After(200 * time.Millisecond):
		}
		if e != nil {
			break
		}
	}
	submitCancel()
	if e != nil {
		cleanup()
		return nil, e
	}
	responseID := id("resp_")
	finish := func(result turnState) error {
		session.Conversation = result.Conversation
		session.LastNode = result.Node
		session.ResponseID = responseID
		session.Dirty = false
		session.HistoryCount = len(req.Messages) + 1
		history := append(req.Messages, message{"assistant", result.Text})
		session.HistoryHash = digest(history)
		return d.store.put(session)
	}
	observe := func(emit func(string, string) error) (turnState, error) {
		return d.observe(turn, page.Account, prompt, session, emit)
	}
	responseHeaders := make(http.Header)
	responseHeaders.Set("X-COT-Session", session.ID)
	responseHeaders.Set("X-COT-Response-Id", responseID)
	if !req.Stream {
		result, e := observe(func(string, string) error { return nil })
		if e == nil {
			e = finish(result)
		}
		if e != nil {
			d.stop(tab)
			cleanup()
			return nil, e
		}
		cleanup()
		out := completed(protocol, responseID, result.Model, result.Text)
		encoded, _ := json.Marshal(out)
		responseHeaders.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: 200, Header: responseHeaders, Body: io.NopCloser(bytes.NewReader(encoded))}, nil
	}
	reader, writer := io.Pipe()
	responseHeaders.Set("Content-Type", "text/event-stream")
	go func() {
		defer cleanup()
		events := newEvents(writer, protocol, responseID, model)
		result, e := observe(events.delta)
		if e == nil {
			e = finish(result)
		}
		if e == nil {
			e = events.complete(result.Model, result.Text)
		}
		if e != nil {
			d.stop(tab)
			_ = writer.CloseWithError(e)
		} else {
			_ = writer.Close()
		}
	}()
	return &http.Response{StatusCode: 200, Header: responseHeaders, Body: reader}, nil
}
func (d *Driver) observe(ctx context.Context, account, prompt string, session Session, emit func(string, string) error) (turnState, error) {
	ticker := time.NewTicker(time.Duration(d.cfg.PollMS) * time.Millisecond)
	defer ticker.Stop()
	last := ""
	var result turnState
	for {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		e := evaluate(ctx, readTurn, map[string]any{"account": account, "conversation": session.Conversation, "parent": session.LastNode, "prompt": prompt, "max_response": d.cfg.MaxResponseBytes}, &result)
		if e != nil {
			return result, e
		}
		if result.Conversation != "" && session.Conversation == "" {
			session.Conversation = result.Conversation
			if e = d.store.put(session); e != nil {
				return result, e
			}
		}
		if !result.Pending {
			if result.Model == "" {
				return result, problem(502, "upstream model identity is unavailable")
			}
			if session.Model != "auto" && result.Model != session.Model {
				return result, problem(409, "ChatGPT selected a different model; no quality downgrade is accepted")
			}
			if !strings.HasPrefix(result.Text, last) {
				return result, problem(502, "web output was revised after streaming; cannot append it faithfully")
			}
			if len(result.Text) > len(last) {
				if e = emit(result.Text[len(last):], result.Model); e != nil {
					return result, e
				}
				last = result.Text
			}
			if result.Done && result.Text != "" && result.Node != "" {
				return result, nil
			}
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
		}
	}
}
func (d *Driver) stop(tab context.Context) {
	ctx, cancel := context.WithTimeout(tab, 2*time.Second)
	defer cancel()
	_ = evaluate(ctx, `const b=document.querySelector('button[data-testid="stop-button"]');if(b&&!b.disabled)b.click();return true;`, nil, nil)
}
func (d *Driver) Check(ctx context.Context) (map[string]any, error) {
	tab, closeTab := d.connect()
	defer closeTab()
	turn, cancel := context.WithCancel(tab)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if e := chromedp.Run(turn, chromedp.Navigate(d.origin+"/?model=auto")); e != nil {
		return nil, errors.New("browser unavailable")
	}
	var p pageState
	deadline := time.Now().Add(10 * time.Second)
	for {
		if e := evaluate(turn, inspectPage, nil, &p); e != nil {
			return nil, e
		}
		if p.Ready || time.Now().After(deadline) { break }
		select {
		case <-turn.Done(): return nil, turn.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return map[string]any{"authenticated": p.Account != "", "composer_ready": p.Ready, "account_fingerprint": digest(p.Account), "sends_inference": false}, nil
}
