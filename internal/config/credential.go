package config

import (
	"context"
	"errors"
	"os"
)

type resolvedCredentialKey struct{}
type resolvedCredential struct{ ref, value string }

// ResolveForRequest resolves once under the request deadline. Only the request
// context retains the value; persistent source configuration still holds a ref.
func (s Source) ResolveForRequest(ctx context.Context) (context.Context, error) {
	if s.CredentialRef == "" || s.CredentialResolverContext == nil {
		return ctx, nil
	}
	value, err := s.CredentialResolverContext(ctx, s.CredentialRef)
	if err != nil || value == "" {
		if ctx.Err() != nil {
			return ctx, ctx.Err()
		}
		return ctx, errors.New("credential resolution failed; check protected credential status")
	}
	return context.WithValue(ctx, resolvedCredentialKey{}, resolvedCredential{s.CredentialRef, value}), nil
}

// CredentialValue resolves an explicit vault reference without copying it into
// process environment variables. A missing reference fails closed.
func (s Source) CredentialValue(contexts ...context.Context) string {
	if s.CredentialRef != "" {
		if s.CredentialResolverContext != nil {
			ctx := context.Background()
			if len(contexts) > 0 && contexts[0] != nil {
				ctx = contexts[0]
			}
			if cached, ok := ctx.Value(resolvedCredentialKey{}).(resolvedCredential); ok && cached.ref == s.CredentialRef {
				return cached.value
			}
			value, err := s.CredentialResolverContext(ctx, s.CredentialRef)
			if err != nil {
				return ""
			}
			return value
		}
		if s.CredentialResolver == nil {
			return ""
		}
		return s.CredentialResolver(s.CredentialRef)
	}
	return os.Getenv(s.KeyEnv)
}
