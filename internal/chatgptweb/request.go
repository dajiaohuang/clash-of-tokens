package chatgptweb

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type webRequest struct {
	Model    string
	Messages []message
	Stream   bool
	Previous string
}

func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fmt.Errorf("unsupported ChatGPT web request field or shape: %w", e)
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
func parseRequest(protocol string, b []byte) (webRequest, error) {
	var out webRequest
	switch protocol {
	case "chat":
		var v struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		if e := decodeStrict(b, &v); e != nil {
			return out, e
		}
		out.Model, out.Stream = v.Model, v.Stream
		for _, m := range v.Messages {
			text, e := textContent(m.Content, false)
			if e != nil {
				return out, e
			}
			if m.Role != "user" && m.Role != "assistant" {
				return out, errors.New("ChatGPT web cannot faithfully implement system, developer or tool roles")
			}
			out.Messages = append(out.Messages, message{m.Role, text})
		}
	case "responses":
		var v struct {
			Model    string          `json:"model"`
			Input    json.RawMessage `json:"input"`
			Stream   bool            `json:"stream"`
			Previous string          `json:"previous_response_id"`
			Store    *bool           `json:"store"`
		}
		if e := decodeStrict(b, &v); e != nil {
			return out, e
		}
		if v.Store != nil {
			return out, errors.New("ChatGPT web retains conversations in the web account; Responses store cannot be enforced")
		}
		out.Model, out.Stream, out.Previous = v.Model, v.Stream, v.Previous
		var text string
		if json.Unmarshal(v.Input, &text) == nil {
			out.Messages = []message{{"user", text}}
		} else {
			var items []struct {
				Type    string          `json:"type,omitempty"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			}
			if e := decodeStrict(v.Input, &items); e != nil {
				return out, e
			}
			for _, item := range items {
				if item.Role != "user" || (item.Type != "" && item.Type != "message") {
					return out, errors.New("web Responses supports text user messages only")
				}
				text, e := textContent(item.Content, true)
				if e != nil {
					return out, e
				}
				out.Messages = append(out.Messages, message{"user", text})
			}
		}
	default:
		return out, errors.New("unsupported ChatGPT web protocol")
	}
	if len(out.Messages) == 0 || out.Messages[len(out.Messages)-1].Role != "user" || out.Messages[len(out.Messages)-1].Content == "" {
		return out, errors.New("a nonempty final user message is required")
	}
	return out, nil
}
func textContent(b []byte, responses bool) (string, error) {
	var text string
	if json.Unmarshal(b, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if e := decodeStrict(b, &parts); e != nil {
		return "", e
	}
	for _, p := range parts {
		if p.Type != "text" && !(responses && p.Type == "input_text") {
			return "", errors.New("ChatGPT web currently supports text content only")
		}
		text += p.Text
	}
	return text, nil
}
