// cot-bench measures an isolated loopback fixture, never an external account.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"clash-of-tokens/internal/api"
	"clash-of-tokens/internal/config"
)

type result struct {
	Path              string  `json:"path"`
	Requests          int     `json:"requests"`
	Concurrency       int     `json:"concurrency"`
	Errors            int     `json:"errors"`
	ErrorSample       string  `json:"error_sample,omitempty"`
	Seconds           float64 `json:"seconds"`
	RequestsPerSecond float64 `json:"requests_per_second"`
	TTFTP50MS         float64 `json:"ttft_p50_ms"`
	TTFTP95MS         float64 `json:"ttft_p95_ms"`
	TTFTP99MS         float64 `json:"ttft_p99_ms"`
	BodyBytes         int     `json:"body_bytes"`
	PeakGoroutines    int     `json:"peak_goroutines"`
	PeakGoHeapBytes   uint64  `json:"peak_go_heap_bytes"`
	GCCycles          uint32  `json:"gc_cycles"`
}

func main() {
	concurrency := flag.Int("concurrency", 100, "simultaneous loopback streams")
	requests := flag.Int("requests", 1000, "requests per path")
	delay := flag.Duration("delay", 10*time.Millisecond, "mock delay before each of 4 chunks")
	bodyBytes := flag.Int("body-bytes", 0, "total JSON request bytes, 0 uses small fixture; maximum 16 MiB")
	flag.Parse()
	if *concurrency < 1 || *concurrency > 1000 || *requests < 1 || *requests > 100000 || *delay < 0 || *delay > time.Second || *bodyBytes < 0 || *bodyBytes > 16<<20 {
		fmt.Fprintln(os.Stderr, "invalid benchmark limits")
		os.Exit(2)
	}
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		for range 4 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(*delay):
			}
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"test\"}}]}\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer mock.Close()
	c := config.Default()
	c.Runtime.MaxInflight = *concurrency
	c.Runtime.MaxQueued = *concurrency
	payload := `{"model":"mock/model","messages":[{"role":"user","content":"fixture"}],"stream":true}`
	if *bodyBytes > 0 {
		if *bodyBytes < len(payload) {
			fmt.Fprintln(os.Stderr, "body-bytes is smaller than fixture JSON")
			os.Exit(2)
		}
		payload = strings.Replace(payload, "fixture", strings.Repeat("x", *bodyBytes-len(payload)+len("fixture")), 1)
	}
	// This fixture emits under 1 KiB. Explicitly budget the requested wave
	// rather than measuring expected admission rejection at default limits.
	c.Runtime.MaxOutputBytes = 64 << 10
	c.Runtime.MaxBufferedBytes = max(c.Runtime.MaxBufferedBytes, int64(*concurrency)*(4*c.Runtime.MaxOutputBytes+2*int64(len(payload))+32768))
	c.Sources = []config.Source{{ID: "mock", Provider: "fixture", Adapter: "openai", BaseURL: mock.URL + "/v1", Enabled: true, Local: true, MaxInflight: *concurrency, QuotaDomain: "fixture", QuotaMaxInflight: *concurrency, Models: []config.Model{{ID: "model", Upstream: "real", Protocols: []string{"chat"}, Tier: "unrated", Tools: "none", MaxInputBytes: 1 << 20}}}}
	c.Sources[0].Models[0].MaxInputBytes = 16 << 20
	gateway, e := api.NewWithKeys(c, "fixture-data-key-0123456789", "fixture-admin-key-0123456789")
	if e != nil {
		panic(e)
	}
	defer gateway.Close()
	server := httptest.NewServer(gateway)
	defer server.Close()
	outputs := []result{load("direct_mock", mock.URL, payload, *requests, *concurrency), load("gateway", server.URL, payload, *requests, *concurrency)}
	report := struct {
		GoVersion      string   `json:"go_version"`
		Platform       string   `json:"platform"`
		Scope          string   `json:"scope"`
		BufferedBudget int64    `json:"buffered_budget_bytes"`
		OutputLimit    int64    `json:"output_limit_bytes"`
		Results        []result `json:"results"`
	}{runtime.Version(), runtime.GOOS + "/" + runtime.GOARCH, "In-process loopback mock and client; excludes browser/provider latency. TTFT is first nonempty SSE data line. No RSS/CPU certification.", c.Runtime.MaxBufferedBytes, c.Runtime.MaxOutputBytes, outputs}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
	for _, r := range outputs {
		if r.Errors > 0 {
			os.Exit(1)
		}
	}
}
func load(label, url, payload string, n, concurrency int) result {
	var initial runtime.MemStats
	runtime.ReadMemStats(&initial)
	peakHeap, peakGoroutines := initial.HeapAlloc, runtime.NumGoroutine()
	stopSampling, samplingDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(samplingDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				peakHeap = max(peakHeap, m.HeapAlloc)
				peakGoroutines = max(peakGoroutines, runtime.NumGoroutine())
			}
		}
	}()
	transport := &http.Transport{MaxIdleConns: concurrency, MaxIdleConnsPerHost: concurrency, MaxConnsPerHost: concurrency}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 30 * time.Second}
	times := make([]float64, n)
	bad := make([]bool, n)
	errorsText := make([]string, n)
	jobs := make(chan int)
	var wg sync.WaitGroup
	start := time.Now()
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				before := time.Now()
				r, _ := http.NewRequest("POST", url+"/v1/chat/completions", strings.NewReader(payload))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer fixture-data-key-0123456789")
				response, e := client.Do(r)
				if e != nil {
					bad[i] = true
					errorsText[i] = e.Error()
					continue
				}
				scan := bufio.NewScanner(response.Body)
				first := false
				done := false
				for scan.Scan() {
					line := scan.Text()
					if strings.HasPrefix(line, "data: ") {
						if !first {
							times[i] = float64(time.Since(before).Microseconds()) / 1000
							first = true
						}
						if line == "data: [DONE]" {
							done = true
						}
					}
				}
				bad[i] = response.StatusCode != 200 || scan.Err() != nil || !first || !done
				if bad[i] {
					errorsText[i] = fmt.Sprintf("status=%d scan=%v first=%v done=%v", response.StatusCode, scan.Err(), first, done)
				}
				response.Body.Close()
			}
		}()
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	seconds := time.Since(start).Seconds()
	valid := make([]float64, 0, n)
	errors := 0
	sample := ""
	for i := range n {
		if bad[i] {
			errors++
			if sample == "" {
				sample = errorsText[i]
			}
		} else {
			valid = append(valid, times[i])
		}
	}
	sort.Float64s(valid)
	percentile := func(p float64) float64 {
		if len(valid) == 0 {
			return 0
		}
		return valid[int(float64(len(valid)-1)*p)]
	}
	close(stopSampling)
	<-samplingDone
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	return result{label, n, concurrency, errors, sample, seconds, float64(n-errors) / seconds, percentile(.5), percentile(.95), percentile(.99), len(payload), peakGoroutines, peakHeap, final.NumGC - initial.NumGC}
}
