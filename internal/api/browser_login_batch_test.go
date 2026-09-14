package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

// Discovery sees an inert executable; no test invokes it or contacts a site.
func fakeInstalledChrome(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "google-chrome")
	if runtime.GOOS == "windows" {
		for _, name := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			t.Setenv(name, root)
		}
		path = filepath.Join(root, `Google\Chrome\Application\chrome.exe`)
	} else if runtime.GOOS == "darwin" {
		t.Skip("fixed application paths; tested on Windows and Linux")
	} else {
		t.Setenv("PATH", root)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("inert test fixture"), 0700); err != nil {
		t.Fatal(err)
	}
}
func loginBatchFixture(t *testing.T) *ControlPlane {
	t.Helper()
	dir := t.TempDir()
	c := config.Default()
	c.Providers = []config.Provider{{ID: "deepseek", Enabled: true}}
	c.Accounts = []config.Account{{ID: "a", ProviderID: "deepseek", QuotaDomain: "a", MaxInflight: 1, Weight: 1}}
	v, _, err := credentials.OpenProviderAccounts(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), c, "", "", v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}
func batchRequest(p *ControlPlane, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/admin/accounts/browser-login"+path, strings.NewReader(body)))
	return w
}
func TestPasswordImportRemoved(t *testing.T) {
	p := loginBatchFixture(t)
	for _, path := range []string{"/admin/credentials/import", "/admin/credentials/auto-configure", "/admin/credentials/browser-passwords/discover", "/admin/credentials/browser-passwords/preview-file", "/admin/credentials/browser-passwords/apply", "/admin/credentials/import-preview/old"} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
		if w.Code != 410 {
			t.Errorf("%s = %d", path, w.Code)
		}
	}
	js, err := os.ReadFile("control.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"function oneClickImport(", "function importCredentials(", "browser-passwords/", "function browserExportImport("} {
		if strings.Contains(string(js), removed) {
			t.Fatal("import UI remains", removed)
		}
	}
}
func TestLoginBatchPreviewConfirmationAndStaleness(t *testing.T) {
	fakeInstalledChrome(t)
	p := loginBatchFixture(t)
	w := batchRequest(p, "/preview", `{"browser":"chrome"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview struct {
		Ticket string      `json:"ticket"`
		Sites  []loginSite `json:"sites"`
	}
	if json.Unmarshal(w.Body.Bytes(), &preview) != nil || preview.Ticket == "" || len(preview.Sites) != 1 {
		t.Fatal("bad preview")
	}
	if len(p.service.Current().Config.BrowserProfiles) != 0 || len(p.browsers.list()) != 0 {
		t.Fatal("preview caused login effects")
	}
	w = batchRequest(p, "/start", `{"ticket":"`+preview.Ticket+`","confirm":false}`)
	if w.Code != 409 {
		t.Fatal("second confirmation not enforced")
	}
	p.loginBatch.expires = time.Now().Add(-time.Second)
	w = batchRequest(p, "/start", `{"ticket":"`+preview.Ticket+`","confirm":true}`)
	if w.Code != 409 {
		t.Fatal("expired ticket accepted")
	}
	batchRequest(p, "/preview", `{"browser":"chrome"}`)
	p.loginBatch.version.revision++
	w = batchRequest(p, "/start", `{"ticket":"`+p.loginBatch.ticket+`","confirm":true}`)
	if w.Code != 409 || len(p.browsers.list()) != 0 {
		t.Fatal("stale preview launched browser")
	}
	w = batchRequest(p, "/preview", `{"browser":"powershell"}`)
	if w.Code != 400 {
		t.Fatal("unknown browser accepted")
	}
	w = batchRequest(p, "/preview", `{"browser":"chrome","accounts":["missing"]}`)
	if w.Code != 400 {
		t.Fatal("unknown account silently omitted")
	}
}
func TestCanceledLoginBatchNeverLaunches(t *testing.T) {
	p := loginBatchFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := loginBatchView{ID: "test", Browser: "chrome", State: "running", Sites: p.loginSites(nil)}
	p.loginBatch.view = v
	p.runLoginBatch(ctx, v, "localhost", p.operationVersion())
	if p.loginBatch.view.State != "canceled" || len(p.browsers.list()) != 0 || len(p.service.Current().Config.BrowserProfiles) != 0 {
		t.Fatal("canceled batch had side effects")
	}
}
func TestSelectedBrowserProfileIsReusable(t *testing.T) {
	fakeInstalledChrome(t)
	p := loginBatchFixture(t)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("POST", "/admin/accounts/a/prepare-login", strings.NewReader(`{"browser":"chrome"}`)))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	c := p.service.Current().Config
	if len(c.BrowserProfiles) != 1 || c.BrowserProfiles[0].Engine != "chrome" || c.Accounts[0].Enabled {
		t.Fatal("profile not reused or unverified enabled")
	}
	if len(p.browsers.list()) != 0 {
		t.Fatal("preparation launched browser")
	}
}
func TestBrowserVerificationProofDoesNotSurviveRuntimeChange(t *testing.T) {
	p := loginBatchFixture(t)
	c := p.service.Current().Config
	c.BrowserProfiles = []config.BrowserProfile{{ID: "b", Engine: "chrome", CDPURL: "http://127.0.0.1:19998", Enabled: true}}
	c.Accounts[0].BrowserProfileID = "b"
	c.Accounts[0].Enabled = true
	c.Accounts[0].VerificationState = "verified"
	c.Accounts[0].VerificationBinding = p.accountProofConfig(c, c.Accounts[0])
	if !p.withVerifiedAccountPolicy(c).Accounts[0].Enabled {
		t.Fatal("current proof disabled")
	}
	p.runtimeID = "different"
	if p.withVerifiedAccountPolicy(c).Accounts[0].Enabled {
		t.Fatal("stale browser proof enabled routing")
	}
	if !c.Accounts[0].Enabled {
		t.Fatal("runtime policy mutated durable configuration")
	}
}
