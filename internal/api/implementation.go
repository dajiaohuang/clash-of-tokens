package api

import (
	"net/http"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/providerdef"
)

type implementationRow struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Adapter            string `json:"adapter"`
	Implementation     string `json:"implementation"`
	Factory            string `json:"factory"`
	ConfiguredSources  int    `json:"configured_sources"`
	ConfiguredAccounts int    `json:"configured_accounts"`
	CatalogLive        bool   `json:"catalog_live_verified"`
	RuntimeVerified    int    `json:"runtime_verified_models"`
	RuntimeFailures    int    `json:"runtime_failed_models"`
	Reference          string `json:"reference"`
	Notes              string `json:"notes"`
}

func (p *ControlPlane) implementationAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if r.URL.Path != "/admin/implementation" {
		return false
	}
	if r.Method != "GET" {
		fail(w, 405, "use GET /admin/implementation")
		return true
	}
	configuredSources := map[string]int{}
	configuredAccounts := map[string]int{}
	for _, source := range s.cfg.Sources {
		configuredSources[source.Provider]++
	}
	for _, account := range s.cfg.Accounts {
		configuredAccounts[account.ProviderID]++
	}
	verified, failed := map[string]int{}, map[string]int{}
	providersBySource := map[string]string{}
	for _, source := range s.cfg.Sources {
		providersBySource[source.ID] = source.Provider
	}
	for _, item := range p.evidence.List() {
		if item.Kind != "validation" {
			continue
		}
		provider := providersBySource[item.Resource]
		if provider == "" {
			continue
		}
		if item.Status == "verified" || item.Status == "completed" {
			verified[provider]++
		} else {
			failed[provider]++
		}
	}
	rows := make([]implementationRow, 0, len(catalog.All()))
	for _, entry := range catalog.All() {
		d, known := providerdef.Lookup(entry.Adapter)
		factory := "unknown"
		if known {
			factory = d.Factory
		}
		rows = append(rows, implementationRow{ID: entry.ID, Kind: entry.Kind, Adapter: entry.Adapter, Implementation: entry.Implementation, Factory: factory, ConfiguredSources: configuredSources[entry.ID], ConfiguredAccounts: configuredAccounts[entry.ID], CatalogLive: entry.LiveVerified, RuntimeVerified: verified[entry.ID], RuntimeFailures: failed[entry.ID], Reference: entry.Reference, Notes: entry.Notes})
	}
	reply(w, map[string]any{"items": rows, "catalog_count": len(rows), "runtime_evidence": len(p.evidence.List()), "live_means": "catalog metadata only; runtime verification requires an explicit source check"})
	return true
}
