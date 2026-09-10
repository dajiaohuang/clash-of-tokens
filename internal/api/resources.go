package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/routing"
)

func managedResource(path string) bool {
	for _, prefix := range []string{"/admin/providers", "/admin/accounts", "/admin/sources", "/admin/groups", "/admin/browser_profiles"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return path == "/admin/routing/simulate"
}

func (p *ControlPlane) resourceAdmin(w http.ResponseWriter, r *http.Request, server *Server) {
	if r.URL.Path == "/admin/routing/simulate" {
		if r.Method != "POST" {
			fail(w, 405, "method not allowed")
			return
		}
		var q routing.Query
		if err := decodeInput(w, r, &q, 4096); err != nil || q.Bytes < 0 {
			fail(w, 400, "invalid routing query")
			return
		}
		reply(w, server.Router.Explain(q))
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/"), "/")
	if len(parts) > 3 {
		fail(w, 404, "unknown resource path")
		return
	}
	kind := parts[0]
	id := ""
	if len(parts) > 1 {
		id = parts[1]
	}
	current := p.service.Current()
	c := current.Config
	var items any
	switch kind {
	case "browser_profiles":
		items = c.BrowserProfiles
	case "providers":
		items = c.Providers
	case "accounts":
		items = c.Accounts
	case "sources":
		items = c.Sources
	case "groups":
		items = c.Groups
	}
	if r.Method == "GET" {
		if len(parts) > 2 {
			fail(w, 404, "unknown resource path")
			return
		}
		if id == "" {
			reply(w, map[string]any{"revision": current.Revision, "items": items})
			return
		}
		list := reflect.ValueOf(items)
		for i := 0; i < list.Len(); i++ {
			if list.Index(i).FieldByName("ID").String() == id {
				reply(w, map[string]any{"revision": current.Revision, "item": list.Index(i).Interface()})
				return
			}
		}
		fail(w, 404, "unknown resource")
		return
	}
	expected := current.Revision
	match := strings.Trim(r.Header.Get("If-Match"), "\"")
	if match != "" {
		var err error
		expected, err = strconv.ParseUint(match, 10, 64)
		if err != nil {
			fail(w, 400, "invalid revision")
			return
		}
	} else if r.Method != "POST" {
		fail(w, 428, "If-Match configuration revision required")
		return
	}
	if expected != current.Revision {
		fail(w, 409, config.ErrRevisionConflict.Error())
		return
	}
	method := r.Method
	var raw json.RawMessage
	if len(parts) == 3 && (parts[2] == "enable" || parts[2] == "disable") && method == "POST" {
		raw = json.RawMessage(fmt.Sprintf("{\"enabled\":%t}", parts[2] == "enable"))
		method = "PATCH"
	} else {
		if len(parts) == 3 {
			fail(w, 404, "unknown resource action")
			return
		}
		if method != "DELETE" {
			if err := decodeInput(w, r, &raw, 4<<20); err != nil {
				fail(w, 400, "invalid resource input")
				return
			}
		}
		// Preserve the original dashboard's enabled action, now durably applied.
		if method == "POST" && id != "" {
			method = "PATCH"
		}
	}
	var err error
	switch kind {
	case "browser_profiles":
		c.BrowserProfiles, err = editResource(c.BrowserProfiles, id, method, raw)
	case "providers":
		if method == "DELETE" {
			for _, s := range c.Sources {
				if s.Provider == id {
					fail(w, 409, "provider still has sources")
					return
				}
			}
		}
		c.Providers, err = editResource(c.Providers, id, method, raw)
	case "accounts":
		c.Accounts, err = editResource(c.Accounts, id, method, raw)
	case "sources":
		c.Sources, err = editResource(c.Sources, id, method, raw)
	case "groups":
		c.Groups, err = editResource(c.Groups, id, method, raw)
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	updated, err := p.service.Apply(expected, c, method+" "+kind+"/"+id)
	if err != nil {
		status := 400
		if errors.Is(err, config.ErrRevisionConflict) {
			status = 409
		}
		fail(w, status, err.Error())
		return
	}
	reply(w, map[string]any{"revision": updated.Revision, "status": "saved", "persistence": "durable", "restart_required": restartFields(p.startup, updated.Config)})
}

func decodeInput(w http.ResponseWriter, r *http.Request, out any, limit int64) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("one JSON object required")
	}
	return nil
}

func editResource[T any](items []T, id, method string, raw json.RawMessage) ([]T, error) {
	index := -1
	for i, v := range items {
		if reflect.ValueOf(v).FieldByName("ID").String() == id {
			index = i
			break
		}
	}
	if method == "POST" && id == "" {
		var value T
		if err := strictResource(raw, &value); err != nil {
			return nil, err
		}
		return append(items, value), nil
	}
	if index < 0 {
		return nil, errors.New("unknown resource")
	}
	if method == "DELETE" {
		return append(items[:index], items[index+1:]...), nil
	}
	if method != "PATCH" && method != "PUT" {
		return nil, errors.New("unsupported resource method")
	}
	var value T
	if method == "PATCH" {
		b, _ := json.Marshal(items[index])
		var existing, patch map[string]json.RawMessage
		_ = json.Unmarshal(b, &existing)
		if json.Unmarshal(raw, &patch) != nil || patch == nil {
			return nil, errors.New("resource patch must be an object")
		}
		for k, v := range patch {
			existing[k] = v
		}
		raw, _ = json.Marshal(existing)
	}
	if err := strictResource(raw, &value); err != nil {
		return nil, err
	}
	if reflect.ValueOf(value).FieldByName("ID").String() != id {
		return nil, errors.New("resource id cannot change")
	}
	items[index] = value
	return items, nil
}
func strictResource(raw []byte, out any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return errors.New("resource must be an object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return errors.New("invalid resource fields")
	}
	return nil
}
