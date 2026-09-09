package majorweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// doGeminiWeb drives the signed-in Gemini Web page. Gemini's StreamGenerate
// response is captured from the page's own request rather than recreated with
// a private RPC client. This keeps Google session state in the user's Chrome
// profile and avoids copying cookies or browser fingerprints into the gateway.
func (c *Client) doGeminiWeb(parent context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" || model != "web" || len(body) > 64<<10 {
		return nil, &requestError{"Gemini Web requires model web, chat protocol, and one user text"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	if !c.browser.Enabled || strings.TrimSpace(c.browser.CDPURL) == "" {
		return nil, &HTTPError{Status: http.StatusServiceUnavailable, What: "Gemini Web requires configured running Chrome"}
	}
	base, err := baseURL(c.source, defaultGeminiBase)
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

	phase := "navigation"
	failure := func() (*http.Response, error) {
		if parent.Err() != nil {
			return nil, fmt.Errorf("Gemini Web %s: %w", phase, parent.Err())
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("Gemini Web %s: %w", phase, ctx.Err())
		}
		return nil, errors.New("Gemini Web browser turn failed")
	}

	if err := chromedp.Run(ctx,
		chromedp.Navigate(strings.TrimRight(base, "/")+"/app"),
		chromedp.WaitVisible(".ql-editor, [contenteditable='true']", chromedp.ByQuery),
	); err != nil {
		return failure()
	}
	var composerState string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(()=>{const all=[...document.querySelectorAll('.ql-editor,[contenteditable="true"]')];const e=all.find(x=>{const r=x.getBoundingClientRect();return (r.width||r.height)&&getComputedStyle(x).visibility!=='hidden'&&getComputedStyle(x).display!=='none'});if(!e)return 'missing';return String(e.value??e.textContent??'').trim()===''?'empty':'nonempty'})()`, &composerState)); err != nil {
		return failure()
	}
	if composerState == "nonempty" {
		return nil, &requestError{"Gemini Web composer already contains draft text"}
	}
	if composerState != "empty" {
		return failure()
	}

	args, _ := json.Marshal(map[string]string{"marker": "StreamGenerate"})
	phase = "capture installation"
	var installed bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(geminiCaptureScript(string(args)), &installed)); err != nil || !installed {
		return failure()
	}

	phase = "composer"
	promptJSON, _ := json.Marshal(input.Prompt)
	var sent bool
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(`(()=>{const prompt=`+string(promptJSON)+`;const all=[...document.querySelectorAll('.ql-editor,[contenteditable="true"]')];const e=all.find(x=>{const r=x.getBoundingClientRect();return (r.width||r.height)&&getComputedStyle(x).visibility!=='hidden'&&getComputedStyle(x).display!=='none'});if(!e)return false;e.textContent=prompt;e.focus();e.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:prompt}));e.dispatchEvent(new Event('change',{bubbles:true}));e.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',code:'Enter',keyCode:13,which:13,bubbles:true,cancelable:true}));return true})()`, &sent),
	); err != nil {
		return failure()
	}
	if !sent {
		return failure()
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
		read := `(()=>{const s=window.__cotGemini;if(!s)return {done:false,error:true,data:''};return {done:s.done,error:s.error,data:s.done&&!s.error?s.data:''}})()`
		if err := chromedp.Run(ctx, chromedp.Evaluate(read, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
			return failure()
		}
		if result.Error {
			return nil, errors.New("Gemini Web captured request failed")
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

	answer, err := parseGeminiStreamResponse([]byte(raw))
	if err != nil {
		return nil, err
	}
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	if strings.TrimSpace(input.Model) == "" {
		input.Model = "gemini-web"
	}
	var result []byte
	if stream {
		result = chatChunk(id, input.Model, created, map[string]any{"role": "assistant"}, nil)
		result = append(result, chatChunk(id, input.Model, created, map[string]any{"content": answer}, nil)...)
		result = append(result, chatChunk(id, input.Model, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, input.Model, answer, "", created)
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

// geminiCaptureScript captures only the browser request whose URL includes
// StreamGenerate. The page owns the request and authentication, while the
// bounded clone lets the adapter parse the response after the page completes.
func geminiCaptureScript(args string) string {
	return `(()=>{const a=` + args + `;const s={done:false,error:false,data:'',captured:false};window.__cotGemini=s;const limit=8388608;
 const match=u=>{try{const p=new URL(u,location.href);return p.origin===location.origin&&p.pathname.includes(a.marker)}catch{return false}};
 const finish=(ok,data)=>{if(s.done)return;s.error=!ok;s.data=ok?data:'';s.done=true};
 const fetch0=window.fetch;window.fetch=async function(...x){const r=await fetch0.apply(this,x);const u=x[0] instanceof Request?x[0].url:x[0];if(match(u)&&!s.captured){s.captured=true;if(!r.ok){finish(false,'');return r}const copy=r.clone();(async()=>{try{if(copy.body&&copy.body.getReader){const reader=copy.body.getReader();const d=new TextDecoder();let n=0;let text='';for(;;){const v=await reader.read();if(v.done)break;n+=v.value.byteLength;if(n>limit){await reader.cancel();finish(false,'');return}text+=d.decode(v.value,{stream:true})}text+=d.decode();finish(true,text)}else{const text=await copy.text();finish(text.length<=limit,text.length<=limit?text:'')}}catch{finish(false,'')}})();}return r};
 const open0=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,u,...rest){this.__cotTarget=match(u);return open0.call(this,method,u,...rest)};
 const send0=XMLHttpRequest.prototype.send;XMLHttpRequest.prototype.send=function(...x){if(this.__cotTarget&&!s.captured){s.captured=true;this.addEventListener('progress',()=>{try{const text=this.responseText||'';if(new TextEncoder().encode(text).length>limit){this.abort();finish(false,'')}}catch{this.abort();finish(false)}});this.addEventListener('loadend',()=>{try{const text=this.responseText||'';finish(this.status>=200&&this.status<300&&new TextEncoder().encode(text).length<=limit,text)}catch{finish(false,'')}})}return send0.apply(this,x)};return true;})()`
}

// parseGeminiStreamResponse decodes the length-prefixed batchexecute body
// returned by Gemini Web. Each wrb.fr payload is a cumulative snapshot, so
// the final snapshot is returned; an empty final snapshot is an error.
func parseGeminiStreamResponse(raw []byte) (string, error) {
	frames, err := parseGeminiFrames(raw)
	if err != nil {
		return "", err
	}
	last := ""
	sawSnapshot := false
	for _, outer := range frames {
		for _, rawRecord := range outer {
			var record []json.RawMessage
			if json.Unmarshal(rawRecord, &record) != nil || len(record) < 3 {
				return "", errors.New("Gemini Web invalid StreamGenerate record")
			}
			var name string
			if json.Unmarshal(record[0], &name) != nil || name == "" {
				return "", errors.New("Gemini Web invalid StreamGenerate record name")
			}
			if name == "er" || name == "error" || name == "rpc_error" {
				return "", errors.New("Gemini Web upstream returned an error record")
			}
			if name != "wrb.fr" {
				continue
			}
			var payload string
			if json.Unmarshal(record[2], &payload) != nil {
				return "", errors.New("Gemini Web invalid StreamGenerate payload")
			}
			text, err := parseGeminiRecordPayload(payload)
			if err != nil {
				return "", err
			}
			sawSnapshot = true
			last = text
		}
	}
	if !sawSnapshot || strings.TrimSpace(last) == "" {
		return "", errors.New("Gemini Web completed without answer")
	}
	return last, nil
}

func parseGeminiFrames(raw []byte) ([][]json.RawMessage, error) {
	if len(raw) > 8<<20 {
		return nil, errors.New("Gemini Web response exceeds limit")
	}
	prefix := []byte(")]}'")
	if !bytes.HasPrefix(raw, prefix) {
		return nil, errors.New("Gemini Web response is missing safety prefix")
	}
	pos := len(prefix)
	if pos < len(raw) && raw[pos] == '\r' {
		pos++
	}
	if pos >= len(raw) || raw[pos] != '\n' {
		return nil, errors.New("Gemini Web response has invalid safety prefix")
	}
	pos++
	frames := make([][]json.RawMessage, 0, 4)
	for {
		for pos < len(raw) && (raw[pos] == '\n' || raw[pos] == '\r') {
			pos++
		}
		if pos == len(raw) {
			break
		}
		lineEnd := bytes.IndexByte(raw[pos:], '\n')
		if lineEnd < 0 {
			return nil, errors.New("Gemini Web response has truncated length prefix")
		}
		lineEnd += pos
		lengthText := strings.TrimSpace(string(raw[pos:lineEnd]))
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 || !isDecimalLine(lengthText) {
			return nil, errors.New("Gemini Web response has invalid frame length")
		}
		pos = lineEnd + 1
		if length > len(raw)-pos {
			return nil, errors.New("Gemini Web response frame is truncated")
		}
		payload := raw[pos : pos+length]
		if !bytes.Equal(payload, bytes.TrimSpace(payload)) || !json.Valid(payload) {
			return nil, errors.New("Gemini Web response frame is not valid JSON")
		}
		var outer []json.RawMessage
		if json.Unmarshal(payload, &outer) != nil || len(outer) == 0 {
			return nil, errors.New("Gemini Web response frame is not an array")
		}
		frames = append(frames, outer)
		pos += length
		if pos == len(raw) {
			break
		}
		if raw[pos] == '\r' {
			pos++
		}
		if pos >= len(raw) || raw[pos] != '\n' {
			return nil, errors.New("Gemini Web response frame delimiter is missing")
		}
		pos++
	}
	if len(frames) == 0 {
		return nil, errors.New("Gemini Web response contains no frames")
	}
	return frames, nil
}

func parseGeminiRecordPayload(payload string) (string, error) {
	var inner []json.RawMessage
	if json.Unmarshal([]byte(payload), &inner) != nil || len(inner) <= 4 {
		return "", errors.New("Gemini Web invalid StreamGenerate inner payload")
	}
	var fourth []json.RawMessage
	if json.Unmarshal(inner[4], &fourth) != nil || len(fourth) == 0 {
		return "", errors.New("Gemini Web invalid StreamGenerate text envelope")
	}
	var first []json.RawMessage
	if json.Unmarshal(fourth[0], &first) != nil || len(first) <= 1 {
		return "", errors.New("Gemini Web invalid StreamGenerate text record")
	}
	var chunks []json.RawMessage
	if json.Unmarshal(first[1], &chunks) != nil {
		return "", errors.New("Gemini Web invalid StreamGenerate text chunks")
	}
	var text strings.Builder
	for _, chunk := range chunks {
		var part string
		if json.Unmarshal(chunk, &part) != nil {
			return "", errors.New("Gemini Web invalid StreamGenerate text chunk")
		}
		text.WriteString(part)
	}
	return text.String(), nil
}

func isDecimalLine(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
