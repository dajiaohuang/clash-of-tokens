# Control plane implementation requirements

Source: user-authorized latest synthesis in conversation 6aa03b52-6774-83ec-bf47-f79c45bae4e4 (2026-09-10). This is the acceptance scope, not a claim of implementation.

可以。你现在下一轮其实已经不是“继续加几个 Provider”，而是要把 Clash of Tokens 从“广覆盖实验网关”推进成一个**完整可操作的本地 Provider 管理平台**。

我建议把下一轮目标收敛成一句话：

> **任何来源都能从前端完成发现、加账号、登录、启停、分组、调度、验证、观察状态；任何配置都不需要手改 JSON。**

下面是综合版清单，已经把前面的审计问题、新方向、正式 API、账密导入、浏览器登录、前端控制面全部合在一起。

---

## 一、P0：先修前面审计里影响正确性的核心问题

这些应该优先于 UI 美化。

### 1. 重做 Source 元数据模型

你现在的 `Local`、`Paid` 太粗。

建议拆成：

```text
source_kind
  vendor_api
  cloud_api
  aggregator_api
  product_reverse
  browser_reverse
  app_reverse
  cli_reverse
  local_model
  custom_api

execution_location
  local
  remote

inference_location
  local
  remote
  unknown

billing_mode
  metered
  subscription
  free_allowance
  local
  unknown

credential_mode
  api_key
  oauth
  browser_session
  cookie
  username_password
  cli_session
  device_session
  anonymous
```

这样正式 API、OpenRouter、网页反代、App 内助手、CLI 订阅、本地模型才不会混成一个布尔逻辑。

---

### 2. 修 `local_only`

以后：

```text
local_only
```

应该表示：

> 请求数据不会离开本机推理环境。

不能再表示：

> 这个进程是在本机运行。

例如 Devin CLI、本地启动的某个云端客户端，不应自动算 local inference。

---

### 3. 修 `paid`

也不要继续：

```text
Paid = !Local
```

应该按真实计费模式判断：

```text
metered
subscription
free
unknown
```

Auto 可以分别控制：

```text
allow_metered
allow_subscription
allow_unknown_cost
```

---

### 4. 增加真正的请求级 fallback

当前应该升级成：

```text
attempt 1
↓
失败分类
↓
是否允许重放？
↓
重新选 Source
↓
attempt 2
```

必须满足：

```text
未产生客户端可见输出
未发生不可逆提交
未进入状态未知
仍有 deadline
仍有 retry budget
```

不能出现：

```text
模型 A 输出半截
→ 模型 B 接着输出
```

---

### 5. 增加流式完成判定

不要只看：

```text
HTTP 200 + EOF
```

应该统一形成：

```go
type ExecutionResult struct {
    TransportOK      bool
    ProtocolComplete bool
    UpstreamError    string
    ClientCanceled   bool
}
```

对于 SSE：

```text
200
≠
模型正常结束
```

要识别：

```text
[DONE]
message_stop
response.completed
finish_reason
error event
```

---

## 二、P0：Credential / Account 系统彻底独立

这是下一轮最重要的新方向之一。

你的 Provider 数量已经很多，没有一个统一账号系统，后面会越来越难用。

最终应该变成：

```text
Provider
  ↓
Source
  ↓
Account
  ↓
Credential
```

而不是：

```text
Source
  → KeyEnv
```

---

## 三、统一账号模型

建议：

```go
type Account struct {
    ID
    ProviderID
    DisplayName

    Enabled
    AutoApproved

    CredentialRef

    Health
    AuthStatus

    QuotaDomain
    MaxInflight

    CreatedAt
    LastValidatedAt
}
```

一份 Provider 可以有很多账号：

```text
ChatGPT Web
├── personal
├── backup
└── work

Claude
├── claude-pro-1
└── claude-pro-2

OpenRouter
├── account-a
└── account-b
```

---

## 四、Credential Store

真正 secret 不再放：

```text
config.json
.env
providers.json
sessions.json
```

配置里只保存：

```text
credential_ref
```

例如：

```yaml
credential_ref: cred://chatgpt/personal
```

真正值放：

