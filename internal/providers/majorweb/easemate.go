package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/chromedp/chromedp"
	"io"
	"net/http"
	"strings"
	"time"
)

func (c *Client) doEaseMate(parent context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 || strings.TrimSpace(model) == "" || len(model) > 128 {
		return nil, &requestError{"EaseMate requires a single user text and exact UI model label"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled || c.browser.CDPURL == "" {
		return nil, &HTTPError{Status: 503, What: "EaseMate requires configured running Chrome"}
	}
	base, err := baseURL(c.source, "https://www.easemate.ai")
	if err != nil {
		return nil, err
	}
	target := "https://api.easemate.ai/api2/stream/exec_operation"
	if strings.HasPrefix(base, "http://") {
		target = base + "/api2/stream/exec_operation"
	}
	allocator, stopAllocator := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	defer stopAllocator()
	tab, stopTab := chromedp.NewContext(allocator)
	defer stopTab()
	ctx, cancel := context.WithTimeout(tab, 120*time.Second)
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	phase := "navigation"
	failure := func() (*http.Response, error) {
		if parent.Err() != nil {
			return nil, fmt.Errorf("EaseMate %s: %w", phase, parent.Err())
		}
		return nil, errors.New("EaseMate browser turn failed")
	}
	if err := chromedp.Run(ctx, chromedp.Navigate(base+"/webapp/chat"), chromedp.WaitVisible("div.model-select-active span.text-sm", chromedp.ByQuery)); err != nil {
		return failure()
	}
	args, _ := json.Marshal(map[string]string{"model": model, "prompt": input.Prompt, "target": target})
	phase = "model selection"
	var state string
	script := `(()=>{const a=` + string(args) + `;const current=document.querySelector('div.model-select-active span.text-sm');if(!current)return 'missing';if(current.textContent.trim()===a.model)return 'selected';document.querySelector('div.model-select-active')?.click();return 'opened';})()`
	if chromedp.Run(ctx, chromedp.Evaluate(script, &state)) != nil {
		return failure()
	}
	if state != "selected" {
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for state != "selected" {
			choose := `(()=>{const a=` + string(args) + `;const current=document.querySelector('div.model-select-active span.text-sm');if(current?.textContent.trim()===a.model)return 'selected';const e=[...document.querySelectorAll('div.model-item span')].find(e=>e.textContent.trim()===a.model);if(e){e.click();return 'clicked'}return 'missing';})()`
			if chromedp.Run(ctx, chromedp.Evaluate(choose, &state)) != nil {
				return failure()
			}
			if state == "selected" {
				break
			}
			select {
			case <-ctx.Done():
				return failure()
			case <-deadline.C:
				return nil, &requestError{"EaseMate requested UI model is unavailable"}
			case <-ticker.C:
			}
		}
	}
	var installed bool
	phase = "capture installation"
	if chromedp.Run(ctx, chromedp.Evaluate(easeCaptureScript(string(args)), &installed)) != nil || !installed {
		return failure()
	}
	// Set the native textarea value then dispatch input for the site's React state.
	var filled bool
	phase = "composer"
	fill := `(()=>{const a=` + string(args) + `;const e=document.querySelector('textarea[placeholder="Ask me anything…"]');if(!e)return false;Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,a.prompt);e.dispatchEvent(new Event('input',{bubbles:true}));e.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`
	if chromedp.Run(ctx, chromedp.Evaluate(fill, &filled)) != nil || !filled {
		return failure()
	}
	var sent bool
	if chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const b=document.querySelector('button.css-1wchz4a');if(!b||b.disabled)return false;b.click();return true})()`, &sent)) != nil || !sent {
		return failure()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	phase = "capture completion"
	defer ticker.Stop()
	var raw string
	for {
		var result struct {
			Done  bool   `json:"done"`
			Error bool   `json:"error"`
			Data  string `json:"data"`
		}
		if chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const s=window.__cotEase;return {done:s.done,error:s.error,data:s.done&&!s.error?s.data:''}})()`, &result)) != nil {
			return failure()
		}
		if result.Error {
			return nil, errors.New("EaseMate captured request failed")
		}
		if result.Done {
			raw = result.Data
			break
		}
		select {
		case <-ctx.Done():
			return failure()
		case <-ticker.C:
		}
	}
	answer, err := easeAnswer(raw)
	if err != nil {
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var result []byte
	if stream {
		result = chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil)
		result = append(result, chatChunk(id, model, created, map[string]any{"content": answer}, nil)...)
		result = append(result, chatChunk(id, model, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, model, answer, "", created)
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

func easeCaptureScript(args string) string {
	return `(()=>{const a=` + args + `;const s={done:false,error:false,data:'',captured:false};window.__cotEase=s;const limit=4194304;
 const match=u=>{try{return new URL(u,location.href).href===a.target}catch{return false}};
 const fetch0=window.fetch;window.fetch=async function(...x){const r=await fetch0.apply(this,x);const u=x[0] instanceof Request?x[0].url:x[0];if(match(u)&&!s.captured){s.captured=true;if(!r.ok){s.error=true;s.done=true;return r}const copy=r.clone();(async()=>{try{const reader=copy.body.getReader();const d=new TextDecoder();let n=0;for(;;){const v=await reader.read();if(v.done)break;n+=v.value.byteLength;if(n>limit){s.error=true;await reader.cancel();break}s.data+=d.decode(v.value,{stream:true})}s.data+=d.decode();}catch{s.error=true}finally{s.done=true}})();}return r};
 const open0=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,u,...rest){this.__cotTarget=match(u);return open0.call(this,method,u,...rest)};
 const send0=XMLHttpRequest.prototype.send;XMLHttpRequest.prototype.send=function(...x){if(this.__cotTarget&&!s.captured){s.captured=true;this.addEventListener('progress',()=>{try{if(this.responseText.length>limit){s.error=true;s.done=true;this.abort()}}catch{s.error=true}});this.addEventListener('loadend',()=>{try{if(this.status<200||this.status>=300||this.responseText.length>limit)s.error=true;else s.data=this.responseText}catch{s.error=true}finally{s.done=true}});}return send0.apply(this,x)};return true;})()`
}

func easeAnswer(raw string) (string, error) {
	if len(raw) > 8<<20 {
		return "", errors.New("EaseMate response exceeds limit")
	}
	decoder := newSSEDecoder(strings.NewReader(raw))
	var answer strings.Builder
	for {
		_, data, ok, err := decoder.next()
		if err != nil {
			return "", err
		}
		if !ok {
			break
		}
		if data == "[DONE]" {
			break
		}
		var frame struct {
			Code int    `json:"code"`
			Data string `json:"data"`
		}
		if json.Unmarshal([]byte(data), &frame) != nil {
			return "", errors.New("EaseMate invalid SSE record")
		}
		if frame.Code != 200 {
			return "", errors.New("EaseMate upstream rejected turn")
		}
		var part struct {
			Answer string `json:"answer"`
		}
		if json.Unmarshal([]byte(frame.Data), &part) != nil {
			return "", errors.New("EaseMate invalid answer record")
		}
		answer.WriteString(part.Answer)
	}
	if strings.TrimSpace(answer.String()) == "" {
		return "", errors.New("EaseMate completed without answer")
	}
	return answer.String(), nil
}
