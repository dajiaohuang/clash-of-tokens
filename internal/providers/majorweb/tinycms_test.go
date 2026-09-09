package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTinyCMSBrowserSigningContract(t *testing.T) {
	cdp := os.Getenv("COT_TEST_CDP")
	if cdp == "" {
		t.Skip("requires Chrome CDP; local protocol fixture only")
	}
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/challenge":
			json.NewEncoder(w).Encode(map[string]any{"challenge": "fixture-challenge", "challengeId": "fixture-id", "version": "1", "expiresAt": time.Now().Add(time.Minute).UnixMilli(), "difficulty": 1})
		case "/api/openai/oneapi/v1/chat/completions":
			sent = true
			for _, k := range []string{"x-secure-signature", "x-secure-fingerprint", "x-secure-pow-hash", "x-secure-pow-nonce", "x-secure-pow-seed-nonce"} {
				if r.Header.Get(k) == "" {
					t.Error("missing " + k)
				}
			}
			if r.Header.Get("uuid") != "Rfixture-owned-identity" || r.Header.Get("x-secure-client-ip") != "127.0.0.1" || r.Header.Get("x-secure-pow-difficulty") != "1" || r.Header.Get("x-session-id") != r.Header.Get("x-secure-nonce") {
				t.Error("wrong signature inputs")
			}
			io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		default:
			t.Error("unexpected request")
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("COT_TEST_TINYCMS", `{"key":"Rfixture-owned-identity","client_ip":"127.0.0.1"}`)
	c := New(config.Source{Adapter: "tinycms-web", BaseURL: srv.URL, KeyEnv: "COT_TEST_TINYCMS"}, config.Browser{Enabled: true, CDPURL: cdp})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	r, e := c.Do(ctx, "chat", "fixture-model", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !sent || !strings.Contains(string(b), "answer") {
		t.Fatal("missing response")
	}
}

func TestTinyCMSRejectsIncompleteOrError(t *testing.T) {
	for _, raw := range []string{"data: [DONE]\n\n", "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", "data: {\"error\":\"secret\"}\n\n", "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"} {
		r := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw))}
		if _, e := tinyResponse(r, true, "x"); e == nil {
			t.Fatal("accepted invalid stream")
		} else if strings.Contains(e.Error(), "secret") {
			t.Fatal("leaked error")
		}
	}
}
