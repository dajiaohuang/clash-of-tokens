# 腾讯 ima

Provider / adapter: `tencent-ima`。已实现原生 Go 请求和本地契约测试；**没有真实账号验证**。

```powershell
./dist/clash-tokens.exe init -config ima.local.json -provider tencent-ima -model glm-5.2
$env:COT_TENCENT_IMA_KEY = 'IMA-TOKEN=YOUR_ACCOUNT_TOKEN;IMA-UID=YOUR_ACCOUNT_UID'
./dist/clash-tokens.exe validate -config ima.local.json
```

来源默认禁用。确认账号可用后，在本机配置中启用；`serve -config ima.local.json` 启动该配置。也支持环境变量内容为 `{"cookie":"IMA-TOKEN=..."}`。使用自己的 `x-ima-cookie`，不读取网关调用者的 Cookie 或 Authorization，不包含自动登录、刷新或账号创建。

协议依据：[1icc0/ima2api 固定版本](https://github.com/1icc0/ima2api/tree/3fc4bd7ed07bd26eccb17327661e62f4fe4b612e)。参考仓库该版本没有 LICENSE；本项目根据请求字段独立编写 Go 实现，没有包含其服务代码、工具模拟或配置凭证。

- 基址 `https://ima.qq.com`；先 POST `/cgi-bin/session_logic/init_session`，再 POST `/cgi-bin/assistant/qa`。每个请求新建会话，不自动重试生成。
- 账号 Cookie 和基于 `IMA-TOKEN` 的 UTF-16 DJB2 `x-ima-bkn` 由适配器发送。
- 固定参考中的模型选择器：`hy3-preview`、`hy3-preview-think`、`deepseek-v4-flash`、`deepseek-v4-flash-think`、`glm-5.2`、`glm-5.2-think`。未知选择器拒绝；这些名称是参考协议映射，不是对当前账号额度或实际底层模型的实测保证。
- 仅 OpenAI Chat Completions、一个 user 文本。拒绝工具、图片、system、历史、续聊和采样参数。推理模式仅是上游选择器，不输出推理轨迹。
- 收集正文并确认 `COMPLETED`、非空答案及无后续错误后才输出。仅 CLOSE 或截断均失败；HTTP 401/403/429 保留给网关分类。
- JSON 与 SSE 均为缓冲交付：`X-COT-Delivery: buffered`。无真实 token 计量；JSON `usage: null`。
- 请求正文 1 MiB、单 SSE 事件 1 MiB、总响应 4 MiB；支持请求取消和网关超时，不跟随重定向。

验证：`go test ./internal/providers/chinaapps`。本地模拟覆盖真实路径/字段、凭证隔离、完成标志、截断、上游错误、不支持请求及取消。上线可用性仍需账号 smoke test。
