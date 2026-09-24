package codegraph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This is the release contract gate. Set CODEGRAPH_ENGINE_BIN to a verified
// CodeGraph 0.20.1 engine; ordinary unit tests use the fake stdio server.
func TestRealCodeGraphContract(t *testing.T) {
	engine := os.Getenv("CODEGRAPH_ENGINE_BIN")
	if engine == "" {
		t.Skip("set CODEGRAPH_ENGINE_BIN to verified CodeGraph 0.20.1 engine")
	}
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\nfunc greet() string { return \"hello\" }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, err := Start(ctx, engine, []string{"--mcp", "--workspace", repo}, repo, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	required := map[string][]string{
		"codegraph_get_curated_context": {"query"}, "codegraph_get_ai_context": {"uri", "line", "intent"}, "codegraph_get_edit_context": {"uri", "line"}, "codegraph_analyze_impact": {"uri", "line", "changeType"}, "codegraph_symbol_search": {"query"}, "codegraph_get_call_graph": {"uri", "line"}, "codegraph_pr_context": {"baseBranch"}, "codegraph_reindex_workspace": {},
	}
	tools := client.Tools()
	for name, keys := range required {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("missing %s", name)
			continue
		}
		for _, key := range keys {
			if _, ok := tool.InputSchema.Properties[key]; !ok {
				t.Errorf("%s missing property %s", name, key)
			}
		}
	}
	if t.Failed() {
		return
	}
	if _, err := client.Call(ctx, "codegraph_reindex_workspace", map[string]any{}); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if _, err := client.Call(ctx, "codegraph_get_curated_context", map[string]any{"query": "where is greet"}); err != nil {
		t.Fatalf("curated context: %v", err)
	}
}
