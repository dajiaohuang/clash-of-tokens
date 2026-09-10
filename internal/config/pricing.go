package config

import "math"

// ValidPrice bounds user-declared rates; nil means unknown, never free.
func ValidPrice(price *float64) bool {
	return price == nil || (!math.IsNaN(*price) && !math.IsInf(*price, 0) && *price >= 0 && *price <= 1e9)
}

// UnitCost is the larger declared input/output USD rate per million tokens.
// It is a rate comparison, not a token estimate or per-request budget.
func (m Model) UnitCost() (float64, bool) {
	if m.InputUSDPerMillion == nil || m.OutputUSDPerMillion == nil || !ValidPrice(m.InputUSDPerMillion) || !ValidPrice(m.OutputUSDPerMillion) {
		return 0, false
	}
	return math.Max(*m.InputUSDPerMillion, *m.OutputUSDPerMillion), true
}
