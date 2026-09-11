# Control plane implementation progress

Acceptance scope: [complete synthesis](CONTROL_PLANE_REQUIREMENTS.md).
Current gaps: [all 51 sections](CONTROL_PLANE_AUDIT.md). This working ledger is
not a completed acceptance audit.

## Completed batches

1. Recorded all 51 sections of the user-authorized synthesis (881a134).
2. Separated source kind, execution location, inference location, billing mode,
   and credential mode. Local-only routing requires explicitly declared local
   inference through a local model API; local CLI processes are excluded.
   Metered, subscription and unknown-cost routing have independent overrides.
   Catalog presets no longer derive billing from process location (a51e968).

3. Account and Provider models, hierarchical enable/approval checks and account
   concurrency across sources (e75167d).
4. All adapters resolve primary credentials through an injectable reference
   resolver; explicit references never fall back to environment credentials
   and resolver values are not serialized (69a9933).
5. Windows DPAPI credential vault, atomic persistence, redacted listing,
   authenticated PUT/DELETE endpoints and bound-credential deletion protection
   (8a2ccac). Native Linux/macOS keychain integration is described in batch 23.

6. Atomic configuration journal, optimistic revision checks, preview and
   rollback (895308f). The sibling `.state` file becomes authoritative after
   the first administrative change; the original JSON remains an import seed.
7. Request-pinned runtime generations preserve shared account/quota capacity,
   metrics and ingress admission. Source changes affect new requests while
   held requests complete; listener/runtime/browser/device changes are marked
   restart-required (61fb2bb).
8. Persistent Provider/Account/Source/Group CRUD, revision-checked patches,
   and a no-upstream routing simulator (811415d).

9. Shared descriptor registry for 100 adapters, protocol/capability validation,
   factory dispatch and form schemas (6bc2756).
10. Embedded management console with provider catalog, accounts, protected
    credentials, sources/models, multi-group membership and ordering, routing
    simulator, health/metrics, all configuration fields, history, preview/apply
    and global search (c78e6a0). Model enabled/Auto approval flags are independent.

11. Account pool round-robin, weighted, least-load and sticky dispatch; weighted
    source groups (031b120).
12. Protocol-aware stream completion/error detection, bounded SSE buffering and
    execution health/TTFT tracking (ec8f263).
13. Conservative pre-submission transport classification (052f3ea) and bounded
    request fallback. Groups accept max_attempts 0..8: zero defaults to one, or
    up to eight group members for fallback groups. Select and stateful requests
    always use one attempt. Only proven pre-connection failures or native API
    401/403/429 rejections can advance; ambiguous errors and stream failures
    never replay. Each attempt rewrites the original input and excludes all
    previously attempted sources.

14. Selected CSV/JSON export import with redacted preview and atomic protected
    batch storage (1f1f212); exact catalog-domain recommendations and effective
    source credential-type validation (0b92b8f). Specialized manager formats
    still require explicit exports; environment/CLI imports and metadata-only
    browser/account discovery are available through separate flows.
15. Browser profile registry, account binding, per-profile CDP/session storage,
    explicit dedicated Chrome/Edge/Chromium login launch, and bounded login
    checks. Launch is never reported as authenticated; provider compatibility
    and installed-browser discovery remain operator/provider dependent.

16. ChatGPT Web authentication evidence without generation or token extraction
    (73148a3). Additional browser checks and persisted evidence are in batch 24.
17. Native OpenAI/Anthropic/Gemini model discovery with bounded pagination,
    explicit partial-list reporting and disabled model configuration; manual
    source validation observes output and protocol completion under a lease.
    Disabled sources remain disabled. Validation does not retry. Discovery
    does not imply generation, tool or quality support. Evidence is returned
    to the caller and persisted in bounded revision-linked history (f2bd6a6).

18. Local ChatGPT session metadata and lease-protected expire/clear operations
(c31a76a). Upstream conversations are not deleted. Batch 109 adds the same
metadata-only controls to China Web, China Next (Yuanbao), Zed Hosted, Amazon
Q, and Augment; stateless adapters explicitly report unsupported management.
19. Configured-source environment import and selected Codex/Gemini/OAuth JSON
    access-token import. Preview never returns tokens; imports allocate new
    references. CLI refresh tokens and account IDs are not imported and there
    is no automatic refresh. The account wizard can retain provider-specific
    Base URL, organization, and project defaults; current access tokens still
    require an explicit re-import after a CLI refresh.

