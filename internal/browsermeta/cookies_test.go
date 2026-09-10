package browsermeta

import (
	"github.com/chromedp/cdproto/network"
	"strings"
	"testing"
)

func TestCookieHeader(t *testing.T) {
	value, count, err := cookieHeader([]*network.Cookie{{Name: "session", Value: "synthetic"}, {Name: "pref", Value: "dark"}})
	if err != nil || count != 2 || value != "session=synthetic; pref=dark" {
		t.Fatalf("unexpected serialization: count=%d err=%v", count, err)
	}
	for _, cookies := range [][]*network.Cookie{
		{{Name: "bad\r\n", Value: "secret"}}, {{Name: "session", Value: "secret; injected=true"}}, {{Name: "session", Value: strings.Repeat("x", 65537)}}, make([]*network.Cookie, 257),
	} {
		if _, _, err := cookieHeader(cookies); err == nil {
			t.Fatal("unsafe or oversized cookies accepted")
		}
	}
}
