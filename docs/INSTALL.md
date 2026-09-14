# Build and install

Supports Windows, Linux and macOS on amd64/x86-64 and arm64. Run the command for your operating system from a checkout. No administrator access is needed for the default user installation.

## One-command source build and installation

Windows (PowerShell 5.1+):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\build.ps1 -Install
```

Linux / macOS (Bash):

```bash
bash build.sh --install
```

These commands download and verify the `go.mod` toolchain when the required Go version is absent, verify Go modules, build the native gateway, check its version, generate its SHA-256 file and install it. Go archives are verified against `scripts/go-toolchains.txt`, sourced from the [official Go download API](https://go.dev/dl/?mode=json&include=all). No Python or GitHub CLI is needed. Git is needed only to obtain a checkout; GitHub's source archive also works.

For a fresh checkout:

```text
git clone https://github.com/dajiaohuang/clash-of-tokens.git
cd clash-of-tokens
```

On Unix, curl, tar and sha256sum or shasum must be available. On macOS, run `xcode-select --install` once if Command Line Tools are absent. The build intentionally enables cgo on macOS for native Keychain. It does not silently produce a Keychain-disabled binary. Linux vault usage requires an unlocked desktop Secret Service (for example GNOME Keyring) and a session D-Bus; a headless machine needs that session configured separately.

Build only: omit `-Install` / `--install`. The output is `dist/clash-tokens.exe` on Windows or `dist/clash-tokens` on Unix. Add `-Test` / `--test` to run the full Go suite before building. Unix unit tests use the isolated test keyring; the **built binary still uses native storage**.

```powershell
.\build.ps1 -Install -Test -InstallDir 'D:\Tools\ClashOfTokens' -Output '.\dist\clash-tokens.exe'
```

```bash
bash build.sh --install --test --install-dir "$HOME/.local/bin" --output ./dist/clash-tokens
```

`install.ps1 -FromSource` and `bash install.sh --from-source` are equivalent source-install entry points. They require the complete checkout containing the matching build script.

## One-command Release installation

From a checkout, no Go compiler is required:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

```bash
bash install.sh
```

Without a checkout, download the installer and run it:

```powershell
$script = Join-Path $env:TEMP ('cot-install-' + [guid]::NewGuid().ToString('N') + '.ps1'); Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/dajiaohuang/clash-of-tokens/main/install.ps1 -OutFile $script; & powershell -NoProfile -ExecutionPolicy Bypass -File $script
```

```bash
script=$(mktemp) && curl --fail --location --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/dajiaohuang/clash-of-tokens/main/install.sh -o "$script" && bash "$script"
```

The downloaded script is left available for inspection. Downloading and inspecting it before execution is also supported. These remote commands require these scripts to be merged into `main`. At implementation time on 2026-09-14, the repository had **no published releases**; until a compatible Release is published, use source installation. CI workflow artifacts are not GitHub Release assets.

The default selects the latest stable Release once and downloads the binary and matching checksum from that same tag. Specify a tag for a prerelease or reproducible installation:

```powershell
.\install.ps1 -Version v0.1.0 -InstallDir 'D:\Tools\ClashOfTokens'
```

```bash
bash install.sh --version v0.1.0 --install-dir "$HOME/.local/bin"
```

`v0.1.0` is an example tag, not an assertion that it exists. For a fork, set `-Repository OWNER/REPO` / `--repository OWNER/REPO`; it must use the same asset contract. Upgrading or downgrading uses the same command with the desired tag. A missing Release, missing checksum, wrong hash or non-runnable binary fails before replacing the existing installation.

## Locations and runtime

| Item | Windows | Linux / macOS |
|---|---|---|
| Default executable | `%LOCALAPPDATA%\ClashOfTokens\bin\clash-tokens.exe` | `~/.local/bin/clash-tokens` |
| Downloaded Go toolchains | `%LOCALAPPDATA%\ClashOfTokens\toolchains` | `${XDG_CACHE_HOME:-~/.cache}/clash-of-tokens/toolchains` |
| PATH | Adds the install directory to user PATH; open a new terminal | Prints an `export PATH=...` command if needed; does not edit shell profiles |

Windows `-NoPath` skips PATH changes. Build scripts restore their process environment after completion. Toolchains are retained and not removed during upgrades. Neither installer changes configuration, stored credentials, browser profiles, services or startup settings. Run installation after stopping a running Windows gateway if Windows reports a locked executable; the previous binary remains intact on failure.

After installation, from a directory where you want your configuration:

```text
clash-tokens version
clash-tokens init -config config.json
clash-tokens serve -config config.json
```

`init` refuses to overwrite an existing configuration. Running `serve` is explicit; installation never starts a gateway automatically. Open the address printed by `serve` and continue configuring sources. For an installation outside PATH, use the full executable path printed by the installer.

To install a previously downloaded artifact offline, pass its exact SHA-256 from the Release checksum file:

```powershell
.\install.ps1 -Binary .\clash-tokens-windows-amd64.exe -Sha256 YOUR_64_CHARACTER_SHA256
```

```bash
bash install.sh --binary ./clash-tokens-linux-amd64 --sha256 YOUR_64_CHARACTER_SHA256
```

SHA-256 verifies that the artifact matches the published checksum; it is not a publisher signature or macOS notarization.

## Release production and tests

`.github/workflows/installers.yml` builds and installs all six native OS/architecture combinations, runs installation rejection/upgrade tests and tests the **exact binary's native vault lifecycle**. macOS builds retain cgo. Runner labels come from the [GitHub runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners). Windows ARM and macOS execution are configured in CI, not claimed as locally verified on an amd64 Windows machine.

Pushing a `v*` tag runs this pipeline. Only after all six jobs pass does it assemble assets and create a **draft Release** for review; publishing the draft makes it available to installers. No tag or Release is created by running the local build/install scripts. Assets use this contract:

```text
clash-tokens-windows-amd64.exe     clash-tokens-windows-arm64.exe
clash-tokens-linux-amd64           clash-tokens-linux-arm64
clash-tokens-darwin-amd64          clash-tokens-darwin-arm64
```

Each binary has a `.sha256` companion containing `HASH  EXACT_FILENAME`. Per-platform module/license metadata and both standalone installers are included. The exact tag is embedded in `clash-tokens version`. Draft creation does not assert that the broader [next-release acceptance gates](NEXT_RELEASE_STATUS.md) are complete.

Local regression command: `python scripts/test_installers.py PATH_TO_NATIVE_BINARY`. It installs the actual binary in a temporary directory and covers replacement, paths with spaces, mandatory checksums, corruption, missing releases, latest/pinned download resolution, configuration preservation and staging cleanup. Download fixtures simulate GitHub responses; they are not proof of a published Release download.

# 中文：构建与安装

支持 Windows、Linux、macOS 的 amd64/x86-64 与 arm64。默认安装到用户目录，无需管理员权限。

## 源码一键构建安装

先取得源码：

```text
git clone https://github.com/dajiaohuang/clash-of-tokens.git
cd clash-of-tokens
```

Windows PowerShell 5.1+：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\build.ps1 -Install
```

