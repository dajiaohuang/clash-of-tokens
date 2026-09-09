package chinaremaining

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"clash-of-tokens/internal/config"
)

func testSource(adapter, base string) config.Source {
	return config.Source{ID: adapter, Adapter: adapter, BaseURL: base, KeyEnv: "COT_CHINA_REMAINING_TEST", MaxInflight: 1}
}

func sse(w http.ResponseWriter, values ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, value := range values {
		_, _ = io.WriteString(w, "data: "+value+"\n\n")
	}
}

func TestEmohaaContractAndXSS(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", "emo-token")
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer emo-token" || r.Header.Get("Origin") != emohaaOrigin {
			t.Fatalf("auth/origin = %q/%q", r.Header.Get("Authorization"), r.Header.Get("Origin"))
		}
		if r.Header.Get("X-Xss-Id") == "" || r.Header.Get("X-Xss-Ts") == "" {
			t.Fatal("missing XSS headers")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /echo-prod/generate/id":
			if r.URL.Query().Get("create") != "true" {
				t.Fatal("missing create=true")
			}
			_, _ = io.WriteString(w, `{"id":"emo-conv"}`)
		case "GET /echo-prod/chat":
			q := r.URL.Query()
			if q.Get("cid") != "emo-conv" || q.Get("role") != "echo" || q.Get("prompt") != "user:hello\n" {
				t.Fatalf("query = %#v", q)
			}
			ts, id := q.Get("xts"), q.Get("xid")
			sum := md5.Sum([]byte(ts + "-_-" + id))
			if q.Get("xreal") != fmt.Sprintf("%x", sum) || r.Header.Get("X-Xss-Ts") != ts || r.Header.Get("X-Xss-Id") != id {
				t.Fatal("XSS query/header mismatch")
			}
			sse(w, "hello ", "world", "[DONE]")
		case "DELETE /echo-prod/conv":
			if r.URL.Query().Get("cid") != "emo-conv" {
				t.Fatal("wrong conversation cleanup")
			}
			deleted = true
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(testSource(AdapterEmohaa, server.URL))
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "emohaa", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Object != "chat.completion" || len(result.Choices) != 1 || result.Choices[0].Message.Content != "hello world" {
		t.Fatalf("result = %#v", result)
	}
	if !deleted {
		t.Fatal("conversation was not deleted")
	}
}

func TestSparkContractMultipartBase64AndCleanup(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", `{"sso_session_id":"sso-1","gt_token":"gt-test"}`)
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != sparkOrigin || !strings.Contains(r.Header.Get("Cookie"), "ssoSessionId=sso-1") {
			t.Fatalf("origin/cookie = %q/%q", r.Header.Get("Origin"), r.Header.Get("Cookie"))
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /iflygpt/u/chat-list/v1/create-chat-list":
			_, _ = io.WriteString(w, `{"code":0,"data":{"id":"spark-conv"}}`)
		case "POST /iflygpt-chat/u/chat_message/chat":
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			fields := map[string]string{}
			for {
				part, e := mr.NextPart()
				if errors.Is(e, io.EOF) {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				value, e := io.ReadAll(part)
				if e != nil {
					t.Fatal(e)
				}
				fields[part.FormName()] = string(value)
			}
			if fields["fd"] == "" || len(fields["fd"]) != 6 || fields["chatId"] != "spark-conv" || fields["GtToken"] != "gt-test" || fields["text"] != "user:hello\nassistant:" {
				t.Fatalf("multipart fields = %#v", fields)
			}
			sse(w, "aGVsbG8g", "<sid>", "d29ybGQ=", "<end>")
		case "POST /iflygpt/u/chat-list/v1/del-chat-list":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["chatListId"] != "spark-conv" {
				t.Fatalf("cleanup payload = %#v err=%v", payload, err)
			}
			deleted = true
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(testSource(AdapterSpark, server.URL))
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "spark", false, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content != "hello world" {
		t.Fatalf("result = %#v", result)
	}
	if !deleted {
		t.Fatal("conversation was not deleted")
	}
}

func TestSparkPreparationRequiresExplicitGtToken(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", `{"sso_session_id":"sso-1"}`)
	client := New(testSource(AdapterSpark, "http://127.0.0.1:1"))
	defer client.Close()
	if _, err := client.Do(context.Background(), "chat", "spark", false, []byte(`{"messages":[{"role":"user","content":"x"}]}`), nil); !errors.Is(err, ErrCredential) {
		t.Fatalf("missing gt_token error = %v", err)
	}
	messages := []chatMessage{{Role: "user", Content: json.RawMessage(`"old"`)}, {Role: "assistant", Content: json.RawMessage(`"answer"`)}, {Role: "user", Content: json.RawMessage(`"new"`)}}
	got := prepareSpark(messages)
	if !strings.HasPrefix(got, "user:old\nassistant:answer\nsystem:") || !strings.HasSuffix(got, "\nuser:new\nassistant:") {
		t.Fatalf("prepared prompt = %q", got)
	}
}

func TestUnsupportedRequestsFailClosed(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", "sso")
	client := New(testSource(AdapterSpark, "http://127.0.0.1:1"))
	defer client.Close()
	for _, body := range []string{
		`{"messages":[{"role":"tool","content":"x"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"text","text":"x"}]}]}`,
		`{"messages":[{"role":"user","content":"x"}],"tools":[]}`,
	} {
		if _, err := client.Do(context.Background(), "chat", "spark", false, []byte(body), nil); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("body=%s error=%v", body, err)
		}
	}
	h := http.Header{}
	h.Set("X-COT-Session", "caller")
	if _, err := client.Do(context.Background(), "chat", "spark", false, []byte(`{"messages":[{"role":"user","content":"x"}]}`), h); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("session error=%v", err)
	}
}

