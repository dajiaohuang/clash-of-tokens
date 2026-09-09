package majorweb

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
)

func geminiFrame(text string) string {
	return geminiFrameRecords(text)
}

func geminiFrameRecords(texts ...string) string {
	inner := make([]any, 5)
	outer := make([]any, 0, len(texts))
	for _, text := range texts {
		inner[4] = []any{[]any{nil, []string{text}}}
		payload, _ := json.Marshal(inner)
		outer = append(outer, []any{"wrb.fr", nil, string(payload)})
	}
	data, _ := json.Marshal(outer)
	return string(data)
}

func geminiBatch(frames ...string) string {
	var b strings.Builder
	b.WriteString(")]}'\n")
	for _, frame := range frames {
		b.WriteString(strconv.Itoa(len([]byte(frame))))
		b.WriteByte('\n')
		b.WriteString(frame)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestParseGeminiStreamResponseUsesFinalCumulativeSnapshot(t *testing.T) {
	raw := geminiBatch(geminiFrame("Hello"), geminiFrame("Hello from Gemini"))
	got, err := parseGeminiStreamResponse([]byte(raw))
	if err != nil || got != "Hello from Gemini" {
		t.Fatalf("answer = %q, err = %v", got, err)
	}
}

func TestParseGeminiStreamResponseRejectsEmptyOrMalformedBody(t *testing.T) {
	for _, raw := range []string{"", ")]}'\n3\nabc", ")]}'\n9223372036854775807\n[]", geminiBatch(`not-json`), geminiBatch(`[["wrb.fr",null,"[]"]]`)} {
		if _, err := parseGeminiStreamResponse([]byte(raw)); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestParseGeminiStreamResponseRejectsMalformedFinalFrameAndErrors(t *testing.T) {
	valid := geminiFrame("partial")
	final := geminiFrame("complete")
	truncated := ")]}'\n" + strconv.Itoa(len([]byte(valid))) + "\n" + valid + "\n" + strconv.Itoa(len([]byte(final))+1) + "\n" + final[:len(final)-1]
	if _, err := parseGeminiStreamResponse([]byte(truncated)); err == nil {
		t.Fatal("accepted truncated final frame after valid partial")
	}
	lengthMismatch := ")]}'\n" + strconv.Itoa(len([]byte(final))+1) + "\n" + final + "\n"
	if _, err := parseGeminiStreamResponse([]byte(lengthMismatch)); err == nil {
		t.Fatal("accepted frame with mismatched byte length")
	}
	errorFrame := `[["error",null,"upstream rejected"]]`
	if _, err := parseGeminiStreamResponse([]byte(geminiBatch(valid, errorFrame))); err == nil || strings.Contains(err.Error(), "upstream rejected") {
		t.Fatalf("error frame was not rejected safely: %v", err)
	}
	if _, err := parseGeminiStreamResponse([]byte(geminiBatch(geminiFrame("answer"), geminiFrame("")))); err == nil {
		t.Fatal("final empty snapshot resurrected previous text")
	}
}

func TestParseGeminiStreamResponseVisitsAllRecordsInFrame(t *testing.T) {
	got, err := parseGeminiStreamResponse([]byte(geminiBatch(geminiFrameRecords("first", "final"))))
	if err != nil || got != "final" {
		t.Fatalf("same-frame snapshots = %q, err=%v", got, err)
	}
}

func TestGeminiWebRequiresBrowserModelSelector(t *testing.T) {
	c := New(config.Source{Adapter: AdapterGeminiWeb, BaseURL: "http://127.0.0.1:1"})
	defer c.Close()
	if _, err := c.Do(context.Background(), "chat", "gemini-3.1-pro", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil); err == nil || !strings.Contains(err.Error(), "model web") {
		t.Fatalf("arbitrary Gemini model was accepted: %v", err)
	}
}

func TestGeminiWebBrowserFixture(t *testing.T) {
	cdp := os.Getenv("COT_TEST_CDP")
	if cdp == "" {
		t.Skip("set COT_TEST_CDP for local browser fixture")
	}

	const prompt = "fixture prompt"
	const answer = "Hello from Gemini fixture"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_/BardChatUi/data/StreamGenerate":
			calls++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), prompt) {
				t.Errorf("prompt not forwarded: %q", body)
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, geminiBatch(geminiFrame("Hello"), geminiFrame(answer)))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			page := `<!doctype html><html><body><div class="ql-editor" contenteditable="true"></div><script>
const e=document.querySelector('.ql-editor'); e.addEventListener('keydown',ev=>{if(ev.key==='Enter'){ev.preventDefault();fetch('/_/BardChatUi/data/StreamGenerate',{method:'POST',body:e.textContent});}});
</script></body></html>`
			_, _ = io.WriteString(w, page)
		}
	}))
	defer server.Close()

	for _, stream := range []bool{false, true} {
		c := New(config.Source{Adapter: AdapterGeminiWeb, BaseURL: server.URL}, config.Browser{Enabled: true, CDPURL: cdp})
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
		t.Fatalf("fixture calls = %d, want 2", calls)
	}
}

func TestGeminiWebCaptureScriptHasBoundedStreamCapture(t *testing.T) {
	script := geminiCaptureScript(strconv.Quote(`{"marker":"StreamGenerate"}`))
	if !strings.Contains(script, "window.__cotGemini") || !strings.Contains(script, "limit=8388608") || !strings.Contains(script, "StreamGenerate") || !strings.Contains(script, "p.origin===location.origin") || !strings.Contains(script, "p.pathname.includes") || !strings.Contains(script, "addEventListener('progress'") {
		t.Fatalf("capture script lacks required safeguards")
	}
}
