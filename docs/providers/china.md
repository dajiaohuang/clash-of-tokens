# 中国网页 Provider 迁移记录

本目录的实现位于 [`internal/providers/china`](../../internal/providers/china)。它只提供独立的 `New(config.Source) *Client`、`Client.Do` 和 `Close` 契约，主线 `internal/upstream` 不会因为本目录存在而自动注册这些网页适配器。

## 能力与配置

| `source.adapter` | 网页端点（`base_url`） | 网页协议 | 凭证 | 当前能力 |
| --- | --- | --- | --- | --- |
| `kimi-web` | `https://www.kimi.com` | Connect JSON/gRPC-Web `POST /apiv2/kimi.gateway.chat.v1.ChatService/Chat` | `KeyEnv` 中的 `kimi-auth` JWT/refresh 值 | 文本 chat，流式/非流式，单轮；显式会话可连续对话 |
| `qwen-web-intl` | `https://chat.qwen.ai` | `POST /api/v2/chats/new`，随后 `POST /api/v2/chat/completions?chat_id=...` SSE | `KeyEnv` 中的 Local Storage `token` JWT | 文本 chat，流式/非流式，单轮；显式会话可连续对话 |
| `glm-web` | `https://chatglm.cn/chatglm` | `POST /user-api/user/refresh`，随后 `POST /backend-api/assistant/stream` SSE | `KeyEnv` 中的 `chatglm_refresh_token` | 文本 chat，流式/非流式，单轮；显式会话可连续对话，refresh token 内存缓存 |
| `zai-web` | `https://chat.z.ai` | `POST /api/v1/chats/new`，随后签名的 `POST /api/v2/chat/completions` SSE | `KeyEnv` 中的 JWT | 文本 chat，流式/非流式，单轮；显式会话可连续对话 |

四个适配器都只从 `Source.KeyEnv` 读取凭证，不读取机器上其他环境变量、浏览器 profile、Cookie 存储或账号文件。入站 Cookie 和内部会话头不会转发到网页端。每个 `Client` 自己维护按 `X-COT-Session` 隔离的 chat/conversation id；没有该头时请求是一次性会话。

入站 `chat` 请求必须包含且只包含一条 `role:user` 文本消息。`system`、`assistant`、`tool`、`function` 等角色、多条历史消息、工具/函数字段、`temperature`、`tool_choice` 和其他未列明字段都会显式返回不支持错误。这样不会把 system 指令降格为 user 文本，也不会在迁移层只取最后一条 user 而静默丢弃历史；需要连续对话时，逐次发送单条当前 user 消息并使用独立的 `X-COT-Session` 值。

当前明确不支持工具调用和图片/文件输入，调用会返回带 `unsupported protocol` 语义的错误，避免把内容静默丢弃。`responses`、`messages`、Gemini 和其他协议同样明确失败。测试 fixture 只使用 `httptest`，没有真实生成请求。

## 主线接入字段

主线注册时需在 `config.Validate` / `config.Supports` 增加以上四个 adapter，并为每个 source 配置：

```json
{
  "id": "kimi-web-main",
  "provider": "kimi",
  "adapter": "kimi-web",
  "base_url": "https://www.kimi.com",
  "key_env": "COT_KIMI_WEB_KEY",
  "enabled": false,
  "auto_approved": false,
  "local": false,
  "paid": false,
  "max_inflight": 1,
  "quota_domain": "kimi-web-account",
  "quota_max_inflight": 1,
  "models": [{
    "id": "kimi-k2.6",
    "upstream": "kimi-k2.6",
    "protocols": ["chat"],
    "tier": "unrated",
    "tools": "none",
    "vision": false,
    "max_input_bytes": 1048576
  }]
}
```

其他三个 adapter 沿用相同字段；建议的网页模型映射是 `qwen3.7-max`、`glm-5.1` 和 `GLM-5.1`。网页接口是未公开、易变化的内部协议，建议主线默认 `enabled:false`、`auto_approved:false`，在单独审计后再启用。网页账户并发和 quota 均应保持 1。

## Z.ai 风控边界

Z.ai 对网页对话可能返回 `FRONTEND_CAPTCHA_REQUIRED`。实现保留网页请求中的签名和模型/会话字段，但不生成、绕过或重放 `captcha_verify_param`，也不启动浏览器或验证码服务；因此收到该响应时会原样保留其 HTTP 状态，配置存在不代表对话可用。需要验证码的能力在主线注册前应继续标为阻塞。

## 公开参考与归属

本实现依据以下公开仓库在迁移时锁定的 commit 对照网页请求路径、字段和事件格式。实现是独立 Go 代码，没有复制其 TypeScript/Python 文件；若后续复制受版权保护的实现，须重新进行许可证审查。

| 来源 | commit | 对照路径 | 许可证 |
| --- | --- | --- | --- |
| [xiaoY233/Chat2API](https://github.com/xiaoY233/Chat2API) | `59f03ab2988867a4d7bacb97d98f3ee018b0e4d0` | `src/main/proxy/adapters/kimi.ts`, `qwen-ai.ts`, `glm.ts`, `zai.ts`, `src/main/proxy/adapters/providerModelOptions.ts` | GPL-3.0-or-later |
| [lza6/kimi-ai-2api](https://github.com/lza6/kimi-ai-2api) | `351c076fed308ebe8ddfc9d4918e64c32ad3c4e0` | `app/providers/kimi_ai_provider.py` | Apache-2.0 |
| [lza6/Qwen-2api](https://github.com/lza6/Qwen-2api) | `4f793ac52a3e9e22fc9af17795aba492d5be2723` | `app/providers/text_provider.py` | Apache-2.0 |
| [lza6/zai.is-2api-python](https://github.com/lza6/zai.is-2api-python) | `b87c0fed70af80d662d564d77996c28d67a62b87` | `app/providers/zai_provider.py` | Apache-2.0 |

Chat2API 的文档明确记录 Qwen 国际版的 JWT 凭证、GLM refresh token、Z.ai 前端验证码限制；这些限制已反映到本适配器的显式 KeyEnv 约束与 Z.ai 阻塞说明中。
