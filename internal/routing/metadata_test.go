package routing

import (
	"clash-of-tokens/internal/config"
	"testing"
)

func TestLocalTransportDoesNotImplyLocalInference(t *testing.T) {
	c := fixture(1)
	c.Groups[0].LocalOnly = true
	c.Sources[0].Local = true
	c.Sources[0].Adapter = "devin-cli"
	if got := New(c).Explain(query())["s0/model"]; got != "local_only" {
		t.Fatal(got)
	}
	c.Sources[0].Adapter = "openai"
	c.Sources[0].SourceKind = "local_model"
	c.Sources[0].InferenceLocation = "local"
	if got := New(c).Explain(query())["s0/model"]; got != "eligible" {
		t.Fatal(got)
	}
}

func TestIndependentBillingPolicies(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		mode  string
		group config.Group
		want  bool
	}{
		{"metered", config.Group{AllowPaid: true, AllowMetered: &no}, false},
		{"subscription", config.Group{AllowSubscription: &yes}, true},
		{"unknown", config.Group{AllowPaid: true}, false},
		{"unknown", config.Group{AllowUnknownCost: &yes}, true},
		{"free_allowance", config.Group{}, true},
	} {
		if got := tc.group.BillingAllowed(config.Source{BillingMode: tc.mode}); got != tc.want {
			t.Fatalf("%s: %v", tc.mode, got)
		}
	}
}
