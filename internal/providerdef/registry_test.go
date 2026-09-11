package providerdef

import "testing"

func TestBrowserAuthDescriptorsAcceptLoginMaterial(t *testing.T) {
	for _, id := range []string{"chatgpt-web", "claude-web", "blackbox"} {
		d, ok := Lookup(id)
		if !ok {
			t.Fatalf("descriptor %s missing", id)
		}
		found := false
		for _, mode := range d.CredentialModes {
			if mode == "username_password" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("descriptor %s does not accept username/password login material: %v", id, d.CredentialModes)
		}
	}
}

func TestDescriptorCredentialFieldsAreModeSpecificAndCopied(t *testing.T) {
	d, ok := Lookup("claude-web")
	if !ok {
		t.Fatal("claude-web descriptor missing")
	}
	login := d.FieldsForMode("username_password")
	if len(login) != 2 || login[0].Name != "username" || login[1].Name != "password" || !login[1].Secret {
		t.Fatalf("unexpected login fields: %#v", login)
	}
	login[0].Label = "mutated"
	again := d.FieldsForMode("username_password")
	if again[0].Label == "mutated" {
		t.Fatal("FieldsForMode returned mutable registry data")
	}
	if got := d.FieldsForMode("unknown"); len(got) != len(d.CredentialFields) {
		t.Fatalf("unknown mode did not use default fields: %#v", got)
	}
}
