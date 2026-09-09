package chatgptweb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"clash-of-tokens/internal/config"
)

type Session struct {
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
	if e = json.Unmarshal(b, &s.items); e != nil {
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
		if time.Since(v.Updated) > time.Duration(s.cfg.SessionTTLSeconds)*time.Second {
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
