package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/example/code-context/internal/tools"
)

type Server struct {
	svc      *tools.Service
	timeout  time.Duration
	maxBody  int64
	maxBatch int
}

func New(svc *tools.Service, timeout time.Duration, maxBatch int) *Server {
	if maxBatch <= 0 {
		maxBatch = 20
	}
	return &Server{svc: svc, timeout: timeout, maxBody: 1 << 20, maxBatch: maxBatch}
}

type ToolRequest struct {
	RepoID       string   `json:"repo_id"`
	File         string   `json:"file"`
	Line         int      `json:"line"`
	Column       int      `json:"column"`
	Query        string   `json:"query"`
	Path         string   `json:"path"`
	Globs        []string `json:"globs"`
	Limit        int      `json:"limit"`
	ContextLines int      `json:"context_lines"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	Staged       bool     `json:"staged"`
	Depth        int      `json:"depth"`
	Direction    string   `json:"direction"`
	TargetFile   string   `json:"target_file"`
	TargetLine   int      `json:"target_line"`
	TargetColumn int      `json:"target_column"`
	GitArgs      []string `json:"git_args"`
}

// v2ToolRequest is the v2 wire format.  Its fields are deliberately limited to
// string and number so that it can be registered by platforms which do not
// support array or boolean tool parameters.  globs and git_args are encoded as
// JSON string arrays; staged is encoded as a boolean string ("true"/"false").
type v2ToolRequest struct {
	RepoID       string `json:"repo_id"`
	File         string `json:"file"`
	Line         int    `json:"line"`
	Column       int    `json:"column"`
	Query        string `json:"query"`
	Path         string `json:"path"`
	Globs        string `json:"globs"`
	Limit        int    `json:"limit"`
	ContextLines int    `json:"context_lines"`
	StartLine    int    `json:"start_line"`
	EndLine      int    `json:"end_line"`
	Base         string `json:"base"`
	Head         string `json:"head"`
	Staged       string `json:"staged"`
	Depth        int    `json:"depth"`
	Direction    string `json:"direction"`
	TargetFile   string `json:"target_file"`
	TargetLine   int    `json:"target_line"`
	TargetColumn int    `json:"target_column"`
	GitArgs      string `json:"git_args"`
}

func (r v2ToolRequest) toToolRequest() (ToolRequest, error) {
	parseList := func(name, value string) ([]string, error) {
		if value == "" {
			return nil, nil
		}
		var values []string
		if err := json.Unmarshal([]byte(value), &values); err != nil {
			return nil, fmt.Errorf("%s must be a JSON string array", name)
		}
		return values, nil
	}
	globs, err := parseList("globs", r.Globs)
	if err != nil {
		return ToolRequest{}, err
	}
	gitArgs, err := parseList("git_args", r.GitArgs)
	if err != nil {
		return ToolRequest{}, err
	}
	staged := false
	if r.Staged != "" {
		if r.Staged != "true" && r.Staged != "false" {
			return ToolRequest{}, fmt.Errorf("staged must be the string true or false")
		}
		staged = r.Staged == "true"
	}
	return ToolRequest{RepoID: r.RepoID, File: r.File, Line: r.Line, Column: r.Column, Query: r.Query, Path: r.Path, Globs: globs, Limit: r.Limit, ContextLines: r.ContextLines, StartLine: r.StartLine, EndLine: r.EndLine, Base: r.Base, Head: r.Head, Staged: staged, Depth: r.Depth, Direction: r.Direction, TargetFile: r.TargetFile, TargetLine: r.TargetLine, TargetColumn: r.TargetColumn, GitArgs: gitArgs}, nil
}

type toolBatch struct {
	Requests []ToolRequest `json:"requests"`
}

type toolResult struct {
	Data      any            `json:"data,omitempty"`
	Truncated bool           `json:"truncated"`
	Error     *toolItemError `json:"error,omitempty"`
}

type toolItemError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.health)
	mux.HandleFunc("GET /v1/tools", s.schemas)
	mux.HandleFunc("POST /v1/tools/", s.tool)
	mux.HandleFunc("/v1/repositories/", s.repository)
	mux.HandleFunc("GET /v2/tools", s.schemasV2)
	mux.HandleFunc("POST /v2/tools/", s.toolV2)
	mux.HandleFunc("/v2/repositories/", s.repository)
	return s.middleware(mux)
}

func (s *Server) toolV2(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v2/tools/")
	if !knownTool(name) {
		fail(w, http.StatusNotFound, "unknown_tool", "unknown tool")
		return
	}
	var input v2ToolRequest
	if err := decode(w, r, &input); err != nil {
		return
	}
	req, err := input.toToolRequest()
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	data, truncated, err := s.execute(ctx, name, req)
	if err != nil {
		write(w, http.StatusOK, map[string]any{"data": nil, "truncated": false, "error": &toolItemError{Code: errorCode(err), Message: err.Error()}})
		return
	}
	data, normalizedTruncated := normalizeV2Result(name, data, v2Limit(s.svc, req.Limit))
	write(w, http.StatusOK, toolResult{Data: data, Truncated: truncated || normalizedTruncated})
}

func v2Limit(svc *tools.Service, requested int) int {
	if requested > 0 && (svc == nil || svc.MaxResults <= 0 || requested <= svc.MaxResults) {
		return requested
	}
	if svc != nil && svc.MaxResults > 0 {
		return svc.MaxResults
	}
	return 100
}

// normalizeV2Result keeps the v1 wire format intact while returning compact,
// model-friendly values from v2 and enforcing a common limit on collections.
func normalizeV2Result(name string, data any, limit int) (any, bool) {
	if name == "search_code" {
		if events, ok := data.([]map[string]any); ok {
			return normalizeSearchMatches(events, limit)
		}
	}
	if name == "search_symbols" {
		if locations, ok := data.([]tools.Location); ok {
			truncated := len(locations) > limit
			if truncated {
				locations = locations[:limit]
			}
			out := make([]v2Symbol, 0, len(locations))
			for _, location := range locations {
				out = append(out, v2Symbol{
					Name: location.Snippet, Kind: location.SymbolType, KindCode: location.SymbolKind,
					Container: location.Container, File: location.File,
					StartLine: location.StartLine, StartColumn: location.StartColumn,
					EndLine: location.EndLine, EndColumn: location.EndColumn,
				})
			}
			return out, truncated
		}
	}
	if name == "get_file_symbols" {
		items := flattenFileSymbols(data)
		if len(items) > limit {
			return items[:limit], true
		}
		return items, false
	}
	v := reflect.ValueOf(data)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Len() <= limit {
		return data, false
	}
	return v.Slice(0, limit).Interface(), true
}

func flattenFileSymbols(data any) []map[string]any {
	out := []map[string]any{}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			item := make(map[string]any, len(typed))
			for key, value := range typed {
				if key != "children" {
					item[key] = value
				}
			}
			out = append(out, item)
			visit(typed["children"])
		}
	}
	visit(data)
	return out
}

type v2Symbol struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	KindCode    int    `json:"kind_code"`
	Container   string `json:"container,omitempty"`
	File        string `json:"file"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
}

