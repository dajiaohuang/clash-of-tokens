package appdevice

import (
	"clash-of-tokens/internal/drivers/device"
	"context"
	"errors"
	"strings"
	"time"
)

const mtPrefix = "com.sankuai.meituan:id/"
const mtQuery = mtPrefix + "tv_ai_small_tuan_query_text"
const mtThinking = mtPrefix + "small_tuan_thinking_v2_tv_title"
const mtSuggestion = mtPrefix + "small_tuan_sug_tv"
const mtCopy = mtPrefix + "aixiaotuan_feedback_copy_button"

func mtInput(nodes []device.Node) (device.Node, bool) {
	var fallback *device.Node
	for _, n := range nodes {
		if (n.Resource == mtPrefix+"ai_search_input_bar" || n.Resource == mtPrefix+"et_expanded_input") && n.Enabled {
			if n.Class == "android.widget.EditText" {
				return n, true
			}
			if fallback == nil {
				v := n
				fallback = &v
			}
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return device.Node{}, false
}
func mtFunctional(nodes []device.Node) bool {
	if _, ok := mtInput(nodes); !ok {
		return false
	}
	for _, n := range nodes {
		if n.Resource == mtQuery || n.Resource == mtThinking || n.Resource == mtPrefix+"dka" || strings.Contains(n.Text, "我是小团") {
			return true
		}
	}
	return false
}
func mtCount(nodes []device.Node, q string) (count, last int) {
	last = -1
	for i, n := range nodes {
		if n.Resource == mtQuery && normalized(n.Text) == normalized(q) {
			count++
			last = i
		}
	}
	return
}

// Unlike the reference's permissive copy fallback, require the current question
// to remain visible. A scrolled-out anchor is not permission to copy an old reply.
func mtAnswer(nodes []device.Node, q string, previousCount int) (string, *device.Node) {
	count, start := mtCount(nodes, q)
	if start < 0 || count <= previousCount {
		return "", nil
	}
	end := len(nodes)
	for i := start + 1; i < len(nodes); i++ {
		if nodes[i].Resource == mtQuery {
			end = i
			break
		}
	}
	completed := false
	var copyNode *device.Node
	for i := start + 1; i < end; i++ {
		n := nodes[i]
		if n.Resource == mtThinking && strings.Contains(n.Text, "已完成") {
			completed = true
		}
		if n.Resource == mtCopy && n.Clickable && n.Enabled {
			v := n
			copyNode = &v
		}
	}
	if !completed && copyNode == nil {
		return "", nil
	}
	best := ""
	for i := start + 1; i < end; i++ {
		n := nodes[i]
		if n.Resource == mtSuggestion {
			break
		}
		if n.Resource == mtThinking || n.Resource == mtCopy || n.Class == "android.widget.EditText" {
			continue
		}
		text := strings.TrimSpace(n.Text)
		skip := false
		for _, v := range []string{"内容由AI生成", "服务须知", "重答", "分享", "发消息或按住说话"} {
			if strings.Contains(text, v) {
				skip = true
			}
		}
		if !skip && len(text) > len(best) {
			best = text
		}
	}
	return best, copyNode
}
func (f *flow) meituan(ctx context.Context, q string) (string, string, error) {
	nodes, e := f.nodes(ctx)
	if e != nil {
		return "", "", e
	}
	if !mtFunctional(nodes) {
		return "", "", errors.New("app-device: open the initialized Meituan Xiaotuan conversation via its normal app entry")
	}
	oldCount, _ := mtCount(nodes, q)
	input, _ := mtInput(nodes)
	if e = f.tap(ctx, input); e != nil {
		return "", "", e
	}
	if e = f.clear(ctx); e != nil {
		return "", "", e
	}
	if e = f.paste(ctx, q); e != nil {
		return "", "", e
	}
	nodes, e = f.nodes(ctx)
	if e != nil {
		return "", "", e
	}
	input, ok := mtInput(nodes)
	if !ok || input.Class != "android.widget.EditText" || input.Text != q {
		return "", "", errors.New("app-device: Meituan input verification failed")
	}
	var send *device.Node
	for _, n := range nodes {
		if n.Resource == mtPrefix+"iv_expanded_input_btn" && n.Clickable && n.Enabled {
			v := n
			send = &v
			break
		}
	}
	if send == nil {
		return "", "", errors.New("app-device: exact Meituan send control missing")
	}
	if e = f.tap(ctx, *send); e != nil {
		return "", "", e
	}
	responseCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ctx = responseCtx
	previous := ""
	stable := 0
	for i := 0; i < 75; i++ {
		if e = f.pause(ctx, 800*time.Millisecond); e != nil {
			return "", "", e
		}
		if e = f.foreground(ctx); e != nil {
			return "", "", e
		}
		nodes, e = f.nodes(ctx)
		if e != nil {
			return "", "", e
		}
		text, copyNode := mtAnswer(nodes, q, oldCount)
		if copyNode != nil && text != "" {
			answer, e := f.copyAt(ctx, *copyNode, 1)
			if e != nil {
				return "", "", e
			}
			if !strings.Contains(compact(answer), compact(text)) {
				return "", "", errors.New("app-device: copied text does not match the anchored Meituan reply")
			}
			return answer, "clipboard", nil
		}
		if text != "" && text == previous {
			stable++
		} else {
			stable = 1
			previous = text
		}
		if text != "" && stable >= 2 {
			return text, "control", nil
		}
	}
	return "", "", errors.New("app-device: Meituan completed reply not observed")
}
