package chinaremaining

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	cdpNetwork "github.com/chromedp/cdproto/network"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// metasoBrowserStream uses the configured, already authenticated Chrome via
// CDP. The page performs the same-origin fetch so Metaso's browser checks and
// cookies remain in the browser session; no caller credential is copied into
// an unrelated browser profile.
func (c *Client) metasoBrowserStream(parent context.Context, base, token, meta, conversation, content, mode string) (*http.Response, error) {
	cdpURL := strings.TrimSpace(c.browser.CDPURL)
	if cdpURL == "" {
		return nil, errors.New("china remaining web adapter: Metaso browser CDP URL is empty")
	}
	baseURL, err := url.Parse(base)
	if err != nil || baseURL.Hostname() == "" {
		return nil, errors.New("china remaining web adapter: invalid Metaso browser origin")
	}
	values := url.Values{
		"sessionId": {conversation}, "question": {content}, "lang": {"zh"}, "mode": {mode},
		"url":       {base + "/search/" + url.PathEscape(conversation) + "?newSearch=true&q=" + url.QueryEscape(content)},
		"enableMix": {"true"}, "scholarSearchDomain": {"all"}, "expectedCurrentSessionSearchCount": {"1"},
		"is-mini-webview": {"0"}, "token": {meta},
	}
	endpoint := base + "/api/searchV2?" + values.Encode()
	allocator, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), cdpURL)
	// WithNewBrowserContext gives every request an isolated incognito context;
	// cookies set for one source account cannot bleed into another Client.
	tab, tabCancel := chromedp.NewContext(allocator, chromedp.WithNewBrowserContext())
	turn, turnCancel := context.WithCancel(tab)
	stopParent := context.AfterFunc(parent, turnCancel)
	cleanup := func() { stopParent(); turnCancel(); tabCancel(); allocatorCancel() }
	fail := func(e error) (*http.Response, error) { cleanup(); return nil, e }
	if err := chromedp.Run(turn); err != nil {
		return fail(errors.New("china remaining web adapter: cannot connect to Metaso browser"))
	}
	if err := chromedp.Run(turn, cdpNetwork.SetCookie("uid", strings.SplitN(token, "-", 2)[0]).WithDomain(baseURL.Hostname()).WithSecure(baseURL.Scheme == "https")); err != nil {
		return fail(errors.New("china remaining web adapter: cannot set Metaso uid cookie"))
	}
	if err := chromedp.Run(turn, cdpNetwork.SetCookie("sid", strings.SplitN(token, "-", 2)[1]).WithDomain(baseURL.Hostname()).WithSecure(baseURL.Scheme == "https")); err != nil {
		return fail(errors.New("china remaining web adapter: cannot set Metaso sid cookie"))
	}
	setup, setupCancel := context.WithTimeout(turn, 30*time.Second)
	defer setupCancel()
	if err := chromedp.Run(setup, chromedp.Navigate(base+"/")); err != nil {
		return fail(errors.New("china remaining web adapter: Metaso browser navigation failed"))
	}
	args, _ := json.Marshal(map[string]string{"url": endpoint})
	initScript := `(()=>{
 const a=` + string(args) + `; const s={queue:[],bytes:0,done:false,error:'',notify:null};
 window.__cotMetaso=s;
 (async()=>{try { const r=await fetch(a.url,{headers:{Accept:'text/event-stream'}}); if(!r.ok){s.error='http_'+r.status;s.done=true;if(s.notify)s.notify();return;}
  const reader=r.body?.getReader(); if(!reader){s.error='missing_body';s.done=true;if(s.notify)s.notify();return true;}
  const td=new TextDecoder(); for(;;){const x=await reader.read();if(x.done)break;const v=td.decode(x.value,{stream:true});const n=new TextEncoder().encode(v).length;if(n>1048576||s.bytes+n>4194304||s.queue.length>=512){s.error='queue_limit';break;}s.queue.push(v);s.bytes+=n;if(s.notify){const f=s.notify;s.notify=null;f();}}
  const tail=td.decode();if(tail){const n=new TextEncoder().encode(tail).length;if(n>1048576||s.bytes+n>4194304||s.queue.length>=512)s.error='queue_limit';else{s.queue.push(tail);s.bytes+=n;}}s.done=true;if(s.notify){const f=s.notify;s.notify=null;f();}
 } catch(e){s.error='fetch_failed';s.done=true;if(s.notify){const f=s.notify;s.notify=null;f();}}})(); return true;
})()`
	var ready bool
	if err := chromedp.Run(turn, chromedp.Evaluate(initScript, &ready, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil || !ready {
		return fail(errors.New("china remaining web adapter: Metaso browser fetch failed to start"))
	}
	pr, pw := io.Pipe()
	var closeOnce sync.Once
	closeBrowser := func() { closeOnce.Do(cleanup) }
	go func() {
		defer closeBrowser()
		defer pw.Close()
		for {
			if err := parent.Err(); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
			var result struct {
				Chunk string `json:"chunk"`
				Error string `json:"error"`
				Done  bool   `json:"done"`
			}
			readScript := `(async()=>{const s=window.__cotMetaso;if(!s)return {error:'missing_state',done:true};if(!s.error&&!s.done&&!s.queue.length){await new Promise(r=>{let x=false;const f=()=>{if(x)return;x=true;clearTimeout(t);r()};const t=setTimeout(f,500);s.notify=f;});}if(s.error)return {error:s.error,done:true};const chunk=s.queue.shift();if(chunk){s.bytes-=new TextEncoder().encode(chunk).length;return {chunk,done:false};}return {done:s.done};})()`
			if err := chromedp.Run(turn, chromedp.Evaluate(readScript, &result, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
				_ = pw.CloseWithError(errors.New("china remaining web adapter: Metaso browser connection failed"))
				return
			}
			if result.Error != "" {
				_ = pw.CloseWithError(errors.New("china remaining web adapter: Metaso stream failed"))
				return
			}
			if result.Chunk != "" {
				if _, err := io.WriteString(pw, result.Chunk); err != nil {
					return
				}
				continue
			}
			if result.Done {
				return
			}
		}
	}()
	resp := &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &metasoBrowserBody{ReadCloser: pr, close: closeBrowser}}
	return resp, nil
}

type metasoBrowserBody struct {
	io.ReadCloser
	close func()
	once  sync.Once
}

func (b *metasoBrowserBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.close)
	}
	return n, err
}
func (b *metasoBrowserBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.close)
	return err
}
