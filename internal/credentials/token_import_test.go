package credentials

import "testing"

func TestSelectedCLIAccessTokenFormats(t *testing.T) {
	for _, tc := range []struct{ format, data string }{{"codex", `{"tokens":{"access_token":"selected-access","refresh_token":"not-imported"}}`}, {"gemini-cli", `{"access_token":"selected-access","refresh_token":"not-imported"}`}, {"oauth", `{"access_token":"selected-access"}`}} {
		got, err := ParseCLIAccessToken(tc.format, []byte(tc.data))
		if err != nil || got != "selected-access" {
			t.Fatal(tc.format, err)
		}
	}
	for _, data := range []string{`{}`, `{"access_token":"bad\nheader"}`, `{"access_token":42}`, `{"refresh_token":"not-an-access-token"}`} {
		if _, err := ParseCLIAccessToken("oauth", []byte(data)); err == nil {
			t.Fatal("invalid export accepted")
		}
	}
}
