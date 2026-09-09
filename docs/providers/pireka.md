# Pi 与 Reka 网页入口

已注册 `pi`、`reka-web`。固定参考为 gpt4free 提交 `502b0e2e8d82292a39d2c3d9da07125322870583` 的 `g4f/Provider/needs_auth/Pi.py`、`Reka.py`，独立实现其 HTTP 文本协议。**没有账号实测；Reka 的参考快照明确设置 `working=False`**，注册不意味着当前上游可用。

Pi 使用 KeyEnv 中已有的完整 Cookie，先 `POST https://pi.ai/api/chat/start`（`x-api-version:3`、`{}`），取得新 `conversations[0].sid`；再 POST `/api/chat`，发送 `text`、`conversation`、`mode:BASE`。独立 Cookie jar 保存本次 start 返回的 Cookie，不与其他账号或请求共享。当前不提供参考中的浏览器 Cookie 引导；如果上游需要新挑战，需用户更新自己的凭证。模型只允许 `pi` 或 `default`。

Reka 使用 KeyEnv 中已有的 bearer token，POST `https://space.reka.ai/api/chat`，发送 human 类型 `conversation_history`、`stream:true`、禁用搜索与代码解释器、`model_name:reka-core` 和时间种子。模型只允许 `reka-core` 或 `default`。不获取浏览器凭证或上传图片。

两者当前只接受一条 user 文本，不接受历史、system、工具、采样字段或 `X-COT-Session`。Pi 的 text 为增量，Reka 的 text 为累计内容，后者必须延续已输出前缀；文本修订明确报错。参考未定义应用层终止标记，因此正常 HTTP EOF 表示完成；网络异常 EOF 仍失败。每行最多 1 MiB，上游累计最多 16 MiB，转换输出另有限制。不虚构 usage。关闭返回体关闭上游。

本地测试覆盖 Pi 新会话与 Cookie 传递、Reka 累计文本、认证隔离和网络截断处理。
