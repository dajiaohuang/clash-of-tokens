package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/evidence"
)

type generation struct {
	server  *Server
	refs    int
	retired bool
}

// ControlPlane owns durable configuration and request-pinned runtime snapshots.
type ControlPlane struct {
	evidence   *evidence.Store
	mu         sync.Mutex
	changes    sync.Mutex
	current    *generation
	service    *config.Service
	startup    config.Config
	key, admin string
	vault      *credentials.Store
	closed     bool
}

func NewControlPlane(path string, c config.Config, key, admin string, vault *credentials.Store) (*ControlPlane, error) {
	p := &ControlPlane{startup: c, key: key, admin: admin, vault: vault}
	history, historyErr := evidence.Open(path + ".evidence")
	if historyErr != nil {
		return nil, historyErr
	}
	p.evidence = history
	svc, err := config.OpenService(path, c, p.prepare)
	if err != nil {
		return nil, err
	}
	p.service = svc
	desired := svc.Current().Config
	p.startup = desired
	server, err := NewWithVault(desired, key, admin, vault)
	if err != nil {
		return nil, err
	}
	p.current = &generation{server: server}
	return p, nil
}

func (p *ControlPlane) prepare(desired config.Config) (func(), func(), error) {
	effective := desired
	effective.Listen = p.startup.Listen
	effective.APIKeyEnv = p.startup.APIKeyEnv
	effective.AdminKeyEnv = p.startup.AdminKeyEnv
	effective.Runtime = p.startup.Runtime
	effective.Browser = p.startup.Browser
	effective.Device = p.startup.Device
	next, err := NewWithVault(effective, p.key, p.admin, p.vault)
	if err != nil {
		return nil, nil, err
	}
	return func() {
		p.mu.Lock()
		old := p.current
		next.metrics = old.server.metrics
		next.ingress = old.server.ingress
		old.server.Router.Adopt(next.Router)
		old.retired = true
		p.current = &generation{server: next}
		closeOld := old.refs == 0
		p.mu.Unlock()
		if closeOld {
			old.server.Close()
		}
	}, next.Close, nil
}

func (p *ControlPlane) acquire() *generation {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	g := p.current
	g.refs++
	return g
}
func (p *ControlPlane) release(g *generation) {
	p.mu.Lock()
	g.refs--
	closeIt := g.retired && g.refs == 0
	p.mu.Unlock()
	if closeIt {
		g.server.Close()
	}
}
func (p *ControlPlane) Close() {
	p.changes.Lock()
	defer p.changes.Unlock()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	g := p.current
	g.retired = true
	closeIt := g.refs == 0
	p.mu.Unlock()
	if closeIt {
		g.server.Close()
	}
}

func (p *ControlPlane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Serialize administrative mutations with snapshot publication, including
	// credential deletion, so reference checks cannot race new bindings.
	mutation := strings.HasPrefix(r.URL.Path, "/admin/") && r.Method != "GET" && r.Method != "HEAD"
	if mutation {
		p.changes.Lock()
		defer p.changes.Unlock()
	}
	g := p.acquire()
	if g == nil {
		fail(w, 503, "gateway is closed")
		return
	}
	defer p.release(g)
	if strings.HasPrefix(r.URL.Path, "/admin/config") || managedResource(r.URL.Path) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !authorized(r, g.server.adminKey) {
			fail(w, 401, "authentication required")
			return
		}
		if mutation && !sameOrigin(r) {
			fail(w, 403, "cross-origin mutation rejected")
			return
		}
		if managedResource(r.URL.Path) {
			if p.sessionAdmin(w, r, g.server) {
				return
			}
			if r.URL.Path == "/admin/evidence" {
				if r.Method != "GET" {
					fail(w, 405, "method not allowed")
				} else {
					reply(w, p.evidence.List())
				}
				return
			}
			if p.sourceCheckAdmin(w, r, g.server) {
				return
			}
			if p.browserLoginAdmin(w, r) {
				return
			}
			p.resourceAdmin(w, r, g.server)
			return
		}
		p.configAdmin(w, r)
		return
	}
	g.server.ServeHTTP(w, r)
}

func sameOrigin(r *http.Request) bool {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == scheme+"://"+r.Host
}
func restartFields(active, desired config.Config) []string {
	fields := []string{}
	for _, v := range []struct {
		name string
		a, b any
	}{
		{"listen", active.Listen, desired.Listen}, {"api_key_env", active.APIKeyEnv, desired.APIKeyEnv}, {"admin_key_env", active.AdminKeyEnv, desired.AdminKeyEnv},
		{"runtime", active.Runtime, desired.Runtime}, {"browser", active.Browser, desired.Browser}, {"device", active.Device, desired.Device},
	} {
		if !reflect.DeepEqual(v.a, v.b) {
			fields = append(fields, v.name)
		}
	}
	return fields
}
func (p *ControlPlane) configAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/admin/config/schema":
			reply(w, config.Schema())
			return
		case "/admin/config":
			v := p.service.Current()
			reply(w, map[string]any{"revision": v.Revision, "config": v.Config, "restart_required": restartFields(p.startup, v.Config)})
			return
		case "/admin/config/history":
			reply(w, p.service.History())
			return
		}
	}
	if r.Method != "POST" && r.Method != "PATCH" {
		fail(w, 405, "method not allowed")
		return
	}
	var input struct {
		Revision uint64        `json:"revision"`
		Config   config.Config `json:"config"`
		Summary  string        `json:"summary"`
		Target   uint64        `json:"target_revision"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, (4<<20)+4096))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "invalid configuration transaction")
		return
	}
	var v config.Version
	var err error
	switch r.URL.Path {
	case "/admin/config/preview":
		v, err = p.service.Preview(input.Revision, input.Config)
		if err == nil {
			_, discard, compileErr := p.prepare(v.Config)
			if compileErr != nil {
				err = compileErr
			} else {
				discard()
			}
		}
	case "/admin/config":
		v, err = p.service.Apply(input.Revision, input.Config, input.Summary)
	case "/admin/config/rollback":
		v, err = p.service.Rollback(input.Revision, input.Target)
	default:
		fail(w, 404, "unknown configuration endpoint")
		return
	}
	if err != nil {
		status := 400
		if errors.Is(err, config.ErrRevisionConflict) {
			status = 409
		}
		fail(w, status, err.Error())
		return
	}
	reply(w, map[string]any{"revision": v.Revision, "config": v.Config, "restart_required": restartFields(p.startup, v.Config), "preview": r.URL.Path == "/admin/config/preview"})
}
