// Package secrets stores gateway access keys, never upstream browser cookies.
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Keys struct {
	API   string `json:"api_key"`
	Admin string `json:"admin_key"`
}

func randomKey() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func LoadOrCreate(path string) (Keys, error) {
	f, e := os.Open(path)
	if e == nil {
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, 16385))
		if e != nil || len(b) > 16384 {
			return Keys{}, errors.New("invalid gateway key store")
		}
		clear, e := unseal(b)
		if e != nil {
			return Keys{}, errors.New("cannot unlock gateway keys for this operating-system account")
		}
		var keys Keys
		if json.Unmarshal(clear, &keys) != nil || len(keys.API) < 16 || len(keys.Admin) < 16 || keys.API == keys.Admin {
			return Keys{}, errors.New("invalid gateway keys")
		}
		return keys, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return Keys{}, e
	}
	keys := Keys{randomKey(), randomKey()}
	clear, _ := json.Marshal(keys)
	data, e := seal(clear)
	if e != nil {
		return Keys{}, e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return Keys{}, e
	}
	f, e = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(e, os.ErrExist) {
		return LoadOrCreate(path)
	}
	if e != nil {
		return Keys{}, e
	}
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return Keys{}, e
	}
	if ce != nil {
		return Keys{}, ce
	}
	return keys, nil
}
