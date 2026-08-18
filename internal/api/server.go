package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/example/code-context/internal/lsp"
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
	return s.middleware(mux)
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
	semanticTool := strings.HasPrefix(name, "find_") || name == "get_hover"
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
	case "find_implementations":
		data, err = s.svc.Semantic(ctx, req.RepoID, req.File, req.Line, req.Column, "textDocument/implementation")
	case "find_references":
		data, err = s.svc.Semantic(ctx, req.RepoID, req.File, req.Line, req.Column, "textDocument/references")
	case "find_callers":
		data, truncated, err = s.svc.CallHierarchy(ctx, req.RepoID, req.File, req.Line, req.Column, req.Depth, true)
	case "find_callees":
		data, truncated, err = s.svc.CallHierarchy(ctx, req.RepoID, req.File, req.Line, req.Column, req.Depth, false)
	case "search_symbols":
		data, err = s.svc.Symbols(ctx, req.RepoID, req.Query)
	case "get_file_symbols":
		data, err = s.fileSymbols(ctx, req.RepoID, req.File)
	case "get_hover":
		data, err = s.hover(ctx, req.RepoID, req.File, req.Line, req.Column)
	case "read_file":
		data, err = s.svc.Read(req.RepoID, req.Path, req.StartLine, req.EndLine)
	case "list_files":
		data, err = s.svc.ListFiles(req.RepoID, req.Path, req.Depth)
	case "get_git_diff":
		var d string
		d, truncated, err = s.svc.Diff(ctx, req.RepoID, req.Base, req.Head, req.Path, req.Staged)
		data = map[string]any{"diff": d}
	}
	return data, truncated, err
}

func knownTool(name string) bool {
	for _, candidate := range []string{"search_code", "find_definition", "find_implementations", "find_references", "find_callers", "find_callees", "read_file", "list_files", "get_git_diff", "search_symbols", "get_file_symbols", "get_hover"} {
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
func (s *Server) hover(ctx context.Context, repoID, file string, line, col int) (any, error) {
	repo, err := s.svc.Repos.Get(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.svc.Repos.File(repo, file)
	if err != nil {
		return nil, err
	}
	return s.svc.JDT.Hover(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: col - 1})
}
func (s *Server) repository(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/repositories/"), "/")
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
	names := []string{"search_code", "find_definition", "find_implementations", "find_references", "find_callers", "find_callees", "read_file", "list_files", "get_git_diff", "search_symbols", "get_file_symbols", "get_hover"}
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
	}
	required := []string{"repo_id"}
	if strings.HasPrefix(name, "find_") || name == "get_hover" {
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
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"requests": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "object", "properties": properties, "required": required}},
		},
		"required": []string{"requests"},
	}
}
