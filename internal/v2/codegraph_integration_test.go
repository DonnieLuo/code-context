package v2

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/code-context/internal/codegraph"
	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/tools"
)

func TestRealCodeGraphV2Flow(t *testing.T) {
	engine := os.Getenv("CODEGRAPH_ENGINE_BIN")
	if engine == "" {
		t.Skip("set CODEGRAPH_ENGINE_BIN to verified CodeGraph 0.20.1 engine")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	repo, _ = filepath.EvalSymlinks(repo)
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc greet() string { return \"hello\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "main.go"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
	}
	var cfg config.Config
	cfg.Repositories = map[string]config.Repository{"fixture": {Path: repo}}
	cfg.CodeGraph.Enabled = true
	cfg.CodeGraph.Command = engine
	cfg.CodeGraph.Args = []string{"--mcp", "--workspace", repo}
	cfg.CodeGraph.DataRoot = filepath.Join(root, "index")
	cfg.CodeGraph.StartupTimeout = 2 * time.Minute
	cfg.CodeGraph.CallTimeout = 2 * time.Minute
	cfg.CodeGraph.MaxConcurrentPerRepo = 2
	cfg.CodeGraph.MaxProcesses = 1
	repos := repository.New(cfg)
	graph, err := codegraph.NewManager(cfg, repos)
	if err != nil {
		t.Fatal(err)
	}
	defer graph.Close()
	service := &Service{Legacy: &tools.Service{Repos: repos, MaxResults: 100}, Graph: graph, MaxResults: 100, MaxTokenBudget: 12000}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := graph.Refresh(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if status.IndexState != "ready" {
		t.Fatalf("index: %+v", status)
	}
	for i, test := range []struct {
		Name    string
		Request Request
	}{
		{"build_context", Request{RepoID: "fixture", Intent: "explain", Question: "where is greet implemented?"}},
		{"build_context", Request{RepoID: "fixture", Intent: "modify", Symbol: &Symbol{File: "main.go", Line: 2, Column: 6}}},
		{"impact", Request{RepoID: "fixture", Symbol: &Symbol{File: "main.go", Line: 2, Column: 6}, ChangeKind: "modify"}},
		{"change_context", Request{RepoID: "fixture", BaseBranch: "HEAD"}},
	} {
		got := service.Execute(ctx, test.Name, test.Request)
		if got.Error != nil {
			t.Fatalf("%s: %+v", test.Name, got.Error)
		}
		if got.Data == nil {
			t.Fatalf("%s: empty data", test.Name)
		}
		if i == 1 {
			raw := got.Data.(map[string]any)["answer_context"].(map[string]any)
			metadata := raw["metadata"].(map[string]any)
			if metadata["usedFallback"] == true {
				t.Fatalf("positioned edit context fell back to a neighboring symbol: %#v", raw)
			}
		}
		if i == 2 {
			raw := got.Data.(map[string]any)["impact_context"].(map[string]any)
			if raw["used_fallback"] == true {
				t.Fatalf("positioned impact fell back to a neighboring symbol: %#v", raw)
			}
		}
	}
}
