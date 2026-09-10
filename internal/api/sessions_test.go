package api

import (
	"context"
	"encoding/json"
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