Linux / macOS：

```bash
bash build.sh --install
```

脚本自动取得 `go.mod` 指定的 Go 版本，校验官方 SHA-256、校验依赖、构建本机程序、试运行版本命令、生成校验文件并安装。不需要 Python、GitHub CLI；Git 仅用于获取源码，也可使用 GitHub 源码压缩包。Go 校验值来自[官方下载 API](https://go.dev/dl/?mode=json&include=all)，固定保存在 `scripts/go-toolchains.txt`。

Unix 需要 curl、tar，以及 sha256sum 或 shasum。macOS 缺少编译工具时先运行一次 `xcode-select --install`，构建强制开启 cgo 保留 Keychain。Linux 使用 vault 需要已解锁的 Secret Service 与会话 D-Bus；无桌面服务器需单独配置会话。

只构建时去掉 `-Install` / `--install`，产物分别为 `dist/clash-tokens.exe` 与 `dist/clash-tokens`。增加 `-Test` / `--test` 可在构建前运行完整 Go 测试。Unix 单元测试使用隔离 keyring，最终二进制仍使用原生存储。

```powershell
.\build.ps1 -Install -Test -InstallDir 'D:\Tools\ClashOfTokens' -Output '.\dist\clash-tokens.exe'
```

```bash
bash build.sh --install --test --install-dir "$HOME/.local/bin" --output ./dist/clash-tokens
```

完整源码目录内也可运行 `install.ps1 -FromSource` 或 `bash install.sh --from-source`。

## Release 一键安装

源码目录内直接运行，不需要 Go：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

```bash
bash install.sh
```

无需克隆仓库的下载执行命令：

```powershell
$script = Join-Path $env:TEMP ('cot-install-' + [guid]::NewGuid().ToString('N') + '.ps1'); Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/dajiaohuang/clash-of-tokens/main/install.ps1 -OutFile $script; & powershell -NoProfile -ExecutionPolicy Bypass -File $script
```

```bash
script=$(mktemp) && curl --fail --location --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/dajiaohuang/clash-of-tokens/main/install.sh -o "$script" && bash "$script"
```

下载的脚本保留以便检查，也可先检查后执行。远程命令需要这些文件已合入 main。**2026-09-14 实现时仓库尚无已发布 Release**；首次兼容 Release 发布前请使用源码安装。Actions artifact 与 GitHub Release 附件不是同一种产物。

默认只解析一次最新稳定版，程序和校验文件来自同一标签。指定版本可安装预发布版或固定版本：

```powershell
.\install.ps1 -Version v0.1.0 -InstallDir 'D:\Tools\ClashOfTokens'
```

```bash
bash install.sh --version v0.1.0 --install-dir "$HOME/.local/bin"
```

`v0.1.0` 仅为示例，不代表标签已经存在。Fork 使用 `-Repository OWNER/REPO` / `--repository OWNER/REPO`，需遵守同一产物命名规则。升级、降级都用同一命令指定目标版本。Release 不存在、缺少校验文件、哈希错误或程序不能执行时，不替换旧程序。

## 安装位置与运行

Windows 默认位置为 `%LOCALAPPDATA%\ClashOfTokens\bin\clash-tokens.exe`，自动加入用户 PATH；新开终端生效。`-NoPath` 可跳过 PATH 修改。Linux/macOS 默认为 `~/.local/bin/clash-tokens`；不在 PATH 时打印 export 命令，不修改 shell 配置文件。

Go 缓存在 Windows 的 `%LOCALAPPDATA%\ClashOfTokens\toolchains` 或 Unix 的 `${XDG_CACHE_HOME:-~/.cache}/clash-of-tokens/toolchains`，升级时保留。构建脚本恢复自己修改的环境变量。安装不更改配置、凭证、浏览器 Profile、服务和开机启动设置。Windows 正在运行的程序若被锁定，先停止网关再安装，失败时保留旧程序。

安装后，在准备保存配置的目录运行：

```text
clash-tokens version
clash-tokens init -config config.json
clash-tokens serve -config config.json
```

`init` 不覆盖已有配置；安装不会自动启动服务。访问 `serve` 打印的地址继续配置来源。未加入 PATH 时，使用安装器打印的完整路径。

离线安装已下载的产物时，必须传入 Release 校验文件中的完整 SHA-256：

```powershell
.\install.ps1 -Binary .\clash-tokens-windows-amd64.exe -Sha256 YOUR_64_CHARACTER_SHA256
```

```bash
bash install.sh --binary ./clash-tokens-linux-amd64 --sha256 YOUR_64_CHARACTER_SHA256
```

SHA-256 只证明程序与校验文件匹配，不等于发布者签名或 macOS 公证。

## 打包发布与验证

`.github/workflows/installers.yml` 在六个原生系统/架构组合构建、安装，执行升级与拒绝路径回归，并验证实际发布二进制的原生 vault 生命周期。macOS 开启 cgo。推送 `v*` 标签后，六个任务全部通过才创建供检查的 **Release 草稿**；手动发布草稿后安装器才能下载。运行本地脚本不会推送标签或创建 Release。

六个产物名称及 `.sha256` 格式与上方英文表一致，每个平台附带依赖/许可元数据，另外附带两个独立安装器。`version` 显示构建标签。生成草稿不代表更广泛的下一版发布验收全部完成。Windows ARM、macOS 等平台已配置 CI，但不冒充 Windows amd64 本机验证结果。

本地测试命令为 `python scripts/test_installers.py 本机二进制路径`，覆盖真实程序安装、重复替换、空格路径、强制校验、损坏附件、缺失 Release、latest/固定版本解析、配置保留及临时文件清理。下载使用 GitHub 响应夹具，不代表已完成真实公开 Release 下载。
