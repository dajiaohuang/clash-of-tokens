# 网页 Provider（Dola / Yuanbao / DeepSeek / Doubao）

`internal/providers/chinanext` 的 Dola、元宝、DeepSeek 和 Doubao 已注册到网关。
Dola、元宝和 DeepSeek 需要用户明确提供网页凭证；Doubao 使用配置的 Chrome CDP 登录状态。当前只有本地契约测试，没有真实账号验证。

## 来源库存与运行时映射

| 来源库存 | inventory number | 运行时 adapter | 当前状态 |
| --- | ---: | --- | --- |
| `deepseek-web` | 30 | `deepseek-web` | 已注册，单轮 |
| `yuanbao` | 41 | `yuanbao` | 已注册，支持显式会话 |
| `dola-web` | 31 | `dola-web` | 已注册，单轮 |
| `doubao` | 43 | `doubao` | 已注册，使用 CDP 浏览器 UI |

兼容别名只用于复用已锁定的网页实现命名；不会把来源库存、网页账号或
模型目录误报成已验证能力。`dola-web` 与中国站 `doubao` 是两个不同的
库存来源，前者不能代表后者。适配器的调用形状与其他独立 provider 相同：

```go
c := chinanext.New(source)
defer c.Close()
resp, err := c.Do(ctx, "chat", model, stream, body, headers)
```

## 端点与凭证

| adapter | 默认 `base_url` | 上游请求 | `Source.KeyEnv` 内容 |
| --- | --- | --- | --- |
| `dola-web` | `https://www.dola.com` | `POST /chat/completion` SSE | Dola Cookie；必须有 `sessionid`，并有 `s_v_web_id` 或 `fp` |
| `yuanbao` | `https://yuanbao.tencent.com` | 先 `POST /api/user/agent/conversation/create`，再 `POST /api/chat/{conversation_id}` SSE | Cookie；必须有 `hy_user` 与 `hy_token` |
| `deepseek-web` | `https://chat.deepseek.com` | 取用户 token 后创建会话、取得 PoW challenge，最后 `POST /api/v0/chat/completion` SSE | DeepSeek 网页用户 token；也接受 `{"value":"..."}` 包装值 |
| `doubao` | `https://www.doubao.com` | 通过 CDP 驱动 `/chat/` UI，由页面自身 `POST /samantha/chat/completion` SSE | 默认不需 `KeyEnv`；显式配置时仅在隔离 Chrome context 注入 Cookie |

`KeyEnv` 是 Dola、元宝和 DeepSeek 的凭证入口；Doubao 默认使用配置 Chrome 的现有登录状态，不导出或读取账号 Cookie。
适配器只转发各网页协议所需的白名单 Cookie；Doubao 若显式配置 `KeyEnv`，仅在隔离浏览器 context 中注入它。
`X-COT-Session` 按适配器实例隔离并串行执行，等待时可取消；Doubao 每次请求都是独立 UI turn，要求精确的 UI 模型名。
Dola 和 DeepSeek 的上游分支状态维护尚未完成，因此拒绝该头，只支持
独立单轮；不会把重复使用一个本地 ID 当作上游续聊。DeepSeek 不伪造 WAF Cookie；Doubao 会拒绝 `X-COT-Session` 以及不支持的请求字段。

`base_url` 默认必须使用 HTTPS；仅 loopback 的本地测试地址允许 HTTP。
禁止跳转，响应和请求体都有边界，调用上下文取消会传到上游请求及 SSE
读取。DeepSeek 的本地 PoW 只接受并求解标准
`DeepSeekHashV1` `/api/v0/chat/completion` challenge；不会启动浏览器、
绕过 CAPTCHA、刷新账号、重置账号或声称网页账号当前可用。

## 请求语义与响应

入口协议只接受 `chat`。请求必须是一个单轮、单条、非空的 `role:user`
文本消息；历史消息、`system`/`assistant`/`tool` 等角色、工具调用、图片
或文件内容以及未列明字段均明确返回不支持错误。`reasoning_effort` 不支持。
Dola 使用 `dola-speed` / `dola-thinking` 模式或 `enable_thinking` 布尔值；
不支持 `web_search`。元宝使用上游模型 ID 选择思考模式，仅支持 `web_search`，
不接受 `enable_thinking`。DeepSeek 支持 `enable_thinking` 与 `web_search`，
模型别名映射到网页的 default/expert 模式，不独立保证对应模型版本。

四个网页上游事件都会转换为 OpenAI Chat Completions 响应：文本增量进入
`content`，网页侧明确公开的思考增量进入 `reasoning_content`，流式响应
以 finish chunk 和 `[DONE]` 结束；非流式响应聚合同一事件流后返回 JSON。
如果上游在明确终止事件前结束，适配器返回 `ErrTruncated`，不生成伪造的
成功 EOF。上游非 2xx 响应保留为 HTTP 响应，便于网关保留原始状态码。

## 公开参考与归属

实现只依据公开来源锁定的协议事实重新编写 Go 代码，没有复制来源文件。
下列 commit 是迁移时使用的固定对照点；网页内部接口可能随时变化，当前没有
真实账号生成请求或 live verification 结论。

| 来源 | commit | 对照路径 | 用途 |
| --- | --- | --- | --- |
| [xiaoY233/Chat2API](https://github.com/xiaoY233/Chat2API) | `59f03ab2988867a4d7bacb97d98f3ee018b0e4d0` | provider catalog 与网页 adapter 目录 | 库存命名、凭证和事件形状交叉核对 |
| [diegosouzapw/OmniRoute](https://github.com/diegosouzapw/OmniRoute) | `ba597b631d22d85e56db6982f24b7d1ebe238df9` | `open-sse/config/providers/registry/doubao/web/index.ts`（Dola `doubao-web`），`yuanbao/web/index.ts`，`deepseek/web/index.ts`；对应 `open-sse/executors/doubao-web.ts`、`yuanbao-web.ts`、`deepseek-web.ts` | 精确 runtime provider id、base URL、请求路径和事件协议 |
| [lza6/doubao-2api](https://github.com/lza6/doubao-2api) | `5b80481b9bb5df6cbd8501911e939a9a1d1fb704` | `app/providers/doubao_provider.py`、`app/services/playwright_manager.py`、`app/core/config.py` | 中国站 Cookie、Playwright 签名、设备指纹和 `msToken` 参考；当前未移植浏览器签名运行时 |

OmniRoute 中的 `doubao-web` executor 当前指向 Dola 国际站；它对应本目录
的 `dola-web` #31，不能映射为中国站 `doubao` #43。中国站 #43 的固定来源
是 `lza6/doubao-2api`；其关键路径与签名要求已记录，但本目录不在没有
浏览器签名契约时伪造 `a_bogus` 或宣称中国站网页可用。来源代码、Cookies、
tokens 和浏览器 profile 均不是运行时依赖，不能提交到本仓库。
