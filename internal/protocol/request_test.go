package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectRewrite(t *testing.T) {
	b := []byte(` {"messages":[{"content":"hello \\\" model"}],"tools":[{"function":{"parameters":{"model":"keep"}}}],"model" : "auto/silver", "stream":true,"unknown":123456789012345678901} `)
	r, e := Inspect(b)
	if e != nil || r.Model != "auto/silver" || !r.Stream || !r.Tools {
		t.Fatalf("%+v %v", r, e)
	}
	out := Rewrite(b, r, "model/real")
	if !json.Valid(out) || !strings.Contains(string(out), `"parameters":{"model":"keep"}`) || !strings.Contains(string(out), "123456789012345678901") {
		t.Fatal(string(out))
	}
	var v struct{ Model string }
	_ = json.Unmarshal(out, &v)
	if v.Model != "model/real" {
		t.Fatal(v)
	}
}

func TestInspectOutputTokenCeilings(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int64
	}{
		{"openai", `{"model":"m","max_tokens":321}`, 321},
		{"responses", `{"model":"m","max_output_tokens":654}`, 654},
		{"anthropic", `{"model":"m","max_completion_tokens":987}`, 987},
		{"gemini", `{"contents":[],"generationConfig":{"maxOutputTokens":123}}`, 123},
		{"gemini-snake", `{"contents":[],"generation_config":{"max_output_tokens":456}}`, 456},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Inspect([]byte(tc.body))
			if err != nil || got.MaxOutputTokens != tc.want {
				t.Fatalf("metadata=%+v err=%v", got, err)
			}
		})
	}
	for _, body := range []string{`{"model":"m","max_tokens":0}`, `{"model":"m","max_tokens":-1}`, `{"model":"m","max_tokens":"10"}`} {
		got, err := Inspect([]byte(body))
		if err != nil || got.MaxOutputTokens != 0 {
			t.Fatalf("invalid output ceiling accepted: %+v %v", got, err)
		}
	}
	got, err := Inspect([]byte(`{"model":"m","max_tokens":1000,"max_output_tokens":10}`))
	if err != nil || got.MaxOutputTokens != 1000 {
		t.Fatalf("multiple ceilings did not retain conservative upper bound: %+v %v", got, err)
	}
}
func TestRejectAmbiguousJSON(t *testing.T) {
	for _, v := range []string{`[]`, `{"model":"a","mo\u0064el":"b"}`, `{"stream":true,"stream":false}`, `{"model":3}`, `{"stream":"true"}`, `{`} {
		if _, e := Inspect([]byte(v)); e == nil {
			t.Fatal(v)
		}
	}
}
func FuzzInspect(f *testing.F) {
	for _, s := range []string{`{"model":"auto"}`, ` {"model":"a","x":[1,{"z":"ab\\\""}]}`, `[]`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		r, e := Inspect(b)
		if e == nil {
			out := Rewrite(b, r, "new-model")
			if !json.Valid(out) {
				t.Fatalf("invalid rewrite %q", out)
			}
		}
	})
}
func BenchmarkInspect(b *testing.B) {
	v := []byte(`{"model":"auto/silver","messages":[{"role":"user","content":"` + strings.Repeat("x", 8192) + `"}],"stream":true}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(v)))
	for b.Loop() {
		_, _ = Inspect(v)
	}
}
