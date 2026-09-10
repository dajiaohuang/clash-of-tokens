package routing

import "sort"

type CapacityStatus struct {
	ID      string   `json:"id"`
	Active  int      `json:"active"`
	Limit   int      `json:"limit"`
	Sources []string `json:"sources"`
	Retired bool     `json:"retired"`
}

// CapacityStatus includes slots held by retired generations. Shared quota
// counters are reported once rather than summed once per source.
func (r *Router) CapacityStatus() (accounts, domains []CapacityStatus) {
	r.mu.Lock()
	defer r.mu.Unlock()
	accounts = []CapacityStatus{}
	domains = []CapacityStatus{}
	accountSources, domainSources := map[string][]string{}, map[string][]string{}
	accountLimits := map[string]int{}
	for _, a := range r.cfg.Accounts {
		accountLimits[a.ID] = a.MaxInflight
	}
	for _, s := range r.cfg.Sources {
		if s.AccountID != "" {
			accountSources[s.AccountID] = append(accountSources[s.AccountID], s.ID)
		}
		domainSources[s.QuotaDomain] = append(domainSources[s.QuotaDomain], s.ID)
	}
	for id, limit := range accountLimits {
		active := 0
		if q := r.accountStates[id]; q != nil {
			active = q.active
		}
		members := append([]string{}, accountSources[id]...)
		sort.Strings(members)
		accounts = append(accounts, CapacityStatus{ID: id, Active: active, Limit: limit, Sources: members})
	}
	for id, q := range r.accountStates {
		if _, exists := accountLimits[id]; !exists && q.active > 0 {
			accounts = append(accounts, CapacityStatus{ID: id, Active: q.active, Limit: q.limit, Sources: []string{}, Retired: true})
		}
	}
	for id, q := range r.quotaStates {
		members, exists := domainSources[id]
		if !exists && q.active == 0 {
			continue
		}
		members = append([]string{}, members...)
		sort.Strings(members)
		domains = append(domains, CapacityStatus{ID: id, Active: q.active, Limit: q.limit, Sources: members, Retired: !exists})
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	sort.Slice(domains, func(i, j int) bool { return domains[i].ID < domains[j].ID })
	return
}
