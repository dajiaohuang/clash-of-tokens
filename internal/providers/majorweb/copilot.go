package majorweb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/gobwas/ws"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) doCopilotWeb(parent context.Context, protocol, model string, stream bool, body []byte, cred credentials) (*http.Response, error) {
	mode := map[string]string{"copilot": "chat", "copilot-chat": "chat", "copilot-think": "reasoning", "copilot-smart": "smart"}[model]
	if mode == "" || protocol != "chat" || len(body) > 1<<20 {
		return nil, &requestError{"Copilot Web requires bounded text chat and a supported Copilot mode selector"}
	}
	input, err := parseChatInput(body, model)
	if err != nil {
		return nil, err
	}
	token := strings.TrimPrefix(cred.value, "Bearer ")
	if token == "" || len(token) > 16384 || strings.ContainsAny(token, "\r\n; \t") {
		return nil, ErrCredential
	}
	base, err := baseURL(c.source, "https://copilot.microsoft.com")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	h := http.Header{"Content-Type": {"application/json"}, "Origin": {base}, "Referer": {base + "/"}, "Authorization": {"Bearer " + token}, "User-Agent": {"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"}}
	payload, _ := json.Marshal(map[string]any{"timeZone": "America/New_York", "startNewConversation": true, "teenSupportEnabled": false})
	resp, err := request(ctx, c.http, http.MethodPost, endpoint(base, "/c/api/start"), payload, h)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	data, err := readBounded(resp.Body, 1<<20)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var session struct {
		CurrentID string `json:"currentConversationId"`
		ID        string `json:"conversationId"`
		Blocked   bool   `json:"isBlocked"`
	}
	if json.Unmarshal(data, &session) != nil || session.Blocked {
		return nil, errors.New("Copilot conversation unavailable")
	}
	if session.CurrentID != "" {
		session.ID = session.CurrentID
	}
	if !huggingID.MatchString(session.ID) {
		return nil, errors.New("Copilot invalid conversation ID")
	}
	u, _ := url.Parse(base)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/c/api/chat"
	u.RawQuery = url.Values{"api-version": {"2"}, "clientSessionId": {randomUUID()}, "accessToken": {token}}.Encode()
	dialer := ws.Dialer{Timeout: 15 * time.Second, Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": {base}, "Authorization": {"Bearer " + token}})}
	conn, prefetch, _, err := dialer.Dial(ctx, u.String())
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("Copilot WebSocket connection failed")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	var reader io.Reader = conn
	if prefetch != nil {
		reader = io.MultiReader(prefetch, conn)
	}
	rw := &gatewayReadWriter{Reader: reader, Writer: conn}
	send := func() error {
		return writeGatewayJSON(conn, map[string]any{"event": "send", "conversationId": session.ID, "content": []any{map[string]string{"type": "text", "text": input.Prompt}}, "mode": mode})
	}
	if send() != nil {
		return nil, errors.New("Copilot send failed")
	}
	var answer, reasoning strings.Builder
	total, challenges := 0, 0
	done := false
	for !done {
		raw, e := readBoundedServerText(rw, 1<<20)
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrTruncated
		}
		total += len(raw)
		if total > 16<<20 {
			return nil, errors.New("Copilot response exceeds limit")
		}
		var event struct {
			Event     string          `json:"event"`
			Text      string          `json:"text"`
			Method    string          `json:"method"`
			Parameter string          `json:"parameter"`
			Error     json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &event) != nil || event.Event == "" {
			return nil, errors.New("Copilot invalid WebSocket event")
		}
		if event.Event == "error" || (len(event.Error) > 0 && string(event.Error) != "null") {
			return nil, errors.New("Copilot upstream error")
		}
		switch event.Event {
		case "appendText":
			answer.WriteString(event.Text)
		case "replaceText":
			answer.Reset()
			answer.WriteString(event.Text)
		case "chainOfThought":
			reasoning.WriteString(event.Text)
		case "imageGenerated":
			return nil, errors.New("Copilot returned unsupported media")
		case "done":
			done = true
		case "challenge":
			challenges++
			if challenges > 2 || event.Method != "hashcash" || answer.Len() > 0 {
				return nil, errors.New("Copilot unsupported verification challenge")
			}
			param, diff, ok := strings.Cut(event.Parameter, ":")
			difficulty, e := strconv.Atoi(diff)
			if !ok || e != nil || len(param) > 1024 {
				return nil, errors.New("Copilot invalid hashcash challenge")
			}
			proof, e := copilotHashcash(ctx, param, difficulty)
			if e != nil {
				return nil, e
			}
			if writeGatewayJSON(conn, map[string]string{"event": "challengeResponse", "token": strconv.Itoa(proof), "method": "hashcash"}) != nil || send() != nil {
				return nil, errors.New("Copilot challenge response failed")
			}
		}
	}
	if strings.TrimSpace(answer.String()) == "" {
		return nil, errors.New("Copilot completed without answer")
	}
	return bufferedProductResponse(model, answer.String(), reasoning.String(), stream), nil
}

func copilotHashcash(ctx context.Context, parameter string, difficulty int) (int, error) {
	if difficulty < 1 || difficulty > 8 {
		return 0, errors.New("Copilot unsupported hashcash difficulty")
	}
	prefix := strings.Repeat("0", difficulty)
	for i := 0; i < 10_000_000; i++ {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		hash := sha256.Sum256([]byte(parameter + ":" + strconv.Itoa(i)))
		if strings.HasPrefix(hex.EncodeToString(hash[:]), prefix) {
			return i, nil
		}
	}
	return 0, errors.New("Copilot hashcash work budget exhausted")
}

func bufferedProductResponse(model, content, reasoning string, stream bool) *http.Response {
	id := randomID("chatcmpl-")
	created := time.Now().Unix()
	var result []byte
	if stream {
		result = chatChunk(id, model, created, map[string]any{"role": "assistant"}, nil)
		if reasoning != "" {
			result = append(result, chatChunk(id, model, created, map[string]any{"reasoning_content": reasoning}, nil)...)
		}
		result = append(result, chatChunk(id, model, created, map[string]any{"content": content}, nil)...)
		result = append(result, chatChunk(id, model, created, map[string]any{}, "stop")...)
		result = append(result, []byte("data: [DONE]\n\n")...)
	} else {
		result = chatCompletion(id, model, content, reasoning, created)
	}
	out := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(result)), ContentLength: int64(len(result))}
	out.Header.Set("X-COT-Delivery", "buffered")
	if stream {
		out.Header.Set("Content-Type", "text/event-stream")
	} else {
		out.Header.Set("Content-Type", "application/json")
	}
	return out
}
