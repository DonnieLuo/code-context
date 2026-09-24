# Code Context v2 HTTP API

默认 `CODE_CONTEXT_BASE_URL=http://10.203.247.48:3001`，运行时明确提供其他地址时优先使用运行时地址。发送 JSON 时设置 `Content-Type: application/json`。所有路径相对受控仓库根目录，HTTP 行列号从 1 开始。调用 CodeGraph 工具之前，服务会核对索引与当前 Git revision。

## 首选流程

1. 通常只调用一次 `build_context`。自然语言问题传 `question`；已知方法位置传 `symbol.file/line/column`；`intent` 必须是 `explain`、`debug`、`test` 或 `modify`。
2. 修改风险问题再调用 `impact`；比较分支改动调用 `change_context`。
3. 需要精确 Java 定义、引用、接口实现或类型层级时调用 JDT LS 底层工具。原文/正则字符串使用 `search_text`，原始文件使用 `read_file`。
4. 若返回 `index_stale`、`index_unavailable` 或 `index_building`，先读取 v2 仓库状态；管理员在仓库更新后调用 `refresh-index`。需要临时查看旧索引时显式传 `require_fresh_index:false`，并在回答中说明 warnings 与 revision。v1 接口仍可使用。

服务 B 不直接调用 CodeGraph 的 42 个 MCP 工具。v2 不提供自制调用图遍历或 Context Builder。

## 连接与格式

- `GET /healthz`、`GET /readyz`：服务进程状态；保持 v1 原有含义。
- `GET /v2/tools`：在线 v2 工具列表与 JSON Schema。
- `POST /v2/tools/{name}`：工具调用。请求体为 `{"requests":[...]}`，批次上限由 `max_batch_requests` 配置；默认每批一项。
- `GET /v2/repositories/{repo_id}/status`：Git/CodeGraph 索引状态。
- `POST /v2/repositories/{repo_id}/refresh-index`：管理员同步刷新该仓库 CodeGraph 索引；调用可能较慢。

```json
{
  "requests": [{
    "repo_id": "easy-rent-contract",
    "intent": "explain",
    "question": "合同取消后退款如何触发？",
    "token_budget": 6000
  }]
}
```

`results[i]` 与 `requests[i]` 对齐。单项结果包含 `data`、`truncated`、`warnings`、`meta` 或 `error`；`error` 含 `code`、`message`、`retryable`。`meta` 含 `repo_revision`、`index_revision`、`index_state`、`working_tree_dirty`、`source`、`duration_ms`。`index_revision` 是 Go 服务记录的最后一次成功索引对应 commit，可能为空。`source` 为 `codegraph`、`jdtls`、`rg`、`git` 或 `go`。

## 高阶工具

| 名称 | 主要参数 | 实现 |
| --- | --- | --- |
| `build_context` | `repo_id`, `intent`, `question` 或 `symbol`, 可选 `precision`, `token_budget`, `require_fresh_index` | 自然语言 `codegraph_get_curated_context`；已知位置 `codegraph_get_ai_context`；修改 `codegraph_get_edit_context` |
| `impact` | `repo_id`, `symbol` 位置, 可选 `change_kind=modify|delete|rename`, `precision` | `codegraph_analyze_impact`；`java_exact` 并列 JDT 引用 |
| `change_context` | `repo_id`, `base_branch`, 可选 `head=HEAD`, `format=json|markdown` | `codegraph_pr_context` + Git diff 元数据；暂不支持 staged/任意 head |

`symbol` 为 `{"file":"src/.../OrderService.java","line":42,"column":16}` 或 `{"name":"com.acme.OrderService.cancel"}`。只给名称时先返回 CodeGraph 候选；请用候选的文件与位置再次调用，特别是有重载时。`precision=java_exact` 只适用于已定位 Java 符号。`token_budget` 默认 6000、最大 12000；超过预算会 `truncated=true` 并给 `budget_exhausted`。

## 精确与底层工具

| 名称 | 主要参数 | 来源 |
| --- | --- | --- |
| `find_definition`, `find_references`, `find_overrides` | `repo_id`, `file`, `line`, `column` | JDT LS，Java；`find_overrides` 实际语义为 implementation |
| `get_type_hierarchy` | 上述位置加可选 `depth`, `direction` | JDT LS，Java |
| `get_call_graph` | 上述位置加可选 `depth`, `direction` | CodeGraph 图谱；`precision=structural` |
| `search_symbols` | `repo_id`, `query` | CodeGraph |
| `get_file_symbols` | `repo_id`, `file` | JDT LS，Java |
| `search_text` | `repo_id`, `query`；可选 `path`, `globs`, `limit`, `context_lines` | rg；文本/正则 |
| `read_file`, `list_files`, `git_query` | 分别使用 `path`、目录范围、`git_args` | 安全文件读取/只读 Git |

`trace_call_path` 与 `get_symbol_context` 仅在 v1 保留。v2 同名 `get_call_graph` 的形状和精度与 v1 不同，不复用 v1 响应解析。

## 失败处理

- `index_stale`：HEAD 或工作区状态不匹配索引。默认高阶查询不会以旧索引给确定结论。
- `index_unavailable`/`codegraph_unavailable`：CodeGraph 未配置、未安装或启动失败；可显式转用 JDT、rg、文件、Git 工具，或 v1。
- `unsupported_capability`：安装的 CodeGraph 版本缺少工具或所需参数；不要猜测参数。
- `ambiguous_symbol`：使用候选的精确位置重试。
- `symbol_not_found`：CodeGraph 对请求位置只找到邻近符号，服务拒绝将其当作精确命中。
- `codegraph_warning`：提供方在上下文内返回了警告，需阅读 `data` 中对应信息。
- `busy`/`timeout`：稍后重试，避免并行发大量请求。
- `working_tree_unindexed`：允许旧索引时会出现；可用 `read_file` 查看实时原文，不要把图谱内容说成实时内容。

只有返回的代码位置与内容足以支持结论时才作答；区分代码事实与推断，并引用仓库相对路径及行号。仍需使用 v1 时阅读 [v1 参考](http-api.md)。

位置映射以固定版本实测为准：CodeGraph 0.20.1 的 MCP 位置查询使用一基行号才能精确命中；服务保留 HTTP 输入的一基 `line`。JDT LS 仍由适配器转换成 LSP 的零基位置。该差异由真实引擎契约测试覆盖。
