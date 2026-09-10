package config

import "os"

// CredentialValue resolves an explicit vault reference without copying it into
// process environment variables. A missing reference fails closed.
func (s Source) CredentialValue() string {
	if s.CredentialRef != "" {
		if s.CredentialResolver == nil {
			return ""
		}
		return s.CredentialResolver(s.CredentialRef)
	}
	return os.Getenv(s.KeyEnv)
}
