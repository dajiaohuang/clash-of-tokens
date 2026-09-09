package chatgptweb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"github.com/chromedp/chromedp"
)

func TestRequestRejectsLostSemantics(t *testing.T) {
	for _, body := range []string{`{"model":"auto","messages":[{"role":"system","content":"policy"},{"role":"user","content":"hello"}]}`, `{"model":"auto","messages":[{"role":"user","content":"hello"}],"tools":[{}]}`, `{"model":"auto","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`, `{"model":"auto","messages":[{"role":"user","content":"hello"}],"temperature":0}`} {
		if _, e := parseRequest("chat", []byte(body)); e == nil {
			t.Fatal("unsupported semantics accepted", body)
		}
	}
}
func TestSessionStoreRestartAndBound(t *testing.T) {
	c := config.Default().Browser
	c.StateFile = filepath.Join(t.TempDir(), "sessions.json")
	c.MaxSessions = 1
	s := newStore(c)
	v := Session{ID: "a", Conversation: "conversation", ResponseID: "resp1"}
	if e := s.put(v); e != nil {
		t.Fatal(e)
	}
	if e := s.put(Session{ID: "b"}); e == nil {
		t.Fatal("capacity not enforced")
	}
	s = newStore(c)
	got, ok, e := s.get("", "resp1")
	if e != nil || !ok || got.Conversation != "conversation" {
		t.Fatalf("%+v %v %v", got, ok, e)
	}
	v.ResponseID = "resp2"
	if e = s.put(v); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.get("", "resp1"); e == nil {
		t.Fatal("old response id accepted")
	}
}

type fakeConversation struct {
	Mapping map[string]any
	Current string
}
type fakeChatGPT struct {
	mu            sync.Mutex
	conversations map[string]*fakeConversation
	account       string
	sends         int
	wrongModel    bool
}

