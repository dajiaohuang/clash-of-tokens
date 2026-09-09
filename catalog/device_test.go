package catalog

import (
	"clash-of-tokens/internal/config"
	"path/filepath"
	"testing"
)

func TestDevicePresetsRequireExplicitEnvironmentAndManualRouting(t *testing.T) {
	for _, id := range []string{"meituan-xiaotuan", "wangzhe-lingbao", "douyin-xiaohuoren"} {
		s, e := Preset(id, "requested-label")
		if e != nil {
			t.Fatal(e)
		}
		c := config.Default()
		s.Enabled = true
		s.Project = "current-app-session"
		c.Sources = []config.Source{s}
		if c.Validate() == nil {
			t.Fatal("enabled without environment")
		}
		dir := t.TempDir()
		c.Device = config.Device{Enabled: true, ADBPath: filepath.Join(dir, "adb"), OCRPath: filepath.Join(dir, "tesseract"), Serial: "test-device", StateDir: dir, ClipboardSyncMS: 2500}
		if e = c.Validate(); e != nil {
			t.Fatal(e)
		}
		c.Sources[0].AutoApproved = true
		if c.Validate() == nil {
			t.Fatal("physical session allowed in Auto")
		}
		c.Sources[0].AutoApproved = false
		c.Sources[0].QuotaMaxInflight = 2
		if c.Validate() == nil {
			t.Fatal("device capacity multiplied")
		}
		c.Sources[0].QuotaMaxInflight = 1
		c.Sources[0].Local = true
		if c.Validate() == nil {
			t.Fatal("cloud app advertised as local inference")
		}
	}
}
