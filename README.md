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
