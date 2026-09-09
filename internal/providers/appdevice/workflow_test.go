package appdevice

import (
	"bytes"
	"clash-of-tokens/internal/drivers/device"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"testing"
	"time"
)

// A deterministic phone fixture exercises the actual command/control/OCR
// workflow, with no Android connection, real clipboard or web inference.
type phoneFixture struct {
	p                                    Profile
	clip, draft                          string
	sent, focused, mention, detail, card bool
	sendCount                            int
	pngBefore, pngAfter                  []byte
	commands                             []string
	afterSend                            func()
}

func node(id, text, class, bounds string) device.Node {
	return device.Node{Resource: id, Text: text, Class: class, Bounds: bounds, Enabled: true, Clickable: true}
}
func xmlNodes(nodes []device.Node) []byte {
	var b bytes.Buffer
	b.WriteString("<hierarchy>")
	for _, n := range nodes {
		data, _ := xml.Marshal(n)
		data = bytes.ReplaceAll(data, []byte("<Node "), []byte("<node "))
		data = bytes.ReplaceAll(data, []byte("Node>"), []byte("node>"))
		b.Write(data)
	}
	b.WriteString("</hierarchy>")
	return b.Bytes()
}
func (p *phoneFixture) nodes() []device.Node {
	switch p.p.ID {
	case "meituan-xiaotuan":
		nodes := []device.Node{node(mtPrefix+"dka", "小团", "android.widget.TextView", "[0,0][200,100]"), node(mtPrefix+"et_expanded_input", p.draft, "android.widget.EditText", "[0,2800][500,2900]"), node(mtPrefix+"iv_expanded_input_btn", "", "android.widget.ImageView", "[1000,2800][1100,2900]")}
		if p.sent {
			nodes = append(nodes, node(mtQuery, "测试问题", "android.widget.TextView", "[100,200][500,300]"), node(mtThinking, "已完成", "android.widget.TextView", "[100,320][500,350]"), node("", "完整答案正文", "android.widget.TextView", "[100,400][500,500]"), node(mtCopy, "复制", "android.widget.ImageView", "[100,510][150,550]"))
		}
		return nodes
	case "wangzhe-lingbao":
		if p.focused {
			n := node("", ""+p.draft, "android.widget.EditText", "[100,1200][2700,1400]")
			n.Focused = true
			return []device.Node{n}
		}
		return nil
	default:
		in := node(p.p.Package+":id/msg_et", p.draft, "android.widget.EditText", "[0,2900][1000,3100]")
		send := node("", "发送", "android.widget.TextView", "[1200,3000][1300,3100]")
		send.Package = p.p.Package
		nodes := []device.Node{in, send}
		if p.draft == "@" {
			nodes = append(nodes, node(p.p.Package+":id/jmn", "小火人", "android.widget.TextView", "[100,2400][200,2500]"))
		}
		if p.sent {
			nodes = append(nodes, node("", "@小火人 测试问题", "android.widget.TextView", "[600,1000][1400,1100]"))
			if p.card {
				nodes = append(nodes, node(p.p.Package+":id/gen", "卡片", "android.view.View", "[400,1400][600,1600]"))
			} else {
				nodes = append(nodes, node(p.p.Package+":id/kd8", "来自小火人的新回复", "android.widget.TextView", "[10,1300][900,1400]"))
			}
		}
		return nodes
	}
}
func (p *phoneFixture) Run(ctx context.Context, args ...string) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s := strings.Join(args, " ")
	p.commands = append(p.commands, s)
	switch {
	case s == "get-state":
		return []byte("device\n"), nil
	case s == "shell dumpsys window windows":
		activity := "Conversation"
		if p.detail {
			activity = "AnnieXHostActivity"
		}
		return []byte("mCurrentFocus=Window{ " + p.p.Package + "/" + activity + "}"), nil
	case strings.HasPrefix(s, "shell uiautomator dump"):
		return nil, nil
	case s == "exec-out cat /sdcard/clash-tokens-window.xml":
		return xmlNodes(p.nodes()), nil
	case s == "exec-out screencap -p":
		if p.sent {
			return p.pngAfter, nil
		}
		return p.pngBefore, nil
	case s == "shell input text @":
		p.draft = "@"
		return nil, nil
	case s == "shell input keyevent KEYCODE_DEL":
		p.draft = ""
		return nil, nil
	case s == "shell input keyevent KEYCODE_PASTE":
		p.draft += p.clip
		return nil, nil
	case s == "shell input keyevent KEYCODE_BACK":
		p.detail = false
		return nil, nil
	case strings.HasPrefix(s, "shell input tap"):
		switch s {
		case "shell input tap 1500 1330":
			p.focused = true
		case "shell input tap 150 2450":
			p.mention = true
			p.draft = "@小火人 "
		case "shell input tap 1050 2850", "shell input tap 2930 1330", "shell input tap 1250 3050":
			if p.p.ID == "douyin-xiaohuoren" && !p.mention {
				return nil, fmt.Errorf("plain mention sent")
			}
			p.sent = true
			p.sendCount++
			if p.afterSend != nil {
				p.afterSend()
			}
		case "shell input tap 125 530":
			p.clip = "完整答案正文"
		case "shell input tap 500 1500":
			p.detail = true
		case "shell input tap 700 2630":
			p.clip = "这是小火人卡片中复制出的完整长回答，不应被截断为预览文本。"
		}
		return nil, nil
	case strings.HasPrefix(s, "shell input keycombination"):
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected command: %s", s)
}
func (p *phoneFixture) Read(ctx context.Context, img image.Image, region image.Rectangle) ([]device.Line, error) {
	if p.p.ID == "wangzhe-lingbao" {
		if img.Bounds().Dx() == 3200 {
			return []device.Line{{Text: "灵宝", Box: image.Rect(0, 0, 100, 100)}, {Text: "发送", Box: image.Rect(2900, 1300, 3000, 1350)}}, nil
		}
		r, _, _, _ := img.At(0, 0).RGBA()
		text := "此前的回答"
		if r > 150*257 {
			text = "这是灵宝新的回答"
		}
		return []device.Line{{Text: text, Box: image.Rect(10, 10, 400, 40), Confidence: .95}}, nil
	}
	if p.detail {
		return []device.Line{{Text: "复制", Box: image.Rect(600, 2700, 800, 2800), Confidence: .98}}, nil
	}
	return []device.Line{{Text: "发消息或按住说话", Box: image.Rect(0, 2800, 1000, 2900)}}, nil
}
func fixturePNG(w, h int, red uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{red, 0, 0, 255}), image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func TestNativeWorkflowsEndToEnd(t *testing.T) {
	for _, tc := range []struct {
		id   string
		card bool
		want string
	}{{"meituan-xiaotuan", false, "完整答案正文"}, {"wangzhe-lingbao", false, "这是灵宝新的回答"}, {"douyin-xiaohuoren", false, "来自小火人的新回复"}, {"douyin-xiaohuoren", true, "这是小火人卡片中复制出的完整长回答"}} {
		t.Run(fmt.Sprintf("%s/card=%v", tc.id, tc.card), func(t *testing.T) {
			p := &phoneFixture{p: profiles[tc.id], clip: "原来的剪贴板", card: tc.card}
			w, h := p.p.Width, p.p.Height
			if w == 0 {
				w, h = 1440, 3200
			}
			p.pngBefore = fixturePNG(w, h, 100)
			p.pngAfter = fixturePNG(w, h, 200)
			dirty := 0
			f := &flow{p: p.p, r: p, ocr: p, readClip: func() (string, error) { return p.clip, nil }, writeClip: func(s string) error { p.clip = s; return nil }, pause: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, dirty: func() { dirty++ }}
			answer, method, e := f.run(context.Background(), "测试问题")
			if e != nil {
				t.Fatal(e, p.commands)
			}
			if !strings.HasPrefix(answer, tc.want) || method == "" || p.sendCount != 1 || dirty == 0 || p.clip != "原来的剪贴板" || p.detail {
				t.Fatalf("answer=%q method=%s sends=%d clip=%q detail=%v", answer, method, p.sendCount, p.clip, p.detail)
			}
		})
	}
}

