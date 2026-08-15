package runner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Run executes a fixed executable and argument vector. Callers must never pass a shell command.
func Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, stderr.String())
	}
	return out, nil
}
