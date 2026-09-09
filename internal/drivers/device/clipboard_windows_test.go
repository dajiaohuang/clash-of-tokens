package device

import (
	"os"
	"testing"
)

func TestWindowsClipboardRoundTrip(t *testing.T) {
	if os.Getenv("COT_TEST_CLIPBOARD") != "1" {
		t.Skip("explicit local clipboard test only")
	}
	original, e := ReadClipboard()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := WriteClipboard(original); e != nil {
			t.Error("clipboard restoration failed")
		}
	}()
	const marker = "Clash of Tokens 剪贴板自检 42"
	if e = WriteClipboard(marker); e != nil {
		t.Fatal(e)
	}
	actual, e := ReadClipboard()
	if e != nil || actual != marker {
		t.Fatal("clipboard round-trip failed")
	}
}
