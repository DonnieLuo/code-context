package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/example/code-context/internal/lsp"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/runner"
)

type Service struct {
	Repos                      *repository.Manager
	JDT                        *lsp.JDT
	MaxResults                 int
	MaxReadBytes, MaxDiffBytes int64
	MaxCallDepth               int
}
type Location struct {
	File        string `json:"file"`
	StartLine   int    `json:"start_line"`
	StartColumn int    `json:"start_column"`
	EndLine     int    `json:"end_line"`
	EndColumn   int    `json:"end_column"`
	Snippet     string `json:"snippet,omitempty"`
	Precision   string `json:"precision"`
	Source      string `json:"source"`
	Depth       int    `json:"depth,omitempty"`
}
type GraphNode struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type GraphEdge struct {
	From      string     `json:"from"`
	To        string     `json:"to"`
	CallSites []Location `json:"call_sites,omitempty"`
}

func (s *Service) repo(id string) (repository.Repository, error) { return s.Repos.Get(id) }
func (s *Service) loc(repo repository.Repository, x lsp.Location) Location {
	p := strings.TrimPrefix(x.URI, "file://")
	rel, err := filepath.Rel(repo.Path, p)
	if err != nil {
		rel = p
	}
	return Location{File: filepath.ToSlash(rel), StartLine: x.Range.Start.Line + 1, StartColumn: x.Range.Start.Character + 1, EndLine: x.Range.End.Line + 1, EndColumn: x.Range.End.Character + 1, Precision: "exact", Source: "jdtls"}
}
func (s *Service) Semantic(ctx context.Context, repoID, file string, line, column int, method string) ([]Location, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, err
	}
	xs, err := s.JDT.Locations(ctx, repoID, repo.Path, full, method, lsp.Position{Line: line - 1, Character: column - 1})
	if err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(xs))
	for _, x := range xs {
		out = append(out, s.loc(repo, x))
	}
	return out, nil
}
func (s *Service) Symbols(ctx context.Context, repoID, q string) ([]Location, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	xs, err := s.JDT.Symbols(ctx, repoID, repo.Path, q)
	if err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(xs))
	for _, x := range xs {
		l := s.loc(repo, x.Location)
		l.Snippet = x.Name
		out = append(out, l)
	}
	return out, nil
}
func (s *Service) TypeHierarchy(ctx context.Context, repoID, file string, line, column, depth int, direction string) ([]Location, bool, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, false, err
	}
	full, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, false, err
	}
	if depth <= 0 {
		depth = 1
	}
	if direction == "" {
		direction = "subtypes"
	}
	if direction != "subtypes" && direction != "supertypes" {
		return nil, false, fmt.Errorf("direction must be subtypes or supertypes")
	}
	if s.MaxCallDepth > 0 && depth > s.MaxCallDepth {
		return nil, false, fmt.Errorf("depth exceeds configured maximum of %d", s.MaxCallDepth)
	}
	items, truncated, err := s.JDT.TypeHierarchy(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: column - 1}, direction, depth, s.MaxResults)
	if err != nil {
		return nil, false, err
	}
	out := make([]Location, 0, len(items))
	for _, item := range items {
		out = append(out, s.loc(repo, lsp.Location{URI: item.URI, Range: item.SelectionRange}))
	}
	return out, truncated, nil
}
func graphNode(repo repository.Repository, item lsp.CallHierarchyItem) GraphNode {
	p := strings.TrimPrefix(item.URI, "file://")
	rel, err := filepath.Rel(repo.Path, p)
	if err != nil {
		rel = p
	}
	return GraphNode{ID: callID(item), Name: item.Name, File: filepath.ToSlash(rel), Line: item.SelectionRange.Start.Line + 1, Column: item.SelectionRange.Start.Character + 1}
}
func callID(item lsp.CallHierarchyItem) string {
	return fmt.Sprintf("%s:%d:%d", item.URI, item.SelectionRange.Start.Line, item.SelectionRange.Start.Character)
}
func (s *Service) CallGraph(ctx context.Context, repoID, file string, line, column, depth int, direction string) (map[string]any, bool, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, false, err
	}
	full, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, false, err
	}
	if direction == "" {
		direction = "outgoing"
	}
	if direction != "outgoing" && direction != "incoming" {
		return nil, false, fmt.Errorf("direction must be outgoing or incoming")
	}
	if depth <= 0 {
		depth = 1
	}
	if s.MaxCallDepth > 0 && depth > s.MaxCallDepth {
		return nil, false, fmt.Errorf("depth exceeds configured maximum of %d", s.MaxCallDepth)
	}
	raw, truncated, err := s.JDT.CallGraph(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: column - 1}, direction, depth, s.MaxResults)
	if err != nil {
		return nil, false, err
	}
	nodes := map[string]GraphNode{}
	roots, err := s.JDT.CallHierarchyRoots(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: column - 1})
	if err != nil {
		return nil, false, err
	}
	for _, root := range roots {
		node := graphNode(repo, root)
		nodes[node.ID] = node
	}
	edges := make([]GraphEdge, 0, len(raw))
	for _, edge := range raw {
		from, to := graphNode(repo, edge.From), graphNode(repo, edge.To)
		nodes[from.ID] = from
		nodes[to.ID] = to
		sites := make([]Location, 0, len(edge.FromRanges))
		for _, r := range edge.FromRanges {
			sites = append(sites, s.loc(repo, lsp.Location{URI: edge.From.URI, Range: r}))
		}
		edges = append(edges, GraphEdge{From: from.ID, To: to.ID, CallSites: sites})
	}
	outNodes := make([]GraphNode, 0, len(nodes))
	for _, node := range nodes {
		outNodes = append(outNodes, node)
	}
	return map[string]any{"nodes": outNodes, "edges": edges}, truncated, nil
}
func (s *Service) TraceCallPath(ctx context.Context, repoID, file string, line, column int, targetFile string, targetLine, targetColumn, maxDepth int) (map[string]any, bool, error) {
	if targetFile == "" || targetLine < 1 || targetColumn < 1 {
		return nil, false, fmt.Errorf("target_file, target_line, and target_column (1-based) are required")
	}
	graph, truncated, err := s.CallGraph(ctx, repoID, file, line, column, maxDepth, "outgoing")
	if err != nil {
		return nil, false, err
	}
	nodes := graph["nodes"].([]GraphNode)
	edges := graph["edges"].([]GraphEdge)
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, false, err
	}
	targetFull, err := s.Repos.File(repo, targetFile)
	if err != nil {
		return nil, false, err
	}
	targetRoots, err := s.JDT.CallHierarchyRoots(ctx, repoID, repo.Path, targetFull, lsp.Position{Line: targetLine - 1, Character: targetColumn - 1})
	if err != nil {
		return nil, false, err
	}
	startFull, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, false, err
	}
	startRoots, err := s.JDT.CallHierarchyRoots(ctx, repoID, repo.Path, startFull, lsp.Position{Line: line - 1, Character: column - 1})
	if err != nil {
		return nil, false, err
	}
	start := ""
	target := ""
	if len(startRoots) > 0 {
		start = callID(startRoots[0])
	}
	if len(targetRoots) > 0 {
		target = callID(targetRoots[0])
	}
	if start == "" {
		return map[string]any{"path": []GraphNode{}}, truncated, nil
	}
	if target == "" {
		return map[string]any{"path": []GraphNode{}}, truncated, nil
	}
	adj := map[string][]string{}
	for _, edge := range edges {
		adj[edge.From] = append(adj[edge.From], edge.To)
	}
	previous := map[string]string{start: ""}
	queue := []string{start}
	for len(queue) > 0 && previous[target] == "" {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adj[current] {
			if _, seen := previous[next]; !seen {
				previous[next] = current
				queue = append(queue, next)
			}
		}
	}
	if _, found := previous[target]; !found {
		return map[string]any{"path": []GraphNode{}}, truncated, nil
	}
	byID := map[string]GraphNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	ids := []string{}
	for at := target; at != ""; at = previous[at] {
		ids = append([]string{at}, ids...)
	}
	path := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		path = append(path, byID[id])
	}
	return map[string]any{"path": path}, truncated, nil
}
func (s *Service) SymbolContext(ctx context.Context, repoID, file string, line, column int) (map[string]any, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, err
	}
	hover, err := s.JDT.Hover(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: column - 1})
	if err != nil {
		return nil, err
	}
	definition, err := s.Semantic(ctx, repoID, file, line, column, "textDocument/definition")
	if err != nil {
		return nil, err
	}
	source, err := s.Read(repoID, file, max(1, line-12), line+12)
	if err != nil {
		return nil, err
	}
	return map[string]any{"hover": hover, "definition": definition, "source": source}, nil
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (s *Service) Read(repoID, path string, start, end int) (map[string]any, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.Repos.File(repo, path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > s.MaxReadBytes {
		return nil, fmt.Errorf("file exceeds read limit")
	}
	lines := strings.Split(string(b), "\n")
	if start < 1 {
		start = 1
	}
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return nil, fmt.Errorf("invalid line range")
	}
	out := make([]map[string]any, 0, end-start+1)
	for i := start - 1; i < end; i++ {
		out = append(out, map[string]any{"line": i + 1, "text": lines[i]})
	}
	return map[string]any{"path": path, "total_lines": len(lines), "lines": out, "truncated": false}, nil
}
func (s *Service) Search(ctx context.Context, repoID, query, path string, globs []string, limit, contextLines int) ([]map[string]any, bool, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, false, err
	}
	if query == "" {
		return nil, false, fmt.Errorf("query is required")
	}
	if limit <= 0 || limit > s.MaxResults {
		limit = s.MaxResults
	}
	args := []string{"--json", "--line-number", "--column", "--no-heading", "--max-count", fmt.Sprint(limit)}
	if contextLines > 0 {
		args = append(args, "--context", fmt.Sprint(contextLines))
	}
	for _, g := range globs {
		args = append(args, "-g", g)
	}
	args = append(args, "--", query)
	dir := repo.Path
	if path != "" {
		full, e := s.Repos.File(repo, path)
		if e != nil {
			return nil, false, e
		}
		dir = filepath.Dir(full)
		args = append(args, filepath.Base(full))
	}
	b, err := runner.Run(ctx, dir, "rg", args...)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return []map[string]any{}, false, nil
		}
		return nil, false, err
	}
	scan := bufio.NewScanner(bytes.NewReader(b))
	scan.Buffer(make([]byte, 4096), 1<<20)
	out := []map[string]any{}
	currentFile := []map[string]any{}
	fileHasMatch := false
	matches := 0
	truncated := false
	for scan.Scan() {
		var v map[string]any
		if json.Unmarshal(scan.Bytes(), &v) != nil {
			continue
		}
		typeName, _ := v["type"].(string)
		switch typeName {
		case "begin":
			currentFile = []map[string]any{v}
			fileHasMatch = false
		case "match":
			if matches < limit {
				currentFile = append(currentFile, v)
				fileHasMatch = true
				matches++
			} else {
				truncated = true
			}
		case "context":
			// Preserve surrounding context for the selected matches only.
			if matches < limit || fileHasMatch {
				currentFile = append(currentFile, v)
			}
		case "end":
			if fileHasMatch {
				currentFile = append(currentFile, v)
				out = append(out, currentFile...)
			}
			if matches >= limit {
				truncated = true
				break
			}
		}
	}
	return out, truncated, scan.Err()
}
func (s *Service) GitQuery(ctx context.Context, repoID string, args []string) (string, bool, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return "", false, err
	}
	if len(args) == 0 || !readOnlyGitCommand(args[0]) || hasUnsafeGitArg(args[1:]) {
		return "", false, fmt.Errorf("git command must be a permitted read-only query")
	}
	if args[0] == "diff" {
		args = append([]string{"diff", "--no-ext-diff", "--no-color"}, args[1:]...)
	}
	b, err := runner.Run(ctx, repo.Path, "git", args...)
	if err != nil {
		return "", false, err
	}
	trunc := int64(len(b)) > s.MaxDiffBytes
	if trunc {
		b = b[:s.MaxDiffBytes]
	}
	return string(b), trunc, nil
}
func readOnlyGitCommand(command string) bool {
	switch command {
	case "annotate", "archive", "blame", "cat-file", "check-attr", "check-ignore", "check-mailmap", "check-ref-format", "count-objects", "describe", "diff", "diff-files", "diff-index", "diff-tree", "for-each-ref", "fsck", "get-tar-commit-id", "grep", "log", "ls-files", "ls-remote", "ls-tree", "merge-base", "name-rev", "range-diff", "rev-list", "rev-parse", "show", "show-branch", "show-index", "show-ref", "shortlog", "status", "symbolic-ref", "verify-commit", "verify-pack", "verify-tag", "whatchanged":
		return true
	}
	return false
}
func hasUnsafeGitArg(args []string) bool {
	for _, arg := range args {
		if arg == "--ext-diff" || arg == "--no-index" || arg == "-o" || arg == "--output" || strings.HasPrefix(arg, "--output=") {
			return true
		}
	}
	return false
}
func (s *Service) SyncAndWarm(ctx context.Context) error {
	for _, repo := range s.Repos.List() {
		if _, err := runner.Run(ctx, repo.Path, "git", "pull", "--ff-only"); err != nil {
			return fmt.Errorf("pull %s: %w", repo.ID, err)
		}
		s.JDT.Refresh(repo.ID)
		if err := s.JDT.Warm(ctx, repo.ID, repo.Path); err != nil {
			return fmt.Errorf("warm %s: %w", repo.ID, err)
		}
	}
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error { return s.JDT.Shutdown(ctx) }
func (s *Service) ListFiles(repoID, path string, depth int) ([]string, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	root := repo.Path
	if path != "" {
		root, err = s.Repos.File(repo, path)
		if err != nil {
			return nil, err
		}
	}
	out := []string{}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(repo.Path, p)
		depthRel, _ := filepath.Rel(root, p)
		if depth > 0 && len(strings.Split(filepath.ToSlash(depthRel), "/")) > depth {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= s.MaxResults {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}
func (s *Service) Refresh(repoID string) error {
	if _, err := s.repo(repoID); err != nil {
		return err
	}
	s.JDT.Refresh(repoID)
	return nil
}
func (s *Service) Status(ctx context.Context, repoID string) (map[string]any, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	head, err := runner.Run(ctx, repo.Path, "git", "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	return map[string]any{"repo_id": repoID, "revision": strings.TrimSpace(string(head)), "jdtls_active": s.JDT.Active(repoID)}, nil
}
