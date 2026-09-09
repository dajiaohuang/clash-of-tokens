// Package device provides bounded, context-aware Android commands for native adapters.
package device

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"image"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

type Node struct {
	Resource    string `xml:"resource-id,attr"`
	Text        string `xml:"text,attr"`
	Description string `xml:"content-desc,attr"`
	Class       string `xml:"class,attr"`
	Package     string `xml:"package,attr"`
	Bounds      string `xml:"bounds,attr"`
	Clickable   bool   `xml:"clickable,attr"`
	Enabled     bool   `xml:"enabled,attr"`
	Focused     bool   `xml:"focused,attr"`
}

var boundsRE = regexp.MustCompile(`^\[(\d{1,5}),(\d{1,5})\]\[(\d{1,5}),(\d{1,5})\]$`)

func (n Node) Center() (int, int, error) {
	r, e := n.Rectangle()
	if e != nil {
		return 0, 0, e
	}
	return r.Min.X + r.Dx()/2, r.Min.Y + r.Dy()/2, nil
}
func (n Node) Rectangle() (image.Rectangle, error) {
	m := boundsRE.FindStringSubmatch(n.Bounds)
	if m == nil {
		return image.Rectangle{}, errors.New("device: invalid control bounds")
	}
	v := [4]int{}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	if v[2] <= v[0] || v[3] <= v[1] || v[2] > 16384 || v[3] > 16384 {
		return image.Rectangle{}, errors.New("device: empty or excessive bounds")
	}
	return image.Rect(v[0], v[1], v[2], v[3]), nil
}
func ParseUI(data []byte) ([]Node, error) {
	if len(data) > 4<<20 {
		return nil, errors.New("device: UI tree too large")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	var nodes []Node
	depth := 0
	root := false
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, errors.New("device: invalid UI XML")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				if root || t.Name.Local != "hierarchy" {
					return nil, errors.New("device: expected one hierarchy")
				}
				root = true
			}
			depth++
			if depth > 128 {
				return nil, errors.New("device: excessive XML depth")
			}
			if t.Name.Local == "node" {
				var n Node
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "resource-id":
						n.Resource = a.Value
					case "text":
						n.Text = a.Value
					case "content-desc":
						n.Description = a.Value
					case "class":
						n.Class = a.Value
					case "package":
						n.Package = a.Value
					case "bounds":
						n.Bounds = a.Value
					case "clickable":
						n.Clickable = a.Value == "true"
					case "enabled":
						n.Enabled = a.Value == "true"
					case "focused":
						n.Focused = a.Value == "true"
					}
				}
				nodes = append(nodes, n)
				if len(nodes) > 20000 {
					return nil, errors.New("device: excessive node count")
				}
			}
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) != 0 {
				return nil, errors.New("device: text outside UI hierarchy")
			}
		}
	}
	if !root || depth != 0 {
		return nil, errors.New("device: incomplete UI tree")
	}
	return nodes, nil
}

type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}
type ADB struct{ Path, Serial string }
type boundedBuffer struct {
	b     bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.b.Len() {
		return 0, errors.New("device: command output limit exceeded")
	}
	return b.b.Write(p)
}
func (a ADB) Run(ctx context.Context, args ...string) ([]byte, error) {
	if a.Path == "" || a.Serial == "" {
		return nil, errors.New("device: explicit ADB path and serial required")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.Path, append([]string{"-s", a.Serial}, args...)...)
	hideCommand(cmd)
	stdout := &boundedBuffer{limit: 4 << 20}
	stderr := &boundedBuffer{limit: 64 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second
	if e := cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("device: ADB command failed")
	}
	return stdout.b.Bytes(), nil
}
func Dump(ctx context.Context, r Runner) ([]Node, error) {
	const path = "/sdcard/clash-tokens-window.xml"
	if _, e := r.Run(ctx, "shell", "uiautomator", "dump", "--compressed", path); e != nil {
		return nil, e
	}
	data, e := r.Run(ctx, "exec-out", "cat", path)
	if e != nil {
		return nil, e
	}
	return ParseUI(data)
}
func Tap(ctx context.Context, r Runner, n Node) error {
	x, y, e := n.Center()
	if e != nil {
		return e
	}
	_, e = r.Run(ctx, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	return e
}
func Pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