func TestBaseURLGuard(t *testing.T) {
	if _, err := baseURL(config.Source{BaseURL: "http://example.com"}, defaultSparkBase); err == nil {
		t.Fatal("external HTTP base URL accepted")
	}
}

func TestQwenCNContractAndCumulativeStream(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", `{"accounts":[{"cookie":"uid=1; sid=2","xsrf_token":"xsrf"}],"model_accounts":{"qwen-plus":0}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "uid=1; sid=2" || r.Header.Get("X-XSRF-Token") != "xsrf" || r.Header.Get("X-Platform") != "pc_tongyi" {
			t.Fatalf("Qwen headers = %#v", r.Header)
		}
		switch r.URL.Path {
		case "/assistant/api/record/list":
			_, _ = io.WriteString(w, `{}`)
		case "/dialog/conversation":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["action"] != "next" || payload["model"] != "qwen-plus" {
				t.Fatalf("Qwen payload = %#v err=%v", payload, err)
			}
			sse(w, `{"contents":[{"contentType":"text","content":"hello"}]}`, `{"contents":[{"contentType":"text","content":"hello world"}]}`, `[DONE]`)
		default:
			t.Fatalf("unexpected Qwen request %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(testSource(AdapterQwenCN, server.URL))
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "qwen-plus", false, []byte(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content != "hello world" {
		t.Fatalf("Qwen result = %#v", result)
	}
}

func TestMetasoContractAndEventStream(t *testing.T) {
	t.Setenv("COT_CHINA_REMAINING_TEST", `{"token":"uid-sid"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "uid=uid") {
			t.Fatalf("Metaso cookie = %q", r.Header.Get("Cookie"))
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<meta id="meta-token" content="meta-1">`)
		case "POST /api/session":
			if r.Header.Get("Token") != "meta-1" {
				t.Fatalf("Metaso token = %q", r.Header.Get("Token"))
			}
			_, _ = io.WriteString(w, `{"data":{"id":"metaso-conv"}}`)
		case "GET /api/searchV2":
			if r.URL.Query().Get("sessionId") != "metaso-conv" || r.URL.Query().Get("mode") != "detail" {
				t.Fatalf("Metaso query = %#v", r.URL.Query())
			}
			sse(w, `{"type":"append-text","text":"[[1]]hello "}`, `{"type":"append-text","text":"world"}`, `[DONE]`)
		default:
			t.Fatalf("unexpected Metaso request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(testSource(AdapterMetaso, server.URL))
	defer client.Close()
	resp, err := client.Do(context.Background(), "chat", "detail", false, []byte(`{"messages":[{"role":"user","content":"search"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Choices) != 1 || result.Choices[0].Message.Content != "hello world" {
		t.Fatalf("Metaso result = %#v", result)
	}
}

func TestConvertedResponsesOmitSyntheticUsage(t *testing.T) {
	input := "data: hello\n\ndata: [DONE]\n\n"
	for _, stream := range []bool{false, true} {
		var out bytes.Buffer
		if err := decodeUpstream(context.Background(), strings.NewReader(input), "emohaa", AdapterEmohaa, &out, stream); err != nil {
			t.Fatalf("stream=%v decode: %v", stream, err)
		}
		if strings.Contains(out.String(), "usage") || strings.Contains(out.String(), "prompt_tokens") {
			t.Fatalf("stream=%v fabricated usage: %s", stream, out.String())
		}
	}
}

func TestMetasoCleanEOFRequiresTextAndAcceptsText(t *testing.T) {
	var out bytes.Buffer
	if err := decodeUpstream(context.Background(), strings.NewReader("data: {\"type\":\"append-text\",\"text\":\"hello\"}\n\n"), "detail", AdapterMetaso, &out, false); err != nil {
		t.Fatalf("clean EOF with text: %v", err)
	}
	if !strings.Contains(out.String(), `"content":"hello"`) {
		t.Fatalf("clean EOF result = %s", out.String())
	}

	out.Reset()
	if err := decodeUpstream(context.Background(), strings.NewReader("data: [DONE]\n\n"), "detail", AdapterMetaso, &out, false); !errors.Is(err, ErrTruncated) {
		t.Fatalf("empty DONE error = %v", err)
	}
}

func TestMetasoErrorEventFailsInsteadOfBecomingText(t *testing.T) {
	var out bytes.Buffer
	err := decodeUpstream(context.Background(), strings.NewReader("data: {\"type\":\"error\",\"code\":\"E_LIMIT\",\"msg\":\"secret upstream detail\"}\n\ndata: [DONE]\n\n"), "detail", AdapterMetaso, &out, false)
	if !errors.Is(err, ErrUpstreamEvent) {
		t.Fatalf("Metaso error event = %v", err)
	}
	if strings.Contains(out.String(), "secret upstream detail") || out.Len() != 0 {
		t.Fatalf("Metaso error leaked as output: %q", out.String())
	}
}

func TestQwenRevisionIsRejectedForStreaming(t *testing.T) {
	input := "data: {\"contents\":[{\"contentType\":\"text\",\"content\":\"hello\"}]}\n\ndata: {\"contents\":[{\"contentType\":\"text\",\"content\":\"replacement\"}]}\n\ndata: [DONE]\n\n"
	var out bytes.Buffer
	err := decodeUpstream(context.Background(), strings.NewReader(input), "qwen-plus", AdapterQwenCN, &out, true)
	if !errors.Is(err, ErrRevision) {
		t.Fatalf("Qwen revision error = %v", err)
	}
	if strings.Count(out.String(), "hello") != 1 || strings.Contains(out.String(), "replacement") {
		t.Fatalf("Qwen revision was duplicated/accepted: %q", out.String())
	}
}

func TestQwenErrorAndStatusEventsFailClosed(t *testing.T) {
	for _, event := range []string{`{"error":"upstream failed"}`, `{"status":"failed"}`, `{"contents":[{"contentType":"status","content":"failed"}]}`} {
		var out bytes.Buffer
		err := decodeUpstream(context.Background(), strings.NewReader("data: "+event+"\n\ndata: [DONE]\n\n"), "qwen-plus", AdapterQwenCN, &out, false)
		if !errors.Is(err, ErrUpstreamEvent) {
			t.Fatalf("Qwen event %s error = %v", event, err)
		}
		if out.Len() != 0 {
			t.Fatalf("Qwen event %s produced output: %q", event, out.String())
		}
	}
}

type errorAfterReader struct {
	data []byte
	done bool
}

func (r *errorAfterReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, errors.New("transport read failed")
}

func TestMetasoTransportReadErrorIsNotCleanEOF(t *testing.T) {
	var out bytes.Buffer
	err := decodeUpstream(context.Background(), &errorAfterReader{data: []byte("data: {\"type\":\"append-text\",\"text\":\"hello\"}\n\n")}, "detail", AdapterMetaso, &out, false)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("transport error = %v", err)
	}
}
