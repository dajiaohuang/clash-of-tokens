package main

import (
	"clash-of-tokens/internal/config"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func browserLogin(c config.Config) error {
	if !c.Browser.Enabled {
		return fmt.Errorf("set browser.enabled before starting the login browser")
	}
	u, e := url.Parse(c.Browser.CDPURL)
	if e != nil || u.Port() == "" {
		return fmt.Errorf("browser.cdp_url requires a port")
	}
	client := http.Client{Timeout: time.Second}
	if r, e := client.Get(c.Browser.CDPURL + "/json/version"); e == nil {
		r.Body.Close()
		if r.StatusCode == 200 {
			fmt.Println("A browser is already listening at", c.Browser.CDPURL, "— use its ChatGPT tab to sign in.")
			return nil
		}
	}
	paths := []string{"google-chrome", "chromium", "chromium-browser", "microsoft-edge"}
	if runtime.GOOS == "windows" {
		paths = []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`}
	} else if runtime.GOOS == "darwin" {
		paths = []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
	}
	executable := ""
	for _, path := range paths {
		if resolved, e := exec.LookPath(path); e == nil {
			executable = resolved
			break
		}
	}
	if executable == "" {
		return fmt.Errorf("Chrome/Chromium not found; start a browser manually with the documented CDP flags")
	}
	profile, e := filepath.Abs(filepath.Join(filepath.Dir(c.Browser.StateFile), "browser-profile"))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(profile, 0700); e != nil {
		return e
	}
	cmd := exec.Command(executable, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port="+u.Port(), "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "https://chatgpt.com/?model=auto")
	if e = cmd.Start(); e != nil {
		return e
	}
	_ = cmd.Process.Release()
	fmt.Println("Opened a dedicated ChatGPT browser. Sign in normally, then run clash-tokens doctor.")
	fmt.Println("The browser stays under your control; closing the gateway does not close it.")
	return nil
}
