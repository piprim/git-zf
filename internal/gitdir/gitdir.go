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
// without importing go-git. Inside a linked worktree this is the per-worktree
// dir (.git/worktrees/<name>), which is where MERGE_HEAD and friends live.
func Get() (string, error) {
	return revParse("--git-dir")
}

// Common returns the path to the common .git directory shared by every
// worktree of the repository. In the main working tree it equals Get; inside
// a linked worktree Get returns .git/worktrees/<name> while Common returns the
// main .git. Files that must be shared across worktrees (the git-zf store)
// belong here.
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
