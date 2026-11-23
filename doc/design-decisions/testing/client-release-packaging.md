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
