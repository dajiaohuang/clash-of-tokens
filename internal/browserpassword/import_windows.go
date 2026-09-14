//go:build windows

package browserpassword

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/credentials"
	"golang.org/x/sys/windows"
	_ "modernc.org/sqlite"
)

var loginStores = []string{"Login Data", "Login Data For Account"}

func openLogins(ctx context.Context, c browsermeta.Candidate, name string) (*sql.DB, error) {
	if c.Browser == "firefox" {
		return nil, ErrUnsupported
	}
	path, err := profileFile(c, name)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(1500)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrLocked
	}
	db.SetMaxOpenConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, ErrLocked
	}
	return db, nil
}

func countProfile(ctx context.Context, c browsermeta.Candidate) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	count := 0
	protected := false
	for _, name := range loginStores {
		db, err := openLogins(ctx, c, name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return count, err
		}
		var n, p int
		err = db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN hex(substr(password_value,1,3)) IN ('763230','763131') THEN 1 ELSE 0 END),0) FROM logins WHERE blacklisted_by_user=0 AND length(password_value)>0`).Scan(&n, &p)
		db.Close()
		if err != nil {
			return count, ErrLocked
		}
		count += n
		protected = protected || p > 0
	}
	if count > MaxEntries {
		return count, ErrLimit
	}
	if protected {
		return count, ErrProtected
	}
	return count, nil
}

func readProfile(ctx context.Context, c browsermeta.Candidate) ([]credentials.ImportEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out := []credentials.ImportEntry{}
	var key []byte
	defer func() { clear(key) }()
	for _, name := range loginStores {
		db, err := openLogins(ctx, c, name)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer db.Close()
			rows, err := db.QueryContext(ctx, `SELECT origin_url,username_value,password_value FROM logins WHERE blacklisted_by_user=0 AND length(password_value)>0 ORDER BY date_created DESC LIMIT 10001`)
			if err != nil {
				return ErrLocked
			}
			defer rows.Close()
			for rows.Next() {
				var e credentials.ImportEntry
				var encrypted []byte
				if err = rows.Scan(&e.URL, &e.Username, &encrypted); err != nil {
					return ErrLocked
				}
				if bytes.HasPrefix(encrypted, []byte("v10")) && key == nil {
					key, err = readBrowserKey(c.Root)
					if err != nil {
						return err
					}
				}
				plain, err := decryptPassword(encrypted, key)
				clear(encrypted)
				if err != nil {
					return err
				}
				e.Password = string(plain)
				clear(plain)
				out = append(out, e)
				if len(out) > MaxEntries {
					return ErrLimit
				}
			}
			if rows.Err() != nil {
				return ErrLocked
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func readBrowserKey(root string) ([]byte, error) {
	f, err := os.Open(filepath.Join(root, "Local State"))
	if err != nil {
		return nil, ErrProtected
	}
	defer f.Close()
	var state struct {
		OSCrypt struct {
			Key string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&state) != nil {
		return nil, ErrProtected
	}
	b, err := base64.StdEncoding.DecodeString(state.OSCrypt.Key)
	if err != nil || !bytes.HasPrefix(b, []byte("DPAPI")) {
		return nil, ErrProtected
	}
	return unprotect(b[5:])
}

func decryptPassword(encrypted, key []byte) ([]byte, error) {
	if bytes.HasPrefix(encrypted, []byte("v10")) {
		if len(key) != 32 {
			return nil, ErrProtected
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrProtected
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrProtected
		}
		payload := encrypted[3:]
		if len(payload) < gcm.NonceSize()+gcm.Overhead() {
			return nil, ErrProtected
		}
		plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
		if err != nil {
			return nil, ErrProtected
		}
		return plain, nil
	}
	// App-bound and unknown versioned ciphertext must be exported by the browser.
	if len(encrypted) >= 3 && strings.HasPrefix(string(encrypted[:3]), "v") {
		return nil, ErrProtected
	}
	return unprotect(encrypted)
}

func unprotect(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, ErrProtected
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out) != nil {
		return nil, ErrProtected
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...), nil
}
