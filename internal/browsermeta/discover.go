// Package browsermeta inspects profile metadata, never credential databases.
package browsermeta

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Root struct{ Browser, Path string }
type Candidate struct {
	ID             string `json:"id"`
	Browser        string `json:"browser"`
	Profile        string `json:"profile"`
	Name           string `json:"name"`
	Root           string `json:"root"`
	Authentication string `json:"authentication"`
}

func StandardRoots() []Root {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".config")
	rel := []Root{{"chrome", "google-chrome"}, {"edge", "microsoft-edge"}, {"brave", "BraveSoftware/Brave-Browser"}, {"chromium", "chromium"}, {"vivaldi", "vivaldi"}, {"opera", "opera"}, {"firefox", "../.mozilla/firefox"}}
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
		rel = []Root{{"chrome", "Google/Chrome/User Data"}, {"edge", "Microsoft/Edge/User Data"}, {"brave", "BraveSoftware/Brave-Browser/User Data"}, {"chromium", "Chromium/User Data"}, {"vivaldi", "Vivaldi/User Data"}}
	} else if runtime.GOOS == "darwin" {
		base = filepath.Join(home, "Library", "Application Support")
		rel = []Root{{"chrome", "Google/Chrome"}, {"edge", "Microsoft Edge"}, {"brave", "BraveSoftware/Brave-Browser"}, {"chromium", "Chromium"}, {"vivaldi", "Vivaldi"}, {"opera", "com.operasoftware.Opera"}, {"firefox", "Firefox"}, {"arc", "Arc/User Data"}}
	}
	out := []Root{}
	if base != "" {
		for _, r := range rel {
			out = append(out, Root{r.Browser, filepath.Join(base, filepath.FromSlash(r.Path))})
		}
	}
	if runtime.GOOS == "windows" {
		if roaming := os.Getenv("APPDATA"); roaming != "" {
			out = append(out, Root{"firefox", filepath.Join(roaming, "Mozilla", "Firefox")}, Root{"opera", filepath.Join(roaming, "Opera Software", "Opera Stable")})
		}
		if base != "" {
			matches, _ := filepath.Glob(filepath.Join(base, "Packages", "TheBrowserCompany.Arc_*", "LocalCache", "Local", "Arc", "User Data"))
			for i, path := range matches {
				if i >= 16 {
					break
				}
				out = append(out, Root{"arc", path})
			}
		}
	}
	return out
}

func Discover(roots []Root) []Candidate {
	out := []Candidate{}
	add := func(root Root, profile, name string) {
		if len(out) >= 256 || len(profile) > 512 || len(name) > 256 {
			return
		}
		sum := sha256.Sum256([]byte(root.Browser + "\x00" + root.Path + "\x00" + profile))
		out = append(out, Candidate{ID: hex.EncodeToString(sum[:12]), Browser: root.Browser, Profile: profile, Name: name, Root: root.Path, Authentication: "not_checked"})
	}
	for _, root := range roots {
		if root.Browser == "firefox" {
			f, err := os.Open(filepath.Join(root.Path, "profiles.ini"))
			if err != nil {
				continue
			}
			scanner := bufio.NewScanner(io.LimitReader(f, 1<<20))
			section, name, path := "", "", ""
			flush := func() {
				if strings.HasPrefix(section, "Profile") && path != "" {
					add(root, path, name)
				}
			}
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
					flush()
					section = strings.Trim(line, "[]")
					name = ""
					path = ""
					continue
				}
				key, value, ok := strings.Cut(line, "=")
				if ok {
					if key == "Name" {
						name = value
					}
					if key == "Path" {
						path = value
					}
				}
			}
			flush()
			f.Close()
			continue
		}
		f, err := os.Open(filepath.Join(root.Path, "Local State"))
		if err != nil {
			continue
		}
		var data struct {
			Profile struct {
				InfoCache map[string]struct {
					Name string `json:"name"`
				} `json:"info_cache"`
			} `json:"profile"`
		}
		err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&data)
		f.Close()
		if err != nil {
			continue
		}
		for profile, metadata := range data.Profile.InfoCache {
			add(root, profile, metadata.Name)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Browser == out[j].Browser {
			return out[i].Profile < out[j].Profile
		}
		return out[i].Browser < out[j].Browser
	})
	return out
}
