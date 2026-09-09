# BLACKBOX AI 网页入口

已注册 `blackbox`。协议依据 OmniRoute 固定提交 `ba597b631d22d85e56db6982f24b7d1ebe238df9` 的 `open-sse/executors/blackbox-web.ts`，独立实现，没有账号实测。

KeyEnv 提供用户自己的 JSON：`{"cookie":"完整 Cookie","validated":"网页当前 tk 校验值"}`。可额外提供 `user_id`。不使用参考实现的随机校验值或默认 Premium 假设。每次依序读取 `/api/auth/session` 与 `/api/check-subscription`，从真实响应建立会话与订阅字段，再向 `/api/chat` 发送请求。缺少可验证会话或订阅字段会失败。

当前仅支持 Chat Completions 单条 user 文本，模型原样写入 `userSelectedModel`，输出上限固定 1024 tokens。system、历史、工具和额外请求字段明确拒绝。上游为原始文本、正常 HTTP EOF 结束；意外断流不会标记完成。最多缓冲 16 MiB，保留文本空白，流式客户端会收到 `X-COT-Delivery: buffered`，不宣称逐 token 上游推送。不估算 usage。

HTTP 错误及参考源码明确列出的登录、订阅、限流正文模式返回通用错误，不回传账号详情或错误正文。该正文识别沿用来源启发式，可能拒绝恰好包含这些提示语的正常回答。Cookie 仅发送到配置源，拒绝跳转。测试覆盖账号字段来源、请求体、凭证隔离、流式/非流式和缺少校验值时禁止网络请求。
