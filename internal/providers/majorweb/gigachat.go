package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	defaultGigaChatBase = "https://giga.chat"
	// The public portal currently links /gigachat, which redirects through an
	// anti-bot path in a plain request. This indexed app route returned the
	// actual GigaChat React application in the captured 0.9.4 build.
	defaultGigaChatPath  = "/055bfe55-33da-41ee-bf6e-26d57c3649d1/home"
	gigaChatCaptureLimit = 8 << 20
)

// doGigaChatWeb drives the signed-in GigaChat page. The browser owns login,
// cookies, and request headers; this adapter only captures the page's own
// same-origin session stream after the user submits one text turn.
func (c *Client) doGigaChatWeb(parent context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" || model != "web" || len(body) > 64<<10 {
		return nil, &requestError{"GigaChat Web requires model web, chat protocol, and one user text"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled || strings.TrimSpace(c.browser.CDPURL) == "" {
		return nil, &HTTPError{Status: http.StatusServiceUnavailable, What: "GigaChat Web requires configured running Chrome"}
	}
	base, err := baseURL(c.source, defaultGigaChatBase)
	if err != nil {
		return nil, err
	}

	allocator, stopAllocator := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	defer stopAllocator()
	tab, stopTab := chromedp.NewContext(allocator)
	defer stopTab()
	ctx, cancel := context.WithTimeout(tab, 120*time.Second)
	defer cancel()
	stopParent := context.AfterFunc(parent, cancel)
	defer stopParent()
	failure := func(phase string) (*http.Response, error) {
		if parent.Err() != nil {
			return nil, errors.New("GigaChat Web " + phase + ": " + parent.Err().Error())
		}
		if ctx.Err() != nil {
			return nil, errors.New("GigaChat Web " + phase + ": " + ctx.Err().Error())
		}
		return nil, errors.New("GigaChat Web " + phase + " failed")
	}

	page := gigaChatPageURL(base)
	if err := chromedp.Run(ctx,
		chromedp.Navigate(page),
		chromedp.WaitVisible("#chat-input-textarea", chromedp.ByQuery),
	); err != nil {
		return failure("navigation")
	}

	var composerState string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const e=document.getElementById('chat-input-textarea');if(!e)return 'missing';const r=e.getBoundingClientRect();if(!(r.width||r.height)||getComputedStyle(e).visibility==='hidden'||getComputedStyle(e).display==='none')return 'hidden';return String(e.value||'').trim()===''?'empty':'nonempty'})()`, &composerState)); err != nil {
		return failure("composer inspection")
	}
	if composerState == "nonempty" {
		return nil, &requestError{"GigaChat Web composer already contains draft text"}
	}
	if composerState != "empty" {
		return failure("composer inspection")
	}

	phase := "capture installation"
	args, _ := json.Marshal(map[string]string{"kind": "sessions"})
	var installed bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(gigaChatCaptureScript(string(args)), &installed)); err != nil || !installed {
		return failure(phase)
	}

	phase = "composer"
	promptJSON, _ := json.Marshal(input.Prompt)
	var filled bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(gigaChatFillScript(string(promptJSON)), &filled)); err != nil || !filled {
		return failure(phase)
	}
	// React commits the controlled textarea state after the input event. Give
	// it a short bounded turn before looking up the stable submit control.
	select {
	case <-ctx.Done():
		return failure(phase)
	case <-time.After(100 * time.Millisecond):
	}
	var sent bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(gigaChatSendScript, &sent)); err != nil || !sent {
		return failure(phase)
	}

	phase = "capture completion"
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var raw string
	for {
		var result struct {
			Done  bool   `json:"done"`
			Error bool   `json:"error"`
			Data  string `json:"data"`
		}
		read := `(()=>{const s=window.__cotGigaChat;if(!s)return {done:false,error:true,data:''};return {done:s.done,error:s.error,data:s.done&&!s.error?s.data:''}})()`
		if err := chromedp.Run(ctx, chromedp.Evaluate(read, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			return failure(phase)
		}
		if result.Error {
			return nil, errors.New("GigaChat Web captured session request failed")
		}
		if result.Done {
			raw = result.Data
			break
		}
		select {
		case <-ctx.Done():
			return failure(phase)
		case <-ticker.C:
		}
	}

	answer, err := parseGigaChatStreamResponse([]byte(raw))
	if err != nil {
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	responseModel := input.Model
	if strings.TrimSpace(responseModel) == "" {
		responseModel = "gigachat-web"
	}
	var result []byte
	if stream {
		result = chatChunk(id, responseModel, created, map[string]any{"role": "assistant"}, nil)
		result = append(result, chatChunk(id, responseModel, created, map[string]any{"content": answer}, nil)...)
		result = append(result, chatChunk(id, responseModel, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, responseModel, answer, "", created)
	}
	out := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}
	out.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		out.Header.Set("Content-Type", "text/event-stream")
	} else {
		out.Header.Set("Content-Type", "application/json")
	}
	return out, nil
}

func gigaChatPageURL(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Path != "" && u.Path != "/" {
		return base
	}
	return strings.TrimRight(base, "/") + defaultGigaChatPath
}

func gigaChatFillScript(prompt string) string {
	return `(()=>{const prompt=` + prompt + `;const e=document.getElementById('chat-input-textarea');if(!e)return false;const r=e.getBoundingClientRect();if(!(r.width||r.height)||getComputedStyle(e).visibility==='hidden'||getComputedStyle(e).display==='none'||e.disabled||e.readOnly)return false;const setter=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value')?.set;if(!setter)return false;setter.call(e,prompt);e.focus();e.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:prompt}));e.dispatchEvent(new Event('change',{bubbles:true}));return e.value===prompt})()`
}

const gigaChatSendScript = `(()=>{const e=document.getElementById('chat-input-textarea');if(!e)return false;const root=document.getElementById('chat-input:submit');const button=root?.querySelector('button');if(button&&!button.disabled){button.click();return true}const form=e.closest('form');if(!form)return false;const submit=form.querySelector('button[type="submit"]');if(submit&&!submit.disabled){submit.click();return true}return false})()`

// gigaChatCaptureScript captures the page request, including its complete SSE
// body. It accepts only the same-origin session request paths observed in the
// GigaChat web bundle and bounds the retained body before parsing.
func gigaChatCaptureScript(args string) string {
	return `(()=>{const a=` + args + `;const s={done:false,error:false,data:'',captured:false};window.__cotGigaChat=s;const limit=` + gigaChatCaptureLimitString() + `;const enc=new TextEncoder();
 const match=(u,m)=>{try{const p=new URL(u,location.href);return String(m||'GET').toUpperCase()==='POST'&&p.origin===location.origin&&/^\/api\/v0\/sessions(?:\/[^/]+)?\/request$/.test(p.pathname)}catch{return false}};
 const finish=(ok,data)=>{if(s.done)return;s.error=!ok;s.data=ok?data:'';s.done=true};
 const fetch0=window.fetch;window.fetch=async function(...x){const request=x[0] instanceof Request?x[0]:null;const u=request?request.url:x[0];const method=request?request.method:(x[1]?.method||'GET');const own=match(u,method)&&!s.captured;if(own)s.captured=true;let r;try{r=await fetch0.apply(this,x)}catch(e){if(own)finish(false,'');throw e}if(own){if(!r.ok){finish(false,'');return r}try{const copy=r.clone();(async()=>{try{if(copy.body&&copy.body.getReader){const reader=copy.body.getReader();const d=new TextDecoder();let n=0,text='';for(;;){const v=await reader.read();if(v.done)break;n+=v.value.byteLength;if(n>limit){await reader.cancel();finish(false,'');return}text+=d.decode(v.value,{stream:true})}text+=d.decode();finish(enc.encode(text).byteLength<=limit,text)}else{const text=await copy.text();const size=enc.encode(text).byteLength;finish(size<=limit,size<=limit?text:'')}}catch{finish(false,'')}})()}catch{finish(false,'')}}return r};
 const open0=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,u,...rest){this.__cotTarget=match(u,method);return open0.call(this,method,u,...rest)};
 const send0=XMLHttpRequest.prototype.send;XMLHttpRequest.prototype.send=function(...x){if(this.__cotTarget&&!s.captured){s.captured=true;this.addEventListener('progress',()=>{try{const text=this.responseText||'';if(enc.encode(text).byteLength>limit){this.abort();finish(false,'')}}catch{this.abort();finish(false)}});this.addEventListener('loadend',()=>{try{const text=this.responseText||'';const size=enc.encode(text).byteLength;finish(this.status>=200&&this.status<300&&size<=limit,size<=limit?text:'')}catch{finish(false,'')}});this.addEventListener('error',()=>finish(false,''));this.addEventListener('abort',()=>finish(false,''));this.addEventListener('timeout',()=>finish(false,''))}return send0.apply(this,x)};return true})()`
}

func gigaChatCaptureLimitString() string {
	return "8388608"
}

// parseGigaChatStreamResponse validates the web stream's lifecycle. A clean
// EOF before READY is an error, even if an earlier IN_PROGRESS delta exists.
func parseGigaChatStreamResponse(raw []byte) (string, error) {
	d := newSSEDecoder(bytes.NewReader(raw))
	accepted := false
	ready := false
	assistantID := ""
	sessionID := ""
	requestMessageID := ""
	answer := strings.Builder{}
	for {
		_, data, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		if strings.TrimSpace(data) == "" {
			return "", errors.New("GigaChat Web empty session event")
		}
		var event struct {
			Status  string `json:"status"`
			Session struct {
				ID string `json:"id"`
			} `json:"session"`
			SessionID        string          `json:"sessionId"`
			RequestMessageID string          `json:"requestMessageId"`
			Message          json.RawMessage `json:"message"`
			ContentDelta     json.RawMessage `json:"contentDelta"`
			Error            struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil || event.Status == "" {
			return "", errors.New("GigaChat Web invalid session event")
		}
		if ready {
			return "", errors.New("GigaChat Web event arrived after READY")
		}
		switch event.Status {
		case "ACCEPTED":
			if accepted || event.RequestMessageID == "" {
				return "", errors.New("GigaChat Web invalid ACCEPTED event")
			}
			sessionID = event.Session.ID
			if sessionID == "" {
				sessionID = event.SessionID
			}
			if sessionID == "" {
				return "", errors.New("GigaChat Web ACCEPTED event has no session")
			}
			id, _, err := gigaChatMessage(event.Message)
			if err != nil {
				return "", err
			}
			assistantID = id
			requestMessageID = event.RequestMessageID
			accepted = true
		case "IN_PROGRESS":
			if !accepted {
				return "", errors.New("GigaChat Web IN_PROGRESS event arrived before ACCEPTED")
			}
			if err := gigaChatCorrelates(event.Session.ID, event.SessionID, event.RequestMessageID, sessionID, requestMessageID); err != nil {
				return "", err
			}
			part, err := gigaChatDelta(event.ContentDelta)
			if err != nil {
				return "", err
			}
			answer.WriteString(part)
		case "READY":
			if !accepted || ready {
				return "", errors.New("GigaChat Web invalid READY event")
			}
			if err := gigaChatCorrelates(event.Session.ID, event.SessionID, event.RequestMessageID, sessionID, requestMessageID); err != nil {
				return "", err
			}
			id, text, err := gigaChatMessage(event.Message)
			if err != nil || assistantID != "" && id != "" && id != assistantID {
				return "", errors.New("GigaChat Web READY event has mismatched message")
			}
			if strings.TrimSpace(text) == "" {
				return "", errors.New("GigaChat Web completed without answer")
			}
			answer.Reset()
			answer.WriteString(text)
			ready = true
		case "ERROR":
			return "", errors.New("GigaChat Web upstream error")
		default:
			return "", errors.New("GigaChat Web unknown session status")
		}
	}
	if !ready {
		return "", ErrTruncated
	}
	return answer.String(), nil
}

func gigaChatMessage(raw json.RawMessage) (id, text string, err error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return "", "", errors.New("GigaChat Web session event has no message")
	}
	var message struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &message) != nil {
		return "", "", errors.New("GigaChat Web session event has invalid message")
	}
	return message.ID, message.Value, nil
}

func gigaChatCorrelates(eventSessionID, eventSessionID2, eventRequestID, sessionID, requestID string) error {
	if eventSessionID != "" && eventSessionID != sessionID || eventSessionID2 != "" && eventSessionID2 != sessionID {
		return errors.New("GigaChat Web session event has mismatched session")
	}
	if eventRequestID != "" && eventRequestID != requestID {
		return errors.New("GigaChat Web session event has mismatched request")
	}
	return nil
}

func gigaChatDelta(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var delta []struct {
		Role  string `json:"role"`
		Delta string `json:"delta"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &delta) != nil {
		return "", errors.New("GigaChat Web invalid contentDelta")
	}
	var answer strings.Builder
	for _, item := range delta {
		if item.Role != "ASSISTANT" && item.Role != "AI" {
			continue
		}
		if item.Delta != "" {
			answer.WriteString(item.Delta)
		} else {
			answer.WriteString(item.Value)
		}
	}
	return answer.String(), nil
}
