package enterpriseweb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

const googleBase = "https://google.com"

type googleBrowserError struct {
	status  int
	message string
}

func (e *googleBrowserError) Error() string   { return e.message }
func (e *googleBrowserError) HTTPStatus() int { return e.status }

type googleSearchResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

func (c *Client) doGoogleAIMode(ctx context.Context, req chatRequest) (*http.Response, error) {
	if req.Model != "ai-mode" || len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		return nil, fmt.Errorf("%w: Google AI Mode accepts only model ai-mode with one user message", ErrUnsupported)
	}
	if !c.browser.Enabled || strings.TrimSpace(c.browser.CDPURL) == "" {
		return nil, &googleBrowserError{status: http.StatusServiceUnavailable, message: "Google AI Mode requires browser.enabled and a running configured Chrome"}
	}
	query := strings.TrimSpace(req.Messages[len(req.Messages)-1].Content)
	if query == "" {
		return nil, fmt.Errorf("%w: Google AI Mode requires a non-empty user query", ErrUnsupported)
	}
	answer, err := c.googleAIModeText(ctx, query)
	if err != nil {
		return nil, err
	}
	parser := func(ctx context.Context, body io.Reader, emit func(string) error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrTruncated
		}
		if len(data) > maxResponseBytes {
			return errors.New("enterpriseweb adapter: Google AI Mode response exceeds limit")
		}
		return emit(string(data))
	}
	if req.Stream {
		body := newConvertedResponse(ctx, io.NopCloser(strings.NewReader(answer)), req.Model, parser)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-COT-Delivery": {"buffered"}}, Body: body, ContentLength: -1}, nil
	}
	return collectResponse(ctx, io.NopCloser(strings.NewReader(answer)), req.Model, parser)
}

func (c *Client) googleAIModeText(ctx context.Context, query string) (string, error) {
	return c.googleAIModeTextAt(ctx, query, googleBase)
}

// googleAIModeTextAt keeps the browser flow testable against a local fixture
// without making the production adapter configurable to arbitrary origins.
func (c *Client) googleAIModeTextAt(ctx context.Context, query, base string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	allocator, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	tab, tabCancel := chromedp.NewContext(allocator)
	turn, turnCancel := context.WithCancel(tab)
	stop := context.AfterFunc(ctx, turnCancel)
	cleanup := func() {
		stop()
		turnCancel()
		tabCancel()
		allocatorCancel()
	}
	defer cleanup()

	fail := func(message string) (string, error) {
		return "", &googleBrowserError{status: http.StatusBadGateway, message: message}
	}
	if err := chromedp.Run(turn); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fail("Google AI Mode browser connection failed")
	}
	setup, setupCancel := context.WithTimeout(turn, 45*time.Second)
	defer setupCancel()
	searchURL := strings.TrimRight(base, "/") + "/search?q=" + url.QueryEscape(query) + "&ai-mode=true"
	if err := chromedp.Run(setup, chromedp.Navigate(searchURL)); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return fail("Google AI Mode navigation failed")
	}
	// Consent pages vary by locale; the pinned client clicks the visible
	// accept control when present and otherwise continues with the search page.
	var consentClicked bool
	_ = chromedp.Run(setup, chromedp.Evaluate(`(()=>{
 const buttons=[...document.querySelectorAll('button, input[type="submit"]')];
 const b=buttons.find(e=>/^(accept all|i agree|agree)$/i.test((e.innerText||e.value||'').trim()));
 if(b){b.click();return true;} return false;
})()`, &consentClicked))
	if consentClicked {
		if err := waitGoogleCondition(setup, `document.querySelectorAll('h3').length > 0`, 10); err != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
	}

	// Search results are emitted before AI Mode text by the pinned client.
	var results []googleSearchResult
	_ = chromedp.Run(setup, chromedp.Evaluate(googleSearchResultsScript, &results))
	if len(results) == 0 {
		// A slow page can expose the h3 nodes after the first evaluation.
		_ = waitGoogleCondition(setup, `document.querySelectorAll('h3').length > 0`, 10)
		_ = chromedp.Run(setup, chromedp.Evaluate(googleSearchResultsScript, &results))
	}

	var clicked bool
	for i := 0; i < 5 && !clicked; i++ {
		if err := chromedp.Run(setup, chromedp.Evaluate(`(()=>{
 const a=[...document.querySelectorAll('a,button')];
 const b=a.filter(e=>{const t=(e.textContent||'').trim();return t.endsWith('KI‑Modus')||t.endsWith('AI Mode');}).pop();
 if(b){b.click();return true;} return false;
		})()`, &clicked)); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return fail("Google AI Mode control lookup failed")
		}
		if clicked {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if !clicked {
		return fail("Google AI Mode control was not found")
	}

	var aiResult struct {
		Text     string `json:"text"`
		Overflow bool   `json:"overflow"`
		Ended    bool   `json:"ended"`
	}
	previous := ""
	var stableSince time.Time
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		aiResult = struct {
			Text     string `json:"text"`
			Overflow bool   `json:"overflow"`
			Ended    bool   `json:"ended"`
		}{}
		if err := chromedp.Run(setup, chromedp.Evaluate(googleAITextScript, &aiResult)); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return fail("Google AI Mode result extraction failed")
		}
		if aiResult.Overflow {
			return "", errors.New("enterpriseweb adapter: Google AI Mode response exceeds limit")
		}
		candidate := strings.TrimSpace(aiResult.Text)
		if candidate != previous {
			previous = candidate
			stableSince = time.Time{}
		}
		if aiResult.Ended && candidate != "" {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
		} else {
			// A pause before the source's explicit end marker is still an
			// in-progress answer; it must not be accepted as completion.
			stableSince = time.Time{}
		}
		if aiResult.Ended && candidate != "" && !stableSince.IsZero() && time.Since(stableSince) >= 2*time.Second {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			if !aiResult.Ended {
				return fail("Google AI Mode result did not expose an end marker")
			}
			return fail("Google AI Mode result did not reach a stable completion")
		case <-time.After(250 * time.Millisecond):
		}
	}
	var b strings.Builder
	if len(results) > 0 {
		b.WriteString(formatGoogleSearchResults(results))
		b.WriteString("\n\n---\n\n")
	}
	if candidate := strings.TrimSpace(aiResult.Text); candidate != "" {
		if b.Len()+len(candidate)+1 > maxResponseBytes {
			return "", errors.New("enterpriseweb adapter: Google AI Mode response exceeds limit")
		}
		b.WriteString(candidate)
		b.WriteByte('\n')
	}
	answer := strings.TrimSpace(b.String())
	if answer == "" {
		return fail("Google AI Mode returned no answer")
	}
	return answer, nil
}

