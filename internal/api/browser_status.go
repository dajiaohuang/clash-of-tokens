package api

import (
	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/config"
	"context"
	"net/http"
	"sync"
	"time"
)

func profileBindings(c config.Config, profileID string) (accounts, sources int) {
	accountIDs := map[string]bool{}
	for _, account := range c.Accounts {
		if account.BrowserProfileID == profileID {
			accounts++
			accountIDs[account.ID] = true
		}
	}
	for _, source := range c.Sources {
		if accountIDs[source.AccountID] {
			sources++
		}
	}
	return accounts, sources
}

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
	current := p.service.Current().Config
	profiles := current.BrowserProfiles
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
				out[i].MaxSessions = current.Browser.MaxSessions
				out[i].BoundAccounts, out[i].BoundSources = profileBindings(current, profiles[i].ID)
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
