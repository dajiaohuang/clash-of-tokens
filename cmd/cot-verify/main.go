// cot-verify is an offline evidence and contract verifier. It has no credentials
// and never starts a browser, device conversation, or downloaded adapter code.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"clash-of-tokens/catalog"
)

type evidence struct {
	SchemaVersion int    `json:"schema_version"`
	Scope         string `json:"scope"`
	Reference     string `json:"reference"`
	Commit        string `json:"reference_commit"`
	License       string `json:"license"`
	Driver        string `json:"driver"`
	Adapter       string `json:"adapter"`
	CheckedAt     string `json:"checked_at"`
	Sources       []struct {
		ID            string   `json:"id"`
		Product       string   `json:"product"`
		Entry         string   `json:"entry"`
		ClientVersion string   `json:"client_version"`
		Catalog       string   `json:"catalog"`
		Connection    string   `json:"connection"`
		Interface     string   `json:"interface"`
		Agent         string   `json:"agent"`
		Runtime       string   `json:"runtime"`
		Tests         []string `json:"tests"`
		Missing       string   `json:"missing"`
	} `json:"sources"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	scope := flag.String("scope", "freecoding", "fixed evidence/contract scope")
	tests := flag.Bool("run-tests", false, "execute this scope's offline Go tests")
	flag.Parse()
	if *scope != "freecoding" {
		return fmt.Errorf("unknown verification scope")
	}
	raw, e := os.ReadFile("catalog/evidence/freecoding.json")
	if e != nil {
		return e
	}
	if len(raw) > 1<<20 {
		return fmt.Errorf("evidence exceeds size limit")
	}
	var doc evidence
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e = d.Decode(&doc); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing evidence data")
	}
	if doc.SchemaVersion != 1 || doc.Scope != *scope || doc.Reference != "https://github.com/Damue01/FreeCoding" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(doc.Commit) || len(doc.Sources) != 3 {
		return fmt.Errorf("invalid source provenance")
	}
	for _, p := range []string{doc.License, doc.Driver, doc.Adapter} {
		if !filepath.IsLocal(p) {
			return fmt.Errorf("evidence paths must be local")
		}
		if _, e = os.Stat(p); e != nil {
			return e
		}
	}
	entries := map[string]catalog.Entry{}
	for _, p := range catalog.All() {
		entries[p.ID] = p
	}
	seen := map[string]bool{}
	live := 0
	for _, s := range doc.Sources {
		p, ok := entries[s.ID]
		if !ok || seen[s.ID] || p.Adapter != "app-device" || s.Product == "" || s.Entry == "" || s.ClientVersion == "" || len(s.Tests) == 0 || s.Catalog != "complete" || s.Interface != "offline_passed" || s.Agent != "single_user_text_current_app_session" {
			return fmt.Errorf("invalid evidence for %s", s.ID)
		}
		seen[s.ID] = true
		if s.Connection == "live_passed" {
			return fmt.Errorf("live evidence ingestion is not implemented; attach and review a physical probe before claiming live_passed")
		}
		if s.Connection != "unverified" || p.LiveVerified || s.Runtime != "blocked_device_missing" || s.Missing == "" {
			return fmt.Errorf("unverified source promoted without evidence: %s", s.ID)
		}
	}
	if *tests {
		cmd := exec.Command("go", "test", "./internal/drivers/device", "./internal/providers/appdevice", "./catalog")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if e = cmd.Run(); e != nil {
			return e
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"scope": doc.Scope, "catalog_complete": len(seen), "offline_contracts_executed": *tests, "live_verified": live, "runtime_ready": 0, "reference_commit": doc.Commit})
}
