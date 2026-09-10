package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

const testKey = "test-data-key-123456789"
const adminKey = "test-admin-key-123456789"

func setup(t *testing.T, h http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	c := config.Default()
	c.Sources = []config.Source{{ID: "mock", Provider: "test", Adapter: "openai", BaseURL: up.URL + "/v1", Enabled: true, Local: true, MaxInflight: 4, QuotaDomain: "mock", QuotaMaxInflight: 4, Models: []config.Model{{ID: "model", Upstream: "real", Protocols: []string{"chat", "responses"}, Tier: "unrated", Tools: "native", MaxInputBytes: 16 << 20}}}}
	t.Setenv(c.APIKeyEnv, testKey)
	t.Setenv(c.AdminKeyEnv, adminKey)
	s, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	gateway := httptest.NewServer(s)
	t.Cleanup(gateway.Close)
	return s, gateway
}
func request(t *testing.T, url, path, body, key string) *http.Response {
	t.Helper()
	r, _ := http.NewRequest("POST", url+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	res, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	return res
}

func TestEmbeddedControlPlaneAssets(t *testing.T) {
	s, err := NewWithKeys(config.Default(), testKey, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gateway := httptest.NewServer(s)
	defer gateway.Close()
	for _, tc := range []struct {
		path, contentType, marker string
	}{
		{"/", "text/html; charset=utf-8", "/assets/control.js"},
		{"/assets/control.js", "text/javascript; charset=utf-8", "'use strict'"},
		{"/assets/control.css", "text/css; charset=utf-8", ":root"},
	} {
		res, err := http.Get(gateway.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil || res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != tc.contentType || !bytes.Contains(body, []byte(tc.marker)) {
			t.Fatalf("embedded asset %s: status=%d content-type=%q read=%v marker=%q", tc.path, res.StatusCode, res.Header.Get("Content-Type"), readErr, tc.marker)
		}
		if tc.path == "/" && !strings.Contains(res.Header.Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Fatalf("dashboard CSP missing same-origin connect restriction: %q", res.Header.Get("Content-Security-Policy"))
		}
	}
}

func TestFailureBoundaryRedactsSecretShapedMessages(t *testing.T) {
	for _, msg := range []string{
		"upstream rejected api_key=private-key-value",
		"authorization: Bearer private-token-value",
		"cookie=session-private-value",
		"password=private-password",
	} {
		w := httptest.NewRecorder()
		fail(w, http.StatusBadRequest, msg)
		if strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), msg) {
			t.Fatalf("secret-shaped error crossed boundary: %s", w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "request failed") {
			t.Fatalf("safe error category missing: %s", w.Body.String())
		}
	}
}

func TestStreamingPassthroughAndSecrets(t *testing.T) {
	wire := []byte("data: {\"text\":\"你好\"}\r\n\r\ndata: [DONE]\n\n")
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error(r.URL.Path)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("client credentials leaked")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"model":"real"`)) {
			t.Error(string(body))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, v := range wire {
			_, _ = w.Write([]byte{v})
			w.(http.Flusher).Flush()
		}
	})
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model","messages":[],"stream":true}`, testKey)
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !bytes.Equal(wire, body) {
		t.Fatalf("%d %q", res.StatusCode, body)
	}
	if s.buffered.Load() != 0 {
		t.Fatal("buffer leak")
	}
}

func TestAccountRoutingMetadataIsUsedWhenSourceOmitsOverrides(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Organization") != "account-org" || r.Header.Get("OpenAI-Project") != "account-project" {
			t.Errorf("account routing headers missing: organization=%q project=%q", r.Header.Get("OpenAI-Organization"), r.Header.Get("OpenAI-Project"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer up.Close()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "openai", Enabled: true}}
	c.Accounts = []config.Account{{ID: "account", ProviderID: "openai", Enabled: true, BaseURL: up.URL, Organization: "account-org", Project: "account-project", QuotaDomain: "quota", MaxInflight: 1, Weight: 1}}
	c.Sources = []config.Source{{ID: "source", Provider: "openai", Adapter: "openai", AccountID: "account", Local: true, Enabled: true, MaxInflight: 1, QuotaDomain: "quota", QuotaMaxInflight: 1, Models: []config.Model{{ID: "model", Upstream: "model", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1024}}}}
	s, err := NewWithKeys(c, testKey, adminKey)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	gateway := httptest.NewServer(s)
	defer gateway.Close()
	res := request(t, gateway.URL, "/v1/chat/completions", `{"model":"source/model","messages":[]}`, testKey)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("request status=%d body=%s", res.StatusCode, body)
	}
	res.Body.Close()
}

