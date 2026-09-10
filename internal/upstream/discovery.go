package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DiscoveredModel struct {
	ID string `json:"id"`
}
type Discovery struct {
	Models    []DiscoveredModel `json:"models"`
	Complete  bool              `json:"complete"`
	CheckedAt time.Time         `json:"checked_at"`
	Method    string            `json:"method"`
	Pages     int               `json:"pages"`
}

// Discover lists native API model metadata. It neither infers generation
// support from a model name nor promotes the result into configured routes.
func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	out := Discovery{Models: []DiscoveredModel{}, CheckedAt: time.Now().UTC(), Method: "models_endpoint"}
	adapter := c.source.Adapter
	if c.http == nil || (adapter != "openai" && adapter != "anthropic" && adapter != "gemini") {
		return out, errors.New("model discovery is not implemented for this adapter")
	}
	key := c.source.CredentialValue()
	if !c.source.Local && key == "" {
		return out, errors.New("source credential is unavailable")
	}
	endpoint := strings.TrimRight(c.source.BaseURL, "/") + "/models"
	seen := map[string]bool{}
	cursors := map[string]bool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		u, err := url.Parse(endpoint)
		if err != nil {
			return out, errors.New("invalid discovery endpoint")
		}
		query := u.Query()
		if adapter == "anthropic" {
			query.Set("limit", "1000")
			if cursor != "" {
				query.Set("after_id", cursor)
			}
		}
		if adapter == "gemini" {
			query.Set("pageSize", "1000")
			if cursor != "" {
				query.Set("pageToken", cursor)
			}
		}
		u.RawQuery = query.Encode()
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return out, errors.New("invalid discovery request")
		}
		req.Header.Set("Accept", "application/json")
		switch adapter {
		case "anthropic":
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case "gemini":
			req.Header.Set("x-goog-api-key", key)
		default:
			if key != "" {
				req.Header.Set("Authorization", "Bearer "+key)
			}
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return out, errors.New("model discovery transport failed")
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, errors.New("model discovery rejected by upstream")
		}
		if readErr != nil || len(data) > 2<<20 {
			return out, errors.New("model discovery response exceeded limit or was interrupted")
		}
		var wire struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			HasMore       bool   `json:"has_more"`
			LastID        string `json:"last_id"`
			NextPageToken string `json:"nextPageToken"`
		}
		if json.Unmarshal(data, &wire) != nil {
			return out, errors.New("invalid model discovery JSON")
		}
		if (adapter == "gemini" && wire.Models == nil) || (adapter != "gemini" && wire.Data == nil) {
			return out, errors.New("model discovery response lacks model list")
		}
		ids := []string{}
		if adapter == "gemini" {
			for _, m := range wire.Models {
				ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
			}
		} else {
			for _, m := range wire.Data {
				ids = append(ids, m.ID)
			}
		}
		for _, id := range ids {
			if id == "" || len(id) > 512 || strings.ContainsAny(id, "\r\n\x00") {
				return out, errors.New("invalid discovered model identifier")
			}
			if !seen[id] {
				if len(out.Models) >= 10000 {
					return out, nil
				}
				seen[id] = true
				out.Models = append(out.Models, DiscoveredModel{ID: id})
			}
		}
		out.Pages++
		cursor = ""
		if adapter == "gemini" {
			cursor = wire.NextPageToken
		} else if wire.HasMore {
			cursor = wire.LastID
			if cursor == "" {
				return out, errors.New("model discovery pagination cursor missing")
			}
		}
		if cursor == "" {
			out.Complete = true
			return out, nil
		}
		if len(cursor) > 4096 || cursors[cursor] {
			return out, errors.New("invalid model discovery pagination")
		}
		cursors[cursor] = true
	}
	return out, nil
}
