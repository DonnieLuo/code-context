package config

import (
	"path/filepath"
	"testing"
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
