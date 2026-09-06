# ADR：Windows 便携客户端完整更新

- 日期：2026-09-06
- 状态：已实施（Rust native updater 迁移；本次提交）
- 迁移关系：保留同日早期 PowerShell 实现的决策历史；当前实现由本 ADR 的 Rust
  `Updater.exe` 方案取代。

## 原始决策（PowerShell 实现，历史）

MatchScope 同时交付 Tauri 主程序、Go sidecar 及可能的附属文件。单独替换 EXE 或使用
面向安装包的升级方案无法覆盖已发布的免安装 ZIP。原方案采用固定 GitHub Release 来源、
校验后暂存完整 portable 树、外部 Windows PowerShell 事务进程、目录级备份与替换。
不在 Go 模拟器 HTTP API 中开放下载或进程管理能力；只有本地 Tauri 命令可触发更新。

完整包与独立便携包均由发布文件名匹配架构。仅支持稳定数值版本，发布来源由构建时
GitHub origin 或显式构建环境变量决定。SHA-256 来自发布资产元数据或同 Release 的
独立校验和文件，验证失败不能进入替换阶段。

原实现的失败边界是：退出前失败保留原运行实例。外部进程从事务目录启动，避免 Windows
当前目录句柄阻止安装目录改名；仅等待指定客户端 PID，并由外壳停止它拥有的 sidecar。
目录整体移动可保证同一版本的主程序与 sidecar 一起切换。新进程完成 sidecar 健康检查
与窗口创建后写确认文件；无确认则恢复旧目录。旧目录及异常事务信息持续保留。

PowerShell 与文件系统事务不能对断电提供数据库式原子性，因此原方案记录明确恢复路径，
不自动删除备份。用户自放文件留在备份，避免新旧应用文件混装。并发替换使用同级独占
文件锁。安装目录或父目录不可写、安装器模式、私有仓库、历史版本无确认协议不进入支持范围。

以下验证文字是迁移前 PowerShell 实现的历史记录，保留用于决策追溯，不作为当前 Rust
实现的验证结论：

> Windows 测试脚本通过真实外部 PowerShell helper 与 .NET EXE 夹具，覆盖完整主程序/sidecar
> 切换、重启确认、启动失败恢复、附加用户文件保留、两种 ZIP 布局、目录穿越、重复路径、
> 缺失 sidecar、错误仓库下载 URL 与稳定版本排序。Web 类型检查和全部 54 个现有测试通过；
> Rust 桌面测试 2 项通过，Web 生产构建与 Rust 开发构建通过。实际 Tauri 客户端经 Hidden
> 启动后写出健康确认，Win32 `IsWindowVisible` 验证主窗口可见，随后正常关闭。
> 公开仓库 v0.1.1 完整 ZIP 实际下载、SHA-256 校验及解包通过，未替换实际用户安装、未推送
> 或发布 Release。测试夹具不构成 ARM64 或签名发布认证。

## 当前决策：Rust native `Updater.exe`

生产更新事务迁移到独立的 `apps/desktop/updater` Rust crate（代码包），最低 Rust 版本为
1.88。Tauri 外壳负责检查 GitHub stable Release、下载和校验 ZIP、拒绝不安全路径并完整
解包；独立 `Updater.exe` 只负责在外壳退出后执行目录替换、启动确认和回滚。Go 模拟器
HTTP API 仍不参与下载、替换或进程管理。

下载阶段必须找到 `MatchScope.exe`、`simulator-api.exe` 和 `Updater.exe`，并校验三个 PE
文件的机器架构与当前客户端一致。包的 SHA-256 必须来自 GitHub 资产 `digest` 或同一
Release 的校验和附件；校验和与解包检查通过前不会进入替换阶段。

外壳只信任当前安装目录中的 `Updater.exe`：它会将该文件复制到同级
`.matchscope-update-<id>/` 事务目录，再以 `Updater.exe --context transaction.json` 启动。
下载到的新更新器只进入待安装目录，不作为本次替换进程；生产更新器不再执行已删除的
`update.ps1`。

### 事务协议

`transaction.json` 固定使用 `protocol_version=1`，字段如下：

| 字段 | 含义 |
| --- | --- |
| `install` | 当前便携安装目录 |
| `stage` | 已完整解包并待切换的新目录 |
| `backup` | 旧安装目录的同级备份目录 |
| `failed` | 启动失败的新目录保留位置 |
| `pid` | 发起更新的桌面进程 PID |
| `ack` | 新客户端启动确认文件路径 |
| `armed` | 更新器已准备、可以等待父进程退出的标记路径 |

更新器校验所有目录为安装目录父目录下的直接子目录，拒绝路径穿越、符号链接和 junction
（重解析点），并拒绝已存在的 `backup`/`failed` 目标。它先创建 `armed` 标记，最多等待
父进程 60 秒；随后把 `install` 移到 `backup`，把 `stage` 移到 `install`，启动新版并
等待 `ack` 最多 45 秒。新版在 sidecar 健康检查完成并创建主窗口后写入确认。

启动失败或确认超时会停止新进程树，将新目录移到 `failed`，把 `backup` 恢复为 `install`
并重新启动旧版；成功、失败和恢复异常分别保留状态文件或 `recovery-error.json`。同一
安装目录使用同级独占锁，重复事务会被拒绝。

### 迁移边界与非目标

- 旧发布物若缺少 `Updater.exe`，不能作为自动更新目标；需重新下载完整便携包。
- NSIS/MSI 安装模式仍需手动安装，不由便携更新事务升级。
- 当前不提供断电后的自动恢复；人工恢复必须保留唯一的 `-backup` 目录并依据事务路径操作。
- 当前不引入代码签名或签名校验；未签名 EXE 的 SmartScreen 行为保持现状。
- 尚未包含启动确认协议的历史客户端会因无 `ack` 而回滚，不能作为自动更新目标。

本次迁移已通过 Rust 单测、真实原生进程的升级/回滚，以及构建后的 Tauri 客户端与
Go sidecar 启动确认验收。命令、环境及验证边界见[原生更新器迁移验收](../testing/native-updater-acceptance.md)。

操作与恢复的权威说明位于[客户端构建与发布](../../simulator/client-build.md#客户端检查更新与便携包升级)。
