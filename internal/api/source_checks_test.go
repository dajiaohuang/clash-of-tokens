package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestExplicitValidationOfDisabledSource(t *testing.T) {
	for _, complete := range []bool{true, false} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			calls := 0
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
				if complete {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer up.Close()
			c := config.Default()
			c.Sources = []config.Source{{ID: "test", Provider: "p", Adapter: "openai", BaseURL: up.URL, Local: true, Enabled: false, MaxInflight: 1, QuotaDomain: "test", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
			dir := t.TempDir()
			vault, _ := credentials.Open(filepath.Join(dir, "vault"))
			p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			req := httptest.NewRequest("POST", "/admin/sources/test/validate", strings.NewReader(`{"model":"model","protocol":"chat"}`))
			req.Header.Set("Authorization", "Bearer "+adminKey)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, req)
			var evidence ValidationEvidence
			if json.Unmarshal(w.Body.Bytes(), &evidence) != nil || w.Code != 200 || evidence.Verified != complete || !evidence.OutputObserved {
				t.Fatal(w.Code, w.Body.String())
			}
			if calls != 1 || p.current.server.Router.Status()[0].Enabled || p.current.server.Router.Status()[0].Active != 0 {
				t.Fatal("validation changed routing or leaked capacity")
			}
		})
	}
}
