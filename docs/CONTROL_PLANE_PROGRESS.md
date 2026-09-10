# Control plane implementation progress

Acceptance scope: [complete synthesis](CONTROL_PLANE_REQUIREMENTS.md).

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
    (73148a3). Other providers return explicitly unsupported login checks.
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

Discovery protocol references: [Anthropic models](https://platform.claude.com/docs/en/api/models/list),
[Gemini models](https://ai.google.dev/api/models),
[OpenAI models](https://platform.openai.com/docs/api-reference/models).

Validation: `go test ./...` passed, 535 tests in 37 packages on Windows.
Windows and Linux-target `govulncheck` reported no vulnerabilities.
Browser regression: credential creation/redaction, account binding, source
creation, group membership, preview/apply, every navigation destination and
390px viewport passed via `scripts/ui_fixture.py --test` after building
`.clash-tokens/ui-test.exe`. Screenshots are in `.clash-tokens/ui-artifacts/`.
Race detection is outstanding: the current Go environment has cgo disabled.

## Remaining implementation

- A: Descriptor registry, accounts and credential references, group metadata
  validation, transactional configuration history, persistence and runtime swaps.
- B: Protected credential store, selected export importers, matching,
  environment/CLI imports, browser profile registry and interactive login.
- C: Complete authenticated administration APIs, model discovery, validation,
  health, quota pools, session controls and live evidence.
- D: Complete embedded frontend with forms, previews, navigation, search,
  routing simulator, fallback ordering and configuration history.
- E: Request-level safe fallback, protocol completion tracking, verification
  provenance, documentation and end-to-end regression validation.

Browser profiles and login launching now have management controls. Device and
Session destinations still primarily expose settings. Login detection, session
operations, specialized imports, discovery and live validation remain required. Account pool strategies now execute in the router. Activity
currently covers configuration history, not a complete runtime event log.

The current application is not yet the completed control plane. Credentials
and upstream availability must never be inferred from catalog implementation.
