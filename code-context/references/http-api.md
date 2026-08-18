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
| `find_implementations` | `file`, `line`, `column` | — |
| `find_references` | `file`, `line`, `column` | — |
| `find_callers` | `file`, `line`, `column` | `depth` |
| `find_callees` | `file`, `line`, `column` | `depth` |
| `search_symbols` | `query` | — |
| `get_file_symbols` | `file` | — |
| `get_hover` | `file`, `line`, `column` | — |
| `read_file` | `path` | `start_line`, `end_line` |
| `list_files` | — | `path`, `depth` |
| `get_git_diff` | — | `base`, `head`, `path`, `staged` |

Field meaning:

- `query`: Text or regular expression for `search_code`; symbol name for `search_symbols`.
- `path`: A repository-relative file or directory scope. For `search_code`, it identifies a file scope; for `list_files`, a directory or file scope; for Git diff, a file scope.
- `globs`: Array of ripgrep glob filters, such as `["*.java"]`.
- `limit`: Per-request result cap. The server clamps it to its configured maximum.
- `context_lines`: Surrounding lines returned by `search_code`.
- `start_line`, `end_line`: Inclusive lines returned by `read_file`; omit to read the complete file within server size limits.
- `depth`: Call-hierarchy traversal depth for callers/callees; omit for direct relationships. For `list_files`, it limits directory depth instead.
- `base`, `head`, `staged`: Git diff revisions and staged-diff selector.

For `find_callers` and `find_callees`, each returned location has `depth`, where `1` is directly related to the requested method. The server deduplicates cycles and may truncate at its configured result limit.

## Repository Management

Use these outside the batch tool API:

```http
GET  {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/status
POST {CODE_CONTEXT_BASE_URL}/v1/repositories/{repo_id}/refresh
```

Use `status` to inspect Git revision and JDT LS activity. Use `refresh` only after the managed repository changes; it closes the repository's language-server session, which is recreated by the next semantic query.
