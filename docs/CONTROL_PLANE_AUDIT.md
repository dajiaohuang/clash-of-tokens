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
| 5. Add any account | Provider Add Account and credential references | Provider-specific forms, login/import paths, organization/project setup |
| 6. Browser import | Standard profile metadata and selected CDP Cookie import | Selected existing-profile account candidates and all named browser import paths |
| 7. Manager import | All named CSV formats, Bitwarden JSON, redacted selection | End-to-end account binding from import; verify all format fixtures |
| 8. Provider matching | Exact catalog-domain/type suggestions | Complete domain aliases and direct binding workflow |
| 9. Launch login | Dedicated browser launch and bounded UI watcher | Broader provider detection and complete account creation flow |
| 10. Profile registry | Profile CRUD, per-account CDP/session isolation | Existing-profile onboarding and lifecycle status |
| 11. Full management pages | Embedded UI, provider/account/credential pages | Overview queue/rate/memory/process/session data; account health/capacity; credential last use/status; complete Provider Detail actions |
| 12. Groups | Group CRUD, billing/kind/tier/tool policies | Vision requirement, cost ceiling and policy coverage |
| 13. Multiple memberships | Source editor and group membership controls | Explicit end-to-end multiple-group routing verification |
| 14. Source Detail | Source/model forms, validation | Consolidated detail view, capability/health/TTFT data, optional explicit benchmark |
| 15. Routing editor | Group strategies, attempt limits, runtime queue fields | Requested preference/cost controls and clear strategy semantics |
| 16. Drag order | Ordered group source widget | Browser drag/drop and actual fallback order test |
| 17. Independent switches | Provider/account/source/model enable/Auto gates | Full UI-to-runtime four-level acceptance test |
| 18. All frontend configuration | Schema-driven forms and config transactions | Missing requested settings must first exist in schema/runtime |
| 19. Configuration service | Journal, compile/prepare, atomic runtime publication tests | Fault and rollback lifecycle audit across all managed resources |
| 20. Versions/rollback | Version history and Compare/restore | Browser rollback/conflict acceptance test |
| 21. Model management | Native API ID discovery and disabled model setup | Rich metadata where available; bulk operations; reverse-provider discovery |
| 22. Discovery approval | New models disabled, unrated and not Auto-approved | Verify persisted discovery selections through routing |
| 23. Official sources | Generic drivers and many official presets | Check every named provider and custom profile; Ark/Hunyuan/Qianfan not found in this pass |
| 24. Visible source types | Source-kind badge and provider-kind filter | Consistent complete official/cloud/browser/app/CLI/local/custom taxonomy |
| 25. Explain exclusions | Router Explain surfaced in simulator | Explanations from Source/Provider detail and all policy reasons |
| 26. Simulator | Model/protocol/tools/vision/size without upstream request | Ordered candidates and distinction between eligibility and final choice |
| 27. Health | Runtime outcome, TTFT, last status/timestamps exist | Surface all fields; provider/account aggregation and distinct degraded/exhausted/broken states |
| 28. Test Provider | Explicit one-request stream validation | Separate connection/auth/request/stream/completion/latency presentation |
| 29. Live verification | Batch 25 derives status/counts from model/protocol evidence and source/credential-version bindings; rotation invalidation tested | Real configured-provider checks and complete account/capability evidence still require verification |
| 30. Discovery hub | Separate browser/export/environment/CLI import actions | Unified Discover Accounts candidates and import/bind flow |
| 31. Shared credential | Account reference shared by multiple sources and quota domain | End-to-end reference reuse/capacity display |
| 32. Manual binding override | Matching and incompatible-type rejection | Explicit reviewed type-override requested by the synthesis is absent |
| 33. Quota domain view | Quota configuration and shared router counters | Domain tree, runtime totals and transactional shared-domain editor |
| 34. Account pools | Four router strategies and account weights | Pool view with per-account switches/capacity/weights |
| 35. Sessions | ChatGPT metadata and expire/clear | Other stateful adapter inventories and controls |
| 36. Browsers | Registry, CDP status, account login | Owned-process launch/stop/open-provider and session capacity view |
| 37. Login wizard | Separate account/profile/import/login controls | Unified existing/isolated/import/manual path ending in Save account |
| 38. Devices | Frontend device settings | Connected-device/ADB/resolution/foreground app/login/test evidence; implement with synthetic tests, no emulator operations |
| 39. Implementation status | Catalog implementation field | Reference/factory/runtime/verified/broken/research view |
| 40. Descriptor registry | Shared validation/factory/schema contract | More provider-specific credential/discovery/auth capabilities; eliminate residual setup switches |
| 41. Administration API | Persistent CRUD, config, simulator, source checks | Account validate and complete browser/device lifecycle operations |
| 42. Embedded frontend | Go-embedded static HTML/CSS/JS, no Node runtime | Packaging/build verification |
| 43. Navigation | All named destinations represented; Activity for logs | Complete content on each destination; runtime log view |
| 44. Search | Global search implementation | Each required entity search + navigation browser test |
| 45. Preview | Config diff and optimistic revision preview | Eligibility/account impact before apply; credential replacement preview |
| 46. Central validation | Schema validation plus runtime prepare before apply | All special actions must retain the same invariants |
| 47. Hot reload | Generation pinning and restart-required fields | Full browser confirmation of live vs pending runtime settings |
| 48. Sensitive confirmation | Config preview, credential-delete confirmation, billing notices | All specified sensitive actions and cost warnings acceptance test |
| 49. Secret-safe logs | Redacted vault/import/session/evidence responses | Global Secret representation and all adapter/runtime error/log paths audit |
| 50. Five epics | Work split into committed batches | A–E remain incomplete until their component rows are accepted |
| 51. End-to-end workflow | Most core pieces exist separately | Install → discover → import/login → accounts → sources → Auto/groups → successful local API call without hand-editing JSON |

Next implementation priorities from this pass: truthful live-verification
status and binding provenance, quota/account pool visibility, the unified
onboarding wizard, owned browser/device controls, and runtime event redaction.
Each subsequent batch should update the affected rows and provide tests rather
than treating this ledger itself as proof of completion.
