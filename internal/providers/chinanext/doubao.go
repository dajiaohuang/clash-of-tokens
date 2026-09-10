package chinanext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	cdpNetwork "github.com/chromedp/cdproto/network"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"clash-of-tokens/internal/config"
)

const (
	maxDoubaoInput       = 64 << 10
	maxDoubaoCapture     = 4 << 20
	maxDoubaoCookie      = 64 << 10
	doubaoSetupTimeout   = 30 * time.Second
	doubaoTurnTimeout    = 120 * time.Second
	doubaoPollInterval   = 100 * time.Millisecond
	defaultDoubaoBaseURL = "https://www.doubao.com"
)

// doDoubao drives a fresh tab in the caller's configured Chrome account. It
// deliberately does not call frontierSign, construct a_bogus, or export the
// account's cookies. The page's own UI submits the request, so the browser
// supplies the current signatures, device state, and authentication.
func (c *Client) doDoubao(parent context.Context, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if headers.Get("X-COT-Session") != "" {
		return nil, fmt.Errorf("%w: Doubao browser turns are isolated per request", ErrUnsupported)
	}
	input, err := decodeDoubaoRequest(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled || strings.TrimSpace(c.browser.CDPURL) == "" {
		return nil, &HTTPError{Status: http.StatusServiceUnavailable, What: "Doubao requires browser.enabled and a running configured Chrome"}
	}
	base, err := baseURL(c.source, defaultDoubaoBaseURL)
	if err != nil {
		return nil, err
	}
	cookie, custom, err := doubaoCookie(c.source)
	if err != nil {
		return nil, err
	}
	if err := c.doubaoGate.Lock(parent); err != nil {
		return nil, err
	}
	defer c.doubaoGate.Unlock()

	allocator, stopAllocator := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	defer stopAllocator()
	var tab context.Context
	var stopTab context.CancelFunc
	if custom {
		tab, stopTab = chromedp.NewContext(allocator, chromedp.WithNewBrowserContext())
	} else {
		tab, stopTab = chromedp.NewContext(allocator)
	}
	defer stopTab()
	turnBase, stopTurn := context.WithCancel(tab)
	defer stopTurn()
	stopParent := context.AfterFunc(parent, stopTurn)
	defer stopParent()
	turn, stopDeadline := context.WithTimeout(turnBase, doubaoTurnTimeout)
	defer stopDeadline()
	if err := chromedp.Run(turn); err != nil {
		return nil, errors.New("china next web adapter: cannot connect to configured Doubao browser")
	}
	setup, cancelSetup := context.WithTimeout(turn, doubaoSetupTimeout)
	defer cancelSetup()
	if custom {
		if err := setDoubaoCookies(setup, base, cookie); err != nil {
			return nil, err
		}
	}
	if err := chromedp.Run(setup, chromedp.Navigate(strings.TrimRight(base, "/")+"/chat/")); err != nil {
		return nil, errors.New("china next web adapter: Doubao browser navigation failed")
	}

	args, _ := json.Marshal(map[string]any{
		"model":  input.Model,
		"prompt": contentText(input.Messages[0].Content),
		"target": strings.TrimRight(base, "/") + "/samantha/chat/completion",
		"limit":  maxDoubaoCapture,
	})
	var installed bool
	if err := chromedp.Run(setup, chromedp.Evaluate(doubaoCaptureScript(string(args)), &installed)); err != nil || !installed {
		return nil, errors.New("china next web adapter: Doubao browser capture could not start")
	}
	if err := doubaoSelectModel(setup, string(args)); err != nil {
		return nil, err
	}
	var filled bool
	if err := chromedp.Run(setup, chromedp.Evaluate(doubaoFillScript(string(args)), &filled)); err != nil || !filled {
		return nil, &HTTPError{Status: http.StatusUnprocessableEntity, What: "Doubao composer is unavailable"}
	}
	var sent bool
	if err := chromedp.Run(setup, chromedp.Evaluate(doubaoSendScript, &sent)); err != nil || !sent {
		return nil, &HTTPError{Status: http.StatusUnprocessableEntity, What: "Doubao send control is unavailable"}
	}

	raw, err := doubaoCapture(turn, turn, maxDoubaoCapture)
	if err != nil {
		return nil, err
	}
	return doubaoResponse(parent, input.Model, stream, raw)
}

func decodeDoubaoRequest(body []byte, model string) (chatRequest, error) {
	if len(body) == 0 || len(body) > maxDoubaoInput {
		return chatRequest{}, fmt.Errorf("%w: Doubao request exceeds byte limit", ErrUnsupported)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return chatRequest{}, fmt.Errorf("%w: Doubao request must be a JSON object", ErrUnsupported)
	}
	for key := range fields {
		if key != "model" && key != "messages" && key != "stream" {
			return chatRequest{}, fmt.Errorf("%w: Doubao does not support request field %q", ErrUnsupported, key)
		}
	}
	request, err := decodeChatRequest(body)
	if err != nil {
		return request, err
	}
	request.Model = model
	return request, nil
}

func doubaoCookie(source config.Source) (string, bool, error) {
	if strings.TrimSpace(source.KeyEnv) == "" {
		return "", false, nil
	}
	raw := strings.TrimSpace(source.CredentialValue())
	if raw == "" || len(raw) > maxDoubaoCookie || strings.ContainsAny(raw, "\r\n\x00") {
		return "", true, ErrCredential
	}
	if strings.HasPrefix(raw, "{") {
		var value struct {
			Cookie string `json:"cookie"`
		}
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&value) != nil || strings.TrimSpace(value.Cookie) == "" || dec.Decode(new(any)) != io.EOF {
			return "", true, ErrCredential
		}
		raw = strings.TrimSpace(value.Cookie)
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "Cookie:"))
	parts := strings.Split(raw, ";")
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 || strings.TrimSpace(pair[0]) == "" || strings.ContainsAny(pair[0], " \t\r\n;,") || strings.ContainsAny(pair[1], "\r\n\x00") {
			return "", true, ErrCredential
		}
		clean = append(clean, strings.TrimSpace(pair[0])+"="+strings.TrimSpace(pair[1]))
	}
	if len(clean) == 0 {
		return "", true, ErrCredential
	}
	return strings.Join(clean, "; "), true, nil
}

