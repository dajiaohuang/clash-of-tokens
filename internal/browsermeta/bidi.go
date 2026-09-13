package browsermeta

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"clash-of-tokens/internal/browserbidi"
	"github.com/chromedp/cdproto/network"
)

func CheckEngine(ctx context.Context, id, endpoint, engine string) Status {
	if engine != "firefox" {
		return Check(ctx, id, endpoint)
	}
	out := Status{Profile: id, State: "unavailable", CheckedAt: time.Now().UTC(), Authentication: "not_checked"}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	client, err := browserbidi.Connect(ctx, endpoint)
	if err != nil {
		return out
	}
	defer client.Close()
	var tree struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if client.Call(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0}, &tree) != nil {
		return out
	}
	out.State = "connected"
	out.Browser = "Firefox (WebDriver BiDi)"
	out.Pages = len(tree.Contexts)
	return out
}

func CookiesEngine(ctx context.Context, endpoint, destination, engine string) (string, int, error) {
	if engine != "firefox" {
		return Cookies(ctx, endpoint, destination)
	}
	u, err := url.Parse(destination)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return "", 0, errors.New("invalid cookie destination")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := browserbidi.Connect(ctx, endpoint)
	if err != nil {
		return "", 0, err
	}
	defer client.Close()
	domains := []string{u.Hostname()}
	// Host cookies and domain cookies are separate filters. Never read the
	// entire profile jar just to discard unrelated products afterwards.
	if net.ParseIP(u.Hostname()) == nil {
		labels := strings.Split(u.Hostname(), ".")
		for i := 0; i < len(labels)-1; i++ {
			domains = append(domains, "."+strings.Join(labels[i:], "."))
		}
	}
	cookies := []*network.Cookie{}
	seen := map[string]bool{}
	requestPath := u.Path
	if requestPath == "" {
		requestPath = "/"
	}
	for _, domain := range domains {
		var result struct {
			Cookies []struct {
				Name   string  `json:"name"`
				Domain string  `json:"domain"`
				Path   string  `json:"path"`
				Secure bool    `json:"secure"`
				Expiry float64 `json:"expiry"`
				Value  struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"value"`
			} `json:"cookies"`
		}
		err = client.Call(ctx, "storage.getCookies", map[string]any{"filter": map[string]string{"domain": domain}}, &result)
		if err != nil {
			return "", 0, err
		}
		for _, item := range result.Cookies {
			if item.Domain != domain || item.Secure && u.Scheme != "https" || item.Expiry > 0 && item.Expiry < float64(time.Now().Unix()) {
				continue
			}
			if item.Path != "/" && requestPath != item.Path && !(strings.HasPrefix(requestPath, item.Path) && (strings.HasSuffix(item.Path, "/") || strings.HasPrefix(strings.TrimPrefix(requestPath, item.Path), "/"))) {
				continue
			}
			key := item.Domain + "\x00" + item.Path + "\x00" + item.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			value := item.Value.Value
			if item.Value.Type == "base64" {
				raw, e := base64.StdEncoding.DecodeString(value)
				if e != nil {
					return "", 0, errors.New("unsupported browser cookie value")
				}
				value = string(raw)
			} else if item.Value.Type != "string" {
				return "", 0, errors.New("unsupported browser cookie value")
			}
			cookies = append(cookies, &network.Cookie{Name: item.Name, Value: value, Path: item.Path})
			if len(cookies) > 256 {
				return "", 0, errors.New("selected provider has too many cookies")
			}
		}
	}
	sort.SliceStable(cookies, func(i, j int) bool { return len(cookies[i].Path) > len(cookies[j].Path) })
	return cookieHeader(cookies)
}
