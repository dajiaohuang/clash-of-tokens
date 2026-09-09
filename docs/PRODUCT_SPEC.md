# Clash of Tokens：最终产品与技术方案

**最终定位：一个本地优先、原生 Go 实现的 AI 反代来源池与模型调度网关。**

把网页 AI、应用内 Bot、编码助手和订阅入口统一管理，提供来源独立启停、质量分级、自动选路、并发调度和故障隔离。上层大量 Agent 连接同一个 API，不必分别处理每个来源的鉴权、协议和可用性。

> **Clash of Tokens — Many AI sources. One API.**

完整方案已经整理为可放入仓库的文件，包含详细架构、接口草案、来源参考、配置、安全边界和验收标准：

:chatgpt-content-reference{index="15"} · :chatgpt-content-reference{index="16"} · :chatgpt-content-reference{index="17"}

以下是最终确定的方案。**所有功能均为设计与实现目标；性能数字是待验收指标，不是已经完成的实测成绩。**

## 一、项目名称、定位与边界

| 项目 | 最终决定 |
|---|---|
| 项目名称 | **Clash of Tokens** |
| GitHub 仓库名 | `clash-of-tokens` |
| 主命令 | `clash-tokens` |
| 实现语言 | Core、Provider Adapter、调度器、管理后端全部 Go |
| 组织方式 | 一个 monorepo，所有原生 Adapter 在本项目内维护 |
| 发布方式 | 一个主要可执行文件，必要时用同一二进制的 worker 模式隔离高成本来源 |
| 外部反代项目 | 仅作为协议、行为和实现研究参考，不作为必需运行时服务 |
| 主要用户 | 在本地同时运行多个 Agent、希望统一使用大量异构 AI 来源的开发者 |
| 核心差异 | 广泛内置反代来源，而不只是聚合官方 API Key |
| 核心性能原则 | **大目录、小活跃集；所有资源有界；网关不与 Agent 抢占不必要的资源** |

名称借鉴 Clash 的节点管理和策略组选路心智：用户管理的不是 VPN 节点，而是 AI 来源。Mihomo 官方文档中的 Select、URL-Test、Fallback、Load-Balance 等策略，可以转化为本项目的手动选择、性能探测、故障转移和容量分配。这里是交互与架构借鉴，不是宣称与相关项目有官方关系。citeturn562086view3

项目不再使用 ReverseHarbor、InferMux、ModelClan 等旧名称；tier 不再与项目名称绑定。也不把 `cot` 作为默认安装命令，避免过短缩写带来的命令与检索歧义。

对外不使用“无限 Token”“永远有模型”“无限并行”等承诺。准确目标是：

**在用户允许的来源、可用权益、质量要求和资源预算内，尽可能持续提供可靠的推理服务。**

## 二、解决什么，以及不解决什么

本项目解决的是：

```text
多个 Agent
    ↓
一个本地 API
    ↓
统一来源池与调度器
    ↓
当前合格、启用、有额度、有容量的 AI 来源
```

它必须把三层限制分开。

**Agent 宿主限制**决定能创建多少 subagent、工具权限是什么、是否可以使用自定义模型。**网关限制**决定本机可以承载多少请求、排队和浏览器任务。**上游限制**决定各来源真正允许的请求速率、并发、模型和权益。

因此，换成 Clash of Tokens 不会自动解除 `agent thread limit reached`。例如，当前 Codex 配置参考仍单独定义了 spawned-agent 并发线程配置；这和模型 API 网关容量是不同的控制层。citeturn845290view6

最终使用方式是：由支持自定义模型入口的 Agent 宿主或自有编排器负责创建 Agent，由 Clash of Tokens 给这些 Agent 分配模型调用资源。

**Auto 不负责递归创建 Agent。**

```text
Orchestrator：拆任务、创建 Agent、管理工具、汇总结果
Clash of Tokens：准入、选模型、分配容量、调用、流式回传
```

普通 `auto` 调用原则上只对应一个正在执行的上游调用。未来增加工作流编排，也必须是独立、显式启用的功能，不能藏在普通模型请求里。

## 三、全 Go 内置，但不做“巨型常驻资源集合”

你的核心要求保留：

**所有正式支持的反代 Adapter，直接原生 Go 实现到本项目内。**

