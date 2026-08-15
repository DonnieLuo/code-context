package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/example/code-context/internal/api"
	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/lsp"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/tools"
)

func main() {
	configPath := flag.String("config", "config.yaml", "service YAML configuration")
	flag.Parse()
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	service := &tools.Service{Repos: repository.New(cfg), JDT: lsp.NewJDT(cfg.JDTLS.Command, cfg.JDTLS.Args, cfg.JDTLS.WorkspaceRoot), MaxResults: cfg.Server.MaxResults, MaxReadBytes: cfg.Server.MaxReadBytes, MaxDiffBytes: cfg.Server.MaxDiffBytes}
	srv := &http.Server{Addr: cfg.Server.Listen, Handler: api.New(service, cfg.Server.RequestTimeout).Handler(), ReadHeaderTimeout: cfg.Server.RequestTimeout}
	slog.Info("code-context service started", "listen", cfg.Server.Listen, "repositories", len(cfg.Repositories))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
