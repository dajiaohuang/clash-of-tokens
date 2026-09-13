// Package browserexec runs provider page actions over Chromium CDP or Firefox
// BiDi. Every operation owns a new tab; it never reuses a user's existing tab.
package browserexec

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"clash-of-tokens/internal/browserbidi"
	cdpNetwork "github.com/chromedp/cdproto/network"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type endpointKey struct{}
type tabKey struct{}
type engineKey struct{}
type initializationErrorKey struct{}
type tab struct {
	endpoint string
	mu       sync.Mutex
	client   *browserbidi.Client
	id       string
	private  bool
}

type ContextOption bool

func WithNewBrowserContext() ContextOption { return true }

func NewRemoteAllocator(ctx context.Context, endpoint, engine string) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithValue(context.WithValue(ctx, endpointKey{}, endpoint), engineKey{}, engine))
}
func NewContext(ctx context.Context, options ...ContextOption) (context.Context, context.CancelFunc) {
	private := len(options) > 0 && bool(options[0])
	endpoint, ok := ctx.Value(endpointKey{}).(string)
	if !ok {
		return context.WithValue(ctx, initializationErrorKey{}, errors.New("browser allocator required")), func() {}
	}
	if ctx.Value(engineKey{}) != "firefox" {
		tab, close, err := OpenCDP(ctx, endpoint, private)
		if err != nil {
			return context.WithValue(ctx, initializationErrorKey{}, err), func() {}
		}
		return tab, close
	}
	t := &tab{endpoint: endpoint, private: private}
	ctx, cancel := context.WithCancel(context.WithValue(ctx, tabKey{}, t))
	return ctx, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.client != nil {
			t.client.Close()
		}
		cancel()
	}
}
func (t *tab) ensure(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return nil
	}
	c, err := browserbidi.Connect(ctx, t.endpoint)
	if err != nil {
		return err
	}
	id, err := c.NewTab(ctx, t.private)
	if err != nil {
		c.Close()
		return err
	}
	t.client, t.id = c, id
	return nil
}

func SetCookie(name, value, domain, path string, secure bool) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if t, ok := ctx.Value(tabKey{}).(*tab); ok {
			return t.client.SetCookie(ctx, t.id, map[string]any{"name": name, "value": map[string]string{"type": "string", "value": value}, "domain": domain, "path": path, "secure": secure, "sameSite": "lax"})
		}
		return cdpNetwork.SetCookie(name, value).WithDomain(domain).WithPath(path).WithSecure(secure).Do(ctx)
	})
}
func Run(ctx context.Context, actions ...chromedp.Action) error {
	if err, ok := ctx.Value(initializationErrorKey{}).(error); ok {
		return err
	}
	t, ok := ctx.Value(tabKey{}).(*tab)
	if !ok {
		return chromedp.Run(ctx, actions...)
	}
	if err := t.ensure(ctx); err != nil {
		return err
	}
	for _, action := range actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := action.Do(ctx); err != nil {
			return err
		}
	}
	return nil
}
func Navigate(destination string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if t, ok := ctx.Value(tabKey{}).(*tab); ok {
			return t.client.Navigate(ctx, t.id, destination)
		}
		return chromedp.Navigate(destination).Do(ctx)
	})
}
func Evaluate(expression string, out any, options ...func(*cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if t, ok := ctx.Value(tabKey{}).(*tab); ok {
			return t.client.EvaluateJSON(ctx, t.id, expression, out)
		}
		return chromedp.Evaluate(expression, out, options...).Do(ctx)
	})
}

type pollOptions struct{ interval, timeout time.Duration }
type PollOption func(*pollOptions)

func WithPollingInterval(d time.Duration) PollOption { return func(p *pollOptions) { p.interval = d } }
func WithPollingTimeout(d time.Duration) PollOption  { return func(p *pollOptions) { p.timeout = d } }

var ErrPollingTimeout = errors.New("browser condition timed out")
var ByQuery = chromedp.ByQuery

func Poll(expression string, out any, options ...PollOption) chromedp.Action {
	p := pollOptions{interval: 100 * time.Millisecond, timeout: 30 * time.Second}
	for _, option := range options {
		option(&p)
	}
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if _, ok := ctx.Value(tabKey{}).(*tab); !ok {
			return chromedp.Poll(expression, out, chromedp.WithPollingInterval(p.interval), chromedp.WithPollingTimeout(p.timeout)).Do(ctx)
		}
		if p.interval <= 0 {
			p.interval = 100 * time.Millisecond
		}
		wait, cancel := context.WithTimeout(ctx, p.timeout)
		defer cancel()
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			var result struct {
				Ready bool            `json:"ready"`
				Value json.RawMessage `json:"value"`
			}
			if err := Evaluate("(async()=>{const value=await ("+expression+");return {ready:!!value,value}})()", &result).Do(wait); err != nil {
				return err
			}
			if result.Ready {
				if out != nil {
					return json.Unmarshal(result.Value, out)
				}
				return nil
			}
			select {
			case <-wait.Done():
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return ErrPollingTimeout
			case <-ticker.C:
			}
		}
	})
}
func WaitVisible(selector string, options ...chromedp.QueryOption) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		if _, ok := ctx.Value(tabKey{}).(*tab); !ok {
			return chromedp.WaitVisible(selector, options...).Do(ctx)
		}
		raw, _ := json.Marshal(selector)
		return Poll("(()=>{const e=document.querySelector("+string(raw)+");if(!e)return false;const r=e.getBoundingClientRect(),s=getComputedStyle(e);return !!(r.width&&r.height)&&s.visibility!=='hidden'&&s.display!=='none'})()", nil).Do(ctx)
	})
}
