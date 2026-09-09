# Conol 网页入口

适配 ID `conol-web` 已注册，依据 OmniRoute 固定提交 `ba597b631d22d85e56db6982f24b7d1ebe238df9` 中 `open-sse/executors/conol-web.ts` 和 `open-sse/services/conolSessionModel.ts` 独立实现。没有账号实测。

KeyEnv 是完整 Cookie 或用户自己的 `__Secure-better-auth.session_token`。目前接受单条 user 文本 Chat Completions 请求，明确拒绝历史、工具、多模态、采样字段和 `X-COT-Session`。每次创建新会话：`POST /api/sessions` 空 messages → 两次 `/api/sessions/{id}/model` 分别设置 `pro` preset、显式 `agentModel`（`agentEffort:null`）→ `/messages` 提交 → GET `/messages?logDeltas=1`。模型设置必须返回 `ok:true`；不会沿用参考中的失败后默认模型回退。暂不支持 effort 控制或会话继续。

流式响应包含累计阶段日志和预览，最终结果优先使用 assistant 日志，再用预览，最后使用 stream_event 文本。由于最终文本可能替换预览，返回体缓冲到真实 `done`，并设置 `X-COT-Delivery: buffered`。完成后立即关闭上游，不等待其保持连接。没有终止事件、超过限制或错误帧均失败。请求最多 1 MiB，事件 1 MiB，总上游内容 16 MiB；不虚构 token usage。自动创建的会话留在用户的 Conol 账号内。

测试检查空会话创建、preset/model/submission 顺序、Cookie 隔离、最终日志优先级、有效终止以及截断和错误响应。
