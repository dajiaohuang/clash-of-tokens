package browsermeta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectionStatusDoesNotExposePageURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/version" {
			fmt.Fprint(w, `{"Browser":"Chrome/Test","Protocol-Version":"1.3"}`)
		} else {
			fmt.Fprint(w, `[{"type":"page","url":"https://private.example","title":"private title"}]`)
		}
	}))
	defer server.Close()
	status := Check(context.Background(), "profile", server.URL)
	if status.State != "connected" || status.Pages != 1 || status.Authentication != "not_checked" {
		t.Fatal(status)
	}
	data, _ := json.Marshal(status)
	if strings.Contains(string(data), "private") {
		t.Fatal("page information leaked")
	}
}
