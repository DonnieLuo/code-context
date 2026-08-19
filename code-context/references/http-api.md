# Code Context HTTP API

Read this reference before calling the Code Context service through an HTTP-capable host.

## Connection and Discovery

Use `CODE_CONTEXT_BASE_URL=http://10.203.247.48:3001` by default. Allow the runtime environment to override it for another deployment. Send JSON requests with `Content-Type: application/json`.

Check availability with `GET {CODE_CONTEXT_BASE_URL}/healthz`. Discover the live tool list and JSON Schema with `GET {CODE_CONTEXT_BASE_URL}/v1/tools`; use it as the source of truth if it differs from this reference.

## Tool Request and Response

Call every tool through:

```http
POST {CODE_CONTEXT_BASE_URL}/v1/tools/{tool_name}
Content-Type: application/json
```

Use one or more independent requests in the body:

```json
{"requests":[{"repo_id":"order-service","query":"createOrder"}]}
```

`results[i]` corresponds to `requests[i]`:

```json
{
  "results": [
    {"data": [], "truncated": false},
    {"error": {"code": "tool_error", "message": "..."}, "truncated": false}
  ]
}
```

An invalid JSON body, an empty or oversized `requests` list, or an unknown tool is an HTTP error. A valid batch can contain per-item errors. Treat `truncated: true` as incomplete output and narrow or split the query.

All `file` and `path` values are repository-relative. `line` and `column` are 1-based.

## Tool Parameters

Every request item requires `repo_id`.

| Tool | Required fields | Optional fields |
| --- | --- | --- |
| `search_code` | `query` | `path`, `globs`, `limit`, `context_lines` |
| `find_definition` | `file`, `line`, `column` | — |
| `find_references` | `file`, `line`, `column` | — |
| `find_overrides` | `file`, `line`, `column` | — |
| `get_type_hierarchy` | `file`, `line`, `column` | `depth`, `direction` (`subtypes` or `supertypes`) |
| `get_call_graph` | `file`, `line`, `column` | `depth`, `direction` (`outgoing` or `incoming`) |
| `trace_call_path` | `file`, `line`, `column`, `target_file`, `target_line`, `target_column` | `depth` |
| `search_symbols` | `query` | — |
| `get_file_symbols` | `file` | — |
| `get_symbol_context` | `file`, `line`, `column` | — |
| `read_file` | `path` | `start_line`, `end_line` |
| `list_files` | — | `path`, `depth` |
| `git_query` | `git_args` | — |

Field meaning:

- `query`: `search_code` 的文本或正则表达式（内部调用 `rg`/ripgrep，语法遵循 ripgrep）；`search_symbols` 的符号名。
- `path`: A repository-relative file or directory scope. For `search_code`, it identifies a file scope; for `list_files`, a directory or file scope.
- `globs`: Array of ripgrep glob filters, such as `["*.java"]`.
- `limit`: Per-request result cap. The server clamps it to its configured maximum.
- `context_lines`: Surrounding lines returned by `search_code`.
- `start_line`, `end_line`: Inclusive lines returned by `read_file`; omit to read the complete file within server size limits.
- `depth`: Type/call graph traversal depth; omit for direct relationships. For `list_files`, it limits directory depth instead.
- `direction`: Graph direction. Type hierarchy accepts `subtypes` or `supertypes`; call graph accepts `outgoing` or `incoming`.
- `target_*`: Target symbol position for `trace_call_path`.
- `git_args`: A Git argument vector beginning with a permitted read-only subcommand, for example `["log", "--oneline", "-20"]` or `["diff", "HEAD~1"]`.

`get_call_graph` returns `nodes` and `edges`; each edge carries the precise source call sites. `trace_call_path` returns an ordered `path` and may be empty when no route is found. Both are bounded by the configured depth and result limit.

## Repository Management

Use these outside the batch tool API:

```http
GET  {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/status
POST {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/refresh
```

Use `status` to inspect Git revision and JDT LS activity. Use `refresh` only after the managed repository changes; it closes the repository's language-server session, which is recreated by the next semantic query.
