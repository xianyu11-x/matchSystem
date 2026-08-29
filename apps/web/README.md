# MatchScope Web

独立的 React 19 + TypeScript + Vite SPA。页面通过 `/api/v1` 访问 simulator
HTTP/SSE；它不会导入 Go 内部结构。

## 开发

```powershell
npm install
npm run dev
```

默认使用真实 API。若仅需查看客户端交互，可显式开启演示适配层：

```powershell
$env:VITE_DEMO_MODE = "true"
npm run dev
```

真实服务地址可通过 `VITE_API_BASE_URL` 指定（例如
`http://127.0.0.1:8080/api/v1`）。Tauri shell 也可以在加载页面前注入
`window.__MATCH_API_BASE_URL__`，或用 `apiBase` 查询参数传入 sidecar 发现的地址。

## 质量门禁

```powershell
npm run format
npm run typecheck
npm test
npm run build
```

`apps/web/go.mod` 是有意保留的空嵌套 Go module：前端的 `node_modules` 不是 Go
包，嵌套 module 让根目录 `go test ./...` 不会把它误扫入；前端仍以 npm 命令为准。
