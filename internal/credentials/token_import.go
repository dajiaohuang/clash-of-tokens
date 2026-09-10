package credentials

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// ParseCLIAccessToken reads user-selected exports; it does not run a CLI or
// access its keychain. Refresh tokens are intentionally not imported until a
// provider-specific refresh contract is implemented.
func ParseCLIAccessToken(format string, data []byte) (string, error) {
	if len(data) == 0 || len(data) > 1<<20 {
		return "", errors.New("CLI export must be between 1 byte and 1 MiB")
	}
	var wire struct {
		AccessToken string `json:"access_token"`
		Tokens      struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return "", errors.New("invalid CLI export JSON")
	}
	value := ""
	switch format {
	case "codex":
		value = wire.Tokens.AccessToken
	case "gemini-cli", "oauth":
		value = wire.AccessToken
	default:
		return "", errors.New("unsupported CLI export format")
	}
	if value == "" || len(value) > 64<<10 || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("CLI export has no usable access token")
	}
	return value, nil
}

func (s *Store) Create(kind, source, value string) (Metadata, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Metadata{}, errors.New("cannot allocate credential reference")
	}
	id := "cred://import-" + hex.EncodeToString(random[:])
	if err := s.Put(id, kind, source, value); err != nil {
		return Metadata{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records[id].Metadata, nil
}
