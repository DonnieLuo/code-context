---
name: code-context
description: 通过 Code Context 工具检索受控源码仓库。适用于追踪代码调用路径、定位定义或覆写、查找引用、搜索代码文本或符号、读取文件、查看文件树，以及执行只读 Git 查询。
---

# Code Context

使用 Code Context HTTP 服务进行面向仓库的代码分析。发起请求前，请阅读 [HTTP API 参考](references/http-api.md)。使用宿主环境提供的 HTTP 能力；不要假定服务地址、认证方式或 Shell 访问权限。

## 工作流程

1. 除非运行时配置覆盖，否则使用 HTTP API 参考中的默认 `CODE_CONTEXT_BASE_URL`。默认依据本 Skill 和 HTTP API 参考调用；仅在服务版本或部署环境未知、接口返回未知工具或参数错误，或需要确认在线 Schema 时，获取 `GET /v1/tools`。
2. 指定 `repo_id`；已知时使用 `path` 或 `globs` 缩小范围。
3. 通过 `requests` 在一次工具调用中批量执行相互独立的任务。每项拥有独立的 `repo_id` 和参数；批量大小不得超过服务端限制。
4. 进行语义导航前先定位符号：使用 `search_symbols`、`search_code`、`get_file_symbols` 或 `find_definition` 获取准确且从 1 开始的 `file`、`line` 和 `column`。
5. 查询关系时优先使用语义工具；仅在语义索引无法回答时使用文本搜索。
6. 按请求顺序检查 `results`。独立处理每项的 `error`；如果 `truncated` 为 true，请缩小查询、路径、深度或拆分批次。

## 工具选择

- `search_code` 用于文本或正则表达式匹配。其内部执行 `rg`（ripgrep），模式语法与 glob 过滤均遵循 ripgrep 语义。
- `search_symbols` 按名称定位类型、方法或字段。
- `find_definition`、`find_overrides` 和 `find_references` 用于符号导航。
- `get_type_hierarchy` 用于查询 Java 继承关系；`get_call_graph` 用于查询入向或出向调用关系。
- 已知起点和目标符号时，使用 `trace_call_path`。
- 使用 `get_file_symbols`、`get_symbol_context` 和 `read_file` 在上下文中理解文件或符号。
- 使用 `list_files` 了解仓库结构；使用 `git_query` 查询只读 Git 历史或工作区状态。

## 调用图

直接关系传入 `depth: 1`；业务流程追踪可使用 `depth: 2` 或 `depth: 3`。`get_call_graph` 返回 `nodes` 和 `edges`，其中边包含准确的 `call_sites`。仅在必要时请求更深的遍历：结果会快速增长，并受服务端最大深度和结果数限制。

每个层级节点包含 `depth`；`1` 表示与请求方法直接相关。服务端会对循环去重。应跟随返回的位置，而不要仅凭名称自行推断调用链。

## 批量请求格式

所有工具均使用以下请求包裹格式：

```json
{
  "requests": [
    {"repo_id": "order-service", "query": "createOrder"},
    {"repo_id": "order-service", "query": "cancelOrder"}
  ]
}
```

语义、类型层级和调用图工具使用相对于仓库根目录的路径，以及从 1 开始的位置：

```json
{
  "requests": [{
    "repo_id": "order-service",
    "file": "src/main/java/com/acme/OrderService.java",
    "line": 42,
    "column": 16,
    "depth": 3
  }]
}
```

不要为了凑满批次而混合无关的分析阶段；仅批量执行其输出不被后续输入依赖的独立调用。
