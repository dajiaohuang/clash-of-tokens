package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type Provider struct {
	ID           string `json:"id"`
	Enabled      bool   `json:"enabled"`
	AutoApproved bool   `json:"auto_approved"`
	PoolStrategy string `json:"pool_strategy,omitempty"`
}

// Account contains references and operational policy, never credential values.
type Account struct {
	ID                     string    `json:"id"`
	ProviderID             string    `json:"provider_id"`
	DisplayName            string    `json:"display_name"`
	Enabled                bool      `json:"enabled"`
	AutoApproved           bool      `json:"auto_approved"`
	CredentialRef          string    `json:"credential_ref,omitempty"`
	CredentialTypeOverride bool      `json:"credential_type_override,omitempty"`
	BrowserProfileID       string    `json:"browser_profile_id,omitempty"`
	BaseURL                string    `json:"base_url,omitempty"`
	Organization           string    `json:"organization,omitempty"`
	Project                string    `json:"project,omitempty"`
	QuotaDomain            string    `json:"quota_domain"`
	MaxInflight            int       `json:"max_inflight"`
	Weight                 int       `json:"weight"`
	CreatedAt              time.Time `json:"created_at"`
}

func ValidCredentialRef(ref string) bool {
	return ref == "" || (strings.HasPrefix(ref, "cred://") && identifier.MatchString(strings.TrimPrefix(ref, "cred://")))
}

func (c Config) SourceCredentialRef(s Source) string {
	if s.CredentialRef != "" {
		return s.CredentialRef
	}
	for _, a := range c.Accounts {
		if a.ID == s.AccountID {
			return a.CredentialRef
		}
	}
	return ""
}

// EffectiveSource fills provider-specific routing metadata from the bound
// account only when the source does not provide an explicit value. The
// returned source is a copy; durable configuration retains the distinction
// between account defaults and source overrides.
func (c Config) EffectiveSource(s Source) Source {
	if s.AccountID == "" {
		return s
	}
	for _, account := range c.Accounts {
		if account.ID != s.AccountID {
			continue
		}
		if s.BaseURL == "" {
			s.BaseURL = account.BaseURL
		}
		if s.Organization == "" {
			s.Organization = account.Organization
		}
		if s.Project == "" {
			s.Project = account.Project
		}
		return s
	}
	return s
}

func validateAccountMetadata(a Account) error {
	for _, field := range []struct {
		name, value string
	}{
		{"organization", a.Organization},
		{"project", a.Project},
	} {
		if len(field.value) > 256 || strings.ContainsAny(field.value, "\r\n\x00") {
			return fmt.Errorf("account %s: invalid %s", a.ID, field.name)
		}
	}
	if a.BaseURL == "" {
		return nil
	}
	u, err := url.Parse(a.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("account %s: invalid base_url", a.ID)
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return fmt.Errorf("account %s: base_url must use HTTPS outside loopback", a.ID)
	}
	return nil
}

func (c Config) ValidateAccounts() error {
	if err := c.ValidateBrowserProfiles(); err != nil {
		return err
	}
	providers := map[string]bool{}
	for _, p := range c.Providers {
		if !identifier.MatchString(p.ID) || providers[p.ID] {
			return fmt.Errorf("invalid or duplicate provider id")
		}
		providers[p.ID] = true
		switch p.PoolStrategy {
		case "", "round-robin", "least-load", "sticky", "weighted":
		default:
			return fmt.Errorf("provider %s: invalid pool strategy", p.ID)
		}
	}
	accounts := map[string]Account{}
	for _, a := range c.Accounts {
		if !identifier.MatchString(a.ID) || accounts[a.ID].ID != "" {
			return fmt.Errorf("invalid or duplicate account id")
		}
		if !providers[a.ProviderID] {
			return fmt.Errorf("account %s: unknown provider", a.ID)
		}
		if !ValidCredentialRef(a.CredentialRef) {
			return fmt.Errorf("account %s: invalid credential reference", a.ID)
		}
		if err := validateAccountMetadata(a); err != nil {
			return err
		}
		if a.MaxInflight < 1 || a.MaxInflight > 10000 || a.Weight < 1 || a.Weight > 10000 || !identifier.MatchString(a.QuotaDomain) {
			return fmt.Errorf("account %s: invalid capacity, weight or quota domain", a.ID)
		}
		accounts[a.ID] = a
	}
	for _, s := range c.Sources {
		if !ValidCredentialRef(s.CredentialRef) {
			return fmt.Errorf("source %s: invalid credential reference", s.ID)
		}
		if s.AccountID == "" {
			continue
		}
		a, ok := accounts[s.AccountID]
		if !ok {
			return fmt.Errorf("source %s: unknown account or provider mismatch", s.ID)
		}
		if a.ProviderID != s.Provider {
			return fmt.Errorf("source %s: account provider changed while source is bound; migrate source first", s.ID)
		}
		if s.QuotaDomain != a.QuotaDomain {
			return fmt.Errorf("source %s: quota domain must match its account", s.ID)
		}
		if s.CredentialRef != "" && a.CredentialRef != "" && s.CredentialRef != a.CredentialRef {
			return fmt.Errorf("source %s: conflicting account credential", s.ID)
		}
	}
	return nil
}
