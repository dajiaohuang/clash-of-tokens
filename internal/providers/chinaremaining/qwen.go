package chinaremaining

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// qwenAccount is deliberately kept separate from the other web credentials:
// Qwen mainland uses a complete browser Cookie and its matching XSRF token.
// A bearer token or an international-site cookie is not interchangeable.
type qwenAccount struct {
	Cookie    string   `json:"cookie"`
	XSRFToken string   `json:"xsrf_token"`
	Models    []string `json:"models,omitempty"`
}

type qwenCredential struct {
	Cookie        string         `json:"cookie,omitempty"`
	XSRFToken     string         `json:"xsrf_token,omitempty"`
	Accounts      []qwenAccount  `json:"accounts,omitempty"`
	ModelAccounts map[string]int `json:"model_accounts,omitempty"`
}

func (c *Client) qwenCredential(model string) (qwenAccount, error) {
	if strings.TrimSpace(c.source.KeyEnv) == "" {
		return qwenAccount{}, ErrCredential
	}
	raw := c.source.CredentialValue()
	if len(raw) == 0 || len(raw) > maxCredentialBytes || strings.ContainsAny(raw, "\r\n\x00") {
		return qwenAccount{}, ErrCredential
	}
	var cred qwenCredential
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cred); err != nil {
		return qwenAccount{}, ErrCredential
	}
	var trailing any
	if dec.Decode(&trailing) != io.EOF {
		return qwenAccount{}, ErrCredential
	}
	if cred.Cookie != "" || cred.XSRFToken != "" {
		if len(cred.Accounts) != 0 || cred.Cookie == "" || cred.XSRFToken == "" {
			return qwenAccount{}, ErrCredential
		}
		cred.Accounts = []qwenAccount{{Cookie: cred.Cookie, XSRFToken: cred.XSRFToken}}
	}
	if len(cred.Accounts) == 0 || len(cred.Accounts) > 32 {
		return qwenAccount{}, ErrCredential
	}
	idx := 0
	if n, ok := cred.ModelAccounts[model]; ok {
		idx = n
	}
	if idx < 0 || idx >= len(cred.Accounts) {
		return qwenAccount{}, ErrCredential
	}
	a := cred.Accounts[idx]
	if !validQwenValue(a.Cookie, 64<<10) || !validQwenValue(a.XSRFToken, 4096) {
		return qwenAccount{}, ErrCredential
	}
	if len(a.Models) > 0 {
		allowed := false
		for _, name := range a.Models {
			if name == model {
				allowed = true
				break
			}
		}
		if !allowed {
			return qwenAccount{}, fmt.Errorf("%w: account is not configured for model %q", ErrCredential, model)
		}
	}
	return a, nil
}

func validQwenValue(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && !strings.ContainsAny(value, "\r\n\x00")
}

func (c *Client) doQwenCN(ctx context.Context, model string, req chatRequest) (*http.Response, error) {
	cred, err := c.qwenCredential(model)
	if err != nil {
		return nil, err
	}
	base, err := baseURL(c.source, defaultQwenBase)
	if err != nil {
		return nil, err
	}
	// The record-list call is a best-effort browser warm-up in the pinned
	// source. It must never replace the actual completion request.
	warmPayload := map[string]any{
		"pageNo": 1, "terminal": "web", "pageSize": 10000, "module": "uploadhistory",
		"fileTypes":     []string{"file", "audio", "video"},
		"recordSources": []string{"chat", "zhiwen", "tingwu"},
		"status":        []int{20, 30, 40, 41},
		"taskTypes":     []string{"local", "net_source", "doc_read", "paper_read", "book_read"},
	}
	if warm, e := json.Marshal(warmPayload); e == nil {
		h := qwenHeaders(cred)
		h.Set("Accept", "application/json, text/plain, */*")
		warmResp, e := c.do(ctx, http.MethodPost, base+"/assistant/api/record/list", warm, h)
		if e == nil {
			warmResp.Body.Close()
		}
	}
	contents := make([]map[string]string, 0, len(req.Messages))
	for _, msg := range req.Messages {
		text, _ := textContent(msg.Content)
		contents = append(contents, map[string]string{"role": msg.Role, "content": text, "contentType": "text"})
	}
	payload := map[string]any{
		"action": "next", "contents": contents, "model": model,
		"parentMsgId": "", "requestId": uuid(), "sessionId": "", "sessionType": "text_chat",
		"userAction": "new_top", "feature_config": map[string]bool{"search_enabled": false, "thinking_enabled": false},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	h := qwenHeaders(cred)
	return c.do(ctx, http.MethodPost, base+"/dialog/conversation", body, h)
}

func qwenHeaders(cred qwenAccount) http.Header {
	h := http.Header{
		"Accept": {"text/event-stream"}, "Content-Type": {"application/json;charset=UTF-8"},
		"Origin": {"https://www.tongyi.com"}, "Referer": {"https://www.tongyi.com/"},
		"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"},
		"Cookie":     {cred.Cookie}, "X-XSRF-Token": {cred.XSRFToken}, "X-Platform": {"pc_tongyi"},
	}
	return h
}
