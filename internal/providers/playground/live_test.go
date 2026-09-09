package playground

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestLivePlayground(t *testing.T) {
	if os.Getenv("COT_PLAYGROUND_LIVE") != "1" {
		t.Skip("explicit live browser opt-in required")
	}
	b := config.Default().Browser
	b.Enabled = true
	c := New(b)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	start := time.Now()
	r, err := c.Do(ctx, "chat", "zai-org/glm-4.7-flash", false, []byte(`{"messages":[{"role":"user","content":"Reply exactly: copper-cloud-29"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "copper-cloud-29") {
		t.Fatal("expected phrase absent from response")
	}
	t.Logf("live public playground succeeded in %s", time.Since(start).Round(time.Millisecond))
}