```text
Windows Credential Manager / DPAPI
macOS Keychain
Linux Secret Service
```

跨平台 fallback：

```text
encrypted vault
+
OS protected master key
```

---

## 五、所有来源都允许“任意添加账号”

这是你前端最重要的操作之一。

每个 Provider 页面应该都有：

```text
+ Add Account
```

然后根据 Provider 的 credential schema 自动显示不同 UI。

例如 OpenAI：

```text
Account name
API key
Base URL
Organization
Project
```

ChatGPT Web：

```text
Account name

[Login with Browser]
[Import Browser Session]
[Import Cookie]
```

Claude Web：

```text
[Launch Login Browser]
[Import Chrome session]
[Import Edge session]
[Manual cookie]
```

Kiro：

```text
[Import existing CLI session]
[Login]
```

自定义 API：

```text
Base URL
API key
Protocol
Model discovery
```

---

## 六、浏览器账号导入

做一个统一入口：

```text
Credentials
→ Import
→ Browser
```

支持优先级：

```text
Chrome
Edge
Brave
Firefox
Opera
Vivaldi
Chromium
Arc
```

扫描时不要复制整个浏览器数据。

只列候选：

```text
Chrome / Profile 1
  chatgpt.com    logged in
  claude.ai      logged in
  poe.com        logged in
```

用户明确勾选。

---

## 七、密码管理器导入

同一个 Import 页面：

```text
1Password
Bitwarden
KeePassXC
Proton Pass
Dashlane
NordPass
Apple Passwords
Google Password Manager
CSV / JSON
```

优先支持：

```text
官方 CLI
官方 export
用户主动选择文件
```

不建议第一版直接去逆向密码管理器 vault。

---

## 八、自动 Provider 匹配

每个 Provider catalog entry 增加：

```json
"credentials": {
  "domains": [
    "chatgpt.com",
    "openai.com"
  ],
  "accepted": [
    "browser_session",
    "cookie"
  ]
}
```

然后：

```text
Import Chrome
↓
发现 chatgpt.com session
↓
自动推荐：
Bind to ChatGPT Web
```

而不是用户手动理解各种 Cookie。

---

## 九、支持“拉起浏览器登录”

这个功能非常值得做。

前端：

```text
ChatGPT Web
Account: personal

Auth: Not logged in

[Login]
```

点击后：

```text
启动隔离 Chrome profile
↓
打开 chatgpt.com
↓
用户自己完成登录
↓
Clash of Tokens 检测登录成功
↓
绑定 browser profile
↓
Validate
```

不要自动填写 2FA 或验证码。

只负责：

```text
启动浏览器
等待用户正常登录
检测成功
保存会话引用
```

---

## 十、浏览器 Profile Registry

以后不要只有一个：

```text
Browser.CDPURL
```

建议：

```text
Browser Profiles
├── chrome-personal
├── chrome-work
├── edge-main
└── cot-isolated-claude
```

每个 Account 可以绑定不同 Profile。

例如：

```text
ChatGPT / personal
→ chrome-personal

Claude / work
→ chrome-work

Gemini / backup
→ edge-main
```

---

# 十一、前端要从“Dashboard”升级成完整 Control Plane

你要的前端最终应该做到：

> 所有配置都能看，所有配置都能改。

不再要求用户打开 JSON。

我建议前端至少分成 9 个主页面。

---

## 1. Overview

首页显示：

```text
Providers        126
Enabled          34
Healthy          28
Degraded         3
Auth Required    3

Accounts         47
Active           39

Auto Sources     25
Inflight         18
Queued           4

Requests / min
Success rate
TTFT
Current memory
Browser processes
Device sessions
```

再加：

```text
Latest failures
Accounts needing login
Quota exhausted
Sources in cooldown
```

---

## 2. Providers

完整 Provider 列表：

```text
Provider
Type
Protocol
Implementation
Accounts
Models
Enabled
Health
Auto
Verification
```

筛选：

```text
Official API
Aggregator
Web reverse
App reverse
CLI
Browser
Local
```

每行都能：

```text
Enable
Disable
Open
Test
```

---

## 3. Provider Detail

点击 ChatGPT Web：

