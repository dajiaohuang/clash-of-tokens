package config

import "testing"

func TestBrowserProfilesIsolateAccounts(t *testing.T) {
	c := Default()
	c.BrowserProfiles = []BrowserProfile{{ID: "personal", Enabled: true, Engine: "chrome", CDPURL: "http://127.0.0.1:9223"}, {ID: "work", Enabled: true, Engine: "edge", CDPURL: "http://127.0.0.1:9224"}}
	c.Accounts = []Account{{ID: "one", BrowserProfileID: "personal"}, {ID: "two", BrowserProfileID: "work"}}
	if err := c.ValidateBrowserProfiles(); err != nil {
		t.Fatal(err)
	}
	a, b := c.SourceBrowser(Source{AccountID: "one"}), c.SourceBrowser(Source{AccountID: "two"})
	if a.StateFile == b.StateFile || a.CDPURL == b.CDPURL || !a.Enabled {
		t.Fatal("account browser state not isolated")
	}
	c.BrowserProfiles[1].CDPURL = "http://[::1]:9223"
	if c.ValidateBrowserProfiles() == nil {
		t.Fatal("shared port accepted")
	}
	c.BrowserProfiles[1].CDPURL = "http://example.com:9224"
	if c.ValidateBrowserProfiles() == nil {
		t.Fatal("non-loopback endpoint accepted")
	}
}
