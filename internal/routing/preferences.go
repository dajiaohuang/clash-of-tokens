package routing

import (
	"math"
	"strings"
)

// Preferences compare available candidates in listed priority order. Existing
// strategy scores and rotating ties apply only after every preference ties.
func (r *Router) comparePreferences(candidate, current int, preferences []string) int {
	if current < 0 {
		return -1
	}
	for _, preference := range preferences {
		a, b := r.preferenceValue(candidate, preference), r.preferenceValue(current, preference)
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func (r *Router) preferenceValue(index int, preference string) float64 {
	target := r.targets[index]
	source := r.cfg.Sources[target.Source]
	switch preference {
	case "lower_latency":
		if latency := r.state[target.Source].latency; latency > 0 {
			return latency
		}
		return math.Inf(1)
	case "lower_cost":
		if cost, known := target.Model.UnitCost(); known {
			return cost
		}
		return math.Inf(1)
	case "existing_subscription":
		if source.BillingMode == "subscription" {
			return 0
		}
	case "official_api":
		if source.SourceKind == "vendor_api" || source.SourceKind == "cloud_api" {
			return 0
		}
	case "reverse_source":
		if strings.HasSuffix(source.SourceKind, "_reverse") {
			return 0
		}
	}
	return 1
}
