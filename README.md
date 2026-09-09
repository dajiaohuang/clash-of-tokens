# Clash of Tokens

**把多个 AI 来源、多个账号接到同一个本地网关，供 Agent 和应用统一调用。**

Clash of Tokens 是原生 Go 实现的本地优先 AI 协议网关，将来源选择、账号并发、共享额度、排队和故障隔离集中处理，提供 OpenAI、Anthropic 和 Gemini 协议入口。

当前版本：**0.1.0-dev，实验阶段**。93 项参考来源均已注册可执行适配路径，其中 **2 项完成过真实上游验证**；其余主要依据源码审查和本地契约测试。注册数量不代表全部账号可用或全部 API 语义兼容。

[来源状态](docs/SOURCE_STATUS.md) · [多账号并发](docs/MULTI_ACCOUNT_CONCURRENCY.md) · [性能记录](docs/PERFORMANCE.md) · [真实验证](docs/LIVE_VALIDATION.md) · [产品方案](docs/PRODUCT_SPEC.md)

## 功能

- **统一入口**：OpenAI Chat Completions / Responses、Anthropic Messages、Gemini 原生请求路径。
- **多来源、多账号调度**：来源、共享额度域及全局并发限制，有界排队、取消和超时。
- **策略与能力过滤**：`auto`、`fallback`、`latency`、`select`，结合明确批准、模型评级、工具/图片能力、付费及本地策略选路。
- **异常隔离**：429 额度域冷却、401/403 来源阻断；不静默重复发送不确定的请求。
- **低开销转发**：连接复用、按需创建客户端、流缓冲池、JSON 定点改写和队列定向唤醒。
- **本地界面**：管理页 `/`、对话页 `/chat`；调用密钥与管理密钥分离。

同协议请求尽量保留原始内容。跨协议及网页适配只支持已经实现的语义，不支持的字段、工具、图片或历史明确拒绝。部分适配器缓冲完整回答后输出 SSE，并标记 buffered，不代表上游逐 token 流式。

## 快速开始

需要支持自动工具链选择的 Go 环境；项目固定使用 **Go 1.27.1**，见 [go.mod](go.mod)。浏览器来源另需 Chrome/Chromium。以下为 PowerShell 示例；Linux/macOS 可将输出名改为 `dist/clash-tokens`。

```powershell
git clone https://github.com/dajiaohuang/clash-of-tokens.git
cd clash-of-tokens
go build -trimpath -ldflags="-s -w" -o dist/clash-tokens.exe ./cmd/clash-tokens
```

### HTTP / 官方 API 来源

填写账号可用的真实模型 ID：

```powershell
./dist/clash-tokens.exe init -config config.json -provider openai -model YOUR_ACTUAL_MODEL -enable
$env:COT_OPENAI_KEY = "YOUR_UPSTREAM_API_KEY"
./dist/clash-tokens.exe validate
./dist/clash-tokens.exe serve
```

默认监听 `127.0.0.1:8317`。`init` 不覆盖已有文件；不传 `-enable` 时来源默认禁用。启用来源不会自动批准 Auto 选路。凭证环境变量以生成配置的 `key_env` 为准，其他来源的格式和限制见 [provider 文档](docs/providers)。

```powershell
./dist/clash-tokens.exe providers
# 需要项目 ID 的来源示例：
./dist/clash-tokens.exe init -config antigravity.local.json -provider antigravity -model YOUR_ACTUAL_MODEL -project YOUR_PROJECT
```

### ChatGPT 网页

未创建 `config.json` 时可使用附带示例；已有配置请另存并通过 `-config` 指定。

```powershell
Copy-Item config.example.json config.json
./dist/clash-tokens.exe validate
./dist/clash-tokens.exe browser-login
# 在专用浏览器窗口中亲自登录 ChatGPT
./dist/clash-tokens.exe doctor
./dist/clash-tokens.exe serve
```

