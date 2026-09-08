# Windows 客户端构建与发布

仓库提供一条完整的 Windows 客户端发布入口：

```powershell
# 可从仓库根目录或任意当前目录执行
powershell -NoProfile -ExecutionPolicy Bypass -File G:\matchSystem\scripts\build-client.ps1

# 也可以在 cmd 中使用 wrapper
G:\matchSystem\scripts\build-client.bat
```

脚本通过自身位置定位仓库，不依赖当前工作目录。默认目标为
`x86_64-pc-windows-msvc`，默认输出目录为 `dist/release`。如需指定已安装的
Rust Windows target，可传入：

```powershell
.\scripts\build-client.ps1 -TargetTriple aarch64-pc-windows-msvc
```

## 构建流程

脚本失败会立即停止，并在输出中标记失败阶段。流程依次为：

1. 检查 `node`、`npm.cmd`、`go`、`rustc` 和 `cargo`；不会安装或修改任何系统工具。
2. 对 `apps/web` 和 `apps/desktop` 执行 `npm.cmd ci`。
3. 执行 Web 的 `typecheck` 和 `test`；Web 生产 `build` 由 Tauri 的
   `beforeBuildCommand` 负责，整个流程只执行一次。
4. 执行桌面配置检查、`build-sidecar.ps1` 和
   `apps/desktop/scripts/build-updater.ps1`，再执行 Tauri Release 构建。
5. 从 Tauri Release 输出中提取 NSIS、可用的 MSI 和桌面程序，并从
   `apps/desktop/src-tauri/binaries/simulator-api-<target-triple>.exe` 取权威 Go sidecar；
   Tauri release 会把同一个 sidecar 嵌入安装包，便携版则复制后统一命名为
   `portable/simulator-api.exe`。独立 Rust `Updater.exe` 从
   `apps/desktop/updater/target/<target-triple>/release/Updater.exe` 取用，便携版统一放在
   `portable/Updater.exe`。
6. 生成独立安装包、便携版目录和便携 ZIP；ZIP 内附 `README.txt`、`MANIFEST.json`、
   `SHA256SUMS.txt`，发布目录另生成覆盖 ZIP 和安装包的 `SHA256SUMS.txt`。

构建前还会校验 `apps/desktop/package.json`、`src-tauri/tauri.conf.json` 和
`src-tauri/Cargo.toml`、`updater/Cargo.toml` 及两个 Cargo.lock 中相关包的版本一致，
并将 target triple 与 `x64`/`arm64` 文件名标签绑定。所有构建都显式指定 target，
桌面程序从 `src-tauri/target/<target-triple>/release/` 取用，不回退到通用的 `target/release/`。
如果 `rustc` 或 `cargo` 不在 PATH，脚本会在当前 PowerShell 进程临时尝试
`$env:USERPROFILE\.cargo\bin`，结束时恢复原 PATH，不会写入用户或系统环境变量。

脚本显式使用 `npm.cmd`，以绕开 Windows PowerShell 中常见的
`npm.ps1` ExecutionPolicy 问题。缺少 Rust、Cargo、Windows SDK 或 WiX 时，脚本
只给出错误指引并停止，不会自动安装 Rust 或修改系统环境。桌面端与 native updater
（原生更新器）最低要求 Rust 1.88；交叉编译还要求目标 Windows target 已由构建机预先安装。

