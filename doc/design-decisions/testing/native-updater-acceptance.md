# 原生更新器迁移验收

- 日期：2026-09-06。
- 代码：`1af0bf8` 基础上的未提交工作区变更，不代表已发布版本。
- 环境：Windows 11 Pro 10.0.26200、Windows x64、Rust 1.98.0、Go 1.26.5、Node 24.16.0。
- 范围：客户端原生检查/下载/校验、独立 `Updater.exe`、便携包打包及失败恢复。

## 已执行验证

| 命令 | 结果及证据范围 |
| --- | --- |
| `cargo test --manifest-path apps/desktop/src-tauri/Cargo.toml --no-default-features --locked` | 12 项通过。包含两个 ZIP 布局、SHA-256/下载长度、版本排序、仓库 URL、PE 架构、缺少更新器、路径穿越、符号链接，以及 ZIP 库合并前的完全同名条目检查 |
| `cargo test --manifest-path apps/desktop/updater/Cargo.toml --locked` | 8 项 Windows 测试通过。覆盖事务路径、协议版本、已有确认文件、缺少下一版更新器及独占锁 |
| `npm --prefix apps/desktop run check:config` | 通过，检查独立 crate、二进制名称、版本与构建入口 |
| `npm --prefix apps/desktop run build:portable` | 通过 Web 类型检查、生产构建、Go sidecar、原生更新器和 Tauri x64 Release 构建，生成包含三个 EXE 的便携 ZIP |
| `powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-client.ps1 -SkipDependencyInstall -OutputDirectory dist/release/native-updater-verification` | 完整发布入口通过：Web 类型检查和 98 项测试、桌面配置、三个 EXE、NSIS/MSI、MANIFEST、SHA256SUMS 和聚合 ZIP。复用现有 node_modules，没有重新执行 npm ci |

真实进程验收命令（仓库根目录）：

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File apps/desktop/scripts/test-update.ps1 `
  -UpdaterPath apps/desktop/updater/target/x86_64-pc-windows-msvc/release/Updater.exe `
  -PortableZip apps/desktop/dist/MatchScope-0.1.0-windows-x64-portable.zip
```

命令通过。测试在唯一临时目录中运行，覆盖：

- Unicode（中文）及带空格的安装目录。
- 更新器从当前安装目录复制到外部事务目录，写就绪文件后等待原进程；原进程存活时文件不变。
- 新主程序、sidecar 与下一版更新器一起切换，旧目录及用户额外文件保留在备份。
- 失败的新程序先生成子进程再退出；Windows Job Object（作业对象）终止子进程，恢复旧目录并重启旧版。
- 将实际构建的 Tauri 便携包作为新版：真实 Go sidecar 完成健康检查、客户端写启动确认、主窗口出现；测试随后正常关闭该测试客户端。

测试脚本里的 PowerShell/.NET 仅是开发验收工具；生产下载与替换流程不调用 PowerShell。

## 实施中发现并处理的问题

- ZIP 库按文件名索引，会隐藏完全同名的重复条目。新增原始中央目录条目计数校验，避免静默选择其中一项。
- 新进程以 suspended（挂起）状态创建，加入 Job Object 后才恢复执行，确保早期创建的子进程也受失败恢复控制。
- 路径校验不递归扫描安装父目录的无关文件。
- 当前安装目录中的更新器也必须是普通文件，其路径组件不能包含重解析点；复制前执行检查。
- 通用 `target/release/simulator-api.exe` 已被现有用户进程占用，最初的生产构建因文件占用失败。构建统一显式指定 target triple（目标三元组），从对应架构目录取产物；保留现有进程，并已在独立架构目录完成构建。

## 验证边界

本次没有替换真实用户安装、没有推送或发布 Release，也没有从网络实际安装新版。
ARM64 仅保留构建与架构检查路径，未在 ARM64 硬件运行；未进行断电恢复或签名认证测试。
测试环境策略拦截了创建真实符号链接夹具的命令；ZIP 内符号链接拒绝测试已通过，当前更新器文件的真实符号链接场景未实测。
Web 生产构建仍有现存的大于 500 kB 的 chunk（代码块）提示，不影响构建成功。

当前机制和人工恢复步骤见[客户端构建与发布](../../simulator/client-build.md#客户端检查更新与便携包升级)，
责任划分和迁移决定见[原生更新器 ADR](../adr/portable-desktop-update.md)。
