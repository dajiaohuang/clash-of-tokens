// cot-device-verify performs an explicit, single-question physical smoke test.
// It never marks a fixture run or source README as live verification.
package main

import (
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/providers/appdevice"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	path := flag.String("config", "device.local.json", "local configuration")
	id := flag.String("source", "", "one explicitly enabled source id")
	live := flag.Bool("live", false, "send one real question to the currently selected app session")
	flag.Parse()
	c, e := config.Load(*path)
	if e != nil {
		return e
	}
	if !*live {
		return json.NewEncoder(os.Stdout).Encode(appdevice.Check(context.Background(), c.Device))
	}
	var source *config.Source
	for _, s := range c.Sources {
		if s.ID == *id && s.Adapter == "app-device" && s.Enabled {
			v := s
			source = &v
			break
		}
	}
	if source == nil || len(source.Models) != 1 {
		return fmt.Errorf("specify one enabled app-device source with one model")
	}
	var random [8]byte
	if _, e = rand.Read(random[:]); e != nil {
		return e
	}
	nonce := "cot-device-" + hex.EncodeToString(random[:])
	body, _ := json.Marshal(map[string]any{"model": source.Models[0].Upstream, "messages": []any{map[string]string{"role": "user", "content": "请只原样回复这个测试字符串，不要增加其他内容：" + nonce}}})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := appdevice.New(*source, c.Device)
	defer client.Close()
	response, e := client.Do(ctx, "chat", source.Models[0].Upstream, false, body, nil)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if e != nil {
		return e
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &out) != nil || len(out.Choices) != 1 {
		return fmt.Errorf("invalid completion")
	}
	text := out.Choices[0].Message.Content
	sum := sha256.Sum256([]byte(text))
	passed := strings.Contains(text, nonce)
	report := map[string]any{"source": source.ID, "checked_at": time.Now().UTC().Format(time.RFC3339), "live_requested": true, "passed": passed, "answer_bytes": len(text), "answer_sha256": hex.EncodeToString(sum[:]), "extraction": response.Header.Get("X-COT-Extraction"), "completion": response.Header.Get("X-COT-Completion")}
	if e = json.NewEncoder(os.Stdout).Encode(report); e != nil {
		return e
	}
	if !passed {
		return fmt.Errorf("physical reply did not reproduce the unique probe; not live-verified")
	}
	return nil
}
