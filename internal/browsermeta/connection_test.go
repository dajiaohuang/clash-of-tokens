package browsermeta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingEndpointOnlyAcceptsNativeLoopbackMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	for _, invalid := range []string{"", "9222\nhttps://evil.test/", "1\n/devtools/browser/abcdefgh", "65536\n/devtools/browser/abcdefgh", "9222\n/devtools/browser/abcdefgh?token=bad", strings.Repeat("x", 5000)} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ExistingEndpoint(dir); err == nil {
			t.Fatal("invalid rendezvous accepted")
		}
	}
	if err := os.WriteFile(path, []byte("9222\r\n/devtools/browser/abcdefgh-1234\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	endpoint, err := ExistingEndpoint(dir)
	if err != nil || endpoint != "ws://127.0.0.1:9222/devtools/browser/abcdefgh-1234" {
		t.Fatal(endpoint, err)
	}
}
