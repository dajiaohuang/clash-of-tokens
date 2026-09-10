package browsermeta

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Cookies reads only cookies applicable to the explicit provider URL. It owns
// and closes a new blank tab, leaving the user's existing tabs untouched.
func Cookies(ctx context.Context, endpoint, destination string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	allocator, stop := chromedp.NewRemoteAllocator(ctx, endpoint)
	defer stop()
	tab, closeTab := chromedp.NewContext(allocator)
	defer closeTab()
	var cookies []*network.Cookie
	err := chromedp.Run(tab, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		cookies, err = network.GetCookies().WithURLs([]string{destination}).Do(ctx)
		return err
	}))
	if err != nil {
		return "", 0, fmt.Errorf("cannot read selected provider cookies from configured browser")
	}
	return cookieHeader(cookies)
}

func cookieHeader(cookies []*network.Cookie) (string, int, error) {
	if len(cookies) > 256 {
		return "", 0, fmt.Errorf("selected provider has too many cookies")
	}
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		cookie := http.Cookie{Name: c.Name, Value: c.Value}
		if cookie.Valid() != nil {
			return "", 0, fmt.Errorf("selected provider contains an unsupported cookie")
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	value := strings.Join(parts, "; ")
	if len(value) > 64<<10 {
		return "", 0, fmt.Errorf("selected provider cookies exceed 64 KiB")
	}
	return value, len(parts), nil
}