type v2SearchMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Match   string `json:"match"`
	Snippet string `json:"snippet"`
}

func normalizeSearchMatches(events []map[string]any, limit int) ([]v2SearchMatch, bool) {
	type fileGroup struct {
		lines   map[int]string
		matches []v2SearchMatch
	}
	groups := map[string]*fileGroup{}
	order := []string{}
	for _, event := range events {
		typ, _ := event["type"].(string)
		if typ != "match" && typ != "context" {
			continue
		}
		payload, _ := event["data"].(map[string]any)
		file := nestedText(payload, "path")
		line := jsonInt(payload["line_number"])
		if file == "" || line < 1 {
			continue
		}
		group := groups[file]
		if group == nil {
			group = &fileGroup{lines: map[int]string{}}
			groups[file] = group
			order = append(order, file)
		}
		group.lines[line] = strings.TrimSuffix(nestedText(payload, "lines"), "\n")
		if typ == "match" {
			column, match := 1, ""
			if raw, ok := payload["submatches"].([]any); ok && len(raw) > 0 {
				if sub, ok := raw[0].(map[string]any); ok {
					column = jsonInt(sub["start"]) + 1
					match = nestedText(sub, "match")
				}
			}
			group.matches = append(group.matches, v2SearchMatch{File: file, Line: line, Column: column, Match: match})
		}
	}
	out := []v2SearchMatch{}
	for _, file := range order {
		group := groups[file]
		lineNumbers := make([]int, 0, len(group.lines))
		for line := range group.lines {
			lineNumbers = append(lineNumbers, line)
		}
		sort.Ints(lineNumbers)
		var snippet strings.Builder
		for _, line := range lineNumbers {
			snippet.WriteString(strconv.Itoa(line))
			snippet.WriteString(": ")
			snippet.WriteString(group.lines[line])
			snippet.WriteByte('\n')
		}
		text := strings.TrimSuffix(snippet.String(), "\n")
		for _, match := range group.matches {
			match.Snippet = text
			out = append(out, match)
		}
	}
	if len(out) > limit {
		return out[:limit], true
	}
	return out, false
}

