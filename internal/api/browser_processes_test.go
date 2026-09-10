package api

import (
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
)

func TestBrowserProcessHelper(t *testing.T) {
	if os.Getenv("COT_OWNED_BROWSER_TEST_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}
func browserHelperCommand() *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrowserProcessHelper$")
	cmd.Env = append(os.Environ(), "COT_OWNED_BROWSER_TEST_HELPER=1")
	return cmd
}
func TestOwnedBrowserProcessesUseLaunchIdentity(t *testing.T) {
	var owned browserProcesses
	first, err := owned.start("profile", browserHelperCommand())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owned.stop(first.ID) })
	if _, err = owned.start("profile", browserHelperCommand()); err == nil {
		t.Fatal("duplicate running profile accepted")
	}
	if err = owned.stop("unowned-process"); err == nil || owned.list()[0].State != "running" {
		t.Fatal("unknown identity changed process state")
	}
	if err = owned.stop(first.ID); err != nil {
		t.Fatal(err)
	}
	if owned.list()[0].State != "stopped" {
		t.Fatal("stop did not reap owned child")
	}
	second, err := owned.start("profile", browserHelperCommand())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owned.stop(second.ID) })
	if second.ID == first.ID {
		t.Fatal("launch identity reused")
	}
	if err = owned.stop(first.ID); err != nil {
		t.Fatal(err)
	}
	if owned.list()[0].ID != second.ID || owned.list()[0].State != "running" {
		t.Fatal("old stop affected replacement")
	}
	view := owned.list()
	*view[1].FinishedAt = time.Time{}
	if owned.list()[1].FinishedAt.IsZero() {
		t.Fatal("snapshot aliases retained timestamp")
	}
}

func TestBrowserProcessStopRequiresAdminOriginAndConfirmation(t *testing.T) {
	dir := t.TempDir()
	vault, err := credentials.Open(filepath.Join(dir, "vault"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewControlPlane(filepath.Join(dir, "config.json"), config.Default(), testKey, adminKey, vault)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	process, err := p.browsers.start("synthetic", browserHelperCommand())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.browsers.stop(process.ID) })
	for _, tc := range []struct {
		key, origin, body string
		want              int
	}{
		{"", "", `{"confirm":true}`, 401},
		{adminKey, "https://elsewhere.test", `{"confirm":true}`, 403},
		{adminKey, "", `{"confirm":false}`, 400},
		{adminKey, "", `{"confirm":true}`, 200},
	} {
		r := httptest.NewRequest("POST", "/admin/browser_profiles/processes/"+process.ID+"/stop", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.key)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.want != 200 && p.browsers.list()[0].State != "running" {
			t.Fatal("rejected stop changed process")
		}
	}
	if p.browsers.list()[0].State != "stopped" {
		t.Fatal("confirmed stop failed")
	}
}

func TestOwnedBrowserRegistryBoundsAndStartFailure(t *testing.T) {
	var owned browserProcesses
	if _, err := owned.start("failed", exec.Command(filepath.Join(t.TempDir(), "missing-browser"))); err == nil || len(owned.list()) != 0 {
		t.Fatal("failed launch retained")
	}
	for i := 0; i < 64; i++ {
		id := fmt.Sprint(i)
		owned.items[id] = &ownedBrowserProcess{view: browserProcessView{ID: id, Profile: id, State: "running"}}
	}
	command := browserHelperCommand()
	if _, err := owned.start("overflow", command); err == nil || command.Process != nil {
		t.Fatal("running limit did not prevent process creation")
	}
	now := time.Now().UTC()
	for i := 0; i < 128; i++ {
		id := fmt.Sprint(i)
		owned.items[id] = &ownedBrowserProcess{view: browserProcessView{ID: id, Profile: id, State: "exited", StartedAt: now.Add(time.Duration(i) * time.Second), FinishedAt: &now}}
	}
	view, err := owned.start("new", browserHelperCommand())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owned.stop(view.ID) })
	if len(owned.list()) != 128 || owned.items["0"] != nil {
		t.Fatal("oldest exited entry not evicted")
	}
}
