package appdevice

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/drivers/device"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type CheckItem struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}
type CheckReport struct {
	Ready         bool        `json:"ready"`
	LiveVerified  bool        `json:"live_verified"`
	ADBReady      bool        `json:"adb_ready"`
	Connected     bool        `json:"connected"`
	Resolution    string      `json:"resolution,omitempty"`
	ForegroundApp string      `json:"foreground_app,omitempty"`
	Checks        []CheckItem `json:"checks"`
}

var (
	resolutionRE = regexp.MustCompile(`(?i)(?:physical|override)\s+size:\s*(\d{1,5}x\d{1,5})`)
	foregroundRE = regexp.MustCompile(`(?i)mCurrentFocus=.*?\s([A-Za-z0-9._-]{1,160})/[^\s}]+`)
)

func parseResolution(data []byte) string {
	matches := resolutionRE.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		return ""
	}
	// Prefer an override because that is the active logical display size.
	for _, match := range matches {
		if strings.HasPrefix(strings.ToLower(match[0]), "override") {
			return match[1]
		}
	}
	return matches[0][1]
}

func parseForeground(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "mCurrentFocus=") {
			continue
		}
		match := foregroundRE.FindStringSubmatch(line)
		if len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

// Check performs only local file checks and read-only Android probes. It neither
// sends a question nor operates the clipboard, app navigation or login flows.
func Check(ctx context.Context, d config.Device) CheckReport {
	report := CheckReport{}
	adbOK, ocrOK := false, false
	add := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, CheckItem{name, ok, detail})
	}
	for _, v := range []struct{ name, path string }{{"adb", d.ADBPath}, {"ocr", d.OCRPath}} {
		st, e := os.Stat(v.path)
		ok := filepath.IsAbs(v.path) && e == nil && !st.IsDir()
		add(v.name, ok, "explicit local executable path")
		if v.name == "adb" {
			adbOK = ok
		} else {
			ocrOK = ok
		}
	}
	report.ADBReady = adbOK
	add("serial", d.Serial != "", "select the authorized physical device explicitly")
	add("clipboard_sync", false, "manually enable vivo clipboard sync; actual paste verification runs before submission")
	if d.Serial == "" || d.ADBPath == "" {
		return report
	}
	r := device.ADB{Path: d.ADBPath, Serial: d.Serial}
	state, e := r.Run(ctx, "get-state")
	online := e == nil && strings.TrimSpace(string(state)) == "device"
	report.Connected = online
	add("device_online", online, "USB device must be connected, unlocked and authorized")
	if !online {
		return report
	}
	if size, e := r.Run(ctx, "shell", "wm", "size"); e == nil {
		report.Resolution = parseResolution(size)
	}
	add("resolution", report.Resolution != "", "active logical display size from read-only wm size")
	if focus, e := r.Run(ctx, "shell", "dumpsys", "window", "windows"); e == nil {
		report.ForegroundApp = parseForeground(focus)
	}
	add("foreground_app", report.ForegroundApp != "", "currently focused Android package")
	for _, id := range []string{"meituan-xiaotuan", "wangzhe-lingbao", "douyin-xiaohuoren"} {
		p := profiles[id]
		data, e := r.Run(ctx, "shell", "pm", "path", p.Package)
		add(id, e == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "package:"), p.Package)
	}
	// Installation and authorization alone do not prove a live conversation.
	report.Ready = adbOK && ocrOK && online && report.Resolution != "" && report.ForegroundApp != ""
	return report
}
