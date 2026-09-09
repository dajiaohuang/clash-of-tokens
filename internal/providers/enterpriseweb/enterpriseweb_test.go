package enterpriseweb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func chatBody(model string, stream bool) []byte {
	flag := "false"
	if stream {
		flag = "true"
	}
	return []byte("{\"model\":\"" + model + "\",\"messages\":[{\"role\":\"user\",\"content\":\"question\"}],\"stream\":" + flag + "}")
}

func TestSupportsOnlyConcreteProtocols(t *testing.T) {
	for _, a := range []string{AdapterMaxAI, AdapterNotionWeb, AdapterOperaAria, AdapterGoogleAI} {
		if !Supports(a) {
			t.Fatalf("Supports(%q)=false", a)
		}
	}
	for _, a := range []string{"maxai-browser", "merlin", "monica", "raycast", "sider"} {
		if Supports(a) {
			t.Fatalf("unsupported %q was registered", a)
		}
	}
}

func maxAICredential() string {
	return "{\"access_token\":\"access\",\"device_id\":\"device\",\"user_id\":\"user\",\"hmac_key\":\"hmac\",\"aes_key\":\"aes\",\"context_key\":\"0123456789abcdef0123456789abcdef01234567\",\"app_version\":\"webpage_1.2.3\"}"
}

func TestMaxAIRequestAndStream(t *testing.T) {
	t.Setenv("ENTERPRISE_MAXAI", maxAICredential())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gpt/cwc/chat" || r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("X-Authorization") == "" {
			t.Errorf("MaxAI route/auth = %s/%q/%q", r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("MaxAI JSON: %v", err)
		}
		if body["model_name"] != "gpt-test" || body["streaming"] != true {
			t.Errorf("MaxAI body = %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"data_key\":\"text\",\"need_merge\":true,\"text\":\"hello\"}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterMaxAI, BaseURL: server.URL, KeyEnv: "ENTERPRISE_MAXAI"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "gpt-test", true, chatBody("gpt-test", true), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "\"content\":\"hello\"") || !strings.Contains(string(data), "[DONE]") {
		t.Fatalf("MaxAI output=%s err=%v", data, err)
	}
}

func TestMaxAISm3Vector(t *testing.T) {
	if got := sm3Hex("abc"); got != "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0" {
		t.Fatalf("SM3(abc)=%s", got)
	}
}

func TestMaxAICancellationClosesUpstream(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	t.Setenv("ENTERPRISE_MAXAI_CANCEL", maxAICredential())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			_, _ = io.WriteString(w, "data: {\"data_key\":\"text\",\"need_merge\":true,\"text\":\"partial\"}\n\n")
			f.Flush()
		}
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterMaxAI, BaseURL: server.URL, KeyEnv: "ENTERPRISE_MAXAI_CANCEL"})
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := c.Do(ctx, "chat", "m", true, chatBody("m", true), nil)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("MaxAI upstream was not closed")
	}
}

func TestMaxAISSEStopsAtDone(t *testing.T) {
	var got strings.Builder
	err := parseMaxAISSE(context.Background(), strings.NewReader(
		"data: {\"data_key\":\"text\",\"need_merge\":true,\"text\":\"first\"}\n\n"+
			"data: [DONE]\n\n"+
			"data: {\"data_key\":\"text\",\"need_merge\":true,\"text\":\"ignored\"}\n\n",
	), func(text string) error {
		got.WriteString(text)
		return nil
	})
	if err != nil || got.String() != "first" {
		t.Fatalf("MaxAI done parse = %q, err=%v", got.String(), err)
	}
}

func TestMaxAISSEErrorAfterPartialFails(t *testing.T) {
	var got strings.Builder
	err := parseMaxAISSE(context.Background(), strings.NewReader(
		"data: {\"data_key\":\"text\",\"need_merge\":true,\"text\":\"partial\"}\n\n"+
			"data: {\"type\":\"error\"}\n\n",
	), func(text string) error {
		got.WriteString(text)
		return nil
	})
	if err == nil || got.String() != "partial" {
		t.Fatalf("MaxAI partial error parse = %q, err=%v", got.String(), err)
	}
}

