package majorweb

import (
	"clash-of-tokens/internal/config"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMonicaContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/custom_bot/chat" || r.Header.Get("Cookie") != "own=cookie" {
			t.Error("wrong request")
		}
		var p struct {
			Bot  string `json:"bot_uid"`
			Data struct {
				Conv   string `json:"conversation_id"`
				Parent string `json:"pre_parent_item_id"`
				Items  []struct {
					ID     string `json:"item_id"`
					Parent string `json:"parent_item_id"`
					Data   struct {
						Content string `json:"content"`
					} `json:"data"`
				} `json:"items"`
			} `json:"data"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Bot != "gpt_4_o_chat" || len(p.Data.Items) != 2 {
			t.Error("wrong payload")
			return
		}
		if p.Data.Items[0].Data.Content != "__RENDER_BOT_WELCOME_MSG__" || p.Data.Items[1].Data.Content != "hello" || p.Data.Items[1].Parent != p.Data.Items[0].ID || p.Data.Parent != p.Data.Items[1].ID {
			t.Error("broken message chain")
		}
		io.WriteString(w, "data: {\"text\":\"answer\",\"finished\":false}\n\ndata: {\"text\":\"!\",\"finished\":true}\n\n")
	}))
	defer srv.Close()
	t.Setenv("COT_TEST_MONICA", "own=cookie")
	c := New(config.Source{Adapter: "monica", BaseURL: srv.URL, KeyEnv: "COT_TEST_MONICA"})
	defer c.Close()
	r, e := c.Do(context.Background(), "chat", "gpt-4o", true, []byte(`{"messages":[{"role":"user","content":"hello"}]}`), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(b), "answer!") {
		t.Fatal(string(b))
	}
}

func TestMonicaRejectsInvalid(t *testing.T) {
	for _, raw := range []string{"data: {\"text\":\"partial\"}\n\n", "data: {\"code\":401,\"msg\":\"secret\"}\n\n", "data: {\"error\":\"secret\"}\n\n", "data: {\"text\":1,\"finished\":true}\n\n"} {
		if _, e := readMonica(strings.NewReader(raw)); e == nil {
			t.Fatal("accepted invalid response")
		}
	}
}
