package credentials

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSelectedExportParsingAndRedaction(t *testing.T) {
	for _, format := range []string{"csv", "json"} {
		data := `[{"name":"Personal","url":"https://chatgpt.com/login","username":"private-user","password":"private-pass"}]`
		if format == "csv" {
			data = "name,url,username,password\r\nPersonal,https://chatgpt.com/login,private-user,private-pass\r\n"
		}
		entries, err := ParseImport(format, []byte(data))
		if err != nil {
			t.Fatal(err)
		}
		preview, _ := json.Marshal(PreviewImport(entries))
		if strings.Contains(string(preview), "private-") || !strings.Contains(string(preview), "chatgpt.com") {
			t.Fatal("unsafe preview", string(preview))
		}
	}
	for _, data := range []string{"url,username,password\na,b,c", "url,username,password,password\nhttps://example.com,u,p,p", "url,username,password\nhttps://example.com,u,"} {
		if _, err := ParseImport("csv", []byte(data)); err == nil {
			t.Fatal("accepted invalid export")
		}
	}
}

func TestImportSelectionAtomicFailure(t *testing.T) {
	s := &Store{records: map[string]record{}, protect: func([]byte) ([]byte, error) { return nil, errors.New("locked") }}
	entries := []ImportEntry{{URL: "https://example.com", Username: "u", Password: "secret"}}
	for _, selected := range [][]int{{0, 0}, {1}, {0}} {
		if _, err := s.ImportSelected(entries, selected); err == nil {
			t.Fatal("expected failure")
		}
		if len(s.List()) != 0 {
			t.Fatal("failed batch changed vault")
		}
	}
}
