package chinafinal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestTencentRequestUsesPinnedRouteModelAndOwnCookie(t *testing.T) {
	const cookie = "hy_token=source-cookie"
	t.Setenv("CHINA_TAS_COOKIE", cookie)
	received := make(chan *http.Request, 1)
	bodySeen := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r
		body, _ := io.ReadAll(r.Body)
		bodySeen <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	client := New(config.Source{Adapter: AdapterTencentAIStudio, BaseURL: server.URL, KeyEnv: "CHINA_TAS_COOKIE"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "hy3-g", false,
		[]byte(`{"model":"caller-model","messages":[{"role":"user","content":"你好"}],"stream":false}`),
		http.Header{"Cookie": []string{"caller=must-not-cross"}})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	r := <-received
	if r.Method != http.MethodPost || r.URL.Path != "/api/chat/HunyuanDefault" {
		t.Fatalf("request = %s %s", r.Method, r.URL.Path)
	}
	if got := r.Header.Get("Cookie"); got != cookie {
		t.Fatalf("Cookie = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != "" {
		t.Fatalf("unexpected Authorization = %q", got)
	}
	var payload struct {
		Model    string            `json:"model"`
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(<-bodySeen, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "HunyuanDefault" || len(payload.Messages) != 1 {
		t.Fatalf("payload model/messages = %q/%d", payload.Model, len(payload.Messages))
	}
}

func TestTencentResponseCloseCancelsDelayedBody(t *testing.T) {
	requestDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		close(requestDone)
	}))
	defer server.Close()

	t.Setenv("CHINA_TAS_COOKIE", "session=delayed")
	client := New(config.Source{Adapter: AdapterTencentAIStudio, BaseURL: server.URL, KeyEnv: "CHINA_TAS_COOKIE"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "hunyuan-3d", true,
		[]byte(`{"messages":[{"role":"user","content":"wait"}]}`), nil)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("closing response body did not cancel delayed request")
	}
}

func TestUnsupportedCatalogEntriesFailClosed(t *testing.T) {
	client := New(config.Source{Adapter: AdapterMetaso})
	_, err := client.Do(context.Background(), "chat", "any", false,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	if Supports(AdapterMetaso) || Supports(AdapterEmohaa) || Supports(AdapterSpark) {
		t.Fatal("catalog-only entries reported as supported")
	}
}

func TestInvalidChatRequestFailsBeforeNetwork(t *testing.T) {
	client := New(config.Source{Adapter: AdapterTencentAIStudio, BaseURL: "https://aistudio.tencent.ai", KeyEnv: "MISSING"})
	_, err := client.Do(context.Background(), "chat", "hy3-g", false,
		[]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`), nil)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "text content") {
		t.Fatalf("error = %v", err)
	}
}

func TestUnsupportedRequestsExposeHTTP422(t *testing.T) {
	var classified interface{ HTTPStatus() int }
	if !errors.As(ErrUnsupported, &classified) || classified.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("ErrUnsupported HTTP status = %v, want 422", classified)
	}

	client := New(config.Source{Adapter: AdapterTencentAIStudio, KeyEnv: "MISSING"})
	_, err := client.Do(context.Background(), "responses", "hy3-g", false,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if !errors.As(err, &classified) || classified.HTTPStatus() != http.StatusUnprocessableEntity {
		t.Fatalf("request error = %v, want HTTP 422 classification", err)
	}
}

func TestTencentRejectsUnknownRoleAndSession(t *testing.T) {
	client := New(config.Source{Adapter: AdapterTencentAIStudio, KeyEnv: "MISSING"})
	defer client.Close()
	_, err := client.Do(context.Background(), "chat", "hy3-g", false,
		[]byte(`{"messages":[{"role":"tool","content":"hi"}]}`), nil)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), `role "tool"`) {
		t.Fatalf("unknown-role error = %v", err)
	}
	h := http.Header{}
	h.Set("X-COT-Session", "caller-session")
	_, err = client.Do(context.Background(), "chat", "hy3-g", false,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), h)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "X-COT-Session") {
		t.Fatalf("session error = %v", err)
	}
}

func TestTencentNonStreamingConvertsOpenAISSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"id\":\"cmpl-1\",\"model\":\"HunyuanDefault\",\"created\":10,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"id\":\"cmpl-1\",\"model\":\"HunyuanDefault\",\"created\":10,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"+"data: [DONE]\n\n")
	}))
	defer server.Close()
	t.Setenv("CHINA_TAS_COOKIE", "session=stream")
	client := New(config.Source{Adapter: AdapterTencentAIStudio, BaseURL: server.URL, KeyEnv: "CHINA_TAS_COOKIE"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "hy3-g", false,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
	var completion struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		t.Fatal(err)
	}
	if completion.Object != "chat.completion" || len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "hello" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestTencentStreamingConvertsOpenAIJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"cmpl-2","model":"HunyuanDefault","created":11,"choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	t.Setenv("CHINA_TAS_COOKIE", "session=json")
	client := New(config.Source{Adapter: AdapterTencentAIStudio, BaseURL: server.URL, KeyEnv: "CHINA_TAS_COOKIE"})
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "hy3-g", true,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(data), `"content":"hello"`) || !strings.HasSuffix(string(data), "data: [DONE]\n\n") {
		t.Fatalf("converted stream headers/body = %q / %q", resp.Header.Get("Content-Type"), data)
	}
}
