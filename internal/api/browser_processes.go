package api

import (
	"crypto/rand"
	"errors"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type browserProcessView struct {
	ID         string     `json:"id"`
	Profile    string     `json:"profile"`
	PID        int        `json:"pid"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	State      string     `json:"state"`
}
type ownedBrowserProcess struct {
	view browserProcessView
	cmd  *exec.Cmd
	done chan struct{}
}
type browserProcesses struct {
	mu    sync.Mutex
	items map[string]*ownedBrowserProcess
}

func (b *browserProcesses) start(profile string, cmd *exec.Cmd) (browserProcessView, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.items == nil {
		b.items = map[string]*ownedBrowserProcess{}
	}
	running := 0
	for _, p := range b.items {
		if p.view.FinishedAt == nil {
			running++
			if p.view.Profile == profile {
				return browserProcessView{}, errors.New("this profile already has an owned browser process")
			}
		}
	}
	if running >= 64 {
		return browserProcessView{}, errors.New("owned browser process limit reached")
	}
	if err := cmd.Start(); err != nil {
		return browserProcessView{}, errors.New("cannot start login browser")
	}
	if len(b.items) >= 128 {
		oldest := ""
		for id, p := range b.items {
			if p.view.FinishedAt != nil && (oldest == "" || p.view.StartedAt.Before(b.items[oldest].view.StartedAt)) {
				oldest = id
			}
		}
		if oldest != "" {
			delete(b.items, oldest)
		}
	}
	p := &ownedBrowserProcess{view: browserProcessView{ID: rand.Text(), Profile: profile, PID: cmd.Process.Pid, StartedAt: time.Now().UTC(), State: "running"}, cmd: cmd, done: make(chan struct{})}
	b.items[p.view.ID] = p
	go func() {
		_ = cmd.Wait()
		b.mu.Lock()
		now := time.Now().UTC()
		p.view.FinishedAt = &now
		if p.view.State == "stop_requested" {
			p.view.State = "stopped"
		} else {
			p.view.State = "exited"
		}
		close(p.done)
		b.mu.Unlock()
	}()
	return p.view, nil
}

func (b *browserProcesses) list() []browserProcessView {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]browserProcessView, 0, len(b.items))
	for _, p := range b.items {
		v := p.view
		if v.FinishedAt != nil {
			t := *v.FinishedAt
			v.FinishedAt = &t
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (b *browserProcesses) stop(id string) error {
	b.mu.Lock()
	p := b.items[id]
	if p == nil {
		b.mu.Unlock()
		return errors.New("no browser process owned by this gateway has that launch ID")
	}
	if p.view.FinishedAt != nil {
		b.mu.Unlock()
		return nil
	}
	// Use the retained os.Process handle, never a looked-up PID, process name,
	// port owner or CDP Browser.close (which could target an external browser).
	if err := p.cmd.Process.Kill(); err != nil {
		b.mu.Unlock()
		return errors.New("owned browser process could not be stopped; refresh its state")
	}
	p.view.State = "stop_requested"
	done := p.done
	b.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
	}
	return nil
}

func (p *ControlPlane) browserProcessesAdmin(w http.ResponseWriter, r *http.Request) bool {
	const root = "/admin/browser_profiles/processes"
	if r.URL.Path == root {
		if r.Method != "GET" {
			fail(w, 405, "method not allowed")
		} else {
			reply(w, p.browsers.list())
		}
		return true
	}
	if !strings.HasPrefix(r.URL.Path, root+"/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, root+"/"), "/")
	if len(parts) != 2 || parts[1] != "stop" || r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	var input struct {
		Confirm bool `json:"confirm"`
	}
	if decodeInput(w, r, &input, 1024) != nil || !input.Confirm {
		fail(w, 400, "confirm stopping this owned browser process")
		return true
	}
	if err := p.browsers.stop(parts[0]); err != nil {
		fail(w, 409, err.Error())
		return true
	}
	reply(w, map[string]string{"status": "stop_requested"})
	return true
}
