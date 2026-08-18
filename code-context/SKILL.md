---
name: code-context
description: Query managed source repositories through Code Context tools. Use when tracing code paths, locating definitions or implementations, finding references, callers or callees, searching repository text or symbols, reading source files, inspecting file trees, or comparing Git changes.
---

# Code Context

Use the Code Context HTTP service for repository-aware code investigation. Before making a request, read [the HTTP API reference](references/http-api.md). Use the host-provided HTTP capability; do not assume a base URL, authentication mechanism, or shell access.

## Workflow

1. Use the default `CODE_CONTEXT_BASE_URL` from the HTTP API reference, unless the runtime configuration overrides it. Fetch `GET /v1/tools` once per session to confirm the live tool schema.
2. Select `repo_id` and narrow with `path` or `globs` whenever known.
3. Batch independent work in one tool call using `requests`. Each item has its own `repo_id` and arguments. Keep a batch at or below the server limit.
4. Locate a symbol before semantic navigation: use `search_symbols`, `search_code`, `get_file_symbols`, or `find_definition` to obtain an exact 1-based `file`, `line`, and `column`.
5. Use semantic tools for relationships. Use text search only when semantic indexing cannot answer the question.
6. Inspect `results` in request order. Handle an item's `error` independently; if `truncated` is true, narrow the query, path, depth, or batch.

## Tool Selection

- Use `search_code` for text or regular-expression matches.
- Use `search_symbols` to locate a type, method, or field by name.
- Use `find_definition`, `find_implementations`, and `find_references` for symbol navigation.
- Use `find_callers` and `find_callees` to trace call flow.
- Use `get_file_symbols`, `get_hover`, and `read_file` to understand a file or symbol in context.
- Use `list_files` to discover repository layout and `get_git_diff` to inspect changes.

## Call Hierarchy

Pass `depth: 1` for direct relationships. Use `depth: 2` or `depth: 3` for a business-flow trace. Request deeper traversal only when needed: the result grows quickly and is bounded by the server's configured maximum depth and result limit.

Each hierarchy node includes `depth`; `1` is directly related to the requested method. The server deduplicates cycles. Follow the returned locations rather than attempting to infer a call chain from names alone.

## Batch Shape

Use the same envelope for every tool:

```json
{
  "requests": [
    {"repo_id": "order-service", "query": "createOrder"},
    {"repo_id": "order-service", "query": "cancelOrder"}
  ]
}
```

For semantic and call-hierarchy tools, use repository-relative paths and 1-based positions:

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

Do not mix unrelated investigation stages merely to fill a batch. Batch only independent calls whose outputs are not needed to form later inputs.
