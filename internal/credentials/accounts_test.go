package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestProviderAccountMigrationExcludesUnboundAndRecovers(t *testing.T) {
	dir := t.TempDir()
	legacy, err := Open(filepath.Join(dir, "credentials.vault"))
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.Put("cred://bound", "username_password", "legacy", `{"username":"fixture-user","password":"fixture-secret"}`); err != nil {
		t.Fatal(err)
	}
	if err = legacy.Put("cred://unbound", "username_password", "legacy", `{"username":"other","password":"discard-secret"}`); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Providers = []config.Provider{{ID: "deepseek"}}
	c.Accounts = []config.Account{{ID: "one", ProviderID: "deepseek", LoginCredentialRef: "cred://bound", QuotaDomain: "one", MaxInflight: 1, Weight: 1}}
	s, backup, err := OpenProviderAccounts(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AccountOwned() || len(s.List()) != 1 || backup == "" {
		t.Fatal("migration did not scope account data")
	}
	if _, err = os.Stat(filepath.Join(dir, "credentials.vault")); !os.IsNotExist(err) {
		t.Fatal("old active vault retained")
	}
	if _, err = os.Stat(backup); err != nil {
		t.Fatal("missing recovery copy")
	}
	b, err := os.ReadFile(filepath.Join(dir, "provider-accounts.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("fixture-secret")) || bytes.Contains(b, []byte("fixture-user")) {
		t.Fatal("account values not encrypted")
	}
	again, secondBackup, err := OpenProviderAccounts(dir, c)
	if err != nil || secondBackup != "" || len(again.List()) != 1 {
		t.Fatal("migration not restart safe", err)
	}
	if err = again.Put("cred://independent", "api_key", "test", "secret"); err == nil {
		t.Fatal("independent material persisted")
	}
	if err = again.ValidateAccountOwners(c); err != nil {
		t.Fatal(err)
	}
	c.Accounts[0].ID = "different"
	if again.ValidateAccountOwners(c) == nil {
		t.Fatal("material crossed account ownership")
	}
}
