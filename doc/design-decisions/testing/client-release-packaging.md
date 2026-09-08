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
