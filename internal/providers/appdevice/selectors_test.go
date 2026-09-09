package appdevice

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/drivers/device"
	"context"
	"errors"
	"testing"
)

func TestMeituanBoundariesRejectOldPartialAndOffscreen(t *testing.T) {
	nodes := []device.Node{{Resource: mtQuery, Text: "旧问题"}, {Resource: mtThinking, Text: "已完成"}, {Text: "旧答案"}, {Resource: mtCopy, Clickable: true, Enabled: true}, {Resource: mtQuery, Text: "新问题"}, {Resource: mtThinking, Text: "思考中"}, {Text: "半截正文"}}
	if a, c := mtAnswer(nodes, "新问题", 0); a != "" || c != nil {
		t.Fatal("partial accepted", a)
	}
	if a, _ := mtAnswer(nodes, "缺失问题", 0); a != "" {
		t.Fatal("offscreen anchor accepted")
	}
	nodes[5].Text = "已完成"
	nodes[6].Text = "# 答案\n\n- 保留 Markdown"
	if a, _ := mtAnswer(nodes, "新问题", 0); a != nodes[6].Text {
		t.Fatal(a)
	}
	if a, _ := mtAnswer(nodes, "新问题", 1); a != "" {
		t.Fatal("identical old turn accepted")
	}
}
func TestXiaohuorenCurrentTurnAndCard(t *testing.T) {
	nodes := []device.Node{{Text: "@小火人 问题"}, {Resource: "my.maya.android:id/kd8", Text: "第一段", Bounds: "[10,100][200,200]"}, {Resource: "my.maya.android:id/kd8", Text: "右侧其他人", Bounds: "[800,100][1000,200]"}, {Resource: "my.maya.android:id/kd8", Text: "第二段", Bounds: "[10,210][200,300]"}, {Resource: "my.maya.android:id/gen", Clickable: true, Enabled: true, Bounds: "[0,0][20,20]"}}
	a, c := xhResponse(nodes, "@小火人 问题", 0)
	if a != "第一段\n第二段" || c == nil {
		t.Fatal(a, c)
	}
	if a, c = xhResponse(nodes, "另一个问题", 0); a != "" || c != nil {
		t.Fatal("old response selected")
	}
	if a, c = xhResponse(nodes, "@小火人 问题", 1); a != "" || c != nil {
		t.Fatal("same old question selected")
	}
}
func TestUnsupportedBeforeDeviceAccess(t *testing.T) {
	c := New(config.Source{Provider: "meituan-xiaotuan"}, config.Device{})
	for _, body := range []string{`{"messages":[{"role":"system","content":"x"}]}`, `{"messages":[{"role":"user","content":"x"}],"tools":[]}`, `{"messages":[{"role":"user","content":"x"},{"role":"assistant","content":"y"}]}`} {
		if _, e := c.Do(context.Background(), "chat", "meituan_xiaotuan", false, []byte(body), nil); !errors.Is(e, Unsupported) {
			t.Fatal(e)
		}
	}
}
