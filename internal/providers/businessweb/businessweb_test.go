package businessweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func businessChat(model string, stream bool) []byte {
	b, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "hello"}}, "stream": stream})
	return b
}

func TestBusinessSupportsOnlyPinnedAdapters(t *testing.T) {
	for _, adapter := range []string{AdapterGeminiBusiness, AdapterAIStudioBuild, AdapterAIStudioPlayground, AdapterCopilotM365, AdapterPromptQL} {
		if !Supports(adapter) {
			t.Fatalf("Supports(%q)=false", adapter)
		}
	}
	if Supports("gemini-web") {
		t.Fatal("generic Gemini web must not be registered here")
	}
}

func TestGeminiBusinessRequestAndResponse(t *testing.T) {
	t.Setenv("BUSINESS_GEMINI", "__Secure-1PSID=psid; __Secure-1PSIDTS=ts; SAPISID=sapisid")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/home/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate" {
			t.Errorf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Cookie") == "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded;charset=UTF-8" {
			t.Errorf("headers=%v", r.Header)
		}
		if err := r.ParseForm(); err != nil || !strings.Contains(r.Form.Get("f.req"), "hello") {
			t.Errorf("form=%v", r.Form)
		}
		inner := make([]any, 5)
		inner[4] = []any{[]any{nil, []any{"answer"}}}
		payload, _ := json.Marshal(inner)
		row, _ := json.Marshal([]any{[]any{"wrb.fr", nil, string(payload)}})
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, ")]}'\n"+strconv.Itoa(len(row))+"\n"+string(row)+"\n")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterGeminiBusiness, BaseURL: server.URL + "/home", KeyEnv: "BUSINESS_GEMINI"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gemini-2.5-pro", false, businessChat("gemini-2.5-pro", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "answer") {
		t.Fatalf("response=%s", b)
	}
}

func TestGeminiBusinessRejectsMediaModelsBeforeSubmitting(t *testing.T) {
	t.Setenv("BUSINESS_GEMINI", "__Secure-1PSID=psid; __Secure-1PSIDTS=ts")
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterGeminiBusiness, BaseURL: server.URL + "/home", KeyEnv: "BUSINESS_GEMINI"})
	defer c.Close()
	for _, model := range []string{"gemini-3-pro-image", "gemini-2.0-flash-image", "veo-3.1-generate"} {
		if _, err := c.Do(context.Background(), "chat", model, false, businessChat(model, false), nil); err == nil || !strings.Contains(err.Error(), "image or video") {
			t.Fatalf("media model %q was accepted: %v", model, err)
		}
	}
	if calls != 0 {
		t.Fatalf("media model request reached upstream %d times", calls)
	}
}

func TestParseGeminiBusinessRejectsOverflowLength(t *testing.T) {
	for _, length := range []string{"9223372036854775807", "184467440737095516160"} {
		raw := []byte(")]}'\n" + length + "\n[]\n")
		if _, err := parseGeminiBusinessResponseStrict(raw); err == nil {
			t.Fatalf("accepted overflow frame length %q", length)
		}
	}
}

