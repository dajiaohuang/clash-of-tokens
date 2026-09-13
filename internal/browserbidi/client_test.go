package browserbidi_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"clash-of-tokens/internal/browserbidi"
	"clash-of-tokens/internal/browsermeta"
)

func TestFirefoxEngineLifecycle(t *testing.T) {
	executable := os.Getenv("COT_TEST_FIREFOX_BIN")
	if executable == "" {
		t.Skip("set COT_TEST_FIREFOX_BIN for a real isolated Firefox engine")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	profile := t.TempDir()
	if err = os.WriteFile(filepath.Join(profile, "user.js"), []byte(`user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("app.update.enabled", false);
user_pref("browser.newtabpage.enabled", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
`), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--headless", "--no-remote", "--profile", profile, "--remote-debugging-port", fmt.Sprint(port), "about:blank")
	command.Stdout = nil
	command.Stderr = nil
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	var client *browserbidi.Client
	for {
		client, err = browserbidi.Connect(ctx, endpoint)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("Firefox BiDi never became ready", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "owned_session", Value: "synthetic-bidi", Path: "/", HttpOnly: true})
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<textarea>fixture</textarea>")
	}))
	defer up.Close()
	tab, err := client.NewTab(ctx)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err = client.Navigate(ctx, tab, up.URL); err != nil {
		client.Close()
		t.Fatal(err)
	}
	var result struct {
		Count  int    `json:"count"`
		Answer string `json:"answer"`
	}
	if err = client.EvaluateJSON(ctx, tab, `({count:document.querySelectorAll('textarea').length,answer:'OK'})`, &result); err != nil || result.Count != 1 || result.Answer != "OK" {
		client.Close()
		t.Fatal("evaluation failed", err, result)
	}
	client.CloseTab(tab)
	client.Close()
	// Ending the BiDi session must leave Firefox running and reconnectable.
	status := browsermeta.CheckEngine(ctx, "fixture", endpoint, "firefox")
	if status.State != "connected" {
		t.Fatal("Firefox did not survive session cleanup", status)
	}
	cookie, count, err := browsermeta.CookiesEngine(ctx, endpoint, up.URL, "firefox")
	if err != nil || count != 1 || cookie != "owned_session=synthetic-bidi" {
		t.Fatal("scoped HttpOnly cookie acquisition failed", err, count)
	}
	client, err = browserbidi.Connect(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := browserbidi.Connect(ctx, endpoint); err == nil {
		duplicate.Close()
		t.Fatal("parallel BiDi session accepted for same profile")
	}
	var before struct {
		Contexts []any `json:"contexts"`
	}
	if err := client.Call(ctx, "browsingContext.getTree", map[string]any{}, &before); err != nil {
		t.Fatal(err)
	}
	tab, err = client.NewTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	if err = client.EvaluateJSON(canceled, tab, `new Promise(()=>{})`, &result); err == nil {
		t.Fatal("hanging script ignored cancellation")
	}
	stop()
	client.Close()
	client, err = browserbidi.Connect(ctx, endpoint)
	if err != nil {
		t.Fatal("could not reconnect after cancellation", err)
	}
	defer client.Close()
	var after struct {
		Contexts []any `json:"contexts"`
	}
	if err := client.Call(ctx, "browsingContext.getTree", map[string]any{}, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Contexts) != len(after.Contexts) {
		t.Fatalf("canceled operation leaked or closed unrelated tabs: before=%d after=%d", len(before.Contexts), len(after.Contexts))
	}
}
