package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ok.java"), []byte("class Ok {}"), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{repos: map[string]Repository{"r": {ID: "r", Path: realRoot}}}
	repo, _ := m.Get("r")
	if _, err := m.File(repo, "../secret"); err == nil {
		t.Fatal("expected traversal error")
	}
	if got, err := m.File(repo, "ok.java"); err != nil || got != filepath.Join(realRoot, "ok.java") {
		t.Fatalf("got %q, %v", got, err)
	}
}
