# Windows 客户端构建与 ZIP 发布

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
4. 执行桌面配置检查、现有 `build-sidecar.ps1`，再执行 Tauri Release 构建。
5. 从 Tauri Release 输出中提取 NSIS、可用的 MSI 和桌面程序，并从
   `apps/desktop/src-tauri/binaries/simulator-api-<target-triple>.exe` 取权威 Go sidecar；
   Tauri release 会把同一个 sidecar 嵌入安装包，便携版则复制后统一命名为
   `portable/simulator-api.exe`。
6. 生成发布目录、便携版目录、`README.txt`、`MANIFEST.json`、`SHA256SUMS.txt` 和 ZIP。

构建前还会校验 `apps/desktop/package.json`、`src-tauri/tauri.conf.json` 和
`src-tauri/Cargo.toml` 的版本一致，并将 target triple 与 `x64`/`arm64` 文件名标签绑定。
如果 `rustc` 或 `cargo` 不在 PATH，脚本会在当前 PowerShell 进程临时尝试
`$env:USERPROFILE\.cargo\bin`，结束时恢复原 PATH，不会写入用户或系统环境变量。

脚本显式使用 `npm.cmd`，以绕开 Windows PowerShell 中常见的
`npm.ps1` ExecutionPolicy 问题。缺少 Rust、Cargo、Windows SDK 或 WiX 时，脚本
只给出错误指引并停止，不会自动安装 Rust 或修改系统环境。

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

每次构建只针对一个 target，因此每份 ZIP 只包含对应架构的安装包；MSI 可能因构建环境
不可用而省略。ZIP 内文件清单如下：

```text
MatchScope-<version>-windows-x64.zip
├── MatchScope_<version>_x64-setup.exe
├── MatchScope_<version>_x64_en-US.msi       # 如果生成
├── portable/
│   ├── MatchScope.exe
│   ├── simulator-api.exe
│   └── README.txt
├── README.txt
├── MANIFEST.json
└── SHA256SUMS.txt

MatchScope-<version>-windows-arm64.zip
├── MatchScope_<version>_arm64-setup.exe
├── MatchScope_<version>_arm64_en-US.msi     # 如果生成
├── portable/
│   ├── MatchScope.exe
│   ├── simulator-api.exe
│   └── README.txt
├── README.txt
├── MANIFEST.json
└── SHA256SUMS.txt
```

脚本只接受 `x86_64-pc-windows-msvc`（文件名标签 `x64`）和
`aarch64-pc-windows-msvc`（文件名标签 `arm64`），不会把不同架构的安装包混入同一份发布物。

ZIP 只包含上述可交付文件，不会把 `src-tauri/target` 或完整构建缓存打进去。
每次执行会在严格校验输出路径后清理并重建 `dist/release`，因此 ZIP 和发布文件
可以安全覆盖；源码目录、仓库根目录和符号链接/junction 不会被脚本清理。

便携版运行时需要将两个 EXE 保持在同一目录，并且目标 Windows 需要可用的
WebView2 Runtime。安装包和 EXE 当前不包含代码签名。

## 仅重新打包

如果只需要对已有 Release 产物重新打包，可使用桌面端的独立便携打包入口：

```powershell
apps\desktop\scripts\build-portable.ps1 -SkipBuild
```

完整发布脚本默认会重新执行所有检查和构建步骤，不提供跳过构建的默认路径，
以避免误把旧二进制当成最新客户端。