func waitGoogleCondition(ctx context.Context, script string, attempts int) error {
	for i := 0; i < attempts; i++ {
		var ready bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(script, &ready)); err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("Google AI Mode condition timed out")
}

func formatGoogleSearchResults(results []googleSearchResult) string {
	var b strings.Builder
	for _, result := range results {
		if strings.TrimSpace(result.Title) == "" || strings.TrimSpace(result.Link) == "" {
			continue
		}
		fmt.Fprintf(&b, "> **%s**\n> %s\n> %s\n", result.Title, result.Snippet, result.Link)
	}
	return strings.TrimSpace(b.String())
}

const googleSearchResultsScript = `(()=>{
 const results=[];let chars=0;
 for(const item of document.querySelectorAll('h3')){
  if(results.length>=64||chars>=1048576)break;
  const linkEl=item.parentElement;
  const title=(item.innerText||'').slice(0,8192);
  const href=linkEl?.href||'';
  if(!title||!href)continue;
  let parent=linkEl?.parentElement?.parentElement?.parentElement;
  let snippet='';
  while(parent){
   if(parent.nextElementSibling){const e=parent.nextElementSibling.querySelector('div div:not(:has(a, svg)) span:not(:has(div, span, a, svg)):not(:empty)');if(e){snippet=(e.innerText||'').slice(0,8192);break;}}
   parent=parent.parentElement;
  }
  try{const u=new URL(href);u.searchParams.delete('srsltid');const link=u.toString().slice(0,16384);results.push({title,link,snippet});chars+=title.length+link.length+snippet.length;}catch{}
 }
 return results;
})()`

const googleAITextScript = `(()=>{
	const root=document.querySelector('[decode-data-ved="1"]');
	if(!root)return {text:'',overflow:false,ended:false};
	const out=[];let ended=false,overflow=false;
	const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);
	let node;
	while((node=walker.nextNode())){
  const text=(node.textContent||'').trim();
  if(!text)continue;
  if(text==='KI-Antworten können Fehler enthalten.'||text==='AI responses may include mistakes.'){
    ended=true;break;
  }
  const candidate=out.length?out.join('\n')+'\n'+text:text;
  if(new TextEncoder().encode(candidate).length>1048576){overflow=true;break;}
  out.push(text);
  if(out.length>=512){overflow=true;break;}
 }
 return {text:out.join('\n'),overflow,ended};
})()`