func setDoubaoCookies(ctx context.Context, base, cookie string) error {
	u, err := baseURL(config.Source{BaseURL: base}, defaultDoubaoBaseURL)
	if err != nil {
		return err
	}
	host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if slash := strings.IndexByte(host, '/'); slash >= 0 {
		host = host[:slash]
	}
	if colon := strings.LastIndexByte(host, ':'); colon >= 0 {
		host = host[:colon]
	}
	secure := strings.HasPrefix(strings.ToLower(u), "https://")
	for _, part := range strings.Split(cookie, ";") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			return ErrCredential
		}
		action := cdpNetwork.SetCookie(pair[0], pair[1]).WithDomain(host).WithPath("/").WithSecure(secure)
		if err := chromedp.Run(ctx, action); err != nil {
			return errors.New("china next web adapter: cannot set Doubao browser cookie")
		}
	}
	return nil
}

func doubaoSelectModel(ctx context.Context, args string) error {
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(doubaoPollInterval)
	defer ticker.Stop()
	for {
		var state string
		if err := chromedp.Run(ctx, chromedp.Evaluate(doubaoSelectScript(args), &state)); err != nil {
			return errors.New("china next web adapter: Doubao model selector failed")
		}
		if state == "selected" {
			return nil
		}
		// The page can still be hydrating its model picker after navigation.
		// Keep polling through a transient `missing` result so a late-rendered
		// exact option is not mistaken for an unsupported model.
		select {
		case <-ctx.Done():
			return errors.New("china next web adapter: Doubao model selection timed out")
		case <-deadline.C:
			return &HTTPError{Status: http.StatusUnprocessableEntity, What: "requested Doubao UI model is unavailable"}
		case <-ticker.C:
		}
	}
}