```text
ChatGPT Web

Type:
Product reverse

Adapter:
chatgpt-web

Protocols:
Chat

Implementation:
native Go + browser

Provider enabled:
ON

Auto allowed:
ON
```

下面显示所有账号：

```text
Accounts

personal
  Enabled: yes
  Login: valid
  Profile: Chrome/Profile 1
  Health: healthy
  Inflight: 0/1

backup
  Enabled: no
```

按钮：

```text
Add Account
Login
Refresh Session
Validate
Delete
```

---

## 4. Accounts

全局账号管理页面。

表格：

```text
Account
Provider
Credential type
Enabled
Auth
Health
Quota domain
Inflight
Last verified
```

操作：

```text
Enable
Disable
Re-authenticate
Change credential
Move quota domain
Delete
```

---

## 5. Credentials

Credential Vault 前端。

不要显示 secret 本身。

显示：

```text
Credential ID
Type
Used by
Created
Last used
Status
Source
```

例如：

```text
cred://chatgpt/personal
browser_session
ChatGPT Web / personal
Chrome Profile 1
Valid
```

操作：

```text
Replace
Re-import
Re-login
Unbind
Delete
```

但默认不能：

```text
Reveal plaintext password
Reveal full cookie
Reveal API key
```

---

# 十二、Groups 页面

这是你的 Clash 感最强的地方。

显示：

```text
Groups
├── auto
├── bronze
├── silver
├── gold
├── platinum
├── diamond
├── reverse-only
├── official-only
├── local-only
└── coding
```

每个 Group 可以编辑：

```text
Type
Min tier
Allowed source kinds
Allow metered
Allow subscription
Require tools
Require vision
Max cost
Routing strategy
```

---

## 十三、任意 Source 都能改变所在组

你明确提到的：

> 改变某来源在 auto 里面所属组

我建议不要只做“属于哪个组”一个字段。

应该支持：

```text
Source membership
```

例如：

```text
chatgpt-web/personal/gpt-x

Groups:
☑ auto
☑ silver
☑ reverse-only
☐ gold
☐ coding
```

一个 Source 可以同时属于多个组。

---

## 十四、Source Detail 页面

Source 是真正调度单位。

显示：

```text
Provider
Account
Target model
Protocol
Tier
Tools
Vision
Quota domain
Max inflight
Auto approved
Enabled
Current health
TTFT
Success rate
Cooldown
```

操作：

```text
Enable / Disable
Set tier
Set auto approved
Set max inflight
Change quota domain
Edit group memberships
Test
Benchmark
```

---

# 十五、调度策略页面

前端必须直接编辑 Router。

例如：

```text
Auto Routing

Strategy:
○ weighted
○ latency
○ fallback
○ load-balance
○ auto

Minimum quality:
Silver

Prefer:
☑ lower latency
☑ existing subscription
☑ lower cost
☐ official API
☐ reverse source

Fallback:
3 attempts

Queue:
128
```

---

## 十六、支持拖拽排序

尤其是 `fallback`。

例如：

```text
fallback / coding
─────────────────
1. Copilot account-1
2. Kiro account-1
3. Claude subscription
4. OpenRouter
```

拖动改变优先级。

---

# 十七、每个 Source / Account 都必须有独立开关

至少四级：

```text
Provider enabled
Account enabled
Source enabled
Auto approved
```

语义：

```text
Provider disabled
→ 全部关闭

Account disabled
→ 该账号所有 Source 不再调度

Source disabled
→ 只关闭这个具体模型/入口

AutoApproved false
→ 允许显式调用
→ Auto 不使用
```

这四个不要混。

---

# 十八、所有配置前端可修改

你明确要求：

> 修改所有配置

我建议配置分成：

```text
Runtime
Browser
Devices
Providers
Accounts
Credentials
Sources
Models
Groups
Routing
Security
Logging
Performance
```

全部通过 Admin API 修改。

不要让前端直接编辑 JSON 文件。

---

## 十九、需要 Configuration Service

目前 admin enable 只是：

```text
runtime only
```

以后不够。

建议：

```text
Frontend
↓
Admin API
↓
Config Service
↓
Validate
↓
Write transaction
↓
Build snapshot
↓
Atomic swap
```

