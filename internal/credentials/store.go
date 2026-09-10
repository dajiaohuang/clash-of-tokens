// Package credentials stores secrets separately from operational configuration.
package credentials

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
)

type Metadata struct {
	Version    uint64     `json:"version"`
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Source     string     `json:"source"`
	Domain     string     `json:"domain,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
type record struct {
	Metadata
	Value string `json:"value"`
}
type Store struct {
	mu        sync.RWMutex
	path      string
	records   map[string]record
	lastUsed  map[string]time.Time
	protect   func([]byte) ([]byte, error)
	unprotect func([]byte) ([]byte, error)
}

func Open(path string) (*Store, error) {
	seal, unseal := platformProtection()
	return open(path, seal, unseal)
}
func open(path string, seal, unseal func([]byte) ([]byte, error)) (*Store, error) {
	s := &Store{path: path, records: map[string]record{}, lastUsed: map[string]time.Time{}, protect: seal, unprotect: unseal}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, errors.New("cannot read credential vault")
	}
	if len(b) > 16<<20 {
		return nil, errors.New("credential vault exceeds size limit")
	}
	plain, err := unseal(b)
	if err != nil {
		return nil, errors.New("cannot unlock credential vault")
	}
	defer clear(plain)
	if json.Unmarshal(plain, &s.records) != nil || s.records == nil {
		return nil, errors.New("invalid credential vault")
	}
	return s, nil
}
func ValidKind(kind string) bool {
	switch kind {
	case "api_key", "oauth", "browser_session", "cookie", "username_password", "cli_session", "device_session", "browser_profile":
		return true
	}
	return false
}
func (s *Store) List() []Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Metadata, 0, len(s.records))
	for _, r := range s.records {
		if used, ok := s.lastUsed[r.ID]; ok {
			r.LastUsedAt = &used
		}
		out = append(out, r.Metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) Resolve(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok {
		return ""
	}
	if s.lastUsed == nil {
		s.lastUsed = make(map[string]time.Time)
	}
	used := time.Now().UTC()
	s.lastUsed[id] = used
	r.Metadata.LastUsedAt = &used
	s.records[id] = r
	return r.Value
}
func (s *Store) Put(id, kind, source, value string) error {
	if id == "" || !config.ValidCredentialRef(id) || !ValidKind(kind) || value == "" || len(value) > 1<<20 || len(source) > 256 {
		return errors.New("invalid credential reference, type or size")
	}
	if err := validateValue(kind, value); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copy()
	now := time.Now().UTC()
	created := now
	version := uint64(1)
	if old, ok := next[id]; ok {
		created = old.CreatedAt
		version = old.Version + 1
		if version == 0 {
			return errors.New("credential version exhausted")
		}
	}
	next[id] = record{Metadata: Metadata{Version: version, ID: id, Kind: kind, Source: source, CreatedAt: created, UpdatedAt: now}, Value: value}
	if err := s.save(next); err != nil {
		return err
	}
	delete(s.lastUsed, id)
	return nil
}

func validateValue(kind, value string) error {
	if kind != "username_password" {
		return nil
	}
	var material struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&material) != nil || decoder.Decode(new(any)) != io.EOF || material.Username == "" || material.Password == "" || len(material.Username) > 1024 || len(material.Password) > 64<<10 {
		return errors.New("username_password credential must be JSON with non-empty username and password")
	}
	return nil
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return errors.New("unknown credential")
	}
	next := s.copy()
	delete(next, id)
	if err := s.save(next); err != nil {
		return err
	}
	delete(s.lastUsed, id)
	return nil
}
func (s *Store) copy() map[string]record {
	next := make(map[string]record, len(s.records))
	for k, v := range s.records {
		next[k] = v
	}
	return next
}
func (s *Store) save(next map[string]record) error {
	plain, err := json.Marshal(next)
	if err != nil {
		return errors.New("cannot encode credential vault")
	}
	defer clear(plain)
	if len(plain) > 8<<20 {
		return errors.New("credential vault capacity exceeded")
	}
	b, err := s.protect(plain)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return errors.New("cannot create credential directory")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".vault-*")
	if err != nil {
		return errors.New("cannot create vault transaction")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write credential vault")
	}
	if err = os.Rename(name, s.path); err != nil {
		return errors.New("cannot commit credential vault")
	}
	s.records = next
	return nil
}