仅验证发布目录路径保护（不会安装依赖或执行构建）可运行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test-build-client-safety.ps1
```

如果开发服务器正在运行并占用 `node_modules` 下的文件，可以在确认依赖已经安装
完成后跳过 `npm ci`：

```powershell
.\scripts\build-client.ps1 -SkipDependencyInstall
```

该开关只跳过 Web/Desktop 的依赖安装，仍会执行类型检查、测试、配置校验、sidecar、
Tauri Release 和 ZIP 打包。它适合本地被开发进程占用文件时使用；正式发布或干净构建
应省略该开关，让脚本执行完整的 `npm.cmd ci`。

## 发布产物

默认产物位于 `dist/release/`：

每次构建只针对一个 target。安装包位于 ZIP 旁边，ZIP 仅包含对应架构的便携版及其
说明、清单和校验文件；MSI 可能因构建环境不可用而省略。发布目录如下（`<arch>` 为
`x64` 或 `arm64`）：

```text
dist/release/
├── MatchScope-<version>-windows-<arch>.zip
├── MatchScope_<version>_<arch>-setup.exe
├── MatchScope_<version>_<arch>_en-US.msi   # 如果生成
├── portable/
│   ├── MatchScope.exe
│   ├── simulator-api.exe
│   ├── Updater.exe
│   └── README.txt
├── README.txt
├── MANIFEST.json
└── SHA256SUMS.txt                        # 包括 ZIP 和安装包的校验值
```

ZIP 内文件清单（不包含任何 NSIS/MSI 安装包）：

```text
MatchScope-<version>-windows-<arch>.zip
├── portable/
│   ├── MatchScope.exe
│   ├── simulator-api.exe
│   ├── Updater.exe
│   └── README.txt
├── README.txt
├── MANIFEST.json
└── SHA256SUMS.txt                        # 仅校验 ZIP 内文件
```

脚本只接受 `x86_64-pc-windows-msvc`（文件名标签 `x64`）和
`aarch64-pc-windows-msvc`（文件名标签 `arm64`），不会把不同架构的安装包混入同一份发布物。

ZIP 只包含上述可交付文件，不会把 `src-tauri/target` 或完整构建缓存打进去。
`MANIFEST.json` 只记录便携版的三个 EXE，不引用 ZIP 外部的安装包。
每次执行会在严格校验输出路径后清理并重建 `dist/release`，因此 ZIP 和发布文件
可以安全覆盖；源码目录、仓库根目录和符号链接/junction 不会被脚本清理。

便携版运行时需要将三个 EXE 保持在同一目录，并且目标 Windows 需要可用的
WebView2 Runtime。安装包和 EXE 当前不包含代码签名。

### GitHub Release Assets

[每周 Windows 发布工作流](../../.github/workflows/weekly-windows-release.yml) 使用相同的
构建脚本，将便携 ZIP、NSIS `*-setup.exe`、可选 MSI 和发布目录的 `SHA256SUMS.txt`
分别上传到同一 Release 的 Assets。创建新 Release 和覆盖已有 Release 资产时都使用
同一文件清单；NSIS 或校验文件缺失会中止发布，未生成 MSI 时可正常发布。

免安装使用者下载 ZIP，解压后运行 `portable/MatchScope.exe`；需要安装的使用者直接
下载独立 NSIS 或 MSI。ZIP 文件名和 `portable/` 布局保持兼容，客户端自动更新仍可使用。

仅验证打包和资产选择、不构建客户端或实际发布，可执行：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-client-release-packaging.ps1
```

验证范围和结果见[Windows 发布打包验收](../design-decisions/testing/client-release-packaging.md)。

## 仅重新打包

如果只需要对已有 Release 产物重新打包，可使用桌面端的独立便携打包入口：

```powershell
apps\desktop\scripts\build-portable.ps1 -SkipBuild
```

完整发布脚本默认会重新执行所有检查和构建步骤，不提供跳过构建的默认路径，
以避免误把旧二进制当成最新客户端。

如需单独构建原生更新器，可在仓库根目录执行：

```powershell
apps\desktop\scripts\build-updater.ps1 -TargetTriple x86_64-pc-windows-msvc
```

## 客户端检查更新与便携包升级

桌面启动后自动检查更新，发现新版本时在侧栏“客户端更新”按钮标注版本，不弹窗或自动重启。
打开更新窗口后也可点击“检查更新”，读取构建时 GitHub `origin`
仓库的 latest Release（最新稳定发布）。窗口展示当前版本、远端版本、仓库与发布说明；
找到匹配 CPU 架构的 ZIP 后，可点击“下载更新并重启”。浏览器部署不显示该入口。
网络、GitHub 限流、附件缺失或校验失败会显示错误，下载阶段不会退出当前客户端。

默认更新来源在构建时从 `git remote get-url origin` 固定为 `owner/repo`，客户端运行时
不需要 Git。源码归档构建或有意更换更新来源时，在构建进程设置
`MATCHSCOPE_UPDATE_REPOSITORY=owner/repo`；仅接受 GitHub 仓库，不能在 Web 页面指定任意 URL。
版本标签为稳定 `vMAJOR.MINOR.PATCH` 或 `MAJOR.MINOR.PATCH`，按数值比较，不降级、不安装预发布。
请求使用公开 GitHub API，不读取用户 Git 凭据；私有仓库暂不支持。

