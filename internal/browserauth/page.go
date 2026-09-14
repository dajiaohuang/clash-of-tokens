package browserauth

import (
	"context"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"net/url"
	"time"
)

// CheckPage checks an already-owned tab without a new connection or navigation.
// It returns evidence only; tokens and response bodies never leave the page.
func CheckPage(ctx context.Context, origin, adapter string) Evidence {
	out := Evidence{Status: "unsupported", Method: "browser_session_endpoint", CheckedAt: time.Now().UTC()}
	s, ok := spec(adapter)
	if !ok {
		return out
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil {
		out.Status = "unknown"
		return out
	}
	origin = u.Scheme + "://" + u.Host
	var observed struct {
		Status string `json:"status"`
		HTTP   int    `json:"http"`
	}
	err = chromedp.Run(ctx, chromedp.Evaluate(scriptExpected(origin, s, adapter, "", ""), &observed, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
	out.Status = "unknown"
	if err == nil {
		out.Status = observed.Status
		out.UpstreamStatus = observed.HTTP
	}
	return out
}
