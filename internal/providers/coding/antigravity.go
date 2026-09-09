package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	wire "clash-of-tokens/internal/protocol"
)

// Antigravity speaks the Cloud Code Assist product protocol. Its request is a
// product envelope around Gemini contents and its endpoint is /v1internal,
// rather than the public Gemini API endpoint.
func (c *Client) doAntigravity(ctx context.Context, protocol, model string, stream bool, body []byte, headers http.Header) (*http.Response, error) {
	if protocol != "gemini" {
		return nil, ErrProtocol
	}
	if strings.TrimSpace(c.source.Project) == "" {
		return nil, errors.New("antigravity source requires an explicit project")
	}
	requestBody, err := antigravityRequest(model, body, c.source.Project, cloneHeaderValue(headers, "X-COT-Session"))
	if err != nil {
		return nil, &requestError{err}
	}
	suffix := "/v1internal:generateContent"
	if stream {
		suffix = "/v1internal:streamGenerateContent"
	}
	endpoint, err := baseURL(c.source, suffix)
	if err != nil {
		return nil, err
	}
	if stream {
		endpoint += "?alt=sse"
	}
	req, err := c.request(ctx, http.MethodPost, endpoint, requestBody)
	if err != nil {
		return nil, err
	}
	setBearer(req, credential(c.source))
	req.Header.Set("Accept", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	req.Header.Set("User-Agent", "antigravity/hub/2.9.1 darwin/arm64")
	resp, err := c.execute(req)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	if stream {
		setResponseBody(resp, &antigravityStream{body: resp.Body, events: wire.NewSSEReader(resp.Body, 16<<20)}, "text/event-stream")
		return resp, nil
	}
	data, readErr := readJSONBody(resp.Body, 16<<20)
	if readErr != nil {
		return nil, readErr
	}
	converted, unwrapErr := unwrapAntigravity(data)
	if unwrapErr != nil {
		return nil, unwrapErr
	}
	setResponseBody(resp, io.NopCloser(bytes.NewReader(converted)), "application/json")
	return resp, nil
}

func antigravityRequest(model string, body []byte, project, session string) ([]byte, error) {
	payload, err := strictObject(body, "Antigravity request")
	if err != nil {
		return nil, err
	}
	if err := validateAntigravityRoot(payload); err != nil {
		return nil, err
	}
	if _, ok := payload["request"]; !ok {
		native := make(map[string]json.RawMessage, len(payload))
		for key, value := range payload {
			switch key {
			case "model", "project", "userAgent", "requestType", "requestId":
				continue
			default:
				native[key] = value
			}
		}
		request, _ := json.Marshal(native)
		outer := map[string]json.RawMessage{"request": request}
		for key, value := range payload {
			switch key {
			case "model", "project", "userAgent", "requestType", "requestId":
				outer[key] = value
			}
		}
		payload = outer
	}
	for key, value := range map[string]string{
		"model":     model,
		"project":   project,
		"userAgent": "antigravity",
	} {
		encoded, _ := json.Marshal(value)
		payload[key] = encoded
	}
	requestType := stringField(payload, "requestType")
	if requestType == "" {
		requestType = "agent"
		encoded, _ := json.Marshal(requestType)
		payload["requestType"] = encoded
	}
	if requestType != "agent" && requestType != "web_search" && requestType != "image_gen" {
		return nil, errors.New("Antigravity requestType is unsupported")
	}
	request, err := rawObject(payload["request"])
	if err != nil {
		return nil, errors.New("Antigravity request field must be an object")
	}
	if requestType != "web_search" {
		if strings.TrimSpace(session) == "" {
			session = stringField(request, "sessionId")
		}
		if strings.TrimSpace(session) == "" {
			session = randomID("session-")
		}
		sessionJSON, _ := json.Marshal(session)
		request["sessionId"] = sessionJSON
	}
	requestBytes, _ := json.Marshal(request)
	payload["request"] = requestBytes
	if requestType != "web_search" {
		requestID, _ := json.Marshal(randomID("agent-"))
		payload["requestId"] = requestID
	}
	return json.Marshal(payload)
}

func unwrapAntigravity(data []byte) ([]byte, error) {
	root, err := rawObject(data)
	if err != nil {
		return nil, errors.New("Antigravity returned an invalid JSON response")
	}
	if hasJSON(root, "error") && !hasJSON(root, "response") {
		return nil, errors.New("Antigravity returned an error response")
	}
	if response, ok := root["response"]; ok {
		if _, err := rawObject(response); err != nil {
			return nil, errors.New("Antigravity response envelope is invalid")
		}
		return append([]byte(nil), response...), nil
	}
	return append([]byte(nil), data...), nil
}

type antigravityStream struct {
	body     io.ReadCloser
	events   *wire.SSEReader
	pending  []byte
	done     bool
	finished map[int]bool
	blocked  bool
}

func (s *antigravityStream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 && !s.done {
		frame, err := s.events.Next()
		if err != nil {
			if err == io.EOF {
				if !s.blocked {
					if len(s.finished) == 0 {
						return 0, io.ErrUnexpectedEOF
					}
					for _, finished := range s.finished {
						if !finished {
							return 0, io.ErrUnexpectedEOF
						}
					}
				}
				s.done = true
				return 0, io.EOF
			}
			return 0, err
		}
		data := wire.SSEData(frame)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
			if len(s.finished) == 0 && !s.blocked {
				return 0, io.ErrUnexpectedEOF
			}
			s.pending = append(s.pending[:0], "data: [DONE]\n\n"...)
			s.done = true
			continue
		}
		converted, unwrapErr := unwrapAntigravity(data)
		if unwrapErr != nil {
			return 0, unwrapErr
		}
		var result struct {
			Candidates []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finishReason"`
			} `json:"candidates"`
			PromptFeedback struct {
				BlockReason string `json:"blockReason"`
			} `json:"promptFeedback"`
		}
		if json.Unmarshal(converted, &result) != nil {
			return 0, errors.New("invalid Antigravity candidate response")
		}
		if s.finished == nil {
			s.finished = make(map[int]bool)
		}
		for _, candidate := range result.Candidates {
			if candidate.Index < 0 || candidate.Index >= 32 {
				return 0, errors.New("too many Antigravity candidates")
			}
			if candidate.FinishReason != "" {
				s.finished[candidate.Index] = true
			} else if _, exists := s.finished[candidate.Index]; !exists {
				s.finished[candidate.Index] = false
			}
		}
		if result.PromptFeedback.BlockReason != "" {
			s.blocked = true
		}
		s.pending = append(s.pending[:0], "data: "...)
		s.pending = append(s.pending, converted...)
		s.pending = append(s.pending, '\n', '\n')
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if n == 0 && s.done {
		return 0, io.EOF
	}
	return n, nil
}

func (s *antigravityStream) Close() error { return s.body.Close() }
