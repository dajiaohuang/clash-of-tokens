package device

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIBoundaries(t *testing.T) {
	good := []byte(`<hierarchy><node resource-id="app:id/input" text="你好 &amp; world" class="android.widget.EditText" enabled="true" focused="true" bounds="[10,20][30,40]"/></hierarchy>`)
	nodes, e := ParseUI(good)
	if e != nil || len(nodes) != 1 || nodes[0].Text != "你好 & world" || !nodes[0].Focused {
		t.Fatal(nodes, e)
	}
	x, y, e := nodes[0].Center()
	if e != nil || x != 20 || y != 30 {
		t.Fatal(x, y, e)
	}
	for _, wire := range []string{`<node/>`, `<hierarchy>`, `<hierarchy/><hierarchy/>`, strings.Repeat(`<hierarchy>`, 130) + strings.Repeat(`</hierarchy>`, 130)} {
		if _, e := ParseUI([]byte(wire)); e == nil {
			t.Fatal("accepted invalid tree")
		}
	}
	for _, bounds := range []string{"[0,0][0,1]", "[50,0][10,1]", "[-1,0][10,20]", "[0,0][20000,20000]", "[0,0][10,10] extra"} {
		if _, _, e := (Node{Bounds: bounds}).Center(); e == nil {
			t.Fatal(bounds)
		}
	}
}
func TestLeaseCancellationAndCrashReview(t *testing.T) {
	dir := t.TempDir()
	l, e := Acquire(dir, "device-one")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Acquire(dir, "device-two"); e == nil {
		t.Fatal("shared clipboard acquired twice")
	}
	l.MarkDirty()
	l.Close()
	if _, e = Acquire(dir, "device-one"); e == nil {
		t.Fatal("uncertain operation replayed")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.lock"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	// Simulates an operator explicitly reviewing and clearing a stopped lease.
	if e = os.Remove(files[0]); e != nil {
		t.Fatal(e)
	}
	l, e = Acquire(dir, "device-one")
	if e != nil {
		t.Fatal(e)
	}
	l.MarkDirty()
	l.Complete()
	l.Close()
	files, _ = filepath.Glob(filepath.Join(dir, "*.lock"))
	if len(files) != 0 {
		t.Fatal(files)
	}
}
func TestOCRTSVAndOverlappingRows(t *testing.T) {
	tsv := "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\t1\t1\t1\t1\t1\t2\t30\t10\t95\t你好\n5\t1\t1\t1\t1\t2\t35\t2\t30\t10\t90\t世界\n"
	lines, e := ParseTSV([]byte(tsv), image.Pt(100, 200))
	if e != nil || len(lines) != 1 || lines[0].Box.Min != image.Pt(101, 202) || lines[0].Text != "你好 世界" {
		t.Fatal(lines, e)
	}
	merged := MergeRows([]Line{{"hello world", image.Rect(0, 0, 100, 20), .9}, {"world again", image.Rect(60, 1, 160, 21), .8}}, 5)
	if len(merged) != 1 || merged[0].Text != "hello world again" {
		t.Fatal(merged)
	}
	if _, e = ParseTSV([]byte("garbage"), image.Point{}); e == nil {
		t.Fatal("accepted non TSV")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = Pause(ctx, 1000000000); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