func (f *fakeChatGPT) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/api/auth/session":
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "fixture-token-stays-in-browser", "user": map[string]string{"id": f.account}})
	case r.URL.Path == "/send":
		var v struct {
			Prompt       string `json:"prompt"`
			Conversation string `json:"conversation"`
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		f.sends++
		if v.Conversation == "" {
			v.Conversation = fmt.Sprint("conversation-", f.sends)
			f.conversations[v.Conversation] = &fakeConversation{Mapping: map[string]any{}}
		}
		conv := f.conversations[v.Conversation]
		user, assistant := fmt.Sprint("u", f.sends), fmt.Sprint("a", f.sends)
		conv.Mapping[user] = map[string]any{"id": user, "parent": conv.Current, "message": map[string]any{"id": user, "author": map[string]string{"role": "user"}, "content": map[string]any{"content_type": "text", "parts": []string{v.Prompt}}}}
		model := "web-model"
		if f.wrongModel {
			model = "different-model"
		}
		conv.Mapping[assistant] = map[string]any{"id": assistant, "parent": user, "message": map[string]any{"id": assistant, "author": map[string]string{"role": "assistant"}, "recipient": "all", "content": map[string]any{"content_type": "text", "parts": []string{"answer: " + v.Prompt}}, "metadata": map[string]string{"model_slug": model}, "end_turn": true, "status": "finished_successfully"}}
		conv.Current = assistant
		_ = json.NewEncoder(w).Encode(map[string]string{"id": v.Conversation})
	case strings.HasPrefix(r.URL.Path, "/backend-api/conversation/"):
		if r.Header.Get("Authorization") != "Bearer fixture-token-stays-in-browser" {
			w.WriteHeader(401)
			return
		}
		conv := f.conversations[strings.TrimPrefix(r.URL.Path, "/backend-api/conversation/")]
		if conv == nil {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"current_node": conv.Current, "mapping": conv.Mapping})
	default:
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html><body><form><textarea id="prompt-textarea" style="width:400px;height:100px"></textarea><button type="submit" data-testid="send-button">Send</button></form><script>document.querySelector('form').onsubmit=async e=>{e.preventDefault();const el=document.querySelector('textarea');const response=await fetch('/send',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({prompt:el.value,conversation:location.pathname.startsWith('/c/')?location.pathname.slice(3):''})});const data=await response.json();history.replaceState(null,'','/c/'+data.id);el.value='';};</script></body></html>`)
	}
}
func browserFixture(t *testing.T) (*Driver, *fakeChatGPT) {
	t.Helper()
	chrome := ""
	for _, path := range []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "google-chrome", "chromium", "chromium-browser"} {
		if found, e := exec.LookPath(path); e == nil {
			chrome = found
			break
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium required for browser contract tests")
	}
	parent, pcancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(pcancel)
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	allocator, acancel := chromedp.NewExecAllocator(parent, options...)
	t.Cleanup(acancel)
	browser, bcancel := chromedp.NewContext(allocator)
	t.Cleanup(bcancel)
	if e := chromedp.Run(browser); e != nil {
		t.Fatal(e)
	}
	fake := &fakeChatGPT{conversations: map[string]*fakeConversation{}, account: "account-a"}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	c := config.Default().Browser
	c.Enabled = true
	c.StateFile = filepath.Join(t.TempDir(), "sessions.json")
	c.PollMS = 50
	d := New(c, "web")
	d.origin = server.URL
	d.connect = func() (context.Context, context.CancelFunc) { return chromedp.NewContext(browser) }
	return d, fake
}
func TestBrowserChatContinuationAndRestart(t *testing.T) {
	d, fake := browserFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	body := `{"model":"auto","messages":[{"role":"user","content":"你好\nworld"}]}`
	resp, e := d.Do(ctx, "chat", "auto", []byte(body), http.Header{})
	if e != nil {
		t.Fatal(e)
	}
	result, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || !strings.Contains(string(result), "answer: 你好") {
		t.Fatalf("%s %v", result, e)
	}
	key := resp.Header.Get("X-COT-Session")
	if key == "" {
		t.Fatal("session missing")
	}
	restarted := New(d.cfg, "web")
	restarted.origin = d.origin
	restarted.connect = d.connect
	body = `{"model":"auto","messages":[{"role":"user","content":"你好\nworld"},{"role":"assistant","content":"answer: 你好\nworld"},{"role":"user","content":"continue"}],"stream":true}`
	h := http.Header{"X-Cot-Session": []string{key}}
	resp, e = restarted.Do(ctx, "chat", "auto", []byte(body), h)
	if e != nil {
		t.Fatal(e)
	}
	result, e = io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || !strings.Contains(string(result), "answer: continue") || !strings.Contains(string(result), "[DONE]") {
		t.Fatalf("%s %v", result, e)
	}
	if _, e = restarted.Do(ctx, "chat", "auto", []byte(body), h); e == nil {
		t.Fatal("duplicate old history accepted")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.sends != 2 || len(fake.conversations) != 1 {
		t.Fatalf("sends=%d conversations=%d", fake.sends, len(fake.conversations))
	}
}
func TestBrowserResponsesAndAccountBinding(t *testing.T) {
	d, fake := browserFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	resp, e := d.Do(ctx, "responses", "auto", []byte(`{"model":"auto","input":"one","stream":true}`), http.Header{})
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || !strings.Contains(string(b), "response.completed") {
		t.Fatalf("%s %v", b, e)
	}
	previous := resp.Header.Get("X-COT-Response-Id")
	next, _ := json.Marshal(map[string]any{"model": "auto", "input": "two", "previous_response_id": previous})
	resp, e = d.Do(ctx, "responses", "auto", next, http.Header{})
	if e != nil {
		t.Fatal(e)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "answer: two") {
		t.Fatal(string(b))
	}
	next, _ = json.Marshal(map[string]any{"model": "auto", "input": "three", "previous_response_id": resp.Header.Get("X-COT-Response-Id")})
	fake.mu.Lock()
	fake.account = "account-b"
	fake.mu.Unlock()
	if _, e = d.Do(ctx, "responses", "auto", next, http.Header{}); e == nil {
		t.Fatal("changed account accepted")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.sends != 2 {
		t.Fatal(fake.sends)
	}
}
func TestBrowserWrongModelLeavesDirtySession(t *testing.T) {
	d, fake := browserFixture(t)
	fake.wrongModel = true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h := http.Header{"X-Cot-Session": []string{"test-session"}}
	body := []byte(`{"model":"web-model","messages":[{"role":"user","content":"hello"}]}`)
	if _, e := d.Do(ctx, "chat", "web-model", body, h); e == nil {
		t.Fatal("silent model downgrade")
	}
	v, ok, e := d.store.get("test-session", "")
	if e != nil || !ok || !v.Dirty {
		t.Fatalf("%+v %v", v, e)
	}
}
