package config

import (
	"math"
	"testing"
)

func TestPriceAndPreferenceValidation(t *testing.T) {
	for _, value := range []float64{-1, math.Inf(1), math.NaN(), 1e9 + 1} {
		c := Default()
		c.Groups[0].MaxUSDPerMillion = &value
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted price %v", value)
		}
	}
	for _, preferences := range [][]string{{"unknown"}, {"lower_cost", "lower_cost"}} {
		c := Default()
		c.Groups[0].Preferences = preferences
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted preferences %v", preferences)
		}
	}
	for _, strategy := range []string{"weighted", "fallback", "select"} {
		c := Default()
		c.Groups[0].Type = strategy
		c.Groups[0].Preferences = []string{"lower_cost"}
		if err := c.Validate(); err == nil {
			t.Fatalf("accepted preferences for %s", strategy)
		}
	}
	c := Default()
	zero := 0.0
	c.Groups[0].MaxUSDPerMillion = &zero
	c.Groups[0].RequireVision = true
	c.Groups[0].Preferences = []string{"lower_cost", "existing_subscription"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	clone, err := Clone(c)
	if err != nil || clone.Groups[0].MaxUSDPerMillion == nil || *clone.Groups[0].MaxUSDPerMillion != 0 || !clone.Groups[0].RequireVision || len(clone.Groups[0].Preferences) != 2 {
		t.Fatalf("roundtrip: %+v %v", clone.Groups[0], err)
	}
	*clone.Groups[0].MaxUSDPerMillion = 1
	if *c.Groups[0].MaxUSDPerMillion != 0 {
		t.Fatal("cloned price aliases original")
	}
}
