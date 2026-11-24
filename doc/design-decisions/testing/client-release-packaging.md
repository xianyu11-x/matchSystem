# Windows 发布打包验收

2026-09-08，工作区未提交变更，Windows / Windows PowerShell 5.1。

当前使用方式见[客户端构建与发布](../../simulator/client-build.md#github-release-assets)。

执行命令：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-client-release-packaging.ps1
git diff --check
```

打包回归通过 x64、ARM64 × 有 MSI、无 MSI 共四种情况。测试在独立临时目录使用
模拟 EXE/MSI，直接执行 `build-client.ps1` 的打包阶段和工作流的资产选择、新建 Release
命令组装阶段，GitHub CLI 由测试记录器替代。验证内容：

- ZIP 精确包含便携版三个 EXE、两份 README、MANIFEST 和包内 SHA256SUMS，不含安装器。
- MANIFEST 只引用三个便携 EXE，包内校验清单仅引用 ZIP 内文件。
- 外部 SHA256SUMS 包含 ZIP、NSIS 和已生成的 MSI，逐行校验实际文件摘要。
- Release Assets 精确包含 ZIP、NSIS、可选 MSI 和外部 SHA256SUMS，并作为独立参数传给发布命令。
- 工作流内嵌 PowerShell 多行脚本全部通过语法解析。

限制：未执行完整 Tauri/Rust/Go 构建、真实安装、GitHub 上传或端到端自动更新；
模拟二进制不验证 PE 架构或程序可运行性。已有 Release 的覆盖分支经代码核对，未进行真实上传。

## 2026-11-23 Windows x64 本地构建

基于 `dd533b2` 及当前未提交工作树构建 MatchScope 0.1.0。环境为 Windows x64、Go 1.24.6、
Node.js 24.16.0、Rust 1.98.0，目标为 `x86_64-pc-windows-msvc`。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build-client.ps1 -OutputDirectory dist/release-2026-11-23
```

完整发布流程通过，包括 Web 类型检查、130 项测试、Tauri Release 构建及便携 ZIP、NSIS、MSI
打包。重新计算并核对发布目录 9 项及 ZIP 内 6 项 SHA-256。ZIP 的 SHA-256 为
`7af7b60e6b382a80d662a875a5c80e1fad4e422af1ed5383c1c796e4f3a7b19b`。

从发布目录启动 `portable/MatchScope.exe`，确认桌面窗口创建，内置 `simulator-api` 通过健康检查，
并验证该后端可添加、查询及删除测试物理节点。安装包验证范围为生成和校验和。

## 2026-11-23 规则库与直接重配置客户端

基于干净的 `697f631` 在 Windows x64 上构建 MatchScope 0.1.0。内置 Go 后端的构建元数据
记录同一提交。工具版本为 Go 1.24.6、Node.js 24.16.0 和 Rust 1.98.0。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-23-latest
```

Web 类型检查、26 个文件中的 183 项测试、Tauri Release 构建及便携 ZIP、NSIS、MSI 打包均通过。
复算发布目录的 9 项 SHA-256 和 ZIP 内的 6 项，全部匹配。ZIP 的 SHA-256 为
`9daf8b4124bcf1bcf216c5d756217cad1fd73c5663c4ae1504afab97ad7ce878`。

启动便携包中的 `simulator-api.exe`，验证健康检查、规则库查询、活动轮次内新增逻辑部署、
拓扑查询与删除物理节点。安装包验证范围为生成和校验和。

## 2026-11-24 时间线回放 Windows x64 本地构建

基于 `bc900ca1b6f4a182e8766a56609664cb4529e742` 和当时包含时间线回放的未提交工作区构建
MatchScope 0.1.0。环境为 Windows x64、Go 1.24.6、Node.js 24.16.0、Rust 1.98.0，
目标为 `x86_64-pc-windows-msvc`。本地产物位于独立目录，未覆盖已有发布目录。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-24-replay
powershell -NoProfile -ExecutionPolicy Bypass -File apps/desktop/scripts/test-update.ps1 `
  -UpdaterPath dist/release-2026-11-24-replay/portable/Updater.exe `
  -PortableZip dist/release-2026-11-24-replay/MatchScope-0.1.0-windows-x64.zip
```

完整发布构建通过：重新执行两处 `npm ci`、Web 类型检查、45 个测试文件共 282 项测试、
桌面配置检查、Go sidecar、原生更新器及 Tauri Release 构建，并生成便携 ZIP、NSIS 和 MSI。
Web 生产构建包含回放 API，桌面程序引用该次构建生成的前端资源。ZIP 为 8,206,281 字节，
SHA-256 为 `f1ccbef733c92b755800647a23ff946d735717548b2e45b68160e00dce9c4e12`。

ZIP 恰含 7 个预期文件，包内 6 项和发布目录校验清单逐项复算通过；三个便携 EXE 均为
Windows x64 PE，包中 sidecar 与本次编译输出的 SHA-256 一致。真实进程测试通过：便携包中
Tauri 客户端和 Go sidecar 完成启动确认，主窗口出现并正常关闭；更新器成功替换、失败回滚及
子进程终止夹具也通过。

限制：未手动操作回放界面，安装包仅验证生成及校验和，未进行实际安装、签名或公开发布。
便携版目标机器需要 WebView2 Runtime，客户端当前未签名。

## 2026-11-24 自动日志 Windows x64 本地构建

本节是固定消息正文改造前的历史构建记录；当前 `.log` 版本见[固定回放消息构建](#2026-11-24-固定回放消息-windows-x64-本地构建)。

基于 `bc900ca1b6f4a182e8766a56609664cb4529e742` 和包含日志持久化的未提交工作区，
在 Windows x64 上使用 Go 1.24.6、Node.js 24.16.0 和 Rust 1.98.0 构建 MatchScope 0.1.0，
目标为 `x86_64-pc-windows-msvc`。初次构建后后端正常关停逻辑发生更新，最终产物已使用
更新后的源码重新执行完整构建。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-24-replay-logs
powershell -NoProfile -ExecutionPolicy Bypass -File apps/desktop/scripts/test-update.ps1 `
  -UpdaterPath dist/release-2026-11-24-replay-logs/portable/Updater.exe `
  -PortableZip dist/release-2026-11-24-replay-logs/MatchScope-0.1.0-windows-x64.zip
```

完整流程通过：两处 `npm ci`、Web 类型检查、45 个测试文件共 282 项测试、桌面配置检查、
Go sidecar、原生更新器和 Tauri Release 构建，并生成便携 ZIP、NSIS 和 MSI。ZIP 为
8,218,403 字节，SHA-256 为
`5ec04a19836c4d4a5e65f7afe9a56bfa088582e6fd11fefce4ac1cc21362ab77`。

ZIP 含 7 个预期文件，包内 6 项和发布目录 9 项校验和逐项复算通过；发布目录的 sidecar
与本次编译输出一致。上一版 `dist/release-2026-11-24-replay/` 的 ZIP 哈希保持不变。
原生更新器测试使用真实 ZIP，通过 Tauri 客户端及 Go sidecar 启动确认、正常关闭、完整
目录更新、失败回滚和子进程终止检查。独立 API 的跨重启回放与自定义目录实测见
[功能验证](functional-validation.md#模拟器日志持久化)。

直接启动发布目录的 `portable/MatchScope.exe`，sidecar 在程序同级 `Logs/` 留下 28 行
JSONL；通过真实 API 上传该文件并以八票规则重放成功，关窗后文件仍在，具体结果见
[功能验证](functional-validation.md#模拟器日志持久化)。该文件是测试运行产生的数据，
不在原始 ZIP 和发布校验清单中。

发布资产选择夹具通过 x64、ARM64 与有无 MSI 四种组合，输出路径安全检查通过。
安装包仅验证生成及校验和，未进行实际安装、签名或公开发布。

## 2026-11-24 带标记文本日志的 Windows x64 本地构建

基于 `bc900ca1b6f4a182e8766a56609664cb4529e742` 和未提交工作区，在 Windows x64 使用
Go 1.24.6、Node.js 24.16.0、Rust 1.98.0 构建 MatchScope 0.1.0，目标为
`x86_64-pc-windows-msvc`。官方脚本完成 Web/Desktop 依赖安装、Web 类型检查、45 个测试
文件共 282 项测试、桌面配置检查、Go sidecar、原生更新器及 Tauri Release 构建，并生成
便携 ZIP、NSIS 和 MSI。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-24-marked-logs
powershell -NoProfile -ExecutionPolicy Bypass -File apps/desktop/scripts/test-update.ps1 `
  -UpdaterPath dist/release-2026-11-24-marked-logs/portable/Updater.exe `
  -PortableZip dist/release-2026-11-24-marked-logs/MatchScope-0.1.0-windows-x64.zip
```

ZIP 为 8,316,184 字节，SHA-256 为
`3fb62b7b4f9f66b246f3473273e6c9435818ab68ff9799e63bc5d068988d54fd`。
ZIP 恰含 7 个预期文件，包内 6 项和发布目录校验清单中的 9 项 SHA-256 逐项复算通过；
清单中的 3 个文件及其长度相符，3 个便携 EXE 均为 Windows x64 PE，sidecar 与构建输出
一致，包内说明含标记格式提示。真实 ZIP 的更新器测试通过客户端和 Go sidecar 的启动确认、
窗口出现及正常关闭、目录更新、失败回滚与子进程终止检查。发布资产选择夹具的 x64、ARM64
及有无 MSI 四种组合和输出路径安全检查均通过。

真实客户端的文本日志提取、错误行号和八票回放见[功能验证](functional-validation.md#带标记的文本日志回放)。
`fifo-replay-example.log` 是单独放在 ZIP 旁的可上传样例，测试运行时生成的 `portable/Logs/`
也不在 ZIP 或构建时校验清单中。安装包仅验证生成及校验和，未进行实际安装、签名或公开发布。

## 2026-11-24 横向时间轴 Windows x64 本地构建

基于 `bc900ca1b6f4a182e8766a56609664cb4529e742` 和未提交工作区，在 Windows x64 构建
MatchScope 0.1.0，目标为 `x86_64-pc-windows-msvc`。沿用已安装且锁文件未变的依赖，使用
官方构建脚本的 `-SkipDependencyInstall`；脚本仍执行 Web 类型检查、47 个文件共 292 项测试、
桌面配置检查、Go sidecar、原生更新器及 Tauri Release 构建，生成便携 ZIP、NSIS 和 MSI。
构建后仅补充一项缩放锚点测试，再次执行 Web 全量测试，47 个文件共 293 项通过；产品代码
没有变化。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -SkipDependencyInstall -OutputDirectory dist/release-2026-11-24-timeline-view
```

ZIP 为 8,319,981 字节，SHA-256 为
`e7f48f89a9f6b512562413643f5cdc023e27c2d60ab6b73c628bb050f3659a6d`。
ZIP 包含 7 个预期文件，包内 6 项和发布目录清单中的 9 项 SHA-256 均复算通过；清单中的
3 个文件与长度相符，3 个便携 EXE 均为 Windows x64 PE。归档内桌面程序包含本次验证的
Web 资源 `index-BzXXDVDj.js` 和 `index-BEl57oft.css`。

从发布目录实际启动 `portable/MatchScope.exe` 后，主窗口正常出现；随包 sidecar 成功提取
样例的 14 条事件，窗口正常关闭。发布版 WebView 未开放测试调试端口，具体点选、缩放和
回放交互使用相同构建资源在独立 Edge 中验证，见[功能验证](functional-validation.md#横向事件时间轴)。
发布目录另附 `timeline-preview.png` 供预览；该截图和测试运行产生的 `portable/Logs/` 均在
构建时校验清单及 ZIP 之外。安装包仅验证生成和校验和，未进行实际安装、签名或公开发布。

## 2026-11-24 时间范围选区 Windows x64 本地构建

基于 `bc900ca1b6f4a182e8766a56609664cb4529e742` 和未提交工作区，在 Windows x64 构建
MatchScope 0.1.0。沿用已安装依赖，执行官方脚本的 Web 类型检查、48 个文件共 301 项测试、
桌面配置检查、Go sidecar、原生更新器和 Tauri Release 构建，生成便携 ZIP、NSIS 和 MSI。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-24-timeline-slider -SkipDependencyInstall
```

ZIP 为 8,321,577 字节，SHA-256 为
`b9aa8e421accae6cf1f086ab9baf54f01b0f31a1680e5f33431375b624176e4d`。ZIP 内外的校验清单
逐项复算通过；归档内桌面程序包含 `index-CU0dwQEJ.js`，与浏览器交互验证使用的资源一致。
从最终 `portable/MatchScope.exe` 启动后，桌面窗口出现，随包 sidecar 健康检查通过，窗口
正常关闭。实际鼠标、键盘及窄屏交互见[功能验证](functional-validation.md#时间轴范围选择与缩放)。

发布目录另附 `timeline-slider-preview.png`，截图位于 ZIP 和构建时校验清单之外。安装包仅
验证生成及校验和，未进行实际安装、签名或公开发布。

## 2026-11-24 固定回放消息 Windows x64 本地构建

基于未提交工作区，在 Windows x64 使用官方脚本构建 MatchScope 0.1.0，沿用已安装依赖：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -OutputDirectory dist/release-2026-11-24-replay-tag -SkipDependencyInstall
```

脚本完成 Web 类型检查、48 个文件共 301 项测试、桌面配置检查及 Release 构建。便携 ZIP 为
8,324,264 字节，SHA-256 为
`4075ea25cb962db548447b06facb083be5f1febea026211802c0958e88adbbbb`，包内和发布目录
校验清单一致。最终 portable 的日志、外部日志兼容及八票回放验证见
[功能验证](functional-validation.md#固定回放消息与外部日志兼容)。

桌面专项脚本完成日志文件和提取检查，但在隐藏启动场景等待可见的启动失败对话框时超时；
整脚本未通过，错误提示的用户可见性未验。未进行实际安装、签名或公开发布。
