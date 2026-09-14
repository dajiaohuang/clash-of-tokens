package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/evidence"
)

const maxManagementJobs = 128
const maxManagementActive = 4

type managementJob struct {
	ID         string               `json:"id"`
	Path       string               `json:"path"`
	State      string               `json:"state"`
	Revision   uint64               `json:"revision"`
	CreatedAt  time.Time            `json:"created_at"`
	FinishedAt time.Time            `json:"finished_at,omitempty"`
	Status     int                  `json:"status,omitempty"`
	Result     json.RawMessage      `json:"result,omitempty"`
	Events     []managementJobEvent `json:"events"`
	Stage      string               `json:"stage"`
	cancel     context.CancelFunc
	resource   string
}
type managementJobEvent struct {
	Sequence int       `json:"sequence"`
	Stage    string    `json:"stage"`
	At       time.Time `json:"at"`
}
type managementJobs struct {
	mu        sync.Mutex
	items     []*managementJob
	closed    bool
	resources map[string]bool
}

func (j *managementJobs) close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closed = true
	for _, item := range j.items {
		if item.cancel != nil {
			item.cancel()
		}
	}
}

type operationKey struct{}
type jobReservedKey struct{}
type operationCommitKey struct{}
type operationOutcomeKey struct{}
type operationOutcome struct {
	committed *operationVersion
	progress  func(string)
}
type operationVersion struct {
	revision uint64
	vault    [32]byte
}

func (p *ControlPlane) operationVersion() operationVersion {
	metadata := p.vault.List()
	for i := range metadata {
		metadata[i].LastUsedAt = nil
		metadata[i].State = ""
	}
	raw, _ := json.Marshal(metadata)
	return operationVersion{p.service.Current().Revision, sha256.Sum256(raw)}
}
func (p *ControlPlane) operationRequest(r *http.Request) *http.Request {
	version, ok := r.Context().Value(operationKey{}).(operationVersion)
	if !ok {
		version = p.operationVersion()
	}
	ctx := context.WithValue(r.Context(), operationKey{}, version)
	ctx = context.WithValue(ctx, operationCommitKey{}, func(fn func() error) error {
		if outcome, ok := r.Context().Value(operationOutcomeKey{}).(*operationOutcome); ok && outcome.progress != nil {
			outcome.progress("committing")
		}
		p.changes.Lock()
		defer p.changes.Unlock()
		if r.Context().Err() != nil || version != p.operationVersion() {
			return errors.New("operation is stale; retry with current configuration")
		}
		if err := fn(); err != nil {
			return err
		}
		if outcome, ok := r.Context().Value(operationOutcomeKey{}).(*operationOutcome); ok {
			committed := p.operationVersion()
			outcome.committed = &committed
		}
		return nil
	})
	return r.WithContext(ctx)
}
func commitOperation(r *http.Request, fn func() error) error {
	if commit, ok := r.Context().Value(operationCommitKey{}).(func(func() error) error); ok {
		return commit(fn)
	}
	return fn()
}
func (p *ControlPlane) appendOperationEvidence(r *http.Request, entry evidence.Entry) error {
	p.changes.Lock()
	defer p.changes.Unlock()
	version, ok := r.Context().Value(operationKey{}).(operationVersion)
	if !ok || r.Context().Err() != nil || version != p.operationVersion() {
		return errors.New("operation evidence is stale")
	}
	entry.Revision = version.revision
	return p.evidence.Append(entry)
}
func managementOperation(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 4 && parts[0] == "admin" {
		switch parts[1] {
		case "sources":
			return parts[3] == "validate" || parts[3] == "discover"
		case "accounts":
			return parts[3] == "verify-login" || parts[3] == "fill-login" || parts[3] == "verify" || parts[3] == "validate" || parts[3] == "check-login" || parts[3] == "login" || parts[3] == "acquire-session"
		case "providers":
			return parts[3] == "validate"
		case "credentials":
			return parts[3] == "check" || parts[3] == "refresh"
		}
	}
	return path == "/admin/device/check" || path == "/admin/discovery/accounts" || path == "/admin/browser_profiles/status" || path == "/admin/browser_profiles/discover" || path == "/admin/browser_profiles/setup-login" || path == "/admin/credentials/import-browser"
}

