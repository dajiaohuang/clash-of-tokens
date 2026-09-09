package appdevice

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/drivers/device"
)

type requestError string

func (e requestError) Error() string   { return string(e) }
func (e requestError) HTTPStatus() int { return 422 }

const Unsupported = requestError("app-device: one user text up to 4000 characters only; API history, tools, images, controls and sessions unsupported")

type Client struct {
	source config.Source
	env    config.Device
}

func New(s config.Source, d config.Device) *Client { return &Client{s, d} }
func (c *Client) Close()                           {}

func (c *Client) Do(ctx context.Context, proto, model string, stream bool, body []byte, caller http.Header) (*http.Response, error) {
	p, ok := profiles[c.source.Provider]
	if !ok || model != p.Model || proto != "chat" || caller.Get("X-COT-Session") != "" || len(body) > 64<<10 || !utf8.Valid(body) {
		return nil, Unsupported
	}
	var in struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || len(in.Messages) != 1 || in.Messages[0].Role != "user" || strings.TrimSpace(in.Messages[0].Content) == "" || utf8.RuneCountInString(in.Messages[0].Content) > 4000 || strings.ContainsRune(in.Messages[0].Content, 0) {
		return nil, Unsupported
	}
	if !c.env.Enabled || !filepath.IsAbs(c.env.ADBPath) || c.env.Serial == "" || c.env.ClipboardSyncMS < 200 || c.env.ClipboardSyncMS > 5000 {
		return nil, errors.New("app-device: configure and enable the physical device environment")
	}
	if runtime.GOOS != "windows" {
		return nil, errors.New("app-device: current clipboard bridge requires Windows")
	}
	if c.source.Project != "current-app-session" || c.source.AutoApproved || c.source.MaxInflight != 1 || c.source.QuotaMaxInflight != 1 {
		return nil, errors.New("app-device: explicit current-app-session and manual single-device routing required")
	}
	lease, e := device.Acquire(c.env.StateDir, c.env.Serial)
	if e != nil {
		return nil, e
	}
	defer lease.Close()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	f := &flow{p: p, r: device.ADB{Path: c.env.ADBPath, Serial: c.env.Serial}, ocr: device.Tesseract{Path: c.env.OCRPath, DataDir: c.env.OCRDataDir}, readClip: device.ReadClipboard, writeClip: device.WriteClipboard, pause: device.Pause, syncDelay: time.Duration(c.env.ClipboardSyncMS) * time.Millisecond, dirty: lease.MarkDirty}
	answer, method, e := f.run(ctx, in.Messages[0].Content)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	lease.Complete()
	return completion(model, answer, method, stream), nil
}

type flow struct {
	p         Profile
	r         device.Runner
	ocr       device.OCR
	readClip  func() (string, error)
	writeClip func(string) error
	pause     func(context.Context, time.Duration) error
	syncDelay time.Duration
	dirty     func()
}

func (f *flow) nodes(ctx context.Context) ([]device.Node, error) { return device.Dump(ctx, f.r) }
func (f *flow) key(ctx context.Context, keys ...string) error {
	_, e := f.r.Run(ctx, append([]string{"shell", "input", "keyevent"}, keys...)...)
	return e
}
func (f *flow) tap(ctx context.Context, n device.Node) error {
	if e := f.foreground(ctx); e != nil {
		return e
	}
	f.dirty()
	return device.Tap(ctx, f.r, n)
}
func (f *flow) point(ctx context.Context, x, y int) error {
	return f.tap(ctx, device.Node{Bounds: fmt.Sprintf("[%d,%d][%d,%d]", x, y, x+1, y+1)})
}
func (f *flow) foreground(ctx context.Context) error {
	data, e := f.r.Run(ctx, "shell", "dumpsys", "window", "windows")
	if e != nil {
		return e
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "mCurrentFocus=") && strings.Contains(line, f.p.Package+"/") {
			return nil
		}
	}
	return errors.New("app-device: target app is not foreground; open the intended conversation")
}
func (f *flow) capture(ctx context.Context) (image.Image, error) {
	img, e := device.Capture(ctx, f.r)
	if e != nil {
		return nil, e
	}
	if f.p.Width > 0 && (img.Bounds().Dx() != f.p.Width || img.Bounds().Dy() != f.p.Height) {
		return nil, errors.New("app-device: screen dimensions differ from the calibrated profile")
	}
	return img, nil
}
func (f *flow) lines(ctx context.Context, region image.Rectangle) ([]device.Line, error) {
	img, e := f.capture(ctx)
	if e != nil {
		return nil, e
	}
	if region.Empty() {
		region = img.Bounds()
	}
	return f.ocr.Read(ctx, img, region)
}
func compact(s string) string    { return strings.Join(strings.Fields(s), "") }
func normalized(s string) string { return strings.Join(strings.Fields(s), " ") }
func (f *flow) guard(ctx context.Context, region image.Rectangle, all bool, texts ...string) error {
	if e := f.foreground(ctx); e != nil {
		return e
	}
	lines, e := f.lines(ctx, region)
	if e != nil {
		return e
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(compact(l.Text))
	}
	matches := 0
	for _, s := range texts {
		if strings.Contains(b.String(), compact(s)) {
			matches++
		}
	}
	if matches == 0 || all && matches != len(texts) {
		return errors.New("app-device: expected conversation page markers missing")
	}
	return nil
}

