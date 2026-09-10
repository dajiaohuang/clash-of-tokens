# Browser login and authentication evidence

Create an enabled profile under **Browsers**, then bind it to an account.
**Accounts → Login** launches a dedicated Chrome, Edge or Chromium data
directory for that profile. Complete sign-in and any verification challenge
yourself. An occupied debugging port prevents launching another process on
that port; use the already-running profile and **Check login** instead.

After a successful launch, the login dialog checks the session until it is
authenticated, returns an unsupported/rate-limited result, or reaches five
minutes. Checks are sequential, with five seconds between results. Each browser
check is limited to twenty seconds. **Stop checks**, closing the dialog, or
replacing it with another dialog cancels its request and stops polling. The
browser remains open under the user's control.

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
