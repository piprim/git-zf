package gitdir

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Common returns the path to the common .git directory shared by every
// worktree of the repository, resolving gitfiles and submodules via git
// rev-parse, without importing go-git. Inside a linked worktree this is the
// main .git, not the per-worktree dir (.git/worktrees/<name>): files that must
// be shared across worktrees (the repo-level config) belong here.
func Common() (string, error) {
	return revParse("--git-common-dir")
}

func revParse(flag string) (string, error) {
	cmd := exec.Command("git", "rev-parse", flag)

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
