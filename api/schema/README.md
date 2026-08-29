# 匹配 DSL JSON Schema

本目录保存给编辑器和 API tooling 使用的 JSON Schema 2020-12 文档：

- `logical-node-contract/v3.schema.json`
- `expression-scalar/v3.schema.json`
- `prefilter/v3.schema.json`
- `evaluation/v3.schema.json`

Schema 负责 JSON 形状、字段和基础类型的快速校验。以下语义仍只能由 Go 生产编译器
最终确认：Contract 内名称唯一性、Attribute/Fact 引用与 scope、index 绑定、Prefilter
scope lattice、复杂度预算和运行时 provider 绑定。

因此客户端的保存流程必须同时满足：

1. 本地 Ajv 校验通过；
2. 图编辑器的端口、基数和无环检查通过；
3. `POST /api/v1/rules/validate` 返回 `valid: true`。

Schema 只描述当前 v3，不兼容 `doc/archive/` 中的旧格式。
