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
   (8a2ccac). Non-Windows platform keychains remain outstanding and fail closed.

6. Atomic configuration journal, optimistic revision checks, preview and
   rollback (895308f). The sibling `.state` file becomes authoritative after
   the first administrative change; the original JSON remains an import seed.
7. Request-pinned runtime generations preserve shared account/quota capacity,
   metrics and ingress admission. Source changes affect new requests while
   held requests complete; listener/runtime/browser/device changes are marked
   restart-required (61fb2bb).
8. Persistent Provider/Account/Source/Group CRUD, revision-checked patches,
   and a no-upstream routing simulator (811415d).

Validation: `go test ./...` passed, 481 tests in 34 packages on Windows.
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

The current application is not yet the completed control plane. Credentials
and upstream availability must never be inferred from catalog implementation.
