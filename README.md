# Clash of Tokens

Clash of Tokens is a local-first Go gateway that presents web, app, IDE,
subscription, official API, aggregator, and local-model sources through one
model-service interface. It manages providers, sources, accounts, protected
credential references, shared quota, bounded concurrency, routing, sessions,
and a local control plane.

The project boundary is deliberately narrow: it simulates a model provider and
connects model APIs. It is not an agent runtime, a business-tool automation
platform, or a credential-collection service. A product appears in the
catalog only as a source candidate; it becomes usable after an explicit
configuration, capability check, and (where available) user-authorized
verification.

## Status and evidence

This is an experimental 0.1.0-dev build. The evidence boundary is part of the
product contract:

| Layer | Current evidence |
| --- | --- |
| Original strict scope | 93/93 source paths are registered; 2/93 have a real upstream check |
| Catalog | 144 inert entries: 120 native HTTP, 10 native browser, 3 native CLI, 3 native device, 8 research-only |
| Live catalog flags | chatgpt-web and cloudflare-playground only |
| Local contract tests | 648 tests in 39 packages pass on Windows |
| Hosted verification | [CI run 34549312886](https://github.com/dajiaohuang/clash-of-tokens/actions/runs/34549312886) passes cross-platform tests, Linux race, native macOS Keychain, and Linux/Windows/macOS gateway builds |

“Registered” means that a descriptor, factory, or recipe path exists. It does
not mean that an account is logged in, that a provider still accepts the
protocol, or that every advertised model capability works. See the
[source-status ledger](docs/SOURCE_STATUS.md), the [live-validation record](docs/LIVE_VALIDATION.md),
and the [51-section control-plane audit](docs/CONTROL_PLANE_AUDIT.md).

## What works today

- OpenAI Chat Completions and Responses, Anthropic Messages, and Gemini
  request paths share one authenticated gateway.
- Provider, source, account, model, group, quota-domain, and credential
  references are validated through a persistent configuration service with
  optimistic revisions, preview/apply, history, and rollback.
- Accounts can share one protected credential across multiple sources. The
  control plane never returns the secret value.
- Routing supports auto, ordered fallback, latency, load-balance, and select,
  with provider/account/source/model enable gates, group membership, account
  pools, shared quota, bounded queues, cancellation, and explainable
  exclusion reasons.
- Request-level fallback rewrites the original request for each attempt and
  advances only after a proven pre-submission or native 401/403/429 rejection.
  Ambiguous failures, partial streams, stateful requests, and client-visible
  output are never silently replayed.
- Streaming completion is protocol-aware. An HTTP 200 plus EOF is not enough:
  the gateway requires the protocol terminal ([DONE], message_stop,
  response.completed, finish_reason, or an equivalent adapter event).
- The embedded control plane includes Overview, Providers and Provider Detail,
  Accounts, Credentials, Sources and Source Detail, Models, Groups, Routing
  and the simulator, Health, Sessions, Browsers, Devices, Metrics, Activity,
  Config, Implementation, and global search.
- Browser flows use explicit existing or isolated profiles, bounded login
  checks, owned-process controls, and metadata-only account discovery. The
  device doctor is read-only and never starts an emulator or sends a message.
- Three FreeCoding-derived Android drivers (Meituan Xiaotuan, Wangzhe Lingbao,
  and Douyin Xiaohuoren) are included as disabled, manual-only Go device
  drivers. They have offline contract coverage, not real-device verification.

## Data model and policy semantics

The runtime keeps the following relationship explicit:

~~~text
Provider
  └─ Source          (a concrete adapter/model/endpoint)
      └─ Account     (an authorized identity)
          └─ CredentialRef (a protected value reference)
~~~

Source metadata separates the dimensions that are often confused:

| Field | Meaning |
| --- | --- |
| source_kind | vendor_api, cloud_api, aggregator_api, product_reverse, browser_reverse, app_reverse, cli_reverse, local_model, or custom_api |
| execution_location | Where the adapter process runs |
| inference_location | Where model inference occurs; local_only requires explicit local inference |
| billing_mode | metered, subscription, free_allowance, local, or unknown |
| credential_mode | API key, OAuth, browser session, cookie, username/password, CLI session, device session, or anonymous |

Provider, account, source, model, and Auto approval are independent switches.
Disabling a parent removes its children from routing; disabling Auto approval
leaves explicit calls possible. Unknown cost is not treated as free. A local
process talking to a cloud model is not a local-inference source.

## Credentials, import, and login

Configuration stores cred:// references. Secret values live in a separate
protected vault:

- Windows uses current-user DPAPI.
- Linux uses an AES-GCM vault whose key is stored in Secret Service.
- macOS uses an AES-GCM vault whose key is stored in the native Keychain in a
  cgo-enabled build.
- Unsupported or unavailable native protection fails closed; there is no
  plaintext file fallback.

The control plane supports selected CSV/JSON exports from the named password
managers, configured environment imports, selected OAuth/CLI token imports,
browser profile metadata discovery, and selected CDP cookie import. Import is
explicit and bounded: the user selects records, the preview is redacted, and
only a new protected reference is persisted. Username/password material is
stored as a bounded structured value. See [credential imports](docs/CREDENTIAL_IMPORTS.md),
[credential binding](docs/CREDENTIAL_BINDING.md), and [storage](docs/CREDENTIAL_STORAGE.md).

Browser login launches an existing or isolated profile and waits for a bounded,
provider-specific authentication check. It does not fill 2FA, export a full
browser database, or claim that launching a browser means authentication
succeeded. See [browser login](docs/BROWSER_LOGIN.md), [browser processes](docs/BROWSER_PROCESSES.md),
and [account discovery](docs/ACCOUNT_DISCOVERY.md).

## Routing and runtime behavior

Groups can express minimum tier, source-kind and billing filters, tool/vision
requirements, rate ceilings, ordered preferences, and ordered source members.
Quota domains make shared entitlement visible across accounts and models.
Account pools support round-robin, weighted, least-load, and sticky dispatch.
The simulator evaluates eligibility without acquiring a lease or contacting an
upstream provider.

Stateful conversations are pinned to their explicit gateway session or
previous_response_id; they do not move to another account during a turn.
Session controls expose metadata, expiry, and clear operations only where an
adapter has a supported local session manager. Conversation text and
credentials are not displayed by default.

Adapters may buffer a complete upstream answer before producing SSE. Such a
response is labelled buffered and does not claim token-by-token upstream
streaming. Unsupported tools, images, system messages, sampling parameters,
or cross-protocol fields are rejected explicitly.

## API surface

| Protocol | Endpoint |
| --- | --- |
| OpenAI Chat Completions | POST /v1/chat/completions |
| OpenAI Responses | POST /v1/responses |
| Anthropic Messages | POST /v1/messages |
| Gemini native | POST /v1beta/models/{model}:generateContent or :streamGenerateContent |
| Models | GET /v1/models |
| Control plane | / with authenticated /admin/* actions |

An endpoint existing does not imply that every configured source supports that
protocol. Choose a source whose descriptor declares the needed capability.

## Quick start

Go 1.27.1 is selected by the repository toolchain. Build the gateway:

~~~powershell
git clone https://github.com/dajiaohuang/clash-of-tokens.git
cd clash-of-tokens
go build -trimpath -ldflags="-s -w" -o dist/clash-tokens.exe ./cmd/clash-tokens
~~~

Create a disabled-by-default HTTP source and validate it with the account's
real upstream model ID:

~~~powershell
./dist/clash-tokens.exe init -config config.json -provider openai -model YOUR_ACTUAL_MODEL
$env:COT_OPENAI_KEY = "YOUR_UPSTREAM_API_KEY"
./dist/clash-tokens.exe validate -config config.json
./dist/clash-tokens.exe serve -config config.json
~~~

Add -enable to init only after reviewing the source. Enabling a source does
not approve it for Auto routing. The generated key_env is authoritative; do
not put an upstream key in the gateway configuration.

For a browser source, start from the supplied example:

~~~powershell
Copy-Item config.example.json config.json
./dist/clash-tokens.exe validate -config config.json
./dist/clash-tokens.exe browser-login -config config.json
# Sign in yourself in the dedicated browser window.
./dist/clash-tokens.exe doctor -config config.json
./dist/clash-tokens.exe serve -config config.json
~~~

The example listens on 127.0.0.1:18317 and exposes the local chat page at
/chat. ChatGPT Web currently supports text-only browser conversations with
one-account concurrency and no native tools, images, system messages, or
sampling parameters.

## Calling the gateway

keys creates or reads separate gateway API and admin keys. You may supply
COT_API_KEY and COT_ADMIN_KEY instead; both must be at least 16 characters.

~~~powershell
$keys = ./dist/clash-tokens.exe keys | ConvertFrom-Json
$headers = @{ Authorization = "Bearer $($keys.api_key)" }
Invoke-RestMethod http://127.0.0.1:8317/v1/models -Headers $headers
~~~

For an OpenAI-compatible client use http://127.0.0.1:8317/v1 (or the port
from your configuration), the gateway API key, and a model ID returned by
/v1/models. Gateway keys and upstream account credentials are different
secrets.

## Verification, performance, and limits

Normal tests use local fixtures and synthetic loopback services; they do not
silently contact provider accounts. The repository runs:

~~~text
go test ./...
go vet ./...
go test -race ./...                  # requires a platform C toolchain
go run ./cmd/cot-bench -concurrency 100 -requests 30000 -delay 1ms
~~~

The recorded Windows microbenchmarks measure dispatch, model-list assembly,
message inspection, and SSE parsing. They exclude model inference, independent
load machines, long-lived browser memory, and provider rate limits. See
[performance](docs/PERFORMANCE.md), [scenario measurements](docs/SCENARIO_PERFORMANCE.md),
and [workload metrics](docs/WORKLOAD_METRICS.md).

Real upstream evidence is currently limited to the signed-in ChatGPT Web and
Cloudflare Playground checks documented in [Live validation](docs/LIVE_VALIDATION.md).
Live checks are opt-in and require the operator's own authorization. They do
not certify model identity, entitlement, tool/image support, general
availability, or parallel capacity.

## Source catalog, provenance, and licenses

The catalog is inert metadata: listing it does not construct a client, read a
browser, log in, or send a request. Each entry keeps its adapter kind,
protocols, reference, notes, implementation label, and live-verification flag.
Research-only entries cannot be generated by init. The original 93-source
scope and its fixed references are preserved under catalog/ and
docs/reference/; Chinese App candidates have their own [bounded status table](docs/CHINA_APP_CANDIDATES.md).

Protocol claims are tied to pinned references and local replay fixtures. A
community project named 2api is a research lead, not proof that an upstream
account, endpoint, license, or current protocol is usable. Review the
provider-specific notes before enabling a source. Third-party notices are in
[docs/licenses](docs/licenses) and the [TinyCMS notice](internal/providers/majorweb/tinycms-LICENSE.txt).

Further operational references include [multi-account concurrency](docs/MULTI_ACCOUNT_CONCURRENCY.md),
[routing policies](docs/ROUTING_POLICIES.md), [model discovery](docs/MODEL_DISCOVERY.md),
[provider detail](docs/PROVIDER_DETAIL.md), [adapter workbench](docs/ADAPTER_WORKBENCH.md),
[CI](docs/CI.md), and the [product specification](docs/PRODUCT_SPEC.md).

## Repository layout

~~~text
cmd/                 gateway, verification, device doctor, and benchmarks
internal/api/        HTTP API and embedded control plane
internal/config/     schema, accounts, metadata, pricing, and transactions
internal/credentials protected vault and importers
internal/providerdef descriptor registry and capability schemas
internal/routing/    strategies, pools, quotas, queues, and explanations
internal/protocol/   request validation, SSE, execution completion, redaction
internal/upstream/   official/cloud clients, sessions, discovery, and catalogs
internal/drivers/    browser/device transport helpers
catalog/             inert source metadata, candidates, and evidence
docs/                provider constraints, provenance, tests, and operations
scripts/              UI regression, smoke tests, audits, and keyring fixtures
~~~

## Roadmap and acceptance boundary

The latest 51-item synthesis has been rechecked against the current code,
tests, control-plane pages, and evidence documents. The detailed ledger marks
each item as implemented with a boundary, partial, or environment-dependent;
it does not turn a local contract test into a live provider claim. Remaining
work includes provider-specific setup and compatibility, all-account live
verification, installed-browser lifecycle coverage, physical-device evidence,
broader end-to-end import/login journeys, signed release artifacts, and
distribution publication. Track changes in the [control-plane audit](docs/CONTROL_PLANE_AUDIT.md)
and the original [requirements](docs/CONTROL_PLANE_REQUIREMENTS.md).

Do not commit config.json, *.local.json, .env*, .clash-tokens/, browser
profiles, session files, cookies, tokens, passwords, or HAR captures.

---

# 中文说明

Clash of Tokens 是一个本机优先的 Go 网关，把网页、App、IDE、订阅、
正式 API、聚合器和本地模型统一成模型服务接口。它管理 Provider、Source、
Account、受保护的凭证引用、共享额度、并发、路由、会话和本地控制面。

项目边界很明确：它负责模拟模型 Provider、接入模型 API，不是 Agent
运行平台、业务工具自动化平台，也不是凭证收集器。目录中的产品只是候选
来源；只有在明确配置、能力检查以及（可行时）用户授权验证之后，来源才可用。

## 当前状态与证据

当前是实验性的 0.1.0-dev 版本，证据边界也是产品契约的一部分：

| 层次 | 当前证据 |
| --- | --- |
| 原始严格范围 | 93/93 个来源路径已注册；2/93 完成真实上游检查 |
| 来源目录 | 144 个惰性目录项：120 个原生 HTTP、10 个原生浏览器、3 个原生 CLI、3 个原生设备、8 个研究项 |
| 目录实时标记 | 只有 chatgpt-web 和 cloudflare-playground |
| 本地契约测试 | Windows 上 39 个包共 648 个测试通过 |
| 托管验证 | [CI 34549312886](https://github.com/dajiaohuang/clash-of-tokens/actions/runs/34549312886) 的跨平台测试、Linux race、macOS 原生 Keychain 及三平台网关构建均通过 |

“已注册”只表示存在 descriptor、factory 或 recipe 路径，不表示账号已
登录、上游仍接受该协议，或所有模型能力都可用。详见
[来源状态](docs/SOURCE_STATUS.md)、[真实验证记录](docs/LIVE_VALIDATION.md)
和[控制面 51 项审计](docs/CONTROL_PLANE_AUDIT.md)。

## 当前可用能力

- 统一提供 OpenAI Chat Completions/Responses、Anthropic Messages 和
  Gemini 请求入口。
- Provider、Source、Account、Model、Group、Quota Domain 和凭证引用通过
  持久化配置服务管理，支持乐观版本、Preview/Apply、历史和回滚。
- 多个 Source 可以共享同一个受保护凭证；控制面不会返回 secret。
- 路由支持 auto、有序 fallback、latency、load-balance 和 select，并提供
  Provider/Account/Source/Model 开关、组成员、账号池、共享额度、有界队列、
  取消和排除原因。
- 请求级 fallback 会为每次尝试重写原始请求，只在已确认的提交前失败或
  原生 401/403/429 拒绝后推进；不确定失败、半截流、状态请求和已有客户
  输出不会被静默重放。
- 流式完成按协议判断。HTTP 200 加 EOF 不足以证明成功，必须识别
  [DONE]、message_stop、response.completed、finish_reason 或适配器等价事件。
- 内置控制面包含 Overview、Providers/Provider Detail、Accounts、
  Credentials、Sources/Source Detail、Models、Groups、Routing/Simulator、
  Health、Sessions、Browsers、Devices、Metrics、Activity、Config、
  Implementation 和全局搜索。
- 浏览器流程使用明确选择的现有或隔离 Profile，登录检查有界，进程可追踪
  和停止；设备 doctor 只读，不启动模拟器，也不发送消息。
- FreeCoding 参考的美团小团、王者灵宝和抖音小火人已作为禁用、手动专用的
  Go 设备驱动加入。三者有离线契约覆盖，尚未完成真机验证。

## 数据模型与策略语义

运行时保持下面的关系：

~~~text
Provider
  └─ Source          （具体适配器/模型/入口）
      └─ Account     （授权身份）
          └─ CredentialRef（受保护值的引用）
~~~

Source 元数据拆开了容易混淆的维度：

| 字段 | 含义 |
| --- | --- |
| source_kind | vendor_api、cloud_api、aggregator_api、product_reverse、browser_reverse、app_reverse、cli_reverse、local_model 或 custom_api |
| execution_location | 适配器进程运行位置 |
| inference_location | 推理发生位置；local_only 必须有明确的本地推理 |
| billing_mode | metered、subscription、free_allowance、local 或 unknown |
| credential_mode | API key、OAuth、浏览器会话、Cookie、账密、CLI 会话、设备会话或匿名 |

Provider、Account、Source、Model 和 Auto 批准是独立开关。关闭父级会让
子项退出路由；关闭 Auto 批准仍可显式调用。未知费用不会按免费处理。
本地运行的云端客户端也不算本地推理来源。

## 凭证、导入与登录

配置只保存 cred:// 引用，secret 保存在独立受保护的 vault：

- Windows 使用当前用户 DPAPI。
- Linux 使用 AES-GCM vault，密钥放在 Secret Service。
- macOS 使用 AES-GCM vault，密钥放在原生 Keychain，需 cgo 构建。
- 原生保护不可用时拒绝读写，不使用明文文件 fallback。

控制面支持指定密码管理器的 CSV/JSON 导出、环境变量导入、部分
OAuth/CLI token 导入、浏览器 Profile 元数据发现和选定 CDP Cookie 导入。
导入必须由用户明确选择，预览会脱敏，只保存新的受保护引用。账密以有界
结构值保存。详见[凭证导入](docs/CREDENTIAL_IMPORTS.md)、
[凭证绑定](docs/CREDENTIAL_BINDING.md)和[存储](docs/CREDENTIAL_STORAGE.md)。

浏览器登录会启动现有或隔离 Profile，并等待有界的 Provider 登录检查。
它不会自动填写 2FA，不会导出整套浏览器数据库，也不会把“已启动浏览器”
当成“已认证”。详见[浏览器登录](docs/BROWSER_LOGIN.md)、
[浏览器进程](docs/BROWSER_PROCESSES.md)和[账号发现](docs/ACCOUNT_DISCOVERY.md)。

## 路由与运行时行为

Group 可以设置最低等级、来源类型和计费过滤、工具/图片要求、速率上限、
有序偏好和有序 Source 成员。Quota Domain 将多个账号和模型共享的权益
显式展示。Account Pool 支持轮询、加权、最少负载和 sticky。Simulator
只计算资格，不获取 lease，也不联系上游。

有状态对话固定在明确的网关会话或 previous_response_id 上，一轮对话
不会随机切到其他账号。只有具备本地会话管理器的适配器才提供元数据、过期
和清理操作；默认不显示对话全文和凭证。

部分适配器会先缓冲完整上游回答再生成 SSE，这类响应会标记 buffered，
不宣称上游逐 token 流式。工具、图片、系统消息、采样参数或跨协议不支持
的字段会明确拒绝。

## API 入口

| 协议 | 入口 |
| --- | --- |
| OpenAI Chat Completions | POST /v1/chat/completions |
| OpenAI Responses | POST /v1/responses |
| Anthropic Messages | POST /v1/messages |
| Gemini 原生 | POST /v1beta/models/{model}:generateContent 或 :streamGenerateContent |
| 模型列表 | GET /v1/models |
| 控制面 | / 及需认证的 /admin/* |

入口存在不代表每个配置来源都支持该协议，应按 descriptor 声明选择能力。

## 快速开始

仓库 toolchain 选择 Go 1.27.1。构建网关：

~~~powershell
git clone https://github.com/dajiaohuang/clash-of-tokens.git
cd clash-of-tokens
go build -trimpath -ldflags="-s -w" -o dist/clash-tokens.exe ./cmd/clash-tokens
~~~

创建默认禁用的 HTTP 来源，并使用账号真实的上游模型 ID 验证：

~~~powershell
./dist/clash-tokens.exe init -config config.json -provider openai -model YOUR_ACTUAL_MODEL
$env:COT_OPENAI_KEY = "YOUR_UPSTREAM_API_KEY"
./dist/clash-tokens.exe validate -config config.json
./dist/clash-tokens.exe serve -config config.json
~~~

只有审查来源后才在 init 加 -enable。启用不等于批准 Auto。生成配置中的
key_env 是唯一依据，不要把上游 key 写入网关配置。

浏览器来源可以从示例开始：

~~~powershell
Copy-Item config.example.json config.json
./dist/clash-tokens.exe validate -config config.json
./dist/clash-tokens.exe browser-login -config config.json
# 在专用浏览器窗口中亲自登录
./dist/clash-tokens.exe doctor -config config.json
./dist/clash-tokens.exe serve -config config.json
~~~

示例监听 127.0.0.1:18317，聊天页为 /chat。ChatGPT Web 当前只支持文本
浏览器对话、单账号并发，不支持原生工具、图片、系统消息和采样参数。

## 调用网关

keys 会创建或读取分开的网关 API key 与 admin key。也可以设置 COT_API_KEY
和 COT_ADMIN_KEY，两者都至少 16 个字符。

~~~powershell
$keys = ./dist/clash-tokens.exe keys | ConvertFrom-Json
$headers = @{ Authorization = "Bearer $($keys.api_key)" }
Invoke-RestMethod http://127.0.0.1:8317/v1/models -Headers $headers
~~~

支持 OpenAI 自定义地址的客户端使用 http://127.0.0.1:8317/v1（或配置中的
端口）、网关 API key 以及 /v1/models 返回的模型 ID。网关 key 与上游账号
凭证是两类不同 secret。

## 验证、性能与限制

常规测试使用本地 fixture 和自建回环服务，不会静默调用真实账号。仓库运行：

~~~text
go test ./...
go vet ./...
go test -race ./...                  # 需要平台 C 工具链
go run ./cmd/cot-bench -concurrency 100 -requests 30000 -delay 1ms
~~~

已有 Windows 微基准测量调度、模型列表、消息检查和 SSE 解析，不包含真实
推理、独立负载机、长时间浏览器内存或 Provider 限流。详见[性能](docs/PERFORMANCE.md)、
[场景测量](docs/SCENARIO_PERFORMANCE.md)和[运行指标](docs/WORKLOAD_METRICS.md)。

当前真实上游证据只有[真实验证记录](docs/LIVE_VALIDATION.md)中的 ChatGPT
Web 和 Cloudflare Playground。真实验证必须显式开启并使用操作者自己的
授权，不证明模型身份、权益、工具/图片支持、普遍可用性或并行容量。

## 来源目录、溯源与许可

目录是惰性元数据：列出条目不会创建客户端、读取浏览器、登录或发送请求。
每项记录适配器类型、协议、参考、备注、实现标签和实时验证标记。研究项
不会由 init 生成可运行配置。原始 93 项及固定参考保存在 catalog/ 和
docs/reference/；中国 App 候选有独立的[状态表](docs/CHINA_APP_CANDIDATES.md)。

协议主张必须对应固定版本参考和本地回放 fixture。社区项目名称中有 2api
不等于账号、端点、许可证或当前协议已经可用。启用前请阅读来源专属说明。
第三方声明见 [docs/licenses](docs/licenses) 和
[TinyCMS notice](internal/providers/majorweb/tinycms-LICENSE.txt)。

更多运维资料：[多账号并发](docs/MULTI_ACCOUNT_CONCURRENCY.md)、
[路由策略](docs/ROUTING_POLICIES.md)、[模型发现](docs/MODEL_DISCOVERY.md)、
[Provider 详情](docs/PROVIDER_DETAIL.md)、[适配器工作台](docs/ADAPTER_WORKBENCH.md)、
[CI](docs/CI.md)和[产品方案](docs/PRODUCT_SPEC.md)。

## 仓库结构

~~~text
cmd/                 网关、验证、设备 doctor、基准
internal/api/        HTTP API 与嵌入式控制面
internal/config/     schema、账号、元数据、价格与事务
internal/credentials 受保护 vault 与导入器
internal/providerdef descriptor 注册表与能力 schema
internal/routing/    策略、账号池、额度、队列与解释
internal/protocol/   请求检查、SSE、完成判定与脱敏
internal/upstream/   正式/云端客户端、会话、发现与目录
internal/drivers/    浏览器/设备传输辅助
catalog/             惰性来源元数据、候选和证据
docs/                来源约束、溯源、测试与运维记录
scripts/              UI 回归、smoke、审计和 keyring fixture
~~~

## 路线图与验收边界

最新 51 项综合清单已经按当前代码、测试、控制面页面和证据文档重新核验。
详细台账把每项标成“已实现但有边界”“部分实现”或“依赖外部环境”；本地
契约测试不会被写成真实 Provider 结论。剩余工作包括 Provider 专属登录与
兼容性、全部账号真实验证、已安装浏览器生命周期、真机证据、更完整的导入/
登录端到端流程、签名发布物和分发上线。请在[控制面审计](docs/CONTROL_PLANE_AUDIT.md)
和[原始需求](docs/CONTROL_PLANE_REQUIREMENTS.md)中跟踪。

不要提交 config.json、*.local.json、.env*、.clash-tokens/、浏览器 Profile、
会话文件、Cookie、token、密码或 HAR。
