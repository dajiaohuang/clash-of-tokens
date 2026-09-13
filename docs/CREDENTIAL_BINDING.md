# Credential binding overrides

Credential bindings are checked against the adapter descriptor before a
configuration revision is published. A `username_password` export belongs only
in `Account.login_credential_ref`; it cannot be used as an invocation credential,
even with a reviewed type override.

Accounts and sources expose the explicit `credential_type_override` checkbox
for a reviewed exception. Saving it records the choice in the versioned
configuration and the editor displays a warning that the adapter may not
understand the credential format. The override does not reveal, transform, or
copy the secret and does not bypass missing-reference checks. A credential
still has to exist in the protected vault; only the descriptor kind mismatch
is bypassed for invocation credential types. Password-to-invocation conversion
is never allowed.

Changing the destination, inherited tenant/project, or bound browser identity
requires a fresh destination confirmation. Configuration preview lists the
affected resources. Apply/rollback use `confirm_credential_destinations`; direct
resource edits require `X-COT-Confirm-Credential-Destinations: true`. The normal
configuration revision guard still applies. This confirmation does not override
missing/revoked references or invalid configuration.

The default remains fail-closed. Use an override only when the provider's
adapter contract has been reviewed and the stored value is intentionally
compatible. Runtime errors and source validation continue to be reported if
the provider rejects the value.
