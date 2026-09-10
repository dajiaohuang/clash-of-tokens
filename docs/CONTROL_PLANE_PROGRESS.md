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
    source credential-type validation (0b92b8f). Specialized manager formats,
    environment/CLI import and browser-session discovery remain outstanding.
15. Browser profile registry, account binding, per-profile CDP/session storage
    and explicit dedicated Chrome/Edge/Chromium login launch. Authentication
    detection and post-login validation remain outstanding; launch is never
    reported as authenticated.

16. ChatGPT Web authentication evidence without generation or token extraction
    (73148a3). Additional browser checks and persisted evidence are in batch 24.
17. Native OpenAI/Anthropic/Gemini model discovery with bounded pagination,
    explicit partial-list reporting and disabled model configuration; manual
    source validation observes output and protocol completion under a lease.
    Disabled sources remain disabled. Validation does not retry. Discovery
    does not imply generation, tool or quality support. Evidence is returned
    to the caller and persisted in bounded revision-linked history (f2bd6a6).

18. Local ChatGPT session metadata and lease-protected expire/clear operations
    (c31a76a). Upstream conversations are not deleted. Other session adapters
    explicitly report unsupported management.
19. Configured-source environment import and selected Codex/Gemini/OAuth JSON
    access-token import. Preview never returns tokens; imports allocate new
    references. CLI refresh tokens and account IDs are not imported and there
    is no automatic refresh. The user must re-import current access tokens and
    configure supplementary account fields separately.

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
    passed in an isolated Ubuntu 24.04 WSL keyring session. macOS cgo/native
    execution remains unverified. See [storage and verification](CREDENTIAL_STORAGE.md).
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
    serialized status redaction and snapshot isolation. This is one boundary of
    the broader secret/log audit; runtime event logging remains outstanding.
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

Discovery protocol references: [Anthropic models](https://platform.claude.com/docs/en/api/models/list),
[Gemini models](https://ai.google.dev/api/models),
[OpenAI models](https://platform.openai.com/docs/api-reference/models).

Validation: `go test ./...` passed, 568 tests in 38 packages on Windows.
Windows and Linux-target `govulncheck` reported no vulnerabilities.
Browser regression: credential creation/redaction, account binding, source
creation, group membership, preview/apply, every navigation destination and
390px viewport passed via `scripts/ui_fixture.py --test` after building
`.clash-tokens/ui-test.exe`. Screenshots are in `.clash-tokens/ui-artifacts/`.
Race detection is outstanding: the current Go environment has cgo disabled.

## Remaining implementation

The [51-section ledger](CONTROL_PLANE_AUDIT.md) now replaces the previous generic
A–E checklist. The main gaps include unified account discovery/onboarding,
complete provider-specific setup and checks, quota/account pool views, richer
model management, broader session management, runtime log redaction, remaining
routing policies and native macOS verification.
All explicit subrequirements and the end-to-end install-to-routing workflow must
be verified before completion.

The current application is not yet the completed control plane. Credentials
and upstream availability must never be inferred from catalog implementation.
