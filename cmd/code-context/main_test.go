package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestTeeHandlerWritesErrorsToErrorHandlerOnly(t *testing.T) {
	var console, errorFile bytes.Buffer
	handler := teeHandler{
		slog.NewTextHandler(&console, &slog.HandlerOptions{Level: slog.LevelInfo}),
		slog.NewTextHandler(&errorFile, &slog.HandlerOptions{Level: slog.LevelError}),
	}
	logger := slog.New(handler)
	logger.Info("started")
	logger.Error("failed")

	if !strings.Contains(console.String(), "started") || !strings.Contains(console.String(), "failed") {
		t.Fatalf("console output = %q", console.String())
	}
	if strings.Contains(errorFile.String(), "started") || !strings.Contains(errorFile.String(), "failed") {
		t.Fatalf("error output = %q", errorFile.String())
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("error level should be enabled")
	}
}

func TestLoadConfigUsesEmbeddedConfig(t *testing.T) {
	previous := embeddedConfig
	t.Cleanup(func() { embeddedConfig = previous })
	repo := t.TempDir()
	embeddedConfig = base64.StdEncoding.EncodeToString([]byte("repositories:\n  test:\n    path: " + repo + "\n"))
	cfg, err := loadConfig("")
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
