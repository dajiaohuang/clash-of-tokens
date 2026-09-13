# Next release acceptance status

This is a work-in-progress implementation against [the requested checklist](NEXT_RELEASE_CHECKLIST.md), based on `8387d509`. It is **not a release approval or a claim that all requirements are complete**. Changes are currently local and uncommitted. The original attachment containing the exact 92 tasks, 28 scenarios and 51-item mapping was not available in the retrieved discussion; those mappings must not be invented.

## Implementation and evidence

| Scope | Implemented and exercised locally | Remaining acceptance |
|---|---|---|
| Credential resolution | Explicit references work through actual adapters; resolver context propagates cancellation; invalid explicit references do not fall back to environment credentials | Complete factory/protocol success coverage for every executable adapter |
| Imports and managers | Selective imports, bounded expiring previews, versioned keep/replace/new conflicts; selected-field `op`/`bw` invocation with bounded output and cancellation | Versioned exports from each named vendor; real manager authorization; paired external username/password login |
| Login and renewal | ChatGPT browser execution binding; Claude captured-cookie tenant verification; encrypted OAuth grants, coordinated refresh, rotation, backoff and revocation tombstones | Other provider-specific login-to-invocation bridges; automatic refresh from imported CLI exports; live upstream lifecycle tests |
| Browser and device | Chromium CDP and Firefox BiDi fixture execution; owned tab cleanup; Windows owned process-tree termination; explicit engine validation | Every browser-brand/platform combination; Unix process-tree ownership and restart/crash reconciliation; authorized physical-device and account checks |
| Control plane | Bounded jobs, events, cancellation, timeout, resource coordination, stale-result rejection; long operations outside the configuration commit lock | Exhaustive interruption/failure injection across all config/vault transactions |
| Frontend | Real Go Admin API fixture, import conflict handling, job cancellation, session binding, destination confirmation, table pagination/search, four native test-console protocols | Full page/action acceptance matrix; runtime refresh for all account/health fields; full first-run journey |
| Routing and streaming | Existing regression suite retained; 100 hot updates preserve held requests and reject new work after disable | Exact eight-switch UI matrix and all requested protocol termination combinations |
| Provider contracts | Browser-dependent provider fixtures exercised on actual Chromium and Firefox engines against local synthetic sites | 100% executable-adapter factory/protocol contract coverage has not been established; live verification is separate |
| Storage and security | Windows/Linux exact-binary vault lifecycle harness, canary scanning, destination-change confirmation, metadata/checksums/dependency licenses and vulnerability checks | Actual macOS final-artifact run; complete platform storage refusal/locking matrix; project root license remains undeclared |
| Performance | 5-second validation/control latency test; 100 hot updates; 32/128/512/1000 concurrency and 1/8/16 MiB body harness | 24-hour mixed soak and separately measured core/browser/device/import-tool processes |
| CI | Native final-artifact jobs, real-backend UI, both browser engines, race/vet/security checks configured | Hosted execution for this revision, including macOS, has not been observed |
| Release journeys | Multiple concrete local API, UI, browser and vault journeys are exercised | Full ten-group release journey acceptance and original attachment mapping remain open |

## Reproducible evidence

The completed local acceptance run ended at **2026-09-13 10:38:48 UTC** with **11/11 checks passing**. Source snapshot SHA-256: `f5a3163fa45a1cddfb2e8b583000dc13f43c96598ea8fe625940bb50343db9df`. The full real-backend UI run includes the previously failing cancellation/tab-cleanup case. Linux/WSL race and native Secret Service final-binary lifecycle checks also passed after the last source change.

| Final local artifact | SHA-256 |
|---|---|
| `dist/clash-tokens.exe` (Windows amd64) | `75f22fff9dbaacc580307f3bb75daaf52a627c71a7a6e0bc58eea1906191e450` |
| `dist/linux/clash-tokens` (Linux amd64, WSL runtime) | `a2a930c4a04adff77c7316ef7ba05724f534e9cce1cce3f57e3c84e98aa25ac1` |

Run `python scripts/record_acceptance.py` from the repository root. It stops at the first failure and records commands, timestamps, exit codes, a source snapshot digest and individual logs under `.clash-tokens/release-evidence/acceptance.json`. Do not treat an earlier successful log as proof for later code changes. Browser sites and model upstreams in this run are synthetic; browser engines and the Go backend are real.

`scripts/artifact_vault_smoke.py` operates on the exact binary passed to it and exercises create, invocation, restart/read, rotation and deletion. `scripts/release_metadata.py` records the artifact hash, embedded module versions and license evidence. Native Linux evidence is retained separately under `.clash-tokens/release-evidence/`; macOS cannot be validated by cross-compiling on Windows.

