package businessweb

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const makerSuiteBase = "https://alkalimakersuite-pa.clients6.google.com/$rpc/google.internal.alkali.applications.makersuite.v1.MakerSuiteService"

// doAIStudioPlayground calls the documented MakerSuite JSON+protobuf RPC.
// Credentials may contain the dynamic browser headers captured from AI
// Studio: cookie, x_goog_api_key, visit_id, authuser and proof.
func (c *Client) doAIStudioPlayground(ctx context.Context, req chatRequest) (*http.Response, error) {
	cred, err := credential(c.source)
	if err != nil {
		return nil, err
	}
	endpoint, err := makerSuiteEndpoint(c.source.BaseURL)
	if err != nil {
		return nil, err
	}
	payload, err := makerSuiteRequest(req, cred)
	if err != nil {
		return nil, err
	}
	h := http.Header{"Content-Type": {"application/json+protobuf"}, "Accept": {"application/json+protobuf"}, "Origin": {"https://aistudio.google.com"}, "Referer": {"https://aistudio.google.com/"}, "User-Agent": {credOr(cred, "user_agent", "Mozilla/5.0")}, "X-User-Agent": {credOr(cred, "x_user_agent", "grpc-web-javascript/0.1")}}
	if v := credOr(cred, "x_goog_api_key", ""); v != "" {
		h.Set("X-Goog-Api-Key", v)
	}
	if v := credOr(cred, "authuser", "0"); v != "" {
		h.Set("X-Goog-Authuser", v)
	}
	if v := credOr(cred, "visit_id", ""); v != "" {
		h.Set("X-Aistudio-Visit-Id", v)
	}
	if v := credOr(cred, "tier", ""); v != "" {
		h.Set("X-AIStudio-G1-Tier", v)
	}
	if v := credOr(cred, "cookie", ""); v != "" {
		h.Set("Cookie", v)
	}
	if auth := makerSuiteAuthorization(cred, "https://aistudio.google.com"); auth != "" {
		h.Set("Authorization", auth)
	}
	resp, err := request(ctx, c.http, http.MethodPost, endpoint, payload, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status := resp.StatusCode
		resp.Body.Close()
		return nil, &HTTPError{Status: status}
	}
	raw, err := readBounded(resp.Body, maxResponseBytes)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	text, parseErr := parseMakerSuiteTextStrict(raw)
	if parseErr != nil {
		return nil, parseErr
	}
	if req.Stream {
		return streamResponse(req.Model, text, ""), nil
	}
	return jsonResponse(req.Model, text, ""), nil
}

func makerSuiteEndpoint(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return makerSuiteBase + "/GenerateContent", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("business web adapter: invalid AI Studio RPC URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", errors.New("business web adapter: AI Studio RPC URL must use HTTPS")
	}
	if strings.HasSuffix(u.Path, "/GenerateContent") {
		return u.String(), nil
	}
	return strings.TrimRight(u.String(), "/") + "/GenerateContent", nil
}

func makerSuiteRequest(req chatRequest, cred map[string]string) ([]byte, error) {
	contents := make([]any, 0, len(req.Messages))
	var system strings.Builder
	for _, m := range req.Messages {
		text, _ := textContent(m.Content)
		if m.Role == "system" {
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(text)
			continue
		}
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, []any{[]any{[]any{nil, text}}, role})
	}
	if len(contents) == 0 {
		return nil, errors.New("business web adapter: AI Studio Playground requires messages")
	}
	wire := make([]any, 11)
	wire[0] = "models/" + strings.TrimPrefix(req.Model, "models/")
	wire[1] = contents
	wire[3] = []any{nil, nil, nil, 1024, 0.8, 1, nil, "TEXT", nil, nil, nil, nil, nil, int64(1)}
	if system.Len() > 0 {
		wire[5] = []any{[]any{[]any{nil, system.String()}}, "user"}
	}
	wire[10] = int64(1)
	if proof := credOr(cred, "proof", ""); proof != "" {
		wire[4] = proof
	}
	return json.Marshal(wire)
}

func credOr(m map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(m[key]); v != "" {
		return v
	}
	return fallback
}

