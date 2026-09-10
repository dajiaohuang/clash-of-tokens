package api

import (
	"fmt"
	"slices"

	"clash-of-tokens/catalog"
	"clash-of-tokens/internal/config"
	"clash-of-tokens/internal/credentials"
	"clash-of-tokens/internal/providerdef"
)

func validateCredentialBindings(c config.Config, metadata []credentials.Metadata) error {
	kinds := map[string]string{}
	for _, m := range metadata {
		kinds[m.ID] = m.Kind
	}
	accounts := map[string]struct {
		ref      string
		override bool
	}{}
	for _, a := range c.Accounts {
		accounts[a.ID] = struct {
			ref      string
			override bool
		}{a.CredentialRef, a.CredentialTypeOverride}
	}
	providerAdapters := map[string]string{}
	for _, provider := range catalog.All() {
		providerAdapters[provider.ID] = provider.Adapter
	}
	for _, account := range c.Accounts {
		if account.CredentialRef == "" {
			continue
		}
		kind, exists := kinds[account.CredentialRef]
		if !exists {
			return fmt.Errorf("account %s: credential reference does not exist", account.ID)
		}
		adapter, known := providerAdapters[account.ProviderID]
		if !known {
			continue
		}
		d, descriptorKnown := providerdef.Lookup(adapter)
		if !descriptorKnown || (!account.CredentialTypeOverride && !slices.Contains(d.CredentialModes, kind)) {
			return fmt.Errorf("account %s: %s credential is incompatible with provider %s", account.ID, kind, account.ProviderID)
		}
	}
	for _, source := range c.Sources {
		ref := source.CredentialRef
		override := source.CredentialTypeOverride
		if ref == "" {
			account := accounts[source.AccountID]
			ref, override = account.ref, account.override
		}
		if ref == "" {
			continue
		}
		kind, exists := kinds[ref]
		if !exists {
			return fmt.Errorf("source %s: credential reference does not exist", source.ID)
		}
		d, ok := providerdef.Lookup(source.Adapter)
		if !ok || (!override && !slices.Contains(d.CredentialModes, kind)) {
			return fmt.Errorf("source %s: %s credential is incompatible with adapter %s", source.ID, kind, source.Adapter)
		}
	}
	return nil
}
