package v2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/example/code-context/internal/codegraph"
	"github.com/example/code-context/internal/lsp"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/runner"
	"github.com/example/code-context/internal/tools"
)

type Symbol struct {
	Name   string `json:"name,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}
type choiceError struct {
	code       string
	candidates any
}

func (e *choiceError) Error() string { return e.code }

type Request struct {
	RepoID            string   `json:"repo_id"`
	Intent            string   `json:"intent,omitempty"`
	Question          string   `json:"question,omitempty"`
	Symbol            *Symbol  `json:"symbol,omitempty"`
	TokenBudget       int      `json:"token_budget,omitempty"`
	Precision         string   `json:"precision,omitempty"`
	RequireFreshIndex *bool    `json:"require_fresh_index,omitempty"`
	ChangeKind        string   `json:"change_kind,omitempty"`
	BaseBranch        string   `json:"base_branch,omitempty"`
	Head              string   `json:"head,omitempty"`
	Format            string   `json:"format,omitempty"`
	Staged            bool     `json:"staged,omitempty"`
	File              string   `json:"file,omitempty"`
	Line              int      `json:"line,omitempty"`
	Column            int      `json:"column,omitempty"`
	Query             string   `json:"query,omitempty"`
	Path              string   `json:"path,omitempty"`
	Globs             []string `json:"globs,omitempty"`
	Limit             int      `json:"limit,omitempty"`
	ContextLines      int      `json:"context_lines,omitempty"`
	StartLine         int      `json:"start_line,omitempty"`
	EndLine           int      `json:"end_line,omitempty"`
	Depth             int      `json:"depth,omitempty"`
	Direction         string   `json:"direction,omitempty"`
	GitArgs           []string `json:"git_args,omitempty"`
}
type ItemError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	Candidates any    `json:"candidates,omitempty"`
}
type Result struct {
	Data      any        `json:"data,omitempty"`
	Truncated bool       `json:"truncated"`
	Warnings  []string   `json:"warnings"`
	Meta      any        `json:"meta"`
	Error     *ItemError `json:"error,omitempty"`
}
type Service struct {
	Legacy         *tools.Service
	Graph          *codegraph.Manager
	MaxResults     int
	MaxTokenBudget int
}
type RepositoryStatus struct {
	codegraph.Status
	JDTLSActive bool `json:"jdtls_active"`
}

var fileURI = regexp.MustCompile(`file://[^\s"'<>),]+`)

func (s *Service) Status(ctx context.Context, id string) (RepositoryStatus, error) {
	status, err := s.Graph.Snapshot(ctx, id)
	if err != nil {
		return RepositoryStatus{}, err
	}
	active := false
	if s.Legacy.JDT != nil {
		active = s.Legacy.JDT.Active(id)
	}
	return RepositoryStatus{Status: status, JDTLSActive: active}, nil
}
func (s *Service) Refresh(ctx context.Context, id string) (RepositoryStatus, error) {
	if _, err := s.Graph.Refresh(ctx, id); err != nil {
		return RepositoryStatus{}, err
	}
	return s.Status(ctx, id)
}
func (s *Service) Execute(ctx context.Context, name string, r Request) (res Result) {
	started := time.Now()
	res = Result{Warnings: []string{}, Meta: map[string]any{"repo_id": r.RepoID, "source": source(name), "duration_ms": int64(0)}}
	defer func() {
		if meta, ok := res.Meta.(map[string]any); ok {
			meta["duration_ms"] = time.Since(started).Milliseconds()
		}
	}()
	if r.RepoID == "" {
		res.Error = bad("repo_id is required")
		return res
	}
	repo, err := s.Legacy.Repos.Get(r.RepoID)
	if err != nil {
		res.Error = bad(err.Error())
		return res
	}
	status, err := s.Graph.Snapshot(ctx, r.RepoID)
	if err != nil {
		res.Error = classify(err)
		return res
	}
	meta := map[string]any{"repo_id": r.RepoID, "source": source(name), "repo_revision": status.RepoRevision, "index_revision": status.IndexRevision, "index_state": status.IndexState, "working_tree_dirty": status.WorkingTreeDirty, "duration_ms": int64(0)}
	if status.IndexedAt != nil {
		meta["indexed_at"] = status.IndexedAt
	}
	if status.ToolSchemaFingerprint != "" {
		meta["tool_schema_fingerprint"] = status.ToolSchemaFingerprint
	}
	res.Meta = meta
	if r.Limit < 0 || r.Limit > s.MaxResults || r.TokenBudget < 0 || r.TokenBudget > s.MaxTokenBudget {
		res.Error = bad("limit or token_budget exceeds configured maximum")
		return res
	}
	if r.TokenBudget == 0 {
		r.TokenBudget = 6000
	}
	if r.Precision == "" {
		r.Precision = "auto"
	}
	if r.Precision != "auto" && r.Precision != "java_exact" {
		res.Error = bad("precision must be auto or java_exact")
		return res
	}
	graphTool := name == "build_context" || name == "impact" || name == "change_context" || name == "get_call_graph" || name == "search_symbols"
	if graphTool {
		fresh := r.RequireFreshIndex == nil || *r.RequireFreshIndex
		if status.IndexState != "ready" {
			if fresh {
				code := "index_stale"
				if status.IndexState == "unavailable" || status.IndexState == "unknown" {
					code = "index_unavailable"
				}
				if status.IndexState == "building" {
					code = "index_building"
				}
				res.Error = &ItemError{Code: code, Message: "CodeGraph index is not current", Retryable: true}
				return res
			}
			res.Warnings = append(res.Warnings, "index_stale")
			if status.WorkingTreeDirty {
				res.Warnings = append(res.Warnings, "working_tree_unindexed")
			}
		}
	}
	var data any
	var truncated bool
	switch name {
	case "build_context":
		data, truncated, err = s.buildContext(ctx, repo, r)
	case "impact":
		data, truncated, err = s.impact(ctx, repo, r)
	case "change_context":
		data, truncated, err = s.changeContext(ctx, repo, r)
	case "get_call_graph":
		data, truncated, err = s.callGraph(ctx, repo, r)
	case "search_symbols":
		data, truncated, err = s.symbolSearch(ctx, repo, r)
	case "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_file_symbols":
		data, err = s.java(ctx, repo, name, r)
	case "search_text":
		data, truncated, err = s.Legacy.Search(ctx, r.RepoID, r.Query, r.Path, r.Globs, r.Limit, r.ContextLines)
	case "read_file":
		data, err = s.Legacy.Read(r.RepoID, r.Path, r.StartLine, r.EndLine)
	case "list_files":
		data, err = s.Legacy.ListFiles(r.RepoID, r.Path, r.Depth)
	case "git_query":
		var output string
		output, truncated, err = s.Legacy.GitQuery(ctx, r.RepoID, r.GitArgs)
		data = map[string]any{"output": output}
	default:
		err = fmt.Errorf("unknown tool")
	}
	if err != nil {
		var choice *choiceError
		if errors.As(err, &choice) {
			res.Error = &ItemError{Code: choice.code, Message: "Select a symbol by file, line, and column", Candidates: choice.candidates}
		} else {
			res.Error = classify(err)
		}
		return res
	}
	if graphTool {
		after, e := s.Graph.Snapshot(ctx, r.RepoID)
		if e != nil {
			res.Error = classify(e)
			return res
		}
		if after.RepoRevision != status.RepoRevision || after.IndexState != status.IndexState || after.WorkingTreeDirty != status.WorkingTreeDirty {
			meta["repo_revision"] = after.RepoRevision
			meta["index_revision"] = after.IndexRevision
			meta["index_state"] = after.IndexState
			meta["working_tree_dirty"] = after.WorkingTreeDirty
			if r.RequireFreshIndex == nil || *r.RequireFreshIndex {
				res.Error = &ItemError{Code: "index_stale", Message: "repository or index changed during request", Retryable: true}
				return res
			}
			res.Warnings = append(res.Warnings, "index_changed_during_request")
		}
		providerWarning, fallback := providerFlags(data)
		if fallback && (r.Symbol != nil || name == "get_call_graph") {
			res.Error = &ItemError{Code: "symbol_not_found", Message: "CodeGraph used a neighboring symbol instead of the requested position"}
			return res
		}
		if providerWarning {
			res.Warnings = append(res.Warnings, "codegraph_warning")
		}
	}
	if name == "get_file_symbols" {
		data = relativePaths(repo, data)
	}
	if (name == "build_context" || name == "impact") && r.Precision == "java_exact" {
		meta["source"] = "composite"
	}
	if name == "get_type_hierarchy" {
		if m, ok := data.(map[string]any); ok {
			if t, ok := m["truncated"].(bool); ok {
				truncated = t
			}
		}
	}
	if r.Limit > 0 {
		var extra bool
		data, extra = limitData(data, r.Limit)
		truncated = truncated || extra
	}
	res.Data = data
	res.Truncated = truncated
	if truncated {
		if graphTool {
			res.Warnings = append(res.Warnings, "budget_exhausted")
		} else {
			res.Warnings = append(res.Warnings, "result_truncated")
		}
	}
	return res
}
func providerFlags(v any) (warning, fallback bool) {
	var walk func(any)
	walk = func(value any) {
		switch x := value.(type) {
		case map[string]any:
			for k, item := range x {
				if k == "warning" {
					if s, ok := item.(string); ok && s != "" {
						warning = true
					}
				}
				if k == "usedFallback" || k == "used_fallback" {
					if b, ok := item.(bool); ok && b {
						fallback = true
					}
				}
				walk(item)
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return
}
func limitData(data any, limit int) (any, bool) {
	if m, ok := data.(map[string]any); ok {
		for _, key := range []string{"locations", "results"} {
			if value, found := m[key]; found {
				clipped, cut := limitData(value, limit)
				if cut {
					m[key] = clipped
					return m, true
				}
			}
		}
		return data, false
	}
	v := reflect.ValueOf(data)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Len() <= limit {
		return data, false
	}
	return v.Slice(0, limit).Interface(), true
}
func source(name string) string {
	switch name {
	case "find_definition", "find_references", "find_overrides", "get_type_hierarchy", "get_file_symbols":
		return "jdtls"
	case "search_text":
		return "rg"
	case "read_file", "list_files":
		return "go"
	case "git_query":
		return "git"
	default:
		return "codegraph"
	}
}
func bad(msg string) *ItemError { return &ItemError{Code: "invalid_request", Message: msg} }
func classify(err error) *ItemError {
	if err == nil {
		return nil
	}
	code := "tool_error"
	retry := false
	switch {
	case errors.Is(err, codegraph.ErrUnavailable):
		code = "codegraph_unavailable"
		retry = true
	case errors.Is(err, codegraph.ErrBusy):
		code = "busy"
		retry = true
	case errors.Is(err, codegraph.ErrCapability):
		code = "unsupported_capability"
	case errors.Is(err, context.DeadlineExceeded):
		code = "timeout"
		retry = true
	case strings.Contains(err.Error(), "unknown repository"), strings.Contains(err.Error(), "required"), strings.Contains(err.Error(), "invalid"), strings.Contains(err.Error(), "unsupported_language"):
		code = "invalid_request"
	case strings.Contains(err.Error(), "unsupported change scope"):
		code = "unsupported_change_scope"
	case strings.Contains(err.Error(), "jdtls command is not configured"), strings.Contains(err.Error(), "jdtls unavailable"):
		code = "jdtls_unavailable"
		retry = true
	}
	return &ItemError{Code: code, Message: err.Error(), Retryable: retry}
}
func (s *Service) graph(ctx context.Context, repo repository.Repository, name string, proposed map[string]any, budget int) (any, bool, error) {
	tool, err := s.Graph.Tool(ctx, repo.ID, name)
	if err != nil {
		return nil, false, err
	}
	args := map[string]any{}
	for _, key := range requiredInput(name) {
		if _, ok := tool.InputSchema.Properties[key]; !ok {
			return nil, false, fmt.Errorf("%w: %s lacks %s", codegraph.ErrCapability, name, key)
		}
	}
	for k, v := range proposed {
		if _, ok := tool.InputSchema.Properties[k]; ok {
			args[k] = v
		}
	}
	for _, required := range tool.InputSchema.Required {
		if _, ok := args[required]; !ok {
			return nil, false, fmt.Errorf("%w: %s requires %s", codegraph.ErrCapability, name, required)
		}
	}
	raw, err := s.Graph.Call(ctx, repo.ID, name, args)
	if err != nil {
		return nil, false, err
	}
	var output any
	if len(raw.StructuredContent) > 0 {
		if json.Unmarshal(raw.StructuredContent, &output) != nil {
			return nil, false, fmt.Errorf("invalid CodeGraph structured content")
		}
	} else if len(raw.Content) == 1 {
		t := raw.Content[0].Text
		if json.Unmarshal([]byte(t), &output) != nil {
			output = t
		}
	} else {
		parts := make([]string, 0, len(raw.Content))
		for _, part := range raw.Content {
			if part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		output = strings.Join(parts, "\n")
	}
	if err = validatePaths(repo, output); err != nil {
		return nil, false, err
	}
	output = relativePaths(repo, output)
	// Keep the provider's context as evidence. A bounded UTF-8 text segment is
	// returned if the provider has no native token-budget parameter.
	return budgetOutput(output, budget)
}
func relativePaths(repo repository.Repository, v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = relativePaths(repo, item)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = relativePaths(repo, item)
		}
		return out
	case string:
		root := filepath.ToSlash(repo.Path)
		x = strings.ReplaceAll(x, "file://"+root+"/", "")
		x = strings.ReplaceAll(x, root+"/", "")
		return x
	default:
		return v
	}
}
func requiredInput(name string) []string {
	switch name {
	case "codegraph_get_curated_context", "codegraph_symbol_search":
		return []string{"query"}
	case "codegraph_get_ai_context":
		return []string{"uri", "line", "intent"}
	case "codegraph_get_edit_context", "codegraph_analyze_impact", "codegraph_get_call_graph":
		return []string{"uri", "line"}
	case "codegraph_pr_context":
		return []string{"baseBranch"}
	default:
		return nil
	}
}
func budgetOutput(v any, budget int) (any, bool, error) {
	if budget <= 0 {
		budget = 6000
	}
	maxBytes := budget * 4
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false, err
	}
	if len(b) <= maxBytes {
		return v, false, nil
	}
	if s, ok := v.(string); ok {
		r := []rune(s)
		limit := maxBytes / 4
		if limit > len(r) {
			limit = len(r)
		}
		return string(r[:limit]), true, nil
	}
	// A structured response must stay valid JSON; retain top-level fields that fit.
	if m, ok := v.(map[string]any); ok {
		out := map[string]any{}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			x := m[k]
			candidate := map[string]any{}
			for k2, v2 := range out {
				candidate[k2] = v2
			}
			candidate[k] = x
			bb, _ := json.Marshal(candidate)
			if len(bb) <= maxBytes {
				out[k] = x
			}
		}
		return out, true, nil
	}
	return map[string]any{"summary": "CodeGraph output exceeds token budget; increase token_budget"}, true, nil
}
func validatePaths(repo repository.Repository, v any) error {
	var walk func(any) error
	walk = func(value any) error {
		switch x := value.(type) {
		case map[string]any:
			for k, item := range x {
				if s, ok := item.(string); ok && (k == "file" || k == "path" || k == "uri") {
					p := s
					if strings.HasPrefix(p, "file://") {
						u, e := url.Parse(p)
						if e != nil {
							return e
						}
						p = u.Path
					}
					if filepath.IsAbs(p) {
						real, e := filepath.EvalSymlinks(p)
						if e != nil {
							return fmt.Errorf("CodeGraph returned invalid path")
						}
						rel, e := filepath.Rel(repo.Path, real)
						if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
							return fmt.Errorf("CodeGraph returned cross-repository path")
						}
					}
				}
				if e := walk(item); e != nil {
					return e
				}
			}
		case []any:
			for _, item := range x {
				if e := walk(item); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if err := walk(v); err != nil {
		return err
	}
	b, _ := json.Marshal(v)
	for _, match := range fileURI.FindAllString(string(b), -1) {
		u, err := url.Parse(match)
		if err != nil {
			return err
		}
		path := u.Path
		if unescaped, e := url.PathUnescape(path); e == nil {
			path = unescaped
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("CodeGraph returned invalid file URI")
		}
		rel, err := filepath.Rel(repo.Path, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("CodeGraph returned cross-repository path")
		}
	}
	return nil
}
func (s *Service) location(repo repository.Repository, sym *Symbol) (map[string]any, error) {
	if sym == nil || sym.File == "" || sym.Line < 1 || sym.Column < 1 {
		return nil, fmt.Errorf("symbol file, line, column (1-based) are required")
	}
	full, err := s.Legacy.Repos.File(repo, sym.File)
	if err != nil {
		return nil, err
	}
	// CodeGraph 0.20.1 MCP resolves positions using one-based source lines.
	// The upstream guide says zero-based, so this is covered by the release test.
	return map[string]any{"uri": (&url.URL{Scheme: "file", Path: full}).String(), "line": sym.Line}, nil
}
func (s *Service) buildContext(ctx context.Context, repo repository.Repository, r Request) (any, bool, error) {
	if r.Intent != "explain" && r.Intent != "debug" && r.Intent != "test" && r.Intent != "modify" {
		return nil, false, fmt.Errorf("intent must be explain, debug, test, or modify")
	}
	if r.Question == "" && r.Symbol == nil {
		return nil, false, fmt.Errorf("question or symbol is required")
	}
	name := "codegraph_get_curated_context"
	args := map[string]any{"query": r.Question}
	if r.Symbol != nil {
		if r.Symbol.Name != "" && r.Symbol.File == "" {
			candidates, _, err := s.graph(ctx, repo, "codegraph_symbol_search", map[string]any{"query": r.Symbol.Name}, r.TokenBudget)
			if err != nil {
				return nil, false, err
			}
			matches := searchMatches(candidates)
			if len(matches) == 0 {
				return nil, false, &choiceError{code: "no_match", candidates: candidates}
			}
			if len(matches) > 1 {
				return nil, false, &choiceError{code: "ambiguous_symbol", candidates: candidates}
			}
			if r.Precision == "java_exact" {
				return nil, false, &choiceError{code: "symbol_position_required", candidates: candidates}
			}
			candidateLoc, err := matchLocation(repo, matches[0])
			if err != nil {
				return nil, false, err
			}
			args = candidateLoc
			if r.Intent == "modify" {
				name = "codegraph_get_edit_context"
			} else {
				name = "codegraph_get_ai_context"
				args["intent"] = r.Intent
			}
		} else {
			loc, err := s.location(repo, r.Symbol)
			if err != nil {
				return nil, false, err
			}
			args = loc
			if r.Intent == "modify" {
				name = "codegraph_get_edit_context"
			} else {
				name = "codegraph_get_ai_context"
				args["intent"] = r.Intent
			}
		}
	}
	args["tokenBudget"] = r.TokenBudget
	args["maxTokens"] = r.TokenBudget
	raw, truncated, err := s.graph(ctx, repo, name, args, r.TokenBudget)
	if err != nil {
		return nil, false, err
	}
	result := map[string]any{"intent": r.Intent, "answer_context": raw, "precision_checks": []any{}, "provenance": []any{map[string]any{"tool": name, "source": "codegraph"}}}
	if r.Symbol != nil {
		result["focus"] = map[string]any{"name": r.Symbol.Name, "file": r.Symbol.File, "line": r.Symbol.Line, "column": r.Symbol.Column, "source": "codegraph"}
	}
	result["related"] = []any{}
	if r.Precision == "java_exact" {
		if r.Symbol == nil || !strings.HasSuffix(strings.ToLower(r.Symbol.File), ".java") {
			return nil, false, fmt.Errorf("java_exact requires a positioned Java symbol")
		}
		checks, err := s.javaChecks(ctx, repo, r.Symbol, r.Question)
		if err != nil {
			return nil, false, err
		}
		result["precision_checks"] = checks
	}
	return result, truncated, nil
}
func searchMatches(v any) []any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	a, _ := m["results"].([]any)
	return a
}
func matchLocation(repo repository.Repository, v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid CodeGraph symbol candidate")
	}
	sym, ok := m["symbol"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid CodeGraph symbol candidate")
	}
	loc, ok := sym["location"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid CodeGraph symbol candidate")
	}
	file, _ := loc["file"].(string)
	line, ok := loc["line"].(float64)
	if file == "" || !ok {
		return nil, fmt.Errorf("invalid CodeGraph symbol location")
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(repo.Path, file)
	}
	real, err := filepath.EvalSymlinks(file)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(repo.Path, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("CodeGraph returned cross-repository path")
	}
	return map[string]any{"uri": (&url.URL{Scheme: "file", Path: real}).String(), "line": int(line)}, nil
}
func (s *Service) javaChecks(ctx context.Context, repo repository.Repository, sym *Symbol, question string) ([]any, error) {
	checks := []any{}
	def, err := s.Legacy.Semantic(ctx, repo.ID, sym.File, sym.Line, sym.Column, "textDocument/definition")
	if err != nil {
		return nil, err
	}
	checks = append(checks, map[string]any{"operation": "definition", "source": "jdtls", "locations": def})
	lower := strings.ToLower(question)
	if strings.Contains(lower, "interface") || strings.Contains(lower, "override") || strings.Contains(lower, "实现") || strings.Contains(lower, "覆写") {
		impl, err := s.Legacy.Semantic(ctx, repo.ID, sym.File, sym.Line, sym.Column, "textDocument/implementation")
		if err != nil {
			return nil, err
		}
		checks = append(checks, map[string]any{"operation": "implementation", "source": "jdtls", "locations": impl})
	}
	return checks, nil
}
func (s *Service) impact(ctx context.Context, repo repository.Repository, r Request) (any, bool, error) {
	loc, err := s.location(repo, r.Symbol)
	if err != nil {
		return nil, false, err
	}
	if r.ChangeKind != "" && r.ChangeKind != "modify" && r.ChangeKind != "delete" && r.ChangeKind != "rename" {
		return nil, false, fmt.Errorf("invalid change_kind")
	}
	if r.ChangeKind == "" {
		r.ChangeKind = "modify"
	}
	args := loc
	args["changeType"] = r.ChangeKind
	args["changeKind"] = r.ChangeKind
	if r.ChangeKind != "" && r.ChangeKind != "modify" {
		tool, e := s.Graph.Tool(ctx, repo.ID, "codegraph_analyze_impact")
		if e != nil {
			return nil, false, e
		}
		if _, ok := tool.InputSchema.Properties["changeType"]; !ok {
			if _, ok = tool.InputSchema.Properties["changeKind"]; !ok {
				return nil, false, fmt.Errorf("%w: change_kind %s", codegraph.ErrCapability, r.ChangeKind)
			}
		}
	}
	raw, truncated, err := s.graph(ctx, repo, "codegraph_analyze_impact", args, r.TokenBudget)
	if err != nil {
		return nil, false, err
	}
	result := map[string]any{"impact_context": raw, "precision_checks": []any{}, "provenance": []any{map[string]any{"tool": "codegraph_analyze_impact", "source": "codegraph"}}}
	if r.Precision == "java_exact" {
		if !strings.HasSuffix(strings.ToLower(r.Symbol.File), ".java") {
			return nil, false, fmt.Errorf("java_exact requires Java symbol")
		}
		refs, e := s.Legacy.Semantic(ctx, repo.ID, r.Symbol.File, r.Symbol.Line, r.Symbol.Column, "textDocument/references")
		if e != nil {
			return nil, false, e
		}
		result["precision_checks"] = []any{map[string]any{"operation": "references", "source": "jdtls", "locations": refs}}
	}
	return result, truncated, nil
}
func (s *Service) changeContext(ctx context.Context, repo repository.Repository, r Request) (any, bool, error) {
	if r.BaseBranch == "" || strings.HasPrefix(r.BaseBranch, "-") || strings.ContainsAny(r.BaseBranch, " \t\n") || r.Staged || (r.Head != "" && r.Head != "HEAD") {
		return nil, false, fmt.Errorf("unsupported change scope: base_branch and HEAD are required; staged is unsupported")
	}
	if r.Format == "" {
		r.Format = "json"
	}
	if r.Format != "json" && r.Format != "markdown" {
		return nil, false, fmt.Errorf("format must be json or markdown")
	}
	if r.Format == "markdown" {
		tool, e := s.Graph.Tool(ctx, repo.ID, "codegraph_pr_context")
		if e != nil {
			return nil, false, e
		}
		if _, ok := tool.InputSchema.Properties["format"]; !ok {
			return nil, false, fmt.Errorf("%w: PR context markdown format", codegraph.ErrCapability)
		}
	}
	if _, err := runner.Run(ctx, repo.Path, "git", "rev-parse", "--verify", r.BaseBranch+"^{commit}"); err != nil {
		return nil, false, fmt.Errorf("invalid base_branch: %w", err)
	}
	diff, err := runner.Run(ctx, repo.Path, "git", "diff", "--no-ext-diff", "--no-color", "--stat", r.BaseBranch+"...HEAD")
	if err != nil {
		return nil, false, err
	}
	if len(diff) > 1<<20 {
		return nil, false, fmt.Errorf("change scope exceeds diff limit")
	}
	raw, truncated, err := s.graph(ctx, repo, "codegraph_pr_context", map[string]any{"baseBranch": r.BaseBranch, "format": r.Format}, r.TokenBudget)
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"change_scope": map[string]any{"base_branch": r.BaseBranch, "head": "HEAD", "diff_stat": string(diff)}, "pr_context": raw, "provenance": []any{map[string]any{"tool": "codegraph_pr_context", "source": "codegraph"}}}, truncated, nil
}
func (s *Service) callGraph(ctx context.Context, repo repository.Repository, r Request) (any, bool, error) {
	loc, err := s.location(repo, &Symbol{File: r.File, Line: r.Line, Column: r.Column})
	if err != nil {
		return nil, false, err
	}
	if r.Depth < 0 || r.Depth > 10 {
		return nil, false, fmt.Errorf("invalid depth")
	}
	if r.Direction != "" && r.Direction != "incoming" && r.Direction != "outgoing" {
		return nil, false, fmt.Errorf("invalid direction")
	}
	loc["depth"] = r.Depth
	if r.Depth <= 0 {
		delete(loc, "depth")
	}
	if r.Direction != "" {
		tool, e := s.Graph.Tool(ctx, repo.ID, "codegraph_get_call_graph")
		if e != nil {
			return nil, false, e
		}
		if _, ok := tool.InputSchema.Properties["direction"]; !ok {
			return nil, false, fmt.Errorf("%w: call graph direction", codegraph.ErrCapability)
		}
		loc["direction"] = r.Direction
	}
	raw, truncated, err := s.graph(ctx, repo, "codegraph_get_call_graph", loc, r.TokenBudget)
	return map[string]any{"graph": raw, "precision": "structural", "source": "codegraph"}, truncated, err
}
func (s *Service) symbolSearch(ctx context.Context, repo repository.Repository, r Request) (any, bool, error) {
	if r.Query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	return s.graph(ctx, repo, "codegraph_symbol_search", map[string]any{"query": r.Query, "limit": r.Limit}, r.TokenBudget)
}
func (s *Service) java(ctx context.Context, repo repository.Repository, name string, r Request) (any, error) {
	if !strings.HasSuffix(strings.ToLower(r.File), ".java") {
		return nil, fmt.Errorf("unsupported_language: Java file required")
	}
	if _, err := s.Legacy.Repos.File(repo, r.File); err != nil {
		return nil, err
	}
	if name == "get_file_symbols" {
		full, _ := s.Legacy.Repos.File(repo, r.File)
		return s.Legacy.JDT.FileSymbols(ctx, repo.ID, repo.Path, full)
	}
	if r.Line < 1 || r.Column < 1 {
		return nil, fmt.Errorf("file, line, column (1-based) are required")
	}
	switch name {
	case "find_definition":
		return s.Legacy.Semantic(ctx, repo.ID, r.File, r.Line, r.Column, "textDocument/definition")
	case "find_references":
		return s.Legacy.Semantic(ctx, repo.ID, r.File, r.Line, r.Column, "textDocument/references")
	case "find_overrides":
		x, e := s.Legacy.Semantic(ctx, repo.ID, r.File, r.Line, r.Column, "textDocument/implementation")
		return map[string]any{"operation": "implementation", "locations": x}, e
	case "get_type_hierarchy":
		x, truncated, e := s.Legacy.TypeHierarchy(ctx, repo.ID, r.File, r.Line, r.Column, r.Depth, r.Direction)
		return map[string]any{"locations": x, "truncated": truncated}, e
	}
	return nil, fmt.Errorf("unknown Java tool")
}

var _ = lsp.Position{}
