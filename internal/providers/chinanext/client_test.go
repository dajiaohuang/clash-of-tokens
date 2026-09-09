package chinanext

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

const testRequest = `{"model":"alias","messages":[{"role":"user","content":"hello"}]}`

func source(adapter, base string) config.Source {
	return config.Source{ID: adapter, Adapter: adapter, BaseURL: base, KeyEnv: "COT_CHINA_NEXT_TEST_KEY", MaxInflight: 1}
}

func writeSSE(w http.ResponseWriter, values ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, value := range values {
		_, _ = io.WriteString(w, "data: "+value+"\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
}

func TestDeepSeekHashV1AndPow(t *testing.T) {
	// Fixed vectors were generated independently with the pinned clean-room
	// reference implementation at .clash-tokens/reference/omniroute/open-sse/lib/
	// deepseek-pow-hash.js, rather than from this Go implementation.
	for _, vector := range []struct {
		input, want string
	}{
		{"", "e594808bc5b7151ac160c6d39a02e0a8e261ed588578403099e3561dc40c26b3"},
		{"abc", "f841106c601ce9be9bc38525e90d4178d47f21dd8eb9f238fc55ffaa4ca94506"},
		{"salt_123_0", "508526b4c1f8138ca3221fd99448aa2d00ba5ec37e603a1d2dc389e56279b281"},
		{"salt_123_1", "a70e9bdd8b2b0f82dbd1b1598ef83c6502b03160dbf4e6ad977a735952d93b54"},
		{"中文", "f0b0d3d156593e6e7d49dbfb1e8474e7c2d3c3181ec974aca1e8eeb6233ae011"},
		{strings.Repeat("a", 136), "680364b336f77918ed390287a581f96f1371599825acd1e348fa7649fcecbbab"},
		{strings.Repeat("a", 137), "dfeb7768d4d48053c083d849570e84cc8120520790f2d09cb56e40038081fab4"},
	} {
		got := fmt.Sprintf("%x", deepSeekHashV1([]byte(vector.input)))
		if got != vector.want {
			t.Fatalf("DeepSeekHashV1(%q)=%s, want %s", vector.input, got, vector.want)
		}
	}
	expireAt := time.Now().Add(time.Minute).UnixMilli()
	digest := deepSeekHashV1([]byte(fmt.Sprintf("salt_%d_0", expireAt)))
	nonce, e := solvePow(context.Background(), powChallenge{Algorithm: "DeepSeekHashV1", Challenge: fmt.Sprintf("%x", digest[:]), Salt: "salt", ExpireAt: expireAt, Difficulty: 1, TargetPath: deepSeekCompletionPath})
	if e != nil || nonce != 0 {
		t.Fatalf("nonce=%d err=%v", nonce, e)
	}
}

func TestDeepSeekPowGuardsAndCancellation(t *testing.T) {
	future := time.Now().Add(time.Minute).UnixMilli()
	base := powChallenge{
		Algorithm: "DeepSeekHashV1", Challenge: strings.Repeat("f", 64), Salt: "salt",
		ExpireAt: future, Difficulty: 100000, TargetPath: deepSeekCompletionPath,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := solvePow(ctx, base); !errors.Is(e, context.Canceled) {
		t.Fatalf("canceled PoW error=%v", e)
	}
	if _, e := solvePow(context.Background(), powChallenge{Algorithm: base.Algorithm, Challenge: base.Challenge, Salt: base.Salt, ExpireAt: future, Difficulty: 1, TargetPath: "/wrong"}); e == nil {
		t.Fatal("unexpected target path acceptance")
	}
	if _, e := solvePow(context.Background(), powChallenge{Algorithm: base.Algorithm, Challenge: base.Challenge, Salt: base.Salt, ExpireAt: 1, Difficulty: 1, TargetPath: deepSeekCompletionPath}); e == nil {
		t.Fatal("unexpected expired challenge acceptance")
	}
}

func TestChinaNextRequestRejectsSemanticLoss(t *testing.T) {
	t.Setenv("COT_CHINA_NEXT_TEST_KEY", "secret")
	c := New(source(AdapterDola, "http://127.0.0.1:1"))
	defer c.Close()
	for _, body := range []string{
		`{"model":"m","messages":[{"role":"user","content":"x"}],"temperature":0}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[]}`,
		`{"model":"m","messages":[{"role":"user","content":"x"}],"tool_choice":"none"}`,
		`{"model":"m","messages":[{"role":"system","content":"x"}]}`,
		`{"model":"m","messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`,
		`{"model":"m","messages":[{"role":"user","content":"x","name":"caller"}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/x"}}]}]}`,
		`{"model":"m","messages":[{"role":"user","content":"   "}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":""}]}]}`,
	} {
		if _, e := c.Do(context.Background(), "chat", "m", false, []byte(body), nil); !errors.Is(e, ErrUnsupported) {
			t.Fatalf("body=%s error=%v", body, e)
		}
	}
}

func TestDolaContractCookiePayloadAndSessions(t *testing.T) {
	t.Setenv("COT_CHINA_NEXT_TEST_KEY", "sessionid=sid; ttwid=tt; s_v_web_id=verify; unrelated=secret")
	var mu sync.Mutex
	var localIDs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completion" || r.Header.Get("Cookie") != "sessionid=sid; ttwid=tt; s_v_web_id=verify" {
			t.Fatalf("Dola request path=%s cookie=%q", r.URL.Path, r.Header.Get("Cookie"))
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		if json.Unmarshal(b, &body) != nil {
			t.Fatalf("payload=%s", b)
		}
		meta := body["client_meta"].(map[string]any)
		localID := meta["local_conversation_id"].(string)
		mu.Lock()
		localIDs = append(localIDs, localID)
		mu.Unlock()
		writeSSE(w,
			`{"content":{"content_block":[{"block_type":10000,"content":{"text_block":{"text":"dola"}}}]}}`,
			`{"event":"ignored"}`,
		)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: SSE_REPLY_END\ndata: {}\n\n")
	}))
	defer server.Close()
	c := New(source(AdapterDola, server.URL))
	defer c.Close()
	h := http.Header{}
	h.Set("X-COT-Session", "same")
	if _, err := c.Do(context.Background(), "chat", "dola-speed", false, []byte(testRequest), h); !errors.Is(err, ErrUnsupported) {
		t.Fatal("unimplemented continuity accepted", err)
	}
	for i := 0; i < 2; i++ {
		resp, e := c.Do(context.Background(), "chat", "dola-speed", false, []byte(testRequest), nil)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(resp.Body)
		resp.Body.Close()
		if e != nil || !bytes.Contains(b, []byte(`"dola"`)) {
			t.Fatalf("body=%s err=%v", b, e)
		}
	}
	resp, e := c.Do(context.Background(), "chat", "dola-speed", false, []byte(testRequest), nil)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(localIDs) != 3 || localIDs[0] == localIDs[1] || localIDs[0] == localIDs[2] {
		t.Fatalf("local ids=%v", localIDs)
	}
}

