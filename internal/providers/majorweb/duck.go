package majorweb

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

	"github.com/chromedp/chromedp"
)

func (c *Client) doDuck(parent context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" || model != "web" || len(body) > 64<<10 {
		return nil, &requestError{"Duck.ai browser mode accepts model web and one user text message"}
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled || c.browser.CDPURL == "" {
		return nil, &HTTPError{Status: 503, What: "Duck.ai requires configured running Chrome"}
	}
	base, err := baseURL(c.source, "https://duck.ai")
	if err != nil {
		return nil, err
	}
	a, stopA := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	defer stopA()
	tab, stopTab := chromedp.NewContext(a)
	defer stopTab()
	ctx, cancel := context.WithTimeout(tab, 120*time.Second)
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	phase := "navigation"
	fail := func() (*http.Response, error) {
		if parent.Err() != nil {
			return nil, fmt.Errorf("Duck.ai %s: %w", phase, parent.Err())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Duck.ai %s: %w", phase, ctx.Err())
		}
		return nil, fmt.Errorf("Duck.ai %s: browser turn failed; check page readiness", phase)
	}
	phase = "navigation"
	if chromedp.Run(ctx, chromedp.Navigate(base+"/chat"), chromedp.WaitVisible("textarea", chromedp.ByQuery)) != nil {
		return fail()
	}
	args, _ := json.Marshal(map[string]string{"prompt": in.Prompt, "target": base + "/duckchat/v1/chat"})
	var installed bool
	phase = "capture installation"
	if chromedp.Run(ctx, chromedp.Evaluate(duckCaptureScript(string(args)), &installed)) != nil || !installed {
		return fail()
	}
	var sent bool
	phase = "composer"
	fill := `(()=>{const a=` + string(args) + `;const e=document.querySelector('textarea');const b=document.querySelector('button[aria-label="Ask"]');if(!e||!b||e.value.trim()!=='')return false;Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(e,a.prompt);e.dispatchEvent(new Event('input',{bubbles:true}));return true})()`
	if chromedp.Run(ctx, chromedp.Evaluate(fill, &sent)) != nil || !sent {
		return fail()
	}
	if chromedp.Run(ctx, chromedp.Poll(`(()=>{const b=document.querySelector('button[aria-label=\"Ask\"]');return !!b&&!b.disabled})()`, nil, chromedp.WithPollingInterval(50*time.Millisecond), chromedp.WithPollingTimeout(5*time.Second))) != nil {
		return fail()
	}
	// React may enable submission on its next update after the native input event.
	var clicked bool
	if chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const b=document.querySelector('button[aria-label="Ask"]');if(!b||b.disabled)return false;b.click();return true})()`, &clicked)) != nil || !clicked {
		return fail()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	phase = "capture completion"
	var capture struct {
		Done  bool   `json:"done"`
		Error bool   `json:"error"`
		Data  string `json:"data"`
		Model string `json:"model"`
	}
	for {
		if chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const s=window.__cotDuck;return {done:s.done,error:s.error,data:s.done&&!s.error?s.data:'',model:s.model}})()`, &capture)) != nil {
			return fail()
		}
		if capture.Error {
			return nil, errors.New("Duck.ai captured request failed")
		}
		if capture.Done {
			break
		}
		select {
		case <-ctx.Done():
			return fail()
		case <-ticker.C:
		}
	}
	if capture.Model == "" || len(capture.Model) > 128 {
		return nil, errors.New("Duck.ai missing captured model identity")
	}
	answer, err := readDuck(strings.NewReader(capture.Data))
	if err != nil {
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var out []byte
	if stream {
		out = chatChunk(id, capture.Model, created, map[string]any{"role": "assistant", "content": answer}, nil)
		out = append(out, chatChunk(id, capture.Model, created, map[string]any{}, "stop")...)
		out = append(out, []byte("data: [DONE]\n\n")...)
	} else {
		out = chatCompletion(id, capture.Model, answer, "", created)
	}
	r := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(out)), ContentLength: int64(len(out))}
	r.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		r.Header.Set("Content-Type", "text/event-stream")
	} else {
		r.Header.Set("Content-Type", "application/json")
	}
	return r, nil
}

func duckCaptureScript(args string) string {
	return `(()=>{const a=` + args + `;const s={done:false,error:false,data:'',model:'',captured:false};window.__cotDuck=s;const original=window.fetch;window.fetch=async function(...x){const req=x[0];const opt=x[1]||{};const u=req instanceof Request?req.url:req;let match=false;try{match=new URL(u,location.href).href===a.target&&String(opt.method||(req instanceof Request?req.method:'GET')).toUpperCase()==='POST'}catch{}if(match&&s.captured)match=false;let payload='';if(match&&!s.captured){s.captured=true;try{payload=opt.body!==undefined?opt.body:await req.clone().text();const p=JSON.parse(payload);s.model=typeof p.model==='string'?p.model:'';}catch{s.error=true}}let r;try{r=await original.apply(this,x)}catch(e){if(match){s.error=true;s.done=true}throw e}if(match){if(!r.ok){s.error=true;s.done=true;return r}const copy=r.clone();(async()=>{let n=0;try{const reader=copy.body.getReader();const decoder=new TextDecoder();for(;;){const v=await reader.read();if(v.done)break;n+=v.value.byteLength;if(n>4194304){s.error=true;await reader.cancel();break}s.data+=decoder.decode(v.value,{stream:true})}s.data+=decoder.decode();}catch{s.error=true}finally{s.done=true}})()}return r};return true})()`
}

func readDuck(r io.Reader) (string, error) {
	d := newSSEDecoder(r)
	var b strings.Builder
	total := 0
	for {
		_, data, ok, err := d.next()
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrTruncated
		}
		total += len(data)
		if total > 4<<20 {
			return "", errors.New("Duck.ai response exceeds limit")
		}
		if data == "[DONE]" {
			if strings.TrimSpace(b.String()) == "" {
				return "", errors.New("Duck.ai empty answer")
			}
			return b.String(), nil
		}
		var f map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &f) != nil {
			return "", errors.New("Duck.ai invalid event")
		}
		if raw, ok := f["error"]; ok && string(raw) != "null" && string(raw) != "false" {
			return "", errors.New("Duck.ai upstream error")
		}
		for _, key := range []string{"content", "message"} {
			if raw, ok := f[key]; ok {
				var s string
				if json.Unmarshal(raw, &s) != nil {
					return "", errors.New("Duck.ai invalid text")
				}
				b.WriteString(s)
				break
			}
		}
	}
}
