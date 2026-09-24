---
name: code-context
description: 通过 Code Context v2 HTTP API 一次获取受控仓库的代码上下文，以 CodeGraph 为主，JDT LS 补充 Java 精确语义。适用于理解实现、准备修改、评估影响和查询变更。
---

# Code Context

使用宿主环境提供的 HTTP 能力调用 Code Context 服务。先阅读 [v2 HTTP API 参考](references/http-api-v2.md)，采用其中的默认服务地址，除非运行时配置覆盖。服务 B 不直接操作 CodeGraph MCP。

## 默认流程

1. 选择受控仓库。新服务 `easy-rent-contract` 是绝大多数业务流程的主要实现；老服务 `home-trusteeship-contract` 主要保留对外 Dubbo 接口、聚合、合同列表、部分详情与未迁移计算。用户指定仓库时遵从；涉及跨服务入口或迁移边界时检查两者。
2. 对自然语言问题先调用一次 `build_context`。已知精确符号位置时传 `symbol`；问题不同则选择 `intent=explain|debug|test|modify`。不要先并行调用大量 `search_symbols`、`get_call_graph` 和 `read_file` 拼上下文。
3. 若是修改风险，调用 `impact`；若是分支变更，调用 `change_context`。需要核对 Java 接口实现、复杂重载、定义或引用时再用 JDT LS 精确工具。字符串/正则或原始文件证据分别用 `search_text`、`read_file`。
4. 检查每项 `error`、`truncated`、`warnings`、`repo_revision` 和 `index_revision`。索引陈旧时先看状态并等待受控刷新；若用户需要立即调查，可显式允许旧索引并说明限制，或者使用底层实时工具。
5. 基于真实代码证据回答，列出实际检索过的仓库，给出仓库相对路径和行号；对动态分派、未索引改动与未命中保持准确表述。

v2 不可用时可按 [v1 参考](references/http-api.md)回退。v1 的批量工具和响应格式与 v2 不同；不要混用解析。旧的 `trace_call_path`、`get_symbol_context` 仅在 v1 存在。
