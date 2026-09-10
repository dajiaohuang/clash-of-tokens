package credentials

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
)

// ParseExport reports excluded non-web/non-password records separately. It
// never imports secure notes, TOTP seeds, cards, or historical passwords.
func ParseExport(format string, data []byte) ([]ImportEntry, int, error) {
	if format == "csv" || format == "json" {
		entries, err := ParseImport(format, data)
		return entries, 0, err
	}
	if len(data) == 0 || len(data) > 4<<20 {
		return nil, 0, errors.New("export must be between 1 byte and 4 MiB")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	entries := []ImportEntry{}
	skipped := 0
	add := func(e ImportEntry) error {
		if !strings.Contains(e.URL, "://") && strings.Contains(e.URL, ".") {
			e.URL = "https://" + e.URL
		}
		u, err := url.Parse(e.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || e.Password == "" {
			skipped++
			return nil
		}
		entries = append(entries, e)
		if len(entries) > 1000 {
			return errors.New("export exceeds 1000 login candidates")
		}
		return nil
	}
	switch format {
	case "bitwarden-json":
		var wire struct {
			Encrypted bool `json:"encrypted"`
			Items     []struct {
				Type  int    `json:"type"`
				Name  string `json:"name"`
				Login struct {
					Username string `json:"username"`
					Password string `json:"password"`
					URIs     []struct {
						URI string `json:"uri"`
					} `json:"uris"`
				} `json:"login"`
			} `json:"items"`
		}
		if json.Unmarshal(data, &wire) != nil || wire.Encrypted || wire.Items == nil || len(wire.Items) > 10000 {
			return nil, 0, errors.New("expected an unencrypted Bitwarden JSON export with at most 10000 items")
		}
		for _, item := range wire.Items {
			if item.Type != 1 || len(item.Login.URIs) == 0 {
				skipped++
				continue
			}
			// Each URI is a separate selectable binding candidate; credential
			// values remain protected and the user sees exactly what is selected.
			for _, uri := range item.Login.URIs {
				if err := add(ImportEntry{Name: item.Name, URL: uri.URI, Username: item.Login.Username, Password: item.Login.Password}); err != nil {
					return nil, skipped, err
				}
			}
		}
	case "bitwarden-csv", "1password-csv", "keepassxc-csv":
		r := csv.NewReader(bytes.NewReader(data))
		header, err := r.Read()
		if err != nil {
			return nil, 0, errors.New("invalid manager CSV header")
		}
		aliases := map[string]string{"title": "name", "website": "url"}
		if format == "bitwarden-csv" {
			aliases = map[string]string{"login_uri": "url", "login_username": "username", "login_password": "password"}
		}
		cols := map[string]int{}
		for i, h := range header {
			h = strings.ToLower(strings.TrimSpace(h))
			if mapped, ok := aliases[h]; ok {
				h = mapped
			}
			if _, exists := cols[h]; exists {
				return nil, 0, errors.New("ambiguous manager CSV headers")
			}
			cols[h] = i
		}
		for _, key := range []string{"url", "username", "password"} {
			if _, ok := cols[key]; !ok {
				return nil, 0, errors.New("manager CSV lacks required login columns")
			}
		}
		for rows := 0; ; rows++ {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil || rows >= 10000 {
				return nil, skipped, errors.New("invalid or oversized manager CSV")
			}
			if i, ok := cols["type"]; format == "bitwarden-csv" && ok && row[i] != "login" {
				skipped++
				continue
			}
			e := ImportEntry{URL: row[cols["url"]], Username: row[cols["username"]], Password: row[cols["password"]]}
			if i, ok := cols["name"]; ok {
				e.Name = row[i]
			}
			if err := add(e); err != nil {
				return nil, skipped, err
			}
		}
	default:
		return nil, 0, errors.New("unsupported password manager export format")
	}
	if len(entries) == 0 {
		return entries, skipped, nil
	}
	encoded, _ := json.Marshal(entries)
	defer clear(encoded)
	validated, err := ParseImport("json", encoded)
	return validated, skipped, err
}