func doubaoCapture(ctx, browserCtx context.Context, limit int) ([]byte, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var result struct {
			Chunk    string `json:"chunk"`
			Error    string `json:"error"`
			Done     bool   `json:"done"`
			Captured bool   `json:"captured"`
		}
		err := chromedp.Run(browserCtx, chromedp.Evaluate(doubaoReadScript, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
		if err != nil {
			return nil, errors.New("china next web adapter: Doubao browser connection failed")
		}
		if result.Error != "" {
			return nil, errors.New("china next web adapter: Doubao browser request failed")
		}
		if result.Chunk != "" {
			if len(result.Chunk) > limit {
				return nil, errors.New("china next web adapter: Doubao browser response exceeds byte limit")
			}
			// Chunks are returned one at a time and are bounded in the page;
			// preserve them in a separate builder in the caller below.
			return collectDoubaoChunks(ctx, browserCtx, result, limit)
		}
		if result.Done {
			if !result.Captured {
				return nil, errors.New("china next web adapter: Doubao UI did not submit a completion")
			}
			return nil, errors.New("china next web adapter: Doubao completion returned no data")
		}
	}
}

func collectDoubaoChunks(ctx, browserCtx context.Context, first struct {
	Chunk    string `json:"chunk"`
	Error    string `json:"error"`
	Done     bool   `json:"done"`
	Captured bool   `json:"captured"`
}, limit int) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(first.Chunk)
	for out.Len() <= limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var result struct {
			Chunk    string `json:"chunk"`
			Error    string `json:"error"`
			Done     bool   `json:"done"`
			Captured bool   `json:"captured"`
		}
		if err := chromedp.Run(browserCtx, chromedp.Evaluate(doubaoReadScript, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			return nil, errors.New("china next web adapter: Doubao browser connection failed")
		}
		if result.Error != "" {
			return nil, errors.New("china next web adapter: Doubao browser request failed")
		}
		if result.Chunk != "" {
			if out.Len()+len(result.Chunk) > limit {
				return nil, errors.New("china next web adapter: Doubao browser response exceeds byte limit")
			}
			out.WriteString(result.Chunk)
			continue
		}
		if result.Done {
			if !result.Captured || out.Len() == 0 {
				return nil, errors.New("china next web adapter: Doubao completion returned no data")
			}
			return out.Bytes(), nil
		}
	}
	return nil, errors.New("china next web adapter: Doubao browser response exceeds byte limit")
}

func doubaoResponse(ctx context.Context, model string, stream bool, raw []byte) (*http.Response, error) {
	var converted bytes.Buffer
	state := newCompletionState(AdapterDoubao, model, nil, &converted, stream)
	if err := parseSSE(ctx, bytes.NewReader(raw), state); err != nil {
		return nil, err
	}
	if strings.TrimSpace(state.content) == "" {
		return nil, errors.New("china next web adapter: Doubao completed without answer text")
	}
	if stream {
		if converted.Len() > maxDoubaoCapture {
			return nil, errors.New("china next web adapter: converted Doubao response exceeds byte limit")
		}
		header := make(http.Header)
		header.Set("Content-Type", "text/event-stream")
		header.Set("X-COT-Delivery", "buffered")
		header.Set("Content-Length", fmt.Sprintf("%d", converted.Len()))
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(bytes.NewReader(converted.Bytes())), ContentLength: int64(converted.Len())}, nil
	}
	result, err := state.nonStreamJSON()
	if err != nil {
		return nil, err
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("Content-Length", fmt.Sprintf("%d", len(result)))
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: header, Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}, nil
}

func doubaoSelectScript(args string) string {
	return `(()=>{const a=` + args + `;const visible=e=>{if(!e)return false;const r=e.getBoundingClientRect();return !!(r.width||r.height)&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none';};const text=e=>(e?.textContent||'').trim();const marker=e=>(e.id||'')+' '+(e.className||'')+' '+(e.getAttribute('data-testid')||'');const nodes=[...document.querySelectorAll('button,[role="button"],[role="option"],[role="combobox"],[aria-haspopup="listbox"],[data-testid],[class*="model" i]')].filter(visible);const exact=nodes.filter(e=>text(e)===a.model);const current=exact.find(e=>e.getAttribute('aria-selected')==='true'||e.getAttribute('role')==='combobox'||e.getAttribute('aria-haspopup')==='listbox'||(e.getAttribute('role')!=='option'&&!/option|item/i.test(marker(e))&&!e.getAttribute('aria-expanded')&&/model/i.test(marker(e))));if(current)return 'selected';const option=exact.find(e=>e.getAttribute('role')==='option'||/option|item/i.test(e.className||''))||exact[exact.length-1];if(option){option.click();return 'clicked';}const opener=nodes.find(e=>e.getAttribute('aria-haspopup')==='listbox'||/model/i.test(marker(e)));if(opener&&opener.getAttribute('aria-expanded')!=='true'){opener.click();return 'opened';}return 'waiting';})()`
}