func nestedText(value map[string]any, key string) string {
	nested, _ := value[key].(map[string]any)
	text, _ := nested["text"].(string)
	return text
}

func jsonInt(value any) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		v, _ := strconv.Atoi(n.String())
		return v
	}
	return 0
}
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		next.ServeHTTP(w, r)
	})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}
func (s *Server) tool(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v1/tools/")
	if !knownTool(name) {
		fail(w, http.StatusNotFound, "unknown_tool", "unknown tool")
		return
	}
	var batch toolBatch
	if err := decode(w, r, &batch); err != nil {
		return
	}
	if len(batch.Requests) == 0 || len(batch.Requests) > s.maxBatch {
		fail(w, http.StatusBadRequest, "invalid_requests", fmt.Sprintf("requests must contain 1 to %d items", s.maxBatch))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	results := make([]toolResult, len(batch.Requests))
	var wg sync.WaitGroup
	for i, req := range batch.Requests {
		wg.Add(1)
		go func(i int, req ToolRequest) {
			defer wg.Done()
			data, truncated, err := s.execute(ctx, name, req)
			if err != nil {
				results[i] = toolResult{Error: &toolItemError{Code: errorCode(err), Message: err.Error()}}
				return
			}
			results[i] = toolResult{Data: data, Truncated: truncated}
		}(i, req)
	}
	wg.Wait()
	write(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) execute(ctx context.Context, name string, req ToolRequest) (any, bool, error) {
	if req.RepoID == "" {
		return nil, false, fmt.Errorf("repo_id is required")
	}
	semanticTool := strings.HasPrefix(name, "find_") || name == "get_symbol_context" || name == "get_call_graph" || name == "trace_call_path" || name == "get_type_hierarchy"
	if semanticTool && (req.File == "" || req.Line < 1 || req.Column < 1) {
		return nil, false, fmt.Errorf("file, line, and column (1-based) are required")
	}
	if name == "get_file_symbols" && req.File == "" {
		return nil, false, fmt.Errorf("file is required")
	}
	if (name == "search_code" || name == "search_symbols") && req.Query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	if name == "read_file" && req.Path == "" {
		return nil, false, fmt.Errorf("path is required")
	}
	var data any
	var truncated bool
	var err error
	switch name {
	case "search_code":
		data, truncated, err = s.svc.Search(ctx, req.RepoID, req.Query, req.Path, req.Globs, req.Limit, req.ContextLines)
	case "find_definition":
		data, err = s.svc.Semantic(ctx, req.RepoID, req.File, req.Line, req.Column, "textDocument/definition")
	case "find_references":
		data, err = s.svc.Semantic(ctx, req.RepoID, req.File, req.Line, req.Column, "textDocument/references")
	case "find_overrides":
		data, err = s.svc.Semantic(ctx, req.RepoID, req.File, req.Line, req.Column, "textDocument/implementation")
	case "get_type_hierarchy":
		data, truncated, err = s.svc.TypeHierarchy(ctx, req.RepoID, req.File, req.Line, req.Column, req.Depth, req.Direction)
	case "get_call_graph":
		data, truncated, err = s.svc.CallGraph(ctx, req.RepoID, req.File, req.Line, req.Column, req.Depth, req.Direction)
	case "trace_call_path":
		data, truncated, err = s.svc.TraceCallPath(ctx, req.RepoID, req.File, req.Line, req.Column, req.TargetFile, req.TargetLine, req.TargetColumn, req.Depth)
	case "search_symbols":
		data, err = s.svc.Symbols(ctx, req.RepoID, req.Query)
	case "get_file_symbols":
		data, err = s.fileSymbols(ctx, req.RepoID, req.File)
	case "get_symbol_context":
		data, err = s.svc.SymbolContext(ctx, req.RepoID, req.File, req.Line, req.Column)
	case "read_file":
		data, err = s.svc.Read(req.RepoID, req.Path, req.StartLine, req.EndLine)
	case "list_files":
		data, err = s.svc.ListFiles(req.RepoID, req.Path, req.Depth)
	case "git_query":
		var d string
		d, truncated, err = s.svc.GitQuery(ctx, req.RepoID, req.GitArgs)
		data = map[string]any{"output": d}
	}
	return data, truncated, err
}

func knownTool(name string) bool {
	for _, candidate := range []string{"search_code", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_call_graph", "trace_call_path", "read_file", "list_files", "git_query", "search_symbols", "get_file_symbols", "get_symbol_context"} {
		if name == candidate {
			return true
		}
	}
	return false
}

func errorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "tool_error"
}
func (s *Server) fileSymbols(ctx context.Context, repoID, file string) (any, error) {
	repo, err := s.svc.Repos.Get(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.svc.Repos.File(repo, file)
	if err != nil {
		return nil, err
	}
	return s.svc.JDT.FileSymbols(ctx, repoID, repo.Path, full)
}
func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/repositories/")
	path = strings.TrimPrefix(path, "/v2/repositories/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "status" && r.Method == http.MethodGet {
		ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
		defer cancel()
		data, err := s.svc.Status(ctx, parts[0])
		if err != nil {
			fail(w, status(err), "repository_error", err.Error())
			return
		}
		write(w, http.StatusOK, map[string]any{"data": data})
		return
	}
	if len(parts) != 2 || parts[1] != "refresh" {
		fail(w, 404, "not_found", "not found")
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if err := s.svc.Refresh(parts[0]); err != nil {
		fail(w, status(err), "repository_error", err.Error())
		return
	}
	write(w, 200, map[string]any{"status": "refresh_scheduled"})
}

func (s *Server) schemasV2(w http.ResponseWriter, r *http.Request) {
	names := []string{"search_code", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_call_graph", "trace_call_path", "read_file", "list_files", "git_query", "search_symbols", "get_file_symbols", "get_symbol_context"}
	tools := make([]map[string]any, 0, len(names))
	for _, n := range names {
		tools = append(tools, map[string]any{"name": n, "description": description(n), "parameters": v2ToolParameters(n)})
	}
	write(w, http.StatusOK, map[string]any{"tools": tools})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, 400, "invalid_json", err.Error())
		return err
	}
	return nil
}
func status(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return 504
	}
	if strings.Contains(err.Error(), "unknown repository") || strings.Contains(err.Error(), "path") || strings.Contains(err.Error(), "invalid") {
		return 400
	}
	return 502
}
func fail(w http.ResponseWriter, status int, code, message string) {
	write(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func write(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) schemas(w http.ResponseWriter, r *http.Request) {
	names := []string{"search_code", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_call_graph", "trace_call_path", "read_file", "list_files", "git_query", "search_symbols", "get_file_symbols", "get_symbol_context"}
	tools := make([]map[string]any, 0, len(names))
	for _, n := range names {
		tools = append(tools, map[string]any{"name": n, "description": description(n), "input_schema": toolInputSchema(n)})
	}
	write(w, 200, map[string]any{"tools": tools})
}
func description(name string) string {
	return fmt.Sprintf("Code context tool: %s. Submit one or more requests; use repository-relative paths and 1-based positions.", name)
}

func toolInputSchema(name string) map[string]any {
	properties := map[string]any{
		"repo_id":       map[string]string{"type": "string"},
		"file":          map[string]string{"type": "string"},
		"line":          map[string]any{"type": "integer", "minimum": 1},
		"column":        map[string]any{"type": "integer", "minimum": 1},
		"query":         map[string]string{"type": "string"},
		"path":          map[string]string{"type": "string"},
		"globs":         map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
		"limit":         map[string]any{"type": "integer", "minimum": 1},
		"context_lines": map[string]any{"type": "integer", "minimum": 0},
		"start_line":    map[string]any{"type": "integer", "minimum": 1},
		"end_line":      map[string]any{"type": "integer", "minimum": 1},
		"base":          map[string]string{"type": "string"},
		"head":          map[string]string{"type": "string"},
		"staged":        map[string]string{"type": "boolean"},
		"depth":         map[string]any{"type": "integer", "minimum": 1},
		"direction":     map[string]any{"type": "string", "enum": []string{"outgoing", "incoming", "subtypes", "supertypes"}},
		"target_file":   map[string]string{"type": "string"},
		"target_line":   map[string]any{"type": "integer", "minimum": 1},
		"target_column": map[string]any{"type": "integer", "minimum": 1},
		"git_args":      map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
	}
	required := []string{"repo_id"}
	if strings.HasPrefix(name, "find_") || name == "get_symbol_context" || name == "get_call_graph" || name == "trace_call_path" || name == "get_type_hierarchy" {
		required = append(required, "file", "line", "column")
	}
	if name == "search_code" || name == "search_symbols" {
		required = append(required, "query")
	}
	if name == "read_file" {
		required = append(required, "path")
	}
	if name == "get_file_symbols" {
		required = append(required, "file")
	}
	if name == "git_query" {
		required = append(required, "git_args")
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requests": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object", "properties": properties, "required": required}},
		},
		"required": []string{"requests"},
	}
}

