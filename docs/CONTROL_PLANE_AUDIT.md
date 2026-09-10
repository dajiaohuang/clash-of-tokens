# Control plane acceptance audit

Scope: all 51 sections in [the original synthesis](CONTROL_PLANE_REQUIREMENTS.md).
This is an active gap ledger, initially inspected at `279105f` on 2026-09-10.
Implemented mechanisms below are evidence of progress, **not acceptance of the
whole section**. No section is declared fully accepted by this initial pass.
The original requirements remain authoritative; examples and subrequirements
must be checked again before final acceptance.

| Section | Current evidence | Remaining work or verification |
| --- | --- | --- |
| 1. Runtime correctness | `config/metadata.go`, router metadata/attempt/stream tests | End-to-end cross-adapter replay/completion audit; metadata preset accuracy |
| 2. Account/credential separation | `config/accounts.go`, `credential.go`, injected adapter resolution | Finish provider-specific credential setup and migration UX |
| 3. Account model | Account configuration and authentication evidence | Account health/capacity/last validation aggregation |
| 4. Protected store | DPAPI; Linux native Secret Service integration tests; AES-GCM envelope tests | Native macOS cgo build/runtime verification |
| 5. Add any account | Batch 27 account wizard combines credential/import selection and profile login | Complete provider-specific forms, organization/project setup and broader login checks |
| 6. Browser import | Standard profile metadata and selected CDP Cookie import | Selected existing-profile account candidates and all named browser import paths |
| 7. Manager import | All named CSV formats, Bitwarden JSON, redacted selection | End-to-end account binding from import; verify all format fixtures |
| 8. Provider matching | Exact catalog-domain/type suggestions | Complete domain aliases and direct binding workflow |
| 9. Launch login | Dedicated browser launch and bounded UI watcher | Broader provider detection and complete account creation flow |
| 10. Profile registry | Profile CRUD, per-account CDP/session isolation | Existing-profile onboarding and lifecycle status |
| 11. Full management pages | Provider Detail; batch 33 adds global workload/queue, buffer and Go-memory metrics, uptime and explicit lifetime-average rate to Overview/Metrics | Process/session inventory; broader account health; credential last use/status; full end-to-end page acceptance |
| 12. Groups | Batch 28 adds vision requirement, declared input/output rate ceiling and ordered preferences to configuration/UI/router | Total-request cost accounting and full policy acceptance still outstanding |
| 13. Multiple memberships | Source editor and group membership controls | Explicit end-to-end multiple-group routing verification |
| 14. Source Detail | Batch 29 consolidates configuration/models/capacity/runtime/TTFT/evidence and edit/check actions; explicit three-sample benchmark with cancellation tests | Full live-adapter acceptance and aggregate benchmark persistence remain unverified |
| 15. Routing editor | Batch 28 adds five ordered preferences with explicit rate/latency semantics and dispatch tests | Complete cross-strategy acceptance and total-request cost limits |
| 16. Drag order | Ordered group source widget | Browser drag/drop and actual fallback order test |
| 17. Independent switches | Provider/account/source/model enable/Auto gates | Full UI-to-runtime four-level acceptance test |
| 18. All frontend configuration | Schema-driven forms and config transactions | Missing requested settings must first exist in schema/runtime |
| 19. Configuration service | Journal, compile/prepare, atomic runtime publication tests | Fault and rollback lifecycle audit across all managed resources |
| 20. Versions/rollback | Version history and Compare/restore | Browser rollback/conflict acceptance test |
| 21. Model management | Native API ID discovery; batch 35 adds filtered multi-selection and atomic enable/Auto/rating/capability edits | Rich discovery metadata where available and reverse-provider discovery |
| 22. Discovery approval | New models disabled, unrated and not Auto-approved | Verify persisted discovery selections through routing |
| 23. Official sources | Generic drivers; batch 34 adds Ark, legacy Hunyuan, Qianfan v2 and two TokenHub region presets with official references and path/auth tests | Live account/model compatibility, other named provider/custom-profile acceptance and optional service-specific parameters |
| 24. Visible source types | Source-kind badge and provider-kind filter | Consistent complete official/cloud/browser/app/CLI/local/custom taxonomy |
| 25. Explain exclusions | Router Explain in simulator; batches 29/30 add filtered Source/Provider Detail explanations | Full policy-reason acceptance across configured adapters |
| 26. Simulator | Model/protocol/tools/vision/size without upstream request | Ordered candidates and distinction between eligibility and final choice |
| 27. Health | Runtime outcome, TTFT, last status/timestamps exist | Surface all fields; provider/account aggregation and distinct degraded/exhausted/broken states |
| 28. Test Provider | Explicit one-request stream validation | Separate connection/auth/request/stream/completion/latency presentation |
| 29. Live verification | Batch 25 derives status/counts from model/protocol evidence and source/credential-version bindings; rotation invalidation tested | Real configured-provider checks and complete account/capability evidence still require verification |
| 30. Discovery hub | Batch 27 returns import results to account setup with single-result binding | Unified automatic Discover Accounts candidates and broader matching |
| 31. Shared credential | Account reference shared by multiple sources and quota domain | End-to-end reference reuse/capacity display |
| 32. Manual binding override | Matching and incompatible-type rejection | Explicit reviewed type-override requested by the synthesis is absent |
| 33. Quota domain view | Batch 26 adds domain members/shared counters, retiring leases and atomic all-member limit editing | Account/domain moves and rename behavior still need complete UX/runtime audit |
| 34. Account pools | Four router strategies; batch 26 adds pool editor, account switches/capacity/weights and UI persistence tests | Broader end-to-end dispatch/strategy acceptance |
| 35. Sessions | ChatGPT metadata and expire/clear | Other stateful adapter inventories and controls |
| 36. Browsers | Registry, CDP status, account login | Owned-process launch/stop/open-provider and session capacity view |
| 37. Login wizard | Batch 27 unifies existing/isolated profile, import/manual credential and login-to-save with synthetic browser tests | All installed-browser account discovery, provider-specific setup and real provider compatibility |
| 38. Devices | Frontend device settings | Connected-device/ADB/resolution/foreground app/login/test evidence; implement with synthetic tests, no emulator operations |
| 39. Implementation status | Catalog implementation field | Reference/factory/runtime/verified/broken/research view |
| 40. Descriptor registry | Shared validation/factory/schema contract | More provider-specific credential/discovery/auth capabilities; eliminate residual setup switches |
| 41. Administration API | Persistent CRUD, config, simulator, source checks | Account validate and complete browser/device lifecycle operations |
| 42. Embedded frontend | Go-embedded static HTML/CSS/JS, no Node runtime | Packaging/build verification |
| 43. Navigation | All named destinations; batch 32 adds bounded runtime execution events and source/outcome filters in Activity | Complete content on every destination and broader event coverage |
| 44. Search | Global search implementation | Each required entity search + navigation browser test |
| 45. Preview | Config diff and optimistic revision preview | Eligibility/account impact before apply; credential replacement preview |
| 46. Central validation | Schema validation plus runtime prepare before apply | All special actions must retain the same invariants |
| 47. Hot reload | Generation pinning and restart-required fields | Full browser confirmation of live vs pending runtime settings |
| 48. Sensitive confirmation | Config preview, credential-delete confirmation, billing notices | All specified sensitive actions and cost warnings acceptance test |
| 49. Secret-safe logs | Redacted vault/import/session/evidence responses; fixed execution categories; batch 32 stores bounded structured events without raw error/body/credential fields | Global Secret representation and all adapter/error/log paths audit; pre-acquisition rejections not logged |
| 50. Five epics | Work split into committed batches | A–E remain incomplete until their component rows are accepted |
| 51. End-to-end workflow | Most core pieces exist separately | Install → discover → import/login → accounts → sources → Auto/groups → successful local API call without hand-editing JSON |

Next implementation priorities from this pass: truthful live-verification
status and binding provenance, quota/account pool visibility, the unified
onboarding wizard, owned browser/device controls, and runtime event redaction.
Each subsequent batch should update the affected rows and provide tests rather
than treating this ledger itself as proof of completion.