func makerSuiteAuthorization(cred map[string]string, origin string) string {
	for _, item := range []struct{ name, label string }{{"SAPISID", "SAPISIDHASH"}, {"__Secure-1PAPISID", "SAPISID1PHASH"}, {"__Secure-3PAPISID", "SAPISID3PHASH"}} {
		if v := cookieValue(credOr(cred, "cookie", ""), item.name); v != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			h := sha1.Sum([]byte(ts + " " + v + " " + origin))
			return item.label + " " + ts + "_" + fmt.Sprintf("%x", h[:])
		}
	}
	return ""
}

// parseMakerSuiteText reads candidate content from the response frame paths
// documented by AIStudio2API. Sparse slots are represented by null values and
// therefore need no protobuf dependency.
func parseMakerSuiteText(raw []byte) string {
	text, _ := parseMakerSuiteTextStrict(raw)
	return text
}

// parseMakerSuiteTextStrict decodes the GenerateContent JSON+protobuf
// envelope and requires the same evidence as the browser client: one
// candidate, a model content role, and an explicit integer finish reason.
// Returning text from an unfinished or error frame would turn an upstream
// failure into a successful completion.
func parseMakerSuiteTextStrict(raw []byte) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, ")]}'") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ")]}'"))
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("business web adapter: malformed AI Studio Playground response")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", errors.New("business web adapter: AI Studio Playground response has multiple root values")
	}
	root, ok := value.([]any)
	if !ok || len(root) == 0 {
		return "", errors.New("business web adapter: AI Studio Playground response root is not an array")
	}
	frames, ok := root[0].([]any)
	if !ok {
		if root[0] != nil {
			return "", errors.New("business web adapter: AI Studio Playground response field 1 is not an array")
		}
		return "", errors.New("business web adapter: AI Studio Playground response has no frames")
	}
	var out strings.Builder
	finished := false
	seenFrame := false
	for _, frame := range frames {
		if finished {
			return "", errors.New("business web adapter: AI Studio Playground frame after completion")
		}
		f, ok := frame.([]any)
		if !ok || len(f) == 0 {
			return "", errors.New("business web adapter: malformed AI Studio Playground frame")
		}
		seenFrame = true
		if f[0] == nil {
			if feedback, ok := fAt(f, 1).([]any); ok && len(feedback) > 0 {
				return "", errors.New("business web adapter: AI Studio Playground rejected the prompt")
			}
			return "", errors.New("business web adapter: AI Studio Playground returned an empty frame")
		}
		candidates, _ := f[0].([]any)
		if len(candidates) != 1 {
			return "", errors.New("business web adapter: AI Studio Playground response does not contain exactly one candidate")
		}
		candidate, _ := candidates[0].([]any)
		if len(candidate) == 0 {
			return "", errors.New("business web adapter: AI Studio Playground empty candidate")
		}
		if finish := fAt(candidate, 1); finish != nil {
			if !isJSONNumber(finish) {
				return "", errors.New("business web adapter: AI Studio Playground finish reason is not an integer")
			}
			code, _ := finish.(json.Number).Int64()
			if code != 1 {
				return "", errors.New("business web adapter: AI Studio Playground did not finish normally")
			}
			finished = true
		}
		if candidate[0] == nil {
			continue
		}
		content, ok := candidate[0].([]any)
		if !ok || len(content) < 2 {
			return "", errors.New("business web adapter: AI Studio Playground candidate content is malformed")
		}
		{
			if role, _ := content[1].(string); role != "model" {
				return "", errors.New("business web adapter: AI Studio Playground response role is not model")
			}
		}
		parts, _ := content[0].([]any)
		if content[0] != nil && parts == nil {
			return "", errors.New("business web adapter: AI Studio Playground content parts are malformed")
		}
		for _, part := range parts {
			p, ok := part.([]any)
			if !ok || len(p) < 2 {
				return "", errors.New("business web adapter: AI Studio Playground content part is malformed")
			}
			if s, ok := p[1].(string); ok {
				out.WriteString(s)
			}
		}
	}
	if !seenFrame || !finished {
		return "", errors.New("business web adapter: AI Studio Playground response has no finish frame")
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", errors.New("business web adapter: AI Studio Playground returned no text")
	}
	return out.String(), nil
}

func fAt(values []any, index int) any {
	if index < 0 || index >= len(values) {
		return nil
	}
	return values[index]
}

func isJSONNumber(value any) bool {
	n, ok := value.(json.Number)
	if !ok {
		return false
	}
	_, err := n.Int64()
	return err == nil
}
