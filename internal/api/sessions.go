package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"clash-of-tokens/internal/chatgptweb"
	"clash-of-tokens/internal/evidence"
	"clash-of-tokens/internal/routing"
	"clash-of-tokens/internal/session"
)

func (p *ControlPlane) sessionAdmin(w http.ResponseWriter, r *http.Request, s *Server) bool {
	if r.URL.Path != "/admin/sessions" && !strings.HasPrefix(r.URL.Path, "/admin/sessions/") {
		return false
	}
	if r.URL.Path == "/admin/sessions" && r.Method == "GET" {
		type row struct {
			session.Metadata
			Provider string `json:"provider"`
			Account  string `json:"account"`
		}
		out := []row{}
		unsupported := []string{}
		failures := []string{}
		type capability struct {
			Source    string `json:"source"`
			Adapter   string `json:"adapter"`
			Supported bool   `json:"supported"`
			Reason    string `json:"reason"`
		}
		capabilities := []capability{}
		truncated := false
		for i, source := range s.cfg.Sources {
			cap := capability{Source: source.ID, Adapter: source.Adapter, Reason: "stateful session inventory is adapter-specific"}
			capabilities = append(capabilities, cap)
			var items []session.Metadata
			var err error
			if source.Adapter == "chatgpt-web" {
				items, err = chatgptweb.ReadSessions(s.cfg.SourceBrowser(source), source.ID)
			} else {
				items, err = s.client(i).Sessions()
			}
			if err != nil {
				if strings.Contains(err.Error(), "not implemented") {
					unsupported = append(unsupported, source.ID)
				} else {
					failures = append(failures, source.ID)
				}
				continue
			}
			capabilities[len(capabilities)-1].Supported = true
			capabilities[len(capabilities)-1].Reason = "local conversation metadata"
			for _, item := range items {
				if len(out) >= 1000 {
					truncated = true
					break
				}
				out = append(out, row{item, source.Provider, source.AccountID})
			}
		}
		reply(w, map[string]any{"items": out, "unsupported_sources": unsupported, "failed_sources": failures, "capabilities": capabilities, "truncated": truncated})
		return true
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/sessions/"), "/")
	if r.Method != "POST" || len(parts) != 3 || (parts[2] != "expire" && parts[2] != "clear") {
		fail(w, 405, "use POST source/session/expire or clear")
		return true
	}
	index := -1
	for i, source := range s.cfg.Sources {
		if source.ID == parts[0] {
			index = i
		}
	}
	if index < 0 {
		fail(w, 404, "unknown source")
		return true
	}
	source := s.cfg.Sources[index]
	model := source.Models[0]
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.Runtime.QueueTimeoutMS)*time.Millisecond)
	defer cancel()
	lease, err := s.Router.Acquire(ctx, routing.Query{Model: source.ID + "/" + model.ID, Protocol: model.Protocols[0], Probe: true})
	if err != nil {
		fail(w, 409, "source is busy; session was not changed")
		return true
	}
	defer lease.ReleaseAdministrative()
	if err = s.client(index).ChangeSession(parts[1], parts[2]); err != nil {
		fail(w, 400, err.Error())
		return true
	}
	recorded := p.evidence.Append(evidence.Entry{Revision: p.service.Current().Revision, Kind: "session", Resource: source.ID, Method: parts[2], Status: "completed"}) == nil
	reply(w, map[string]any{"status": "completed", "history_recorded": recorded})
	return true
}
