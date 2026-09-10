package credentials

import "testing"

func TestPasswordManagerFormats(t *testing.T) {
	for _, tc := range []struct {
		format, data   string
		count, skipped int
	}{
		{"bitwarden-json", `{"encrypted":false,"items":[{"type":1,"name":"Login","login":{"username":"user","password":"secret","uris":[{"uri":"https://example.com"},{"uri":"example.org"}]}},{"type":2,"name":"Note","notes":"private note"}]}`, 2, 1},
		{"bitwarden-csv", "type,name,login_uri,login_username,login_password,notes\nlogin,Login,example.com,user,secret,private\nnote,Note,,,,private\n", 1, 1},
		{"1password-csv", "Title,Website,Username,Password,Notes\nLogin,https://example.com,user,secret,private\nPassword,,,secret,private\n", 1, 1},
		{"keepassxc-csv", "Group,Title,Username,Password,URL,Notes,TOTP\nGroup,Login,user,secret,https://example.com,private,seed\n", 1, 0},
		{"protonpass-csv", "type,name,url,email,username,password,note\nlogin,Login,https://example.com,mail@example.com,,secret,private\nnote,Note,,,,,private\n", 1, 1},
		{"dashlane-csv", "Type,name,url,username,password,note\nLogin,Login,https://example.com,user,secret,private\nSecure Note,Note,,,,private\n", 1, 1},
		{"nordpass-csv", "name,url,username,password,note,cardnumber\nLogin,https://example.com,user,secret,private,\nCard,,,,,12345\n", 1, 1},
		{"apple-passwords-csv", "Title,URL,Username,Password,OTPAuth,Notes\nLogin,https://example.com,user,secret,private-seed,private\n", 1, 0},
		{"google-passwords-csv", "name,url,username,password,note\nLogin,https://example.com,user,secret,private\n", 1, 0},
	} {
		entries, skipped, err := ParseExport(tc.format, []byte(tc.data))
		if err != nil || len(entries) != tc.count || skipped != tc.skipped {
			t.Fatal(tc.format, entries, skipped, err)
		}
		if entries[0].Password != "secret" || entries[0].Name != "Login" {
			t.Fatal("wrong field mapping")
		}
		if tc.format == "protonpass-csv" && (entries[0].Username != "mail@example.com" || entries[0].Email != "mail@example.com") {
			t.Fatal("Proton email fallback lost")
		}
	}
	if _, _, err := ParseExport("bitwarden-json", []byte(`{"encrypted":true,"items":[]}`)); err == nil {
		t.Fatal("encrypted vault accepted")
	}
	if _, _, err := ParseExport("1password-csv", []byte("Title,Website,URL,Username,Password\nx,https://a.com,https://b.com,u,p")); err == nil {
		t.Fatal("ambiguous columns accepted")
	}
}
