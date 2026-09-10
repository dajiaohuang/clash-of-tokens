package upstream

import (
	"clash-of-tokens/internal/config"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoveryPaginationAndCredentialHeaders(t *testing.T) {
	for _, adapter := range []string{"openai", "anthropic", "gemini"} {
		t.Run(adapter, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/models" {
					t.Error("wrong discovery request")
				}
				if r.Header.Get("Authorization") != "Bearer test-secret" && r.Header.Get("x-api-key") != "test-secret" && r.Header.Get("x-goog-api-key") != "test-secret" {
					t.Error("missing credential")
				}
				if calls == 2 && r.URL.Query().Get("after_id") != "first" && r.URL.Query().Get("pageToken") != "next" {
					t.Error("missing cursor")
				}
				switch adapter {
				case "gemini":
					if calls == 1 {
						fmt.Fprint(w, `{"models":[{"name":"models/first","displayName":"First model","description":"bounded description","inputTokenLimit":1234,"outputTokenLimit":567,"supportedGenerationMethods":["generateContent"]}],"nextPageToken":"next"}`)
					} else {
						fmt.Fprint(w, `{"models":[{"name":"models/second"}]}`)
					}
				case "anthropic":
					if calls == 1 {
						fmt.Fprint(w, `{"data":[{"id":"first","display_name":"First model","created_at":"2026-01-02T03:04:05Z"}],"has_more":true,"last_id":"first"}`)
					} else {
						fmt.Fprint(w, `{"data":[{"id":"second"}],"has_more":false}`)
					}
				default:
					fmt.Fprint(w, `{"data":[{"id":"first","owned_by":"owner","created":1700000000},{"id":"second"}]}`)
				}
			}))
			defer server.Close()
			t.Setenv("DISCOVERY_TEST_KEY", "test-secret")
			c := newHTTP(config.Source{Adapter: adapter, BaseURL: server.URL + "/v1", KeyEnv: "DISCOVERY_TEST_KEY", MaxInflight: 1})
			defer c.Close()
			result, err := c.Discover(context.Background())
			if err != nil || !result.Complete || len(result.Models) != 2 {
				t.Fatal(result, err)
			}
			switch adapter {
			case "gemini":
				if result.Models[0].DisplayName != "First model" || result.Models[0].InputTokenLimit != 1234 || result.Models[0].OutputTokenLimit != 567 || len(result.Models[0].SupportedMethods) != 1 {
					t.Fatalf("gemini metadata missing: %+v", result.Models[0])
				}
			case "anthropic":
				if result.Models[0].DisplayName != "First model" || result.Models[0].CreatedUnix == 0 {
					t.Fatalf("anthropic metadata missing: %+v", result.Models[0])
				}
			default:
				if result.Models[0].OwnedBy != "owner" || result.Models[0].CreatedUnix != 1700000000 {
					t.Fatalf("openai metadata missing: %+v", result.Models[0])
				}
			}
		})
	}
}

func TestDiscoveryRejectsMissingListAndRepeatedCursor(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":[],"has_more":true,"last_id":"repeat"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		c := newHTTP(config.Source{Adapter: "anthropic", BaseURL: server.URL, Local: true, MaxInflight: 1})
		result, err := c.Discover(context.Background())
		c.Close()
		server.Close()
		if err == nil || result.Complete {
			t.Fatal("invalid discovery reported complete")
		}
	}
}
