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
	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name,omitempty"`
	OwnedBy          string   `json:"owned_by,omitempty"`
	Description      string   `json:"description,omitempty"`
	CreatedUnix      int64    `json:"created_unix,omitempty"`
	InputTokenLimit  int64    `json:"input_token_limit,omitempty"`
	OutputTokenLimit int64    `json:"output_token_limit,omitempty"`
	SupportedMethods []string `json:"supported_methods,omitempty"`
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
				ID          string `json:"id"`
				DisplayName string `json:"display_name"`
				OwnedBy     string `json:"owned_by"`
				Created     int64  `json:"created"`
				CreatedAt   string `json:"created_at"`
			} `json:"data"`
			Models []struct {
				Name                       string   `json:"name"`
				DisplayName                string   `json:"displayName"`
				Description                string   `json:"description"`
				InputTokenLimit            int64    `json:"inputTokenLimit"`
				OutputTokenLimit           int64    `json:"outputTokenLimit"`
				SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
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
		models := []DiscoveredModel{}
		if adapter == "gemini" {
			for _, m := range wire.Models {
				models = append(models, DiscoveredModel{ID: strings.TrimPrefix(m.Name, "models/"), DisplayName: boundedMetadata(m.DisplayName, 256), Description: boundedMetadata(m.Description, 2048), InputTokenLimit: boundedTokenLimit(m.InputTokenLimit), OutputTokenLimit: boundedTokenLimit(m.OutputTokenLimit), SupportedMethods: boundedMethods(m.SupportedGenerationMethods)})
			}
		} else {
			for _, m := range wire.Data {
				created := m.Created
				if created == 0 && m.CreatedAt != "" {
					if parsed, err := time.Parse(time.RFC3339, m.CreatedAt); err == nil {
						created = parsed.Unix()
					}
				}
				models = append(models, DiscoveredModel{ID: m.ID, DisplayName: boundedMetadata(m.DisplayName, 256), OwnedBy: boundedMetadata(m.OwnedBy, 256), CreatedUnix: created})
			}
		}
		for _, model := range models {
			id := model.ID
			if id == "" || len(id) > 512 || strings.ContainsAny(id, "\r\n\x00") {
				return out, errors.New("invalid discovered model identifier")
			}
			if !seen[id] {
				if len(out.Models) >= 10000 {
					return out, nil
				}
				seen[id] = true
				out.Models = append(out.Models, model)
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

func boundedMetadata(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func boundedTokenLimit(value int64) int64 {
	if value < 0 || value > 1<<31 {
		return 0
	}
	return value
}

func boundedMethods(values []string) []string {
	if len(values) > 32 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = boundedMetadata(value, 128)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