不要求用户部署 Chat2API、g4f、CLIProxyAPI 等外部服务，再套一层代理。已有 Go 项目可以在许可审查后复用合适代码；其他语言的项目用于理解协议和行为，最终实现仍归入本仓库。

但是，需要区分三个概念：

| 概念 | 是否纳入最终方案 |
|---|---|
| 原生 Go 处理 HTTP、SSE、WebSocket、gRPC 等协议 | 是，主要实现方式 |
| Go 通过 CDP 控制浏览器 | 是，可选高成本路径 |
| Go 启动一个 Python/Node 反代服务 | 否，不作为正式接入方式 |
| Go 仅包装第三方 CLI/Language Server | 不能算独立原生实现；未验证直连协议前保留为研究项 |

这项区分很重要：目前找到的 Cursor 参考项目明确是 CLI 包装，Windsurf 参考项目的主要路径包含外部 Language Server；不能仅把包装层换成 Go，就声称整个接入已经原生化。citeturn904509view3turn904509view1

浏览器来源允许依赖用户安装的 Chrome/Chromium，使用 Go CDP 实现，例如 chromedp；但应明确标注浏览器依赖，而不是宣传“完全无外部依赖”。citeturn577876view3

**所有 Adapter 编译进程序，不等于全部启动。** 禁用来源只保留轻量目录信息，不创建连接、探测定时器、浏览器、账号刷新任务或 worker。

## 四、完整来源池范围

所有前面讨论过的来源进入首期总目录和实现 backlog，不再丢回外部代理服务。按验证批次完成，但不把“列入目录”写成“已经支持”。

| 来源类别 | 纳入范围 |
|---|---|
| 主流 Web AI | ChatGPT Web、Claude Web、Gemini Web、Grok Web、Perplexity Web |
| 编码助手与订阅入口 | Copilot、Cursor、Windsurf、Kiro、Codex、Claude Code、Gemini CLI、Antigravity、Qwen Code、iFlow、Qoder |
| 中国 Web/App | DeepSeek、Kimi、Qwen 中国站与国际站、GLM、Z.ai、MiniMax、MiMo、腾讯元宝、豆包、文心 |
| 多模型应用与专用研究产品 | Poe、Genspark、AI Studio Build、AI Studio Playground、NotebookLM、Meta AI、Duck.ai |
| Bot 与角色平台 | Character.AI、Discord AI Bot、Telegram AI Bot、Slack AI Bot |
| 多模态任务 | Midjourney，以及 AI Studio、Genspark 等来源的图像、视频、音频任务 |
| 长尾观察目录 | HuggingChat、Blackbox、Trae、You、Phind、Pi、Arena 类站点及后续发现的入口 |
| 可选保障来源 | 用户明确启用的官方 API、Poe 官方 API、本地模型 API |

这里有两个修正。

首先，**Poe 不能全部归类成网页逆向。** Poe 现有官方兼容 API，文档说明可以使用订阅积分。因此需要区分 `poe-api` 与待独立验证的 `poe-web`；前者可以作为正式授权入口，不必为了“反代”标签强行使用更脆弱的路径。citeturn845290view8

其次，**聚合项目不是新的独立容量来源。** CLIProxyAPI、Chat2API、g4f 是研究入口，需要拆解其中具体 Provider；不能同时把“Gemini Web”“CLIProxyAPI 中的 Gemini”“g4f 中的 Gemini”算成三份独立账号额度。CLIProxyAPI 的多协议与账号管理、Chat2API 的多站点适配，都有参考价值，但不应原封不动成为本项目的运行时依赖。citeturn577876view0turn577876view1turn567165view7

优先研究的参考实现包括：

| 方向 | 主要参考 |
|---|---|
| ChatGPT Web | `Octo-Lex/ChatGPT-Web2API`、`aurorax-neo/chat2api` |
| Claude / Gemini Web | `yushangxiao/claude2api`、`ntthanh2603/gemini-web-to-api`、`qutek/gemini-web-api` |
| Grok / Perplexity | `chenyme/grok2api`、`jamie950315/pplx-proxy` |
| Copilot / Kiro | `messense/copilot-api-proxy`、`caidaoli/kiro2api` |
| 多入口与中国 AI | `router-for-me/CLIProxyAPI`、`xiaoY233/Chat2API` |
| Bot / 异步任务 | `kamilio/poe-api-bridge`、`kramcat/CharacterAI`、`novicezk/midjourney-proxy` |

