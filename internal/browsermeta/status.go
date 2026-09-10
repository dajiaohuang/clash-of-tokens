package browsermeta

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type Status struct {
	Profile        string    `json:"profile"`
	State          string    `json:"state"`
	Browser        string    `json:"browser"`
	Pages          int       `json:"pages"`
	// MaxSessions is the configured browser session ceiling. BoundAccounts and
	// BoundSources describe control-plane bindings, not authenticated pages;
	// providers may retain additional upstream state outside this metadata
	// probe.
	MaxSessions    int       `json:"max_sessions,omitempty"`
	BoundAccounts  int       `json:"bound_accounts,omitempty"`
	BoundSources   int       `json:"bound_sources,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	Authentication string    `json:"authentication"`
}

func Check(ctx context.Context, id, endpoint string) Status {
	out := Status{Profile: id, State: "unavailable", CheckedAt: time.Now().UTC(), Authentication: "not_checked"}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, dst any) bool {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(endpoint, "/")+path, nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
		if err != nil || resp.StatusCode != 200 || len(data) > 256<<10 {
			return false
		}
		return json.Unmarshal(data, dst) == nil
	}
	var version struct {
		Browser  string `json:"Browser"`
		Protocol string `json:"Protocol-Version"`
	}
	if !get("/json/version", &version) || version.Browser == "" || version.Protocol == "" || len(version.Browser) > 128 {
		return out
	}
	out.Browser = version.Browser
	out.State = "connected"
	var tabs []struct {
		Type string `json:"type"`
	}
	if get("/json/list", &tabs) {
		for _, tab := range tabs {
			if tab.Type == "page" {
				out.Pages++
			}
		}
	} else {
		out.State = "connected_page_list_unavailable"
	}
	return out
}
