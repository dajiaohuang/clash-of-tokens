package appdevice

import (
	"clash-of-tokens/internal/drivers/device"
	"context"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"
)

func xhValue(n device.Node) string {
	if n.Description != "" {
		return n.Description
	}
	return n.Text
}
func xhInput(nodes []device.Node) (device.Node, bool) {
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		if n.Class == "android.widget.EditText" && strings.HasSuffix(n.Resource, ":id/msg_et") && n.Enabled {
			return n, true
		}
	}
	return device.Node{}, false
}
func xhCount(nodes []device.Node, q string) (count, last int) {
	last = -1
	for i, n := range nodes {
		if n.Class != "android.widget.EditText" && normalized(xhValue(n)) == normalized(q) {
			count++
			last = i
		}
	}
	return
}
func xhResponse(nodes []device.Node, q string, oldCount int) (string, *device.Node) {
	count, start := xhCount(nodes, q)
	if start < 0 || count <= oldCount {
		return "", nil
	}
	var card *device.Node
	var values []string
	seen := map[string]bool{}
	for i := start + 1; i < len(nodes); i++ {
		n := nodes[i]
		if strings.HasSuffix(n.Resource, ":id/gen") && n.Clickable && n.Enabled {
			v := n
			card = &v
		}
		if strings.HasSuffix(n.Resource, ":id/kd8") {
			bounds, e := n.Rectangle()
			if e != nil {
				continue
			}
			if bounds.Min.X > 220 {
				continue
			}
			v := strings.TrimSpace(xhValue(n))
			if v != "" && !seen[v] {
				seen[v] = true
				values = append(values, v)
			}
		}
	}
	return strings.Join(values, "\n"), card
}
func (f *flow) xhGuard(ctx context.Context) error {
	return f.guard(ctx, image.Rect(0, 2800, 1440, 3200), false, "发消息或按住说话", "发消息")
}
func (f *flow) xhInputQuestion(ctx context.Context, q string) (string, error) {
	if e := f.xhGuard(ctx); e != nil {
		return "", e
	}
	nodes, e := f.nodes(ctx)
	if e != nil {
		return "", e
	}
	input, ok := xhInput(nodes)
	if !ok {
		return "", errors.New("app-device: Xiaohuoren input control missing")
	}
	if e = f.tap(ctx, input); e != nil {
		return "", e
	}
	if input.Text != "" {
		if e = f.clear(ctx); e != nil {
			return "", e
		}
	}
	if _, e = f.r.Run(ctx, "shell", "input", "text", "@"); e != nil {
		return "", e
	}
	if e = f.pause(ctx, 800*time.Millisecond); e != nil {
		return "", e
	}
	nodes, e = f.nodes(ctx)
	if e != nil {
		return "", e
	}
	var candidate *device.Node
	for _, n := range nodes {
		bounds, err := n.Rectangle()
		if err == nil && bounds.Min.Y >= 2300 && xhValue(n) == "小火人" && strings.HasSuffix(n.Resource, ":id/jmn") {
			v := n
			candidate = &v
			break
		}
	}
	if candidate == nil {
		return "", errors.New("app-device: real Xiaohuoren mention candidate missing")
	}
	if e = f.tap(ctx, *candidate); e != nil {
		return "", e
	}
	if e = f.pause(ctx, 400*time.Millisecond); e != nil {
		return "", e
	}
	nodes, e = f.nodes(ctx)
	if e != nil {
		return "", e
	}
	input, ok = xhInput(nodes)
	if !ok || !strings.Contains(input.Text, "小火人") {
		return "", errors.New("app-device: mention was not formed")
	}
	mention := input.Text
	if e = f.paste(ctx, q); e != nil {
		return "", e
	}
	nodes, e = f.nodes(ctx)
	if e != nil {
		return "", e
	}
	input, ok = xhInput(nodes)
	if !ok || input.Text != mention+q {
		return "", errors.New("app-device: mention or question input mismatch")
	}
	return input.Text, nil
}
func (f *flow) detailOpen(ctx context.Context) (bool, error) {
	data, e := f.r.Run(ctx, "shell", "dumpsys", "window", "windows")
	if e != nil {
		return false, e
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.Contains(l, "mCurrentFocus=") && strings.Contains(l, f.p.Package+"/") && strings.Contains(l, "AnnieXHostActivity") {
			return true, nil
		}
	}
	return false, nil
}
func (f *flow) xhCard(ctx context.Context, card device.Node) (string, error) {
	if e := f.tap(ctx, card); e != nil {
		return "", e
	}
	opened := false
	for i := 0; i < 25; i++ {
		ok, e := f.detailOpen(ctx)
		if e != nil {
			return "", e
		}
		if ok {
			opened = true
			break
		}
		if e = f.pause(ctx, 200*time.Millisecond); e != nil {
			return "", e
		}
	}
	if !opened {
		return "", errors.New("app-device: card detail did not open; navigation stopped")
	}
	if e := f.pause(ctx, 3*time.Second); e != nil {
		return "", e
	}
	for attempt := 0; attempt <= 10; attempt++ {
		ok, e := f.detailOpen(ctx)
		if e != nil {
			return "", e
		}
		if !ok {
			return "", errors.New("app-device: card detail focus changed")
		}
		lines, e := f.lines(ctx, image.Rectangle{})
		if e != nil {
			return "", e
		}
		for _, l := range lines {
			if compact(l.Text) != "复制" {
				continue
			}
			x, y := l.Box.Min.X+l.Box.Dx()/2, l.Box.Min.Y+l.Box.Dy()/2-120
			if x < 0 || y < 0 {
				return "", errors.New("app-device: invalid calibrated card copy position")
			}
			answer, e := f.copyAt(ctx, device.Node{Bounds: fmt.Sprintf("[%d,%d][%d,%d]", x, y, x+1, y+1)}, 20)
			if e != nil {
				return "", e
			}
			if ok, e = f.detailOpen(ctx); e != nil || !ok {
				return "", errors.New("app-device: card changed before return")
			}
			if e = f.key(ctx, "KEYCODE_BACK"); e != nil {
				return "", e
			}
			if e = f.pause(ctx, 800*time.Millisecond); e != nil {
				return "", e
			}
			if ok, e = f.detailOpen(ctx); e != nil || ok {
				return "", errors.New("app-device: card copied but conversation not restored")
			}
			if e = f.xhGuard(ctx); e != nil {
				return "", e
			}
			return answer, nil
		}
		if attempt == 10 {
			break
		}
		if _, e = f.r.Run(ctx, "shell", "input", "swipe", "720", "2700", "720", "550", "500"); e != nil {
			return "", e
		}
		if e = f.pause(ctx, 800*time.Millisecond); e != nil {
			return "", e
		}
	}
	return "", errors.New("app-device: complete card copy unavailable; card left open for inspection")
}
func (f *flow) xiaohuoren(ctx context.Context, q string) (string, string, error) {
	if e := f.xhGuard(ctx); e != nil {
		return "", "", e
	}
	baseline, e := f.nodes(ctx)
	if e != nil {
		return "", "", e
	}
	wire, e := f.xhInputQuestion(ctx, q)
	if e != nil {
		return "", "", e
	}
	oldCount, _ := xhCount(baseline, wire)
	nodes, e := f.nodes(ctx)
	if e != nil {
		return "", "", e
	}
	var send *device.Node
	for _, n := range nodes {
		if xhValue(n) == "发送" && n.Clickable && n.Enabled && n.Package == f.p.Package {
			v := n
			send = &v
			break
		}
	}
	if send == nil {
		return "", "", errors.New("app-device: exact Xiaohuoren send control missing")
	}
	if e = f.tap(ctx, *send); e != nil {
		return "", "", e
	}
	if e = f.pause(ctx, 12*time.Second); e != nil {
		return "", "", e
	}
	responseCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ctx = responseCtx
	previous := ""
	stable := 0
	for i := 0; i < 120; i++ {
		if e = f.foreground(ctx); e != nil {
			return "", "", e
		}
		nodes, e = f.nodes(ctx)
		if e != nil {
			return "", "", e
		}
		text, card := xhResponse(nodes, wire, oldCount)
		if card != nil {
			answer, e := f.xhCard(ctx, *card)
			return answer, "clipboard", e
		}
		if text != "" && text == previous {
			stable++
		} else {
			stable = 1
			previous = text
		}
		if text != "" && stable >= 2 {
			return text, "control-stable", nil
		}
		if e = f.pause(ctx, time.Second); e != nil {
			return "", "", e
		}
	}
	return "", "", errors.New("app-device: Xiaohuoren new reply not observed")
}