这些项目分别提供了 Web/CDP、协议转换、账号池、Bot 或异步任务方面的参考；本次核对不等于完成真实账号运行测试或完整代码审计。完整文档中保留了更全面的参考列表。citeturn203006view0turn203006view1turn203006view2turn203006view3turn845290view5turn577876view2turn203006view4turn577876view4turn904509view2turn904509view6turn904509view5

来源目录长期按 **50–100 个来源家族**设计，但管理界面必须分别显示：目录总数、已实现数、已配置数、已验证数、当前可调度数。

## 五、统一资源模型

不再把“一个 Provider”直接等同于“一个可调度模型”。

核心对象统一为：

| 对象 | 含义 |
|---|---|
| Provider | 上游产品或明确接入入口 |
| Adapter | 对应的 Go 协议实现 |
| Account | 用户授权身份与凭证引用 |
| Target | 远端模型、Bot、角色、Agent 或工作流 |
| Source | 一份具体可调度的接入组合 |
| Group | 来源集合及其选路策略 |
| Session | 本地会话与上游账号、会话、执行配置的绑定 |
| Lease | 有期限的容量占用或预留 |
| Quota Domain | 多个来源共享的额度或并发限制 |
| Failure Domain | 可能同时故障的来源集合 |

Source 可以理解成：

```text
Provider × Account × Target × 接入方式 × 执行配置
```

例如，同一个模型经网页、编码助手、Bot 和官方 API 接入，可以是不同 Source，因为系统提示词、工具能力、上下文处理和可观测性不同。

但它们是否拥有独立额度，要由 Quota Domain 决定，不能因为 URL 不同就重复记账。

**Target 不限定为基础模型。** NotebookLM 可以是资料库绑定的研究能力，Character.AI 可以是角色，Midjourney 可以是异步媒体任务。这些来源可以进入同一个目录，但不能不加区分地进入同一个编码模型池。

## 六、Tier 最终命名与评价规则

保留已经讨论过的五级体系：

| Tier | 机器值 | 定位 |
|---|---|---|
| Bronze | `bronze` | 分类、简单抽取、格式整理等轻任务 |
| Silver | `silver` | 大量常规并行 subagent 的质量基线 |
| Gold | `gold` | 常规开发、分析、研究中的强能力来源 |
| Platinum | `platinum` | 困难代码修改、复杂推理、较长任务链 |
| Diamond | `diamond` | 达到最高质量验收门槛的来源 |

接口统一为：

```text
auto
auto/bronze
auto/silver
auto/gold
auto/platinum
auto/diamond
```

`auto/silver` 表示**至少达到 Silver**。预算允许时可以选 Gold，但不能静默降成 Bronze。

这里固定四条规则：

**质量与速度分开。** Silver 不是“快模型”的别名，Diamond 也不代表一定慢、一定贵。

**按任务维度评级。** 一个来源可以代码 Gold、研究 Silver、结构化抽取 Bronze，而不是给整个供应商品牌统一贴等级。

**评级必须带证据。** 保存评测集版本、执行配置、验证日期、样本数和置信度。没有证据就是 `unrated`；人工指定可以存在，但必须明确显示。

**最高等级可以为空。** 当前剩下的模型不够强时，不能把其中最强的一个临时改叫 Diamond。

同时，工具能力必须区分：

```text
native / emulated / none / unknown
```

Chat2API 和 Poe API Bridge 的公开说明都包含提示词模拟工具调用的方式；这与上游原生结构化工具调用不是同一回事。严格 coding 组应要求经过测试的工具闭环，不能把“能聊天”自动提升成“能可靠跑 Agent”。citeturn203006view7turn904509view0

## 七、总体架构与协议设计

```text
Agent / Codex / 自有 Orchestrator
                │
    OpenAI / Anthropic / Gemini API
                │
  鉴权 + 请求边界 + 全局资源准入
                │
    元数据扫描 / 按需类型化 IR
                │
       Rules → Groups → Auto
                │
  共享配额域 + Source 容量 + Session
                │
       原生 Go Provider Adapter
                │
   HTTP / SSE / WS / gRPC / CDP
                │
        用户启用的 AI 来源
                │
       有界流式回传 / 任务结果
```

控制平面负责配置、凭证引用、发现、健康、评测、路由快照和管理 UI；数据平面只负责准入、选择、调用、回传、取消和释放。

