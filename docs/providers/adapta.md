# Adapta One

`adapta-web` 已注册，依据 OmniRoute 固定提交 `ba597b631d22d85e56db6982f24b7d1ebe238df9` 的 `open-sse/executors/adapta-web.ts` 独立实现。未进行账号实测。

KeyEnv 是用户已有的 Clerk `__client` Cookie 值，可包含 `__client=` 前缀。适配器向 `clerk.agent.adapta.one/v1/client` 查询 active session，再向 `/v1/client/sessions/{id}/tokens` 换取短期 JWT；仅在请求时换取。按完整凭证的 SHA-256 隔离一个有界缓存，提前 30 秒失效，并序列化并发交换。JWT 的 exp 仅用于缓存期限，签名与权限仍由上游验证。Cookie 不会发给聊天域名；聊天请求只使用换得的 bearer。

聊天端点为 `POST https://agent.adapta.one/api/chat/stream/v1`，请求包含文本 parts 和 `aiModelId:14`。来源中的 gpt/claude/gemini 等别名全部指向同一个 14，故本项目只接受 `adapta-one` 或 `default`，它们表示产品自动模式，不保证具体底层模型。当前单条 user 文本，额外字段、历史、system、工具及多模态明确拒绝。

SSE 只输出真实 `text-delta`，跳过 `quick-response` 加载占位；error 帧失败，不作为成功正文。要求 done/end 或 [DONE] 才完成，提前 EOF 失败。使用有界流转换，非流请求聚合文本；不虚构 usage。认证失败清除对应缓存供下一次请求重新交换，本次请求不会静默重复生成。

测试检查认证域/聊天域凭证隔离、认证路由、缓存复用、模型限制、占位过滤、流式和非流式输出。
