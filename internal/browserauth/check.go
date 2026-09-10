// Package browserauth performs bounded, read-only authentication checks in
// gateway-owned browser tabs. Account identity and tokens stay inside the page.
package browserauth

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type Evidence struct {
	Status             string    `json:"status"`
	Method             string    `json:"method"`
	CheckedAt          time.Time `json:"checked_at"`
	ComposerReady      bool      `json:"composer_ready"`
	GenerationVerified bool      `json:"generation_verified"`
	UpstreamStatus     int       `json:"upstream_status,omitempty"`
}

type specification struct{ path, predicate, composer string }

func spec(adapter string) (specification, bool) {
	switch adapter {
	case "claude-web":
		return specification{"/api/organizations", `Array.isArray(data)&&data.some(x=>typeof x?.uuid==='string'&&x.uuid.trim())?'authenticated':'unknown'`, "[contenteditable='true']"}, true
	case "blackbox":
		return specification{"/api/auth/session", `typeof data?.user?.email==='string'&&data.user.email.trim()?'authenticated':data&&typeof data==='object'&&!Array.isArray(data)&&(Object.keys(data).length===0||data.user===null)?'login_required':'unknown'`, "textarea,[contenteditable='true']"}, true
	}
	return specification{}, false
}

func Check(ctx context.Context, endpoint, origin, adapter string) Evidence {
	result := Evidence{Status: "unsupported", Method: "browser_session_endpoint", CheckedAt: time.Now().UTC()}
	s, ok := spec(adapter)
	if !ok {
		return result
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		result.Status = "unknown"
		return result
	}
	origin = u.Scheme + "://" + u.Host
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	allocator, stopAllocator := chromedp.NewRemoteAllocator(ctx, endpoint)
	defer stopAllocator()
	tab, stopTab := chromedp.NewContext(allocator)
	defer stopTab()
	if err = chromedp.Run(tab, chromedp.Navigate(origin+"/")); err != nil {
		result.Status = "browser_unavailable"
		return result
	}
	var observed struct {
		Status string `json:"status"`
		Ready  bool   `json:"ready"`
		HTTP   int    `json:"http"`
	}
	err = chromedp.Run(tab, chromedp.Evaluate(script(origin, s), &observed, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	result.Status = "unknown"
	if err == nil {
		switch observed.Status {
		case "authenticated", "login_required", "challenge_or_access_denied", "rate_limited", "unknown":
			result.Status = observed.Status
		}
		result.ComposerReady = observed.Ready
		result.UpstreamStatus = observed.HTTP
	}
	return result
}

func script(origin string, s specification) string {
	args, _ := json.Marshal(map[string]string{"origin": origin, "path": s.path, "composer": s.composer})
	return `(async()=>{const a=` + string(args) + `;const out={status:'unknown',ready:false,http:0};if(location.origin!==a.origin)return out;
 const control=new AbortController(),timer=setTimeout(()=>control.abort(),8000);
 try{const r=await fetch(a.path,{method:'GET',credentials:'include',cache:'no-store',redirect:'error',signal:control.signal,headers:{Accept:'application/json'}});out.http=r.status;
 if(r.status===401){out.status='login_required';return out}if(r.status===403){out.status='challenge_or_access_denied';return out}if(r.status===429){out.status='rate_limited';return out}if(!r.ok||!r.headers.get('content-type')?.includes('application/json'))return out;
 const reader=r.body.getReader(),decoder=new TextDecoder();let text='',size=0;for(;;){const chunk=await reader.read();if(chunk.done)break;size+=chunk.value.byteLength;if(size>1048576){await reader.cancel();return out}text+=decoder.decode(chunk.value,{stream:true})}text+=decoder.decode();const data=JSON.parse(text);
 out.status=` + s.predicate + `;out.ready=[...document.querySelectorAll(a.composer)].some(e=>{const r=e.getBoundingClientRect();return !!(r.width&&r.height)&&getComputedStyle(e).visibility!=='hidden'&&getComputedStyle(e).display!=='none'});return out;
 }catch{return out}finally{clearTimeout(timer)}})()`
}
