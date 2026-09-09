# 微信读书 AI

Provider / adapter: `weread-ai`。已实现原生 Go 轮询和本地契约测试；**没有真实账号验证**。能力是围绕指定书籍提问，必须设置 `project` 为自己账号可访问的 book ID。

```powershell
./dist/clash-tokens.exe init -config weread.local.json -provider weread-ai -model web -project YOUR_BOOK_ID
$env:COT_WEREAD_AI_KEY = '{"vid":"YOUR_READER_ID","access_token":"YOUR_MOBILE_ACCESS_TOKEN"}'
./dist/clash-tokens.exe validate -config weread.local.json
```

来源默认禁用。确认账号可用后，在本机配置中启用；`serve -config weread.local.json` 启动该配置。凭证为自己的移动端 `vid` 和 access token；网页 Cookie 不可直接替代。不包含登录、刷新、购书或设备注册。

协议依据：[teng-lin/weread-omni 固定版本](https://github.com/teng-lin/weread-omni/tree/3a7fee58995ef502f2044d93f11af7b76fdacd0e)，主要参照 `src/api/resources/ai.ts`、`src/profile.ts` 和设备请求头定义。独立 Go 实现；参考项目 [MIT 许可](../licenses/weread-omni-MIT.txt)。

- POST `https://i.weread.qq.com/ai/chatv2`，传递 `bookId`、`query`、`scene: 1`、`accept_text_type: 1`；后续轮询携带响应中的 `chatid` / `session_id`。
- 仅 `web` 选择器，不声明底层模型身份。仅 OpenAI Chat Completions、一个 user 文本；拒绝 history、system、工具、图片、续聊和采样参数。
- `result.text` 是累计快照，替换上次非空文本；终止帧没有文本时保留最后答案。`result.has_more: 0`，或本帧非空正文且 `extra_sections.has_more: false`，才是完成。
- 缺失续轮询 ID、ID 变化、错误码、空答案或轮询耗尽都失败，不把部分答案包装成成功。
- 最多 80 次轮询，间隔 50–1500 ms；单响应 4 MiB，累计响应 16 MiB。取消会中止等待；网关超时可能更早生效。不自动重试生成，不跟随重定向。
- HTTP 401/403/429 保留供网关阻断/冷却。凭证仅使用来源环境变量，调用者 Cookie 和 Authorization 不转发。
- JSON / SSE 均缓冲完成后交付，标记 `X-COT-Delivery: buffered`；不提供真实 token 用量或书籍引用结构。

验证：`go test ./internal/providers/chinaapps`。本地模拟覆盖累计文本去重、终止帧保留正文、不能提前完成的空 sections 帧、续轮询 ID、请求头隔离、拒绝不支持请求、取消和 429。仍需自己的账号与书籍进行真实验证。
