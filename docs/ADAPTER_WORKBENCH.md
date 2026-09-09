# 适配器工作台：第一批设备样板

当前已落地的流程是“固定参考版本 → 项目内 Go 驱动 → 离线契约测试 → 独立真机探针”。FreeCoding 的三个 App 是第一批设备样板。

| 层 | 可执行资产 | 当前状态 |
|---|---|---|
| 来源身份与证据 | `catalog/evidence/freecoding.json` | 三个 App 的入口、固定 commit、许可、测试和具体阻塞 |
| 共用设备机制 | `internal/drivers/device` | 有界 ADB、XML、Windows 剪贴板、原生 OCR 命令、分片、设备租约 |
| 来源差异 | `internal/providers/appdevice` | 小团、灵宝、小火人各自的 Go 页面状态机 |
| 离线验收 | `cmd/cot-verify` | 检查证据与目录一致性；运行固定范围测试，不执行证据中的任意命令 |
| 授权实测 | `cmd/cot-device-verify` | 显式 `-live`，一次真实提问，核对随机测试字符串，输出摘要与哈希 |
| 本机预检 | `clash-tokens device-doctor` | 只读检查，不发消息、不自动登录 |

```powershell
go run ./cmd/cot-verify -scope freecoding -run-tests
```

报告分别展示目录条目数、是否执行离线契约、真实验证数和当前运行就绪数。当前三项均为离线通过、真实接入未验证，运行阻塞于设备缺失。测试夹具、合成 OCR 图片和参考 README 均不能提升 `live_verified`。

`cot-verify` 当前只实现固定的 FreeCoding 范围，不是任意来源配置执行器。真实探针结果仍需人工核验后接入证据记录；当前工具会拒绝直接将证据标签改为 `live_passed`。不会自动下载或执行参考仓库代码。

开发分层采用用户方案中的四条路径：声明式 HTTP、专用 Go、浏览器会话、设备会话。本批只补设备执行和验收样板，不把其他三条路径重写。现有来源继续运行。

后续顺序：先完成这三个 App 的真机校准与验收；再用同一证据结构推进剩余候选。HTTP 配方编译器、受控录制/脱敏工具和通用故障包仍需后续实现，此文不将它们标为已交付。

设备依赖、手动会话限制及详细测试见 [设备 provider](providers/app-device.md)；最新中国 App 剩余项见 [候选清单](CHINA_APP_CANDIDATES.md)。
