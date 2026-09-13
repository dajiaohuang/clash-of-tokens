package api

import (
	"encoding/json"
	"sort"
	"strings"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
)

type credentialDestinationChange struct {
	Resource string `json:"resource"`
	Before   string `json:"before"`
	After    string `json:"after"`
}

// Compare non-secret destinations, including inherited tenant and browser
// identity. Explicit confirmation applies only to this config revision.
func credentialDestinationChanges(before, after config.Config) []credentialDestinationChange {
	type binding struct{ material, destination string }
	bindings := func(c config.Config) map[string]binding {
		out := map[string]binding{}
		bases := map[string]string{}
		for _, p := range catalog.All() {
			bases[p.ID] = p.BaseURL
		}
		for _, a := range c.Accounts {
			material := a.CredentialRef
			if material == "" {
				material = a.LoginCredentialRef
			}
			if a.BrowserProfileID != "" {
				material += " profile:" + a.BrowserProfileID
			}
			base := a.BaseURL
			if base == "" {
				base = bases[a.ProviderID]
			}
			browser := c.SourceBrowser(config.Source{AccountID: a.ID})
			value, _ := json.Marshal([]string{strings.TrimRight(base, "/"), a.Organization, a.Project, a.ExpectedIdentity, a.BrowserProfileID, browser.CDPURL, browser.Engine})
			out["account:"+a.ID] = binding{material, string(value)}
		}
		for _, source := range c.Sources {
			s := c.EffectiveSource(source)
			material := c.SourceCredentialRef(s)
			if material == "" {
				material = s.KeyEnv
			}
			base := s.BaseURL
			if base == "" {
				base = bases[s.Provider]
			}
			value, _ := json.Marshal([]string{strings.TrimRight(base, "/"), s.Organization, s.Project, s.Adapter, s.AccountID})
			out["source:"+s.ID] = binding{material, string(value)}
		}
		return out
	}
	old, next := bindings(before), bindings(after)
	changes := []credentialDestinationChange{}
	for id, n := range next {
		if o, ok := old[id]; ok && o.material != "" && n.material != "" && o.destination != n.destination {
			changes = append(changes, credentialDestinationChange{id, o.destination, n.destination})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Resource < changes[j].Resource })
	return changes
}
