package lsp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestShutdownKillsUnresponsiveLanguageServer(t *testing.T) {
	client, err := Start("sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = client.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context deadline exceeded", err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("language server process was not terminated")
	}
}
