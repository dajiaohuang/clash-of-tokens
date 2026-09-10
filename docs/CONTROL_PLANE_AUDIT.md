# Control plane acceptance audit

Scope: all 51 sections in [the original synthesis](CONTROL_PLANE_REQUIREMENTS.md).
This is an active gap ledger, initially inspected at `279105f` on 2026-09-10.
Implemented mechanisms below are evidence of progress, **not acceptance of the
whole section**. No section is declared fully accepted by this initial pass.
The original requirements remain authoritative; examples and subrequirements
must be checked again before final acceptance.

| Section | Current evidence | Remaining work or verification |
| --- | --- | --- |
| 1. Runtime correctness | `config/metadata.go`, router metadata/attempt/stream tests; both streaming and non-streaming leases record redacted execution health | End-to-end cross-adapter replay/completion audit; metadata preset accuracy |
| 2. Account/credential separation | `config/accounts.go`, `credential.go`, injected adapter resolution | Finish provider-specific credential setup and migration UX |
| 3. Account model | Account configuration plus `/admin/status` account health aggregation: health/auth state, capacity, sources, counters, last success/failure and last validation; explicit account validation entry point; Batch 60 blocks deletion while sources remain bound | Provider-specific health evidence and broader account lifecycle |
| 4. Protected store | DPAPI; Linux native Secret Service integration tests; AES-GCM envelope tests; credentials package cross-compiles for darwin/amd64 and linux/amd64 with cgo disabled | Native macOS cgo runtime verification |
| 5. Add any account | Batch 27 account wizard combines credential/import selection and profile login | Complete provider-specific forms, organization/project setup and broader login checks |
| 6. Browser import | Standard profile metadata and selected CDP Cookie import | Selected existing-profile account candidates and all named browser import paths |
| 7. Manager import | All named CSV formats, Bitwarden JSON, redacted selection | End-to-end account binding from import; verify all format fixtures |
| 8. Provider matching | Exact catalog-domain/type suggestions plus official product/API aliases with lookalike rejection | Broader alias coverage and direct binding workflow |
| 9. Launch login | Dedicated browser launch and bounded UI watcher | Broader provider detection and complete account creation flow |
| 10. Profile registry | Profile CRUD, per-account CDP/session isolation; Batch 61 blocks profile deletion while accounts remain bound | Existing-profile onboarding and lifecycle status |
| 11. Full management pages | Provider Detail; global workload/queue, buffer and Go-memory metrics; source token/cost observations in Source Detail and Metrics; account health/capacity rows in Accounts; credential last-use metadata in Credentials; owned browser process and session capability views | Full end-to-end page acceptance |
| 12. Groups | Batch 28 adds vision requirement, declared input/output rate ceiling and ordered preferences to configuration/UI/router; Batch 54 records declared usage and cost observations without inventing missing tokens; Batch 62 blocks source deletion while group membership remains | Full policy acceptance and any provider-specific billing controls still require verification |
| 13. Multiple memberships | Source editor and group membership controls | Explicit end-to-end multiple-group routing verification |
| 14. Source Detail | Batch 29 consolidates configuration/models/capacity/runtime/TTFT/evidence and edit/check actions; explicit three-sample benchmark with cancellation tests | Full live-adapter acceptance and aggregate benchmark persistence remain unverified |
| 15. Routing editor | Batch 28 adds five ordered preferences with explicit rate/latency semantics and dispatch tests; Batch 54 exposes bounded runtime token/cost accounting | Complete cross-strategy acceptance and any hard per-request budget controls |
| 16. Drag order | Ordered group source widget | Browser drag/drop and actual fallback order test |
| 17. Independent switches | Provider/account/source/model enable/Auto gates | Full UI-to-runtime four-level acceptance test |
| 18. All frontend configuration | Schema-driven forms and config transactions | Missing requested settings must first exist in schema/runtime |
| 19. Configuration service | Journal, compile/prepare, atomic runtime publication tests | Fault and rollback lifecycle audit across all managed resources |
| 20. Versions/rollback | Version history and Compare/restore | Browser rollback/conflict acceptance test |
| 21. Model management | Native API discovery with bounded pagination and provider metadata (display/owner/created/token limits/methods); batch 35 adds filtered multi-selection and atomic enable/Auto/rating/capability edits | Reverse-provider discovery and live compatibility |
| 22. Discovery approval | Configure-model action persists discovered/canonical IDs as disabled, unrated and not Auto-approved; routing ignores them until explicit policy edits | Verify persisted discovery selections through routing and richer reverse-provider approval |
| 23. Official sources | Generic drivers; batch 34 adds Ark, legacy Hunyuan, Qianfan v2 and two TokenHub region presets with official references and path/auth tests | Live account/model compatibility, other named provider/custom-profile acceptance and optional service-specific parameters |
| 24. Visible source types | Source-kind badge and provider-kind filter | Consistent complete official/cloud/browser/app/CLI/local/custom taxonomy |
| 25. Explain exclusions | Router Explain in simulator; batches 29/30 add filtered Source/Provider Detail explanations | Full policy-reason acceptance across configured adapters |
| 26. Simulator | Model/protocol/tools/vision/size without upstream request | Ordered candidates and distinction between eligibility and final choice |
| 27. Health | Runtime outcome, TTFT, declared token counts/cost-known state, last status/timestamps and account health/auth aggregation exist; Batch 59 adds the account health table beside source and shared-domain views | Surface all source/account fields and distinguish degraded/exhausted/broken states consistently |
| 28. Test Provider | Explicit one-request stream validation now records separate connection/auth/request/stream/completion stages and duration | Provider-specific phase detail and live compatibility still require operator checks |
| 29. Live verification | Batch 25 derives status/counts from model/protocol evidence and source/credential-version bindings; rotation invalidation tested | Real configured-provider checks and complete account/capability evidence still require verification |
| 30. Discovery hub | Batch 27 returns import results to account setup with single-result binding; batch 43 adds bounded metadata-only account candidates from vault, configured environments and optional standard-browser profile scan; stored candidates can prefill the account wizard | End-to-end candidate binding and broader provider matching |
| 31. Shared credential | Account reference shared by multiple sources and quota domain; API integration test binds one protected reference to an account, two sources and an Auto request | End-to-end UI evidence and live-provider reuse still require operator verification |
| 32. Manual binding override | Descriptor matching rejects incompatible kinds by default; account/source `credential_type_override` is persisted, warned in the editor, and covered by binding tests | Provider-specific contract review and end-to-end UI acceptance for every credential kind |
| 33. Quota domain view | Batch 26 adds domain members/shared counters, retiring leases and atomic all-member limit editing; Batch 57 cascades an account quota-domain move to all bound sources transactionally; Batch 58 applies the same invariant to source edits and source-account moves | Rename behavior and complete UX/runtime audit still require verification |
| 34. Account pools | Four router strategies; batch 26 adds pool editor, account switches/capacity/weights and UI persistence tests | Broader end-to-end dispatch/strategy acceptance |
| 35. Sessions | ChatGPT metadata and expire/clear plus capability rows for every source | Other stateful adapter inventories and controls |
| 36. Browsers | Batch 37 retains launch handles/identities, lists owned processes, opens provider origins and confirms exact-launch stops; Batch 61 protects bound browser profiles from deletion; synthetic process/UI tests | Browser child-process lifecycle across platforms and session capacity view |
| 37. Login wizard | Batch 27 unifies existing/isolated profile, import/manual credential and login-to-save with synthetic browser tests | All installed-browser account discovery, provider-specific setup and real provider compatibility |
| 38. Devices | `POST /admin/device/check` read-only doctor reports ADB/OCR paths, connection, resolution, foreground package, app installation, and evidence-backed login/last-test rows; Devices page and synthetic API/UI tests | Physical-device login and live test evidence still require explicit operator action; no emulator operations |
| 39. Implementation status | `GET /admin/implementation` and System > Implementation join catalog kind/reference, descriptor factory, configured counts, catalog live flag and runtime check counts with separate provenance labels | Live compatibility and broken-state history still require explicit provider checks |
| 40. Descriptor registry | Shared validation/factory/schema contract | More provider-specific credential/discovery/auth capabilities; eliminate residual setup switches |
| 41. Administration API | Persistent CRUD, config, simulator, source checks, account validation, browser process controls and device doctor; Batch 60 returns a clear conflict for account deletion with bound sources; Batch 61 does the same for browser profiles; Batch 62 does the same for group-bound sources | Broader browser/device lifecycle operations |
| 42. Embedded frontend | Go-embedded static HTML/CSS/JS, no Node runtime | Packaging/build verification |
| 43. Navigation | All named destinations; batch 32 adds bounded runtime execution events and source/outcome filters in Activity; Batch 54 includes declared usage in status/events | Complete content on every destination and broader event coverage |
| 44. Search | Batch 36 adds explicit model/browser/device matches and tests all eight entity navigation paths | Broader naming/localization and large-catalog usability acceptance |
| 45. Preview | Config diff, optimistic revision preview, bounded read-only group eligibility/account/source impact counts before apply, and credential replacement impact review | Complete impact coverage for every sensitive action |
| 46. Central validation | Schema validation plus runtime prepare before apply | All special actions must retain the same invariants |
| 47. Hot reload | Generation pinning and restart-required fields | Full browser confirmation of live vs pending runtime settings |
| 48. Sensitive confirmation | Config preview, credential-delete confirmation, credential replacement impact review, billing notices | All specified sensitive actions and cost warnings acceptance test |
| 49. Secret-safe logs | Redacted vault/import/session/evidence responses; fixed execution categories; batch 32 stores bounded structured events without raw error/body/credential fields; Batch 54 stores only numeric declared usage/cost fields; generation and model-discovery HTTP errors use stable public categories | Global Secret representation and all adapter/error/log paths audit; pre-acquisition rejections not logged |
| 50. Five epics | Work split into committed batches | A–E remain incomplete until their component rows are accepted |
| 51. End-to-end workflow | API integration test covers protected credential → account → two inherited sources → Auto group → successful synthetic local API call without editing JSON | Browser-driven full install/import/login journey and live-provider call remain external-environment checks |

Next implementation priorities from this pass: truthful live-verification
status and binding provenance, quota/account pool visibility, the unified
onboarding wizard, owned browser/device controls, and runtime event redaction.
Each subsequent batch should update the affected rows and provide tests rather
than treating this ledger itself as proof of completion.