### 内部 IR 保留，但不是每次都完整转换

采用三条路径：

```text
协议与语义一致：安全直通
少数字段不同：结构化局部修改
协议不同：按需解析 → 类型化 IR → 上游编码
```

不把每个请求都变成：

```text
JSON → map[string]any → IR → 新对象树 → JSON
```

但也不使用简单字符串替换修改 JSON。字段重复、转义、嵌套、未知字段和工具 schema 都必须正确处理。

需要纠正之前的绝对化描述：**任意 JSON 中的 model 字段可能在请求体末尾，所以浅扫描最坏仍是 O(请求体大小)**。可以优化的是分配、复制和解析次数，不能把整条链路宣传为 O(1)。

### API 兼容按功能验收

主要兼容面：

```text
GET  /v1/models
POST /v1/chat/completions
POST /v1/responses
POST /v1/messages
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent
```

第一阶段就应包含 Responses 的关键语义，而不是只做 Chat Completions。当前 Codex 自定义 Provider 配置以 `responses` 作为 wire API，因此接入验收必须包含对应的流事件、工具调用和多轮交互。citeturn845290view7

路径一致不等于完整兼容。每个 Adapter 必须声明支持的角色、工具、结构化输出、图片、附件、会话和取消能力；无法保真的必需语义返回明确错误，不能悄悄丢字段。

## 八、Auto、策略组与并发调度

策略组提供五种类型：

| 类型 | 行为 |
|---|---|
| `select` | 用户手动固定来源 |
| `fallback` | 按明确顺序选择可用来源 |
| `latency` | 在合格来源中优先低延迟来源 |
| `load-balance` | 按权重与容量分散请求 |
| `auto` | 综合质量、能力、期限、配额、成本与本机资源选路 |

Auto 的执行顺序固定为：

```text
人工启用
→ 数据与授权策略
→ 实现成熟度
→ 账号与健康状态
→ 所需能力
→ 上下文是否容纳
→ 质量等级
→ 共享额度与速率限制
→ 实时容量与会话绑定
→ 预算和 deadline
→ 候选排序与分配
```

**先过滤，再评分。** 再高的性能分数，也不能覆盖人工禁用、质量门槛或隐私限制。

常规 Auto 不调用额外 LLM 做路由。任务类型优先由 Agent 提供；未提供就按保守规则处理。语义分类器只能作为显式可选功能，不进入默认热路径。

候选选择不能永远拿第一名，也不能只保留几个 top 候选后，在它们满载时错误报告“全池不可用”。采用有界、容量感知的选择，并保留后备候选或进入有界等待。

### 容量必须跨账号、模型和共享权益协调

不能只维护：

```text
某模型最大并发 = 8
```

而要同时考虑：

```text
全局容量
客户端公平份额
Provider 容量
Account 容量
共享 Quota Domain
有状态会话写入限制
```

未知上游的初始并发采用保守策略，之后只在用户许可和已知限额内调整。不能通过持续撞 429 的方式“探测最大可薅额度”。

排队只保留一个逻辑等待层，避免 Provider、账号、模型和连接池各排一次，造成不可解释的长尾。

**可选容量预留**用于父 Agent 一次派发大量子任务。预留有短 TTL、client 作用域、一次消费和幂等释放，允许部分成功；它不是对未来上游必定可用的保证。

## 九、人工开关、会话绑定和安全故障转移

### 人工控制永远优先

Provider、Account、Target、Source 均可独立启停，并区分：

```text
enabled：允许被使用
auto_approved：允许被自动选择
```

一个来源可以允许手动测试，但不加入默认 Auto。

人工禁用成功后，不再提交新的上游任务；在途请求默认 drain，也提供显式立即取消。不能仅等下一次路由快照更新后才生效。

运行状态与配置状态分开。`enabled=true` 的来源暂时故障可以自动摘除，修复验证后重新入池；`disabled` 来源不能因为探测成功而自动开启。

### 有状态会话不能随意换账号

默认保存小型绑定元数据，不长期保存完整对话：

```text
本地 Session
→ Source
→ Account
→ 上游 conversation/thread ID
→ 执行配置
```

同一上游会话的并发写入默认串行。不同 subagent 默认获得独立会话，不偷偷共享一个网页 thread。

