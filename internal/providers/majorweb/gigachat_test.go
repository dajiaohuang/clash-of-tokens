package majorweb

import (
	"context"
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

func gigaEvent(status string, extra string) string {
	return "data: {\"status\":\"" + status + "\"" + extra + "}\n\n"
}

func gigaFixtureStream() string {
	return gigaEvent("ACCEPTED", ",\"session\":{\"id\":\"session-1\"},\"requestMessageId\":\"request-1\",\"message\":{\"id\":\"assistant-1\",\"value\":\"\"}") +
		gigaEvent("IN_PROGRESS", ",\"contentDelta\":[{\"id\":\"delta-1\",\"role\":\"ASSISTANT\",\"delta\":\"Hello\"}]") +
		gigaEvent("READY", ",\"message\":{\"id\":\"assistant-1\",\"value\":\"Hello from GigaChat fixture\"}")
}

func TestParseGigaChatStreamResponseRequiresReadyAndCorrelatesMessage(t *testing.T) {
	answer, err := parseGigaChatStreamResponse([]byte(gigaFixtureStream()))
	if err != nil || answer != "Hello from GigaChat fixture" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
	if _, err := parseGigaChatStreamResponse([]byte(strings.Replace(gigaFixtureStream(), gigaEvent("READY", ",\"message\":{\"id\":\"assistant-1\",\"value\":\"Hello from GigaChat fixture\"}"), "", 1))); !errors.Is(err, ErrTruncated) {
		t.Fatalf("missing READY err=%v, want ErrTruncated", err)
	}
	badID := strings.Replace(gigaFixtureStream(), `"id":"assistant-1","value":"Hello`, `"id":"other-assistant","value":"Hello`, 1)
	if _, err := parseGigaChatStreamResponse([]byte(badID)); err == nil {
		t.Fatal("accepted READY message with mismatched id")
	}
	if _, err := parseGigaChatStreamResponse([]byte(gigaEvent("IN_PROGRESS", ",\"contentDelta\":[]"))); err == nil {
		t.Fatal("accepted IN_PROGRESS before ACCEPTED")
	}
	emptyReady := gigaEvent("ACCEPTED", ",\"sessionId\":\"session-1\",\"requestMessageId\":\"request-1\",\"message\":{\"id\":\"assistant-1\",\"value\":\"\"}") +
		gigaEvent("IN_PROGRESS", ",\"contentDelta\":[{\"role\":\"ASSISTANT\",\"delta\":\"partial\"}]") +
		gigaEvent("READY", ",\"message\":{\"id\":\"assistant-1\",\"value\":\"\"}")
	if _, err := parseGigaChatStreamResponse([]byte(emptyReady)); err == nil {
		t.Fatal("accepted authoritative empty READY value")
	}
	upstreamError := gigaEvent("ERROR", ",\"error\":{\"message\":\"secret upstream details\"}")
	if _, err := parseGigaChatStreamResponse([]byte(upstreamError)); err == nil || strings.Contains(err.Error(), "secret upstream details") {
		t.Fatalf("upstream error leaked or was accepted: %v", err)
	}
	unknown := gigaEvent("PAUSED", "")
	if _, err := parseGigaChatStreamResponse([]byte(unknown)); err == nil {
		t.Fatal("accepted unknown session status")
	}
	afterReady := gigaFixtureStream() + gigaEvent("IN_PROGRESS", ",\"contentDelta\":[{\"role\":\"ASSISTANT\",\"delta\":\"late\"}]")
	if _, err := parseGigaChatStreamResponse([]byte(afterReady)); err == nil {
		t.Fatal("accepted event after READY")
	}
	mismatchedSession := gigaEvent("ACCEPTED", ",\"sessionId\":\"session-1\",\"requestMessageId\":\"request-1\",\"message\":{\"id\":\"assistant-1\",\"value\":\"\"}") +
		gigaEvent("IN_PROGRESS", ",\"sessionId\":\"session-2\",\"contentDelta\":[]")
	if _, err := parseGigaChatStreamResponse([]byte(mismatchedSession)); err == nil {
		t.Fatal("accepted mismatched session correlation")
	}
	optionalMessageIDs := gigaEvent("ACCEPTED", ",\"sessionId\":\"session-1\",\"requestMessageId\":\"request-1\",\"message\":{\"value\":\"\"}") +
		gigaEvent("READY", ",\"message\":{\"value\":\"answer without optional ids\"}")
	if got, err := parseGigaChatStreamResponse([]byte(optionalMessageIDs)); err != nil || got != "answer without optional ids" {
		t.Fatalf("optional message IDs: got=%q err=%v", got, err)
	}
}

func TestGigaChatWebRequiresBrowserAndWebModel(t *testing.T) {
	c := New(config.Source{Adapter: AdapterGigaChatWeb, BaseURL: "http://127.0.0.1:1"})
	defer c.Close()
	body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
	if _, err := c.Do(context.Background(), "chat", "gigachat-pro", false, body, nil); err == nil || !strings.Contains(err.Error(), "model web") {
		t.Fatalf("arbitrary GigaChat model was accepted: %v", err)
	}
	if _, err := c.Do(context.Background(), "chat", "web", false, body, nil); err == nil || !strings.Contains(err.Error(), "configured running Chrome") {
		t.Fatalf("missing browser was not rejected: %v", err)
	}
}

func TestGigaChatWebBrowserFixture(t *testing.T) {
	cdp := strings.TrimSpace(os.Getenv("COT_TEST_CDP"))
	if cdp == "" {
		t.Skip("set COT_TEST_CDP for local browser fixture")
	}
	const prompt = "fixture prompt"
	const answer = "Hello from GigaChat fixture"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/sessions/request" {
			calls++
			data, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(data), prompt) {
				t.Errorf("prompt not forwarded: %q", data)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, gigaFixtureStream())
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
<form id="chat-form"><textarea id="chat-input-textarea"></textarea><div id="chat-input:submit"><button type="button">send</button></div></form>
<script>const e=document.getElementById('chat-input-textarea');document.querySelector('#chat-input\\:submit button').addEventListener('click',()=>fetch('/api/v0/sessions/request',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({text:e.value})}));</script>
</body></html>`)
	}))
	defer server.Close()

	for _, stream := range []bool{false, true} {
		c := New(config.Source{Adapter: AdapterGigaChatWeb, BaseURL: server.URL}, config.Browser{Enabled: true, CDPURL: cdp})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		resp, err := c.Do(ctx, "chat", "web", stream, []byte(`{"messages":[{"role":"user","content":"`+prompt+`"}]}`), nil)
		cancel()
		if err != nil {
			c.Close()
			t.Fatalf("stream=%v: %v", stream, err)
		}
		data, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		c.Close()
		if readErr != nil || !strings.Contains(string(data), answer) {
			t.Fatalf("stream=%v body=%s err=%v", stream, data, readErr)
		}
		if stream && !strings.Contains(string(data), "[DONE]") {
			t.Fatalf("stream response missing terminal marker: %s", data)
		}
	}
	if calls != 2 {
		t.Fatalf("fixture calls=%d, want 2", calls)
	}
}

func TestGigaChatCaptureScriptIsBoundedAndSameOrigin(t *testing.T) {
	script := gigaChatCaptureScript(`{"kind":"sessions"}`)
	for _, part := range []string{"window.__cotGigaChat", "limit=8388608", "toUpperCase()==='POST'", "p.origin===location.origin", "sessions(?:", "getReader", "loadend", "catch(e){if(own)finish(false,'');throw e}"} {
		if !strings.Contains(script, part) {
			t.Fatalf("capture script missing %q", part)
		}
	}
}
