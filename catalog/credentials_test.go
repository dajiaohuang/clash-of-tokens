package catalog

import "testing"

func TestCredentialDomainMatching(t *testing.T) {
	matched := MatchCredentials("api.openai.com", "api_key")
	if len(matched) == 0 || !matched[0].Compatible {
		t.Fatal("expected API credential match", matched)
	}
	for _, domain := range []string{"evil-api.openai.com", "api.openai.com.evil.test"} {
		if len(MatchCredentials(domain, "api_key")) != 0 {
			t.Fatal("lookalike matched", domain)
		}
	}
	for _, m := range MatchCredentials("api.openai.com", "username_password") {
		if m.Compatible {
			t.Fatal("password treated as key")
		}
	}
}