func TestCancellationAfterSubmissionStopsPhoneActions(t *testing.T) {
	for _, id := range []string{"meituan-xiaotuan", "wangzhe-lingbao", "douyin-xiaohuoren"} {
		t.Run(id, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &phoneFixture{p: profiles[id], clip: "原来的剪贴板", afterSend: cancel}
			w, h := p.p.Width, p.p.Height
			if w == 0 {
				w, h = 1440, 3200
			}
			p.pngBefore = fixturePNG(w, h, 100)
			p.pngAfter = fixturePNG(w, h, 200)
			f := &flow{p: p.p, r: p, ocr: p, readClip: func() (string, error) { return p.clip, nil }, writeClip: func(s string) error { p.clip = s; return nil }, pause: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, dirty: func() {}}
			answer, _, err := f.run(ctx, "测试问题")
			if !errors.Is(err, context.Canceled) || answer != "" || p.sendCount != 1 || p.clip != "原来的剪贴板" {
				t.Fatalf("err=%v answer=%q sends=%d clipboard restored=%v", err, answer, p.sendCount, p.clip == "原来的剪贴板")
			}
			last := p.commands[len(p.commands)-1]
			if !strings.HasPrefix(last, "shell input tap ") {
				t.Fatalf("phone action after canceled submission: %s", last)
			}
		})
	}
}
