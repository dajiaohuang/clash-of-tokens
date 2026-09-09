package appdevice

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/drivers/device"
	"context"
	"os"
	"path/filepath"
	"strings"
)

type CheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type CheckReport struct {
	Ready        bool        `json:"ready"`
	LiveVerified bool        `json:"live_verified"`
	Checks       []CheckItem `json:"checks"`
}

// Check performs only local file checks and read-only Android probes. It neither
// sends a question nor operates the clipboard, app navigation or login flows.
func Check(ctx context.Context, d config.Device) CheckReport {
	report := CheckReport{}
	add := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, CheckItem{name, ok, detail})
	}
	for _, v := range []struct{ name, path string }{{"adb", d.ADBPath}, {"ocr", d.OCRPath}} {
		st, e := os.Stat(v.path)
		add(v.name, filepath.IsAbs(v.path) && e == nil && !st.IsDir(), "explicit local executable path")
	}
	add("serial", d.Serial != "", "select the authorized physical device explicitly")
	add("clipboard_sync", false, "manually enable vivo clipboard sync; actual paste verification runs before submission")
	if d.Serial == "" || d.ADBPath == "" {
		return report
	}
	r := device.ADB{Path: d.ADBPath, Serial: d.Serial}
	state, e := r.Run(ctx, "get-state")
	online := e == nil && strings.TrimSpace(string(state)) == "device"
	add("device_online", online, "USB device must be connected, unlocked and authorized")
	if !online {
		return report
	}
	for _, id := range []string{"meituan-xiaotuan", "wangzhe-lingbao", "douyin-xiaohuoren"} {
		p := profiles[id]
		data, e := r.Run(ctx, "shell", "pm", "path", p.Package)
		add(id, e == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "package:"), p.Package)
	}
	// Installation and authorization alone do not prove a live conversation.
	return report
}