func TestYuanbaoContractCookieAndSSE(t *testing.T) {
	t.Setenv("COT_CHINA_NEXT_TEST_KEY", "hy_user=user1; hy_token=token1; unrelated=secret")
	var create, completions int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "hy_source=web; hy_user=user1; hy_token=token1" {
			t.Fatalf("cookie=%q", r.Header.Get("Cookie"))
		}
		switch {
		case r.URL.Path == "/api/user/agent/conversation/create":
			create++
			_, _ = io.WriteString(w, `{"id":"conv-yb"}`)
		case r.URL.Path == "/api/chat/conv-yb":
			completions++
			writeSSE(w, `{"type":"think","content":"reason"}`, `{"type":"text","msg":"answer"}`, `{"stopReason":"stop"}`)
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(source(AdapterYuanbao, server.URL))
	defer c.Close()
	resp, e := c.Do(context.Background(), "chat", "deepseek-r1", false, []byte(testRequest), nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || !bytes.Contains(b, []byte(`"answer"`)) || !bytes.Contains(b, []byte(`"reason"`)) {
		t.Fatalf("body=%s err=%v", b, e)
	}
	if create != 1 || completions != 1 {
		t.Fatalf("create=%d completions=%d", create, completions)
	}
}

func TestDeepSeekContractTokenSessionPowAndSSE(t *testing.T) {
	t.Setenv("COT_CHINA_NEXT_TEST_KEY", "user-token")
	expireAt := time.Now().Add(time.Minute).UnixMilli()
	challengeDigest := deepSeekHashV1([]byte(fmt.Sprintf("salt_%d_0", expireAt)))
	challenge := fmt.Sprintf("%x", challengeDigest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/users/current" {
			if r.Header.Get("Authorization") != "Bearer user-token" {
				t.Fatalf("auth=%q", r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"biz_data":{"token":"access-token"}}}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access-token" {
			t.Fatalf("access auth=%q path=%s", r.Header.Get("Authorization"), r.URL.Path)
		}
		switch r.URL.Path {
		case "/api/v0/chat_session/create":
			_, _ = io.WriteString(w, `{"data":{"biz_data":{"chat_session":{"id":"ds-session"}}}}`)
		case "/api/v0/chat/create_pow_challenge":
			_, _ = io.WriteString(w, fmt.Sprintf(`{"data":{"biz_data":{"challenge":{"algorithm":"DeepSeekHashV1","challenge":"%s","salt":"salt","signature":"sig","difficulty":1,"expire_at":%d,"target_path":"/api/v0/chat/completion"}}}}`, challenge, expireAt))
		case "/api/v0/chat/completion":
			var payload map[string]any
			b, _ := io.ReadAll(r.Body)
			if json.Unmarshal(b, &payload) != nil || payload["chat_session_id"] != "ds-session" {
				t.Fatalf("payload=%s", b)
			}
			pow, e := base64.StdEncoding.DecodeString(r.Header.Get("X-Ds-Pow-Response"))
			if e != nil {
				t.Fatal(e)
			}
			var answer map[string]any
			if json.Unmarshal(pow, &answer) != nil || answer["answer"] != float64(0) {
				t.Fatalf("pow=%s", pow)
			}
			writeSSE(w, `{"p":"response/fragments","v":[{"type":"THINK","content":"reason"},{"type":"ANSWER","content":"answer"}]}`, `{"p":"response/status","v":"FINISHED"}`)
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(source(AdapterDeepSeek, server.URL))
	defer c.Close()
	resp, e := c.Do(context.Background(), "chat", "deepseek-r1", false, []byte(testRequest), nil)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil || !bytes.Contains(b, []byte(`"answer"`)) || !bytes.Contains(b, []byte(`"reason"`)) {
		t.Fatalf("body=%s err=%v", b, e)
	}
}

func TestChinaNextMissingCompletionIsTruncated(t *testing.T) {
	t.Setenv("COT_CHINA_NEXT_TEST_KEY", "sessionid=sid; s_v_web_id=verify")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "event: STREAM_MSG_NOTIFY\ndata: {\"content\":{\"content_block\":[]}}\n\n")
	}))
	defer server.Close()
	c := New(source(AdapterDola, server.URL))
	defer c.Close()
	if _, e := c.Do(context.Background(), "chat", "dola-speed", false, []byte(testRequest), nil); !errors.Is(e, ErrTruncated) {
		t.Fatalf("error=%v", e)
	}
}

