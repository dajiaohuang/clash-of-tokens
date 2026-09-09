package api

import (
	"bytes"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/routing"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var ingressResult []byte

func BenchmarkModelsLargeCatalog(b *testing.B) {
	c := config.Default()
	for i := range 100 {
		s := config.Source{ID: fmt.Sprint("account-", i), Provider: "test", Enabled: true}
		for j := range 10 {
			s.Models = append(s.Models, config.Model{ID: fmt.Sprint("model-", j)})
		}
		c.Sources = append(c.Sources, s)
	}
	s := &Server{cfg: c, Router: routing.New(c)}
	b.ReportAllocs()
	for b.Loop() {
		s.models(httptest.NewRecorder())
	}
}

func BenchmarkChunkedSmallBody(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	b.ReportAllocs()
	for b.Loop() {
		var err error
		ingressResult, err = readIngressBody(bytes.NewReader(payload), 4<<20, true)
		if err != nil && err != io.ErrUnexpectedEOF {
			b.Fatal(err)
		}
	}
}

func TestIngressBodyBoundaries(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		for _, size := range []int{0, 31, 32, 33, 64} {
			data, err := readIngressBody(bytes.NewReader(bytes.Repeat([]byte("x"), size)), 32, chunked)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				t.Fatal(err)
			}
			if len(data) != min(size, 33) {
				t.Fatalf("chunked=%v size=%d len=%d", chunked, size, len(data))
			}
		}
	}
}

func TestChunkedBodyGrowthBounded(t *testing.T) {
	for _, size := range []int{4095, 4096, 4097, 9000, 10000, 10001, 20000} {
		payload := bytes.Repeat([]byte("x"), size)
		data, err := readIngressBody(bytes.NewReader(payload), 10000, true)
		if err != nil || !bytes.Equal(data, payload[:min(size, 10001)]) || cap(data) > 10001 {
			t.Fatalf("size=%d len=%d cap=%d err=%v", size, len(data), cap(data), err)
		}
	}
}

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) { return 0, nil }

func TestChunkedBodyNoProgress(t *testing.T) {
	if _, err := readIngressBody(noProgressReader{}, 10000, true); err != io.ErrNoProgress {
		t.Fatalf("error=%v", err)
	}
}

func TestQueuedChunkedBodiesReleaseExcessReservation(t *testing.T) {
	s, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	s.cfg.Runtime.MaxBodyBytes = 4 << 20
	s.cfg.Runtime.MaxBufferedBytes = 16 << 20
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	held := make([]*routing.Lease, 4)
	for i := range held {
		var err error
		held[i], err = s.Router.Acquire(ctx, routing.Query{Model: "mock/model", Protocol: "chat"})
		if err != nil {
			t.Fatal(err)
		}
		defer held[i].Release(200, 0)
	}
	done := make(chan int, 8)
	for i := range 8 {
		r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"mock/model","messages":[]}`)).WithContext(ctx)
		r.ContentLength = -1
		r.Header.Set("Authorization", "Bearer "+testKey)
		go func() { w := httptest.NewRecorder(); s.ServeHTTP(w, r); done <- w.Code }()
		deadline := time.Now().Add(time.Second)
		for {
			reserved := s.buffered.Load()
			if s.requests.Load() == uint64(i+1) && reserved > int64(i*12000) && reserved < int64((i+1)*16000) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("small queued requests retain excess reservation: %d", reserved)
			}
			time.Sleep(time.Millisecond)
		}
	}
	for _, l := range held {
		l.Release(200, 0)
	}
	for range 8 {
		if code := <-done; code != 200 {
			t.Fatalf("status=%d", code)
		}
	}
	if got := s.buffered.Load(); got != 0 {
		t.Fatalf("reservation leak %d", got)
	}
}

func TestModelListReflectsEnableChanges(t *testing.T) {
	s, _ := setup(t, func(http.ResponseWriter, *http.Request) {})
	for _, enabled := range []bool{true, false, true} {
		s.Router.SetEnabled("mock", enabled)
		w := httptest.NewRecorder()
		s.models(w)
		var result struct {
			Object string                                 `json:"object"`
			Data   []struct{ ID, Object, OwnedBy string } `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range result.Data {
			if m.ID == "mock/model" {
				found = true
			}
		}
		if result.Object != "list" || found != enabled {
			t.Fatalf("enabled=%v result=%s", enabled, w.Body.String())
		}
	}
}