更新器优先选择 `MatchScope-<version>-windows-<x64|arm64>-portable.zip`，也接受本页
便携 ZIP 的 `portable/` 子目录。根目录便携包或 `portable/` 内的所有文件一并安装，
必须包含 `MatchScope.exe`、`simulator-api.exe` 和 `Updater.exe`；三个 PE 文件的机器架构
都必须与当前客户端一致。压缩包上限 1 GiB，解压上限 2 GiB，
拒绝目录穿越、重复路径、符号链接和不完整包。

发布资产必须有 GitHub `digest` 提供的 SHA-256，或同一 Release 上传的
`<ZIP文件名>.sha256` / `SHA256SUMS.txt`（须含对应 ZIP 行）。包内校验和不能替代下载包校验。
校验保证下载内容与该仓库发布资产一致；当前包未做独立代码签名。

**自动替换仅支持独立目录中的 Windows 免安装 `MatchScope.exe`**，安装目录及其父目录
必须可写且不能是链接/junction。解压本页 ZIP 的用户从 `portable/MatchScope.exe` 启动即可。
NSIS/MSI 安装模式和重命名主程序暂不自动升级，应下载对应安装包手动安装。
请关闭同目录的其他客户端实例，并先保存规则编辑：重启会清空 Go sidecar 内存中的
Tickets、模拟配置和比赛历史；浏览器本地存储中的规则仍由原 Tauri 应用标识管理。

更新依次完成下载、校验、完整解包和三个 PE 架构检查，再把当前安装目录中的可信
`Updater.exe` 复制到同级 `.matchscope-update-<id>/` 事务目录，并启动
`Updater.exe --context transaction.json`。本次事务不执行已删除的 `update.ps1`，也不把刚
下载的新 `Updater.exe` 当作替换进程。外壳停止自身 sidecar 后退出；原生更新器等待父进程
退出，将整个旧目录移到同级备份，再将新目录移入原位置并启动。新客户端完成 sidecar
健康检查并创建主窗口后写启动确认；45 秒内无确认则停止此次启动的进程树，恢复旧目录
并重新启动旧版。更新器自身不占用安装目录作为工作目录，替换期间并发事务被同级独占锁
拒绝。

`transaction.json` 使用 `protocol_version=1`，字段为 `install`、`stage`、`backup`、
`failed`、`pid`、`ack` 和 `armed`。更新器先校验这些路径为安装目录父目录下的直接子目录、
拒绝链接/junction，再写入 `armed` 标记并等待 `pid` 最多 60 秒；事务替换和启动确认均由
独立 Rust `Updater.exe` 完成。

更新结果显示在“客户端更新”窗口，记录于安装目录 `.matchscope-update-status.json`。
旧目录保留为同级 `.matchscope-update-<id>-backup`，其中也保留用户自行放入安装目录的文件；
这些额外文件不会混入新程序目录。失败的新目录保留为 `-failed`。下载 ZIP 和
`transaction.json` 位于同级 `.matchscope-update-<id>/`。确认使用正常后可自行删除这些备份。
下载/解包失败留下的暂存目录也可在客户端关闭后删除。

若断电、强制结束更新进程或文件锁导致自动恢复无法完成，请先关闭相关客户端，读取
`transaction.json` 中 `install`、`backup`、`stage`、`failed` 路径；若 `backup` 存在，
保留当前 `install` 为其他名称，再把 `backup` 移回 `install` 并运行 `MatchScope.exe`。
不要删除唯一备份。恢复异常另写入事务目录的 `recovery-error.json`。
当前没有断电自动恢复机制；代码签名和 MSI/NSIS 安装模式自动升级也不在本流程范围内。
尚未包含启动确认协议的历史版本会因无确认而自动回滚，不能作为自动更新目标。

验证入口（结果以对应任务验收记录为准）：

```powershell
cargo test --manifest-path apps/desktop/updater/Cargo.toml
cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml
powershell -NoProfile -ExecutionPolicy Bypass -File apps/desktop/scripts/test-update.ps1
```

给测试脚本传入 `-PortableZip <已构建的便携ZIP路径>` 可同时验证真实 Tauri 客户端、Go sidecar
和主窗口启动；测试仅在独立临时目录运行并关闭测试实例。
具体结果见[原生更新器迁移验收](../design-decisions/testing/native-updater-acceptance.md)。

设计依据见 [ADR：Windows 便携客户端更新](../design-decisions/adr/portable-desktop-update.md)。
