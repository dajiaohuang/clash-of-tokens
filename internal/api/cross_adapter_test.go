package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"clash-of-tokens/internal/config"
)

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

// TestCrossAdapterReplayAndCompletion exercises the shared gateway boundary
// with the three native generic API protocols. Each request is replayed from
// the original JSON body with the selected upstream model, and each stream is
// accepted only when its protocol-specific terminal marker is observed.
func TestCrossAdapterReplayAndCompletion(t *testing.T) {
	type caseDef struct {
		name, adapter, protocol, path, model, upstream, credential string
		streamBody, responseBody                                   string
		wantPath, wantAuth                                         string
	}
	cases := []caseDef{
		{
			name: "openai chat", adapter: "openai", protocol: "chat",
			path: "/v1/chat/completions", model: "openai-model", upstream: "openai-upstream", credential: "openai-secret",
			streamBody:   "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
			responseBody: `{"choices":[{"message":{"content":"ok"}}]}`,
			wantPath:     "/v1/chat/completions", wantAuth: "Bearer openai-secret",
		},
		{
			name: "anthropic messages", adapter: "anthropic", protocol: "messages",
			path: "/v1/messages", model: "claude-model", upstream: "claude-upstream", credential: "claude-secret",
			streamBody:   "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":2}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			responseBody: `{"type":"message","content":[{"type":"text","text":"ok"}]}`,
			wantPath:     "/v1/messages", wantAuth: "Bearer claude-secret",
		},
		{
			name: "gemini", adapter: "gemini", protocol: "gemini",
			path: "/v1beta/models/gemini-model:generateContent", model: "gemini-model", upstream: "gemini-upstream", credential: "gemini-secret",
			streamBody:   "data: {\"candidates\":[{\"index\":0,\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n",
			responseBody: `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`,
			wantPath:     "/v1/models/gemini-upstream:generateContent", wantAuth: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []struct {
				path, model, authorization, apiKey, anthropicKey string
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request: %v", err)
				}
				var payload map[string]any
				if json.Unmarshal(body, &payload) != nil {
					t.Errorf("invalid rewritten JSON: %s", body)
				}
				mu.Lock()
				requests = append(requests, struct {
					path, model, authorization, apiKey, anthropicKey string
				}{r.URL.Path, stringValue(payload["model"]), r.Header.Get("Authorization"), r.Header.Get("x-goog-api-key"), r.Header.Get("x-api-key")})
				mu.Unlock()
				stream := r.Header.Get("Accept") == "text/event-stream"
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, tc.streamBody)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.responseBody)
			}))
			defer upstream.Close()

			c := config.Default()
			c.Sources = []config.Source{{
				ID: "source", Provider: tc.adapter, Adapter: tc.adapter, BaseURL: upstream.URL + "/v1", CredentialRef: "cred://" + tc.adapter,
				SourceKind: "vendor_api", ExecutionLocation: "local", InferenceLocation: "remote", BillingMode: "free_allowance", CredentialMode: "api_key",
				Enabled: true, AutoApproved: true, Local: true, MaxInflight: 2, QuotaDomain: "source", QuotaMaxInflight: 2,
				Models: []config.Model{{ID: tc.model, Upstream: tc.upstream, Protocols: []string{tc.protocol}, Tier: "unrated", Tools: "none", MaxInputBytes: 4096}},
			}}
			// Gemini uses the URL model selector and does not accept a model field
			// in the JSON body. The generic client rewrites the body accordingly.
			if tc.adapter == "gemini" {
				c.Sources[0].SourceKind = "cloud_api"
				c.Sources[0].Local = true
			}
			s, err := NewWithCredentials(c, testKey, adminKey, func(ref string) string {
				if ref == "cred://"+tc.adapter {
					return tc.credential
				}
				return ""
			})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			gateway := httptest.NewServer(s)
			defer gateway.Close()

			body := `{"model":"source/` + tc.model + `","stream":false}`
			if tc.adapter == "gemini" {
				body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`
			}
			res := request(t, gateway.URL, tc.path, body, testKey)
			response, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil || res.StatusCode != http.StatusOK || !strings.Contains(string(response), "ok") {
				t.Fatalf("non-stream response status=%d err=%v body=%s", res.StatusCode, err, response)
			}

			body = `{"model":"source/` + tc.model + `","stream":true}`
			if tc.adapter == "gemini" {
				body = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"stream":true}}`
			}
			res = request(t, gateway.URL, tc.path, body, testKey)
			streamResponse, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil || res.StatusCode != http.StatusOK || !strings.Contains(string(streamResponse), "ok") {
				t.Fatalf("stream response status=%d err=%v body=%s", res.StatusCode, err, streamResponse)
			}

			mu.Lock()
			got := append([]struct {
				path, model, authorization, apiKey, anthropicKey string
			}{}, requests...)
			mu.Unlock()
			if len(got) != 2 {
				t.Fatalf("upstream requests=%d want 2", len(got))
			}
			for _, seen := range got {
				wantModel := tc.upstream
				if tc.adapter == "gemini" {
					wantModel = ""
				}
				if seen.path != tc.wantPath || seen.model != wantModel {
					t.Fatalf("rewritten request=%+v want path=%s model=%s", seen, tc.wantPath, tc.upstream)
				}
				if tc.adapter == "anthropic" && seen.anthropicKey != tc.credential {
					t.Fatalf("x-api-key=%q want %q", seen.anthropicKey, tc.credential)
				}
				if tc.adapter != "anthropic" && tc.wantAuth != "" && seen.authorization != tc.wantAuth {
					t.Fatalf("authorization=%q want %q", seen.authorization, tc.wantAuth)
				}
				if tc.adapter == "gemini" && seen.apiKey != tc.credential {
					t.Fatalf("x-goog-api-key=%q want %q", seen.apiKey, tc.credential)
				}
			}
		})
	}
}
