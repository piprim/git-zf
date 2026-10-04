package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ExecPath returns git's exec path, the directory git looks in for its
// subcommands.
func ExecPath(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "--exec-path").Output()
	if err != nil {
		return "", fmt.Errorf("git --exec-path: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}
