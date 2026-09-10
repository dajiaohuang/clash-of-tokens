package catalog

import (
	"slices"
	"strings"
)

type CredentialPolicy struct {
	Domains  []string `json:"domains"`
	Accepted []string `json:"accepted"`
}

type CredentialMatch struct {
	Provider   string `json:"provider"`
	Compatible bool   `json:"compatible"`
	Reason     string `json:"reason"`
}

// MatchCredentials uses exact catalog hosts only. A lookalike or arbitrary
// subdomain never inherits a match. Matching is a recommendation, not login or
// account-availability evidence.
func MatchCredentials(domain, kind string) []CredentialMatch {
	out := []CredentialMatch{}
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	for _, p := range All() {
		if !slices.Contains(p.Credentials.Domains, domain) {
			continue
		}
		compatible := slices.Contains(p.Credentials.Accepted, kind)
		reason := "domain_and_credential_type"
		if !compatible {
			reason = "domain_only_requires_login_or_different_credential"
		}
		out = append(out, CredentialMatch{Provider: p.ID, Compatible: compatible, Reason: reason})
	}
	return out
}
