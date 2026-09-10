package config

import (
	"reflect"
	"strings"
)

type FieldSchema struct {
	Name            string        `json:"name"`
	Type            string        `json:"type"`
	Nullable        bool          `json:"nullable,omitempty"`
	Enum            []string      `json:"enum,omitempty"`
	Fields          []FieldSchema `json:"fields,omitempty"`
	Item            *FieldSchema  `json:"item,omitempty"`
	RestartRequired bool          `json:"restart_required,omitempty"`
}

func Schema() []FieldSchema {
	fields := schemaFields(reflect.TypeOf(Config{}))
	for i := range fields {
		switch fields[i].Name {
		case "listen", "api_key_env", "admin_key_env", "runtime", "browser", "device":
			fields[i].RestartRequired = true
		}
	}
	return fields
}
func schemaFields(t reflect.Type) []FieldSchema {
	out := []FieldSchema{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		s := schemaType(f.Type)
		s.Name = name
		enums := map[string][]string{
			"engine":             {"chrome", "edge", "chromium"},
			"source_kind":        {"vendor_api", "cloud_api", "aggregator_api", "product_reverse", "browser_reverse", "app_reverse", "cli_reverse", "local_model", "custom_api"},
			"execution_location": {"local", "remote"}, "inference_location": {"local", "remote", "unknown"},
			"billing_mode":    {"metered", "subscription", "free_allowance", "local", "unknown"},
			"credential_mode": {"api_key", "oauth", "browser_session", "cookie", "username_password", "cli_session", "device_session", "anonymous"},
			"tier":            {"unrated", "bronze", "silver", "gold", "platinum", "diamond"}, "min_tier": {"bronze", "silver", "gold", "platinum", "diamond"},
			"tools": {"none", "native", "unknown"}, "pool_strategy": {"round-robin", "least-load", "sticky", "weighted"},
			"type": {"auto", "select", "fallback", "latency", "load-balance", "weighted"},
		}
		s.Enum = enums[name]
		if name == "protocols" && s.Item != nil {
			s.Item.Enum = []string{"chat", "responses", "messages", "gemini"}
		}
		out = append(out, s)
	}
	return out
}
func schemaType(t reflect.Type) FieldSchema {
	s := FieldSchema{}
	if t.Kind() == reflect.Pointer {
		s.Nullable = true
		t = t.Elem()
	}
	if t.PkgPath() == "time" && t.Name() == "Time" {
		s.Type = "datetime"
		return s
	}
	switch t.Kind() {
	case reflect.Struct:
		s.Type = "object"
		s.Fields = schemaFields(t)
	case reflect.Slice:
		s.Type = "array"
		item := schemaType(t.Elem())
		s.Item = &item
	case reflect.Bool:
		s.Type = "boolean"
	case reflect.Int, reflect.Int64, reflect.Uint64:
		s.Type = "integer"
	default:
		s.Type = "string"
	}
	return s
}
