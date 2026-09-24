package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadBytes(t *testing.T) {
	repo := t.TempDir()
	contents := []byte("repositories:\n  test:\n    path: " + repo + "\n")
	cfg, err := LoadBytes(contents)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Repositories["test"].Path != expected {
		t.Fatalf("repository path = %q, want %q", cfg.Repositories["test"].Path, expected)
	}
}

func TestCodeGraphYAMLConfiguration(t *testing.T) {
	repo := t.TempDir()
	yaml := []byte("server:\n  listen: 127.0.0.1:8080\ncodegraph:\n  enabled: true\n  command: codegraph-mcp\n  args: [\"--embedding-model\", \"granite-97m\"]\n  data_root: /tmp/codegraph-test\n  startup_timeout: 2m\n  call_timeout: 15s\n  max_concurrent_per_repo: 3\n  max_processes: 1\n  reindex_on_start: true\n  telemetry: false\nrepositories:\n  test:\n    path: " + repo + "\n")
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CodeGraph.Enabled || cfg.CodeGraph.Command != "codegraph-mcp" || len(cfg.CodeGraph.Args) != 2 || cfg.CodeGraph.CallTimeout != 15*time.Second || cfg.CodeGraph.MaxProcesses != 1 || !cfg.CodeGraph.ReindexOnStart {
		t.Fatalf("codegraph config: %+v", cfg.CodeGraph)
	}
}
