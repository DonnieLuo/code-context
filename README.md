# Code Context Service

面向服务 B Skill 的本地代码检索 HTTP 服务。服务 B 调用本服务的工具获取代码上下文，再将结果用于自己的模型推理。

Java 语义查询由 Eclipse JDT LS 提供，支持精确的定义、引用、覆写、类型层级和调用图；文本检索使用 `ripgrep`，Git 查询使用只读的 `git` 子命令。

## 前置条件

- Go 1.24+
- Git 与 ripgrep (`rg`)
- JDK 21+
- Eclipse JDT Language Server (`jdtls`)

将 `config.local.yaml` 复制为 `config.yaml`，配置服务器上受控的仓库和 jdtls 启动命令。仓库由部署流程同步；调用方只传 `repo_id`，不会接触服务器路径。

```bash
go run ./cmd/code-context
curl http://127.0.0.1:8080/healthz
```

## 工具调用

所有工具为 `POST /v1/tools/{name}`，请求体必须以 `requests` 列表承载一项或多项查询。文件路径相对于仓库根目录，行列号从 1 开始；响应的 `results` 与请求顺序一一对应，单项失败只会在对应结果中返回 `error`。

```json
{
  "requests": [{
    "repo_id": "order-service",
    "file": "src/main/java/com/acme/OrderService.java",
    "line": 42,
    "column": 16
  }]
}
```

可用工具：

- `search_code`：内部调用 `rg`（ripgrep）执行文本/正则搜索，支持 ripgrep 语法和 glob 过滤。
- `find_definition`、`find_references`、`find_overrides`：JDT LS 语义导航。
- `get_type_hierarchy`：返回父类型或子类型；通过 `direction` 指定 `supertypes` 或 `subtypes`。
- `get_call_graph`：返回保留节点、边和实际调用点的调用图；通过 `direction` 指定 `outgoing` 或 `incoming`。
- `trace_call_path`：从起点到目标符号寻找调用路径。
- `search_symbols`、`get_file_symbols`、`get_symbol_context`：符号定位、文件符号和符号上下文。
- `read_file`、`list_files`、`git_query`：文件与 Git 上下文。`git_query` 的 `git_args` 必须以允许的只读 Git 子命令开始。

`GET /v1/tools` 返回批量请求的 JSON Schema，适合嵌入服务 B 的 Skill 定义。代码仓库更新后调用：

```bash
curl -X POST http://127.0.0.1:8080/v1/repositories/order-service/refresh
```

这会关闭该仓库的 JDT LS 会话；下一次语义查询会启动新会话并重新导入项目。服务启动时会先对全部受控仓库执行 `git pull --ff-only` 并初始化 JDT LS，完成后才开始监听。

可通过 `GET /v1/repositories/order-service/status` 查看当前 Git revision 和 JDT LS 会话是否已启动。

## Ubuntu 打包与运行

在 macOS 或 Linux 构建机执行：

```bash
./scripts/package-ubuntu.sh
```

将生成的 `dist/code-context-amd64` 复制到 Ubuntu 主机。打包时会将当前 `config.yaml` 嵌入可执行文件；更新配置后需重新打包。也可通过 `-config` 指定外部配置文件覆盖内置配置：

```bash
./code-context-amd64
./code-context-amd64 -config /etc/code-context/config.yaml
```

## 安全边界

仓库只来自配置文件。服务拒绝绝对路径、`..` 路径穿越和符号链接逃逸；外部命令均以固定参数执行，不使用 shell。默认仅监听 `127.0.0.1`。

## v2：CodeGraph 上下文接口

v2 在现有 v1 路由之外提供 `POST /v2/tools/{name}`，仍使用 `{"requests":[...]}` 批量格式。默认工具是 `build_context`：自然语言问题由 CodeGraph 的 `get_curated_context` 提供跨代码库上下文；已知符号位置使用 `get_ai_context`，修改意图使用 `get_edit_context`。`impact` 和 `change_context` 分别映射 CodeGraph 的影响分析与 PR 上下文。Java 精确定义、引用、实现和类型层级仍由 JDT LS 提供。完整请求与错误码见 [v2 API 参考](code-context/references/http-api-v2.md)，实现边界见 [设计文档](docs/codegraph-v2-design.md)。

CodeGraph 固定为 `@astudioplus/codegraph-mcp@0.20.1`。本地/Ubuntu 可安装：

```bash
npm install -g @astudioplus/codegraph-mcp@0.20.1
```

安装器会下载对应平台的官方引擎并校验发布的 SHA-256；安装后确认 `codegraph-mcp` 可执行。Dockerfile 在独立 Node 构建阶段安装同一版本，并在镜像构建时检查引擎是否存在。Ubuntu 打包脚本也可通过 `CODEGRAPH_ENGINE_BIN` 与官方 `CODEGRAPH_ENGINE_SHA256` 将已校验的 Linux 引擎和 `codegraph-mcp` 启动包装器放入 `dist/`；运行时把 `dist/` 放进 `PATH`。`config.yaml` 的 `codegraph.command` 应指向实际安装的 `codegraph-mcp` 或该包装器。CodeGraph 文档：[安装与选项](https://github.com/codegraph-ai/CodeGraph/blob/main/mcp-package/README.md)、[工具调用格式](https://github.com/codegraph-ai/CodeGraph/blob/main/docs/tool-calling-guide.md)。

配置中的 `codegraph.data_root` 必须位于受控仓库之外，且服务进程可写。每个 `repo_id` 使用独立的常驻 MCP stdio 进程和数据目录。`reindex_on_start: true` 会在原有 Git 同步和 JDT 预热之后异步重建索引；索引完成前 v1 继续可用，v2 高阶工具返回索引状态。受控仓库更新后，管理员调用：

```bash
curl -X POST http://127.0.0.1:8080/v2/repositories/order-service/refresh-index
curl http://127.0.0.1:8080/v2/repositories/order-service/status
```

`index_revision` 是 Go 服务在成功重建索引且工作树干净、HEAD 未变化时记录的 commit。服务重启后会重新验证索引，旧元数据不会直接使状态变为 `ready`。若需要回滚服务 B 的 v2 调用，可改回 [v1 参考](code-context/references/http-api.md)；配置 `codegraph.enabled: false` 可关闭 CodeGraph 进程，v1 路由继续工作。

示例配置使用 `granite-97m`，因为 CodeGraph 将其列为多语言模型，适合中文问题；首次启动每个仓库隔离的数据目录时需要准备该模型。离线部署应在发布包中预热模型及索引，或在可联网环境完成首次索引后复制受控数据目录。不要使用 `--graph-only` 作为自然语言检索的默认模式。

固定版本 MCP 契约验证（需要本机已有并通过发布校验和核对的引擎）：

```bash
CODEGRAPH_ENGINE_BIN=/path/to/codegraph-server go test ./internal/codegraph -run TestRealCodeGraphContract -v
```

普通 `go test ./...` 使用内置的协议模拟服务验证 Go 适配器和路由；上线前仍需运行上述真实引擎契约测试及目标 Java 多模块仓库的端到端验收。
