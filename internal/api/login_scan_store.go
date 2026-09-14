package api

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Only the latest metadata-only scan is persisted. Connection endpoints and
// authorization tickets are intentionally excluded from loginBatchView JSON.
func (q *loginBatchQueue) save() error {
	if q.path == "" {
		return nil
	}
	data, err := json.Marshal(q.view)
	if err != nil || len(data) > 1<<20 {
		return errors.New("scan history exceeds limit")
	}
	if err = os.MkdirAll(filepath.Dir(q.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(q.path), ".login-scan-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), q.path)
}

func (q *loginBatchQueue) load(path string) error {
	q.path = path
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 1<<20 {
		return errors.New("invalid scan history size")
	}
	d := json.NewDecoder(io.LimitReader(f, (1<<20)+1))
	d.DisallowUnknownFields()
	if d.Decode(&q.view) != nil || d.Decode(new(any)) != io.EOF || len(q.view.Sites) > 1024 {
		return errors.New("invalid scan history")
	}
	// Windows cannot replace a destination still held open by this reader.
	if err := f.Close(); err != nil {
		return err
	}
	if q.view.State == "running" {
		q.view.State = "interrupted"
		q.view.FinishedAt = time.Now().UTC()
		for i := range q.view.Sites {
			if q.view.Sites[i].State == "running" || q.view.Sites[i].State == "queued" {
				q.view.Sites[i].State = "not_attempted"
			}
		}
		return q.save()
	}
	return nil
}
