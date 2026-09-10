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
