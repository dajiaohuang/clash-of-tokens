package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/routing"
)

func TestSessionMutationDispatchesStatefulAdapter(t *testing.T) {
	t.Setenv("COT_ZED_SESSION_KEY", "synthetic-zed-session-key")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Seed only the adapter's local session registry; no response conversion
		// or provider dependency is needed for this control-plane test.
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"synthetic"}`))
	}))
	defer upstream.Close()
	source, err := catalog.Preset("zed-hosted", "model", upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	source.ID = "zed-session-source"
	source.Provider = "zed-hosted"
	source.KeyEnv = "COT_ZED_SESSION_KEY"
	source.Enabled = true
	source.AutoApproved = true
	c := config.Default()
	c.Sources = []config.Source{source}
	c.Groups[0].Sources = []string{source.ID}
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	client := p.current.server.client(0)
	headers := make(http.Header)
	headers.Set("X-COT-Session", "synthetic-session")
	resp, err := client.Do(context.Background(), "chat", "model", false,
		[]byte(`{"model":"model","messages":[{"role":"user","content":"seed"}]}`), headers)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	items, err := client.Sessions()
	if err != nil || len(items) != 1 {
		t.Fatalf("seeded sessions=%+v err=%v", items, err)
	}
	req := httptest.NewRequest("POST", "/admin/sessions/"+source.ID+"/"+items[0].ID+"/clear", nil)
	req.Header.Set("Authorization", "Bearer "+adminKey)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", w.Code, w.Body.String())
	}
	items, err = client.Sessions()
	if err != nil || len(items) != 0 {
		t.Fatalf("cleared sessions=%+v err=%v", items, err)
	}
}

func TestSessionMutationWaitsForExecutionLease(t *testing.T) {
	dir := t.TempDir()
	c := config.Default()
	c.Browser.StateFile = filepath.Join(dir, "sessions.json")
	c.Runtime.QueueTimeoutMS = 20
	source, err := catalog.Preset("chatgpt-web", "auto")
	if err != nil {
		t.Fatal(err)
	}
	c.Sources = []config.Source{source}
	data, _ := json.Marshal(map[string]chatgptweb.Session{"one": {ID: "one", Source: source.ID, Conversation: "conversation", Updated: time.Now()}})
	if err = os.WriteFile(c.Browser.StateFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	vault, _ := credentials.Open(filepath.Join(dir, "vault"))
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	lease, err := p.current.server.Router.Acquire(context.Background(), routing.Query{Model: source.ID + "/" + source.Models[0].ID, Protocol: "chat", Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/admin/sessions/"+source.ID+"/one/clear", strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		p.ServeHTTP(w, req)
		return w
	}
	if w := call(); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	items, _ := chatgptweb.ReadSessions(c.Browser, source.ID)
	if len(items) != 1 {
		t.Fatal("busy session changed")
	}
	lease.ReleaseAdministrative()
	if w := call(); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	items, _ = chatgptweb.ReadSessions(c.Browser, source.ID)
	if len(items) != 0 {
		t.Fatal("clear failed")
	}
	state := p.current.server.Router.Status()[0]
	if state.Completed != 0 || state.Failures != 0 || state.Active != 0 {
		t.Fatal("administration polluted generation health", state)
	}
}