type v2Parameter struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

func v2ToolParameters(name string) []v2Parameter {
	params := []v2Parameter{{"repo_id", "string", "受控仓库 ID，例如：order-service", true}}
	add := func(name, typ, description string, required bool) {
		params = append(params, v2Parameter{name, typ, description, required})
	}
	switch name {
	case "search_code":
		add("query", "string", "要检索的文本或 ripgrep 正则表达式", true)
		add("path", "string", "可选的仓库相对文件路径范围", false)
		add("globs", "string", "可选的 JSON 字符串数组，例如：[\"*.java\"]", false)
		add("limit", "number", "可选的单次返回结果上限", false)
		add("context_lines", "number", "可选的命中行前后上下文行数", false)
	case "search_symbols":
		add("query", "string", "要查找的符号名称", true)
		add("limit", "number", "可选的单次返回结果上限", false)
	case "read_file":
		add("path", "string", "要读取的仓库相对文件路径", true)
		add("start_line", "number", "可选的起始行号（从 1 开始）", false)
		add("end_line", "number", "可选的结束行号（从 1 开始，含该行）", false)
	case "list_files":
		add("path", "string", "可选的仓库相对目录或文件路径", false)
		add("depth", "number", "可选的目录遍历深度", false)
		add("limit", "number", "可选的单次返回结果上限", false)
	case "git_query":
		add("git_args", "string", "JSON 字符串数组；只允许只读 Git 参数，例如：[\"log\",\"--oneline\",\"-20\"]", true)
	case "get_file_symbols":
		addPositionParameters(&params, false)
		params = removeV2Parameter(params, "line", "column")
		add("limit", "number", "可选的单次返回结果上限", false)
	case "find_definition", "find_references", "find_overrides", "get_symbol_context":
		addPositionParameters(&params, true)
		if name != "get_symbol_context" {
			add("limit", "number", "可选的单次返回结果上限", false)
		}
	case "get_type_hierarchy":
		addPositionParameters(&params, true)
		add("depth", "number", "可选的层级遍历深度", false)
		add("direction", "string", "可选：subtypes 或 supertypes", false)
		add("limit", "number", "可选的单次返回结果上限", false)
	case "get_call_graph":
		addPositionParameters(&params, true)
		add("depth", "number", "可选的调用图遍历深度", false)
		add("direction", "string", "可选：outgoing 或 incoming", false)
	case "trace_call_path":
		addPositionParameters(&params, true)
		add("target_file", "string", "目标符号所在的仓库相对文件路径", true)
		add("target_line", "number", "目标符号行号（从 1 开始）", true)
		add("target_column", "number", "目标符号列号（从 1 开始）", true)
		add("depth", "number", "可选的最大调用路径深度", false)
	}
	return params
}

func addPositionParameters(params *[]v2Parameter, includePosition bool) {
	*params = append(*params, v2Parameter{"file", "string", "符号所在的仓库相对文件路径", true})
	if includePosition {
		*params = append(*params, v2Parameter{"line", "number", "符号行号（从 1 开始）", true}, v2Parameter{"column", "number", "符号列号（从 1 开始）", true})
	}
}

func removeV2Parameter(params []v2Parameter, names ...string) []v2Parameter {
	remove := map[string]bool{}
	for _, name := range names {
		remove[name] = true
	}
	result := params[:0]
	for _, param := range params {
		if !remove[param.Name] {
			result = append(result, param)
		}
	}
	return result
}
