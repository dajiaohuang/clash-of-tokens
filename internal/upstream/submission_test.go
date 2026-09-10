package upstream

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"clash-of-tokens/internal/config"
)

func TestTransportSubmissionClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	defer server.Close()
	for _, mode := range []string{"dial", "submitted", "custom", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			c := newHTTP(config.Source{Adapter: "openai", Local: true, BaseURL: server.URL, MaxInflight: 1})
			defer c.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "dial":
				c.http.Transport.(*http.Transport).DialContext = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("dial failed") }
			case "custom":
				c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("unknown delivery") })
			case "canceled":
				cancel()
			}
			_, err := c.Do(ctx, "chat", "test", false, []byte(`{"model":"test"}`), nil)
			var transport *TransportError
			if !errors.As(err, &transport) {
				t.Fatalf("expected transport classification, got %v", err)
			}
			if transport.BeforeSubmission != (mode == "dial") {
				t.Fatalf("%s classified before submission=%v", mode, transport.BeforeSubmission)
			}
		})
	}
}