哈希分配只能辅助新会话。已有上游 conversation 建立后必须显式绑定，不能在账号池变化时因重新哈希而把旧会话发到另一个账号。

默认不自动摘要、截断历史或缩小工具 schema。超出能力时应换到真正支持的来源或拒绝，而不是改变输入后假装原请求已执行。

### 故障转移以输出正确性为先

| 情况 | 默认处理 |
|---|---|
| 建连前明确失败 | 可以有限重选 |
| 明确限流 | 尊重冷却与共享额度域，选择其他仍合法可用的来源 |
| 凭证失效 | 协调正常刷新；失败后要求人工认证 |
| 验证码、403、风控或封禁 | 停止来源，不自动绕过 |
| 已向客户端返回有效内容或工具调用 | 不透明切换供应商续写 |
| 异步任务提交结果未知 | 先查询原任务，不贸然重复提交 |
| 客户端取消 | 停止本地工作，并记录上游是否真的确认取消 |

最重要的一条：

**不能把模型 A 的半段回答和模型 B 的另一半拼成一份正常完成的回答。**

透明重试只适用于可重放、未提交业务输出、没有不可重复副作用的请求，并且同时受最大 attempt、总 deadline 和预算约束。

本地取消也不代表上游停止生成或退还额度。需要区分“本地连接已断”“上游确认停止”“不支持取消”“状态未知”，并在必要时保留有限的 draining 容量记录。

## 十、本地性能设计：把资源消耗作为核心功能

这里保留前面“极充分优化”的要求，但把目标从堆砌技巧改成可验证的工程标准。

### 1. 一个常驻 Core，所有 Agent 共享

同一用户/信任域运行一个服务。复用连接、目录、必要会话、鉴权刷新协调和路由状态，而不是每个 Agent 启动一套网关。

但不跨无关账号共享 Cookie、WebSocket 身份或浏览器会话。连接复用按出口、TLS 配置和连接级身份正确隔离。Go 官方文档明确建议复用 Transport，而不是逐请求创建。citeturn562086view1

### 2. 先消除额外工作，再优化单步速度

首要优化对象是：

**重复浏览器、重复外部进程、完整请求体多份复制、重复编解码、无界排队、每 token 日志。**

这些优先于更换 JSON 库、编写 lock-free 容器或使用 `unsafe`。

不再把“全无锁”当成硬约束。正确的小型临界区、分片锁和原子计数都可使用，最终由 profile 和回归测试决定。

### 3. 所有资源同时限制数量和字节数

不仅限制请求数，还限制：

```text
总在途请求体字节数
队列持有字节数
单事件与累计输出大小
工具参数缓冲
附件与临时文件
会话缓存
空闲连接
浏览器进程、页面与任务
```

一个排队项即使只有指针，也可能持有一个 8 MiB 请求体。**128 个这样的 body，仅原始数据就需要 1 GiB 内存。**

因此读取大 body 前就要申请入口预算，完成必要的发送/重试阶段后尽早释放；不能让整个数分钟生成过程都持有不再需要的输入副本。

### 4. 流式处理必须端到端有界

同协议优先 relay，跨协议按事件解析。不积攒整个回答，不给每个 token 构建完整对象树。

正确处理 SSE 多行内容、分片 UTF-8、跨包 JSON、工具参数增量和终止事件；不能把网络包当 token。

慢客户端通过背压或超时处理，不无限缓存。复用 WebSocket 时，一个慢消费者不能拖住其他会话。

分别观测首网络字节、首协议事件和**首个有效模型输出**，心跳不计为 TTFT。

### 5. 路由读路径使用快照，实时状态单独校验

目录、能力和策略组使用不可变快照，由控制平面构建后原子替换。

实时容量、禁用、冷却独立维护。快照选中来源后，上游提交前还要检查最新状态。

数据库、配置文件写入、健康探测和日志落盘不阻塞流式数据平面。

### 6. Buffer 复用不能变成隐藏内存池

只复用适当大小的临时对象，大 body 不永久放回池中。硬字节预算独立实现。

`sync.Pool` 的对象可能被自动移除，而且它不是严格有界缓存，不能把它当内存上限机制。citeturn845290view9

### 7. 浏览器默认关闭、按需启动、单独计费

浏览器路径必须显式启用，设进程、页面、任务和空闲退出预算。可以排队，不能为了高并发宣传同时启动几十个 Chrome。

