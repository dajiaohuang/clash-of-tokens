package api

import (
	"clash-of-tokens/internal/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginArgumentsUseDedicatedDirectory(t *testing.T) {
	root := t.TempDir()
	args, err := loginArguments(config.BrowserProfile{ID: "personal", CDPURL: "http://127.0.0.1:9223"}, filepath.Join(root, "sessions.json"), "https://chatgpt.com/backend-api/conversation")
	if err != nil {
		t.Fatal(err)
	}
	if args[len(args)-1] != "https://chatgpt.com/" || !strings.Contains(strings.Join(args, "\n"), filepath.Join(root, "profiles", "personal", "browser-data")) {
		t.Fatal(args)
	}
	if _, err := loginArguments(config.BrowserProfile{}, "state", "file:///secret"); err == nil {
		t.Fatal("unsafe destination accepted")
	}
}