配置失败：

```text
不写入
不应用
返回错误
```

成功：

```text
持久化
+
立即生效
```

---

# 二十、配置版本与回滚

前端：

```text
Configuration history

v84  Added Claude account
v83  Disabled Poe
v82  Changed auto strategy
```

支持：

```text
View diff
Rollback
```

这对于大量 Provider 非常重要。

---

# 二十一、模型管理

每个 Provider/account 后面应该可以：

```text
Discover Models
```

返回：

```text
model
upstream id
protocol
context
tools
vision
enabled
tier
```

然后用户选择：

```text
Enable all
Disable
Set tier
Add to group
```

---

# 二十二、模型发现不能自动进入 Auto

新发现：

```text
model-x
```

默认：

```text
enabled = false
auto_approved = false
tier = unrated
```

用户或测试验证后再开启。

---

# 二十三、正式 API 来源全部纳入

这一轮同时做。

建议正式 Provider：

```text
OpenAI
Anthropic
Gemini
DeepSeek
Kimi
Zhipu
MiniMax
Alibaba DashScope
Volcengine Ark
Tencent Hunyuan
Baidu Qianfan
OpenRouter
SiliconFlow
Together
Groq
Mistral
Fireworks
Cerebras
OpenAI-compatible custom
Anthropic-compatible custom
```

不用为每个都写完全独立 client。

做：

```text
generic OpenAI driver
generic Anthropic driver
generic Gemini driver
```

然后 Provider profile 配差异。

---

# 二十四、Provider 类型前端明显区分

例如徽标：

```text
Official
Cloud
Aggregator
Reverse
Browser
App
CLI
Local
Custom
```

用户一眼知道：

```text
这是什么来源。
```

---

# 二十五、Auto 的来源过滤前端完全透明

点：

```text
Why wasn't this source selected?
```

应该显示：

```text
ChatGPT Web / personal

Excluded because:
- current inflight 1/1
```

或者：

```text
Notion AI

Excluded because:
- tier unrated
- auto requires silver
```

或者：

```text
OpenRouter

Excluded because:
- metered sources disabled
```

---

# 二十六、Routing Simulator

这个很值得做。

用户在前端填：

```text
Model: auto/silver
Protocol: responses
Tools: yes
Vision: no
Input: 120 KB
```

点击：

```text
Simulate
```

输出：

```text
1. Copilot / personal       eligible
2. Kiro / account-a         eligible
3. Claude Web               rejected: tools unsupported
4. OpenRouter               rejected: metered disabled
5. Gemini Web               rejected: busy
```

不用真的发请求。

---

# 二十七、Health 页面

显示：

```text
Provider
Account
Source

Healthy
Degraded
Cooldown
Auth Required
Blocked
Exhausted
Broken
Disabled
```

以及：

```text
Last success
Last failure
HTTP status
Cooldown until
TTFT
Success rate
```

---

# 二十八、Test Provider

每个来源：

```text
[Test]
```

发送一个最小测试请求。

显示：

```text
Connection        pass
Auth              pass
Request           pass
Streaming         pass
Completion        pass
Latency           1.3s
```

不应默认跑昂贵 benchmark。

---

# 二十九、Live Verification

把当前：

```text
live_verified_sources = 0
```

改成真实系统。

分别显示：

```text
Catalog verified
Local credential verified
Live upstream verified
Last verified
```

不要一个布尔值覆盖全部。

---

# 三十、Credential 自动发现页

首页加：

```text
Discover Accounts
```

扫描：

```text
Browsers
Environment variables
Password managers
CLI sessions
Existing profiles
```

输出：

```text
Detected:

ChatGPT Web
Chrome Profile 1
[Import]

Claude
Edge Default
[Import]

OpenRouter
Bitwarden
[Import]

OpenAI
OPENAI_API_KEY
[Import]
```

---

# 三十一、账号绑定必须支持“一份凭证多个 Source”

例如：

```text
OpenRouter account
```

下面可能：

```text
GPT model
Claude model
Gemini model
```

共用：

```text
credential
quota domain
```

