package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"clash-of-tokens/internal/browserpassword"
	"clash-of-tokens/internal/credentials"
)

func TestBrowserBatchRequiresConfirmationAndDoesNotLeakPasswords(t *testing.T) {
	vault, err := credentials.Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{vault: vault}
	defer s.imports.Close()
	read := func(context.Context, string) (browserpassword.Report, error) {
		return browserpassword.Report{Entries: []credentials.ImportEntry{{URL: "https://example.test/login", Username: "fixture-user", Password: "fixture-secret"}}}, nil
	}
	discover := func(context.Context) []browserpassword.Browser { return []browserpassword.Browser{} }
	call := func(action, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/admin/credentials/browser-passwords/"+action, strings.NewReader(body))
		if !s.browserPasswordImportWith(w, r, discover, read) {
			t.Fatal("route not handled")
		}
		if strings.Contains(w.Body.String(), "fixture-secret") || strings.Contains(w.Body.String(), "fixture-user") {
			t.Fatal("preview exposed login values")
		}
		return w
	}
	preview := func() string {
		w := call("preview", `{"browser":"chrome"}`)
		if w.Code != 200 {
			t.Fatalf("preview status %d", w.Code)
		}
		var result struct {
			Ticket string `json:"ticket"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Ticket == "" {
			t.Fatal("missing ticket")
		}
		return result.Ticket
	}
	ticket := preview()
	if len(vault.List()) != 0 {
		t.Fatal("preview persisted credentials")
	}
	if call("apply", `{"ticket":"`+ticket+`","confirm":false}`).Code != 400 {
		t.Fatal("confirmation not enforced")
	}
	if len(vault.List()) != 0 {
		t.Fatal("unconfirmed import persisted credentials")
	}
	w := call("apply", `{"ticket":"`+ticket+`","confirm":true}`)
	if w.Code != 200 || len(vault.List()) != 1 {
		t.Fatal("confirmed import failed")
	}
	if call("apply", `{"ticket":"`+ticket+`","confirm":true}`).Code != 409 {
		t.Fatal("ticket reused")
	}
	ticket = preview()
	if call("apply", `{"ticket":"`+ticket+`","confirm":true}`).Code != 200 || len(vault.List()) != 1 {
		t.Fatal("duplicate browser import created duplicate credential")
	}
	ticket = preview()
	s.imports.Drop(ticket)
	if call("apply", `{"ticket":"`+ticket+`","confirm":true}`).Code != 409 {
		t.Fatal("cancelled preview persisted")
	}
}
