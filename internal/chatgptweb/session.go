package chatgptweb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/session"
)

type Session struct {
	Expires      time.Time `json:"expires_at,omitempty"`
	Created      time.Time `json:"created,omitempty"`
	ID           string    `json:"id"`
	Source       string    `json:"source"`
	Account      string    `json:"account"`
	Conversation string    `json:"conversation"`
	LastNode     string    `json:"last_node"`
	ResponseID   string    `json:"response_id"`
	Model        string    `json:"model"`
	Protocol     string    `json:"protocol"`
	HistoryHash  string    `json:"history_hash"`
	HistoryCount int       `json:"history_count"`
	Dirty        bool      `json:"dirty"`
	Updated      time.Time `json:"updated"`
}
type sessionStore struct {
	mu      sync.Mutex
	cfg     config.Browser
	items   map[string]Session
	loadErr error
}

func newStore(c config.Browser) *sessionStore {
	s := &sessionStore{cfg: c, items: map[string]Session{}}
	b, e := os.ReadFile(c.StateFile)
	if errors.Is(e, os.ErrNotExist) {
		return s
	}
	if e != nil {
		s.loadErr = errors.New("cannot read browser session state")
		return s
	}
	if len(b) > 4<<20 {
		s.loadErr = errors.New("browser session state too large")
		return s
	}
	if e = json.Unmarshal(b, &s.items); e != nil || s.items == nil {
		s.loadErr = errors.New("invalid browser session state")
	}
	return s
}
func id(prefix string) string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return prefix + hex.EncodeToString(b[:])
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *sessionStore) get(key, previous string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return Session{}, false, s.loadErr
	}
	for k, v := range s.items {
		if sessionExpired(v, s.cfg.SessionTTLSeconds) {
			delete(s.items, k)
		}
	}
	if previous != "" {
		for _, v := range s.items {
			if v.ResponseID == previous {
				if key != "" && key != v.ID {
					return Session{}, false, errors.New("session and previous_response_id disagree")
				}
				return v, true, nil
			}
		}
		return Session{}, false, errors.New("previous_response_id is unknown, expired or no longer the latest turn")
	}
	v, ok := s.items[key]
	return v, ok, nil
}
func (s *sessionStore) put(v Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	if _, ok := s.items[v.ID]; !ok && len(s.items) >= s.cfg.MaxSessions {
		return errors.New("browser session capacity exhausted; wait for TTL expiry")
	}
	v.Updated = time.Now()
	previous, existed := s.items[v.ID]
	if existed {
		v.Created = previous.Created
	} else if v.Created.IsZero() {
		v.Created = v.Updated
	}
	s.items[v.ID] = v
	if e := s.save(); e != nil {
		if existed {
			s.items[v.ID] = previous
		} else {
			delete(s.items, v.ID)
		}
		return e
	}
	return nil
}

// SessionMetadata remains an alias for callers of the ChatGPT Web package;
// the control plane uses the same redacted shape for every stateful adapter.
type SessionMetadata = session.Metadata

// ReadSessions reads an atomic disk snapshot without constructing a cached
// driver that could outlive a concurrently completing runtime generation.
func ReadSessions(c config.Browser, source string) ([]SessionMetadata, error) {
	return (&Driver{cfg: c, source: source, store: newStore(c)}).Sessions()
}

func (d *Driver) Sessions() ([]SessionMetadata, error) {
	s := d.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	out := []SessionMetadata{}
	for _, v := range s.items {
		if v.Source == d.source {
			out = append(out, SessionMetadata{ID: v.ID, Source: v.Source, Conversation: v.Conversation, Model: v.Model, Protocol: v.Protocol, Created: v.Created, Updated: v.Updated, Expired: sessionExpired(v, s.cfg.SessionTTLSeconds), Dirty: v.Dirty})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// ChangeSession must run while the caller holds the source's execution lease,
// so a finishing generation cannot restore a cleared/expired reference.
func (d *Driver) ChangeSession(key, action string) error {
	s := d.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return s.loadErr
	}
	v, ok := s.items[key]
	if !ok || v.Source != d.source {
		return errors.New("unknown source session")
	}
	previous := v
	switch action {
	case "clear":
		delete(s.items, key)
	case "expire":
		v.Expires = time.Now()
		s.items[key] = v
	default:
		return errors.New("unsupported session action")
	}
	if err := s.save(); err != nil {
		s.items[key] = previous
		return errors.New("cannot persist session action")
	}
	return nil
}

func sessionExpired(v Session, ttl int) bool {
	return (!v.Expires.IsZero() && !time.Now().Before(v.Expires)) || time.Since(v.Updated) > time.Duration(ttl)*time.Second
}
func (s *sessionStore) save() error {
	dir := filepath.Dir(s.cfg.StateFile)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return errors.New("cannot create session state directory")
	}
	b, e := json.Marshal(s.items)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, "sessions-*.tmp")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	_ = f.Chmod(0600)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(name, s.cfg.StateFile)
}