func doubaoFillScript(args string) string {
	return `(()=>{const a=` + args + `;const visible=e=>{if(!e)return false;const r=e.getBoundingClientRect();return !!(r.width||r.height)&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none';};const all=[...document.querySelectorAll('textarea,[contenteditable="true"],[role="textbox"],[data-testid*="input" i],[data-testid*="composer" i]')].filter(visible);const e=all.find(x=>/message|chat|composer|输入|消息/i.test((x.getAttribute('placeholder')||'')+' '+(x.getAttribute('data-testid')||'')))||all[0];if(!e)return false;if(e instanceof HTMLTextAreaElement||e instanceof HTMLInputElement){const d=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value');if(d?.set)d.set.call(e,a.prompt);else e.value=a.prompt;}else{e.textContent=a.prompt;}e.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:a.prompt}));e.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`
}

const doubaoSendScript = `(()=>{const visible=e=>{if(!e)return false;const r=e.getBoundingClientRect();return !!(r.width||r.height)&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none';};const nodes=[...document.querySelectorAll('button,[role="button"],[data-testid],[aria-label]')].filter(visible).filter(e=>!e.disabled);const b=nodes.find(e=>/send|发送|提交|chat-input-send/i.test((e.getAttribute('aria-label')||'')+' '+(e.getAttribute('data-testid')||'')+' '+(e.textContent||'')))||nodes.find(e=>e.getAttribute('type')==='submit');if(!b)return false;b.click();return true;})()`

const doubaoReadScript = `(async()=>{const s=window.__cotDoubao;if(!s)return {error:'missing_state'};if(!s.error&&!s.done&&!s.queue.length){await new Promise(resolve=>{let settled=false;const finish=()=>{if(settled)return;settled=true;clearTimeout(timer);s.notify=null;resolve();};const timer=setTimeout(finish,500);s.notify=finish;});}if(s.error)return {error:s.error,captured:s.captured,done:true};const chunk=s.queue.shift();if(chunk){s.bytes-=new TextEncoder().encode(chunk).length;return {chunk,captured:s.captured,done:false};}return {captured:s.captured,done:s.done};})()`

func doubaoCaptureScript(args string) string {
	return `(()=>{const a=` + args + `;const s={queue:[],bytes:0,done:false,error:'',captured:false,notify:null};const enc=new TextEncoder();const signal=()=>{if(s.notify){const f=s.notify;s.notify=null;f();}};const push=v=>{if(!v)return true;const n=enc.encode(v).length;if(n>1048576||s.bytes+n>a.limit||s.queue.length>=512){s.error='queue_limit';s.done=true;signal();return false;}s.queue.push(v);s.bytes+=n;signal();return true;};const match=u=>{try{return new URL(u,location.href).pathname===new URL(a.target,location.href).pathname}catch{return false;}};const read=async body=>{try{const reader=body?.getReader();if(!reader){s.error='missing_body';s.done=true;signal();return;}const d=new TextDecoder();for(;;){const x=await reader.read();if(x.done)break;if(!push(d.decode(x.value,{stream:true})))return;}push(d.decode());}catch{s.error='read_failed';}finally{s.done=true;signal();}};const fetch0=window.fetch;window.fetch=async function(...x){const r=await fetch0.apply(this,x);const u=x[0] instanceof Request?x[0].url:x[0];if(match(u)&&!s.captured){s.captured=true;if(!r.ok){s.error='http_'+r.status;s.done=true;signal();return r;}read(r.clone().body);}return r;};const open0=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,u,...rest){this.__cotDoubaoTarget=match(u);return open0.call(this,method,u,...rest);};const send0=XMLHttpRequest.prototype.send;XMLHttpRequest.prototype.send=function(...x){if(this.__cotDoubaoTarget&&!s.captured){s.captured=true;let sent=0;const copy=()=>{try{const v=this.responseText||'';if(v.length<sent){s.error='xhr_rewind';return false;}const part=v.slice(sent);sent=v.length;return push(part);}catch{s.error='xhr_read_failed';return false;}};this.addEventListener('progress',copy);this.addEventListener('loadend',()=>{copy();if(this.status<200||this.status>=300)s.error='http_'+this.status;s.done=true;signal();});}return send0.apply(this,x);};window.__cotDoubao=s;return true;})()`
}
