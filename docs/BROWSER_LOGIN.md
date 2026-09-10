# Browser login and authentication evidence

Create an enabled profile under **Browsers**, then bind it to an account.
**Accounts → Login** launches a dedicated Chrome, Edge, Brave, Firefox, Opera,
Vivaldi, Chromium or Arc data
directory for that profile. Complete sign-in and any verification challenge
yourself. An occupied debugging port prevents launching another process on
that port; use the already-running profile and **Check login** instead.

After a successful launch, the login dialog checks the session until it is
authenticated, returns an unsupported/rate-limited result, or reaches five
minutes. Checks are sequential, with five seconds between results. Each browser
check is limited to twenty seconds. **Stop checks**, closing the dialog, or
replacing it with another dialog cancels its request and stops polling. The
browser remains open under the user's control.

The registry accepts all eight discovered browser families. Chromium-family
launches use an isolated `--user-data-dir`; Firefox uses an isolated
`--profile` directory. The gateway still requires an explicit loopback CDP
endpoint, and selecting a family does not verify installation, CDP
compatibility, or provider authentication.

| Adapter | Authentication evidence |
| --- | --- |
| ChatGPT Web | Same-origin `/api/auth/session` with an authenticated session, through the existing driver |
| Claude Web | Same-origin `/api/organizations` containing a nonempty organization UUID |
| Blackbox | Same-origin `/api/auth/session` containing a nonempty user email |
| Other adapters | Explicitly unsupported browser login check |

Claude and Blackbox use the read-only lookups already required by their local
adapters. The checks operate in a newly created gateway-owned tab and close it
afterward. They submit no chat prompt, create no conversation, and do not copy
identity or token fields out of the page. Claude/Blackbox JSON reads are limited
to 1 MiB, eight seconds and the provider's exact origin; redirects and malformed
responses do not become successful authentication.

The result distinguishes `authenticated`, `login_required`,
`challenge_or_access_denied`, `rate_limited`, `browser_unavailable`, `unknown`
and `unsupported`. A composer being visible is a separate observation.
Authentication does not verify a model, establish remaining quota, approve
Auto routing, or make a source credential usable. For example, an HTTP adapter
may still require an imported cookie or supplementary credential fields even
when its browser session is authenticated. Use **Sources → Validate** for an
explicit generation check.

Checks are saved in the bounded evidence history with the account and
configuration revision. Accounts display the latest recorded result and its
timestamp. A result from an older configuration is labeled historical. It is
a point-in-time observation, not a continuously verified login guarantee.

The browser regression uses intercepted synthetic Claude/Blackbox pages and
session endpoints. It covers authentication, rejection/error states, redaction,
history persistence, revision labeling and stopping polling on dialog close.
It makes no requests to the live providers. Real account compatibility must
still be checked using the user's selected browser profile; provider page and
private endpoint behavior may change.

## Account setup wizard

**Accounts → Add account** combines provider selection, a protected credential
reference, an existing or new isolated browser profile, and login checking.
Manual credential entry, password-manager import, token import and selected
browser-cookie import return to the draft with its fields preserved. A single
imported credential is selected automatically; multiple results require a
selection. Provider-specific supplementary fields still use their existing
configuration controls.

The manual credential dialog follows the selected descriptor and credential
kind. API-key providers show an API key field; username/password login material
shows separate Username and Password fields; a device-session descriptor with
no declared secret fields does not offer a misleading manual secret form.

The setup toolbar follows the selected provider descriptor. Manual credential
types and token imports are offered only when their resulting modes are
declared; browser-cookie import, isolated-profile creation and login checking
are shown for browser-capable providers. Changing providers refreshes both the
credential references and these actions.

Login can launch the selected profile before saving the account. If its port is
occupied, **Use running browser** explicitly selects that connection for the
check. Draft checks do not save configuration or authentication history.
After a successful draft check, applying the account configuration performs a
new check against the saved account and records that result. The account starts
disabled and is not approved for Auto routing. Authentication alone does not
validate generation or configure a source.

New profiles and the account are applied together through configuration preview.
Canceling setup leaves any launched browser open and any already imported
credential in the protected store, potentially unbound. It does not erase either
resource. This wizard does not yet discover accounts automatically from all
installed browser profiles or provide login detection for every provider.