func TestChinaNextURLAndCredentialGuards(t *testing.T) {
	if _, e := baseURL(config.Source{BaseURL: "http://example.com"}, defaultDolaBase); e == nil {
		t.Fatal("external HTTP accepted")
	}
	if _, e := dolaCookie("sessionid=sid"); !errors.Is(e, ErrCredential) {
		t.Fatalf("cookie error=%v", e)
	}
	if _, e := yuanbaoCookie("hy_user=u"); !errors.Is(e, ErrCredential) {
		t.Fatalf("Yuanbao error=%v", e)
	}
}

func TestDoubaoResponseEnvelope(t *testing.T) {
	content, _ := json.Marshal(map[string]string{"text": "fixture answer"})
	eventData, _ := json.Marshal(map[string]any{"message": map[string]string{"content": string(content)}})
	envelope, _ := json.Marshal(map[string]any{"event_type": 2001, "event_data": string(eventData)})
	raw := []byte("data: " + string(envelope) + "\n\n")
	for _, stream := range []bool{false, true} {
		resp, err := doubaoResponse(context.Background(), "Exact UI Model", stream, raw)
		if err != nil {
			t.Fatalf("stream=%t: %v", stream, err)
		}
		b, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || !bytes.Contains(b, []byte("fixture ")) || !bytes.Contains(b, []byte("answer")) || bytes.Contains(b, []byte(`"usage"`)) {
			t.Fatalf("stream=%t body=%s err=%v", stream, b, readErr)
		}
	}
}