不能每模型复制一套 secret。

---

# 三十二、同一凭证可手动绑定任意 Provider

你明确说：

> 任意给任意来源增加账密

前端应该允许：

```text
Add credential binding
```

流程：

```text
Choose Provider
Choose credential
Choose account name
Validate
```

但如果 Credential 类型不匹配：

```text
Bitwarden password
→ OpenAI API Key provider
```

应该提示：

```text
incompatible credential type
```

可以手动 override，但要明确风险。

---

# 三十三、Quota Domain 可视化

前端显示：

```text
Quota Domains

chatgpt-personal
├── chatgpt-web/gpt-x
└── chatgpt-web/auto

openrouter-main
├── openrouter/gpt
├── openrouter/claude
└── openrouter/gemini
```

可以手动编辑。

这样不会重复计算容量。

---

# 三十四、Account Pool 页面

例如：

```text
ChatGPT Web
Accounts: 4

☑ account-a
☑ account-b
☐ account-c
☑ account-d
```

设置：

```text
round-robin
least-load
sticky
weighted
```

每账号权重：

```text
a 3
b 1
d 2
```

---

# 三十五、Session 页面

虽然你不是 Agent 平台，但反代会话仍需要可见。

显示：

```text
Session ID
Provider
Account
Upstream conversation
Age
Last activity
```

操作：

```text
Expire
Clear
```

不能显示对话全文，除非用户显式开启 debug。

---

# 三十六、Browser 页面

前端显示：

```text
Browsers

Chrome personal
Running
CDP connected
Sessions 2/8

Chrome work
Stopped
```

操作：

```text
Launch
Stop
Login
Open provider
Refresh
```

---

# 三十七、Browser Login Wizard

点击：

```text
Add ChatGPT account
```

弹出：

```text
Choose:

○ Use existing Chrome profile
○ Launch isolated profile
○ Import session
○ Manual cookie
```

选择：

```text
Launch isolated profile
```

然后：

```text
Browser opened.
Please log in normally.

[Waiting for login...]
```

成功：

```text
Login detected
[Save account]
```

---

# 三十八、Device 页面

中国 App Provider：

```text
Android Devices

OnePlus 9 Pro
connected
ADB ready
Resolution
Foreground app
```

每个 App Provider：

```text
Meituan Xiaotuan
Login state: detected
Last test: pass
```

仍然只模拟聊天 Provider。

---

# 三十九、Source Implementation 状态页

每个来源显示：

```text
Implementation:
native-go
browser
device
cli-wrapper
generic-http
recipe

Reference:
repo URL

Status:
implemented
verified
broken
research
```

方便你长期维护。

---

# 四十、Provider Descriptor Registry

必须解决现在代码里大量 `switch`。

以后一个 Descriptor：

```go
type Descriptor struct {
    ID
    Kind
    Factory
    Protocols
    CredentialRequirements
    Capabilities
    DefaultLimits
}
```

配置校验、UI schema、模型发现、credential setup 都从 Descriptor 读取。

这样新增 Provider 不用改五六处。

---

# 四十一、Admin API 重构

建议：

```text
GET  /admin/providers
GET  /admin/providers/{id}

POST /admin/providers/{id}/enable
POST /admin/providers/{id}/disable
POST /admin/providers/{id}/validate

GET  /admin/accounts
POST /admin/accounts
PATCH /admin/accounts/{id}
DELETE /admin/accounts/{id}

POST /admin/accounts/{id}/login
POST /admin/accounts/{id}/validate

GET  /admin/credentials
POST /admin/credentials/import
DELETE /admin/credentials/{id}

GET  /admin/sources
PATCH /admin/sources/{id}

GET  /admin/groups
POST /admin/groups
PATCH /admin/groups/{id}

POST /admin/routing/simulate

GET /admin/browser/profiles
POST /admin/browser/launch
POST /admin/browser/stop

GET /admin/config
PATCH /admin/config
GET /admin/config/history
POST /admin/config/rollback
```

---

# 四十二、前端技术结构

如果你继续坚持低资源，我不建议 Electron。

直接：

```text
Go server
+
embedded static frontend
```

前端可以：

```text
Vue / React / Solid
```

