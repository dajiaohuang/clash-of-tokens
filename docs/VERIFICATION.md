# Verification evidence

**Sources → Validate** sends one explicit generation request to the configured
target. It does not enable a source, approve Auto, change a tier or retry. The
check requires visible output and the protocol's completion signal; an HTTP
success followed by an incomplete stream fails validation.

**Sources → Verification** separates catalog implementation/catalog claims,
credential configuration, and the latest model/protocol validation. The
Overview count and `/admin/status.live_verified_sources` count sources with at
least one configured model/protocol whose latest validation both succeeded and
matches its current binding. They do not mean every model or capability of that
source is verified. The standalone server without the control-plane evidence
store reports zero; the deployed control plane derives the count from history.

The result is one of:

- `not_checked`: no retained validation for this source/model/protocol.
- `verified`: the latest check observed output and protocol completion, with a
  successful upstream status, for the current binding.
- `failed`: the latest check for the current binding did not meet those conditions.
- `historical`: a retained check exists, but it predates binding provenance or
  no longer matches the configured source/credential version.

Each new check records a SHA-256 identifier of non-secret source configuration,
browser/device settings and credential metadata. No credential value or hash of
a credential value is used. Vault credentials carry monotonically increasing
versions; a successful replacement increments the version, and a failed save
does not. Credential creation/update timestamps also distinguish deleting and
recreating a reference. The version survives a vault reopen. Older vault records
begin at version zero and acquire version one on their next replacement.

Environment credentials and supplementary environment account IDs have no
durable version. Their evidence is additionally tied to the current server run
and becomes historical after restart. Protected-reference evidence can survive
restart when its complete binding matches. Unrelated configuration revisions
alone do not invalidate a binding; the check still displays its original
revision and timestamp. Old evidence without a binding identifier is historical.

Credential configuration states such as `protected_reference`,
`environment_present` or `browser_configured` establish only configuration or
presence. They are not authentication checks. Browser authentication has its
own [account evidence](BROWSER_LOGIN.md). A validation result remains a
point-in-time observation: tokens can expire, browser users can sign out and
remote providers can change after a successful check. It does not establish
model identity, model quality, tool correctness, vision support or future quota.

History retains at most 1,000 check records. If a record is no longer retained,
the corresponding status becomes `not_checked`. The synthetic browser
regression verifies a complete check increments the count, replacing the bound
credential clears it, historical evidence remains visible and secrets stay out
of the management page. API tests distinguish completed and truncated streams;
binding tests cover credential replacement, endpoint changes and restart scope.
