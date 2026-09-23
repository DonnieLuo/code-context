# Code Context v1 HTTP API

调用 Code Context 服务前阅读本参考。使用宿主环境提供的 HTTP 能力，不依赖 MCP 或 Shell。

## 连接与发现

默认使用 `CODE_CONTEXT_BASE_URL=http://10.203.247.48:3001`；若运行时明确提供其他地址，则以运行时配置为准。发送 JSON 时设置 `Content-Type: application/json`。

- `GET {CODE_CONTEXT_BASE_URL}/healthz`：检查服务可用性。
- `GET {CODE_CONTEXT_BASE_URL}/v1/tools`：获取在线工具列表与 JSON Schema。仅在服务版本或部署未知、接口报告未知工具/参数，或确需确认在线 Schema 时调用；否则直接按本参考执行，避免额外交互。

## 调用格式

每个工具通过对应端点调用：

```http
POST {CODE_CONTEXT_BASE_URL}/v1/tools/{tool_name}
Content-Type: application/json
```

同一工具的一项或多项独立查询放入 `requests`：

```json
{
  "requests": [
    {"repo_id": "order-service", "query": "createOrder"},
    {"repo_id": "order-service", "query": "cancelOrder"}
  ]
}
```

`results[i]` 与 `requests[i]` 一一对应：

```json
{
  "results": [
    {"data": [], "truncated": false},
    {"error": {"code": "tool_error", "message": "..."}, "truncated": false}
  ]
}
```

不同工具不能混在同一个批次中，应调用各自端点；互不依赖时可由宿主并行调用。无效 JSON、空批次、超大批次或未知工具会产生 HTTP 错误；有效批次中的单项失败只影响对应结果。

所有 `file` 和 `path` 均为仓库相对路径，`line` 和 `column` 从 1 开始。每个请求项都要求 `repo_id`。

## 工具参数

| 工具 | 必填字段 | 可选字段 |
| --- | --- | --- |
| `search_code` | `query` | `path`, `globs`, `limit`, `context_lines` |
| `find_definition` | `file`, `line`, `column` | `limit` |
| `find_references` | `file`, `line`, `column` | `limit` |
| `find_overrides` | `file`, `line`, `column` | `limit` |
| `get_type_hierarchy` | `file`, `line`, `column` | `depth`, `direction`, `limit` |
| `get_call_graph` | `file`, `line`, `column` | `depth`, `direction` |
| `trace_call_path` | `file`, `line`, `column`, `target_file`, `target_line`, `target_column` | `depth` |
| `search_symbols` | `query` | `limit` |
| `get_file_symbols` | `file` | `limit` |
| `get_symbol_context` | `file`, `line`, `column` | — |
| `read_file` | `path` | `start_line`, `end_line` |
| `list_files` | — | `path`, `depth`, `limit` |
| `git_query` | `git_args` | — |

字段说明：

- `query`：`search_code` 使用 ripgrep 文本或正则语法；`search_symbols` 使用符号名。
- `path`：仓库相对文件或目录范围。
- `globs`：ripgrep glob 数组，如 `["*.java", "!**/generated/**"]`。
- `limit`：单个请求项的结果上限，服务端可能进一步限制。
- `context_lines`：`search_code` 返回的上下文行数。
- `start_line`、`end_line`：`read_file` 的包含式行范围。
- `depth`：类型/调用图遍历深度；用于 `list_files` 时表示目录深度。
- `direction`：类型层级为 `subtypes` 或 `supertypes`；调用图为 `outgoing` 或 `incoming`。
- `target_*`：`trace_call_path` 的目标符号位置。
- `git_args`：以允许的只读子命令开头的 Git 参数数组，例如 `["log", "--oneline", "-20"]`。

`get_call_graph` 返回 `nodes` 和 `edges`，边包含精确 `call_sites`；`trace_call_path` 返回有序 `path`，找不到路径时可能为空。两者均受深度和结果规模限制。

## 仓库管理

以下端点不使用批量工具格式：

```http
GET  {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/status
POST {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/refresh
```

`status` 用于查看 Git revision 和 JDT LS 状态。仅在受控仓库实际更新后使用 `refresh`；它会关闭语言服务器会话，并由下一次语义查询重新创建。