func TestAIStudioPlaygroundSparseRequest(t *testing.T) {
	t.Setenv("BUSINESS_STUDIO", `{"cookie":"SAPISID=sapisid","x_goog_api_key":"public-key","visit_id":"visit"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/GenerateContent" || r.Header.Get("Content-Type") != "application/json+protobuf" {
			t.Errorf("request=%s headers=%v", r.URL.Path, r.Header)
		}
		var root []any
		if json.NewDecoder(r.Body).Decode(&root) != nil || len(root) < 2 {
			t.Fatalf("invalid sparse request")
		}
		if got, _ := root[0].(string); got != "models/gemini-2.5-flash" {
			t.Errorf("model=%v", root[0])
		}
		part := []any{nil, "studio answer"}
		parts := []any{part}
		content := []any{parts, "model"}
		candidate := []any{content, 1}
		candidates := []any{candidate}
		frame := []any{candidates}
		response := []any{[]any{frame}}
		b, _ := json.Marshal(response)
		w.Header().Set("Content-Type", "application/json+protobuf")
		_, _ = w.Write(b)
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterAIStudioPlayground, BaseURL: server.URL, KeyEnv: "BUSINESS_STUDIO"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gemini-2.5-flash", false, businessChat("gemini-2.5-flash", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "studio answer") {
		t.Fatalf("response=%s", b)
	}
}

func TestAIStudioPlaygroundResponseRequiresFinishAndRejectsFeedback(t *testing.T) {
	part := []any{nil, "answer"}
	content := []any{[]any{part}, "model"}
	withoutFinishValue := []any{
		[]any{
			[]any{
				[]any{
					[]any{content},
				},
			},
		},
	}
	withoutFinish, _ := json.Marshal(withoutFinishValue)
	if _, err := parseMakerSuiteTextStrict(withoutFinish); err == nil {
		t.Fatal("unfinished response was accepted")
	}
	feedback, _ := json.Marshal([]any{[]any{[]any{nil, []any{1}}}})
	if _, err := parseMakerSuiteTextStrict(feedback); err == nil {
		t.Fatal("prompt feedback was accepted as a completion")
	}
	finishedValue := []any{
		[]any{
			[]any{
				[]any{
					[]any{content, 1},
				},
			},
		},
	}
	finished, _ := json.Marshal(finishedValue)
	if got, err := parseMakerSuiteTextStrict(finished); err != nil || got != "answer" {
		t.Fatalf("finished response=%q err=%v", got, err)
	}
}

func TestAIStudioBuildRequiresConfiguredBrowserAndOwnedURL(t *testing.T) {
	c := New(config.Source{Adapter: AdapterAIStudioBuild, BaseURL: "https://ai.studio/apps/operator-owned"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gemini-build", false, businessChat("gemini-build", false), nil)
	if resp != nil || err == nil || !strings.Contains(err.Error(), "browser.enabled") {
		t.Fatalf("Build browser requirement response=%v err=%v", resp, err)
	}
	for _, raw := range []string{"", "https://example.test/apps/a", "https://ai.studio/", "https://ai.studio/apps/"} {
		if _, err := aiStudioBuildAppURL(raw); err == nil {
			t.Fatalf("invalid Build app URL accepted: %q", raw)
		}
	}
	if got, err := aiStudioBuildAppURL("https://ai.studio/apps/operator-owned"); err != nil || got != "https://ai.studio/apps/operator-owned" {
		t.Fatalf("valid Build app URL=%q err=%v", got, err)
	}
}

func TestAIStudioBuildRequestAndResponseContract(t *testing.T) {
	req := chatRequest{Model: "gemini-2.5-flash", Messages: []chatMessage{
		{Role: "system", Content: "be concise"},
		{Role: "user", Content: "hello"},
	}, Stream: false}
	path, body, err := aiStudioBuildRequest(req)
	if err != nil || path != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("Build request path=%q err=%v", path, err)
	}
	if !strings.Contains(string(body), "systemInstruction") || !strings.Contains(string(body), "be concise") {
		t.Fatalf("Build request omitted system instruction: %s", body)
	}
	response := []byte(`{"candidates":[{"content":{"parts":[{"text":"build answer"}],"role":"model"},"finishReason":"STOP"}]}`)
	if got, err := parseAIStudioBuildResponse(response, "application/json"); err != nil || got != "build answer" {
		t.Fatalf("Build response=%q err=%v", got, err)
	}
	stream := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"build \"}],\"role\":\"model\"},\"finishReason\":\"\"}]}\n\ndata: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"answer\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}]}\n")
	if got, err := parseAIStudioBuildResponse(stream, "text/event-stream"); err != nil || got != "build answer" {
		t.Fatalf("Build stream response=%q err=%v", got, err)
	}
	for _, raw := range []string{
		`{"candidates":[{"content":{"parts":[{"text":"answer"}],"role":"model"},"finishReason":"SAFETY"}]}`,
		`{"error":{"code":500},"candidates":[{"content":{"parts":[{"text":"answer"}],"role":"model"},"finishReason":"STOP"}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":"answer"}],"role":"model"},"finishReason":"STOP"}]}

data: {"candidates":[{"content":{"parts":[{"text":"late"}],"role":"model"},"finishReason":""}]}
`,
	} {
		contentType := "application/json"
		if strings.HasPrefix(raw, "data:") {
			contentType = "text/event-stream"
		}
		if _, err := parseAIStudioBuildResponse([]byte(raw), contentType); err == nil {
			t.Fatalf("accepted invalid Build response: %s", raw)
		}
	}
}

func TestPromptQLStartPollAndThreadHeader(t *testing.T) {
	t.Setenv("BUSINESS_PQL", `{"token":"token","llm_config_id":"operator-config"}`)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("auth=%q", r.Header.Get("Authorization"))
		}
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		query, _ := request["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(query, "StartThread") {
			_, _ = io.WriteString(w, `{"data":{"start_thread":{"thread_id":"thread-1","thread_events":[]}}}`)
			return
		}
		if strings.Contains(query, "Events") {
			_, _ = io.WriteString(w, `{"data":{"thread_events":[{"thread_event_id":1,"event_data":{"AgentMessage":{"final_response.message":"promptql answer","agent_loop_action_result_type":"final_response_sent"}}}]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"send_thread_message":{"thread_event_id":2}}}`)
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterPromptQL, BaseURL: server.URL, KeyEnv: "BUSINESS_PQL", Project: "project"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "web", false, businessChat("web", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "promptql answer") || resp.Header.Get("X-PromptQL-Thread-Id") != "thread-1" {
		t.Fatalf("response=%s headers=%v", b, resp.Header)
	}
	if calls < 2 {
		t.Fatalf("expected start and events calls, got %d", calls)
	}
}

func TestCopilotM365SignalRContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(r, w)
		if err != nil {
			return
		}
		defer conn.Close()
		raw, err := wsutil.ReadClientText(conn)
		if err != nil || !strings.Contains(string(raw), `"protocol":"json"`) {
			t.Errorf("handshake=%s err=%v", raw, err)
			return
		}
		_ = wsutil.WriteServerText(conn, []byte("{}\x1e"))
		raw, err = wsutil.ReadClientText(conn)
		if err != nil || !strings.Contains(string(raw), `"target":"chat"`) || !strings.Contains(string(raw), `"target":"Metrics"`) {
			t.Errorf("invocation=%s err=%v", raw, err)
			return
		}
		frames := `{"type":1,"target":"update","arguments":[{"messages":[{"text":"hello"}]}]}` + "\x1e" + `{"type":3,"invocationId":"0"}` + "\x1e"
		_ = wsutil.WriteServerText(conn, []byte(frames))
	}))
	defer server.Close()
	base := strings.Replace(server.URL, "http://", "ws://", 1)
	t.Setenv("BUSINESS_M365", `{"access_token":"access","chathub_path":"user@tenant"}`)
	c := New(config.Source{Adapter: AdapterCopilotM365, BaseURL: base, KeyEnv: "BUSINESS_M365"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "web", false, businessChat("web", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "hello") {
		t.Fatalf("response=%s", b)
	}
}

func TestCopilotM365ContentShapes(t *testing.T) {
	if got := m365FrameWriteAtCursor(map[string]any{
		"type": float64(1), "arguments": []any{map[string]any{"writeAtCursor": "delta"}},
	}); got != "delta" {
		t.Fatalf("writeAtCursor=%q", got)
	}
	if got := m365FrameText(map[string]any{
		"type": float64(1), "arguments": []any{map[string]any{"messages": []any{
			map[string]any{"author": "bot", "messageType": "Progress", "text": "ignore"},
			map[string]any{"author": "bot", "text": "answer"},
		}}},
	}); got != "answer" {
		t.Fatalf("message snapshot=%q", got)
	}
	if got := m365FrameText(map[string]any{
		"type": float64(2), "item": map[string]any{"result": map[string]any{"message": "final"}},
	}); got != "final" {
		t.Fatalf("final result=%q", got)
	}
	if got := m365FrameText(map[string]any{
		"type": float64(1), "arguments": []any{map[string]any{
			"messages": []any{map[string]any{"author": "user", "text": "prompt"}},
			"result":   map[string]any{"message": "untrusted fallback"},
		}},
	}); got != "" {
		t.Fatalf("accepted non-answer message fallback=%q", got)
	}
	if got := m365FrameText(map[string]any{
		"type": float64(1), "arguments": []any{map[string]any{"text": "unrecognized update"}},
	}); got != "" {
		t.Fatalf("accepted unrecognized update text=%q", got)
	}
	if got := m365FrameText(map[string]any{
		"type": float64(1), "item": map[string]any{"result": map[string]any{"message": "wrong frame"}},
	}); got != "" {
		t.Fatalf("accepted invocation result on update=%q", got)
	}
}

func TestCopilotM365RequiresWebModel(t *testing.T) {
	c := New(config.Source{Adapter: AdapterCopilotM365})
	defer c.Close()
	for _, model := range []string{"copilot-m365", "gemini-2.5-flash"} {
		if _, err := c.Do(context.Background(), "chat", model, false, businessChat(model, false), nil); err == nil || !strings.Contains(err.Error(), "only the web model") {
			t.Fatalf("model %q accepted: %v", model, err)
		}
	}
}

func TestAIStudioBuildBrowserFixture(t *testing.T) {
	cdp := strings.TrimSpace(os.Getenv("COT_TEST_CDP"))
	if cdp == "" {
		t.Skip("set COT_TEST_CDP to a loopback Chrome DevTools endpoint")
	}
	var calls int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apps/fixture":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<!doctype html><iframe data-cot-aistudio-preview="true" src="/preview"></iframe>`)
		case "/preview":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<!doctype html><script>
const nativeFetch = window.fetch.bind(window);
window.fetch = (input, init) => {
  const target = String(input);
  if (target.startsWith('https://generativelanguage.googleapis.com/')) {
    const local = new URL(target);
    local.protocol = location.protocol;
    local.host = location.host;
    return nativeFetch(local.toString(), init);
  }
  return nativeFetch(input, init);
};
</script>`)
		case "/v1beta/models/gemini-2.5-flash:generateContent":
			calls++
			if r.Method != http.MethodPost {
				t.Errorf("method=%s", r.Method)
			}
			requestBody, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(requestBody), "fixture query") {
				t.Errorf("missing prompt in body=%s", requestBody)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"fixture answer"}]},"finishReason":"STOP"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := New(config.Source{Adapter: AdapterAIStudioBuild, BaseURL: s.URL + "/apps/fixture"}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := c.Do(ctx, "chat", "gemini-2.5-flash", false, []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"fixture query"}]} `), nil)
	if err != nil {
		t.Fatalf("%v (fixture calls=%d)", err, calls)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(raw), "fixture answer") || calls != 1 {
		t.Fatalf("response=%s calls=%d", raw, calls)
	}
}
