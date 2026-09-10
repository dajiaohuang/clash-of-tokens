package api

import (
	"fmt"
	"slices"

	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/providerdef"
)

func validateCredentialBindings(c config.Config, metadata []credentials.Metadata) error {
	kinds := map[string]string{}
	for _, m := range metadata {
		kinds[m.ID] = m.Kind
	}
	accounts := map[string]string{}
	for _, a := range c.Accounts {
		accounts[a.ID] = a.CredentialRef
	}
	for _, source := range c.Sources {
		ref := source.CredentialRef
		if ref == "" {
			ref = accounts[source.AccountID]
		}
		if ref == "" {
			continue
		}
		kind, exists := kinds[ref]
		if !exists {
			return fmt.Errorf("source %s: credential reference does not exist", source.ID)
		}
		d, ok := providerdef.Lookup(source.Adapter)
		if !ok || !slices.Contains(d.CredentialModes, kind) {
			return fmt.Errorf("source %s: %s credential is incompatible with adapter %s", source.ID, kind, source.Adapter)
		}
	}
	return nil
}
