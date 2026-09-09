# 原生 Go 设备来源（FreeCoding 移植）

三个 provider 的页面工作流已移植到项目内：

| Provider | 模型选择器 | Android 包名 | 回复路径 |
|---|---|---|---|
| `meituan-xiaotuan` | `meituan_xiaotuan` | `com.sankuai.meituan` | 当前问题之后的完成/复制控件，剪贴板或控件文本 |
| `wangzhe-lingbao` | `wangzhe_lingbao` | `com.tencent.tmgp.sgame` | 3200×1440 横屏，分片 OCR 与稳定帧 |
| `douyin-xiaohuoren` | `douyin_xiaohuoren` | `my.maya.android` | FreeCoding 对应的多闪入口；真实提及、左侧气泡或完整卡片复制 |

**移植代码与离线测试已完成，三项都尚未完成本机真机验收。** 本机安装完成时 ADB 的设备列表为空。参考项目的真机结果不算本项目的结果。

固定参考：[Damue01/FreeCoding @ f0ae520](https://github.com/Damue01/FreeCoding/tree/f0ae52075e584bd55989ec4053b5ff61c09e422f)。Go 工作流、控件标识和校准坐标根据 `app2api/drivers/vivo_adb.py`、`workflows.py`、`target_configs/*.json` 移植，保留 [MIT 许可](../licenses/FreeCoding-MIT.txt)。

## 运行构成

网关、调度、Provider、ADB 调用、XML 解析、Windows 剪贴板、图片裁切/放大、OCR 分片合并和回复转换都在 Go 中。不运行 FreeCoding、FastAPI、Python、PaddleOCR 或另一个反代服务。

显式外部环境依赖：Windows、官方 Android Platform-Tools、用户授权手机、vivo 办公套件的投屏/剪贴板同步、原生 Tesseract 可执行文件及 `chi_sim` 识别数据。目标 App 必须已登录并由用户打开正确聊天页。ADB 不是推理服务，手机 App 的模型仍在云端，因此来源不标记为 `local` 推理。

本次部署安装了 Platform-Tools 37.0.1、Tesseract 5.4.0.20240606、vivo 办公套件 6.8.2。中文数据来自 [tessdata_fast 固定版本](https://github.com/tesseract-ocr/tessdata_fast/tree/87416418657359cb625c412a48b6e1d6d41c29bd)，`chi_sim.traineddata` SHA-256 为 `a5fcb6f0db1e1d6d8522f39db4e848f05984669172e584e8d76b6b3141e1f730`。中文合成图识别通过；这不是 App 截图实测。

## 配置

```powershell
./dist/clash-tokens.exe init -config device.local.json -provider meituan-xiaotuan -model meituan_xiaotuan -project current-app-session
```

填写该配置中的 `device`（示意路径必须替换为本机实际值）：

```json
{
  "enabled": true,
  "adb_path": "C:/Android/platform-tools/adb.exe",
  "serial": "YOUR_AUTHORIZED_DEVICE_SERIAL",
  "state_dir": "D:/clash-tokens-local/device-state",
  "ocr_path": "C:/Program Files/Tesseract-OCR/tesseract.exe",
  "ocr_data_dir": "C:/Users/YOUR_USER/AppData/Local/ClashOfTokens/tessdata",
  "clipboard_sync_ms": 2500
}
```

这是 `device` 字段的值，不是完整网关配置。来源默认禁用；环境准备好后将所需 source 的 `enabled` 设为 true。三个来源都必须使用 `quota_domain: android-device`、账号和共享容量 1、`auto_approved: false`。一个手机不能因有三个 App 而获得三份执行容量。`project: current-app-session` 是明确接受手机当前会话的配置标记。

```powershell
./dist/clash-tokens.exe validate -config device.local.json
./dist/clash-tokens.exe device-doctor -config device.local.json
```

`device-doctor` 只检查本地文件和 Android 连接/安装情况，不发送消息、不修改剪贴板。剪贴板同步及实际会话仍需操作验收，因此仅安装通过不会自动报告可调度。

## 支持范围与差异

- 仅 Chat Completions、一个 user 文本、最多 4000 字符。拒绝 API 历史、system、工具、图片、采样控制和 `X-COT-Session`。模型选择器是产品入口名，不是底层模型身份。
- 复用手机当前聊天，不创建独立 Agent 房间，不宣称会话隔离。默认只接受手动选择；不能批准进入 Auto 池。小火人须由用户选定对应聊天，代码不猜群聊、不切换聊天对象。
- Unicode 通过 Windows 剪贴板与 vivo 同步输入，完整验证输入后才发送。原问题不放入 ADB shell 命令。剪贴板原有 Unicode 文本会恢复；非文本剪贴板会阻止输入，避免静默破坏。
- 小团必须从正常入口打开已初始化对话页。此移植不自动 force-stop App 或直接启动内部 Activity。必须看见当前问题锚点；不会在问题滚出屏幕后盲目复制旧回复。
- 灵宝主路径使用临时原生 EditText，保留长按粘贴备用路径。固定点击前验证分辨率、页面标记和发送按钮位置。OCR 引擎改为本地 Tesseract；该版本校准仍需真机验证。分片合并只做确定的文字重叠，不使用参考中的模糊字符替换。
- 小火人先输入 `@`，点击真实“小火人”候选形成提及，再粘贴正文。卡片分支验证详情 Activity、有限滚动、复制新内容，确认后才返回。当前不宣称覆盖其他抖音包名或不同分辨率。
- 灵宝与普通小火人气泡用新文本稳定帧观察完成，**不是上游原生结束事件**。返回 `X-COT-Completion: observed-stability`；其他路径为 `ui-marker`。`X-COT-Extraction` 说明来源，`X-COT-Context: current-app-session` 说明上下文。
- JSON/SSE 均在观察到完整结果后缓冲交付，`X-COT-Delivery: buffered`，`usage: null`。不捏造 token 用量。

## 取消、故障与人工复核

ADB 命令最长 20 秒，OCR 最长 30 秒，整体最长 4 分钟；UI XML、命令输出、截图和 OCR 输出均有大小上限。不会自动重复发送。

手机开始生成后，取消网关请求只能停止本机观察，不能保证手机停止生成。任何设备操作开始后的失败/取消都会保留设备状态目录中的 `.lock`，阻止下一次自动操作。先检查手机是否生成中、输入框是否残留、卡片是否仍打开，并确认没有其他网关使用此设备；再手动删除该设备对应的锁文件。不要在任务运行时删除锁。正常完成会自动释放。

当前设备驱动没有自动登录、账号创建、支付、下单、好友选择或业务按钮流程；它也不替用户操作手机 USB 授权。保持实验来源手动使用。

## 可复现验收

```powershell
go test ./internal/drivers/device ./internal/providers/appdevice ./catalog
go vet ./internal/drivers/device ./internal/providers/appdevice
go test -race ./internal/drivers/device ./internal/providers/appdevice ./catalog
```

离线测试覆盖三个完整 Go 工作流（小火人另含卡片分支）、真实提及创建、输入验证、只发送一次、剪贴板恢复、旧问题/截断回复拒绝、锁与取消、XML/OCR 解析和共享容量约束。夹具是合成设备状态，不能替代真机测试。

手机就绪、对应 App 聊天页打开、source 启用后，逐个执行：

```powershell
go run ./cmd/cot-device-verify -config device.local.json -source meituan-xiaotuan -live
```

不传 `-live` 只做诊断。实测每次发一个随机测试字符串，检查回答是否包含它，输出不含回答正文的 JSON 结果及哈希。三个 App 需要分别手动切到相应页面后执行。只有这些实际结果通过，才更新证据包里的真实接入状态。
