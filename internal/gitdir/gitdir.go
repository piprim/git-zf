package gitdir

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Get returns the path to the .git directory for the current working tree.
// It resolves gitfiles, submodules, and linked worktrees via git rev-parse,
// without importing go-git.
func Get() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--git-dir")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}

	d := strings.TrimSpace(string(out))
	if !filepath.IsAbs(d) {
		wd, werr := os.Getwd()
		if werr != nil {
			return "", fmt.Errorf("getwd: %w", werr)
		}

		d = filepath.Join(wd, d)
	}

	return d, nil
}
