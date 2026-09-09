package businessweb

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestMakerSuiteDocumentedContentThenFinish(t *testing.T) {
	// Exact independent frames from pinned AIStudio2API docs/protocol.md.
	text := `[[[[[[null,"42"]],"model"]]]]`
	finish := `[[[null,1]],null,[27,1,28,null,null,null,null,0,null,0],null,null,null,null,"response_01"]`
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{"[[" + text + "," + finish + "]]", true},
		{"[[" + text + "]]", false},
		{"[[" + finish + "," + text + "]]", false},
		{"[[" + text + `,[[[null,0]]]]]`, false},
		{"[[" + text + `,[[[null,3]]]]]`, false},
	} {
		got, err := parseMakerSuiteTextStrict([]byte(tc.raw))
		if tc.valid && (err != nil || got != "42") {
			t.Fatalf("%s: %q %v", tc.raw, got, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("accepted incomplete/abnormal wire: %s", tc.raw)
		}
	}
}

func TestGeminiBusinessLengthFramingWithSeparators(t *testing.T) {
	inner, _ := json.Marshal([]any{nil, nil, nil, nil, []any{[]any{nil, []any{"hello"}}}})
	frame, _ := json.Marshal([]any{[]any{"wrb.fr", nil, string(inner)}})
	raw := fmt.Sprintf(")]}'\n\n%d\n%s\n\n", len(frame), frame)
	got, err := parseGeminiBusinessResponseStrict([]byte(raw))
	if err != nil || got != "hello" {
		t.Fatalf("%q %v", got, err)
	}
}
