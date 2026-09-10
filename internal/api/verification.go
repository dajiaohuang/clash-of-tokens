package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"time"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	audit "clash-of-tokens/internal/evidence"
	"clash-of-tokens/internal/providerdef"
)

type modelVerification struct {
	Model     string     `json:"model"`
	Protocol  string     `json:"protocol"`
	Status    string     `json:"status"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Revision  uint64     `json:"revision,omitempty"`
}
type sourceVerification struct {
	Source              string              `json:"source"`
	Provider            string              `json:"provider"`
	Account             string              `json:"account,omitempty"`
	CatalogImplemented  bool                `json:"catalog_implemented"`
	CatalogLiveVerified bool                `json:"catalog_live_verified"`
	CredentialState     string              `json:"credential_state"`
	CredentialVersion   uint64              `json:"credential_version,omitempty"`
	Models              []modelVerification `json:"models"`
}

type accountHealth struct {
	ID                string     `json:"id"`
	Provider          string     `json:"provider"`
	PoolStrategy      string     `json:"pool_strategy"`
	Weight            int        `json:"weight"`
	Health            string     `json:"health"`
	AuthStatus        string     `json:"auth_status"`
	Active            int        `json:"active"`
	Limit             int        `json:"limit"`
	Sources           []string   `json:"sources"`
	Completed         uint64     `json:"completed"`
	Failures          uint64     `json:"failures"`
	LastSuccess       *time.Time `json:"last_success,omitempty"`
	LastFailure       *time.Time `json:"last_failure,omitempty"`
	LastValidatedAt   *time.Time `json:"last_validated_at,omitempty"`
	LastAuthCheckedAt *time.Time `json:"last_auth_checked_at,omitempty"`
}

type providerHealth struct {
	ID                  string     `json:"id"`
	Enabled             bool       `json:"enabled"`
	PoolStrategy        string     `json:"pool_strategy"`
	CatalogImplemented  bool       `json:"catalog_implemented"`
	CatalogLiveVerified bool       `json:"catalog_live_verified"`
	VerifiedSources     int        `json:"verified_sources"`
	Health              string     `json:"health"`
	AuthStatus          string     `json:"auth_status"`
	Active              int        `json:"active"`
	Limit               int        `json:"limit"`
	Accounts            []string   `json:"accounts"`
	Sources             []string   `json:"sources"`
	Completed           uint64     `json:"completed"`
	Failures            uint64     `json:"failures"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
	LastFailure         *time.Time `json:"last_failure,omitempty"`
	LastValidatedAt     *time.Time `json:"last_validated_at,omitempty"`
	LastAuthCheckedAt   *time.Time `json:"last_auth_checked_at,omitempty"`
}

func (p *ControlPlane) credentialMetadata(ref string) credentials.Metadata {
	for _, m := range p.vault.List() {
		if m.ID == ref {
			return m
		}
	}
	return credentials.Metadata{}
}

// Bind evidence to non-secret configuration and the exact vault revision.
// Environment credentials have no persistent revision, so their evidence is
// additionally limited to this server run. No secret or secret hash is stored.
func (p *ControlPlane) sourceBinding(c config.Config, s config.Source) string {
	meta := p.credentialMetadata(c.SourceCredentialRef(s))
	return p.bindingWithMetadata(c, s, meta)
}