func TestDoubaoBrowserFixture(t *testing.T) {
	cdp := strings.TrimSpace(os.Getenv("COT_TEST_CDP"))
	if cdp == "" {
		t.Skip("set COT_TEST_CDP to a loopback Chrome DevTools endpoint")
	}
	probe, err := http.Get(strings.TrimRight(cdp, "/") + "/json/version")
	if err != nil {
		t.Skipf("configured Chrome DevTools endpoint unavailable: %v", err)
	}
	_ = probe.Body.Close()
	if probe.StatusCode != http.StatusOK {
		t.Skipf("configured Chrome DevTools endpoint returned %d", probe.StatusCode)
	}

	const model = "Exact UI Model"
	var mu sync.Mutex
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/chat/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><body>
<button id="model-picker" aria-haspopup="listbox" aria-expanded="false">Old UI Model</button>
<div id="model-options" role="listbox" style="display:none"><div id="model-option" role="option">`+model+`</div></div>
<textarea placeholder="Ask a message"></textarea><button id="send">Send</button>
<script>
const picker=document.getElementById('model-picker');
const options=document.getElementById('model-options');
const option=document.getElementById('model-option');
picker.addEventListener('click',()=>{picker.setAttribute('aria-expanded','true');options.style.display='block'});
option.addEventListener('click',()=>{picker.textContent=option.textContent;picker.setAttribute('aria-expanded','false');options.style.display='none'});
document.getElementById('send').addEventListener('click',()=>{
 const selected=picker.textContent.trim(),prompt=document.querySelector('textarea').value;
 fetch('/samantha/chat/completion',{method:'POST',headers:{'Content-Type':'application/json','X-Fixture-Model':selected},body:JSON.stringify({model:selected,prompt})});
});
</script></body></html>`)
		case "/samantha/chat/completion":
			body, _ := io.ReadAll(r.Body)
			var request struct {
				Model  string `json:"model"`
				Prompt string `json:"prompt"`
			}
			if json.Unmarshal(body, &request) != nil || request.Model != model || request.Prompt != "fixture prompt" || r.Header.Get("X-Fixture-Model") != model {
				t.Errorf("fixture request body=%s model-header=%q", body, r.Header.Get("X-Fixture-Model"))
			}
			mu.Lock()
			calls++
			mu.Unlock()
			event := func(text string) string {
				content, _ := json.Marshal(map[string]string{"text": text})
				eventData, _ := json.Marshal(map[string]any{"message": map[string]string{"content": string(content)}})
				envelope, _ := json.Marshal(map[string]any{"event_type": 2001, "event_data": string(eventData)})
				return string(envelope)
			}
			writeSSE(w, event("fixture "), event("answer"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New(config.Source{Adapter: AdapterDoubao, BaseURL: server.URL}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		resp, err := c.Do(ctx, "chat", model, stream, []byte(`{"model":"ignored","messages":[{"role":"user","content":"fixture prompt"}]}`), nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		b, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || !bytes.Contains(b, []byte("fixture ")) || !bytes.Contains(b, []byte("answer")) || bytes.Contains(b, []byte(`"usage"`)) {
			t.Fatalf("stream=%t body=%s err=%v", stream, b, readErr)
		}
		if stream && (resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("X-COT-Delivery") != "buffered" || !bytes.Contains(b, []byte("[DONE]"))) {
			t.Fatalf("stream headers/body content-type=%q delivery=%q body=%s", resp.Header.Get("Content-Type"), resp.Header.Get("X-COT-Delivery"), b)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("fixture completion calls=%d", calls)
	}
}
