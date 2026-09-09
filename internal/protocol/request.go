// Package protocol inspects routing fields without building a generic JSON tree.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

type Request struct {
	Model                string
	Stream               bool
	Tools                bool
	Stateful             bool
	Vision               bool
	ModelStart, ModelEnd int
}

func Inspect(b []byte) (Request, error) {
	r := Request{ModelStart: -1}
	if !json.Valid(b) {
		return r, errors.New("invalid JSON")
	}
	i := space(b, 0)
	if i >= len(b) || b[i] != '{' {
		return r, errors.New("request must be an object")
	}
	i++
	seenStream := false
	for {
		i = space(b, i)
		if b[i] == '}' {
			break
		}
		start := i
		i = stringEnd(b, i)
		key := routingKey(b[start:i])
		i = space(b, i)
		i = space(b, i+1)
		start = i
		i = valueEnd(b, i)
		switch key {
		case "model":
			if r.ModelStart >= 0 {
				return r, errors.New("duplicate model")
			}
			if e := json.Unmarshal(b[start:i], &r.Model); e != nil {
				return r, errors.New("model must be a string")
			}
			r.ModelStart = start
			r.ModelEnd = i
		case "stream":
			if seenStream {
				return r, errors.New("duplicate stream")
			}
			seenStream = true
			if e := json.Unmarshal(b[start:i], &r.Stream); e != nil {
				return r, errors.New("stream must be boolean")
			}
		case "tools", "functions":
			if string(b[start:i]) != "[]" && string(b[start:i]) != "null" {
				r.Tools = true
			}
		case "previous_response_id", "conversation":
			if string(b[start:i]) != "null" {
				r.Stateful = true
			}
		case "messages", "contents", "input":
			r.Vision = r.Vision || hasImage(b[start:i])
		}
		i = space(b, i)
		if b[i] == ',' {
			i++
		}
	}
	return r, nil
}

// The input was validated before scanning. Literal keys need no JSON decoder
// or temporary string allocation; escaped spellings retain JSON semantics.
func routingKey(raw []byte) string {
	switch string(raw) {
	case `"model"`:
		return "model"
	case `"stream"`:
		return "stream"
	case `"tools"`:
		return "tools"
	case `"functions"`:
		return "functions"
	case `"previous_response_id"`:
		return "previous_response_id"
	case `"conversation"`:
		return "conversation"
	case `"messages"`:
		return "messages"
	case `"contents"`:
		return "contents"
	case `"input"`:
		return "input"
	default:
		if bytes.IndexByte(raw, '\\') >= 0 {
			var key string
			_ = json.Unmarshal(raw, &key)
			return key
		}
		return ""
	}
}
func hasImage(b []byte) bool {
	if !bytes.Contains(b, []byte("image")) && !bytes.Contains(b, []byte("inlineData")) && !bytes.Contains(b, []byte("inline_data")) && !bytes.Contains(b, []byte(`\u`)) {
		return false
	}
	for i := 0; i < len(b); {
		if b[i] != '"' {
			i++
			continue
		}
		end := stringEnd(b, i)
		next := space(b, end)
		if next < len(b) && b[next] == ':' {
			key := imageMarker(b[i:end])
			if key == "image_url" || key == "inlineData" || key == "inline_data" {
				return true
			}
			if key == "type" {
				value := space(b, next+1)
				if value < len(b) && b[value] == '"' {
					kind := imageMarker(b[value:stringEnd(b, value)])
					if kind == "image" || kind == "image_url" || kind == "input_image" {
						return true
					}
				}
			}
		}
		i = end
	}
	return false
}

// Common markers avoid invoking a JSON decoder for every conversation field.
// Escaped spellings still use JSON string semantics.
func imageMarker(raw []byte) string {
	switch string(raw) {
	case `"type"`:
		return "type"
	case `"image"`:
		return "image"
	case `"input_image"`:
		return "input_image"
	case `"image_url"`:
		return "image_url"
	case `"inlineData"`:
		return "inlineData"
	case `"inline_data"`:
		return "inline_data"
	default:
		if bytes.IndexByte(raw, '\\') >= 0 {
			var decoded string
			_ = json.Unmarshal(raw, &decoded)
			return decoded
		}
		return ""
	}
}
func space(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\r' || b[i] == '\n' || b[i] == '\t') {
		i++
	}
	return i
}
func stringEnd(b []byte, i int) int {
	i++
	for i < len(b) {
		quote := bytes.IndexByte(b[i:], '"')
		if quote < 0 {
			return len(b)
		}
		end := i + quote
		backslashes := 0
		for j := end - 1; j >= i && b[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return end + 1
		}
		i = end + 1
	}
	return i
}
func valueEnd(b []byte, i int) int {
	if b[i] == '"' {
		return stringEnd(b, i)
	}
	depth := 0
	for i < len(b) {
		switch b[i] {
		case '"':
			i = stringEnd(b, i)
			continue
		case '[', '{':
			depth++
		case ']', '}':
			if depth == 0 {
				return i
			}
			depth--
			if depth == 0 {
				return i + 1
			}
		case ',':
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return i
}

// Rewrite preserves every byte outside the top-level model value, including
// unknown fields, tool schemas, numbers and escaped text.
func Rewrite(b []byte, r Request, model string) []byte {
	if r.ModelStart < 0 || r.Model == model {
		return b
	}
	out := make([]byte, 0, len(b)+len(model)+8)
	out = append(out, b[:r.ModelStart]...)
	out = strconv.AppendQuote(out, model)
	out = append(out, b[r.ModelEnd:]...)
	return out
}
