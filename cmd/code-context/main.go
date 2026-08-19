package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/code-context/internal/api"
	"github.com/example/code-context/internal/config"
	"github.com/example/code-context/internal/lsp"
	"github.com/example/code-context/internal/repository"
	"github.com/example/code-context/internal/tools"
)

// embeddedConfig is set by the packaging script. It contains the Base64 form
// of config.yaml so the Linux artifact has no runtime configuration file.
var embeddedConfig string

func main() {
	errLog, err := os.OpenFile("err.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("open error log failed", "path", "err.log", "error", err)
		os.Exit(1)
	}
	defer errLog.Close()
	console := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	errorFile := slog.NewTextHandler(errLog, &slog.HandlerOptions{Level: slog.LevelError})
	slog.SetDefault(slog.New(teeHandler{console, errorFile}))

	configPath := flag.String("config", "", "external YAML configuration (overrides embedded configuration)")
	flag.Parse()
	cfg, err := loadConfig(*configPath)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	service := &tools.Service{Repos: repository.New(cfg), JDT: lsp.NewJDT(cfg.JDTLS.Command, cfg.JDTLS.Args, cfg.JDTLS.WorkspaceRoot), MaxResults: cfg.Server.MaxResults, MaxReadBytes: cfg.Server.MaxReadBytes, MaxDiffBytes: cfg.Server.MaxDiffBytes, MaxCallDepth: cfg.Server.MaxCallDepth}
	startupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	err = service.SyncAndWarm(startupCtx)
	cancel()
	if err != nil {
		slog.Error("repository synchronization or language-server warmup failed", "error", err)
		os.Exit(1)
	}
	srv := &http.Server{Addr: cfg.Server.Listen, Handler: api.New(service, cfg.Server.RequestTimeout, cfg.Server.MaxBatchRequests).Handler(), ReadHeaderTimeout: cfg.Server.RequestTimeout}
	slog.Info("code-context service started", "listen", cfg.Server.Listen, "repositories", len(cfg.Repositories))
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serverDone:
		if err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
	case sig := <-signals:
		slog.Info("shutdown signal received", "signal", sig.String())
		httpShutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), 30*time.Second)
		if err := srv.Shutdown(httpShutdownCtx); err != nil {
			slog.Error("HTTP server shutdown failed", "error", err)
		}
		cancelHTTP()
		jdtShutdownCtx, cancelJDT := context.WithTimeout(context.Background(), 10*time.Second)
		if err := service.Shutdown(jdtShutdownCtx); err != nil {
			slog.Error("JDT LS shutdown failed", "error", err)
		}
		cancelJDT()
		if err := <-serverDone; err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}
}

func loadConfig(path string) (config.Config, error) {
	if path != "" {
		return config.Load(path)
	}
	if embeddedConfig != "" {
		contents, err := base64.StdEncoding.DecodeString(embeddedConfig)
		if err != nil {
			return config.Config{}, err
		}
		return config.LoadBytes(contents)
	}
	return config.Load("config.yaml")
}

// teeHandler writes each record to every configured handler. Individual handler
// levels keep err.log limited to Error and above while retaining console output.
type teeHandler []slog.Handler

func (h teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}
func (h teeHandler) Handle(ctx context.Context, record slog.Record) error {
	var first error
	for _, handler := range h {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}
func (h teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(teeHandler, len(h))
	for i, handler := range h {
		out[i] = handler.WithAttrs(attrs)
	}
	return out
}
func (h teeHandler) WithGroup(name string) slog.Handler {
	out := make(teeHandler, len(h))
	for i, handler := range h {
		out[i] = handler.WithGroup(name)
	}
	return out
}

var _ slog.Handler = teeHandler{}
