# 中国网页 Provider（Metaso / Emohaa / Tencent AI Studio / Spark）

本目录对应来源库存 #38、#39、#40 和 #42。Tencent 已注册到内置 provider registry；调用方需配置源并提供
自己的网页凭证。实现状态只按固定参考源码判断，不代表任何网页账号已经
live 验证。

## 来源与状态

| 来源库存 | inventory number | adapter | 状态 |
| --- | ---: | --- | --- |
| 秘塔 AI / Metaso | 38 | `metaso` | 未实现：源码依赖 Puppeteer/CDP 和动态站点 token |
| 聆心智能 / Emohaa | 39 | `emohaa` | 未实现：源码依赖 AI-Role 的 token/XSS 会话协议 |
| 腾讯 AI Studio | 40 | `tencent-aistudio-web`（别名 `tasw`） | 独立窄实现；未 live 验证 |
| 讯飞星火 / Spark | 42 | `spark-web` | 未实现：源码依赖 SSO Cookie、上传流程和固定网页字段 |

Metaso、Emohaa 和 Spark 的 GitHub 链接在审计时已不可访问；对应 Gitee
镜像提供了源码快照。Metaso 的 `chat.ts` 明确启动 Puppeteer，访问
`metaso.cn/search/{conversationId}`，通过 CDP 读取响应流，并从站点脚本
交换动态 token；没有浏览器运行时就不能安全复刻。Emohaa 的源码虽然使用
HTTP，但要求 `ai-role.cn/echo-prod/generate/id`、`/chat`、`/conv`，并为
每次调用生成 `X-Xss-Id`、`X-Xss-Ts` 和 MD5 `X-Xss-Real`；Spark 还要求
`xinghuo.xfyun.cn` 的 SSO Cookie、创建/删除聊天、OSS 签名上传和含
`GtToken` 的 multipart 请求。这些协议都没有在本包中实现，因此
`Supports` 只返回 Tencent，另外三个 adapter 在 `Do` 中返回 `ErrUnsupported`；
没有用猜测的官方接口替代网页协议。

本次审计固定的 Gitee mirror heads 为 Metaso
`53e886172ee0563d50bf7f7277cc45e97d552b04`、Emohaa
`809fe6e83ef3a853a08494fcbb77cf8cafbdf507` 和 Spark
`ec2c4c4042fe096c9ffb98e2aa3d3d724eab488c`；这些镜像仅用于核对协议，
不作为运行时依赖。对应镜像入口是
[`metaso-free-api`](https://gitee.com/llm-red-team/metaso-free-api)、
[`emohaa-free-api`](https://gitee.com/llm-red-team/emohaa-free-api) 和
[`spark-free-api`](https://gitee.com/llm-red-team/spark-free-api)。

## Tencent AI Studio 请求契约

固定参考是 `diegosouzapw/OmniRoute` commit
`ba597b631d22d85e56db6982f24b7d1ebe238df9`，路径为
`open-sse/executors/tencent-aistudio-web.ts`。该 executor 的可复现事实为：

- 默认站点是 `https://aistudio.tencent.ai`。
- 使用 `POST /api/chat/{targetModel}`。
- `hy3-g` 和 `hunyuan-default` 映射为 `HunyuanDefault`；`hunyuan-3d`
  映射为 `Hunyuan3D`。
- JSON 请求体只包含上游模型名和输入 `messages`：
  `{"model":"HunyuanDefault","messages":[...]}`。
- 凭证是用户从 AI Studio 网页会话取得的完整 `Cookie`，不接受调用方
  传入的 Cookie 或 Authorization 作为替代。
- registry 将该 provider 标记为 OpenAI 格式，executor 直接返回上游响应。
  本实现保留非 2xx 状态和错误体；成功的 OpenAI SSE 会在 `stream=false`
  时聚合为 `chat.completion`，成功的 OpenAI JSON 会在 `stream=true` 时
  转换为标准 SSE，从而不忽略调用方的流式选择。

```go
client := chinafinal.New(config.Source{
	Adapter: "tencent-aistudio-web",
	KeyEnv:  "TENCENT_AI_STUDIO_COOKIE",
})
defer client.Close()
response, err := client.Do(ctx, "chat", "hy3-g", true, body, nil)
```

`KeyEnv` 是唯一凭证入口。`base_url` 默认必须是 HTTPS；仅 loopback 的
本地测试服务器允许 HTTP。请求禁用跳转，调用上下文会传递到上游；关闭或
读完返回体会取消派生请求上下文，从而释放延迟的流式响应。适配器使用一个
`providerutil.Gate` 串行化同一 Cookie 的并发生成，并拒绝
`X-COT-Session`，因为 Tencent executor 没有可复用的会话 ID。只接受
`chat`、非空已知模型以及 `system`、`user`、`assistant` 中带非空文本
`role`/`content` 的消息，未知字段、工具、多模态内容和空消息会明确失败；
这类不支持的请求实现 `HTTPStatus() == 422`。

## 来源归属

本包根据上述固定快照重新编写，没有复制来源代码。来源代码、Cookie、
tokens、浏览器 profile 和账号状态均不是仓库运行时依赖；本次实现没有
创建账号、刷新会话、重置配额或声称网页服务当前可用。

响应加固：JSON 必须是单 choice 的有效 completion；SSE 验证真实 finish_reason 与 [DONE]，拒绝错误帧或截断。推理文本和 usage 保留，JSON 转 SSE 的 usage 位于顶层。
