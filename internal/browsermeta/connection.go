package browsermeta

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var activeBrowserPath = regexp.MustCompile(`^/devtools/browser/[A-Za-z0-9-]{8,128}$`)

// ExistingEndpoint reads only Chrome's opt-in remote-debugging rendezvous.
// It never reads cookies, Local State encryption keys, or password databases.
func ExistingEndpoint(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "DevToolsActivePort"))
	if err != nil {
		return "", errors.New("browser_authorization_required")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return "", errors.New("invalid_browser_connection")
	}
	lines := strings.Fields(string(b))
	if len(lines) != 2 {
		return "", errors.New("invalid_browser_connection")
	}
	port, err := strconv.Atoi(lines[0])
	if err != nil || port < 1024 || port > 65535 || !activeBrowserPath.MatchString(lines[1]) {
		return "", errors.New("invalid_browser_connection")
	}
	return "ws://127.0.0.1:" + strconv.Itoa(port) + lines[1], nil
}