// Paste uses the OS clipboard only for Unicode transport. No question is ever
// interpolated into an adb shell command. A changed clipboard aborts submission.
func (f *flow) paste(ctx context.Context, text string) (err error) {
	old, e := f.readClip()
	if e != nil {
		return e
	}
	f.dirty()
	if e = f.writeClip(text); e != nil {
		return e
	}
	defer func() {
		current, e := f.readClip()
		if e != nil {
			if err == nil {
				err = e
			}
			return
		}
		if current == text {
			if e = f.writeClip(old); e != nil && err == nil {
				err = e
			}
		} else if err == nil {
			err = errors.New("app-device: clipboard changed during input; verify phone draft before retry")
		}
	}()
	if e = f.pause(ctx, f.syncDelay); e != nil {
		return e
	}
	if e = f.key(ctx, "KEYCODE_PASTE"); e != nil {
		return e
	}
	return f.pause(ctx, 600*time.Millisecond)
}
func (f *flow) clear(ctx context.Context) error {
	f.dirty()
	if _, e := f.r.Run(ctx, "shell", "input", "keycombination", "KEYCODE_CTRL_LEFT", "KEYCODE_A"); e != nil {
		return e
	}
	return f.key(ctx, "KEYCODE_DEL")
}
func (f *flow) copyAt(ctx context.Context, n device.Node, minChars int) (text string, err error) {
	old, e := f.readClip()
	if e != nil {
		return "", e
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return "", e
	}
	marker := "__COT_COPY_" + hex.EncodeToString(nonce[:]) + "__"
	if e = f.writeClip(marker); e != nil {
		return "", e
	}
	defer func() {
		if e := f.writeClip(old); e != nil && err == nil {
			err = e
		}
	}()
	if e = f.pause(ctx, f.syncDelay); e != nil {
		return "", e
	}
	if e = f.tap(ctx, n); e != nil {
		return "", e
	}
	for i := 0; i < 70; i++ {
		current, e := f.readClip()
		if e != nil {
			return "", e
		}
		if current != marker && current != old && utf8.RuneCountInString(strings.TrimSpace(current)) >= minChars {
			return strings.TrimSpace(current), nil
		}
		if e = f.pause(ctx, 100*time.Millisecond); e != nil {
			return "", e
		}
	}
	return "", errors.New("app-device: copy did not yield a fresh complete answer")
}
func (f *flow) run(ctx context.Context, q string) (string, string, error) {
	if _, e := f.readClip(); e != nil {
		return "", "", e
	}
	state, e := f.r.Run(ctx, "get-state")
	if e != nil || strings.TrimSpace(string(state)) != "device" {
		return "", "", errors.New("app-device: Android device is offline or unauthorized")
	}
	if e = f.foreground(ctx); e != nil {
		return "", "", e
	}
	switch f.p.ID {
	case "meituan-xiaotuan":
		return f.meituan(ctx, q)
	case "wangzhe-lingbao":
		return f.lingbao(ctx, q)
	case "douyin-xiaohuoren":
		return f.xiaohuoren(ctx, q)
	}
	return "", "", errors.New("app-device: unknown profile")
}
func completion(model, text, method string, stream bool) *http.Response {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	id := "chatcmpl-" + hex.EncodeToString(nonce[:])
	created := time.Now().Unix()
	h := make(http.Header)
	h.Set("Content-Type", "application/json")
	h.Set("X-COT-Delivery", "buffered")
	h.Set("X-COT-Extraction", method)
	h.Set("X-COT-Context", "current-app-session")
	if strings.HasSuffix(method, "-stable") {
		h.Set("X-COT-Completion", "observed-stability")
	} else {
		h.Set("X-COT-Completion", "ui-marker")
	}
	var data []byte
	if stream {
		h.Set("Content-Type", "text/event-stream")
		var buf bytes.Buffer
		for _, choice := range []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": text}, "finish_reason": nil}, map[string]any{"index": 0, "delta": map[string]string{}, "finish_reason": "stop"}} {
			raw, _ := json.Marshal(map[string]any{"id": id, "model": model, "object": "chat.completion.chunk", "created": created, "choices": []any{choice}})
			fmt.Fprintf(&buf, "data: %s\n\n", raw)
		}
		buf.WriteString("data: [DONE]\n\n")
		data = buf.Bytes()
	} else {
		data, _ = json.Marshal(map[string]any{"id": id, "model": model, "object": "chat.completion", "created": created, "choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": nil})
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
}