The Linux/WSL performance run completed all seven synthetic scenarios without request errors. At concurrency 512, gateway throughput was approximately 6,859 requests/s with TTFT p99 65.558 ms; at 1,000, approximately 11,919 requests/s with p99 95.294 ms and combined peak RSS 190.45 MiB. These figures include the mock upstream, client and gateway in one process and are **not core-only or production measurements**. Windows 512/1,000 runs encountered connection-refused errors in both direct and gateway paths; those failed results are retained. No 24-hour soak has completed.

## 中文状态

本轮以 `8387d509` 为基线，正在实现[完整开发清单](NEXT_RELEASE_CHECKLIST.md)。当前改动仅在本地，尚未提交；**不能宣布全部完成或可以发布**。原讨论提及的 92 项任务、28 个案例与原 51 项映射附件未返回，不能自行编造对应关系。

| 范围 | 已实现并进行本地验证 | 仍需完成 |
|---|---|---|
| 统一凭证 | 真实 Adapter 使用引用；取消传递至 resolver；显式引用失效不回退环境变量 | 全部可执行 Adapter 的工厂与协议成功路径覆盖 |
| 导入与管理器 | 选择导入、预览超时清除、带版本的保留/替换/新建；限定字段的 op/bw 调用和输出边界 | 各厂商真实版本导出样本、管理器实际授权、外部账密成对登录 |
| 登录与续期 | ChatGPT 浏览器绑定、Claude Cookie 与租户复验、加密 OAuth grant、并发刷新协调、轮换和撤销记录 | 其他 Provider 登录到调用转换、CLI 导入后的自动续期、真实账号生命周期 |
| 浏览器与设备 | 实际 Chromium/Firefox 引擎访问本地合成站点；标签页归属与取消清理；Windows 自有进程树终止 | 全部品牌与系统组合、Unix 进程树与重启/崩溃校对、授权实体设备和账号 |
| 控制面 | 有界 Job、事件、取消、超时、资源协调、过期结果拦截；长操作不占配置提交锁 | 所有配置/vault 事务的中断和故障注入矩阵 |
| 前端 | 真实 Go Admin API 回归；导入冲突、任务取消、会话绑定、目的地确认、分页搜索、四协议测试台 | 完整页面操作矩阵、所有账号/健康状态字段实时刷新、完整首次启动流程 |
| 路由流式 | 保留原回归；100 次热更新保留在途计数，禁用后拒绝新请求 | 八类开关的完整 UI 矩阵及所有协议终止分支 |
| Provider 合约 | 浏览器相关 Provider 使用真实双引擎和本地合成网站验证 | 尚未建立全部可执行 Adapter 工厂/协议 100% 覆盖；真实上游单独验收 |
| 存储安全 | Windows/Linux 最终二进制生命周期脚本、canary、目的地变更确认、产物元数据/许可/漏洞检查 | macOS 真实产物运行、各平台拒绝授权/锁定矩阵；项目根许可证未声明 |
| 性能 | 慢验证控制面延迟、100 次热更新、四档并发和三档输入大小 | 24 小时混合稳态及核心/浏览器/设备/导入工具分别计量 |
| CI | 已配置原生平台产物、真实后端 UI、双引擎、race/vet/安全检查 | 当前版本托管 CI 运行，尤其 macOS，尚无证据 |
| 最终流程 | 已运行多条本地 API、UI、浏览器与 vault 路径 | 十组发布流程的完整验收与原附件逐项映射 |

本地证据入口为 `.clash-tokens/release-evidence/acceptance.json`，包含源码摘要、命令、时间、返回码与日志。`scripts/record_acceptance.py` 遇到失败立即停止；早期通过不能替代后续代码的验证。浏览器和 Go 后端为真实进程，上游和登录站点为合成夹具。

最新本地验收于 **2026-09-13 10:38:48 UTC** 完成，**11/11 项通过**，包括先前失败的 UI 取消与标签页清理。最后一次源码变更后的 Linux/WSL race 与原生 Secret Service 二进制生命周期测试也已通过。源码摘要与两个产物的完整 SHA-256 见上表；它们仍是本地未提交工作区产物。

Linux/WSL 七个性能场景均无请求错误；512 并发约 6,859 请求/秒、TTFT p99 65.558 ms，1,000 并发约 11,919 请求/秒、p99 95.294 ms、合并峰值 RSS 190.45 MiB。数据合并了同进程客户端、mock 上游和网关，不能称为纯核心资源或生产成绩。Windows 512/1,000 并发在直连和网关路径均出现连接拒绝，失败结果保留。尚未完成 24 小时稳态。
