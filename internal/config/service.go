package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrRevisionConflict = errors.New("configuration changed; refresh the preview before applying")

type Version struct {
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	Summary   string    `json:"summary"`
	Config    Config    `json:"config"`
}

type journal struct {
	Format   int       `json:"format"`
	Versions []Version `json:"versions"`
}

// Prepare constructs the next runtime before any durable change. Commit must
// not fail; discard releases the prepared runtime if persistence fails.
type Prepare func(Config) (commit func(), discard func(), err error)

type Service struct {
	mu       sync.Mutex
	path     string
	versions []Version
	prepare  Prepare
}

// OpenService uses one atomically replaced journal as the authoritative state.
// The original JSON remains the import seed and is never partly overwritten.
func OpenService(path string, seed Config, prepare Prepare) (*Service, error) {
	if err := seed.Validate(); err != nil {
		return nil, err
	}
	s := &Service{path: path + ".state", prepare: prepare}
	versions, err := readJournal(s.path)
	if errors.Is(err, os.ErrNotExist) {
		seed, err = Clone(seed)
		if err != nil {
			return nil, err
		}
		s.versions = []Version{{Revision: 1, CreatedAt: time.Now().UTC(), Summary: "Initial configuration", Config: seed}}
	} else if err != nil {
		return nil, err
	} else {
		s.versions = versions
	}
	return s, nil
}

func Clone(c Config) (Config, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return Config{}, err
	}
	var out Config
	if err = json.Unmarshal(b, &out); err != nil {
		return Config{}, err
	}
	return out, nil
}

func readJournal(path string) ([]Version, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 32<<20 {
		return nil, errors.New("configuration history exceeds size limit")
	}
	d := json.NewDecoder(io.LimitReader(f, 32<<20))
	d.DisallowUnknownFields()
	var j journal
	if d.Decode(&j) != nil || d.Decode(new(any)) != io.EOF || j.Format != 1 || len(j.Versions) == 0 || len(j.Versions) > 50 {
		return nil, errors.New("invalid configuration history")
	}
	var previous uint64
	for _, v := range j.Versions {
		if v.Revision <= previous {
			return nil, errors.New("invalid configuration revision order")
		}
		previous = v.Revision
		if err := v.Config.Validate(); err != nil {
			return nil, fmt.Errorf("invalid configuration history revision %d: %w", v.Revision, err)
		}
	}
	return j.Versions, nil
}

func (s *Service) Current() Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneVersion(s.versions[len(s.versions)-1])
}
func cloneVersion(v Version) Version {
	v.Config, _ = Clone(v.Config)
	return v
}
func (s *Service) History() []Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Version, len(s.versions))
	for i, v := range s.versions {
		out[i] = cloneVersion(v)
	}
	return out
}

func (s *Service) Preview(expected uint64, next Config) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preview(expected, next)
}
func (s *Service) preview(expected uint64, next Config) (Version, error) {
	current := s.versions[len(s.versions)-1]
	if expected != current.Revision {
		return Version{}, ErrRevisionConflict
	}
	if err := next.Validate(); err != nil {
		return Version{}, err
	}
	b, err := json.Marshal(next)
	if err != nil || len(b) > 4<<20 {
		return Version{}, errors.New("configuration exceeds size limit")
	}
	next, err = Clone(next)
	if err != nil {
		return Version{}, err
	}
	return Version{Revision: current.Revision + 1, CreatedAt: time.Now().UTC(), Config: next}, nil
}

func (s *Service) Apply(expected uint64, next Config, summary string) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(expected, next, summary)
}
func (s *Service) Rollback(expected, target uint64) (Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.versions {
		if v.Revision == target {
			return s.apply(expected, v.Config, fmt.Sprintf("Rollback to revision %d", target))
		}
	}
	return Version{}, errors.New("configuration revision is not retained")
}
func (s *Service) apply(expected uint64, next Config, summary string) (Version, error) {
	v, err := s.preview(expected, next)
	if err != nil {
		return Version{}, err
	}
	if len(summary) > 512 {
		return Version{}, errors.New("configuration summary is too long")
	}
	v.Summary = summary
	commit, discard := func() {}, func() {}
	if s.prepare != nil {
		commit, discard, err = s.prepare(v.Config)
		if err != nil {
			return Version{}, err
		}
		if commit == nil || discard == nil {
			return Version{}, errors.New("invalid runtime preparation")
		}
	}
	persisted := false
	defer func() {
		if !persisted {
			discard()
		}
	}()
	versions := append(append([]Version(nil), s.versions...), v)
	if len(versions) > 50 {
		versions = versions[len(versions)-50:]
	}
	var b []byte
	for {
		b, err = json.MarshalIndent(journal{Format: 1, Versions: versions}, "", "  ")
		if err != nil {
			return Version{}, err
		}
		if len(b) < 32<<20 {
			break
		}
		if len(versions) == 1 {
			return Version{}, errors.New("configuration history exceeds size limit")
		}
		versions = versions[1:]
	}
	if err = atomicWrite(s.path, b); err != nil {
		return Version{}, err
	}
	s.versions = versions
	commit()
	persisted = true
	return cloneVersion(v), nil
}

func atomicWrite(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create configuration directory")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return errors.New("cannot start configuration transaction")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = io.Copy(f, bytes.NewReader(b))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot write configuration transaction")
	}
	if err = os.Rename(name, path); err != nil {
		return errors.New("cannot commit configuration transaction")
	}
	return nil
}
