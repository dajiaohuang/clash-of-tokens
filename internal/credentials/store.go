// Package credentials stores secrets separately from operational configuration.
package credentials

import (
	"context"
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
	External   *ExternalReference `json:"external,omitempty"`
	OAuthInfo  *OAuthMetadata     `json:"oauth,omitempty"`
	State      string             `json:"state,omitempty"`
	Version    uint64             `json:"version"`
	ID         string             `json:"id"`
	Kind       string             `json:"kind"`
	Source     string             `json:"source"`
	Domain     string             `json:"domain,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	LastUsedAt *time.Time         `json:"last_used_at,omitempty"`
}
type record struct {
	Metadata
	Revoked   bool        `json:"revoked,omitempty"`
	Value     string      `json:"value"`
	OAuth     *OAuthGrant `json:"oauth_grant,omitempty"`
	ImportURL string      `json:"import_url,omitempty"`
}
type Store struct {
	accountOwners  map[string][]AccountOwner
	refreshes      map[string]*refreshFlight
	refreshBackoff map[string]time.Time
	mu             sync.RWMutex
	path           string
	records        map[string]record
	lastUsed       map[string]time.Time
	protect        func([]byte) ([]byte, error)
	unprotect      func([]byte) ([]byte, error)
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
		if r.Revoked {
			continue
		}
		r.State = "ready"
		if r.External != nil {
			copy := *r.External
			r.External = &copy
			r.State = "external_authorization_required"
		}
		if r.OAuthInfo != nil {
			copy := *r.OAuthInfo
			r.OAuthInfo = &copy
			if !copy.ExpiresAt.IsZero() && time.Now().After(copy.ExpiresAt) {
				r.State = "expired"
			}
			if s.refreshes[r.ID] != nil {
				r.State = "refreshing"
			}
			if time.Now().Before(s.refreshBackoff[r.ID]) {
				r.State = "needs_reauthorization"
			}
		}
		if used, ok := s.lastUsed[r.ID]; ok {
			r.LastUsedAt = &used
		}
		out = append(out, r.Metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
func (s *Store) Resolve(id string) string {
	value, _ := s.ResolveContext(context.Background(), id)
	return value
}
func (s *Store) ResolveContext(ctx context.Context, id string) (string, error) {
	s.mu.Lock()
	r, ok := s.records[id]
	if !ok || r.Revoked {
		s.mu.Unlock()
		return "", errors.New("credential is missing or revoked")
	}
	s.mu.Unlock()
	if r.External != nil {
		value, err := ReadExternal(ctx, *r.External)
		s.mu.RLock()
		current, exists := s.records[id]
		s.mu.RUnlock()
		if !exists || current.Revoked || current.Version != r.Version {
			return "", errors.New("external credential result is stale")
		}
		return value, err
	}
	if r.OAuth != nil && !r.OAuth.ExpiresAt.IsZero() && time.Now().Add(30*time.Second).After(r.OAuth.ExpiresAt) {
		if err := s.refreshOAuth(ctx, id, false); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok = s.records[id]
	if !ok || r.Revoked {
		return "", errors.New("credential is revoked")
	}
	if s.lastUsed == nil {
		s.lastUsed = make(map[string]time.Time)
	}
	used := time.Now().UTC()
	s.lastUsed[id] = used
	r.Metadata.LastUsedAt = &used
	s.records[id] = r
	return r.Value, nil
}
func (s *Store) Put(id, kind, source, value string) error {
	return s.putRecord(id, kind, source, value, nil, nil)
}
func (s *Store) putRecord(id, kind, source, value string, external *ExternalReference, oauth *OAuthGrant) error {
	if id == "" || !config.ValidCredentialRef(id) || !ValidKind(kind) || (value == "" && external == nil) || len(value) > 1<<20 || len(source) > 256 {
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
		if old.Revoked {
			return errors.New("credential reference is revoked; create a new reference")
		}
		created = old.CreatedAt
		version = old.Version + 1
		if version == 0 {
			return errors.New("credential version exhausted")
		}
	}
	next[id] = record{Metadata: Metadata{Version: version, ID: id, Kind: kind, Source: source, CreatedAt: created, UpdatedAt: now, External: external}, Value: value, OAuth: oauth}
	if prior, exists := s.records[id]; exists && prior.Kind == "username_password" && kind == prior.Kind {
		// Replacing a saved login changes its value, not its website identity.
		// Preserve import provenance used by duplicate detection and account setup.
		updated := next[id]
		updated.Domain, updated.ImportURL = prior.Domain, prior.ImportURL
		next[id] = updated
	}
	if oauth != nil {
		record := next[id]
		record.OAuthInfo = &OAuthMetadata{ExpiresAt: oauth.ExpiresAt, Scope: oauth.Scope, AccountID: oauth.AccountID, AutomaticRefresh: oauth.RefreshToken != ""}
		next[id] = record
	}
	if err := s.save(next); err != nil {
		return err
	}
	delete(s.lastUsed, id)
	delete(s.refreshBackoff, id)
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
	old, ok := s.records[id]
	if !ok || old.Revoked {
		return errors.New("unknown credential")
	}
	next := s.copy()
	// Retain only a non-secret tombstone. Reusing a deleted ID could otherwise
	// revive old configuration references or let an old refresh overwrite it.
	next[id] = record{Metadata: Metadata{ID: id, Version: old.Version, UpdatedAt: time.Now().UTC()}, Revoked: true}
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
	var payload any = next
	if s.accountOwners != nil {
		grouped, err := groupAccountRecords(next, s.accountOwners)
		if err != nil {
			return err
		}
		payload = grouped
	}
	plain, err := json.Marshal(payload)
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
