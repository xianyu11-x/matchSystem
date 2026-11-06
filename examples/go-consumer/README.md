# 独立 Go 消费者示例

本目录是独立 Go 模块，使用根模块 `github.com/xianyu11-x/matchSystem/pkg/` 下的
`client`、`common`、`identity` 和 `matchsystem` 四个公开包。路由器为两张同分区 Ticket
选择 OwnerRef，PhysicalNode 串行处理投递、匹配和查询；自描述的 Match Fact Provider
维护人数，规则在人数达到 2 时完成匹配。
示例会检查两张 Ticket 已消费，并验证它们在本轮及下一轮都不会恢复或重复匹配。

`go.mod` 中的 `v0.0.0` 和 `replace => ../..` 仅用于本地示例。实际消费者应使用
可用的模块版本，按自己的部署方式调整或移除 `replace`。

在本目录执行：

```powershell
$env:GOWORK = 'off'
go test ./... -count=1
go run .
```

预期输出：

```text
matched: 1001,1002
remaining: 0
duplicate: false
```

根目录的 `go test ./...` 不会进入嵌套模块，必须单独运行上述测试。也可以在仓库
根目录执行以下脚本：它将示例复制到仓库外的临时目录，关闭 Go workspace，验证
模块位置和应用层依赖边界，然后运行 `go mod tidy`、测试及示例。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-go-consumer.ps1
```