浏览器 CPU/RSS 必须显示在总资源面板中，不能只报告 Go Core 很轻，隐藏真正的浏览器开销。

### 8. GC 与 CPU 配置是调节器，不是补救无限资源的手段

保留正常 GC。`GOMEMLIMIT` 是 Go runtime 软限制，不是 RSS 硬上限，也不控制浏览器进程；设置过低可能导致 GC thrashing。citeturn562086view0

提供 `eco / balanced / throughput` 配置及 `GOMAXPROCS` 设置，但不声称它是硬 CPU 配额。

同一 Go 进程中的“每 Provider 内存限制”只是应用层记账；需要硬隔离时使用受控 worker 和操作系统级限制。

### 9. 遥测和 UI 不成为负担

正常请求只产生少量起止、选路和失败记录；默认不记录完整 prompt、response 或每 token 日志。

普通遥测有界、可采样，丢弃也要计数。指标标签保持低基数，不把 session、request 或 prompt 当标签。

管理页采用 Go 模板或嵌入静态资源，不引入 Electron/Node 常驻服务。

## 十一、性能验收标准

**以下为初始目标，必须用固定条件验证后才能对外发布。**

基准分为纯路由、同协议流转发、跨协议转换、大 body、异常/取消、浏览器，以及本地 Agent 共存测试。

| 指标 | 初始目标与口径 |
|---|---|
| 纯内存选路 | p50 < 100 μs，p99 < 1 ms；不含 body 读取、排队和上游 |
| 路由稳态分配 | 目标 0 alloc/op，不泛化到整个 HTTP/TLS 请求 |
| Core 空闲 RSS | < 64 MiB；浏览器和全文日志关闭 |
| 1000 流 mock 基准 RSS | < 512 MiB；同时报告峰值与每流增量 |
| 1000 流 mock 基准 CPU | 初始目标不超过一个逻辑核的 CPU 时间消耗 |
| 本地额外流转发延迟 | p99 ≤ 10 ms，与直接连接 mock 对照 |
| 禁用 Provider | 不产生后台网络、探测、浏览器或活动 worker |
| 本地取消清理 | mock 可确认取消场景中 p99 ≤ 200 ms |
| 超载 | 在预算内排队或拒绝，不靠 OOM 限流 |
| 长稳态 | 24 小时 mock soak 不出现持续 goroutine、FD、会话或子进程泄漏 |

1000 流基准需要明确负载，例如每流每秒 20 个事件、平均载荷 256 B、8 KiB 请求体；与 1000 个完整重型 Agent 不是一回事。真实账号不做这种暴力压测。

大上下文分别测试 1、8、16 MiB 请求体，并记录准入拒绝和缓冲峰值。不能同时承诺任意大输入、无限并发和固定极低内存。

每个 PR 做对应微基准；固定机器运行完整回归，观察 CPU、RSS、B/op、allocs/op 和尾延迟。PGO 使用代表性 workload 的 CPU profile，实际收益另行测量。citeturn562086view2

## 十二、配置、管理与可观察性

配置使用带版本的声明式 schema。文件与 UI 共用一个配置服务，经过校验、事务更新、快照编译再生效，支持回滚。

核心配置关系如下：

```yaml
schema_version: 1

server:
  listen: "127.0.0.1:8317"
  api_key_ref: "env:COT_API_KEY"

runtime:
  max_inflight: 128
  max_queued: 128
  queue_timeout: 5s
  max_body_bytes: 16777216
  max_buffered_body_bytes: 134217728
  max_attempts: 3

browser:
  enabled: false
  max_processes: 1
  max_active_tasks: 1

providers:
  chatgpt-web:
    enabled: false
  gemini-web:
    enabled: false
  copilot:
    enabled: false
  kiro:
    enabled: false

auto:
  default_min_tier: silver
  approved_sources: []
  allow_unrated: false
  allow_quality_downgrade: false
  allow_paid_fallback: false

groups:
  coding:
    type: auto
    task_profile: code
    min_tier: silver
    required_capabilities:
      tools: native
    allowed_sources: []
```

这是拟定 schema。首次安装全部关闭和空 allowlist 是有意设计：用户认证、验证并批准来源后，才允许自动发送内容。

管理页必须解释“为什么没选这个来源”，而不只是显示一个红灯：