func TestNonStreamingResponseRecordsExecutionHealth(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[{"message":{"content":"OK"}}],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}}`)
	})
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model","messages":[]}`, testKey)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("non-stream request failed: %d", res.StatusCode)
	}
	state := s.Router.Status()[0]
	if state.Completed != 1 || state.Failures != 0 || state.LastHTTPStatus != 200 || state.InputTokens != 12 || state.OutputTokens != 4 || state.TotalTokens != 16 || state.LastExecution == nil || !state.LastExecution.UsageKnown {
		t.Fatalf("non-stream execution was not recorded: %+v", state)
	}
}

func TestNonStreamingUsageCaptureRemainsBounded(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"padding":"`+strings.Repeat("x", 1<<20)+`","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	})
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model","messages":[]}`, testKey)
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	state := s.Router.Status()[0]
	if res.StatusCode != 200 || state.LastExecution == nil || state.LastExecution.UsageKnown {
		t.Fatalf("oversized non-stream capture claimed usage: status=%d execution=%+v", res.StatusCode, state.LastExecution)
	}
	if s.buffered.Load() != 0 {
		t.Fatalf("capture reservation leaked: %d", s.buffered.Load())
	}
}
func TestAuthenticationAndBodyValidation(t *testing.T) {
	var calls atomic.Int32
	_, g := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, tc := range []struct {
		body, key string
		status    int
	}{{`{"model":"mock/model"}`, "wrong", 401}, {`{"model":"a","model":"b"}`, testKey, 400}, {`[]`, testKey, 400}} {
		res := request(t, g.URL, "/v1/chat/completions", tc.body, tc.key)
		res.Body.Close()
		if res.StatusCode != tc.status {
			t.Fatal(res.StatusCode)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid requests reached upstream")
	}
}
func TestNoRetryAndBlockedSource(t *testing.T) {
	var calls atomic.Int32
	_, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "secret upstream content")
	})
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model"}`, testKey)
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if bytes.Contains(b, []byte("secret")) {
		t.Fatal("upstream error body leaked")
	}
	res = request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model"}`, testKey)
	res.Body.Close()
	if res.StatusCode != 503 || calls.Load() != 1 {
		t.Fatalf("%d %d", res.StatusCode, calls.Load())
	}
}
func TestCancelReleasesCapacity(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
			close(stopped)
		case <-time.After(3 * time.Second):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", g.URL+"/v1/responses", strings.NewReader(`{"model":"mock/model","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+testKey)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, _ := http.DefaultClient.Do(req)
		if res != nil {
			res.Body.Close()
		}
	}()
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream did not cancel")
	}
	<-done
	deadline := time.Now().Add(time.Second)
	for s.Router.Status()[0].Active != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Router.Status()[0].Active != 0 {
		t.Fatal("lease leaked")
	}
}
func TestAdminIsolationAndDisable(t *testing.T) {
	var calls atomic.Int32
	_, g := setup(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, key := range []string{testKey, adminKey} {
		res := request(t, g.URL, "/admin/sources/mock", `{"enabled":false}`, key)
		res.Body.Close()
		if key == testKey && res.StatusCode != 401 {
			t.Fatal("data key admitted to management")
		}
		if key == adminKey && res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
	}
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model"}`, testKey)
	res.Body.Close()
	if res.StatusCode != 503 || calls.Load() != 0 {
		t.Fatal("disabled source used")
	}
}
func TestOutputLimitAbortsStream(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("x", 1024))
	})
	s.cfg.Runtime.MaxOutputBytes = 64
	req, _ := http.NewRequest("POST", g.URL+"/v1/chat/completions", strings.NewReader(`{"model":"mock/model","stream":true}`))
	req.Header.Set("Authorization", "Bearer "+testKey)
	res, e := http.DefaultClient.Do(req)
	if e == nil {
		_, e = io.ReadAll(res.Body)
		res.Body.Close()
	}
	if e == nil {
		t.Fatal("oversized stream looked successful")
	}
}
func TestBodyReadDeadlineDoesNotCancelGeneration(t *testing.T) {
	s, g := setup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-time.After(100 * time.Millisecond):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case <-r.Context().Done():
			t.Error("body deadline cancelled a completed upload")
		}
	})
	s.cfg.Runtime.BodyReadTimeoutMS = 20
	res := request(t, g.URL, "/v1/chat/completions", `{"model":"mock/model"}`, testKey)
	defer res.Body.Close()
	body, e := io.ReadAll(res.Body)
	if e != nil || res.StatusCode != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("%s %v %d", body, e, res.StatusCode)
	}
}
