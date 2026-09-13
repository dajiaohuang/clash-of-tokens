package browserexec

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestCancellationBrowserFixture(t *testing.T) {
	endpoint := os.Getenv("COT_TEST_CDP")
	if endpoint == "" || os.Getenv("COT_TEST_BROWSER_ENGINE") == "firefox" {
		t.Skip("requires isolated Chromium fixture")
	}
	targets := func() map[string]bool {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(endpoint + "/json/list")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var entries []struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(response.Body).Decode(&entries); err != nil {
			t.Fatal(err)
		}
		ids := make(map[string]bool)
		for _, entry := range entries {
			ids[entry.ID] = true
		}
		return ids
	}
	before := targets()
	for _, private := range []bool{false, true} {
		parent, cancel := context.WithCancel(context.Background())
		tab, closeTab, err := OpenCDP(parent, endpoint, private)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if len(targets()) <= len(before) {
			closeTab()
			cancel()
			t.Fatal("owned tab was not created")
		}
		cancel()
		<-tab.Done()
		closeTab()
		closeTab()
		after := targets()
		if len(after) != len(before) {
			t.Fatalf("canceled private=%v leaked targets: before=%v after=%v", private, before, after)
		}
		for id := range before {
			if !after[id] {
				t.Fatalf("unowned target %s was closed", id)
			}
		}
	}
}

func TestPrivateBrowserFixture(t *testing.T) {
	endpoint := os.Getenv("COT_TEST_CDP")
	if endpoint == "" {
		t.Skip("set COT_TEST_CDP for real browser fixture")
	}
	a, stopA := NewRemoteAllocator(context.Background(), endpoint, os.Getenv("COT_TEST_BROWSER_ENGINE"))
	defer stopA()
	tab, stopTab := NewContext(a, WithNewBrowserContext())
	defer stopTab()
	ctx, cancel := context.WithTimeout(tab, 5*time.Second)
	defer cancel()
	if err := Run(ctx); err != nil {
		t.Fatal(err)
	}
}
