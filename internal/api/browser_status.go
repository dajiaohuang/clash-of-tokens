package api

import (
	"clash-of-tokens/internal/browsermeta"
	"context"
	"net/http"
	"sync"
	"time"
)

func (p *ControlPlane) browserMetadataAdmin(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/admin/browser_profiles/discover" && r.URL.Path != "/admin/browser_profiles/status" {
		return false
	}
	if r.Method != "POST" {
		fail(w, 405, "method not allowed")
		return true
	}
	if r.URL.Path == "/admin/browser_profiles/discover" {
		reply(w, browsermeta.Discover(browsermeta.StandardRoots()))
		return true
	}
	profiles := p.service.Current().Config.BrowserProfiles
	out := make([]browsermeta.Status, len(profiles))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				out[i] = browsermeta.Check(ctx, profiles[i].ID, profiles[i].CDPURL)
			}
		}()
	}
	for i := range profiles {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	reply(w, out)
	return true
}
