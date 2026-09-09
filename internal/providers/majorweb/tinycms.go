package majorweb

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

//go:embed tinycms-signer.js
var tinySignerJS string

type tinyChallenge struct {
	Challenge  string      `json:"challenge"`
	ID         string      `json:"challengeId"`
	Expires    json.Number `json:"expiresAt"`
	Version    string      `json:"version"`
	Difficulty int         `json:"difficulty"`
}
type tinyPayload struct {
	Signature   string `json:"signature"`
	Fingerprint string `json:"fingerprint"`
	ClientIP    string `json:"client_ip"`
	Version     string `json:"v"`
	Pow         struct {
		Seed       json.Number `json:"seed_nonce"`
		Nonce      json.Number `json:"nonce"`
		Hash       string      `json:"hash"`
		Difficulty int         `json:"difficulty"`
	} `json:"pow"`
}

func (c *Client) tinySign(parent context.Context, uuid, stamp, nonce, ip string, ch tinyChallenge) (tinyPayload, error) {
	var out tinyPayload
	if !c.browser.Enabled || c.browser.CDPURL == "" {
		return out, &HTTPError{Status: 503, What: "TinyCMS signing requires configured Chrome"}
	}
	a, stopA := chromedp.NewRemoteAllocator(context.Background(), c.browser.CDPURL)
	defer stopA()
	tab, stopTab := chromedp.NewContext(a)
	defer stopTab()
	ctx, cancel := context.WithTimeout(tab, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	args, _ := json.Marshal([]any{uuid, stamp, nonce, ch.Challenge, ip, ch.Difficulty})
	script := "(async()=>{" + tinySignerJS + "\nawait __wbg_init(Uint8Array.from(atob(WASM_BASE64),c=>c.charCodeAt(0)));return generate_secure_payload(..." + string(args) + ");})()"
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank"), chromedp.Evaluate(script, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		return out, errors.New("TinyCMS browser signer failed")
	}
	return out, nil
}

func (c *Client) doTinyCMS(ctx context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	if protocol != "chat" || len(body) > 64<<10 {
		return nil, ErrUnsupported
	}
	in, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	// Identity and egress address are supplied by the account owner, never generated.
	if !strings.HasPrefix(cred.value, "R") || len(cred.value) > 256 || strings.ContainsAny(cred.value, "\r\n\t ") || net.ParseIP(cred.clientIP) == nil {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://gov.freegpt.win")
	if err != nil {
		return nil, err
	}
	r, err := request(ctx, c.http, http.MethodGet, base+"/api/challenge", nil, http.Header{"uuid": {cred.value}, "x-origin": {base}, "Accept": {"application/json"}})
	if err != nil {
		return nil, err
	}
	if r.StatusCode != 200 {
		r.Body.Close()
		return nil, &HTTPError{Status: r.StatusCode, What: "TinyCMS challenge failed"}
	}
	raw, err := readBounded(r.Body, 16384)
	r.Body.Close()
	if err != nil {
		return nil, err
	}
	var ch tinyChallenge
	if json.Unmarshal(raw, &ch) != nil || ch.Challenge == "" || len(ch.Challenge) > 4096 || ch.ID == "" || len(ch.ID) > 256 || ch.Version == "" || len(ch.Version) > 32 || ch.Difficulty < 0 || ch.Difficulty > 16 {
		return nil, errors.New("TinyCMS unsupported challenge")
	}
	expires, err := ch.Expires.Int64()
	if err != nil || expires <= time.Now().UnixMilli() {
		return nil, errors.New("TinyCMS expired challenge")
	}
	for _, v := range []string{ch.ID, ch.Version} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, errors.New("TinyCMS invalid challenge")
		}
	}
	stamp, nonce := strconv.FormatInt(time.Now().UnixMilli(), 10), randomUUID()
	signed, err := c.tinySign(ctx, cred.value, stamp, nonce, cred.clientIP, ch)
	if err != nil {
		return nil, err
	}
	if signed.ClientIP != cred.clientIP || signed.Pow.Difficulty != ch.Difficulty || signed.Signature == "" || signed.Fingerprint == "" || signed.Version == "" || signed.Pow.Hash == "" {
		return nil, errors.New("TinyCMS invalid signature result")
	}
	if time.Now().UnixMilli() >= expires {
		return nil, errors.New("TinyCMS challenge expired during signing")
	}
	h := http.Header{"uuid": {cred.value}, "x-origin": {base}, "Referer": {base + "/"}, "x-secure-challenge-id": {ch.ID}, "x-secure-challenge-expires-at": {ch.Expires.String()}, "x-secure-challenge-version": {ch.Version}, "x-secure-signature": {signed.Signature}, "x-secure-fingerprint": {signed.Fingerprint}, "x-secure-client-ip": {signed.ClientIP}, "x-secure-pow-seed-nonce": {signed.Pow.Seed.String()}, "x-secure-pow-nonce": {signed.Pow.Nonce.String()}, "x-secure-pow-hash": {signed.Pow.Hash}, "x-secure-pow-difficulty": {strconv.Itoa(ch.Difficulty)}, "x-secure-timestamp": {stamp}, "x-secure-nonce": {nonce}, "x-secure-version": {signed.Version}, "x-session-id": {nonce}, "userid": {cred.userID}, "Content-Type": {"application/json"}, "Accept": {"text/event-stream"}}
	if cred.userID == "" {
		h.Set("userid", cred.value[:min(len(cred.value), 20)])
	}
	for _, values := range h {
		for _, v := range values {
			if len(v) > 8192 || strings.ContainsAny(v, "\r\n") {
				return nil, errors.New("TinyCMS invalid signed header")
			}
		}
	}
	payload, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]string{"role": "user", "content": in.Prompt}}, "stream": true})
	r, err = request(ctx, c.http, http.MethodPost, base+"/api/openai/oneapi/v1/chat/completions", payload, h)
	if err != nil {
		return nil, err
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		r.Body.Close()
		return nil, &HTTPError{Status: r.StatusCode, What: "TinyCMS chat failed"}
	}
	// Converted through the strict OpenAI stream parser below.
	return tinyResponse(r, stream, model)
}

