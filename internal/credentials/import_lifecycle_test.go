package credentials

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestNamedBrowserExportSchemas(t *testing.T) {
	for _, format := range []string{"chrome-csv", "edge-csv", "brave-csv", "firefox-csv", "opera-csv", "vivaldi-csv", "chromium-csv", "arc-csv", "safari-csv", "keepass-csv", "lastpass-csv", "enpass-csv"} {
		t.Run(format, func(t *testing.T) {
			entries, _, err := ParseExport(format, []byte("Title,URL,Username,Password,Notes\nFixture,https://example.org/login,user,\"secret,with,commas\",do-not-import\n"))
			if err != nil || len(entries) != 1 || entries[0].Password != "secret,with,commas" {
				t.Fatal("format mismatch", err)
			}
			if strings.Contains(fmt.Sprint(PreviewImport(entries)), "secret") {
				t.Fatal("secret in preview")
			}
		})
	}
}
func TestSelectiveImportConflictAndPreviewCancellation(t *testing.T) {
	entries := make([]ImportEntry, 100)
	for i := range entries {
		entries[i] = ImportEntry{Name: fmt.Sprint(i), URL: fmt.Sprintf("https://p%d.example/login", i), Username: "same@example.org", Password: "synthetic-password"}
	}
	store, err := Open(filepath.Join(t.TempDir(), "vault"))
	if err != nil {
		t.Fatal(err)
	}
	previews := &PreviewStore{}
	defer previews.Close()
	canceled, err := previews.Add(entries)
	if err != nil {
		t.Fatal(err)
	}
	previews.Drop(canceled)
	if _, err = previews.Take(canceled); err == nil || len(store.List()) != 0 {
		t.Fatal("canceled preview persisted")
	}
	ticket, err := previews.Add(entries)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := previews.Take(ticket)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := store.ImportSelected(selected, []int{2, 97})
	if err != nil || len(imported) != 2 || len(store.List()) != 2 {
		t.Fatal("unselected records persisted", err)
	}
	conflicts := store.ImportConflicts(entries)
	if len(conflicts) != 2 || len(conflicts[0]) != 0 {
		t.Fatal("different products merged by email")
	}
	first := conflicts[2][0]
	if _, err = store.ImportWithConflicts(entries, []ImportSelection{{Index: 2, Action: "keep", Reference: first.ID, Version: first.Version}}); err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 2 {
		t.Fatal("keep created duplicate")
	}
	entries[2].Password = "replacement"
	if _, err = store.ImportWithConflicts(entries, []ImportSelection{{Index: 2, Action: "replace", Reference: first.ID, Version: first.Version}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(store.Resolve(first.ID), "replacement") {
		t.Fatal("replacement not used")
	}
	if _, err = store.ImportWithConflicts(entries, []ImportSelection{{Index: 2, Action: "replace", Reference: first.ID, Version: first.Version}}); err == nil {
		t.Fatal("stale replacement accepted")
	}
	if _, err = store.ImportWithConflicts(entries, []ImportSelection{{Index: 3, Action: "replace", Reference: first.ID, Version: first.Version + 1}}); err == nil {
		t.Fatal("cross-product replacement accepted")
	}
}

func TestExternalReferenceSelectionBoundaries(t *testing.T) {
	for _, ref := range []ExternalReference{
		{Manager: "1password", Reference: "op://vault/item/password"},
		{Manager: "1password", Reference: "op://vault/item/section/field"},
		{Manager: "bitwarden", Reference: "12345678-1234-1234-1234-123456789abc", Field: "password"},
	} {
		if err := ref.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []ExternalReference{
		{Manager: "1password", Reference: "op://vault"},
		{Manager: "1password", Reference: "op://vault/item/password?query=secret"},
		{Manager: "bitwarden", Reference: "ambiguous display name", Field: "password"},
		{Manager: "bitwarden", Reference: "12345678-1234-1234-1234-123456789abc", Field: "item"},
	} {
		if ref.Validate() == nil {
			t.Fatal("unbounded or ambiguous manager request accepted")
		}
	}
}