// Jobs coordinate shared account/profile resources without serializing unrelated
// network work. Provider validation uses the same selected source as its handler.
func (p *ControlPlane) operationResource(path string, body json.RawMessage) string {
	c := p.service.Current().Config
	if path == "/admin/credentials/import-browser" || path == "/admin/browser_profiles/setup-login" {
		var input struct {
			Profile json.RawMessage `json:"profile"`
		}
		if json.Unmarshal(body, &input) == nil {
			var id string
			if json.Unmarshal(input.Profile, &id) == nil && id != "" {
				return "profile:" + id
			}
			var profile struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(input.Profile, &profile) == nil && profile.ID != "" {
				return "profile:" + profile.ID
			}
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 {
		return path
	}
	sourceID, accountID := "", ""
	switch parts[1] {
	case "credentials":
		return "credential:" + parts[2]
	case "sources":
		sourceID = parts[2]
	case "accounts":
		accountID = parts[2]
	case "providers":
		for _, s := range c.Sources {
			if s.Provider == parts[2] {
				sourceID = s.ID
				break
			}
		}
		if sourceID == "" {
			for _, a := range c.Accounts {
				if a.ProviderID == parts[2] {
					accountID = a.ID
					break
				}
			}
		}
	}
	for _, s := range c.Sources {
		if s.ID == sourceID {
			accountID = s.AccountID
			if s.Adapter == "app-device" {
				return "device"
			}
			break
		}
	}
	if accountID != "" {
		for _, a := range c.Accounts {
			if a.ID == accountID && a.BrowserProfileID != "" {
				return "profile:" + a.BrowserProfileID
			}
		}
		return "account:" + accountID
	}
	if sourceID != "" {
		return "source:" + sourceID
	}
	return path
}
func (p *ControlPlane) jobsAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/admin/jobs" && r.Method == "POST" {
		var input struct {
			Path  string          `json:"path"`
			Input json.RawMessage `json:"input"`
		}
		if decodeInput(w, r, &input, 64<<10) != nil || !managementOperation(input.Path) {
			fail(w, 400, "unsupported management operation")
			return
		}
		if len(input.Input) == 0 {
			input.Input = json.RawMessage("{}")
		}
		// Capture a consistent version/resource before accepting the operation.
		p.changes.Lock()
		version := p.operationVersion()
		resource := p.operationResource(input.Path, input.Input)
		p.changes.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		ctx = context.WithValue(ctx, operationKey{}, version)
		job := &managementJob{ID: rand.Text(), Path: input.Path, State: "running", Revision: version.revision, CreatedAt: time.Now().UTC(), cancel: cancel, resource: resource}
		job.Stage = "accepted"
		job.Events = []managementJobEvent{{Sequence: 1, Stage: "accepted", At: job.CreatedAt}}
		p.jobs.mu.Lock()
		if p.jobs.resources[resource] {
			p.jobs.mu.Unlock()
			cancel()
			fail(w, 409, "resource already has a running management operation")
			return
		}
		if p.jobs.closed || len(p.jobs.resources) >= maxManagementActive {
			p.jobs.mu.Unlock()
			cancel()
			fail(w, 503, "management job capacity exhausted")
			return
		}
		if p.jobs.resources == nil {
			p.jobs.resources = map[string]bool{}
		}
		p.jobs.resources[resource] = true
		if len(p.jobs.items) >= maxManagementJobs {
			for i, old := range p.jobs.items {
				if old.State != "running" {
					p.jobs.items = append(p.jobs.items[:i], p.jobs.items[i+1:]...)
					break
				}
			}
		}
		p.jobs.items = append(p.jobs.items, job)
		accepted := map[string]any{"job_id": job.ID, "state": job.State, "revision": job.Revision}
		p.jobs.mu.Unlock()
		ctx = context.WithValue(ctx, jobReservedKey{}, true)
		req, _ := http.NewRequestWithContext(ctx, "POST", input.Path, bytes.NewReader(input.Input))
		req.Host = r.Host
		req.Header.Set("Authorization", "Bearer "+p.admin)
		go p.runManagementJob(job, req, version)
		w.Header().Set("Location", "/admin/jobs/"+job.ID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		reply(w, accepted)
		return
	}
	p.jobs.mu.Lock()
	defer p.jobs.mu.Unlock()
	if r.URL.Path == "/admin/jobs" && r.Method == "GET" {
		reply(w, p.jobs.items)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/jobs/")
	for _, job := range p.jobs.items {
		if job.ID != id {
			continue
		}
		if r.Method == "GET" {
			reply(w, job)
			return
		}
		if r.Method == "DELETE" {
			if job.cancel != nil {
				job.cancel()
			}
			reply(w, map[string]string{"state": "cancellation_requested"})
			return
		}
		fail(w, 405, "method not allowed")
		return
	}
	fail(w, 404, "unknown management job")
}

type jobResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func (r *jobResponse) Header() http.Header { return r.header }
func (r *jobResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *jobResponse) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = 200
	}
	if r.body.Len()+len(b) > 1<<20 {
		r.overflow = true
		return 0, io.ErrShortBuffer
	}
	return r.body.Write(b)
}
func (p *ControlPlane) runManagementJob(job *managementJob, r *http.Request, version operationVersion) {
	out := &jobResponse{header: make(http.Header)}
	outcome := &operationOutcome{}
	outcome.progress = func(stage string) {
		p.jobs.mu.Lock()
		defer p.jobs.mu.Unlock()
		job.Stage = stage
		if len(job.Events) < 15 {
			job.Events = append(job.Events, managementJobEvent{Sequence: len(job.Events) + 1, Stage: stage, At: time.Now().UTC()})
		}
	}
	outcome.progress("executing")
	r = r.WithContext(context.WithValue(r.Context(), operationOutcomeKey{}, outcome))
	defer func() {
		panicValue := recover()
		current := p.operationVersion()
		committed := outcome.committed != nil && out.status < 400
		if committed {
			version = *outcome.committed
		}
		ownRefresh := false
		if ((strings.HasPrefix(job.Path, "/admin/credentials/") && strings.HasSuffix(job.Path, "/refresh")) || job.Path == "/admin/credentials/import-browser") && out.status == 200 && current.revision == version.revision {
			var result struct {
				ID      string `json:"id"`
				Version uint64 `json:"version"`
			}
			if json.Unmarshal(out.body.Bytes(), &result) == nil {
				for _, meta := range p.vault.List() {
					if meta.ID == result.ID && meta.Version == result.Version {
						ownRefresh = true
					}
				}
			}
		}
		p.jobs.mu.Lock()
		defer p.jobs.mu.Unlock()
		delete(p.jobs.resources, job.resource)
		job.Status = out.status
		job.State = "completed"
		switch {
		case panicValue != nil || out.overflow:
			job.State = "failed"
			job.Status = 500
		case r.Context().Err() == context.DeadlineExceeded && !committed:
			job.State = "timed_out"
		case r.Context().Err() != nil && !committed:
			job.State = "canceled"
		case current != version && !ownRefresh:
			job.State = "stale"
		case out.status >= 400:
			job.State = "failed"
		}
		if job.State == "completed" || (job.State == "failed" && panicValue == nil && !out.overflow) {
			if json.Valid(out.body.Bytes()) {
				job.Result = append(json.RawMessage(nil), out.body.Bytes()...)
			}
		}
		job.FinishedAt = time.Now().UTC()
		job.Stage = job.State
		job.Events = append(job.Events, managementJobEvent{Sequence: len(job.Events) + 1, Stage: job.State, At: job.FinishedAt})
		job.cancel()
		job.cancel = nil
	}()
	if version != p.operationVersion() {
		return
	}
	p.ServeHTTP(out, r)
}
