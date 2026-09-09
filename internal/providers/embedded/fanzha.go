package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type fanzhaSessionResponse struct {
	Data struct {
		Data      string `json:"data"`
		SessionID string `json:"session_id"`
		ID        string `json:"id"`
	} `json:"data"`
}

func (c *Client) doFanzha(ctx context.Context, protocol, model string, stream bool, body []byte) (*http.Response, error) {
	if protocol != "chat" {
		return nil, errors.New("fanzha adapter supports only chat protocol")
	}
	key, err := c.credential()
	if err != nil {
		return nil, err
	}
	base, err := c.baseURL(fanzhaDefaultBase)
	if err != nil {
		return nil, err
	}
	req, err := parseChatRequest(body)
	if err != nil {
		return nil, err
	}
	prompt, err := fanzhaPrompt(req.Messages)
	if err != nil {
		return nil, err
	}
	session, err := c.fanzhaCreateSession(ctx, base, key)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]string{"conversation_id": session, "query": prompt})
	if err != nil {
		return nil, errors.New("cannot encode fanzha request")
	}
	headers := http.Header{
		"Authorization": []string{"Bearer " + key},
		"Content-Type":  []string{"application/json"},
		"Accept":        []string{"text/event-stream"},
		"User-Agent":    []string{"Mozilla/5.0 (Linux; Android 15; V2425A)"},
	}
	resp, err := c.request(ctx, http.MethodPost, base+"/api/ai/chat?type=0", payload, headers)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, nil
	}
	if stream && !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		_ = resp.Body.Close()
		return nil, errors.New("fanzha upstream did not return an SSE stream")
	}
	id := chatID()
	created := time.Now().Unix()
	streamBody := c.newStreamBody(resp.Body, id, model, func(event, data string) ([]byte, error) {
		return fanzhaEvent(event, data, id, model, created)
	})
	return c.responseFromStream(resp, streamBody, stream, id, model)
}

func fanzhaPrompt(messages []chatMessage) (string, error) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return textContent(messages[i].Content)
		}
	}
	return textContent(messages[len(messages)-1].Content)
}

func (c *Client) fanzhaCreateSession(ctx context.Context, base, key string) (string, error) {
	headers := http.Header{
		"Authorization": []string{"Bearer " + key},
		"Content-Type":  []string{"application/json"},
		"User-Agent":    []string{"Mozilla/5.0 (Linux; Android 15; V2425A)"},
	}
	resp, err := c.request(ctx, http.MethodPost, base+"/api/ai/create_session", []byte("{}"), headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fanzha session creation returned status %d", resp.StatusCode)
	}
	var value fanzhaSessionResponse
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", errors.New("cannot read fanzha session response")
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return "", errors.New("invalid fanzha session response")
	}
	session := value.Data.Data
	if session == "" {
		session = value.Data.SessionID
	}
	if session == "" {
		session = value.Data.ID
	}
	if session == "" {
		return "", errors.New("fanzha session response did not contain a session id")
	}
	return session, nil
}

func fanzhaEvent(_ string, data, id, model string, created int64) ([]byte, error) {
	if data == "[DONE]" {
		return nil, errStreamComplete
	}
	if data == "" {
		return nil, nil
	}
	var value struct {
		Data struct {
			Type   string `json:"type"`
			Answer string `json:"answer"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return nil, fmt.Errorf("invalid fanzha SSE event: %w", err)
	}
	if value.Data.Type != "answer" || value.Data.Answer == "" {
		return nil, nil
	}
	return chatChunk(id, model, created, map[string]any{"content": value.Data.Answer}, nil), nil
}