构建成静态文件 embed 到 Go binary。

运行时没有 Node。

---

# 四十三、前端导航最终建议

```text
Overview

Providers
Accounts
Credentials
Sources
Models

Groups
Routing

Browsers
Devices

Health
Sessions
Metrics

Config
Logs
About
```

---

# 四十四、前端必须有全局搜索

例如搜索：

```text
claude
```

返回：

```text
Provider Claude Web
Provider Anthropic
Account claude-main
Source claude-web/claude-x
Group claude-premium
Credential cred://claude/main
```

---

# 四十五、所有变更都要有 Preview

比如修改 Auto：

```text
Remove ChatGPT Web from silver
```

前端提示：

```text
Impact:

silver eligible sources:
12 → 9

3 accounts will no longer be routed by silver.

Apply?
```

---

# 四十六、配置验证必须集中

任何 UI 修改都走：

```text
Validate
→ Compile
→ Apply
```

不能：

```text
前端把数据库直接改掉
```

---

# 四十七、配置热更新

Provider 开关、账号开关、Group、Auto 策略：

```text
即时生效
```

Browser runtime、监听地址等：

```text
需要 restart
```

前端明确标：

```text
Live
Restart required
```

---

# 四十八、敏感操作确认

例如：

```text
Delete credential
Disable entire provider
Change quota domain
Enable metered API in auto
```

需要确认。

尤其：

```text
Enable paid source in auto
```

前端明确显示：

```text
This may incur additional charges.
```

---

# 四十九、日志彻底脱敏

全局 Secret 类型。

日志永远不能出现：

```text
API key
Cookie
refresh token
password
Authorization
```

只显示：

```text
credential_ref
account_id
source_id
```

---

# 五十、下一轮建议实际拆成 5 个 Epic

如果准备真正开发，我会这样拆。

### Epic A — Data Model & Config Core

做：

```text
Provider Descriptor
Account
CredentialRef
Source metadata
QuotaDomain
Group membership
Config persistence
Config history
```

这是底座。

---

### Epic B — Credential & Login System

做：

```text
OS credential store
Browser import
Password manager import
Env import
CLI session import
Browser profile registry
Login wizard
Credential matching
```

---

### Epic C — Complete Admin Backend

做：

```text
Provider CRUD-ish config
Account management
Source management
Group management
Routing config
Browser control
Validation
Health
Metrics
```

---

### Epic D — Complete Frontend

先做到功能完整：

```text
Providers
Accounts
Credentials
Sources
Groups
Routing
Browser
Health
Config
```

再做视觉优化。

---

### Epic E — Runtime Correctness

并行修：

```text
request-level fallback
stream completion observer
execution result model
source kind semantics
live verification
routing explain
```

---

# 五十一、我给你的最终优先级

真正实施顺序我建议：

```text
1. Provider Descriptor Registry

2. Account + CredentialRef 数据模型

3. Credential Store

4. Config Service + persistence

5. Admin API 全面化

6. Provider / Account / Source / Group 四层状态

7. Browser Profile Registry + Login Wizard

8. Browser / password manager / env / CLI import

9. 完整 frontend

10. Routing editor + simulator

11. Request-level fallback

12. Stream completion observer

13. Model discovery

14. Quota domain editor

15. Live verification

16. 继续批量扩 Provider
```

这套完成之后，用户体验应该变成：

```text
安装 Clash of Tokens
        ↓
打开 Web UI
        ↓
自动发现浏览器 / API Key / 密码管理器 / CLI 账号
        ↓
Import
        ↓
缺登录的点 Login
        ↓
Providers 自动出现账号
        ↓
选择哪些账号启用
        ↓
选择哪些 Source 进入 Auto
        ↓
拖拽配置 Groups / fallback / load balance
        ↓
调用 localhost:8317
```

这会比“有 150 个 Adapter，但要手工改 JSON”强一个数量级。

从产品形态上，它也会真正接近你最初的 Clash 隐喻：

> **Clash 管代理节点、订阅、策略组。  
> Clash of Tokens 管 AI Provider、账号、会话、模型和策略组。**

这应该就是下一轮的主目标。
