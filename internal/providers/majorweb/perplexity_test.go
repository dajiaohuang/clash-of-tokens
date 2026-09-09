package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPerplexityWorkflowAndTerminal(t *testing.T) {
	t.Setenv("PPLX_TEST", "own-cookie")
	for _, mode := range []string{"ok", "error", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/rest/sse/perplexity_ask" || r.Header.Get("Cookie") != "__Secure-next-auth.session-token=own-cookie" || r.Header.Get("Authorization") != "" {
					t.Error("routing")
				}
				var p map[string]any
				json.NewDecoder(r.Body).Decode(&p)
				params, _ := p["params"].(map[string]any)
				if p["query_str"] != "hello" || params["mode"] != "copilot" || params["model_preference"] != "pplx_pro" || params["version"] != "2.18" || params["use_schematized_api"] != true || params["last_backend_uuid"] != nil {
					t.Errorf("body %#v", p)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: "+`{"blocks":[{"intended_usage":"workflow_root","diff_block":{"field":"workflow_block","patches":[{"op":"add","path":"/steps/0","value":{"items":[{"type":"WORKFLOW_ITEM_TEXT","payload":{"text_payload":{"variant":"thinking","chunks":["private thought"]}}},{"type":"WORKFLOW_ITEM_TEXT","payload":{"text_payload":{"variant":"answer","chunks":["hello"]}}}]}}]}}]}`+"\n\n")
				io.WriteString(w, "data: "+`{"blocks":[{"intended_usage":"workflow_root","diff_block":{"field":"workflow_block","patches":[{"op":"add","path":"/steps/0/items/1/payload/text_payload/chunks/1","value":" world"}]}}]}`+"\n\n")
				switch mode {
				case "ok":
					io.WriteString(w, "event: end_of_stream\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				case "error":
					io.WriteString(w, "data: "+`{"status":"FAILED","error_message":"secret"}`+"\n\n")
				}
			}))
			defer s.Close()
			c := New(config.Source{Adapter: "perplexity-web", BaseURL: s.URL, KeyEnv: "PPLX_TEST"})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			resp, err := c.Do(ctx, "chat", "pplx_pro", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), http.Header{"Authorization": {"Bearer caller"}})
			if mode != "ok" {
				if err == nil {
					resp.Body.Close()
					t.Fatal("expected failure")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("leak")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(b), "hello world") || strings.Contains(string(b), "private thought") || ctx.Err() != nil {
				t.Fatalf("%s", b)
			}
		})
	}
}

func TestPerplexityFinalTextAndPatchBudget(t *testing.T) {
	raw := `[{"step_type":"FINAL","content":{"answer":"{\"answer\":\"final reply\"}"}}]`
	if s := pplxFinalText(raw); s != "final reply" {
		t.Fatal(s)
	}
	slots := 0
	var root any
	for i := 0; i < 5; i++ {
		_, err := pplxPatch(root, []string{"16384"}, "add", "x", 0, &slots)
		if i < 3 && err != nil {
			t.Fatal(err)
		}
		if i == 4 && err == nil {
			t.Fatal("unbounded sparse arrays")
		}
	}
}
