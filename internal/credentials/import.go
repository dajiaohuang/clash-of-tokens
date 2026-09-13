package credentials

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
)

type ImportEntry struct {
	Email    string `json:"email,omitempty"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ParseImport handles explicitly supplied exports only; it never searches the
// filesystem or opens a browser/password-manager vault.
func ParseImport(format string, data []byte) ([]ImportEntry, error) {
	if len(data) == 0 || len(data) > 4<<20 {
		return nil, errors.New("import must contain 1 byte to 4 MiB")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	var entries []ImportEntry
	switch format {
	case "json":
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if d.Decode(&entries) != nil || d.Decode(new(any)) != io.EOF {
			return nil, errors.New("expected JSON array of name, url, username and password records")
		}
	case "csv":
		r := csv.NewReader(bytes.NewReader(data))
		header, err := r.Read()
		if err != nil {
			return nil, errors.New("invalid CSV header")
		}
		cols := map[string]int{}
		for i, h := range header {
			h = strings.ToLower(strings.TrimSpace(h))
			if _, exists := cols[h]; exists {
				return nil, errors.New("duplicate CSV header")
			}
			cols[h] = i
		}
		for _, required := range []string{"url", "username", "password"} {
			if _, ok := cols[required]; !ok {
				return nil, errors.New("CSV requires url, username and password columns")
			}
		}
		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("invalid CSV row")
			}
			e := ImportEntry{URL: row[cols["url"]], Username: row[cols["username"]], Password: row[cols["password"]]}
			if i, ok := cols["name"]; ok {
				e.Name = row[i]
			}
			entries = append(entries, e)
			if len(entries) > 1000 {
				return nil, errors.New("import exceeds 1000 entries")
			}
		}
	default:
		return nil, errors.New("unsupported import format; use csv or json")
	}
	if len(entries) == 0 || len(entries) > 1000 {
		return nil, errors.New("import requires 1 to 1000 entries")
	}
	for _, e := range entries {
		u, err := url.Parse(e.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || len(e.URL) > 2048 || len(e.Name) > 256 || len(e.Username) > 1024 || len(e.Email) > 1024 || e.Password == "" || len(e.Password) > 64<<10 {
			return nil, errors.New("invalid import entry URL, password or field size")
		}
	}
	return entries, nil
}

type ImportPreview struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Domain string `json:"domain"`
	Kind   string `json:"kind"`
}
type ImportSelection struct {
	Index     int    `json:"index"`
	Action    string `json:"action"`
	Reference string `json:"reference,omitempty"`
	Version   uint64 `json:"version,omitempty"`
}

func (s *Store) ImportConflicts(entries []ImportEntry) map[int][]Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := map[int][]Metadata{}
	for i, entry := range entries {
		for _, record := range s.records {
			if record.Kind != "username_password" || record.ImportURL != entry.URL {
				continue
			}
			var saved struct {
				Username string `json:"username"`
			}
			if json.Unmarshal([]byte(record.Value), &saved) == nil && saved.Username == entry.Username {
				out[i] = append(out[i], record.Metadata)
			}
		}
	}
	return out
}

func PreviewImport(entries []ImportEntry) []ImportPreview {
	out := make([]ImportPreview, 0, len(entries))
	for i, e := range entries {
		u, _ := url.Parse(e.URL)
		out = append(out, ImportPreview{Index: i, Name: e.Name, Domain: strings.ToLower(u.Hostname()), Kind: "username_password"})
	}
	return out
}

// ImportSelected commits every selected entry in one encrypted transaction.
// Random references ensure importing never replaces a bound credential.
func (s *Store) ImportSelected(entries []ImportEntry, selected []int) ([]Metadata, error) {
	choices := make([]ImportSelection, len(selected))
	for i, index := range selected {
		choices[i] = ImportSelection{Index: index, Action: "new"}
	}
	return s.ImportWithConflicts(entries, choices)
}
func (s *Store) ImportWithConflicts(entries []ImportEntry, selected []ImportSelection) ([]Metadata, error) {
	if len(selected) == 0 || len(selected) > 1000 {
		return nil, errors.New("select 1 to 1000 entries")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	seen := map[int]bool{}
	out := make([]Metadata, 0, len(selected))
	now := time.Now().UTC()
	touched := map[string]bool{}
	for _, selection := range selected {
		i := selection.Index
		if i < 0 || i >= len(entries) || seen[i] {
			return nil, errors.New("invalid or duplicate selection")
		}
		seen[i] = true
		if selection.Action != "new" && selection.Action != "keep" && selection.Action != "replace" {
			return nil, errors.New("choose new, keep or replace")
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, errors.New("cannot allocate credential reference")
		}
		id := "cred://import-" + hex.EncodeToString(random[:])
		e := entries[i]
		version := uint64(1)
		created := now
		if selection.Action != "new" {
			prior, ok := next[selection.Reference]
			var saved struct {
				Username string `json:"username"`
			}
			if !ok || prior.Version != selection.Version || prior.Kind != "username_password" || prior.ImportURL != e.URL || json.Unmarshal([]byte(prior.Value), &saved) != nil || saved.Username != e.Username || touched[selection.Reference] {
				return nil, errors.New("import conflict changed or does not match this exact login")
			}
			touched[selection.Reference] = true
			if selection.Action == "keep" {
				out = append(out, prior.Metadata)
				continue
			}
			id = selection.Reference
			version = prior.Version + 1
			created = prior.CreatedAt
			if version == 0 {
				return nil, errors.New("credential version exhausted")
			}
		}
		u, _ := url.Parse(e.URL)
		value, _ := json.Marshal(struct {
			Email    string `json:"email,omitempty"`
			Username string `json:"username"`
			Password string `json:"password"`
		}{e.Email, e.Username, e.Password})
		m := Metadata{Version: version, ID: id, Kind: "username_password", Source: "selected-export", Domain: strings.ToLower(u.Hostname()), CreatedAt: created, UpdatedAt: now}
		next[id] = record{Metadata: m, Value: string(value), ImportURL: e.URL}
		clear(value)
		out = append(out, m)
	}
	if err := s.save(next); err != nil {
		return nil, err
	}
	return out, nil
}
