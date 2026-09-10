package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"time"
)

// Choose an eligible account within each provider before comparing providers.
// Explicit fallback/select source ordering intentionally takes precedence.
func (r *Router) poolPreferences(q Query, candidates []int, now time.Time) map[string]string {
	type preference struct {
		id    string
		score float64
		last  uint64
	}
	best := map[string]preference{}
	for _, index := range candidates {
		target := r.targets[index]
		source := r.cfg.Sources[target.Source]
		a := r.accounts[target.Source]
		if a == nil {
			continue
		}
		if ok, _ := r.eligible(target, q); !ok {
			continue
		}
		st, quota := r.state[target.Source], r.quotas[target.Source]
		if a.active >= a.limit || st.active >= source.MaxInflight || quota.active >= quota.limit || now.Before(st.cooldown) || now.Before(quota.cooldown) {
			continue
		}
		var score float64
		switch r.poolStrategies[target.Source] {
		case "weighted":
			score = a.virtualFinish
		case "least-load":
			score = float64(a.active) / float64(a.limit)
		case "sticky":
			if q.Affinity != "" {
				digest := sha256.Sum256([]byte(q.Affinity + "\x00" + source.AccountID))
				score = float64(binary.BigEndian.Uint64(digest[:8])) / float64(^uint64(0))
			}
		}
		old, exists := best[source.Provider]
		if !exists || score < old.score || (score == old.score && a.lastDispatch < old.last) {
			best[source.Provider] = preference{source.AccountID, score, a.lastDispatch}
		}
	}
	out := make(map[string]string, len(best))
	for provider, p := range best {
		out[provider] = p.id
	}
	return out
}
