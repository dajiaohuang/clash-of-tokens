# 中国网页 Provider（MiniMax / Mimo / StepChat）

`internal/providers/chinamore` 是三个中国网页或 Agent 产品协议的独立 Go 适配器。它们使用网页产品的私有接口，不是 MiniMax、MiMo 或阶跃星辰的官方开发者 API。适配器已加入内置注册表；运行时仍须显式配置 source 与凭证。注册和契约测试不表示已经 live 验证。

## 来源与运行时映射

| 来源 | inventory number | 运行时 adapter | 默认模型 | 当前状态 |
| --- | ---: | --- | --- | --- |
| MiniMax Agent（Chat2API） | 33 | `minimax-web` | `default`（产品自行选模） | 独立实现，未 live 验证 |
| Xiaomi Mimo AI Studio（Chat2API） | 36 | `mimo` | `MiMo-V2.5-Pro`、`MiMo-V2.5`、`MiMo-V2-Flash` | 独立实现，未 live 验证 |
| 阶跃星辰 StepChat（step-free-api） | 44 | `stepchat` | `default`（产品自行选模） | 独立实现，未 live 验证 |

## 凭证

凭证只从 `Source.KeyEnv` 指定的环境变量读取；调用方的 `Authorization`、`Cookie` 和其他请求头不会被转发。

MiniMax 使用显式 JWT 与用户 ID。环境变量可以是 JSON：

```json
{"token":"JWT_TOKEN","realUserID":"REAL_USER_ID"}
```

也可以使用 `REAL_USER_ID+JWT_TOKEN`。只填写 JWT 时，JWT payload 必须有 `user.id` 或 `user_id`。适配器按网页 Agent 合同先调用 `/v1/api/user/device/register`，再调用 Agent 的发送和轮询接口；这不是注册账号或绕过验证。

Mimo 使用三个网页 Cookie 字段的 JSON：

```json
{"service_token":"SERVICE_TOKEN","user_id":"USER_ID","ph_token":"XIAOMICHATBOT_PH"}
```

StepChat 使用一个用户自己取得的 `Oasis-Token`。适配器用该 token 调用网页产品的 `RegisterDevice` 换取短期 access token；不接受逗号分隔的多账号 token，也不创建账号、不处理 CAPTCHA 或其他挑战。

## 网页端点与请求

| adapter | 默认 `base_url` | 端点 | 传输格式 |
| --- | --- | --- | --- |
| `minimax-web` | `https://agent.minimaxi.com` | `POST /v1/api/user/device/register`、`POST /matrix/api/v1/chat/send_msg`、`POST /matrix/api/v1/chat/get_chat_detail` | JSON；发送后轮询 Agent 对话 |
| `mimo` | `https://aistudio.xiaomimimo.com` | `POST /open-apis/chat/conversation/save`、`POST /open-apis/bot/chat` | JSON + SSE |
| `stepchat` | `https://stepchat.cn` | `POST /passport/proto.api.passport.v1.PassportService/RegisterDevice`、`POST /api/proto.chat.v1.ChatService/CreateChat`、`POST /api/proto.chat.v1.ChatMessageService/SendMessageStream` | JSON + Connect 风格 5 字节长度帧 |

三个 adapter 都只接受 `chat` 协议、一个 `role=user` 的非空字符串消息。`model` 参数由 `Do` 的参数决定，避免请求体中的旧模型名覆盖路由选择。网页协议不能安全保留任意 OpenAI 历史、工具、图片或文件字段，因此这些字段会明确返回 `ErrUnsupported`，不会被静默改写。`web_search`、`reasoning_effort` 与 `enable_thinking` 也不会被伪装成已支持的功能。

三个适配器均拒绝 `X-COT-Session`，每次创建独立产品会话。MiniMax 与 StepChat 没有可验证的模型选择字段，必须使用 `default` 别名，不把参考模块的展示名称当作模型版本保证。StepChat 成功完成后会尽力删除本次创建的临时会话。

流式响应统一转换为 OpenAI Chat Completions SSE，包含 finish chunk 和 `[DONE]`。非流式请求会等待完整上游事件后返回 JSON。调用上下文贯穿 HTTP、轮询、SSE 和长度帧读取；取消请求会结束上游读取并释放会话锁。轮询次数、事件和帧大小均有上限，提前截断的上游响应返回 `ErrTruncated`，不会生成伪造的成功 EOF。

## 固定来源对照

实现重新编写，没有复制来源文件。以下 commit 是当前本地读取的固定对照点，网页私有接口可能变化：

| 来源 | commit | 对照路径 | 用途 |
| --- | --- | --- | --- |
| [xiaoY233/Chat2API](https://github.com/xiaoY233/Chat2API) | `59f03ab2988867a4d7bacb97d98f3ee018b0e4d0` | `src/main/providers/builtin/minimax.ts`、`src/main/proxy/adapters/minimax.ts`、`src/main/providers/builtin/mimo.ts`、`src/main/proxy/adapters/mimo.ts` | MiniMax Agent 签名、设备登记、发送/轮询与 Mimo Cookie、会话和 SSE 字段 |
| [jonnyquan/step-free-api](https://github.com/jonnyquan/step-free-api) | `e50e53f6201c617a68b5979d5ffe0693b15fbeac` | `src/api/controllers/chat.ts`、`src/api/routes/chat.ts` | StepChat `RegisterDevice`、临时会话、Connect 帧、事件结构与 Oasis Cookie |

上述来源只用于协议研究。仓库没有提交网页 Cookie、JWT、Oasis token、浏览器 profile 或 live 账号结果。

