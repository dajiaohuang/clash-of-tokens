package api

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/evidence"
	"clash-of-tokens/internal/providers/appdevice"
	"context"
	"net/http"
	"time"
)

type deviceProviderStatus struct {
	SourceID   string `json:"source_id"`
	Provider   string `json:"provider"`
	Installed  bool   `json:"installed"`
	LoginState string `json:"login_state"`
	LastTest   string `json:"last_test"`
}

type deviceCheckResponse struct {
	Revision       uint64                 `json:"revision"`
	CheckedAt      time.Time              `json:"checked_at"`
	RestartPending bool                   `json:"restart_pending"`
	Report         appdevice.CheckReport  `json:"report"`
	Providers      []deviceProviderStatus `json:"providers"`
}

// deviceCheckAdmin performs a bounded, read-only Android doctor run. It does
// not start an emulator, launch an app, modify the clipboard, or send a
// message. A successful source validation is the only evidence used to label
// an app session as detected.
func (p *ControlPlane) deviceCheckAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if r.URL.Path != "/admin/device/check" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(min(s.cfg.Runtime.RequestTimeoutMS, 45000))*time.Millisecond)
	defer cancel()
	report := appdevice.Check(ctx, s.cfg.Device)
	history := p.evidence.List()
	latest := map[string]evidence.Entry{}
	for i := len(history) - 1; i >= 0; i-- {
		entry := history[i]
		if entry.Kind == "validation" {
			if _, exists := latest[entry.Resource]; !exists {
				latest[entry.Resource] = entry
			}
		}
	}
	checks := map[string]bool{}
	for _, check := range report.Checks {
		checks[check.Name] = check.OK
	}
	providers := make([]deviceProviderStatus, 0)
	for _, source := range s.cfg.Sources {
		if source.Adapter != "app-device" {
			continue
		}
		status := deviceProviderStatus{SourceID: source.ID, Provider: source.Provider, Installed: checks[source.Provider], LoginState: "not_observed", LastTest: "not_tested"}
		if entry, ok := latest[source.ID]; ok {
			if entry.Status == "verified" {
				status.LoginState = "detected"
				status.LastTest = "pass"
			} else {
				status.LastTest = "failed"
			}
		}
		providers = append(providers, status)
	}
	desired := p.service.Current()
	reply(w, deviceCheckResponse{Revision: desired.Revision, CheckedAt: time.Now().UTC(), RestartPending: !sameDevice(s.cfg.Device, desired.Config.Device), Report: report, Providers: providers})
	return true
}

func sameDevice(a, b config.Device) bool {
	return a == b
}
