// Package evidence persists bounded operational facts, never request content.
package evidence

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Binding          string    `json:"binding,omitempty"`
	Sequence         uint64    `json:"sequence"`
	Revision         uint64    `json:"revision"`
	Kind             string    `json:"kind"`
	Resource         string    `json:"resource"`
	Model            string    `json:"model,omitempty"`
	Protocol         string    `json:"protocol,omitempty"`
	CheckedAt        time.Time `json:"checked_at"`
	Method           string    `json:"method"`
	Status           string    `json:"status"`
	UpstreamStatus   int       `json:"upstream_status,omitempty"`
	ProtocolComplete bool      `json:"protocol_complete"`
	OutputObserved   bool      `json:"output_observed"`
	Count            int       `json:"count,omitempty"`
}
type Store struct {
	mu      sync.RWMutex
	path    string
	entries []Entry
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: []Entry{}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, errors.New("cannot open evidence history")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 2<<20 {
		return nil, errors.New("evidence history exceeds size limit")
	}
	d := json.NewDecoder(io.LimitReader(f, (2<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(&s.entries) != nil || d.Decode(new(any)) != io.EOF || len(s.entries) > 1000 {
		return nil, errors.New("invalid evidence history")
	}
	return s, nil
}
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry{}, s.entries...)
}
func (s *Store) Append(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(entry.Binding) > 64 || len(entry.Resource) > 512 || len(entry.Model) > 512 || len(entry.Method) > 128 || len(entry.Status) > 128 || len(entry.Kind) > 64 {
		return errors.New("invalid evidence field size")
	}
	entry.Sequence = 1
	if n := len(s.entries); n > 0 {
		entry.Sequence = s.entries[n-1].Sequence + 1
	}
	if entry.CheckedAt.IsZero() {
		entry.CheckedAt = time.Now().UTC()
	}
	next := append(append([]Entry{}, s.entries...), entry)
	if len(next) > 1000 {
		next = next[len(next)-1000:]
	}
	data, err := json.Marshal(next)
	if err != nil || len(data) > 2<<20 {
		return errors.New("evidence history capacity exceeded")
	}
	if os.MkdirAll(filepath.Dir(s.path), 0700) != nil {
		return errors.New("cannot create evidence directory")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".evidence-*")
	if err != nil {
		return errors.New("cannot create evidence transaction")
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write evidence history")
	}
	if os.Rename(f.Name(), s.path) != nil {
		return errors.New("cannot commit evidence history")
	}
	s.entries = next
	return nil
}
