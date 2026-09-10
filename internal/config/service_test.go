package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigurationTransactionAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	commits, discards := 0, 0
	service, err := OpenService(path, Default(), func(c Config) (func(), func(), error) {
		return func() { commits++ }, func() { discards++ }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next := service.Current().Config
	next.Groups[0].AllowUnknownCost = new(bool)
	*next.Groups[0].AllowUnknownCost = true
	preview, err := service.Preview(1, next)
	if err != nil || preview.Revision != 2 || commits != 0 {
		t.Fatal(preview.Revision, err)
	}
	if _, err = os.Stat(path + ".state"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preview wrote state")
	}
	if _, err = service.Apply(1, next, "Allow unknown costs"); err != nil {
		t.Fatal(err)
	}
	if commits != 1 || discards != 0 {
		t.Fatal(commits, discards)
	}
	next.Groups[0].MinTier = "gold"
	if _, err = service.Apply(1, next, "stale"); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal(err)
	}
	current := service.Current()
	current.Config.Groups[0].MinTier = "diamond"
	if service.Current().Config.Groups[0].MinTier != "silver" {
		t.Fatal("caller mutated state")
	}
	loaded, err := Load(path)
	if err != nil || loaded.Groups[0].AllowUnknownCost == nil || !*loaded.Groups[0].AllowUnknownCost {
		t.Fatal("restart lost config", err)
	}
	if _, err = service.Rollback(2, 1); err != nil {
		t.Fatal(err)
	}
	if service.Current().Revision != 3 || service.Current().Config.Groups[0].AllowUnknownCost != nil {
		t.Fatal("rollback failed")
	}
	loaded, err = Load(path)
	if err != nil || loaded.Groups[0].AllowUnknownCost != nil {
		t.Fatal("rollback not persistent", err)
	}
}

func TestConfigurationFailurePreservesCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	committed, discarded := false, false
	s, err := OpenService(path, Default(), func(Config) (func(), func(), error) {
		return func() { committed = true }, func() { discarded = true }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path+".state", 0700); err != nil {
		t.Fatal(err)
	}
	next := Default()
	next.Groups[0].MinTier = "gold"
	if _, err = s.Apply(1, next, "change"); err == nil {
		t.Fatal("expected failed persistence")
	}
	if committed || !discarded || s.Current().Revision != 1 {
		t.Fatal("failure changed runtime")
	}
	next.Listen = "invalid"
	discarded = false
	if _, err = s.Apply(1, next, "invalid"); err == nil || discarded {
		t.Fatal("invalid config prepared runtime")
	}
}

func TestConfigurationPreparationFailureDoesNotPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := OpenService(path, Default(), func(Config) (func(), func(), error) { return nil, nil, errors.New("cannot compile") })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(1, Default(), "bad build"); err == nil {
		t.Fatal("expected prepare failure")
	}
	if _, err = os.Stat(path + ".state"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed prepare wrote history")
	}
}

func TestConfigurationRollbackRejectsStaleRevisionAndUnknownTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	prepared, committed, discarded := 0, 0, 0
	s, err := OpenService(path, Default(), func(Config) (func(), func(), error) {
		prepared++
		return func() { committed++ }, func() { discarded++ }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next := Default()
	next.Runtime.MaxQueued++
	if _, err = s.Apply(1, next, "first"); err != nil {
		t.Fatal(err)
	}
	next.Runtime.MaxQueued++
	if _, err = s.Apply(2, next, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Rollback(2, 1); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale rollback error = %v", err)
	}
	if got := s.Current().Revision; got != 3 {
		t.Fatalf("stale rollback changed revision to %d", got)
	}
	if prepared != 2 || committed != 2 || discarded != 0 {
		t.Fatalf("stale rollback changed preparation lifecycle: prepared=%d committed=%d discarded=%d", prepared, committed, discarded)
	}
	if _, err = s.Rollback(3, 999); err == nil {
		t.Fatal("unknown rollback target accepted")
	}
	if got := s.Current().Revision; got != 3 {
		t.Fatalf("unknown rollback changed revision to %d", got)
	}
}
