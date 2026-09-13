package chatgptweb

import (
	"context"
	"encoding/json"
	"sync"

	"clash-of-tokens/internal/browserbidi"
	"github.com/chromedp/cdproto/input"
	cdpRuntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type bidiTabKey struct{}
type bidiTab struct {
	endpoint string
	client   *browserbidi.Client
	id       string
	mu       sync.Mutex
}

func (b *bidiTab) ensure(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		return nil
	}
	client, err := browserbidi.Connect(ctx, b.endpoint)
	if err != nil {
		return err
	}
	id, err := client.NewTab(ctx)
	if err != nil {
		client.Close()
		return err
	}
	b.client = client
	b.id = id
	return nil
}
func browserInitialize(ctx context.Context) error {
	if b, ok := ctx.Value(bidiTabKey{}).(*bidiTab); ok {
		return b.ensure(ctx)
	}
	return chromedp.Run(ctx)
}
func browserNavigate(ctx context.Context, destination string) error {
	if b, ok := ctx.Value(bidiTabKey{}).(*bidiTab); ok {
		if err := b.ensure(ctx); err != nil {
			return err
		}
		return b.client.Navigate(ctx, b.id, destination)
	}
	return chromedp.Run(ctx, chromedp.Navigate(destination))
}
func browserEvaluate(ctx context.Context, script string, out any) error {
	if b, ok := ctx.Value(bidiTabKey{}).(*bidiTab); ok {
		if err := b.ensure(ctx); err != nil {
			return err
		}
		return b.client.EvaluateJSON(ctx, b.id, script, out)
	}
	return chromedp.Run(ctx, chromedp.Evaluate(script, out, func(p *cdpRuntime.EvaluateParams) *cdpRuntime.EvaluateParams { return p.WithAwaitPromise(true) }))
}
func browserInsertText(ctx context.Context, text string) error {
	if b, ok := ctx.Value(bidiTabKey{}).(*bidiTab); ok {
		// Insert multiline text without synthesizing Enter/Tab key presses, which
		// could submit a form or move focus before the explicit guarded send step.
		raw, _ := json.Marshal(text)
		var inserted bool
		script := `(()=>{const text=` + string(raw) + `;const el=document.activeElement;
   if(el instanceof HTMLTextAreaElement){const set=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;set.call(el,text);el.dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:text}));return true}
   if(el?.isContentEditable)return document.execCommand('insertText',false,text);
   return false})()`
		if err := b.client.EvaluateJSON(ctx, b.id, script, &inserted); err != nil {
			return err
		}
		if !inserted {
			return problem(502, "Firefox composer did not accept text")
		}
		return nil
	}
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error { return input.InsertText(text).Do(ctx) }))
}
func (b *bidiTab) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client != nil {
		b.client.CloseTab(b.id)
		b.client.Close()
		b.client = nil
	}
}
