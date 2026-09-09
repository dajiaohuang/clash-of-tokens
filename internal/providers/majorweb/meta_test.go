package majorweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"clash-of-tokens/internal/config"
)

func TestMetaSignedInContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/graphql/" || r.Header.Get("x-fb-lsd") != "ownlsd" || r.Header.Get("Cookie") != "own=cookie" {
			t.Error("wrong Meta request")
		}
		r.ParseForm()
		if r.Form.Get("doc_id") != "7783822248314888" || r.Form.Get("fb_dtsg") != "owndtsg" || r.Form.Get("access_token") != "" {
			t.Error("wrong signed-in form")
		}
		var vars map[string]any
		json.Unmarshal([]byte(r.Form.Get("variables")), &vars)
		msg, _ := vars["message"].(map[string]any)
		if msg["sensitive_string_value"] != "hello" || vars["offlineThreadingId"] == "" || vars["externalConversationId"] == "" {
			t.Error("wrong message")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{\"data\":{\"node\":{\"bot_response_message\":{\"streaming_state\":\"STREAMING\",\"snippet\":\"draft\"}}}}\n{\"data\":{\"node\":{\"bot_response_message\":{\"streaming_state\":\"OVERALL_DONE\",\"snippet\":\"final\"}}}}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	t.Setenv("COT_TEST_META", `{"cookie":"own=cookie","lsd":"ownlsd","fb_dtsg":"owndtsg"}`)
	c := New(config.Source{Adapter: "meta-ai", BaseURL: s.URL, KeyEnv: "COT_TEST_META"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := c.Do(ctx, "chat", "meta-ai", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "final") || strings.Contains(string(b), "draft") {
		t.Fatal(string(b))
	}
}

func TestMetaRejectsInvalidCompletion(t *testing.T) {
	for _, raw := range []string{
		`{"errors":[{"message":"secret"}]}`,
		`{"data":{"node":{"bot_response_message":{"streaming_state":"STREAMING","snippet":"partial"}}}}`,
		`{"data":{"node":{"bot_response_message":{"streaming_state":"OVERALL_DONE","snippet":"","imagine_card":{}}}}}`,
		`{"data":{"node":{"bot_response_message":{"streaming_state":"OVERALL_DONE","snippet":1}}}}`,
		`{"data":{"node":{"bot_response_message":{"streaming_state":"UNKNOWN","snippet":"text"}}}}`,
	} {
		if _, err := readMeta(strings.NewReader(raw)); err == nil {
			t.Fatal("accepted invalid completion")
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("leaked upstream error")
		}
	}
}