此示例监听 `127.0.0.1:18317`，模型为 `chatgpt-web/web`。打开 [本机聊天页](http://127.0.0.1:18317/chat) 后输入调用密钥。当前仅支持文本，账号并发为 1；不支持 API 原生工具、图片、系统消息及采样参数。页面轮询产生的流输出与原生 SSE 不同。

## Agent 与 API 调用

`keys` 命令读取本机自动生成的网关密钥。也可用 `COT_API_KEY`、`COT_ADMIN_KEY` 提供两个不同的、至少 16 字符的密钥。上游账号密钥与网关调用密钥是不同凭证。

```powershell
$keys = ./dist/clash-tokens.exe keys | ConvertFrom-Json
$headers = @{ Authorization = "Bearer $($keys.api_key)" }
Invoke-RestMethod http://127.0.0.1:8317/v1/models -Headers $headers
$body = @{
  model = "openai/YOUR_ACTUAL_MODEL"
  messages = @(@{ role = "user"; content = "Hello" })
  stream = $false
} | ConvertTo-Json -Depth 8
Invoke-RestMethod http://127.0.0.1:8317/v1/chat/completions `
  -Method Post -Headers $headers -ContentType "application/json" -Body $body
```

在支持自定义 OpenAI 地址的 Agent 中配置：

| 配置项 | 值 |
|---|---|
| Base URL | `http://127.0.0.1:8317/v1`，端口以配置为准 |
| API key | 网关调用密钥 |
| Model | `/v1/models` 返回的 ID，例如 `openai/YOUR_ACTUAL_MODEL` |

| 协议 | 入口 |
|---|---|
| OpenAI Chat Completions | `POST /v1/chat/completions` |
| OpenAI Responses | `POST /v1/responses` |
| Anthropic Messages | `POST /v1/messages` |
| Gemini | `POST /v1beta/models/{model}:generateContent` 或 `:streamGenerateContent` |

入口存在不代表每个来源都支持该协议。工具调用型 Agent 应选择明确支持 native tools 的来源；网页文本适配器不能替代完整工具 API。

## 同一个 provider 挂多个账号

HTTP 来源的每个账号配置一个独立 source：相同 `provider` / `adapter`，不同 `id` 和凭证环境变量。将它们加入同一 group，或使用相同模型 ID 进行无状态选路。

| 字段 | 作用 |
|---|---|
| `source.max_inflight` | 单账号活动请求上限 |
| `quota_domain` / `quota_max_inflight` | 共享额度域的总并发；同账号别名或共享组织额度应使用同域 |
| `runtime.max_inflight` | 整个网关活动请求上限 |
| `runtime.max_queued` / `queue_timeout_ms` | 等待队列容量及最长等待时间 |

`auto` 综合负载与延迟，`fallback` 优先使用前面的来源，`select` 固定首个来源。来源必须明确批准并满足 group 策略才能参与组内调度。预设模型默认 unrated，评级需实际依据 `rating_basis`。

队列只唤醒可运行请求，繁忙账号不会挡住后面能使用其他账号的请求。增大队列不会增加上游容量。

**有状态对话绑定具体 source。** ChatGPT 续聊使用返回的 `X-COT-Session` 或 Responses 的 `previous_response_id`，不能随机切到其他账号。浏览器来源共用全局 browser 配置，复制 source 不会创建独立登录环境，尚未实现通用浏览器账号池。完整说明见 [多账号并发](docs/MULTI_ACCOUNT_CONCURRENCY.md)。

## 性能与验证

以下为 Windows / i9-14900KF / Go 1.27.1 的局部基准中位数，不包含真实模型推理，也不代表生产容量：

| 场景 | 优化前 | 优化后 |
|---|---:|---:|
| 8 账号、128 个排队请求，每批调度完成 | 6.92 ms | 0.56 ms |
| 1000 模型列表响应 | 522 µs | 167 µs |
| 100 条含 image 文本的消息检查 | 23.9 µs | 8.8 µs |
| 1000 行 SSE data 合并 | 2.91 ms | 15.2 µs |

本地测试覆盖 256 请求/8 账号的容量约束、取消、冷却、停用及队列排空；100 并发、30,000 请求的回环模拟测试零错误。真实浏览器长时稳定性、独立负载机容量及全部来源账号实测尚未完成。

```powershell
go test ./...
go vet ./...
go test -race ./...  # 需要平台 C 工具链
go test ./internal/routing ./internal/api ./internal/protocol -run '^$' -bench . -benchmem
go run ./cmd/cot-bench -concurrency 100 -requests 30000 -delay 1ms
```

常规测试不主动调用真实账号；浏览器契约测试需要 Chrome，缺少环境时相关测试可能跳过。`cot-bench` 仅使用自建回环模拟服务。真实 smoke test 必须显式使用 `--live` 或对应环境开关，会发送实际请求。

[完整性能记录](docs/PERFORMANCE.md) · [全量审计](docs/PERFORMANCE_AUDIT.md) · [场景优化](docs/SCENARIO_PERFORMANCE.md)

## 配置与目录

本机 `config.json`、`*.local.json`、`.env*`、`.clash-tokens/` 和构建产物均不提交。可公开的起点是 `config.example.json`。自动网关密钥在 Windows 使用 DPAPI，Unix 文件权限为 0600。不要分享 `keys` 输出、浏览器 profile、会话文件或 HAR。

```text
cmd/                 网关、模拟压测与浏览器诊断
internal/api/        HTTP 入口与本地界面
internal/routing/    策略、并发、额度与队列
internal/protocol/   JSON 检查与 SSE 处理
internal/providers/ 来源适配器
internal/chatgptweb/ ChatGPT 页面驱动与会话
catalog/             来源预设及范围清单
docs/                来源约束、参考出处和验证记录
```

协议参考与固定版本记录在 `docs/providers/`。随附第三方许可见 [docs/licenses](docs/licenses) 和 [TinyCMS signer notice](internal/providers/majorweb/tinycms-LICENSE.txt)。产品方案包含未实现的设计目标，以实现和验证记录为准。

## English overview

Clash of Tokens is a local-first Go gateway for multiple AI providers and accounts. It offers OpenAI, Anthropic and Gemini request endpoints, bounded queues, account/shared-quota concurrency limits, routing policies and a local dashboard.

This is an experimental `0.1.0-dev` project. The 93 registered adapter paths are not 93 live-verified services: only two have recorded real upstream validation. Protocol capabilities vary by adapter. HTTP accounts can be configured as separate sources; a general isolated browser account pool is not implemented. See the linked provider documentation and performance reports for exact scope and reproducible evidence.
