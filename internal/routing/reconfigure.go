package routing

// Adopt activates an already compiled router while preserving outstanding
// capacity and health. Call before exposing next to incoming requests.
func (r *Router) Adopt(next *Router) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next.capacity = r.capacity
	for i, source := range next.cfg.Sources {
		st := r.sourceStates[source.ID]
		if st == nil {
			st = next.state[i]
			r.sourceStates[source.ID] = st
		}
		if !st.enabled && source.Enabled {
			st.blocked = false
		}
		st.enabled = source.Enabled
		next.state[i] = st
		q := r.quotaStates[source.QuotaDomain]
		if q == nil {
			q = next.quotas[i]
			r.quotaStates[source.QuotaDomain] = q
		}
		q.limit = source.QuotaMaxInflight
		next.quotas[i] = q
		if source.AccountID != "" {
			a := r.accountStates[source.AccountID]
			if a == nil {
				a = next.accounts[i]
				r.accountStates[source.AccountID] = a
			}
			if a != nil {
				a.limit = next.accounts[i].limit
			}
			next.accounts[i] = a
		}
	}
	r.retired = true
	r.current = next
	// Old queued requests have not submitted upstream and must not use stale
	// routing policy. Wake them to fail promptly; held requests continue.
	for w := r.head; w != nil; w = w.next {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
	next.signal()
}
