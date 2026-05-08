package git

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func ExecPath(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--exec-path")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("exec-path pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec-path start: %w", err)
	}
	result, err := io.ReadAll(stdout)
	if err != nil {
		return "", fmt.Errorf("exec-path read: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("exec-path wait: %w", err)
	}

	return strings.TrimSpace(string(result)), nil
}
