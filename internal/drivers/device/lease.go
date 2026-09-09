package device

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// One clipboard/physical input pipeline per process. The file also protects
// against other gateway processes sharing this configured state directory.
var slot = make(chan struct{}, 1)

type Lease struct {
	path  string
	dirty bool
}

func Acquire(stateDir, serial string) (*Lease, error) {
	select {
	case slot <- struct{}{}:
	default:
		return nil, errors.New("device: execution resource busy")
	}
	failed := true
	defer func() {
		if failed {
			<-slot
		}
	}()
	if !filepath.IsAbs(stateDir) || serial == "" {
		return nil, errors.New("device: absolute state directory and serial required")
	}
	if e := os.MkdirAll(stateDir, 0700); e != nil {
		return nil, e
	}
	hash := sha256.Sum256([]byte(serial))
	path := filepath.Join(stateDir, hex.EncodeToString(hash[:16])+".lock")
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, errors.New("device: resource locked or previous operation needs manual review; inspect device state directory")
	}
	if _, e = f.WriteString("active; if this process stopped, inspect the device before removing this lock\n"); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil || ce != nil {
		return nil, errors.New("device: cannot persist resource lease")
	}
	failed = false
	return &Lease{path: path}, nil
}
func (l *Lease) MarkDirty() { l.dirty = true }
func (l *Lease) Complete()  { l.dirty = false }
func (l *Lease) Close() {
	if !l.dirty {
		_ = os.Remove(l.path)
	}
	<-slot
}
