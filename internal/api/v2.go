package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/example/code-context/internal/v2"
)

var v2Names = []string{"build_context", "impact", "change_context", "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_call_graph", "search_symbols", "get_file_symbols", "search_text", "read_file", "list_files", "git_query"}

func v2Known(name string) bool {
	for _, n := range v2Names {
		if n == name {
			return true
		}
	}
	return false
}
func (s *Server) registerV2(mux *http.ServeMux) {
	mux.HandleFunc("GET /v2/tools", s.v2Schemas)
	mux.HandleFunc("POST /v2/tools/", s.v2Tool)
	mux.HandleFunc("GET /v2/repositories/{repo_id}/status", s.v2Status)
	mux.HandleFunc("POST /v2/repositories/{repo_id}/refresh-index", s.v2Refresh)
}
func (s *Server) v2Schemas(w http.ResponseWriter, r *http.Request) {
	items := make([]map[string]any, 0, len(v2Names))
	for _, name := range v2Names {
		items = append(items, map[string]any{"name": name, "input_schema": v2Schema(name, s.maxBatch), "description": v2Description(name)})
	}
	write(w, 200, map[string]any{"tools": items})
}
func v2Description(name string) string {
	switch name {
	case "build_context":
		return "Get code context in one request. Start here for natural-language questions or a known symbol."
	case "impact":
		return "Analyze the effect of changing a positioned symbol."
	case "change_context":
		return "Summarize changes against a Git base branch with CodeGraph PR context."
	default:
		return "Code Context v2 precise or lower-level tool: " + name
	}
}
func v2Schema(name string, maxBatch int) map[string]any {
	props := map[string]any{"repo_id": map[string]any{"type": "string"}}
	add := func(keys ...string) {
		for _, key := range keys {
			switch key {
			case "limit", "depth", "context_lines", "start_line", "end_line", "token_budget":
				props[key] = map[string]any{"type": "integer", "minimum": 0}
			case "line", "column":
				props[key] = map[string]any{"type": "integer", "minimum": 1}
			case "require_fresh_index", "staged":
				props[key] = map[string]any{"type": "boolean"}
			case "globs", "git_args":
				props[key] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
			case "symbol":
				props[key] = map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "file": map[string]any{"type": "string"}, "line": map[string]any{"type": "integer", "minimum": 1}, "column": map[string]any{"type": "integer", "minimum": 1}}}
			default:
				props[key] = map[string]any{"type": "string"}
			}
		}
	}
	required := []string{"repo_id"}
	switch name {
	case "build_context":
		add("intent", "question", "symbol", "token_budget", "precision", "require_fresh_index")
		required = append(required, "intent")
	case "impact":
		add("symbol", "change_kind", "precision", "token_budget", "require_fresh_index")
		required = append(required, "symbol")
	case "change_context":
		add("base_branch", "head", "format", "staged", "token_budget", "require_fresh_index")
		required = append(required, "base_branch")
	case "get_call_graph", "find_definition", "find_references", "find_overrides", "get_type_hierarchy":
		add("file", "line", "column", "depth", "direction", "token_budget", "require_fresh_index")
		required = append(required, "file", "line", "column")
	case "get_file_symbols":
		add("file", "limit")
		required = append(required, "file")
	case "search_symbols":
		add("query", "limit", "token_budget", "require_fresh_index")
		required = append(required, "query")
	case "search_text":
		add("query", "path", "globs", "limit", "context_lines")
		required = append(required, "query")
	case "read_file":
		add("path", "start_line", "end_line")
		required = append(required, "path")
	case "list_files":
		add("path", "depth", "limit")
	case "git_query":
		add("git_args")
		required = append(required, "git_args")
	}
	return map[string]any{"type": "object", "properties": map[string]any{"requests": map[string]any{"type": "array", "minItems": 1, "maxItems": maxBatch, "items": map[string]any{"type": "object", "properties": props, "required": required}}}, "required": []string{"requests"}}
}
func (s *Server) v2Tool(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/v2/tools/")
	if !v2Known(name) {
		fail(w, 404, "unknown_tool", "unknown v2 tool")
		return
	}
	var batch struct {
		Requests []v2.Request `json:"requests"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&batch); err != nil {
		fail(w, 400, "invalid_json", err.Error())
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(w, 400, "invalid_json", "request body must contain exactly one JSON object")
		return
	}
	if len(batch.Requests) == 0 || len(batch.Requests) > s.maxBatch {
		fail(w, 400, "invalid_requests", fmt.Sprintf("requests must contain 1 to %d items", s.maxBatch))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	results := make([]v2.Result, len(batch.Requests))
	var wg sync.WaitGroup
	for i, req := range batch.Requests {
		wg.Add(1)
		go func(i int, req v2.Request) { defer wg.Done(); results[i] = s.v2.Execute(ctx, name, req) }(i, req)
	}
	wg.Wait()
	write(w, 200, map[string]any{"results": results})
}
func (s *Server) v2Status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	status, err := s.v2.Status(ctx, r.PathValue("repo_id"))
	if err != nil {
		fail(w, 500, "repository_error", err.Error())
		return
	}
	write(w, 200, map[string]any{"data": status})
}
func (s *Server) v2Refresh(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	status, err := s.v2.Refresh(ctx, r.PathValue("repo_id"))
	if err != nil {
		fail(w, 502, "index_error", err.Error())
		return
	}
	write(w, 200, map[string]any{"data": status})
}