func (p *ControlPlane) bindingWithMetadata(c config.Config, s config.Source, meta credentials.Metadata) string {
	run := ""
	if s.AccountIDEnv != "" || (meta.ID == "" && s.KeyEnv != "") {
		run = p.runtimeID
	}
	data, _ := json.Marshal(struct {
		Source           config.Source
		Browser          config.Browser
		Device           config.Device
		CredentialID     string
		Version          uint64
		Created, Updated time.Time
		Runtime          string
	}{s, c.SourceBrowser(s), c.Device, meta.ID, meta.Version, meta.CreatedAt, meta.UpdatedAt, run})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (s *Server) runtimeStatus() map[string]any {
	accounts, domains := s.Router.CapacityStatus()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	uptime, rate := 0.0, 0.0
	requests := s.requests.Load()
	if !s.started.IsZero() {
		uptime = time.Since(s.started).Seconds()
		if uptime > 0 {
			rate = float64(requests) / uptime
		}
	}
	return map[string]any{"workload": s.Router.WorkloadStatus(), "ingress_active": len(s.ingress), "ingress_limit": cap(s.ingress), "buffered_limit_bytes": s.cfg.Runtime.MaxBufferedBytes, "go_heap_bytes": memory.HeapAlloc, "go_runtime_bytes": memory.Sys, "goroutines": runtime.NumGoroutine(), "uptime_seconds": uptime, "average_requests_per_second": rate, "execution_events": s.Router.ExecutionEvents(), "accounts": accounts, "quota_domains": domains, "sources": s.Router.Status(), "requests": requests, "rejected": s.rejected.Load(), "buffered_bytes": s.buffered.Load(), "output_bytes": s.outputBytes.Load(), "stream_errors": s.streamErrors.Load(), "live_verified_sources": 0, "verification": []sourceVerification{}}
}

func (p *ControlPlane) controlStatus(s *Server) map[string]any {
	out := s.runtimeStatus()
	entries := p.evidence.List()
	type checkKey struct{ source, model, protocol string }
	latest := map[checkKey]audit.Entry{}
	for _, entry := range entries {
		if entry.Kind == "validation" {
			key := checkKey{entry.Resource, entry.Model, entry.Protocol}
			if current, ok := latest[key]; !ok || entry.CheckedAt.After(current.CheckedAt) {
				latest[key] = entry
			}
		}
	}
	metadata := map[string]credentials.Metadata{}
	for _, meta := range p.vault.List() {
		metadata[meta.ID] = meta
	}
	providers := map[string]catalog.Entry{}
	for _, entry := range catalog.All() {
		providers[entry.ID] = entry
	}
	verification := make([]sourceVerification, 0, len(s.cfg.Sources))
	verifiedSources := 0
	for _, source := range s.cfg.Sources {
		credentialRef := s.cfg.SourceCredentialRef(source)
		meta := metadata[credentialRef]
		descriptor, _ := providerdef.Lookup(source.Adapter)
		entry, known := providers[source.Provider]
		v := sourceVerification{Source: source.ID, Provider: source.Provider, Account: source.AccountID, CatalogImplemented: known && entry.Adapter == source.Adapter && entry.Implementation != "not_implemented", CatalogLiveVerified: known && entry.Adapter == source.Adapter && entry.LiveVerified, CredentialState: "not_configured", CredentialVersion: meta.Version, Models: []modelVerification{}}
		switch {
		case credentialRef != "":
			v.CredentialState = "missing_reference"
			if meta.ID != "" {
				v.CredentialState = "protected_reference"
			}
		case source.KeyEnv != "" && os.Getenv(source.KeyEnv) != "":
			v.CredentialState = "environment_present"
		case descriptor.BrowserRequired && s.cfg.SourceBrowser(source).Enabled:
			v.CredentialState = "browser_configured"
		case source.Anonymous:
			v.CredentialState = "anonymous"
		}
		binding := p.bindingWithMetadata(s.cfg, source, meta)
		verified := false
		for _, model := range source.Models {
			for _, proto := range model.Protocols {
				mv := modelVerification{Model: model.ID, Protocol: proto, Status: "not_checked"}
				if e, ok := latest[checkKey{source.ID, model.ID, proto}]; ok {
					checked := e.CheckedAt
					mv.CheckedAt = &checked
					mv.Revision = e.Revision
					mv.Status = "historical"
					if e.Binding != "" && e.Binding == binding {
						mv.Status = "failed"
						if e.Status == "verified" && e.Method == "explicit_stream_generation" && e.ProtocolComplete && e.OutputObserved && e.UpstreamStatus >= 200 && e.UpstreamStatus < 300 {
							mv.Status = "verified"
							verified = true
						}
					}
				}
				v.Models = append(v.Models, mv)
			}
		}
		if verified {
			verifiedSources++
		}
		verification = append(verification, v)
	}
	out["verification"] = verification
	out["live_verified_sources"] = verifiedSources
	accounts := p.accountHealth(s, entries)
	out["account_health"] = accounts
	providerRows := p.providerHealth(s, accounts, entries)
	catalogByID := map[string]catalog.Entry{}
	for _, entry := range catalog.All() {
		catalogByID[entry.ID] = entry
	}
	verifiedByProvider := map[string]int{}
	for _, source := range verification {
		for _, model := range source.Models {
			if model.Status == "verified" {
				verifiedByProvider[source.Provider]++
				break
			}
		}
	}
	for i := range providerRows {
		entry, known := catalogByID[providerRows[i].ID]
		providerRows[i].CatalogImplemented = known && entry.Implementation != "not_implemented"
		providerRows[i].CatalogLiveVerified = known && entry.LiveVerified
		providerRows[i].VerifiedSources = verifiedByProvider[providerRows[i].ID]
	}
	out["provider_health"] = providerRows
	out["revision"] = p.service.Current().Revision
	return out
}

func (p *ControlPlane) providerHealth(s *Server, accounts []accountHealth, entries []audit.Entry) []providerHealth {
	type aggregate struct {
		providerHealth
		hasHealthy, hasDegraded, hasBlocked, hasCooldown, hasExhausted, hasBroken, hasDisabled bool
		authenticated, authRequired, authChecked                                               bool
	}
	type runtimeAggregate struct {
		health                   string
		active                   int
		completed, failures      uint64
		lastSuccess, lastFailure time.Time
	}
	byID := map[string]*aggregate{}
	ordered := []string{}
	ensure := func(id string) *aggregate {
		if current := byID[id]; current != nil {
			return current
		}
		current := &aggregate{providerHealth: providerHealth{ID: id, Enabled: true, Health: "untested", AuthStatus: "not_checked", Accounts: []string{}, Sources: []string{}}}
		byID[id] = current
		ordered = append(ordered, id)
		return current
	}
	for _, provider := range s.cfg.Providers {
		row := ensure(provider.ID)
		row.Enabled = provider.Enabled
		row.PoolStrategy = provider.PoolStrategy
	}
	for _, account := range accounts {
		row := ensure(account.Provider)
		row.Accounts = append(row.Accounts, account.ID)
		row.Active += account.Active
		row.Limit += account.Limit
		row.Completed += account.Completed
		row.Failures += account.Failures
		if account.LastSuccess != nil && account.LastSuccess.After(valueTime(row.LastSuccess)) {
			value := *account.LastSuccess
			row.LastSuccess = &value
		}
		if account.LastFailure != nil && account.LastFailure.After(valueTime(row.LastFailure)) {
			value := *account.LastFailure
			row.LastFailure = &value
		}
		if account.LastAuthCheckedAt != nil && account.LastAuthCheckedAt.After(valueTime(row.LastAuthCheckedAt)) {
			value := *account.LastAuthCheckedAt
			row.LastAuthCheckedAt = &value
		}
		switch account.AuthStatus {
		case "authenticated":
			row.authenticated, row.authChecked = true, true
		case "not_checked", "":
		default:
			row.authChecked = true
			if authRequiresLogin(account.AuthStatus) {
				row.authRequired = true
			}
		}
	}
	runtimeByID := map[string]runtimeAggregate{}
	latestValidation := map[string]time.Time{}
	for _, entry := range entries {
		if entry.Kind != "validation" {
			continue
		}
		if current, ok := latestValidation[entry.Resource]; !ok || entry.CheckedAt.After(current) {
			latestValidation[entry.Resource] = entry.CheckedAt
		}
	}
	for _, status := range s.Router.Status() {
		runtimeByID[status.ID] = runtimeAggregate{health: status.Health, active: status.Active, completed: status.Completed, failures: status.Failures, lastSuccess: status.LastSuccess, lastFailure: status.LastFailure}
	}
	for _, source := range s.cfg.Sources {
		row := ensure(source.Provider)
		row.Sources = append(row.Sources, source.ID)
		if checked, ok := latestValidation[source.ID]; ok && checked.After(valueTime(row.LastValidatedAt)) {
			value := checked
			row.LastValidatedAt = &value
		}
		status, ok := runtimeByID[source.ID]
		if !ok {
			continue
		}
		// Account-bound source capacity and counters are already represented by
		// the account row; unbound sources contribute their own boundaries here.
		if source.AccountID == "" {
			row.Active += status.active
			row.Limit += source.MaxInflight
			row.Completed += status.completed
			row.Failures += status.failures
			if status.lastSuccess.After(valueTime(row.LastSuccess)) {
				value := status.lastSuccess
				row.LastSuccess = &value
			}
			if status.lastFailure.After(valueTime(row.LastFailure)) {
				value := status.lastFailure
				row.LastFailure = &value
			}
		}
		switch status.health {
		case "healthy":
			row.hasHealthy = true
		case "degraded":
			row.hasDegraded = true
		case "blocked":
			row.hasBlocked = true
		case "cooldown":
			row.hasCooldown = true
		case "exhausted":
			row.hasExhausted = true
		case "broken":
			row.hasBroken = true
		case "disabled":
			row.hasDisabled = true
		}
	}
	out := make([]providerHealth, 0, len(ordered))
	for _, id := range ordered {
		row := byID[id]
		switch {
		case !row.Enabled:
			row.Health = "disabled"
		case row.authRequired && row.Completed == 0 && row.Failures == 0 && !row.hasHealthy:
			row.Health = "auth_required"
		case row.hasHealthy && (row.hasDegraded || row.hasBlocked || row.hasCooldown || row.hasExhausted || row.hasBroken):
			row.Health = "degraded"
		case row.hasHealthy:
			row.Health = "healthy"
		case row.hasBlocked:
			row.Health = "blocked"
		case row.hasCooldown:
			row.Health = "cooldown"
		case row.hasExhausted:
			row.Health = "exhausted"
		case row.hasBroken:
			row.Health = "broken"
		case row.hasDisabled && len(row.Sources) > 0:
			row.Health = "disabled"
		case row.Completed > 0 && row.Failures > 0:
			row.Health = "degraded"
		case row.Completed > 0:
			row.Health = "healthy"
		case row.Failures > 0:
			row.Health = "broken"
		}
		switch {
		case row.authRequired:
			row.AuthStatus = "auth_required"
		case row.authenticated:
			row.AuthStatus = "authenticated"
		case row.authChecked:
			row.AuthStatus = "checked"
		}
		out = append(out, row.providerHealth)
	}
	return out
}

func (p *ControlPlane) accountHealth(s *Server, entries []audit.Entry) []accountHealth {
	capacities, _ := s.Router.CapacityStatus()
	providerEnabled := map[string]bool{}
	for _, provider := range s.cfg.Providers {
		providerEnabled[provider.ID] = provider.Enabled
	}
	capacityByID := map[string]routingCapacity{}
	for _, value := range capacities {
		capacityByID[value.ID] = routingCapacity{active: value.Active, limit: value.Limit}
	}
	type sourceAggregate struct {
		completed, failures      uint64
		lastSuccess, lastFailure time.Time
		cooldown, blocked        bool
	}
	bySource := map[string]sourceAggregate{}
	for _, value := range s.Router.Status() {
		bySource[value.ID] = sourceAggregate{completed: value.Completed, failures: value.Failures, lastSuccess: value.LastSuccess, lastFailure: value.LastFailure, cooldown: !value.Cooldown.IsZero() && time.Now().Before(value.Cooldown), blocked: value.Blocked}
	}
	latestAuth := map[string]audit.Entry{}
	latestValidation := map[string]audit.Entry{}
	for _, entry := range entries {
		switch entry.Kind {
		case "authentication":
			if current, ok := latestAuth[entry.Resource]; !ok || entry.CheckedAt.After(current.CheckedAt) {
				latestAuth[entry.Resource] = entry
			}
		case "validation":
			if current, ok := latestValidation[entry.Resource]; !ok || entry.CheckedAt.After(current.CheckedAt) {
				latestValidation[entry.Resource] = entry
			}
		}
	}
	out := make([]accountHealth, 0, len(s.cfg.Accounts))
	for _, account := range s.cfg.Accounts {
		health := accountHealth{ID: account.ID, Provider: account.ProviderID, Health: "untested", AuthStatus: "not_checked", Limit: account.MaxInflight, Weight: account.Weight, Sources: []string{}}
		for _, provider := range s.cfg.Providers {
			if provider.ID == account.ProviderID {
				health.PoolStrategy = provider.PoolStrategy
				break
			}
		}
		if capacity, ok := capacityByID[account.ID]; ok {
			health.Active = capacity.active
			health.Limit = capacity.limit
		}
		for _, source := range s.cfg.Sources {
			if source.AccountID != account.ID {
				continue
			}
			health.Sources = append(health.Sources, source.ID)
			aggregate := bySource[source.ID]
			health.Completed += aggregate.completed
			health.Failures += aggregate.failures
			if aggregate.lastSuccess.After(valueTime(health.LastSuccess)) {
				v := aggregate.lastSuccess
				health.LastSuccess = &v
			}
			if aggregate.lastFailure.After(valueTime(health.LastFailure)) {
				v := aggregate.lastFailure
				health.LastFailure = &v
			}
			if entry, ok := latestValidation[source.ID]; ok && entry.CheckedAt.After(valueTime(health.LastValidatedAt)) {
				v := entry.CheckedAt
				health.LastValidatedAt = &v
			}
		}
		if entry, ok := latestAuth[account.ID]; ok {
			health.AuthStatus = entry.Status
			checked := entry.CheckedAt
			health.LastAuthCheckedAt = &checked
		}
		switch {
		case !account.Enabled || !providerEnabled[account.ProviderID]:
			health.Health = "disabled"
		case authRequiresLogin(health.AuthStatus):
			health.Health = "auth_required"
		case health.Active >= health.Limit && health.Limit > 0:
			health.Health = "exhausted"
		default:
			for _, source := range s.cfg.Sources {
				if source.AccountID != account.ID {
					continue
				}
				aggregate := bySource[source.ID]
				if aggregate.blocked {
					health.Health = "blocked"
					break
				}
				if aggregate.cooldown {
					health.Health = "cooldown"
				}
			}
			if health.Health == "untested" && health.Completed > 0 {
				health.Health = "healthy"
			} else if health.Health == "untested" && health.Failures > 0 {
				health.Health = "degraded"
			}
		}
		out = append(out, health)
	}
	return out
}

func authRequiresLogin(status string) bool {
	switch status {
	case "unauthenticated", "expired", "rejected", "login_required", "challenge_or_access_denied":
		return true
	default:
		return false
	}
}

type routingCapacity struct{ active, limit int }

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
