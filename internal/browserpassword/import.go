// Package browserpassword imports saved logins from an explicitly selected
// browser owned by the current OS user. Discovery never decrypts passwords.
package browserpassword

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"clash-of-tokens/internal/browsermeta"
	"clash-of-tokens/internal/credentials"
)

const MaxEntries = 10000

var (
	ErrUnsupported = errors.New("browser_export_required")
	ErrLocked      = errors.New("browser_database_unavailable")
	ErrProtected   = errors.New("browser_protected_passwords")
	ErrLimit       = errors.New("browser_import_limit")
)

type Profile struct {
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Status string `json:"status"`
}

type Browser struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Count    int       `json:"count"`
	Status   string    `json:"status"`
	Profiles []Profile `json:"profiles"`
}

type Report struct {
	Entries    []credentials.ImportEntry
	Duplicates int
	Skipped    int
}

var browserNames = map[string]string{"chrome": "Google Chrome", "edge": "Microsoft Edge", "chromium": "Chromium", "brave": "Brave", "vivaldi": "Vivaldi", "opera": "Opera", "arc": "Arc", "firefox": "Firefox"}

func Discover(ctx context.Context) []Browser {
	return discover(ctx, browsermeta.Discover(browsermeta.StandardRoots()))
}

func discover(ctx context.Context, candidates []browsermeta.Candidate) []Browser {
	out := []Browser{}
	indices := map[string]int{}
	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}
		i, ok := indices[c.Browser]
		if !ok {
			i = len(out)
			indices[c.Browser] = i
			name := browserNames[c.Browser]
			if name == "" {
				name = c.Browser
			}
			out = append(out, Browser{ID: c.Browser, Name: name, Status: "ready", Profiles: []Profile{}})
		}
		count, err := countProfile(ctx, c)
		status := "ready"
		if err != nil {
			status = err.Error()
			out[i].Status = status
		}
		out[i].Count += count
		out[i].Profiles = append(out[i].Profiles, Profile{Name: c.Name, Count: count, Status: status})
	}
	for i := range out {
		if out[i].Status == "ready" && out[i].Count == 0 {
			out[i].Status = "empty"
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func Read(ctx context.Context, browser string) (Report, error) {
	return read(ctx, browser, browsermeta.Discover(browsermeta.StandardRoots()))
}

func read(ctx context.Context, browser string, candidates []browsermeta.Candidate) (Report, error) {
	result := Report{Entries: []credentials.ImportEntry{}}
	seen := map[string]bool{}
	found := false
	for _, c := range candidates {
		if c.Browser != browser {
			continue
		}
		found = true
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		entries, err := readProfile(ctx, c)
		if err != nil {
			return Report{}, err
		}
		for _, e := range entries {
			u, err := url.Parse(e.URL)
			if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || e.Password == "" {
				result.Skipped++
				continue
			}
			if len(e.URL) > 2048 || len(e.Username) > 1024 || len(e.Password) > 64<<10 {
				return Report{}, ErrLimit
			}
			// Keep the first (newest within a store) login for the exact URL/user.
			key := e.URL + "\x00" + e.Username
			if seen[key] {
				result.Duplicates++
				continue
			}
			seen[key] = true
			e.Name = u.Hostname()
			result.Entries = append(result.Entries, e)
			if len(result.Entries) > MaxEntries {
				return Report{}, ErrLimit
			}
		}
	}
	if !found {
		return Report{}, errors.New("browser_not_found")
	}
	return result, nil
}

// Profile names come from browser metadata, not trusted paths. Do not follow a
// profile name or junction outside its discovered browser root.
func profileFile(c browsermeta.Candidate, name string) (string, error) {
	if filepath.IsAbs(c.Profile) || c.Profile == "" {
		return "", ErrLocked
	}
	root, err := filepath.EvalSymlinks(c.Root)
	if err != nil {
		return "", ErrLocked
	}
	path := filepath.Join(root, c.Profile, name)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", ErrLocked
	}
	actual, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", ErrLocked
	}
	rel, err = filepath.Rel(root, actual)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", ErrLocked
	}
	return actual, nil
}
