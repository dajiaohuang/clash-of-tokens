package config

import (
	"fmt"
	"strings"
)

// Legacy Local describes the transport, never a guarantee of local inference.
func (s Source) LocalInference() bool {
	return s.InferenceLocation == "local" && s.SourceKind == "local_model" && s.Local
}

func (s Source) EffectiveBilling() string {
	if s.BillingMode != "" {
		return s.BillingMode
	}
	if s.Paid {
		return "metered"
	}
	return "unknown"
}

func (s Source) ValidateMetadata() error {
	if len(s.Organization) > 256 || strings.ContainsAny(s.Organization, "\r\n\x00") {
		return fmt.Errorf("invalid organization")
	}
	for _, field := range []struct {
		name, value string
		allowed     []string
	}{
		{"source_kind", s.SourceKind, []string{"vendor_api", "cloud_api", "aggregator_api", "product_reverse", "browser_reverse", "app_reverse", "cli_reverse", "local_model", "custom_api"}},
		{"execution_location", s.ExecutionLocation, []string{"local", "remote"}},
		{"inference_location", s.InferenceLocation, []string{"local", "remote", "unknown"}},
		{"billing_mode", s.BillingMode, []string{"metered", "subscription", "free_allowance", "local", "unknown"}},
		{"credential_mode", s.CredentialMode, []string{"api_key", "oauth", "browser_session", "cookie", "username_password", "cli_session", "device_session", "anonymous"}},
	} {
		if field.value == "" {
			continue
		}
		found := false
		for _, v := range field.allowed {
			if v == field.value {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("invalid %s", field.name)
		}
	}
	if s.InferenceLocation == "local" && (s.SourceKind != "local_model" || !s.Local) {
		return fmt.Errorf("local inference requires an explicit local_model and loopback transport")
	}
	if s.SourceKind == "local_model" && (s.InferenceLocation != "local" || !s.Local) {
		return fmt.Errorf("local_model requires local inference and loopback transport")
	}
	if s.SourceKind == "local_model" && (s.Adapter != "openai" && s.Adapter != "anthropic" && s.Adapter != "gemini") {
		return fmt.Errorf("local_model requires a generic local inference API")
	}
	return nil
}

func (g Group) BillingAllowed(s Source) bool {
	var override *bool
	switch s.EffectiveBilling() {
	case "metered":
		override = g.AllowMetered
	case "subscription":
		override = g.AllowSubscription
	case "unknown":
		override = g.AllowUnknownCost
	case "free_allowance", "local":
		return true
	}
	if override != nil {
		return *override
	}
	// Preserve legacy cost opt-in for paid sources; unknown costs need explicit approval.
	if s.EffectiveBilling() == "unknown" {
		return false
	}
	return g.AllowPaid
}