CLI format references: [Codex auth fixture](https://github.com/openai/codex/blob/main/codex-rs/model-provider/src/auth.rs),
[Gemini OAuth implementation](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/code_assist/oauth2.ts).

20. All named password managers have selected CSV login export support, plus
    Bitwarden JSON. See [import formats and limits](CREDENTIAL_IMPORTS.md).
21. Browser metadata discovery from standard Chrome/Edge/Brave/Chromium/
    Vivaldi/Opera/Firefox/Arc locations where available, plus configured CDP
    status with four workers and a ten-second deadline. Metadata discovery
    does not inspect cookies or establish authentication. Existing browser
    session import requires a configured CDP connection. Registry limit: 64 profiles.
22. Selected-provider browser Cookie import with redacted domain/count preview,
    new protected references and a bounded CDP read. Only catalog providers
    declaring Cookie support are selectable. Authentication remains unchecked.
    Isolated browser regression uses synthetic selected and unrelated-domain
    cookies and verifies only the selected domain contributes to the preview.
23. AES-GCM vault envelopes with per-vault random OS-keychain keys on Linux
    and macOS, restricted to Secret Service / native Keychain with no plaintext
    fallback. Linux native create/reopen/update and unavailable-service checks
    passed in an isolated Ubuntu 24.04 WSL keyring session. Hosted macOS cgo
    validation is covered by Batch 123; a local operator Keychain remains
    environment-dependent. See [storage and verification](CREDENTIAL_STORAGE.md).
24. Claude Web / Blackbox read-only browser session checks, account/revision
    evidence persistence, authentication history on Accounts, and sequential
    login polling with cancellation and a five-minute UI limit. ChatGPT checks
    also reject changed origins. Other browser adapters remain unsupported.
    Synthetic UI verification covers auth/rejection/error states and closing
    the watcher; it does not establish live provider compatibility.
    See [browser login behavior](BROWSER_LOGIN.md).
25. Verification status/counts now derive from retained model/protocol results,
    source settings and versioned credential bindings. Credential replacement
    makes old evidence historical without deleting it; environment evidence is
    scoped to one server run. Overview and Source verification display separate
    catalog/configuration/generation layers. Synthetic UI regression confirms
    count 1 after validation and 0 after credential replacement.
    See [verification semantics](VERIFICATION.md).
26. Account and shared-domain runtime capacity snapshots, including retiring
    leases; provider account-pool editor and transactional shared-domain limit
    editor. Router tests cover shared counting and reductions below active use;
    UI regression covers preview/apply and persisted pool/domain values.
    See [capacity controls](CAPACITY_CONTROLS.md).
27. Unified account setup draft with protected credential/import selection,
    existing or isolated browser profiles, explicit running-browser attachment,
    login checking and transactional account/profile preview. Synthetic browser
    regression covers login-to-save and token-import-to-binding; draft checks
    are tested to leave configuration and evidence history unchanged.
    See [account setup wizard](BROWSER_LOGIN.md#account-setup-wizard).
28. Group vision requirements, declared USD input/output rate metadata and
    ceilings, and five ordered routing preferences. Includes dispatch/capacity,
    unknown-price/latency, validation and browser editing coverage. This rate
    ceiling is not a total-request spending budget.
    See [routing policies](ROUTING_POLICIES.md).
29. Source detail consolidates models, quota/account capacity, runtime duration,
    TTFT, verification evidence and read-only eligibility explanations. Optional
    three-sample validation exposes billing and measurement limits; browser
    regression covers samples/history, first-failure stop and cancellation.
    See [source detail](SOURCE_DETAIL.md).
30. Provider detail now combines account/source/model counts, scoped generation
    evidence, runtime observations, account controls, source actions and filtered
    eligibility explanations. Browser regression covers count provenance,
    credential-rotation invalidation and navigation to Source Detail.
    See [provider detail](PROVIDER_DETAIL.md).
31. Runtime execution status and validation responses now retain only fixed
    gateway error categories. Unknown adapter error text becomes `upstream_error`
    while preserving failure status. A synthetic URL/key/body regression verifies
    serialized status redaction and snapshot isolation. Generation and model
    discovery HTTP failures also use stable public categories; adapter-wide
    logging remains outside the gateway boundary audit.
32. Bounded structured execution-event ring survives hot updates and records
    exactly one event per released attempt, including finishing old leases.
    Activity exposes source/outcome filters. Tests cover capacity/order,
    redaction, cancellation, snapshot isolation, administrative exclusions and
    UI visibility after credential rotation. See [execution events](EXECUTION_EVENTS.md).
33. Overview and Metrics expose global active leases/current queue, ingress,
    buffer reservations, Go memory, goroutines, uptime and lifetime-average
    request rate. Global active now includes removed sources' held leases.
    Router/API/browser tests cover retention, cancellation, access and display.
    See [workload metrics](WORKLOAD_METRICS.md).
34. Added official Ark, legacy Hunyuan, Qianfan v2 and TokenHub region presets
    with explicit cloud classification, protected API-key support, disabled
    defaults, current migration/region notes and synthetic path/auth tests.
    See [China cloud APIs](providers/china-cloud-apis.md).
35. Models supports source/text filters, explicit multi-selection and one
    previewed transaction for enable, Auto, rating and capability changes.
    Browser regression covers two-model editing, required rating evidence and
    preservation of an unselected model. See [model bulk edits](MODEL_BULK_EDIT.md).
36. Global search now matches models, browser profiles and device settings in
    addition to providers/accounts/sources/groups/credential metadata. Matching
    uses named identity fields rather than serialized configuration. Browser
    regression exercises all eight entity navigation paths; opening device
    settings performs no device discovery or execution.
37. Browser launches retain process ownership with per-launch IDs, bounded
    history and explicit confirmed stop. Browsers adds owned-process and
    launch-provider controls. Tests use dedicated child processes and synthetic
    UI records; external and untracked child processes are not terminated.
    See [browser processes](BROWSER_PROCESSES.md).
38. Devices now has an authenticated, same-origin `POST /admin/device/check`
    doctor and a Devices-page report. The bounded read-only probe reports ADB
    and OCR path readiness, Android connection, logical resolution, foreground
    package and configured app installation. App login and last-test labels use
    only verified source evidence; the endpoint never starts an emulator,
    opens an app, changes the clipboard or sends a message. Synthetic Go/API
    tests and the isolated UI regression cover the empty-device path.
    See [device controls](DEVICE_CONTROLS.md).
39. Account status now aggregates the active account capacity, member sources,
    runtime success/failure counters, last success/failure, last validation,
    authentication evidence and a bounded health state (`disabled`,
    `auth_required`, `exhausted`, `blocked`, `cooldown`, `healthy`,
    `degraded` or `untested`). Accounts displays these fields without exposing
    credential values, and a status test covers evidence and capacity joins.
40. Accounts now expose `POST /admin/accounts/{id}/validate`. Source-backed
    accounts use the existing explicit stream validation for their first
    configured source/model/protocol; browser-only accounts use the existing
    non-generating authentication check. The action is redacted, evidence
    preserving, does not enable or Auto-approve a source, and is covered by a
    synthetic disabled-source API test. See [account validation](ACCOUNT_VALIDATION.md).
41. Credential binding remains fail-closed by default, while accounts and
    sources can opt into an explicit `credential_type_override`. The protected
    vault still requires an existing reference and never returns the secret;
    the UI shows a risk warning and descriptor validation tests cover both the
    rejected default and reviewed account/source overrides. See [credential binding](CREDENTIAL_BINDING.md).
42. Native model discovery now preserves bounded provider metadata: display name,
    owner, creation time, description, token limits and declared generation
    methods for OpenAI/Anthropic/Gemini responses where available. Configure
    model records these IDs as disabled, unrated, not Auto-approved declared /
    canonical values; no capability or quality is inferred. Parser tests cover
    metadata and pagination. See [model discovery](MODEL_DISCOVERY.md).
43. Provider validation now exposes `POST /admin/providers/{id}/validate`.
    Configured providers use the first source/model/protocol through the existing
    staged stream check; providers with browser-only accounts fall back to the
    existing non-generating authentication check. The action preserves redacted
    evidence, leaves all routing switches unchanged, and is available from both
    Providers and Provider Detail. Synthetic API and browser regression tests
    cover source and browser fallback paths. See [provider detail](PROVIDER_DETAIL.md).
44. Provider health now includes the latest source generation-validation time,
    alongside account and source validation timestamps. Health renders the field
    as a freshness indicator without treating historical evidence as current
    availability.

Discovery protocol references: [Anthropic models](https://platform.claude.com/docs/en/api/models/list),
[Gemini models](https://ai.google.dev/api/models),
[OpenAI models](https://platform.openai.com/docs/api-reference/models).

Validation: `go test ./...` passed, 610 tests in 39 packages on Windows.
Windows and Linux-target `govulncheck` reported no vulnerabilities.
Browser regression: credential creation/redaction, account binding, source
creation, group membership, preview/apply, session metadata/clear controls,
every navigation destination and 390px viewport passed via
`scripts/ui_fixture.py --test` after building
`.clash-tokens/ui-test.exe`. Screenshots are in `.clash-tokens/ui-artifacts/`.
Race detection is outstanding: the current Go environment has cgo disabled.
The credentials package also compiles for `darwin/amd64` and `linux/amd64`
with cgo disabled; native keychain runtime behavior still requires those OSes.

## Remaining implementation

The [51-section ledger](CONTROL_PLANE_AUDIT.md) now replaces the previous generic
A–E checklist. The main gaps include complete provider-specific setup and
checks, quota/account pool end-to-end views, remaining routing policies and
native macOS verification.
All explicit subrequirements and the end-to-end install-to-routing workflow must
be verified before completion.

The current application is not yet the completed control plane. Credentials
and upstream availability must never be inferred from catalog implementation.
Batch 43 adds `POST /admin/discovery/accounts` and the Accounts-page discovery
dialog. It returns bounded, secret-free candidates from stored credential
metadata and configured environment names, with optional read-only standard
browser profile metadata scanning. Suggestions are catalog matches only;
binding, login, and source validation remain explicit actions. See
[account discovery](ACCOUNT_DISCOVERY.md).

Batch 44 adds authenticated implementation status at `/admin/implementation`
and the System > Implementation page. Catalog implementation, descriptor
factory, configured resource counts, catalog live metadata, and explicit
runtime evidence are displayed as separate fields. See
[implementation status](IMPLEMENTATION_STATUS.md).

Batch 45 extends provider matching with an explicit, host-exact alias allowlist
for official product and API consoles. Case and trailing-dot normalization are
safe; suffix lookalikes remain unmatched. See [provider matching](PROVIDER_MATCHING.md).

Batch 47 enriches source validation evidence with connection, authentication,
request, streaming, completion, and measured duration fields; the UI presents
these stages separately and never treats an incomplete stream as verified.

Batch 48 adds session capability rows to `/admin/sessions` and the Sessions
page. ChatGPT Web initially exposed local conversation inventory; Batch 109
extends metadata-only inventory and expire/clear controls to China Web, China
Next (Yuanbao), Zed Hosted, Amazon Q, and Augment. Other adapters remain
explicitly marked unavailable instead of being scraped.

Batch 49 makes execution accounting mode-independent: non-streaming responses
record transport completion, upstream status, rejection, or cancellation
through the same redacted lease-health path used by streaming responses. See
[runtime health](RUNTIME_HEALTH.md).

Batch 50 hardens upstream-facing errors: generation failures and model
discovery failures return stable public categories instead of adapter error text
that could contain URLs or response fragments.

Batch 51 retains a normalized source domain as non-sensitive metadata on
password-manager imports. Account discovery uses it for exact catalog provider
suggestions and can carry a stored credential into the account wizard; secret
values remain encrypted and absent from responses. See
[credential imports](CREDENTIAL_IMPORTS.md).

Batch 132 binds validation evidence to the effective source, including inherited
account Base URL, organization and project defaults. Source verification now
reports the safe credential reference and binding owner (`source`, `account`,
`environment`, `browser`, `anonymous` or `none`) so runtime status cannot imply
current validity after an account-default change.

Batch 133 adds an API-to-router regression for the four independent control
switches. Provider, account, source, and model enable/Auto changes each produce
their own simulator exclusion reason and never alter the other switches.

Batch 134 makes Source Detail display the effective account endpoint defaults
used by runtime routing and the same safe credential binding provenance shown
in Health and verification details.

Batch 135 extends configuration previews with sorted changed-resource IDs for
sources, accounts and groups, so a same-count edit remains reviewable alongside
the bounded routing eligibility impact.

Batch 136 makes the Credentials page include sources that inherit an account's
protected reference in its safe “Used by” view, with browser regression coverage.

Batch 137 extends global search to credential references and account/source
Base URL, organization, project and quota metadata without indexing secrets.

Batch 52 adds a direct **Use in account** action for stored credentials and
configured browser profiles discovered by the Accounts page. It pre-fills the
existing transactional account wizard without enabling the account or source.

Batch 53 adds an end-to-end API regression covering one protected credential
reference shared by two account sources, ordered Auto membership, and a
successful synthetic local generation. The fixture asserts the upstream sees
the resolved secret only in the Authorization header and never in control-plane
metadata; no real provider is contacted.

Batch 49 makes execution accounting mode-independent: non-streaming responses
now record transport completion, upstream status, rejection, or cancellation
through the same redacted lease-health path used by streaming responses. See
[runtime health](RUNTIME_HEALTH.md).

Batch 50 hardens two upstream-facing error paths: generation failures and model
discovery failures now return stable public categories instead of adapter error
text that could contain URLs or response fragments. Detailed causes remain
outside the HTTP/event boundary.

Batch 54 adds bounded token and declared-cost accounting. Streaming observers
accept common OpenAI/Anthropic field names, Gemini `usageMetadata`, camelCase
variants and Anthropic usage split across events. Non-streaming JSON is captured
only up to 1 MiB while forwarding, so oversized or non-JSON responses remain
unknown rather than estimated. Source status, Metrics, Source Detail and
execution events expose numeric observations; cost is marked known only when
every recorded source attempt has usage and both operator-supplied rates.
Protocol, routing, API and non-stream regression tests cover accumulation,
explicit zero usage, cost math, duplicate recording and bounded forwarding.
See [token accounting](TOKEN_ACCOUNTING.md).

Batch 55 extends configuration preview with a bounded routing-impact report.
Before applying a valid revision, the admin API compares old and proposed
group eligibility counts for configured protocols and reports source/account
count deltas. The report uses a zero-byte read-only simulation, never contacts
an upstream, and is labeled as impact rather than final candidate selection.
API coverage verifies disabling the only Auto source changes the preview from
one eligible candidate to zero; the browser flow continues to exercise the
transactional diff and apply path.

Batch 56 adds a credential replacement impact review in the browser control
plane. Replacing a protected reference now pauses for a review showing bound
accounts, inherited/direct sources and credential-type transition. Confirming
the review performs the existing protected vault write; canceling leaves the
new value untouched. The review explains that matching generation evidence is
historical after rotation and that in-flight requests are unaffected. The
browser regression covers the confirmation before continuing its rotation and
verification checks.

Batch 57 closes the account quota-domain move invariant. The resource API and
the schema-driven account editor now update every source bound to the account
when its quota domain changes, in one validated transaction. This prevents an
account move from leaving sources on a stale shared-capacity boundary. A
resource API regression verifies persistence of both account and source domains;
the shared-domain limit editor continues to govern unrelated domain members.

Batch 58 extends that invariant to source edits. Changing the quota domain on a
bound source now moves its account and sibling sources together; moving a source
to another account adopts the destination account's existing domain. The API
regression covers both paths and confirms unrelated account domains stay fixed.

Batch 59 adds the aggregated account-health table to the Health page. It shows
provider, bounded health and authentication state, account capacity, member
sources, success/failure counters and the latest validation timestamp beside
the existing source and shared-domain views. The data remains the same
redacted runtime snapshot exposed by `/admin/status`.

Batch 60 protects account lifecycle integrity in the administration API. An
account with bound sources now returns a clear conflict instead of attempting a
configuration that would fail later reference validation. The quota regression
also verifies the guard before exercising source-domain moves.

Batch 61 applies the same lifecycle guard to browser profiles. Deleting a
profile that is still selected by an account now returns a clear conflict,
preserving per-account CDP and session isolation until the binding is removed.

Batch 62 protects group membership during source deletion. The administration
API now returns a clear conflict while a source is still listed by any group,
so a deletion cannot leave dangling fallback or Auto references.

Batch 63 completes the provider reference guard. Deleting a provider with
accounts still bound now returns a clear conflict, matching the existing source
guard and preserving the provider/account relationship until bindings are
removed.

Batch 64 exercises configuration rollback in the browser regression. It edits a
runtime limit through the schema form, reviews and applies the transaction,
opens version history, compares the newest revision, and restores it through
the existing guarded API flow.

Batch 138 also advances the saved revision out of band before selecting a
restore. The browser regression confirms the stale optimistic rollback is
rejected with a conflict, refreshes the history, and then completes a restore
against the current revision.

The API regression in the same batch performs two durable edits and submits a
rollback with the first revision after the second is current. It verifies the
endpoint returns `409` and leaves the active revision unchanged.

Batch 139 makes account-provider changes explicit migrations. The admin API
returns a conflict explaining that bound sources must be migrated first, and
the regression verifies no configuration revision is advanced by the rejected
edit.

The shared account validator now emits the same migration guidance during a
full configuration preview, so the schema-driven account editor does not hide
the cause behind a generic provider mismatch.

Batch 140 extends the browser regression to toggle a source off and back on.
Both changes pass through preview/apply and the final row state confirms the
enabled switch remains independent from the source's Auto approval.

Batch 141 follows the saved disabled source into Routing → Simulate and
asserts that the candidate table reports the `disabled` exclusion reason. This
connects the browser control state to the router's visible explanation.

Batch 142 adds an API-to-router discovery regression. A synthetic native
`/models` response is recorded, its model is configured with disabled and
unapproved defaults, and the simulator confirms the candidate is excluded as
`model_disabled` until a later policy edit explicitly enables it.

Batch 143 extends the wizard regression to the Codex descriptor. Its manual
credential dialog exposes only `oauth`, `cli_session`, and `api_key` modes, so
cookie material cannot be bound accidentally to a CLI/OAuth provider.

Batch 65 adds explicit OpenAI organization and project metadata to source
configuration. The schema-driven source editor exposes both fields, validates
control characters and length, and the native OpenAI client forwards them as
`OpenAI-Organization` and `OpenAI-Project` headers. The upstream contract test
verifies the headers while keeping caller-controlled sensitive headers filtered.

Batch 66 completes the shared quota-domain rename path. Health → Edit shared
quota now renames the domain and changes its limit together, updating all
matching accounts and sources in one reviewed configuration transaction. The
browser regression renames the fixture domain and confirms the new shared row;
existing-domain collisions remain subject to normal configuration validation.

Batch 67 exposes independent Auto approval actions beside the Provider,
Account and Source enabled switches. Each action still uses the central
preview/apply transaction, and enabling Auto for a metered provider, account
or source displays the possible billing impact. The browser regression toggles
all three levels and confirms their state without changing enabled state.

Batch 68 verifies configured fallback ordering end to end. Router coverage
selects the first eligible member in the group's declared order and advances
to the next member after a retryable rejection. The browser regression checks
the selected-member drag affordance and persists a reordered two-source group
through the normal review/apply flow.

Batch 69 aligns account health with browser authentication evidence. Login
required, expired, rejected and challenge/access-denied results now classify an
enabled account as `auth_required` instead of leaving it untested or healthy;
the status regression covers a login-required account alongside an otherwise
untested account.

Batch 70 gives each runtime source an explicit bounded health state in the
router status snapshot: `disabled`, `blocked`, `cooldown`, `exhausted`,
`broken`, `degraded`, `healthy` or `untested`. The Health, Sources and Source
Detail views consume that state instead of re-deriving it from partial counters;
router coverage exercises capacity exhaustion, successful recovery, mixed
results and disablement.

Batch 71 propagates the Provider enabled gate into account health aggregation.
An enabled account whose parent Provider is disabled now reports `disabled`,
alongside the existing account switch and authentication classifications.

Batch 72 hardens account-discovery provider suggestions to use normalized,
host-exact catalog aliases. URL paths, trailing dots and case normalize safely;
lookalike suffixes, subdomains and malformed origins no longer inherit a
provider match.

Batch 73 makes source configuration fail closed when a source has no configured
models. The central validator now rejects an empty model list before quota and
model checks; a config regression covers both the rejection and a valid model
record.

Batch 74 makes the routing simulator show the router's ordered candidates and
selected target without acquiring a lease or contacting an upstream. The
existing reason-map response remains available to older callers; detailed
simulation is opt in and covered by fallback-order, API and browser tests.

Batch 75 adds provider-level health aggregation to the control-plane status and
Health page. Provider rows now expose enabled state, bounded health and auth
status, account/source membership, capacity and runtime counters without
duplicating account-bound capacity.

Batch 77 adds the latest source generation-validation timestamp to provider
health. The Health page now surfaces provider freshness beside the existing
account and source timestamps; the value describes recorded evidence and does
not claim current upstream availability.

Batch 78 adds last-auth-check timestamps to provider and account health. The
generation-validation and browser-auth clocks remain separate so a successful
login check is not presented as generation proof.

Batch 79 fixes verification provenance for account-bound sources. Status now
resolves the inherited credential reference before reporting protected/missing
state and credential version, matching the binding used for evidence invalidation
without exposing the secret.

Batch 80 adds catalog implementation/live flags and the count of currently
verified sources to provider health. Health keeps catalog declarations beside
runtime evidence and capacity instead of collapsing them into one status.

Batch 81 adds provider pool strategy and account weight to the Health snapshot.
The pool editor and runtime health view now expose the same selection metadata
alongside active limits and observed counters.

Batch 82 fixes System > Implementation runtime totals to consume the validation
evidence emitted by explicit source/provider checks. Verified and failed checks
now appear under the matching catalog provider without changing catalog live
claims.

Batch 83 makes source verification choose the newest validation by its recorded
timestamp. Out-of-order evidence writes no longer let an older result replace a
newer status.

Batch 84 brings provider health, authentication, pool strategy, catalog
provenance, verified-source count and freshness timestamps into Provider Detail.
The detail view now exposes the same runtime evidence as Health before showing
source-level explanations.

Batch 85 adds explicit validation-state labels to provider and account health.
The latest audit entry remains available alongside its timestamp; current
binding evidence is classified as `verified` or `failed`, while entries from a
previous credential/source binding are retained as `historical`. Health and
Provider Detail display the state next to the timestamp, and API regressions
cover both provider and account projections.

Batch 86 keeps that provenance visible in the Health page's provider table as
well as Provider Detail and account rows. The browser regression now checks a
verified provider row after an explicit validation.

Batch 87 adds provider and account enabled/Auto policy flags to the health API
and Health tables. These policy switches remain visible beside runtime health,
capacity and validation evidence, with API coverage for the configured values.

Batch 88 adds an embedded-frontend serving regression. The compiled Go server
must return the dashboard, JavaScript and CSS with their expected content types,
asset markers and same-origin connect policy; the test uses no Node runtime.

Batch 89 carries the newest validation timestamp and provenance state onto each
runtime source status row. The Health page now shows `not_checked`, `verified`,
`failed` or `historical` beside source runtime counters, with an API regression
covering the current binding.

Batch 90 locally cross-builds the gateway for `linux/amd64`, `windows/amd64`
and `darwin/amd64` from the same source revision. The binaries are kept under
the ignored `.clash-tokens/platform-builds/` workspace area; hosted CI and
signed release packaging remain separate work.

Batch 91 makes newest-evidence selection deterministic when timestamps tie.
Across model, source, account and provider projections, the persisted evidence
sequence now breaks equal-time ties; a focused regression covers both ordering
directions.

Batch 92 tightens source metadata validation: `local_model` now requires both
`inference_location: local` and loopback transport. Remote or ambiguous sources
cannot satisfy a `local_only` group by label alone; configuration regressions
cover remote, non-loopback and valid local declarations.

Batch 93 aligns billing metadata with that boundary. A `local_model` must use
`billing_mode: local`, and a non-local source cannot claim local billing; focused
configuration regressions cover both invalid combinations.

Batch 94 makes the local billing declaration mandatory for `local_model` sources.
An omitted billing mode no longer becomes an ambiguous `unknown` value; the
metadata regression keeps the valid local tuple explicit.

Batch 95 carries evidence sequence through provider and account health
aggregation. When two sources validate at the same instant, the newest stored
entry now controls the exposed timestamp and state deterministically.

Batch 96 keeps credential `last_used_at` on the protected record whenever a
credential is resolved. A later encrypted vault write now preserves that
metadata across reopen, covered by the credential store regression.

Batch 97 carries redacted credential state and vault version onto account
health rows. Accounts and Health can now distinguish an unconfigured account,
a missing protected reference, a protected reference with its current version,
and a browser-profile account without exposing secret values. Provider Detail
shows the same account-level state beside its credential binding.

Batch 98 carries the same redacted account credential state/version into the
main Accounts table, keeping the account list consistent with Health and
Provider Detail.

Batch 99 derives account credential posture from effective source bindings when
an account has no direct reference. Protected, missing, environment-backed,
browser-profile, and multiple-source states remain metadata-only; a focused
API regression covers protected and environment source bindings.

Batch 100 makes the account wizard provider-aware. It filters stored protected
credentials to the selected adapter's declared modes, clears a binding when
the provider changes, and labels the compatible credential types without
showing secret values.

Batch 101 expands the Account pools dialog with enabled-account counts, live
in-flight versus aggregate limits, and per-account weights. These values are
read-only snapshots until the existing transactional pool editor is opened.

Batch 102 adds account identity and redacted credential state/version to the
Health page's source table, keeping source runtime counters and binding
provenance visible in one operational view.

Batch 103 enforces the same provider credential-mode contract at the API
boundary. An account with a protected reference is rejected before any source
exists when its kind is incompatible with the catalog adapter, unless the
reviewed account override is explicit.

Batch 104 applies that contract before a new protected credential is written,
too. The PUT path validates the candidate metadata even when the reference did
not previously exist, so an incompatible account binding cannot be created by
ordering credential and account mutations around the wizard.

Batch 105 extends global search to credential provenance metadata (import
source and normalized domain) while keeping the existing ID and kind matches.
Selecting a result still opens the redacted credential editor.

Batch 106 expands Source Detail's configuration panel with the effective base
URL, organization/project routing fields, credential mode, source taxonomy,
execution/inference location, and billing declaration. These are configuration
metadata only; protected values remain in the vault.

Batch 107 adds a metadata-only action for discovered browser candidates. The
operator can configure a profile ID, engine, and loopback CDP endpoint and
continue into the transactional account wizard; discovery still reads no
cookies, launches no browser, and does not claim authentication.

Batch 108 surfaces the account `credential_type_override` posture as a
redacted reviewed flag in Accounts, Health, and Provider Detail, making
fail-open binding exceptions visible without exposing credential values.

Batch 109 unifies metadata-only session inventory and expire/clear controls
across ChatGPT Web, China Web, China Next, Zed Hosted, Amazon Q, and Augment
adapters. Explicit
`X-COT-Session` keys are represented by stable hashes in the control plane;
provider conversation identifiers remain metadata and no credential is
returned. Adapters that do not retain gateway session state continue to report
an explicit unsupported capability.

Batch 110 hardens the shared HTTP failure boundary. Dynamic error strings are
normalized and messages containing API-key, access-token, refresh-token,
password, Cookie, Authorization, or Bearer-shaped values are replaced with a
stable `request failed` category before JSON encoding; a focused regression
covers each sensitive shape.

Batch 111 closes the Zed Hosted expiry path: expiring a retained session now
rotates its provider thread ID before the next request, so the control action
cannot accidentally continue the previous conversation.

Batch 112 adds an upstream dispatch regression proving stateful adapter
inventory is available through the shared client while native API adapters
return an explicit unsupported capability instead of an empty, misleading
session list.

Batch 113 extends the isolated browser regression to the Sessions page,
covering redacted metadata rendering, capability boundaries, and the
lease-protected Clear confirmation flow.

Batch 115 makes account setup actions follow the selected provider descriptor.
Manual credential types are limited to declared modes; password-manager and
token imports are hidden when their resulting credential kinds are not
supported; browser-cookie import, isolated-profile creation and login checks
appear only for browser-capable providers. The regression keeps OpenAI's
environment API-key import path and checks the contrasting browser-only Doubao
toolbar without exposing protected values.

Batch 116 carries descriptor credential-mode filtering into the schema-driven
source editor. A source now sees only vault references compatible with its
adapter, while an existing incompatible reference remains visible for an
explicit review or override instead of being silently discarded. The browser
regression binds an API-key source and confirms a cookie reference is absent
from the OpenAI source dropdown.

Batch 117 consumes the descriptor's secret-field label in the account wizard's
manual credential form. OpenAI setup now labels the protected input `API key`,
while adapters without a specialized field retain the generic label; the
underlying value remains write-only and the credential kind still follows the
declared mode list.

Batch 118 extends the browser regression to multiple group membership. It
creates a second fallback group, adds the same two sources already ordered in
Auto, and verifies that the membership persists independently of the Auto
group's ordering transaction.

Batch 119 extends the browser regression through a synthetic browser-login
account setup for Claude Web. The flow selects an existing browser profile,
records launch and bounded check requests, saves the account transactionally,
and verifies the persisted account check remains redacted and evidence-backed.
The regression exercises the provider-specific login path without contacting a
real provider or extracting browser credentials.

Batch 120 routes Sessions expire/clear mutations through the shared adapter
session-manager contract. Stateful adapters that already expose metadata-only
inventory (including Zed Hosted and the China Web clients) can now be changed
from the admin API; stateless adapters still return an explicit unsupported
capability. A synthetic Zed Hosted API test seeds one local session through a
loopback upstream and verifies the lease-protected clear operation.

Batch 121 marks username/password records as supported login material for the
browser-auth-check descriptors (ChatGPT Web, Claude Web and Blackbox). The
browser regression now launches a password-manager CSV import from the Claude
Web account wizard, returns the protected reference into the draft, saves the
account and confirms that the imported secret never reaches the DOM.

Batch 122 verifies the hot-reload boundary in the browser regression. A runtime
queue-limit edit shows the restart-required warning during preview and remains
visible as pending after apply, while the transaction and configuration history
still complete normally.

Batch 123 adds hosted cross-platform CI. Ubuntu, Windows and macOS run the full
Go tests and vet; Ubuntu runs the full race detector; macOS executes the
cgo-enabled native Keychain round trip with a synthetic vault entry; and the
workflow builds Linux amd64, Windows amd64 and macOS arm64 gateway artifacts
with cgo disabled. This proves the repository's repeatable build and test
boundary, while live provider compatibility, signed releases and operator
device/browser environments remain explicit external checks. See [CI](CI.md).

Batch 124 extends **Browsers → Check connections** with the configured session
limit and bound account/source counts alongside the CDP page and browser
identity probe. These are clearly labeled configuration metadata: they do not
claim authentication or upstream capacity. The API regression covers a
connected synthetic CDP endpoint and ignores sources that are not bound to the
profile.

Batch 125 adds the same connection-capacity assertions to the isolated browser
regression, including the configured profile's bound-count and session-limit
columns before the login wizard starts.

Batch 126 makes manual credential setup descriptor-aware at field level. The
dialog hides Username for API-key credentials, labels the protected value as
Password for username/password login material, and suppresses a manual secret
field when a descriptor declares no credential fields. The UI regression covers
OpenAI API-key and Claude username/password forms without saving test secrets.

Batch 127 adds `scripts/redaction_audit.py` to the hosted CI checks. It scans
production `internal/` and `cmd/` Go files for direct logger imports/calls while
excluding tests, so adapter error details cannot bypass the existing fixed
category boundary through a newly added logging sink.

Batch 128 adds a storage-level shape check for `username_password` credentials.
Manual API writes now require one bounded JSON object with non-empty username
and password fields, while password-manager imports already produce that same
representation. Invalid strings, incomplete objects and unknown fields are
rejected before an encrypted vault transaction.

Batch 129 expands the browser profile contract to every browser family listed
by metadata discovery: Chrome, Edge, Brave, Firefox, Opera, Vivaldi, Chromium
and Arc. The schema-driven editor and discovered-profile flow expose the same
engine list. Dedicated launches use Chromium's isolated user-data directory or
Firefox's isolated profile flag; all require an explicit loopback CDP endpoint
and remain separate from authentication evidence.

Batch 130 applies the same bounded metadata validation to both OpenAI
organization and project fields. Control characters and overlong values are
rejected before configuration preview or persistence, so provider-specific
routing headers and Cloud Code Assist project metadata cannot inject a header
or oversized configuration value.

Batch 131 adds account-level Base URL, Organization, and Project defaults to
the setup wizard and schema-driven editor. Bound sources inherit omitted fields
and an untouched catalog endpoint, preserving explicit source overrides;
validation covers HTTPS or loopback endpoints and bounded metadata, and the
runtime client uses the effective values when constructing requests.

Batch 145 extends configuration preview impact to provider and browser-profile
changes. The API now reports before/after counts and sorted changed IDs for
these resources alongside sources, accounts and groups; the review dialog
renders all five resource classes so same-count lifecycle and enable/disable
edits remain explicit before apply.

Batch 144 corrects catalog preset metadata for browser-authenticated products.
Entries whose provider descriptor requires a browser or exposes a browser login
check now use `source_kind: browser_reverse` and
`credential_mode: browser_session`; anonymous browser products retain their
`anonymous` credential mode. The catalog test covers every descriptor-backed
browser entry so routing and credential views cannot classify it as a generic
product reverse adapter.

Batch 146 extends the isolated browser regression to the model enabled gate.
It selects one configured model, applies a disabled state through the bulk
editor and confirms the Models page reflects it, then restores the model to
enabled before continuing the source validation journey. This keeps model
enablement independently reviewable from provider, account and source gates.

Batch 147 normalizes Provider list type badges and filters from catalog kind plus
descriptor capabilities. Official APIs, cloud and aggregator endpoints,
browser and web reverse adapters, app and CLI subscriptions, local runtimes and
research entries now have stable human-readable labels while the underlying
machine metadata remains unchanged. The browser regression asserts the OpenAI
row renders `Official API`.

Batch 148 carries the same taxonomy into the Sources table and Source Detail.
Badges and detail values now explain `vendor_api`, `browser_reverse`,
`app_reverse`, `cli_reverse`, `local_model` and `custom_api` in operator-facing
language while configuration forms continue to submit their stable enum values.

Batch 149 adds browser regression assertions for both an Official API row and a
Browser reverse row, covering descriptor-driven type classification in the
Provider filter and list.

Batch 152 applies the normalized Provider type to Provider Detail, keeping the
catalog list, detail dialog, Sources table and Source Detail consistent while
retaining the machine-readable enum values in configuration forms.

Batch 153 adds a reviewed Credentials -> Unbind action. It removes a protected
reference from every matching account and source through the normal preview and
configuration transaction, keeps the vault value intact for optional deletion,
and the browser regression confirms the resulting entry is shown as Unbound.

Batch 150 adds a direct configuration-service rollback lifecycle regression.
An expected-revision conflict and a missing target revision now prove that no
runtime preparation, journal write, commit callback or revision advance occurs.

Batch 151 adds the missing model-management group action. The bulk model editor
can add every selected model's source to an existing group in the same reviewed
configuration transaction, while model enablement, approval and capability
changes remain atomic.

Batch 154 adds a cross-adapter replay and completion audit. The API regression
drives OpenAI Chat, Anthropic Messages and Gemini through both buffered and
streaming requests, verifies model rewriting and credential headers, and checks
that each protocol's terminal event is required for a successful stream. This
is a local contract audit; live provider compatibility remains an explicit
operator check.

Batch 155 adds a routing strategy matrix. Router regressions now cover ordered
fallback advancement, select's no-fallthrough rule, latency and load-balance
selection, shared source membership across groups, disabled-member simulator
reasons, and the fact that simulation never acquires capacity. The matrix uses
synthetic sources and does not claim live-provider behavior.

Batch 156 adds a dedicated reviewed credential unbind API at
`POST /admin/credentials/{id}/unbind`. It requires explicit confirmation and
an optimistic revision, clears direct and account-inherited source references
in one configuration transaction, reports the affected IDs, and retains the
encrypted vault value for later rebind or explicit deletion.

Batch 157 adds a reviewed provider-disable step to the control-plane UI. The
disable action first explains that existing requests may finish and lists the
affected accounts and sources; Cancel leaves the switch unchanged, while
Review disable enters the existing revision-aware preview/apply transaction.
The browser regression covers cancellation, review/apply, re-enable, the
affected-account rendering, and the disabled account-health projection with a
rebuilt isolated gateway binary.

Batch 158 exposes the existing bound-profile login flow as explicit `Login`
actions in Provider Detail and `Re-authenticate` actions in Accounts. The
shared launch helper opens the existing `/admin/accounts/{id}/login` endpoint
and continues into the metadata-only login evidence watcher; the browser
regression covers the synthetic launch and check without opening a real
provider browser.

Batch 159 adds Provider Detail deletion to the same reviewed control-plane
workflow. The confirmation dialog inventories bound accounts and sources and
keeps the provider configuration intact on Cancel; Review deletion submits the
optimistic revision through the shared preview when no references are bound;
bound references are rejected before preview with an actionable migration
message, matching the service deletion conflict.

Batch 160 adds the remaining browser-account credential controls. Provider
Detail now offers `Refresh session` beside `Login`, and Credentials offers
`Re-login` when a protected reference is bound to a browser-profile account;
the latter presents the eligible accounts before launching the same isolated
profile flow, without exposing the protected value.

Batch 161 gives account deletion its own reviewed lifecycle action. The
Accounts and Provider Detail views now inventory bound sources, reject deletion
with an actionable migration message while references remain, and preserve the
existing account on Cancel.

Batch 162 makes shared-quota changes an explicit sensitive confirmation. The
Health quota editor now lists every affected account and source before the
revision-aware preview, calls out in-flight requests, and keeps Cancel
side-effect free; the browser regression covers both the impact rendering and
the cancellation path before applying a rename.

Batch 163 applies the same quota impact review to Account and Source editor
changes. A quota-domain mutation now lists the affected account/source members
and new domain before entering the shared configuration preview, while Back
leaves the edit form unchanged.

Batch 164 resets the quota confirmation state whenever an Account or Source
editor is reopened, including after Back or a stale-revision response, so a
later quota mutation cannot reuse an earlier approval.

Batch 165 carries the bound provider descriptor into Credentials replacement
and global-search editors. Provider-specific secret-field labels remain
visible when replacing an existing protected value, while the vault keeps its
explicit credential-kind choices; the browser regression verifies the OpenAI
`API key` label without exposing the value.

Batch 166 gives generic destructive actions an impact review. Source deletion
now removes its group memberships in the same reviewed transaction, while
browser-profile deletion lists bound accounts and fails closed until they are
migrated; the browser regression covers source cancellation and the profile
preflight error.

Batch 167 broadens global search across catalog references/notes, provider
protocols, source metadata, group policy, model capability fields, profile
endpoints, and device metadata while keeping secrets out of the search index.
The browser regression verifies a provider found by its catalog description.

Batch 168 makes the Provider administration API self-describing. `GET
/admin/providers` now returns the durable configured items alongside the inert
catalog and descriptor registries; `GET /admin/providers/{id}` also returns
the matching catalog entry, descriptor and bound account/source IDs. Existing
configuration fields remain under `items`/`item`, so clients can adopt the
joined view without changing mutation semantics. The API regression verifies
the OpenAI catalog and HTTP descriptor are present and that no associations are
invented.

Batch 169 closes the catalog-to-Source onboarding gap. Creating or editing a
Source through the persistent API or embedded console now materializes its
parent Provider policy when one is not already configured. The new record is
enabled for explicit use but excluded from Auto until reviewed, and existing
Provider settings are untouched. An API regression verifies a direct Source
creation produces the parent policy with the safe defaults.
