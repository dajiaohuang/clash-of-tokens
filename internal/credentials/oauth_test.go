package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthRefreshSingleFlightRotationAndReopen(t *testing.T) {
	var calls atomic.Int32
	started, finish := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil || r.Form.Get("refresh_token") != "synthetic-refresh-1" || r.Form.Get("client_id") != "fixture-client" {
			t.Error("wrong refresh grant")
		}
		close(started)
		<-finish
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"synthetic-access-2","refresh_token":"synthetic-refresh-2","expires_in":3600,"token_type":"Bearer","scope":"chat"}`)
	}))
	defer up.Close()
	path := filepath.Join(t.TempDir(), "vault")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	grant := OAuthGrant{AccessToken: "synthetic-access-1", RefreshToken: "synthetic-refresh-1", TokenURL: up.URL, ClientID: "fixture-client", ExpiresAt: time.Now().Add(-time.Minute), AccountID: "account-fixture"}
	if err = store.PutOAuth("cred://oauth", "fixture", grant); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value, err := store.ResolveContext(context.Background(), "cred://oauth")
			if err != nil || value != "synthetic-access-2" {
				t.Error("refresh failed", err)
			}
		}()
	}
	<-started
	close(finish)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("concurrent refresh duplicated", calls.Load())
	}
	metadata := store.List()
	data, _ := json.Marshal(metadata)
	if strings.Contains(string(data), "synthetic-access") || strings.Contains(string(data), "synthetic-refresh") {
		t.Fatal("OAuth metadata contains secrets")
	}
	if metadata[0].Version != 2 || metadata[0].OAuthInfo.AccountID != "account-fixture" {
		t.Fatal("version or identity lost")
	}
	restored, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Resolve("cred://oauth") != "synthetic-access-2" {
		t.Fatal("rotated access token not durable")
	}
	if restored.records["cred://oauth"].OAuth.RefreshToken != "synthetic-refresh-2" {
		t.Fatal("rotated refresh token not durable")
	}
}

func TestOAuthStaleRefreshCannotOverwriteReplacement(t *testing.T) {
	started, finish := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		fmt.Fprint(w, `{"access_token":"old-result","expires_in":3600}`)
	}))
	defer up.Close()
	store, err := Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PutOAuth("cred://oauth", "fixture", OAuthGrant{AccessToken: "old", RefreshToken: "old-refresh", TokenURL: up.URL, ClientID: "fixture", ExpiresAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- store.RefreshOAuth(context.Background(), "cred://oauth") }()
	<-started
	if err = store.Put("cred://oauth", "oauth", "replacement", "new-credential"); err != nil {
		t.Fatal(err)
	}
	close(finish)
	if <-done == nil || store.Resolve("cred://oauth") != "new-credential" {
		t.Fatal("old refresh overwrote replacement")
	}
}

func TestOAuthFailureBackoffAndNoImplicitEndpoint(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(401)
		fmt.Fprint(w, "secret-canary")
	}))
	defer up.Close()
	store, err := Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PutOAuth("cred://oauth", "fixture", OAuthGrant{AccessToken: "old", RefreshToken: "refresh", TokenURL: up.URL, ClientID: "fixture", ExpiresAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		value, err := store.ResolveContext(context.Background(), "cred://oauth")
		if value != "" || err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatal("refresh failure did not fail closed")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("refresh failure retried without backoff")
	}
	if validTokenURL("http://outside.example/token") || validTokenURL("https://user:pass@example.org/token") {
		t.Fatal("unsafe token destination")
	}
}
