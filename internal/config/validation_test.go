package config

import (
	"strings"
	"testing"
)

func TestSourceRequiresAtLeastOneModel(t *testing.T) {
	c := Default()
	c.Sources = []Source{{
		ID:               "source",
		Provider:         "test",
		Adapter:          "openai",
		BaseURL:          "http://127.0.0.1:1",
		Local:            true,
		MaxInflight:      1,
		QuotaDomain:      "source",
		QuotaMaxInflight: 1,
	}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "source source: at least one model is required") {
		t.Fatalf("Validate() error = %v, want missing-model error", err)
	}

	c.Sources[0].Models = []Model{{
		ID:            "model",
		Upstream:      "model",
		Protocols:     []string{"chat"},
		Tier:          "unrated",
		Tools:         "none",
		MaxInputBytes: 1024,
	}}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() rejected source with a model: %v", err)
	}
}

func TestLocalModelRequiresExplicitLocalInference(t *testing.T) {
	source := Source{Adapter: "openai", SourceKind: "local_model", Local: true, InferenceLocation: "remote"}
	if err := source.ValidateMetadata(); err == nil || !strings.Contains(err.Error(), "local_model requires local inference") {
		t.Fatalf("remote local_model was accepted: %v", err)
	}
	source.InferenceLocation = "local"
	source.Local = false
	if err := source.ValidateMetadata(); err == nil || !strings.Contains(err.Error(), "local inference") {
		t.Fatalf("non-loopback local_model was accepted: %v", err)
	}
	source.Local = true
	source.BillingMode = "local"
	if err := source.ValidateMetadata(); err != nil {
		t.Fatalf("explicit local_model was rejected: %v", err)
	}
	source.BillingMode = "metered"
	if err := source.ValidateMetadata(); err == nil || !strings.Contains(err.Error(), "local billing mode") {
		t.Fatalf("metered local_model was accepted: %v", err)
	}
	source.BillingMode = "local"
	source.SourceKind = "vendor_api"
	source.InferenceLocation = "remote"
	if err := source.ValidateMetadata(); err == nil || !strings.Contains(err.Error(), "local billing mode") {
		t.Fatalf("local billing on a remote source was accepted: %v", err)
	}
}

func TestSourceMetadataRejectsUnsafeOrganizationAndProject(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*Source)
	}{
		{"organization newline", func(s *Source) { s.Organization = "org\nheader" }},
		{"project newline", func(s *Source) { s.Project = "project\rvalue" }},
		{"organization nul", func(s *Source) { s.Organization = "org\x00value" }},
		{"project too long", func(s *Source) { s.Project = strings.Repeat("p", 257) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := Source{}
			test.set(&source)
			if err := source.ValidateMetadata(); err == nil {
				t.Fatal("unsafe metadata accepted")
			}
		})
	}
}
