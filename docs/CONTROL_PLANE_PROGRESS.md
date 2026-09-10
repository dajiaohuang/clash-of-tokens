# Control plane implementation progress

Acceptance scope: [complete synthesis](CONTROL_PLANE_REQUIREMENTS.md).

## Completed batches

1. Recorded all 51 sections of the user-authorized synthesis (881a134).
2. Separated source kind, execution location, inference location, billing mode,
   and credential mode. Local-only routing requires explicitly declared local
   inference through a local model API; local CLI processes are excluded.
   Metered, subscription and unknown-cost routing have independent overrides.
   Catalog presets no longer derive billing from process location (a51e968).

Validation: `go test ./...` passed, 460 tests in 33 packages on Windows.

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
