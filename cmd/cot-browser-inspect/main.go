// Read-only DOM diagnostics in a new gateway-owned browser tab. No credentials.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/chromedp/chromedp"
	"os"
	"time"
)

func main() {
	clear:=flag.Bool("clear-smoke-draft",false,"clear only the exact draft created by scripts/smoke_chatgpt.py");flag.Parse()
	alloc, cancel := chromedp.NewRemoteAllocator(context.Background(), "http://127.0.0.1:9222")
	defer cancel()
	tab, closeTab := chromedp.NewContext(alloc)
	defer closeTab()
	ctx, stop := context.WithTimeout(tab, 25*time.Second)
	defer stop()
	var state json.RawMessage
	e := chromedp.Run(ctx, chromedp.Navigate("https://chatgpt.com/"), chromedp.Sleep(3*time.Second), chromedp.Evaluate(`JSON.stringify({url:location.href,title:document.title,inputs:[...document.querySelectorAll('textarea,[contenteditable]')].map(e=>({tag:e.tagName,id:e.id,role:e.getAttribute('role'),editable:e.getAttribute('contenteditable'),visible:!!e.getClientRects().length})),buttons:[...(document.querySelector('#prompt-textarea')?.closest('form')?.querySelectorAll('button')||[])].map(b=>({text:b.innerText,aria:b.getAttribute('aria-label'),type:b.type,testid:b.dataset.testid,disabled:b.disabled,html:b.outerHTML.slice(0,350)}))})`, &state))
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println(string(state))
	if *clear {var cleaned bool;e=chromedp.Run(ctx,chromedp.Evaluate(`(()=>{const e=document.querySelector('#prompt-textarea');if(!e||e.innerText.trim()!=='Remember the codeword amber-sparrow-47. Reply with only that codeword.')return false;e.focus();window.getSelection().selectAllChildren(e);document.execCommand('delete');return e.innerText.trim()==='';})()`,&cleaned),chromedp.Sleep(2*time.Second));fmt.Printf("smoke_draft_cleared=%v error=%v\n",cleaned,e)}
}
