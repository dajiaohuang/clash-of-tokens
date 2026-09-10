package chatgptweb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthCheckDoesNotSubmitOrExposeIdentity(t *testing.T) {
	d, fake := browserFixture(t)
	evidence := d.CheckAuth(context.Background())
	if evidence.Status != "authenticated" || !evidence.ComposerReady || evidence.GenerationVerified {
		t.Fatalf("unexpected evidence %+v", evidence)
	}
	encoded, _ := json.Marshal(evidence)
	if strings.Contains(string(encoded), "account-a") || strings.Contains(string(encoded), "fixture-token") {
		t.Fatal("identity or token escaped page")
	}
	fake.mu.Lock()
	fake.account = ""
	fake.mu.Unlock()
	if result := d.CheckAuth(context.Background()); result.Status != "login_required" {
		t.Fatal(result)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.sends != 0 {
		t.Fatal("auth check submitted generation")
	}
}
