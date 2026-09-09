package upstream

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func TestNativeAdapterContracts(t *testing.T) {
	for _, tc := range []struct {
		adapter, protocol, path, auth string
		stream                        bool
	}{{"openai", "chat", "/v1/chat/completions", "Authorization", false}, {"openai", "responses", "/v1/responses", "Authorization", true}, {"anthropic", "messages", "/v1/messages", "X-Api-Key", true}, {"gemini", "gemini", "/v1/models/real:streamGenerateContent", "X-Goog-Api-Key", true}, {"claude-code", "messages", "/v1/messages", "Authorization", true}, {"qwen-code", "chat", "/v1/chat/completions", "Authorization", true}, {"kimi-code", "messages", "/v1/messages", "Authorization", true}, {"codex", "responses", "/v1/responses", "Authorization", true}} {
		t.Run(tc.adapter+"/"+tc.protocol, func(t *testing.T) {
			t.Setenv("UPSTREAM_TEST_KEY", "upstream-secret")
			s := config.Source{Adapter: tc.adapter, BaseURL: "https://upstream.example/v1", KeyEnv: "UPSTREAM_TEST_KEY", MaxInflight: 1}
			c := New(s)
			defer c.Close()
			c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.path {
					t.Errorf("path=%s", r.URL.Path)
				}
				if !strings.Contains(r.Header.Get(tc.auth), "upstream-secret") {
					t.Error("incorrect authentication")
				}
				if r.Header.Get("Cookie") != "" || r.Header.Get("X-Internal-Secret") != "" {
					t.Error("client headers leaked")
				}
				b, _ := io.ReadAll(r.Body)
				r.Body.Close()
				if string(b) != `{"model":"real","unknown":{"deep":1}}` {
					t.Error(string(b))
				}
				return response(`{}`), nil
			})
			r, e := c.Do(context.Background(), tc.protocol, "real", tc.stream, []byte(`{"model":"real","unknown":{"deep":1}}`), http.Header{"Cookie": []string{"secret"}, "X-Internal-Secret": []string{"hidden"}})
			if e != nil {
				t.Fatal(e)
			}
			r.Body.Close()
		})
	}
}
func TestCloudCodeNativeEnvelope(t *testing.T) {
	t.Setenv("UPSTREAM_TEST_KEY", "token")
	s := config.Source{Adapter: "gemini-cli", BaseURL: "https://cloudcode-pa.googleapis.com/v1internal", KeyEnv: "UPSTREAM_TEST_KEY", Project: "my-project", MaxInflight: 1}
	c := New(s)
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Fatal(r.URL.String())
		}
		var v struct {
			Model, Project string
			Request        json.RawMessage
		}
		_ = json.NewDecoder(r.Body).Decode(&v)
		r.Body.Close()
		if v.Model != "real" || v.Project != "my-project" || !strings.Contains(string(v.Request), "contents") {
			t.Fatal(v)
		}
		out := response("data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\r\n\r\n")
		out.Header.Set("Content-Type", "text/event-stream")
		return out, nil
	})
	r, e := c.Do(context.Background(), "gemini", "real", true, []byte(`{"contents":[]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, e := io.ReadAll(r.Body)
	if e != nil || string(b) != "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n" {
		t.Fatalf("%s %v", b, e)
	}
}
func TestCopilotRefreshSingleFlightAndEndpointGuard(t *testing.T) {
	t.Setenv("UPSTREAM_TEST_KEY", "github-token")
	c := New(config.Source{Adapter: "copilot", KeyEnv: "UPSTREAM_TEST_KEY", MaxInflight: 8})
	var exchanges atomic.Int32
	c.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.github.com" {
			exchanges.Add(1)
			time.Sleep(10 * time.Millisecond)
			return response(`{"token":"copilot-token","refresh_in":600,"endpoints":{"api":"https://api.individual.githubcopilot.com"}}`), nil
		}
		if r.Header.Get("Authorization") != "Bearer copilot-token" || r.URL.Host != "api.individual.githubcopilot.com" {
			t.Error("wrong copilot endpoint or token")
		}
		r.Body.Close()
		return response(`{}`), nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := c.Do(context.Background(), "chat", "model", false, []byte(`{}`), nil)
			if e != nil {
				t.Error(e)
			} else {
				r.Body.Close()
			}
		}()
	}
	wg.Wait()
	if exchanges.Load() != 1 {
		t.Fatal(exchanges.Load())
	}
	for _, endpoint := range []string{"https://attacker.example", "https://api.githubcopilot.com.attacker.example", "http://api.githubcopilot.com", "https://user@api.githubcopilot.com"} {
		t.Run(endpoint, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(fmt.Sprintf(`{"token":"secret","refresh_in":600,"endpoints":{"api":%q}}`, endpoint)), nil
			})}
			if _, _, _, e := exchange(context.Background(), client, "token"); e == nil {
				t.Fatal("untrusted endpoint accepted")
			}
		})
	}
}