func TestNotionRequestAndNDJSON(t *testing.T) {
	t.Setenv("ENTERPRISE_NOTION", "{\"token_v2\":\"session\",\"space_id\":\"space\",\"user_id\":\"user\"}")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/runInferenceTranscript" || r.Header.Get("Cookie") != "token_v2=session" || r.Header.Get("x-notion-space-id") != "space" || r.Header.Get("x-notion-active-user-header") != "user" {
			t.Errorf("Notion route/auth = %s/%q/%q/%q", r.URL.Path, r.Header.Get("Cookie"), r.Header.Get("x-notion-space-id"), r.Header.Get("x-notion-active-user-header"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Notion JSON: %v", err)
		}
		if body["createThread"] != true || body["threadType"] != "workflow" {
			t.Errorf("Notion body = %#v", body)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, "{\"type\":\"markdown-chat\",\"value\":\"notion answer\"}\n")
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterNotionWeb, BaseURL: server.URL, KeyEnv: "ENTERPRISE_NOTION"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "notion-model", false, chatBody("notion-model", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "notion answer") {
		t.Fatalf("Notion output=%s err=%v", data, err)
	}
}

func TestOperaAriaRefreshAndV2(t *testing.T) {
	t.Setenv("ENTERPRISE_OPERA", "{\"refresh_token\":\"refresh\"}")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/v1/token/":
			if r.FormValue("grant_type") != "refresh_token" || r.FormValue("refresh_token") != "refresh" {
				t.Errorf("token form = %s", r.Form.Encode())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "{\"access_token\":\"access\"}")
		case "/api/v2/a-chat":
			if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("X-Requested-With") != "com.opera.mini.native" {
				t.Errorf("Opera headers = %#v", r.Header)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("Opera JSON: %v", err)
			}
			if body["sia"] != true || body["think_harder"] != false {
				t.Errorf("Opera body = %#v", body)
			}
			encryption, _ := body["encryption"].(map[string]any)
			key, _ := encryption["key"].(string)
			decoded, decodeErr := base64.StdEncoding.DecodeString(key)
			if decodeErr != nil || len(decoded) != 32 {
				t.Errorf("Opera encryption key = %q (decoded=%d, err=%v)", key, len(decoded), decodeErr)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"response\":{\"content_type\":\"text\",\"message\":\"aria answer\"}}\n\n")
		default:
			t.Errorf("unexpected Opera path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterOperaAria, BaseURL: server.URL, KeyEnv: "ENTERPRISE_OPERA"})
	defer c.Close()
	resp, err := c.Do(context.Background(), "chat", "aria", false, chatBody("aria", false), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || !strings.Contains(string(data), "aria answer") {
		t.Fatalf("Opera output=%s err=%v", data, err)
	}
}

type enterpriseRoundTrip func(*http.Request) (*http.Response, error)

func (f enterpriseRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOperaRefreshUsesSeparateProductionTokenHost(t *testing.T) {
	for _, configuredBase := range []string{"", operaBase} {
		t.Run(map[bool]string{true: "explicit-composer", false: "default"}[configuredBase != ""], func(t *testing.T) {
			t.Setenv("ENTERPRISE_OPERA_HOST", `{"refresh_token":"refresh"}`)
			var tokenHost, chatHost string
			c := New(config.Source{Adapter: AdapterOperaAria, BaseURL: configuredBase, KeyEnv: "ENTERPRISE_OPERA_HOST"})
			c.http = &http.Client{Transport: enterpriseRoundTrip(func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/oauth2/v1/token/":
					tokenHost = r.URL.Host
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"access"}`)), Request: r}, nil
				case "/api/v2/a-chat":
					chatHost = r.URL.Host
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"response\":{\"content_type\":\"text\",\"message\":\"answer\"}}\n\n")), Request: r}, nil
				default:
					return nil, errors.New("unexpected Opera path: " + r.URL.Path)
				}
			})}
			defer c.Close()
			resp, err := c.Do(context.Background(), "chat", "aria", false, chatBody("aria", false), nil)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if tokenHost != operaTokenBase[len("https://"):] || chatHost != operaBase[len("https://"):] {
				t.Fatalf("Opera hosts token=%q chat=%q", tokenHost, chatHost)
			}
		})
	}
}

func TestOperaSSEStopsAtDone(t *testing.T) {
	var got strings.Builder
	err := parseOperaSSE(context.Background(), strings.NewReader(
		"data: {\"response\":{\"content_type\":\"text\",\"message\":\"first\"}}\n\n"+
			"data: [DONE]\n\n"+
			"data: {\"response\":{\"content_type\":\"text\",\"message\":\"ignored\"}}\n\n",
	), "v2", func(text string) error {
		got.WriteString(text)
		return nil
	})
	if err != nil || got.String() != "first" {
		t.Fatalf("Opera done parse = %q, err=%v", got.String(), err)
	}
}

func TestOperaSSEErrorAfterPartialFails(t *testing.T) {
	var got strings.Builder
	err := parseOperaSSE(context.Background(), strings.NewReader(
		"data: {\"response\":{\"content_type\":\"text\",\"message\":\"partial\"}}\n\n"+
			"data: {\"status\":\"error\"}\n\n",
	), "v2", func(text string) error {
		got.WriteString(text)
		return nil
	})
	if err == nil || got.String() != "partial" {
		t.Fatalf("Opera partial error parse = %q, err=%v", got.String(), err)
	}
}

func TestOperaSSESkipsThinkingStatusPayload(t *testing.T) {
	var got strings.Builder
	err := parseOperaSSE(context.Background(), strings.NewReader(
		"event: thinking_status\n\n"+
			"data: {\"response\":{\"content_type\":\"text\",\"message\":\"thinking\"}}\n\n"+
			"data: {\"response\":{\"content_type\":\"text\",\"message\":\"answer\"}}\n\n",
	), "v2", func(text string) error {
		got.WriteString(text)
		return nil
	})
	if err != nil || got.String() != "answer" {
		t.Fatalf("Opera thinking payload parse = %q, err=%v", got.String(), err)
	}
}

func TestNotionPatchStateAccumulatesOrderedDeltas(t *testing.T) {
	raw := []byte(
		`{"type":"patch","v":[{"o":"p","p":"/steps/0/value","v":"delta one"}]}` + "\n" +
			`{"type":"patch","v":[{"o":"x","p":"/steps/0/value","v":" delta two"}]}` + "\n",
	)
	text, inBandError := parseNotionText(raw)
	if inBandError || text != "delta one delta two" {
		t.Fatalf("Notion patch parse = %q, error=%v", text, inBandError)
	}
}

func TestNotionNestedErrorIsDetected(t *testing.T) {
	raw := []byte(`{"type":"patch-start","data":{"s":[{"type":"error","subType":"temporarily-unavailable"}]}}`)
	_, inBandError := parseNotionText(raw)
	if !inBandError {
		t.Fatal("Notion nested patch-start error was not detected")
	}
}

func TestUnsupportedIsHTTP422(t *testing.T) {
	var err error = ErrUnsupported
	e, ok := err.(interface{ HTTPStatus() int })
	if !ok || e.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatal("ErrUnsupported is not HTTP 422")
	}
	if errors.Is(err, errors.New("different")) {
		t.Fatal("unexpected errors.Is")
	}
}

func TestGoogleAIModeRequiresConfiguredBrowser(t *testing.T) {
	c := New(config.Source{Adapter: AdapterGoogleAI})
	defer c.Close()
	_, err := c.Do(context.Background(), "chat", "ai-mode", false, chatBody("ai-mode", false), nil)
	var classified interface{ HTTPStatus() int }
	if err == nil || !errors.As(err, &classified) || classified.HTTPStatus() != http.StatusServiceUnavailable {
		t.Fatalf("Google AI Mode browser requirement = %v", err)
	}
}

func TestGoogleSearchResultFormatting(t *testing.T) {
	got := formatGoogleSearchResults([]googleSearchResult{{Title: "Result", Link: "https://example.test/?srsltid=ignored", Snippet: "snippet"}})
	if !strings.Contains(got, "> **Result**") || !strings.Contains(got, "https://example.test/?srsltid=ignored") {
		t.Fatalf("Google result formatting = %q", got)
	}
}

func TestGoogleAIModeBrowserFixture(t *testing.T) {
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
<h3>Fixture result</h3><a href="https://example.test/result">fixture</a>
<button id="ai">AI Mode</button>
<script>
 document.getElementById('ai').addEventListener('click',()=>{
  const root=document.createElement('div'); root.setAttribute('decode-data-ved','1');
  root.appendChild(document.createTextNode('answer one'));
  const middle=document.createElement('span'); middle.textContent=' answer two'; root.appendChild(middle);
  document.body.appendChild(root);
  // Keep a partial answer visible longer than one polling interval. The
  // explicit disclaimer appears only after the answer is complete.
  setTimeout(()=>{
   const end=document.createElement('span'); end.textContent='AI responses may include mistakes.'; root.appendChild(end);
   const footer=document.createElement('span'); footer.textContent='footer must not be emitted'; root.appendChild(footer);
  },1000);
 });
</script></body></html>`)
	}))
	defer server.Close()

	c := New(config.Source{Adapter: AdapterGoogleAI}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	answer, err := c.googleAIModeTextAt(context.Background(), "fixture query", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(answer, "answer one\nanswer two") {
		t.Fatalf("fixture answer = %q", answer)
	}
	if strings.Index(answer, "answer one") > strings.Index(answer, "answer two") {
		t.Fatalf("fixture text order = %q", answer)
	}
	if strings.Contains(answer, "footer must not be emitted") {
		t.Fatalf("fixture leaked text after completion disclaimer: %q", answer)
	}
}

func TestGoogleAIModeBrowserFixtureBounds(t *testing.T) {
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
	large := strings.Repeat("x", (2<<20)+1000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body><button id="ai">AI Mode</button><script>
document.getElementById('ai').addEventListener('click',()=>{
 const root=document.createElement('div'); root.setAttribute('decode-data-ved','1');
 const text=document.createTextNode('`+large+`'); root.appendChild(text); document.body.appendChild(root);
});
</script></body></html>`)
	}))
	defer server.Close()
	c := New(config.Source{Adapter: AdapterGoogleAI}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	_, err = c.googleAIModeTextAt(context.Background(), "fixture bounds", server.URL)
	if err == nil || !strings.Contains(err.Error(), "response exceeds limit") {
		t.Fatalf("fixture overflow error = %v", err)
	}
}
