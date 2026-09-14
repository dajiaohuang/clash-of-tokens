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
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"clash-of-tokens/internal/browsermeta"
	"golang.org/x/sys/windows"
)

func protectFixture(t *testing.T, b []byte) []byte {
	t.Helper()
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		t.Fatal(err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, int(out.Size))...)
}

func fixture(t *testing.T) (browsermeta.Candidate, *sql.DB, []byte) {
	t.Helper()
	root := t.TempDir()
	profile := filepath.Join(root, "Default")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	encoded := base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), protectFixture(t, key)...))
	state, _ := json.Marshal(map[string]any{"os_crypt": map[string]string{"encrypted_key": encoded}})
	if err := os.WriteFile(filepath.Join(root, "Local State"), state, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(profile, "Login Data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE logins(origin_url TEXT,username_value TEXT,password_value BLOB,blacklisted_by_user INTEGER,date_created INTEGER);`)
	if err != nil {
		t.Fatal(err)
	}
	return browsermeta.Candidate{ID: "fixture", Browser: "chrome", Root: root, Profile: "Default", Name: "Test profile"}, db, key
}

func encryptedFixture(t *testing.T, key []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{1}, gcm.NonceSize())
	return append(append([]byte("v10"), nonce...), gcm.Seal(nil, nonce, []byte("synthetic-password"), nil)...)
}

func TestSelectedBrowserImportReadsWALAndKeepsSource(t *testing.T) {
	c, db, key := fixture(t)
	encrypted := encryptedFixture(t, key)
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(`INSERT INTO logins VALUES('https://example.test/login','test-user',?,0,?)`, encrypted, i); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO logins VALUES('https://example.test/blocked','',?,1,0)`, encrypted); err != nil {
		t.Fatal(err)
	}
	n, err := countProfile(context.Background(), c)
	if err != nil || n != 2 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	r, err := read(context.Background(), "chrome", []browsermeta.Candidate{c})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Entries) != 1 || r.Duplicates != 1 || r.Entries[0].Password != "synthetic-password" || r.Entries[0].Username != "test-user" {
		t.Fatal("incorrect batch or decrypted fixture")
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logins`).Scan(&after); err != nil || after != 3 {
		t.Fatal("source database changed")
	}
	if _, err = read(context.Background(), "edge", []browsermeta.Candidate{c}); err == nil {
		t.Fatal("accepted undiscovered browser")
	}
}

func TestProtectedPasswordsAbortWholeBrowser(t *testing.T) {
	c, db, key := fixture(t)
	for _, value := range [][]byte{encryptedFixture(t, key), []byte("v20-unavailable")} {
		if _, err := db.Exec(`INSERT INTO logins VALUES('https://example.test','user',?,0,0)`, value); err != nil {
			t.Fatal(err)
		}
	}
	n, err := countProfile(context.Background(), c)
	if n != 2 || !errors.Is(err, ErrProtected) {
		t.Fatal("protected storage not identified")
	}
	r, err := read(context.Background(), "chrome", []browsermeta.Candidate{c})
	if !errors.Is(err, ErrProtected) || len(r.Entries) != 0 {
		t.Fatal("partial import returned for protected browser")
	}
}

func TestDiscoveryDoesNotUnlockAndProfilesCannotEscape(t *testing.T) {
	c, db, key := fixture(t)
	if _, err := db.Exec(`INSERT INTO logins VALUES('https://example.test','user',?,0,0)`, encryptedFixture(t, key)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Root, "Local State"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	n, err := countProfile(context.Background(), c)
	if n != 1 || err != nil {
		t.Fatal("discovery tried to unlock")
	}
	if _, err = readProfile(context.Background(), c); !errors.Is(err, ErrProtected) {
		t.Fatal("missing key accepted")
	}
	c.Profile = "../outside"
	if _, err = profileFile(c, "Login Data"); !errors.Is(err, ErrLocked) {
		t.Fatal("profile traversal accepted")
	}
}
