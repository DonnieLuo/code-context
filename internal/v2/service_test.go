package v2

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/code-context/internal/codegraph"
	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/tools"
)

// The helper exercises the real stdio handshake and tools/call framing without
// requiring a network download in ordinary unit tests.
func TestMCPHelper(t *testing.T) {
	if os.Getenv("CODE_CONTEXT_TEST_MCP") != "1" {
		return
	}
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var msg map[string]json.RawMessage
		if json.Unmarshal(scan.Bytes(), &msg) != nil {
			continue
		}
		var method string
		_ = json.Unmarshal(msg["method"], &method)
		id := msg["id"]
		if len(id) == 0 {
			continue
		}
		result := any(map[string]any{})
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "fake", "version": "1"}}
		case "tools/list":
			tool := func(name string, keys ...string) map[string]any {
				props := map[string]any{}
				for _, key := range keys {
					props[key] = map[string]any{"type": "string"}
				}
				return map[string]any{"name": name, "inputSchema": map[string]any{"type": "object", "properties": props}}
			}
			result = map[string]any{"tools": []any{tool("codegraph_reindex_workspace"), tool("codegraph_get_curated_context", "query"), tool("codegraph_get_ai_context", "uri", "line", "intent"), tool("codegraph_get_edit_context", "uri", "line"), tool("codegraph_analyze_impact", "uri", "line", "changeType"), tool("codegraph_symbol_search", "query"), tool("codegraph_get_call_graph", "uri", "line", "depth", "direction"), tool("codegraph_pr_context", "baseBranch", "format")}}
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(msg["params"], &params)
			payload := map[string]any{"tool": params.Name, "arguments": params.Arguments}
			if params.Name == "codegraph_symbol_search" {
				cwd, _ := os.Getwd()
				candidate := func(line int) any {
					return map[string]any{"symbol": map[string]any{"name": "work", "location": map[string]any{"file": filepath.Join(cwd, "Main.java"), "line": line}}}
				}
				matches := []any{candidate(0)}
				if params.Arguments["query"] == "overload" {
					matches = append(matches, candidate(1))
				}
				payload = map[string]any{"results": matches}
			}
			b, _ := json.Marshal(payload)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "isError": false}
		}
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		fmt.Println(string(b))
	}
	os.Exit(0)
}
func testService(t *testing.T) (*Service, *codegraph.Manager, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	repo, _ = filepath.EvalSymlinks(repo)
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "Main.java"), []byte("class Main {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "Main.java"}, {"commit", "-m", "initial"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	var cfg config.Config
	cfg.Repositories = map[string]config.Repository{"test": {Path: repo}}
	cfg.CodeGraph.Enabled = true
	cfg.CodeGraph.Command = os.Args[0]
	cfg.CodeGraph.Args = []string{"-test.run=TestMCPHelper"}
	cfg.CodeGraph.DataRoot = filepath.Join(root, "data")
	cfg.CodeGraph.CallTimeout = 5 * time.Second
	cfg.CodeGraph.StartupTimeout = 5 * time.Second
	cfg.CodeGraph.MaxConcurrentPerRepo = 2
	cfg.CodeGraph.MaxProcesses = 1
	t.Setenv("CODE_CONTEXT_TEST_MCP", "1")
	repos := repository.New(cfg)
	graph, err := codegraph.NewManager(cfg, repos)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &tools.Service{Repos: repos, MaxResults: 100}
	return &Service{Legacy: legacy, Graph: graph, MaxResults: 100, MaxTokenBudget: 12000}, graph, repo
}
func TestBuildContextRoutesAndFreshness(t *testing.T) {
	service, graph, repo := testService(t)
	defer graph.Close()
	ctx := context.Background()
	before := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Question: "what does Main do"})
	if before.Error == nil || before.Error.Code != "index_unavailable" {
		t.Fatalf("before index: %+v", before)
	}
	if _, err := graph.Refresh(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	got := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Question: "what does Main do"})
	if got.Error != nil {
		t.Fatalf("context: %+v", got.Error)
	}
	contextData := got.Data.(map[string]any)["answer_context"].(map[string]any)
	if contextData["tool"] != "codegraph_get_curated_context" {
		t.Fatalf("wrong tool: %#v", contextData)
	}
	if meta := got.Meta.(map[string]any); meta["index_state"] != "ready" || meta["duration_ms"] == nil {
		t.Fatalf("meta: %#v", meta)
	}
	modified := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "modify", Symbol: &Symbol{File: "Main.java", Line: 1, Column: 1}})
	if modified.Error != nil {
		t.Fatal(modified.Error)
	}
	modifiedData := modified.Data.(map[string]any)["answer_context"].(map[string]any)
	if modifiedData["tool"] != "codegraph_get_edit_context" {
		t.Fatalf("wrong tool: %#v", modifiedData)
	}
	if args := modifiedData["arguments"].(map[string]any); args["line"] != float64(1) {
		t.Fatalf("CodeGraph release line mapping: %#v", args)
	}
	ambiguous := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Symbol: &Symbol{Name: "overload"}})
	if ambiguous.Error == nil || ambiguous.Error.Code != "ambiguous_symbol" {
		t.Fatalf("ambiguous: %+v", ambiguous)
	}
	unique := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Symbol: &Symbol{Name: "single"}})
	if unique.Error != nil {
		t.Fatalf("unique: %+v", unique.Error)
	}
	impact := service.Execute(ctx, "impact", Request{RepoID: "test", Symbol: &Symbol{File: "Main.java", Line: 1, Column: 1}, ChangeKind: "delete"})
	if impact.Error != nil {
		t.Fatalf("impact: %+v", impact.Error)
	}
	change := service.Execute(ctx, "change_context", Request{RepoID: "test", BaseBranch: "HEAD"})
	if change.Error != nil {
		t.Fatalf("change: %+v", change.Error)
	}
	if err := os.WriteFile(filepath.Join(repo, "Main.java"), []byte("class Main { int x; }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Question: "what changed"})
	if stale.Error == nil || stale.Error.Code != "index_stale" {
		t.Fatalf("stale: %+v", stale)
	}
	allow := false
	old := service.Execute(ctx, "build_context", Request{RepoID: "test", Intent: "explain", Question: "what changed", RequireFreshIndex: &allow})
	if old.Error != nil || !strings.Contains(strings.Join(old.Warnings, ","), "working_tree_unindexed") {
		t.Fatalf("allowed stale: %+v", old)
	}
}
