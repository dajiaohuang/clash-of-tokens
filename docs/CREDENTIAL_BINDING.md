# Credential binding overrides

Credential bindings are checked against the adapter descriptor before a
configuration revision is published. A `username_password` export cannot be
bound to an API-key adapter by default, for example.

Accounts and sources expose the explicit `credential_type_override` checkbox
for a reviewed exception. Saving it records the choice in the versioned
configuration and the editor displays a warning that the adapter may not
understand the credential format. The override does not reveal, transform, or
copy the secret and does not bypass missing-reference checks. A credential
still has to exist in the protected vault; only the descriptor kind mismatch
is bypassed.

The default remains fail-closed. Use an override only when the provider's
adapter contract has been reviewed and the stored value is intentionally
compatible. Runtime errors and source validation continue to be reported if
the provider rejects the value.
