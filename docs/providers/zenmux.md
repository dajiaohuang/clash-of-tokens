# ZenMux 网页入口

参考 `diegosouzapw/OmniRoute` 固定提交 `ba597b631d22d85e56db6982f24b7d1ebe238df9` 的 `open-sse/executors/zenmux-free.ts`，独立实现其网页 Cookie 协议。适配 ID 为 `zenmux-web`，已接入配置与路由。未进行账号实测。

`KeyEnv` 指向用户自己的完整 Cookie，必须含 `ctoken`。请求发送到 `POST https://zenmux.ai/api/anthropic/v1/messages?ctoken=...`，使用参考中的订阅来源头、请求 UUID 和 Anthropic 版本头，不读取或转发调用方凭证。默认模型可配置为 `deepseek/deepseek-chat`，其他模型名称原样交由上游检查。

目前接受 Chat Completions 的单条 user 文本，最多 1 MiB 请求、固定 `max_tokens=4096`；历史、system、工具、图像与额外采样字段明确拒绝。参考实现会丢弃历史、把 system 拼接成普通文本；本实现不接受这些无法保留语义的输入。

上游始终流式；转换文本与 `reasoning_content`，保留 `max_tokens` 对应的 `length` 停止原因。必须观察到有效停止原因和 `message_stop`，提前 EOF 与错误帧失败；不估算或虚构 usage。输入、事件与累计输出有界，关闭响应取消上游。非成功状态不向调用方回传网页错误正文。

本地合约测试覆盖 URL/Cookie 隔离、请求体、流式及非流式、推理输出、长度停止、截断和错误消息脱敏。
