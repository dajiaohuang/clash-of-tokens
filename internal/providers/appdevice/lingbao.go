package appdevice

import (
	"clash-of-tokens/internal/drivers/device"
	"context"
	"errors"
	"image"
	"strings"
	"time"
)

func (f *flow) lingbaoGuard(ctx context.Context) error {
	return f.guard(ctx, image.Rectangle{}, true, "灵宝", "发送")
}
func (f *flow) hideIME(ctx context.Context) error {
	for i := 0; i < 4; i++ {
		lines, e := f.lines(ctx, image.Rectangle{})
		if e != nil {
			return e
		}
		found, hidden := false, false
		for _, l := range lines {
			if compact(l.Text) == "发送" {
				found = true
				if l.Box.Min.Y >= 1100 {
					hidden = true
				}
			}
		}
		if found && hidden {
			return nil
		}
		if e = f.key(ctx, "KEYCODE_ESCAPE"); e != nil {
			return e
		}
		if e = f.pause(ctx, 500*time.Millisecond); e != nil {
			return e
		}
	}
	return errors.New("app-device: Lingbao send button position does not confirm keyboard is hidden")
}
func edit(nodes []device.Node) (device.Node, bool) {
	for _, n := range nodes {
		if n.Class == "android.widget.EditText" && n.Enabled && n.Focused {
			return n, true
		}
	}
	return device.Node{}, false
}
func (f *flow) lingbaoInput(ctx context.Context, q string) error {
	if e := f.lingbaoGuard(ctx); e != nil {
		return e
	}
	if e := f.point(ctx, 1500, 1330); e != nil {
		return e
	}
	for i := 0; i < 20; i++ {
		nodes, e := f.nodes(ctx)
		if e != nil {
			return e
		}
		if n, ok := edit(nodes); ok {
			if n.Text != q {
				if e = f.clear(ctx); e != nil {
					return e
				}
				if e = f.paste(ctx, q); e != nil {
					return e
				}
			}
			for j := 0; j < 16; j++ {
				nodes, e = f.nodes(ctx)
				if e != nil {
					return e
				}
				if n, ok = edit(nodes); ok && n.Text == q {
					return nil
				}
				if e = f.pause(ctx, 250*time.Millisecond); e != nil {
					return e
				}
			}
			return errors.New("app-device: Lingbao EditText does not contain exact question")
		}
		if e = f.pause(ctx, 250*time.Millisecond); e != nil {
			return e
		}
	}
	// The configured source fallback uses the actual context-menu Paste action.
	return f.lingbaoContextPaste(ctx, q)
}
func (f *flow) lingbaoContextPaste(ctx context.Context, q string) (err error) {
	if e := f.hideIME(ctx); e != nil {
		return e
	}
	if e := f.lingbaoGuard(ctx); e != nil {
		return e
	}
	old, e := f.readClip()
	if e != nil {
		return e
	}
	if e = f.writeClip(q); e != nil {
		return e
	}
	defer func() {
		if e := f.writeClip(old); e != nil && err == nil {
			err = e
		}
	}()
	if e = f.pause(ctx, f.syncDelay); e != nil {
		return e
	}
	f.dirty()
	if _, e = f.r.Run(ctx, "shell", "input", "swipe", "1500", "1330", "1500", "1330", "700"); e != nil {
		return e
	}
	lines, e := f.lines(ctx, image.Rect(0, 900, 1400, 1300))
	if e != nil {
		return e
	}
	for _, l := range lines {
		label := strings.ToLower(compact(l.Text))
		if label == "全选" || label == "selectall" {
			if e = f.point(ctx, l.Box.Min.X+l.Box.Dx()/2, l.Box.Min.Y+l.Box.Dy()/2); e != nil {
				return e
			}
			if e = f.key(ctx, "KEYCODE_DEL"); e != nil {
				return e
			}
			if _, e = f.r.Run(ctx, "shell", "input", "swipe", "1500", "1330", "1500", "1330", "700"); e != nil {
				return e
			}
			lines, e = f.lines(ctx, image.Rect(0, 900, 1400, 1300))
			if e != nil {
				return e
			}
			break
		}
	}
	found := false
	for _, l := range lines {
		label := strings.ToLower(compact(l.Text))
		if label == "粘贴" || label == "paste" {
			if e = f.point(ctx, l.Box.Min.X+l.Box.Dx()/2, l.Box.Min.Y+l.Box.Dy()/2); e != nil {
				return e
			}
			found = true
			break
		}
	}
	if !found {
		return errors.New("app-device: Lingbao paste menu action not observed")
	}
	if e = f.pause(ctx, 500*time.Millisecond); e != nil {
		return e
	}
	lines, e = f.lines(ctx, image.Rect(100, 1220, 2760, 1410))
	if e != nil {
		return e
	}
	var text strings.Builder
	for _, l := range lines {
		text.WriteString(compact(l.Text))
	}
	if !strings.Contains(text.String(), compact(q)) {
		return errors.New("app-device: Lingbao OCR did not verify pasted input")
	}
	return nil
}
func (f *flow) lingbaoText(ctx context.Context) (string, error) {
	img, e := f.capture(ctx)
	if e != nil {
		return "", e
	}
	lines, e := device.TiledOCR(ctx, f.ocr, img, image.Rect(250, 850, 2750, 1135), 1000, 400, 2)
	if e != nil {
		return "", e
	}
	lines = device.MergeRows(lines, 45)
	var out []string
	for _, l := range lines {
		v := strings.TrimSpace(l.Text)
		if l.Box.Min.X > 1200 || v == "" || compact(v) == "返回首页" {
			continue
		}
		out = append(out, v)
	}
	return strings.Join(out, "\n"), nil
}
func (f *flow) lingbao(ctx context.Context, q string) (string, string, error) {
	if e := f.lingbaoGuard(ctx); e != nil {
		return "", "", e
	}
	baseline, e := f.lingbaoText(ctx)
	if e != nil {
		return "", "", e
	}
	if e = f.lingbaoInput(ctx, q); e != nil {
		return "", "", e
	}
	if e = f.hideIME(ctx); e != nil {
		return "", "", e
	}
	if e = f.lingbaoGuard(ctx); e != nil {
		return "", "", e
	}
	if e = f.point(ctx, 2930, 1330); e != nil {
		return "", "", e
	}
	if e = f.pause(ctx, 2*time.Second); e != nil {
		return "", "", e
	}
	responseCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	ctx = responseCtx
	previous := ""
	stable := 0
	for i := 0; i < 75; i++ {
		if e = f.foreground(ctx); e != nil {
			return "", "", e
		}
		text, e := f.lingbaoText(ctx)
		if e != nil {
			return "", "", e
		}
		if text != "" && text != baseline && compact(text) != compact(q) {
			if text == previous {
				stable++
			} else {
				stable = 1
				previous = text
			}
			if stable >= 2 {
				return text, "ocr-stable", nil
			}
		} else {
			stable = 0
		}
		if e = f.pause(ctx, 1200*time.Millisecond); e != nil {
			return "", "", e
		}
	}
	return "", "", errors.New("app-device: Lingbao new stable OCR reply not observed")
}
