package protocol

import (
	"bytes"
	"fmt"
	"testing"
)

var performanceBytes []byte

func BenchmarkInspectImageDiscussion(b *testing.B) {
	item := []byte(`{"role":"user","content":"Explain image rendering and caching."},`)
	body := append([]byte(`{"model":"x","messages":[`), bytes.Repeat(item, 100)...)
	body[len(body)-1] = ']'
	body = append(body, '}')
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Inspect(body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSSEData(b *testing.B) {
	for _, lines := range []int{1, 100, 1000} {
		b.Run(fmt.Sprint(lines), func(b *testing.B) {
			frame := append(bytes.Repeat([]byte("data: abcdefghijklmnopqrstuvwxyz\n"), lines), '\n')
			b.ReportAllocs()
			b.SetBytes(int64(len(frame)))
			for b.Loop() {
				performanceBytes = SSEData(frame)
			}
		})
	}
}

func TestMultilineSSEInputUnchanged(t *testing.T) {
	frame := []byte("data:\r\ndata: first\r\n: comment\ndata:\ndata: last\n\n")
	copyFrame := bytes.Clone(frame)
	if got := SSEData(frame); string(got) != "\nfirst\n\nlast" {
		t.Fatalf("%q", got)
	}
	if !bytes.Equal(frame, copyFrame) {
		t.Fatal("input was mutated")
	}
}

func TestEscapedRoutingKeysRemainEquivalent(t *testing.T) {
	r, err := Inspect([]byte(`{"mo\u0064el":"x","st\u0072eam":true,"previ\u006fus_response_id":"p","to\u006fls":[{}]}`))
	if err != nil || r.Model != "x" || !r.Stream || !r.Stateful || !r.Tools {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = Inspect([]byte(`{"model":"x","mo\u0064el":"y"}`)); err == nil {
		t.Fatal("escaped duplicate accepted")
	}
}

func TestImageMarkersAndTextRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		content string
		vision  bool
	}{
		{`[{"role":"user","content":"Explain image rendering"}]`, false},
		{`[{"type":"image_url","image_url":{"url":"x"}}]`, true},
		{`[{"t\u0079pe":"\u0069\u006d\u0061\u0067\u0065"}]`, true},
		{`[{"\u0069mage_url":"x"}]`, true},
		{`[{"inlineData":{"data":"x"}}]`, true},
		{`[{"content":"\\\"image_url\\\": literal text"}]`, false},
	} {
		r, err := Inspect([]byte(`{"model":"x","messages":` + tc.content + `}`))
		if err != nil || r.Vision != tc.vision {
			t.Fatalf("%s: %+v %v", tc.content, r, err)
		}
	}
}
