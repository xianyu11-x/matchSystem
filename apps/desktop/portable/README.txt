MatchScope Portable（Windows 便携版）
========================================

压缩包名称
----------
- 桌面端独立便携包：`MatchScope-<version>-windows-<x64|arm64>-portable.zip`
- 根发布脚本的聚合包：`MatchScope-<version>-windows-<x64|arm64>.zip`，便携文件位于
  包内的 `portable/` 目录

请使用与目标 Windows CPU 架构一致的压缩包；安装包文件名中的 `x64` 或 `arm64`
也是相同的架构标签。

使用方法
--------
1. 请先完整解压 ZIP，不要直接在压缩包预览窗口中运行。
2. 保持 MatchScope.exe、simulator-api.exe 与 Updater.exe 位于同一目录。
3. 双击 MatchScope.exe。
4. 在侧栏“客户端更新”中检查 GitHub 新版，确认后下载更新并重启。
   更新会清空模拟器内存状态，请先保存规则编辑。目录及父目录必须可写。
   自动更新由同目录 Updater.exe 执行，保留同级 .matchscope-update-<id>-backup 旧目录，
   新版启动失败自动恢复。不要删除、重命名或手动运行 Updater.exe。
   自行放入旧目录的额外文件保留在备份中，确认更新正常后可手动取回。

运行机制
--------
MatchScope.exe 会自动启动同目录的 simulator-api.exe，并在桌面客户端关闭时终止该后端进程。
Updater.exe 是独立的 Windows 原生更新器，仅在客户端准备好事务并退出后由客户端调用。
程序不会通过 MSI/NSIS 安装，也不会写入 Windows 的“已安装的应用”列表。

系统要求
--------
- 与压缩包 CPU 架构匹配的 Windows 10 或 Windows 11。
- Microsoft Edge WebView2 Runtime。现代 Windows 10/11 通常已经包含；如果窗口无法启动，请安装 Microsoft 官方 WebView2 Runtime。

提示
----
- 目标电脑不需要安装 Go、Node.js 或 Rust；这些工具只在构建发布包时使用。
- 更新需要三个 EXE 保持同一 CPU 架构；请不要从其他便携包复制 Updater.exe。
- 断电或强制结束更新进程不提供自动恢复；如需人工恢复，请按随包文档中的
  `transaction.json` 与备份目录说明操作，并保留唯一的 `-backup` 目录。
- 未签名的内部测试版本可能触发 Windows SmartScreen 提示。面向外部发布时建议对 EXE 进行代码签名。
