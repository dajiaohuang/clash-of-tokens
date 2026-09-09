package webnext

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clash-of-tokens/internal/config"
)

const (
	defaultFlowithBase  = "https://edge.flowith.net"
	defaultLangFastBase = "https://yzaaxwkjukajpwqflndu.supabase.co"
	defaultLangFastWS   = "wss://langfast-prompt-runner-35808038077.us-central1.run.app"
	defaultLiaoBotsBase = "https://liaobots.work"
)

func (c *Client) doEaseMate(ctx context.Context, cred credentials, req chatRequest) (*http.Response, error) {
	return nil, ErrUnsupported
}

func (c *Client) doFlowith(ctx context.Context, cred credentials, req chatRequest) (*http.Response, error) {
	base, err := baseURL(c.source, defaultFlowithBase)
	if err != nil {
		return nil, err
	}
	if cred.SupabaseURL != "" {
		if err := c.flowithPreauthorize(ctx, cred); err != nil {
			return nil, err
		}
	}
	model := strings.TrimPrefix(req.Model, "flowith-")
	payload := map[string]any{
		"stream":   true,
		"model":    model,
		"messages": req.Messages,
		"nodeId":   randomID(""),
	}
	headers := providerHeaders("https://flowith.net", "https://flowith.net/")
	headers.Set("Accept", "text/event-stream")
	headers.Set("ResponseType", "stream")
	if cred.Cookie != "" {
		headers.Set("Cookie", cred.Cookie)
	}
	resp, err := postJSON(ctx, c.http, withQuery(joinPath(base, "/ai/chat"), "mode", "general"), payload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if req.Stream {
		resp.Body = newConvertedResponse(ctx, resp.Body, req.Model, parseFlowith)
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Set("X-COT-Delivery", "upstream")
		return resp, nil
	}
	return collectResponse(ctx, resp.Body, req.Model, parseFlowith)
}

func (c *Client) flowithPreauthorize(ctx context.Context, cred credentials) error {
	base, err := baseURL(configSource(cred.SupabaseURL), "")
	if err != nil {
		return err
	}
	convID := randomID("")
	userID := randomID("")
	endpoint := joinPath(base, "/rest/v1/cooperate")
	values := url.Values{"select": {"*"}, "conv_id": {"eq." + convID}, "user_id": {"eq." + userID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+values.Encode(), nil)
	if err != nil {
		return errors.New("webnext adapter: invalid Flowith preauthorization request")
	}
	req.Header.Set("Accept", "application/vnd.pgrst.object+json")
	req.Header.Set("apikey", cred.SupabaseAnonKey)
	req.Header.Set("Authorization", "Bearer "+cred.SupabaseAnonKey)
	req.Header.Set("Accept-Profile", "public")
	req.Header.Set("X-Client-Info", "supabase-js-web/2.51.0")
	req.Header.Set("Origin", "https://flowith.net")
	req.Header.Set("Referer", "https://flowith.net/")
	if cred.Cookie != "" {
		req.Header.Set("Cookie", cred.Cookie)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("webnext adapter: Flowith preauthorization failed")
	}
	defer resp.Body.Close()
	// The pinned reference continues after an HTTP preauthorization status
	// (the request is a session warm-up and may still establish useful edge
	// state). A transport failure is still fatal, while the actual chat call
	// remains responsible for reporting authentication or WAF rejection.
	return nil
}

// configSource allows baseURL's URL policy to be reused for a credential-level
// endpoint without widening the public config.Source surface.
func configSource(value string) (s config.Source) { s.BaseURL = value; return s }

func (c *Client) doLiaoBots(ctx context.Context, cred credentials, req chatRequest) (*http.Response, error) {
	base, err := baseURL(c.source, defaultLiaoBotsBase)
	if err != nil {
		return nil, err
	}
	model := liaoModel(req.Model)
	payload := map[string]any{
		"conversationId": randomID(""),
		"models": []any{map[string]any{
			"CreatedAt": time.Now().UTC().Format(time.RFC3339Nano),
			"context":   model.context, "modelId": req.Model, "name": model.name, "provider": model.provider,
			"inputOrigin": 0, "inputPricing": 0, "outputOrigin": 0, "outputPricing": 0,
			"supportFiles": "jpg,jpeg,png,webp,wav,aac,mp3,ogg",
		}},
		"search":    "false",
		"messages":  req.Messages,
		"key":       "",
		"prompt":    "你是 {{model}}，一个由 {{provider}} 训练的大型语言模型，请仔细遵循用户的指示。",
		"prompt_id": "",
	}
	headers := providerHeaders("https://liaobots.work", "https://liaobots.work/")
	headers.Set("Accept", "text/event-stream")
	headers.Set("X-Auth-Code", cred.AuthCode)
	headers.Set("Cookie", cred.Cookie)
	resp, err := postJSON(ctx, c.http, joinPath(base, "/api/chat"), payload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if req.Stream {
		resp.Body = newConvertedResponse(ctx, resp.Body, req.Model, parseLiaoBots)
		resp.ContentLength = -1
		resp.Header.Del("Content-Length")
		resp.Header.Set("Content-Type", "text/event-stream")
		resp.Header.Set("X-COT-Delivery", "upstream")
		return resp, nil
	}
	return collectResponse(ctx, resp.Body, req.Model, parseLiaoBots)
}

type liaoModelInfo struct {
	name, provider string
	context        int
}

func liaoModel(model string) liaoModelInfo {
	known := map[string]liaoModelInfo{
		"gemini-3-pro-preview": {"Gemini-3-Pro-Preview", "Google", 1000},
		"gpt-4o":               {"GPT-4o", "OpenAI", 128000},
		"claude-3-5-sonnet":    {"Claude-3.5-Sonnet", "Anthropic", 200000},
		"gpt-4o-mini":          {"GPT-4o-Mini", "OpenAI", 128000},
		"o1-preview":           {"O1-Preview", "OpenAI", 128000},
		"o1-mini":              {"O1-Mini", "OpenAI", 128000},
	}
	if info, ok := known[model]; ok {
		return info
	}
	return liaoModelInfo{model, "Unknown", 10000}
}

func providerHeaders(origin, referer string) http.Header {
	return http.Header{
		"Accept":        []string{"application/json"},
		"Content-Type":  []string{"application/json"},
		"Origin":        []string{origin},
		"Referer":       []string{referer},
		"User-Agent":    []string{"clash-tokens/0.1"},
		"Cache-Control": []string{"no-cache"},
	}
}

func joinPath(base, suffix string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(suffix, "/")
}

func withQuery(base, key, value string) string {
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func isEventStream(resp *http.Response) bool {
	return strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
}
