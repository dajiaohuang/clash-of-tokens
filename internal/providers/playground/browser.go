package playground

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type browserTransport struct {
	ctx   context.Context
	close func()
	once  sync.Once
}

func (t *browserTransport) Close() error { t.once.Do(t.close); return nil }

func (c *Client) startBrowser(ctx context.Context, id string, payload any) (frameTransport, error) {
	allocator, acancel := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	tab, tcancel := chromedp.NewContext(allocator)
	turn, cancel := context.WithCancel(tab)
	stop := context.AfterFunc(ctx, cancel)
	transport := &browserTransport{ctx: turn, close: func() { stop(); cancel(); tcancel(); acancel() }}
	fail := func() (frameTransport, error) {
		transport.Close()
		return nil, &Error{503, "cannot open playground in configured Chrome"}
	}
	if err := chromedp.Run(turn); err != nil {
		return fail()
	}
	setup, setupCancel := context.WithTimeout(turn, 30*time.Second)
	defer setupCancel()
	if err := chromedp.Run(setup, chromedp.Navigate(origin)); err != nil {
		return fail()
	}
	args, _ := json.Marshal(map[string]any{"id": id, "payload": payload})
	script := "(()=>{const args=" + string(args) + ";" + openSocket + "})()"
	var ready bool
	if err := chromedp.Run(setup, chromedp.Evaluate(script, &ready)); err != nil || !ready {
		return fail()
	}
	return transport, nil
}

func (t *browserTransport) Next(ctx context.Context) (string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var result struct {
			Frame  string `json:"frame"`
			Error  string `json:"error"`
			Closed bool   `json:"closed"`
		}
		err := chromedp.Run(t.ctx, chromedp.Evaluate(readSocket, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
		if err != nil {
			return "", errors.New("playground browser connection failed")
		}
		if result.Error != "" {
			return "", errors.New("playground WebSocket failed or exceeded queue limit")
		}
		if result.Frame != "" {
			return result.Frame, nil
		}
		if result.Closed {
			return "", io.EOF
		}
	}
}

// The socket is confined to a new tab on the public playground origin.
// It uses the installed browser's normal transport; no TLS fingerprint
// fabrication, CAPTCHA solver or challenge response generator is involved.
const openSocket = `
if(location.origin!=='https://playground.ai.cloudflare.com') return false;
const state={queue:[],bytes:0,error:'',closed:false,notify:null};
const encoder=new TextEncoder();
const room='playground-'+crypto.randomUUID().replaceAll('-','').slice(0,25);
const socket=new WebSocket('wss://playground.ai.cloudflare.com/agents/playground/'+room+'?_pk='+crypto.randomUUID());
state.socket=socket;
const signal=()=>{if(state.notify){const f=state.notify;state.notify=null;f();}};
socket.onopen=()=>{
 socket.send(JSON.stringify({type:'cf_agent_stream_resume_request'}));
 socket.send(JSON.stringify({type:'rpc',id:'cot-config',method:'setConfig',args:[{model:args.payload.model,temperature:args.payload.temperature,stream:true}]}));
 socket.send(JSON.stringify({id:args.id,init:{method:'POST',body:JSON.stringify({messages:args.payload.messages,trigger:'submit-message'})},type:'cf_agent_use_chat_request'}));
};
socket.onmessage=e=>{
 if(typeof e.data!=='string'){state.error='non_text_frame';socket.close();signal();return;}
 const n=encoder.encode(e.data).length;
 if(n>1048576||state.bytes+n>4194304||state.queue.length>=512){state.error='queue_limit';socket.close();signal();return;}
 state.queue.push({raw:e.data,bytes:n});state.bytes+=n;signal();
};
socket.onerror=()=>{state.error='socket_error';signal();};
socket.onclose=()=>{state.closed=true;signal();};
window.__cotPlayground=state;
return true;
`

const readSocket = `(async()=>{
 const s=window.__cotPlayground;
 if(!s)return {error:'missing_socket'};
 if(!s.error&&!s.closed&&!s.queue.length){await new Promise(resolve=>{let settled=false;const finish=()=>{if(settled)return;settled=true;clearTimeout(timer);resolve();};const timer=setTimeout(finish,500);s.notify=finish;});}
 if(s.error)return {error:s.error};
 const entry=s.queue.shift();if(entry){s.bytes-=entry.bytes;return {frame:entry.raw};}
 return {closed:s.closed};
})()`
