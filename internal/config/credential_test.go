package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialReferenceFailsClosedAndDoesNotSerializeValue(t *testing.T) {
	t.Setenv("COT_TEST_REFERENCE", "legacy-secret")
	s := Source{KeyEnv: "COT_TEST_REFERENCE", CredentialRef: "cred://a"}
	if s.CredentialValue() != "" {
		t.Fatal("unresolved vault fell back to environment")
	}
	s.CredentialResolver = func(ref string) string {
		if ref != "cred://a" {
			t.Fatal(ref)
		}
		return "vault-secret"
	}
	if s.CredentialValue() != "vault-secret" {
		t.Fatal("resolver ignored")
	}
	b, err := json.Marshal(s)
	if err != nil || strings.Contains(string(b), "vault-secret") {
		t.Fatal("secret serialized", err)
	}
	s.CredentialRef = ""
	if s.CredentialValue() != "legacy-secret" {
		t.Fatal("legacy import compatibility failed")
	}
}