```text
人工禁用
未授权进入此组
未达到质量门槛
不支持原生工具
上下文不足
账号需要认证
共享额度耗尽
当前容量已满
协议损坏
数据策略不允许
```

同时显示真实配额可信度。上游未报告 Token 或额度时标为 unknown/estimated，不伪造精确使用量。

## 十三、来源维护、安全与兼容性

每个 Adapter 独立维护请求编码、事件解析、鉴权、发现、错误分类、探测和脱敏 fixtures。

```text
providers/<provider>/
  provider.go
  auth.go
  request.go
  stream.go
  models.go
  errors.go
  probe.go
  provider_test.go
  testdata/
  README.md
  upstream.yaml
```

`upstream.yaml` 记录参考项目、固定 revision、许可状态、协议样本日期和最近真实验证。没有执行 live verification，就不能填写“验证通过”。

离线 CI 做契约测试、协议回放、fuzz 和 race 检测；真实推理 smoke test 只在用户授权的环境里按预算运行。一个上游改版应让其 Adapter 被隔离，而不是把整个系统拖停。

安全边界纳入架构，而不是发布前补一段免责声明：

**凭证只保存在受保护的本地存储，Agent 不接触上游 Cookie/Token；数据 API 与管理 API 分离；默认 loopback 但仍需认证。**

**自动 fallback 受数据流向约束。** 用户允许把代码发给某个来源，不代表允许把它发给全目录任何网站。来源 allowlist、敏感数据分类、本地-only 和官方-only 策略必须在评分前执行。

**Bot 与媒体任务要正确关联。** 不能把频道里下一条 Bot 消息当作当前请求答案；处理线程、消息 ID、编辑、重复事件和附件异步到达。不能因提交超时重复生成一遍。

**Go 改写不等于可以忽略原许可证。** 复用代码必须审计、保留要求的声明；依赖与上游版本锁定，提供 SBOM、漏洞扫描和回滚。Go 官方安全实践可作为供应链流程参考。citeturn567165view4

## 十四、仓库组织与实施阶段

```text
clash-of-tokens/
├── cmd/clash-tokens/
├── internal/
│   ├── api/
│   ├── protocol/
│   ├── registry/
│   ├── routing/
│   ├── scheduler/
│   ├── quota/
│   ├── sessions/
│   ├── health/
│   ├── transport/
│   ├── resources/
│   ├── secrets/
│   ├── config/
│   ├── telemetry/
│   └── workers/
├── providers/
├── catalog/
├── web/
├── sdk/go/
├── examples/
├── tests/
├── bench/
└── docs/
```

按验收门槛推进，而不是一次填满几十个空壳 Adapter：

| 阶段 | 交付重点 | 通过条件 |
|---|---|---|
| A：协议与可靠基础 | 配置、IR、Adapter 合约、mock、Chat/Responses 基础兼容、至少两个真正原生异构 Adapter | 多轮工具闭环、取消、错误传播、资源边界、无凭证泄漏 |
| B：Auto 与本地并发 | 分级、共享配额域、公平队列、手动开关、故障转移、管理页 | 32/128/512/1000 mock 流、异常与取消、本机 Agent 共存验收 |
| C：主力来源簇 | 主流 Web、编码助手、中国 Web 的原生 Go 实现 | 每个来源有许可审查、能力矩阵和最近验证证据 |
| D：浏览器、Bot 与媒体 | CDP 资源池、消息关联、异步任务、专用研究来源、worker 隔离 | 资源可见有界、不串会话、不重复提交、取消状态真实 |
| E：长尾与稳定维护 | 持续扩大来源家族、断裂检测、回滚、代表性 PGO | 扩大目录不明显增加禁用状态开销；故障不破坏 Core |

前面讨论过的来源全部保留在目标目录中，但实现状态必须真实。**“已经列入计划”“已原生实现”“已通过契约测试”“当前账号实测可用”是四个不同状态。**

---

最终，Clash of Tokens 的衡量标准不是“收集了多少反代名字”，而是：

**当你本地同时运行大量 Agent 时，它能用很小且可控的额外开销，把请求送到合格、授权、启用、有容量的来源；某个来源失效时正确隔离；没有合适来源时明确失败，而不是降低质量、破坏会话或无限重试。**

这就是最终确定的方向：**一个真正面向并行 Agent 的、原生 Go 的 AI 反代池与策略路由器。**
