package browserexec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// OpenCDP keeps the transport alive for tab cleanup after request cancellation.
// Initial target creation has a separate three-second bound so cancellation
// cannot discard a successfully created target before ownership is recorded.
func OpenCDP(parent context.Context, endpoint string, private ...bool) (context.Context, func(), error) {
	return openCDP(parent, endpoint, 3*time.Second, private...)
}

// OpenAuthorizedCDP permits the user's native browser authorization dialog to
// complete. The connection and one new tab are retained for the whole scan.
func OpenAuthorizedCDP(parent context.Context, endpoint string) (context.Context, func(), error) {
	return openCDP(parent, endpoint, 45*time.Second)
}
func openCDP(parent context.Context, endpoint string, wait time.Duration, private ...bool) (context.Context, func(), error) {
	allocator, stopAllocator := chromedp.NewRemoteAllocator(context.Background(), endpoint)
	options := []chromedp.ContextOption{}
	if len(private) > 0 && private[0] {
		options = append(options, chromedp.WithNewBrowserContext())
	}
	root, stopRoot := chromedp.NewContext(allocator, options...)
	tab, stopTab := root, func() {}
	err := initializeCDPWithTimeout(tab, wait)
	if err != nil && len(private) > 0 && private[0] && strings.Contains(err.Error(), "no browser is open") {
		current := chromedp.FromContext(root)
		if current != nil && current.Browser != nil && current.BrowserContextID != "" {
			creation, stopCreation := context.WithTimeout(root, 3*time.Second)
			id, createErr := target.CreateTarget("about:blank").WithBrowserContextID(current.BrowserContextID).WithNewWindow(true).Do(cdp.WithExecutor(creation, current.Browser))
			stopCreation()
			err = createErr
			if err == nil {
				tab, stopTab = chromedp.NewContext(root, chromedp.WithTargetID(id))
				err = initializeCDP(tab)
			}
		}
	}
	if err != nil {
		stopTab()
		stopRoot()
		stopAllocator()
		return nil, func() {}, err
	}
	action, cancel := context.WithCancel(tab)
	stopPropagation := context.AfterFunc(parent, cancel)
	var once sync.Once
	close := func() {
		once.Do(func() {
			stopPropagation()
			cancel()
			// The remote browser reader belongs to root. Close our target
			// while root is still alive; canceling root first races its
			// transport shutdown against chromedp's automatic tab cleanup.
			cleanup, stopCleanup := context.WithTimeout(context.Background(), 3*time.Second)
			current := chromedp.FromContext(tab)
			if current != nil && current.Browser != nil && current.Target != nil {
				_ = target.CloseTarget(current.Target.TargetID).Do(cdp.WithExecutor(cleanup, current.Browser))
			}
			owner := chromedp.FromContext(root)
			if len(private) > 0 && private[0] && owner != nil && owner.Browser != nil && owner.BrowserContextID != "" {
				_ = target.DisposeBrowserContext(owner.BrowserContextID).Do(cdp.WithExecutor(cleanup, owner.Browser))
			}
			stopCleanup()
			stopTab()
			stopRoot()
			stopAllocator()
		})
	}
	if parent.Err() != nil {
		close()
		return nil, func() {}, parent.Err()
	}
	return action, close, nil
}

func initializeCDP(ctx context.Context) error {
	return initializeCDPWithTimeout(ctx, 3*time.Second)
}
func initializeCDPWithTimeout(ctx context.Context, wait time.Duration) error {
	// chromedp binds its browser/target reader loops to the first Run context.
	// That context must survive initialization and remain alive until cleanup.
	done := make(chan error, 1)
	go func() { done <- chromedp.Run(ctx) }()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errors.New("browser initialization timed out")
	}
}
