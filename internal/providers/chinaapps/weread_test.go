package chinaapps

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const bookRequest = `{"model":"web","messages":[{"role":"user","content":"解释本书"}]}`

func TestWeReadSnapshotsAndContinuation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "sse"}[stream], func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.URL.Path != "/ai/chatv2" || r.Method != "POST" || r.Header.Get("vid") != "reader" || r.Header.Get("accessToken") != "own-token" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("wire or credential isolation")
				}
				var p map[string]any
				if json.NewDecoder(r.Body).Decode(&p) != nil {
					t.Error("payload")
				}
				if p["bookId"] != "book-1" || p["query"] != "解释本书" || p["scene"] != float64(1) {
					t.Error(p)
				}
				if n == 1 {
					if p["chatid"] != "" || p["session_id"] != "" {
						t.Error("opening IDs")
					}
				} else if p["chatid"] != "c1" || p["session_id"] != "s1" {
					t.Error("lost continuation")
				}
				switch n {
				case 1:
					io.WriteString(w, `{"chatid":"c1","session_id":"s1","request_interval":0,"result":{"text":"第一","has_more":1}}`)
				case 2:
					io.WriteString(w, `{"request_interval":0,"result":{"text":"第一章","has_more":1}}`)
				case 3:
					io.WriteString(w, `{"request_interval":0,"extra_sections":{"has_more":false}}`)
				case 4:
					io.WriteString(w, `{"result":{"has_more":0}}`)
				default:
					t.Error("extra poll")
					w.WriteHeader(500)
				}
			}))
			defer up.Close()
			t.Setenv("WEREAD_TEST", `{"vid":"reader","access_token":"own-token"}`)
			c := New(config.Source{Adapter: "weread-ai", Project: "book-1", BaseURL: up.URL, KeyEnv: "WEREAD_TEST"})
			defer c.Close()
			resp, e := c.Do(context.Background(), "chat", "web", stream, []byte(bookRequest), http.Header{"Authorization": {"Bearer caller"}, "Cookie": {"caller-secret"}})
			if e != nil {
				t.Fatal(e)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if calls.Load() != 4 || strings.Count(string(data), "第一章") != 1 || strings.Contains(string(data), "第一第一") || resp.Header.Get("X-COT-Delivery") != "buffered" {
				t.Fatalf("calls=%d body=%s", calls.Load(), data)
			}
			if stream && !strings.HasSuffix(string(data), "data: [DONE]\n\n") {
				t.Fatal("no finish")
			}
		})
	}
}

func TestWeReadRejectsMalformedAndIncomplete(t *testing.T) {
	for _, wire := range []string{`{}`, `{"errCode":-201,"message":"private"}`, `{"result":{"has_more":0}}`, `{"result":{"text":"partial","has_more":1}}`, `null`, `{"result":{"text":"x","has_more":"0"}}`} {
		t.Run(wire, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, wire) }))
			defer up.Close()
			t.Setenv("WEREAD_TEST", `{"vid":"reader","access_token":"own-token"}`)
			c := New(config.Source{Adapter: "weread-ai", Project: "book-1", BaseURL: up.URL, KeyEnv: "WEREAD_TEST"})
			defer c.Close()
			if _, e := c.Do(context.Background(), "chat", "web", false, []byte(bookRequest), nil); e == nil || strings.Contains(e.Error(), "private") {
				t.Fatalf("error=%v", e)
			}
		})
	}
}

func TestWeReadCancellationDuringPollDelay(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, `{"chatid":"c1","session_id":"s1","request_interval":1500,"result":{"text":"partial","has_more":1}}`)
	}))
	defer up.Close()
	t.Setenv("WEREAD_TEST", `{"vid":"reader","access_token":"own-token"}`)
	c := New(config.Source{Adapter: "weread-ai", Project: "book-1", BaseURL: up.URL, KeyEnv: "WEREAD_TEST"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, e := c.Do(ctx, "chat", "web", false, []byte(bookRequest), nil); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("cancelled poll repeated")
	}
}

func TestWeReadHTTPStatusAndUnsupported(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	}))
	defer up.Close()
	t.Setenv("WEREAD_TEST", `{"vid":"reader","access_token":"own-token"}`)
	c := New(config.Source{Adapter: "weread-ai", Project: "book-1", BaseURL: up.URL, KeyEnv: "WEREAD_TEST"})
	defer c.Close()
	for _, body := range []string{`{"messages":[{"role":"user","content":"x"}],"tools":[]}`, `{"messages":[{"role":"system","content":"x"}]}`} {
		if _, e := c.Do(context.Background(), "chat", "web", false, []byte(body), nil); e == nil {
			t.Fatal("unsupported accepted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unsupported reached upstream")
	}
	resp, e := c.Do(context.Background(), "chat", "web", false, []byte(bookRequest), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "30" || calls.Load() != 1 {
		t.Fatal("status or retry")
	}
}
