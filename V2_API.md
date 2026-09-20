# V2 API

V2 工具接口均为 `POST http://10.203.247.48:3001/v2/tools/{tool_name}`。请求体直接是参数对象，不再包在 `requests` 列表中；响应为单个结果对象。参数类型仅使用 `string` 和 `number`。`globs`、`git_args` 这两个原本的数组参数使用 JSON 字符串数组传递；例如 `"[\"*.java\"]"`。

## http://10.203.247.48:3001/v2/tools/search_code

功能：在指定仓库中按文本或 ripgrep 正则表达式搜索代码。结果规范化为包含 `file`、`line`、`column`、`match` 和 `snippet` 的代码片段列表。

场景：模型需要定位某个函数调用、配置项、异常信息或业务关键词时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/search_code`，请求体：`{"repo_id":"order-service","query":"createOrder","globs":"[\"*.java\"]"}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"query","type":"string","description":"要检索的文本或 ripgrep 正则表达式","required":true},{"name":"path","type":"string","description":"可选的仓库相对文件路径范围","required":false},{"name":"globs","type":"string","description":"可选的 JSON 字符串数组，例如：[\"*.java\"]","required":false},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false},{"name":"context_lines","type":"number","description":"可选的命中行前后上下文行数","required":false}]
```

## http://10.203.247.48:3001/v2/tools/find_definition

功能：查询指定符号的定义位置。

场景：模型已定位到某个方法、类或字段的引用，需要跳转到其声明处时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/find_definition`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/find_references

功能：查找指定符号的全部引用位置。

场景：模型需要评估修改某个方法、类或字段可能影响的调用方时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/find_references`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/find_overrides

功能：查找指定类型或方法的实现/覆写位置。

场景：模型需要了解接口的具体实现，或分析父类方法被哪些子类覆写时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/find_overrides`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/get_symbol_context

功能：获取指定符号的类型信息、悬浮信息和定义上下文。

场景：模型需要确认某个符号的签名、所属类型或声明内容时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/get_symbol_context`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true}]
```

## http://10.203.247.48:3001/v2/tools/get_type_hierarchy

功能：获取类型的父类、接口或子类型层级。

场景：模型需要理解某个类的继承关系、实现关系或可替换实现时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/get_type_hierarchy`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16,"direction":"supertypes","depth":2}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"depth","type":"number","description":"可选的层级遍历深度","required":false},{"name":"direction","type":"string","description":"可选：subtypes 或 supertypes","required":false},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/get_call_graph

功能：获取指定方法的调用图，包含节点、边及调用位置。

场景：模型需要追踪方法调用链，分析上游调用方或下游依赖时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/get_call_graph`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16,"direction":"outgoing","depth":2}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"depth","type":"number","description":"可选的调用图遍历深度","required":false},{"name":"direction","type":"string","description":"可选：outgoing 或 incoming","required":false}]
```

## http://10.203.247.48:3001/v2/tools/trace_call_path

功能：查找从起始符号到目标符号的一条调用路径。

场景：模型需要验证两个业务方法之间是否存在调用链，或解释调用如何逐层传递时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/trace_call_path`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java","line":42,"column":16,"target_file":"src/main/java/com/acme/PaymentService.java","target_line":20,"target_column":10,"depth":4}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"符号所在的仓库相对文件路径","required":true},{"name":"line","type":"number","description":"符号行号（从 1 开始）","required":true},{"name":"column","type":"number","description":"符号列号（从 1 开始）","required":true},{"name":"target_file","type":"string","description":"目标符号所在的仓库相对文件路径","required":true},{"name":"target_line","type":"number","description":"目标符号行号（从 1 开始）","required":true},{"name":"target_column","type":"number","description":"目标符号列号（从 1 开始）","required":true},{"name":"depth","type":"number","description":"可选的最大调用路径深度","required":false}]
```

## http://10.203.247.48:3001/v2/tools/read_file

功能：读取仓库中的完整文件或指定行范围。

场景：模型已通过搜索或语义导航定位文件，需要查看实现上下文时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/read_file`，请求体：`{"repo_id":"order-service","path":"src/main/java/com/acme/OrderService.java","start_line":1,"end_line":120}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"path","type":"string","description":"要读取的仓库相对文件路径","required":true},{"name":"start_line","type":"number","description":"可选的起始行号（从 1 开始）","required":false},{"name":"end_line","type":"number","description":"可选的结束行号（从 1 开始，含该行）","required":false}]
```

## http://10.203.247.48:3001/v2/tools/list_files

功能：列出仓库中指定目录下的文件和目录。

场景：模型需要了解项目目录结构，或在未知文件路径时缩小检索范围。

调用示例：`POST http://10.203.247.48:3001/v2/tools/list_files`，请求体：`{"repo_id":"order-service","path":"src/main/java","depth":2}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"path","type":"string","description":"可选的仓库相对目录或文件路径","required":false},{"name":"depth","type":"number","description":"可选的目录遍历深度","required":false},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/git_query

功能：执行受限的只读 Git 查询。

场景：模型需要查看近期提交、历史变更或版本差异时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/git_query`，请求体：`{"repo_id":"order-service","git_args":"[\"log\",\"--oneline\",\"-20\"]"}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"git_args","type":"string","description":"JSON 字符串数组；只允许只读 Git 参数，例如：[\"log\",\"--oneline\",\"-20\"]","required":true}]
```

## http://10.203.247.48:3001/v2/tools/search_symbols

功能：按名称搜索仓库中的 Java 符号。每项包含 `name`、可读的 `kind`、LSP `kind_code`、`container`（所属类型或包）及精确位置。

场景：模型只知道类、方法或字段名称，不知道其具体文件和位置时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/search_symbols`，请求体：`{"repo_id":"order-service","query":"createOrder"}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"query","type":"string","description":"要查找的符号名称","required":true},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/tools/get_file_symbols

功能：获取一个文件内的符号列表。

场景：模型需要快速理解文件包含哪些类、方法和字段时使用。

调用示例：`POST http://10.203.247.48:3001/v2/tools/get_file_symbols`，请求体：`{"repo_id":"order-service","file":"src/main/java/com/acme/OrderService.java"}`。

字段列表
```json
[{"name":"repo_id","type":"string","description":"受控仓库 ID，例如：order-service","required":true},{"name":"file","type":"string","description":"要查询符号的仓库相对文件路径","required":true},{"name":"limit","type":"number","description":"可选的单次返回结果上限","required":false}]
```

## http://10.203.247.48:3001/v2/repositories/{repo_id}/status

功能：查询受控仓库当前的 Git 修订版本和 JDT LS 会话状态。

场景：调用代码语义工具前，需要确认仓库是否已就绪或排查仓库同步状态时使用。

调用示例：`GET http://10.203.247.48:3001/v2/repositories/order-service/status`。

`GET`；无请求字段。路径参数 `repo_id` 为 `string`，表示受控仓库 ID。

## http://10.203.247.48:3001/v2/repositories/{repo_id}/refresh

功能：关闭指定仓库当前的 JDT LS 会话，使后续语义查询重新初始化索引。

场景：受控仓库已被部署流程更新后，需要刷新语言服务缓存时使用。

调用示例：`POST http://10.203.247.48:3001/v2/repositories/order-service/refresh`。

`POST`；无请求字段。路径参数 `repo_id` 为 `string`，表示需要刷新 JDT LS 会话的受控仓库 ID。