func tinyResponse(r *http.Response, stream bool, model string) (*http.Response, error) {
	defer r.Body.Close()
	d := newSSEDecoder(r.Body)
	var answer strings.Builder
	finished, done := false, false
	finish := ""
	total := 0
	for !done {
		_, raw, ok, err := d.next()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrTruncated
		}
		total += len(raw)
		if total > 4<<20 {
			return nil, errors.New("TinyCMS response exceeds limit")
		}
		if raw == "[DONE]" {
			if !finished {
				return nil, ErrTruncated
			}
			done = true
			continue
		}
		var e struct {
			Error   any `json:"error"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					ToolCalls any    `json:"tool_calls"`
				} `json:"delta"`
				Finish *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(raw), &e) != nil || e.Error != nil {
			return nil, errors.New("TinyCMS invalid upstream event")
		}
		for _, choice := range e.Choices {
			if choice.Index != 0 || finished || choice.Delta.ToolCalls != nil {
				return nil, errors.New("TinyCMS unsupported choice")
			}
			answer.WriteString(choice.Delta.Content)
			if choice.Finish != nil {
				finish = *choice.Finish
				if finish != "stop" && finish != "length" {
					return nil, errors.New("TinyCMS unsupported finish")
				}
				finished = true
			}
		}
	}
	if answer.Len() == 0 {
		return nil, errors.New("TinyCMS empty response")
	}
	id, created := randomID("chatcmpl-"), time.Now().Unix()
	var data []byte
	if stream {
		data = chatChunk(id, model, created, map[string]any{"role": "assistant", "content": answer.String()}, nil)
		data = append(data, chatChunk(id, model, created, map[string]any{}, finish)...)
		data = append(data, []byte("data: [DONE]\n\n")...)
	} else {
		data = []byte(fmt.Sprintf(`{"id":%q,"object":"chat.completion","created":%d,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":%q}]}`, id, created, model, mustTinyJSON(answer.String()), finish))
	}
	r.Header = make(http.Header)
	r.Header.Set("X-COT-Delivery", "buffered")
	ct := "application/json"
	if stream {
		ct = "text/event-stream"
	}
	setBody(r, io.NopCloser(bytes.NewReader(data)), ct)
	return r, nil
}
func mustTinyJSON(s string) []byte { b, _ := json.Marshal(s); return b }
