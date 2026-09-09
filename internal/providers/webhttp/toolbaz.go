package webhttp

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// This adapter only submits a token already issued for the user's legitimate
// session. It does not generate verification tokens or automate challenges.
func (c *Client) doToolbaz(ctx context.Context, model string, stream bool, prompt string) (*http.Response, error) {
	var cred struct {
		SessionID string `json:"session_id"`
		Token     string `json:"token"`
	}
	if json.Unmarshal([]byte(os.Getenv(c.source.KeyEnv)), &cred) != nil || cred.SessionID == "" || cred.Token == "" {
		return nil, &Error{401, "ToolBaz key_env requires session_id and an existing writing token"}
	}
	form := url.Values{"text": {prompt}, "model": {model}, "session_id": {cred.SessionID}, "capcha": {cred.Token}}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.source.BaseURL, "/")+"/writing.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid ToolBaz endpoint")
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://toolbaz.com")
	req.Header.Set("Referer", "https://toolbaz.com/writer/chat-gpt-alternative")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.AddCookie(&http.Cookie{Name: "SessionID", Value: cred.SessionID})
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("ToolBaz request failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("ToolBaz response incomplete or oversized")
	}
	if len(data) == 0 {
		return nil, errors.New("ToolBaz returned no answer")
	}
	content := html.UnescapeString(strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n").Replace(string(data)))
	return output(model, stream, "buffered", func(emit func(string) error) (string, error) { return "stop", emit(content) }, nil), nil
}
