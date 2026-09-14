package credentials

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"clash-of-tokens/internal/config"
)

type AccountOwner struct {
	Account  string
	Provider string
}
type accountBundle struct {
	Provider  string            `json:"provider"`
	Materials map[string]record `json:"materials"`
}
type accountDocument struct {
	Format   int                      `json:"format"`
	Accounts map[string]accountBundle `json:"accounts"`
}

func groupAccountRecords(records map[string]record, owners map[string][]AccountOwner) (accountDocument, error) {
	doc := accountDocument{Format: 1, Accounts: map[string]accountBundle{}}
	for ref, r := range records {
		if r.Revoked {
			continue
		}
		if len(owners[ref]) == 0 {
			return doc, errors.New("material must belong to a provider account")
		}
		for _, owner := range owners[ref] {
			if owner.Account == "" || owner.Provider == "" {
				return doc, errors.New("invalid account material owner")
			}
			bundle := doc.Accounts[owner.Account]
			if bundle.Provider != "" && bundle.Provider != owner.Provider {
				return doc, errors.New("account material provider conflict")
			}
			if bundle.Materials == nil {
				bundle = accountBundle{Provider: owner.Provider, Materials: map[string]record{}}
			}
			bundle.Materials[ref] = r
			doc.Accounts[owner.Account] = bundle
		}
	}
	return doc, nil
}

// OpenProviderAccounts migrates only account-bound material to an encrypted,
// account-keyed document. Unmatched legacy imports are not active account data.
// A recovery copy of the old encrypted file is retained, never read at runtime.
func OpenProviderAccounts(dir string, c config.Config) (*Store, string, error) {
	path := filepath.Join(dir, "provider-accounts.enc")
	seal, unseal := platformProtection()
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > 16<<20 {
			return nil, "", errors.New("account material storage exceeds limit")
		}
		plain, err := unseal(b)
		if err != nil {
			return nil, "", errors.New("cannot unlock provider account data")
		}
		defer clear(plain)
		var doc accountDocument
		if json.Unmarshal(plain, &doc) != nil || doc.Format != 1 || doc.Accounts == nil {
			return nil, "", errors.New("invalid provider account data")
		}
		s := &Store{path: path, records: map[string]record{}, lastUsed: map[string]time.Time{}, protect: seal, unprotect: unseal, accountOwners: map[string][]AccountOwner{}}
		for id, bundle := range doc.Accounts {
			for ref, r := range bundle.Materials {
				if old, ok := s.records[ref]; ok && !reflect.DeepEqual(old, r) {
					return nil, "", errors.New("conflicting shared account material")
				}
				s.records[ref] = r
				s.accountOwners[ref] = append(s.accountOwners[ref], AccountOwner{Account: id, Provider: bundle.Provider})
			}
		}
		return s, "", nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	legacy := filepath.Join(dir, "credentials.vault")
	s, err := Open(legacy)
	if err != nil {
		return nil, "", err
	}
	owners := map[string][]AccountOwner{}
	for _, a := range c.Accounts {
		for _, ref := range []string{a.LoginCredentialRef, a.CredentialRef} {
			if ref != "" {
				owners[ref] = append(owners[ref], AccountOwner{Account: a.ID, Provider: a.ProviderID})
			}
		}
	}
	for _, source := range c.Sources {
		if source.CredentialRef != "" {
			if source.AccountID == "" {
				return nil, "", errors.New("bind source credentials to a provider account before migrating")
			}
			owners[source.CredentialRef] = append(owners[source.CredentialRef], AccountOwner{Account: source.AccountID, Provider: source.Provider})
		}
	}
	kept := map[string]record{}
	for ref := range owners {
		r, ok := s.records[ref]
		if !ok || r.Revoked {
			return nil, "", errors.New("account references missing legacy material")
		}
		kept[ref] = r
	}
	s.path = path
	s.accountOwners = owners
	if err = s.save(kept); err != nil {
		return nil, "", err
	}
	// Verify the new encrypted document before retiring the old active file.
	verified, _, err := OpenProviderAccounts(dir, c)
	if err != nil {
		return nil, "", err
	}
	if !reflect.DeepEqual(verified.records, kept) {
		return nil, "", errors.New("account migration verification failed")
	}
	backup := ""
	if _, err = os.Stat(legacy); err == nil {
		backup = legacy + ".legacy-backup-" + time.Now().UTC().Format("20060102T150405.000000000")
		if err = os.Rename(legacy, backup); err != nil {
			return nil, "", errors.New("account data migrated but legacy archive failed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	return verified, backup, nil
}

func (s *Store) AccountOwned() bool { return s.accountOwners != nil }

// PutAccount material ownership is enforced by encrypted persistence. No
// caller-supplied URL, password or username is placed in the config journal.
func (s *Store) PutAccount(owner AccountOwner, ref, kind, value string) error {
	s.mu.Lock()
	if s.accountOwners != nil {
		prior := s.accountOwners[ref]
		if len(prior) > 0 && !reflect.DeepEqual(prior, []AccountOwner{owner}) {
			s.mu.Unlock()
			return errors.New("account material belongs to another owner")
		}
		s.accountOwners[ref] = []AccountOwner{owner}
	}
	s.mu.Unlock()
	return s.Put(ref, kind, "provider-account", value)
}

func (s *Store) AdoptAccount(owner AccountOwner, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accountOwners == nil {
		return nil
	}
	if len(s.accountOwners[ref]) > 0 {
		return errors.New("account material already has an owner")
	}
	s.accountOwners[ref] = []AccountOwner{owner}
	return nil
}

func (s *Store) SetAccountDomain(ref, domain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[ref]
	if !ok {
		return errors.New("unknown account material")
	}
	next := s.copy()
	r.Domain = domain
	next[ref] = r
	return s.save(next)
}

func (s *Store) LoginOrigin(ref string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, err := url.Parse(s.records[ref].ImportURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (s *Store) ValidateAccountOwners(c config.Config) error {
	if !s.AccountOwned() {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	check := func(ref, id, provider string) bool {
		if ref == "" {
			return true
		}
		for _, owner := range s.accountOwners[ref] {
			if owner.Account == id && owner.Provider == provider {
				return true
			}
		}
		return false
	}
	for _, a := range c.Accounts {
		if !check(a.LoginCredentialRef, a.ID, a.ProviderID) || !check(a.CredentialRef, a.ID, a.ProviderID) {
			return errors.New("material ownership does not match provider account")
		}
	}
	for _, source := range c.Sources {
		if !check(source.CredentialRef, source.AccountID, source.Provider) {
			return errors.New("source material must belong to its provider account")
		}
	}
	return nil
}

func (s *Store) CreateAccountMaterial(owner AccountOwner, kind, value string) (Metadata, error) {
	ref := "cred://account-" + rand.Text()
	if err := s.PutAccount(owner, ref, kind, value); err != nil {
		return Metadata{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records[ref].Metadata, nil
}
