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
	ID              string     `json:"id"`
	Provider        string     `json:"provider"`
	Health          string     `json:"health"`
	AuthStatus      string     `json:"auth_status"`
	Active          int        `json:"active"`
	Limit           int        `json:"limit"`
	Sources         []string   `json:"sources"`
	Completed       uint64     `json:"completed"`
	Failures        uint64     `json:"failures"`
	LastSuccess     *time.Time `json:"last_success,omitempty"`
	LastFailure     *time.Time `json:"last_failure,omitempty"`
	LastValidatedAt *time.Time `json:"last_validated_at,omitempty"`
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
			latest[checkKey{entry.Resource, entry.Model, entry.Protocol}] = entry
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
		meta := metadata[source.CredentialRef]
		descriptor, _ := providerdef.Lookup(source.Adapter)
		entry, known := providers[source.Provider]
		v := sourceVerification{Source: source.ID, Provider: source.Provider, Account: source.AccountID, CatalogImplemented: known && entry.Adapter == source.Adapter && entry.Implementation != "not_implemented", CatalogLiveVerified: known && entry.Adapter == source.Adapter && entry.LiveVerified, CredentialState: "not_configured", CredentialVersion: meta.Version, Models: []modelVerification{}}
		switch {
		case source.CredentialRef != "":
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
	out["account_health"] = p.accountHealth(s, entries)
	out["revision"] = p.service.Current().Revision
	return out
}

func (p *ControlPlane) accountHealth(s *Server, entries []audit.Entry) []accountHealth {
	capacities, _ := s.Router.CapacityStatus()
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
		health := accountHealth{ID: account.ID, Provider: account.ProviderID, Health: "untested", AuthStatus: "not_checked", Limit: account.MaxInflight, Sources: []string{}}
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
		}
		switch {
		case !account.Enabled:
			health.Health = "disabled"
		case health.AuthStatus == "unauthenticated" || health.AuthStatus == "expired" || health.AuthStatus == "rejected":
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

type routingCapacity struct{ active, limit int }

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
