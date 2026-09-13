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
		if account.LoginCredentialRef != "" {
			kind, exists := kinds[account.LoginCredentialRef]
			if !exists || kind != "username_password" {
				return fmt.Errorf("account %s: login reference requires protected username_password material", account.ID)
			}
			d, ok := providerdef.Lookup(providerAdapters[account.ProviderID])
			if !ok || !slices.Contains(d.LoginMaterials, kind) {
				return fmt.Errorf("account %s: provider does not support this login material", account.ID)
			}
		}
		if account.CredentialRef == "" {
			continue
		}
		kind, exists := kinds[account.CredentialRef]
		if !exists {
			return fmt.Errorf("account %s: credential reference does not exist", account.ID)
		}
		if kind == "username_password" {
			return fmt.Errorf("account %s: password material belongs in login_credential_ref, never credential_ref", account.ID)
		}
		adapter, known := providerAdapters[account.ProviderID]
		if !known {
			continue
		}
		d, descriptorKnown := providerdef.Lookup(adapter)
		if !descriptorKnown || (!account.CredentialTypeOverride && !slices.Contains(d.InvokeCredentials, kind)) {
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
		if kind == "username_password" {
			return fmt.Errorf("source %s: login material cannot be used as an invocation credential", source.ID)
		}
		d, ok := providerdef.Lookup(source.Adapter)
		if !ok || (!override && !slices.Contains(d.InvokeCredentials, kind)) {
			return fmt.Errorf("source %s: %s credential is incompatible with adapter %s", source.ID, kind, source.Adapter)
		}
	}
	return nil
}
