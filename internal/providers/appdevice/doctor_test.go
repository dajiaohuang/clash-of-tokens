package appdevice

import (
	"clash-of-tokens/internal/config"
	"context"
	"strings"
	"testing"
)

func TestParseDeviceDisplayMetadata(t *testing.T) {
	if got := parseResolution([]byte("Physical size: 1080x2400\n")); got != "1080x2400" {
		t.Fatalf("physical resolution = %q", got)
	}
	if got := parseResolution([]byte("Physical size: 1080x2400\nOverride size: 720x1600\n")); got != "720x1600" {
		t.Fatalf("override resolution = %q", got)
	}
	if got := parseResolution([]byte("Physical density: 420\n")); got != "" {
		t.Fatalf("unexpected resolution = %q", got)
	}
	if got := parseForeground([]byte("mCurrentFocus=Window{a1 u0 com.example.chat/.MainActivity}\n")); got != "com.example.chat" {
		t.Fatalf("foreground package = %q", got)
	}
	if got := parseForeground([]byte("mCurrentFocus=Window{no package}\n")); got != "" {
		t.Fatalf("unexpected foreground package = %q", got)
	}
}

func TestDeviceDoctorWithoutConfiguredDeviceIsReadOnly(t *testing.T) {
	report := Check(context.Background(), config.Device{})
	if report.Ready || report.Connected || report.ADBReady || report.LiveVerified {
		t.Fatalf("empty device reported ready: %+v", report)
	}
	for _, check := range report.Checks {
		if strings.Contains(strings.ToLower(check.Name+" "+check.Detail), "send") && check.OK {
			t.Fatal("doctor marked a message-sending check as successful")
		}
	}
}
