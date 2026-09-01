MatchScope Portable（Windows 便携版）
========================================

压缩包名称
----------
- x64：`MatchScope-<version>-windows-x64.zip`
- ARM64：`MatchScope-<version>-windows-arm64.zip`

请使用与目标 Windows CPU 架构一致的压缩包；安装包文件名中的 `x64` 或 `arm64`
也是相同的架构标签。

使用方法
--------
1. 请先完整解压 ZIP，不要直接在压缩包预览窗口中运行。
2. 保持 MatchScope.exe 与 simulator-api.exe 位于同一目录。
3. 双击 MatchScope.exe。

运行机制
--------
MatchScope.exe 会自动启动同目录的 simulator-api.exe，并在桌面客户端关闭时终止该后端进程。
程序不会通过 MSI/NSIS 安装，也不会写入 Windows 的“已安装的应用”列表。

系统要求
--------
- 与压缩包 CPU 架构匹配的 Windows 10 或 Windows 11。
- Microsoft Edge WebView2 Runtime。现代 Windows 10/11 通常已经包含；如果窗口无法启动，请安装 Microsoft 官方 WebView2 Runtime。

提示
----
- 目标电脑不需要安装 Go、Node.js 或 Rust；这些工具只在构建发布包时使用。
- 未签名的内部测试版本可能触发 Windows SmartScreen 提示。面向外部发布时建议对 EXE 进行代码签名。
