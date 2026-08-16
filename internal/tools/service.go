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
func (s *Service) CallHierarchy(ctx context.Context, repoID, file string, line, column int, incoming bool) ([]Location, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return nil, err
	}
	full, err := s.Repos.File(repo, file)
	if err != nil {
		return nil, err
	}
	xs, err := s.JDT.CallHierarchy(ctx, repoID, repo.Path, full, lsp.Position{Line: line - 1, Character: column - 1}, incoming)
	if err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(xs))
	for _, x := range xs {
		out = append(out, s.loc(repo, lsp.Location{URI: x.URI, Range: x.SelectionRange}))
	}
	return out, nil
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
func (s *Service) Diff(ctx context.Context, repoID, base, head, path string, staged bool) (string, bool, error) {
	repo, err := s.repo(repoID)
	if err != nil {
		return "", false, err
	}
	args := []string{"diff", "--no-ext-diff", "--no-color"}
	if staged {
		args = append(args, "--staged")
	}
	if base != "" {
		args = append(args, base)
		if head != "" {
			args = append(args, head)
		}
	}
	if path != "" {
		if _, err := s.Repos.File(repo, path); err != nil {
			return "", false, err
		}
		args = append(args, "--", path)
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
