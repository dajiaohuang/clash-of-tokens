package catalog

import (
	"net/url"
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
	domain = normalizeHost(domain)
	if domain == "" {
		return out
	}
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

// normalizeHost accepts either a host or an explicit HTTP(S) URL while
// retaining host-exact matching. Paths and query strings never influence the
// recommendation, and malformed or credential-bearing URLs are rejected.
func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return ""
		}
		value = u.Hostname()
	}
	if strings.ContainsAny(value, "/?#\r\n\x00") {
		return ""
	}
	return strings.TrimSuffix(value, ".")
}
