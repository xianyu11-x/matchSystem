# MatchScope Desktop

apps/desktop 是 MatchScope 的 Tauri 2 Windows shell（桌面壳）。它复用
../web 的 React/Vite 构建产物，不把 Go API 或匹配核心编译进前端。

## 生命周期

桌面应用启动时由 Rust shell 通过 tauri-plugin-shell 的 sidecar API 启动
simulator-api，固定传入：

    --addr 127.0.0.1:0

Rust 端只读取这个 child（子进程）自己的 stdout，并接受一行
{"type":"ready","apiBaseUrl":"http://127.0.0.1:<port>"}。它会校验地址为
loopback（本机回环地址），等待 GET /api/v1/health 返回 HTTP 200，然后才创建
main 窗口。窗口的 document-start initialization script（文档开始初始化脚本）
设置 window.__MATCH_API_BASE_URL__。注意：Go ready JSON 给出 origin（来源），
shell 注入给 Web UI 的值会补上 /api/v1，因此 Web UI 的首次请求不会抢跑。

Rust 端持有本次启动的 CommandChild handle（子进程句柄）。窗口关闭或应用退出时
只调用这个 handle 的 kill()；不会按进程名查找、杀掉或接管用户自行启动的
simulator-api。sidecar 非预期退出会向窗口广播 simulator-sidecar-exited，shell
错误会广播 simulator-sidecar-error；事件 payload 包含退出码/信号或错误消息，
供 UI 显示故障状态。

## 开发与打包

在 apps/desktop 目录运行：

    # 先生成 Tauri 要求的 target-triple sidecar 文件名
    .\scripts\build-sidecar.ps1

    # 验证 JSON、capability scope 和 npm scripts（不需要 Rust）
    npm run check:config

    # 开发模式：Tauri 启动 ../web 的 Vite dev server
    npm run dev

    # 生产打包：会再次执行 ../web 的 build
    npm run build

    # 便携发布：生成解压即用的 ZIP，不生成或安装 MSI/NSIS
    npm run build:portable

`build:portable` 会执行 Web、Go sidecar 和 Tauri release 构建，但使用
`tauri build --no-bundle` 跳过安装器，随后生成：

    dist/MatchScope-<version>-windows-x64-portable.zip
    dist/MatchScope-<version>-windows-x64-portable.zip.sha256

ZIP 内只有 `MatchScope.exe`、`simulator-api.exe` 和 `README.txt`。目标电脑完整解压后
直接运行 `MatchScope.exe`，并保持两个 EXE 在同一目录；不需要安装 Go、Node.js 或
Rust。便携包不负责安装 WebView2 Runtime（网页视图运行时），目标 Windows 需要已有
该运行时。Windows 10/11 通常已经具备；缺失时应安装 Microsoft 官方 WebView2 Runtime。

如果 Release EXE 已经构建完成，只重新封装现有产物可运行：

    .\scripts\build-portable.ps1 -SkipBuild

build-sidecar.ps1 默认生成
src-tauri/binaries/simulator-api-x86_64-pc-windows-msvc.exe。交叉或 ARM Windows
构建可以传入 Tauri target triple，例如：

    .\scripts\build-sidecar.ps1 -TargetTriple aarch64-pc-windows-msvc

Sidecar 二进制被 .gitignore 忽略，不能把本地生成物提交进仓库；每台构建机或
CI 都应在 tauri dev/build 前运行 helper。Tauri 配置中的 externalBin 使用无
后缀逻辑名，Tauri 会根据当前 target triple 选择
src-tauri/binaries/simulator-api-<triple>.exe。

普通网页部署不经过此目录，也不会启动 sidecar；网页端继续使用
VITE_API_BASE_URL、apiBase 查询参数或默认 /api/v1。

## 验证边界

当前 Windows x64 环境已安装 stable MSVC Rust toolchain，并完成以下验证：

    cargo fmt --check
    cargo check
    cargo test
    npm run check:config
    npm run build

`npm run build` 已生成 NSIS setup 和 MSI 安装包；`npm run build:portable` 已生成并
校验 portable ZIP（便携压缩包）。从 ZIP 解压后的 Release smoke test 还验证了桌面
进程会拉起自己的 simulator-api sidecar，正常关闭窗口后主进程与该 sidecar 都会退出。
其他构建机仍需安装 Rust、Tauri CLI 依赖、WebView2 打包工具及 Go，并先生成匹配
目标 triple 的 sidecar。
