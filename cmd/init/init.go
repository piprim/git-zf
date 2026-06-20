package init_cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// prePushHookScript is the shell script written to .git/hooks/pre-push.
// It calls `git zf review guard` for each pushed branch so that pushes to
// branches locked for code review are blocked at the client side.
//
// Requirements:
//   - git zf must be installed in the git exec-path (run `git zf install` first).
//   - The hook is per-repository; run `git zf init` in every repo (and in every
//     git submodule) where the lock guard should be active.
const prePushHookScript = `#!/bin/sh
# git-zf review lock guard — installed by 'git zf init'
# Requires: git zf install (binary in git exec-path)
while IFS=' ' read -r local_ref local_sha remote_ref remote_sha; do
    branch=$(echo "$local_ref" | sed 's|^refs/heads/||')
    if ! git zf review guard "$branch" 2>&1; then
        exit 1
    fi
done
exit 0
`

// Init is the `git zf init` command.
type Init struct{}

// New creates an Init command.
func New() Init { return Init{} }

// GetRootCmd returns the `init` cobra command.
func (i Init) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize git-zf in the current repository (installs the pre-push hook)",
		Long: `Install the git-zf pre-push hook into the current repository.

The hook calls 'git zf review guard' before every push, blocking pushes to
branches that are locked for code review.

Works correctly in git submodules: the hook is written to the submodule's own
git directory (resolved via 'git rev-parse --git-dir'), not the parent repo.

Run 'git zf install' first to make the 'git zf' binary available in the git
exec-path, then run 'git zf init' once per repository (and per submodule).`,
		RunE: i.runE,
	}
}

func (i Init) runE(cmd *cobra.Command, _ []string) error {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	// GitDir resolves the real git directory via 'git rev-parse --git-dir',
	// which handles submodules (where <worktree>/.git is a gitlink file, not
	// a directory) and linked worktrees transparently.
	gitDir, err := client.GitDir()
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}

	hookPath := filepath.Join(gitDir, "hooks", "pre-push")

	// If a hook already exists, check whether it is ours.
	if info, err := os.Stat(hookPath); err == nil {
		existing, readErr := os.ReadFile(hookPath) //nolint:gosec
		if readErr == nil {
			if string(existing) == prePushHookScript {
				fmt.Fprintf(cmd.OutOrStdout(), "Pre-push hook already up to date at %s\n", hookPath)
				return nil
			}
			// Foreign hook: ensure it is at least executable, then warn.
			if info.Mode()&0o111 == 0 {
				_ = os.Chmod(hookPath, info.Mode()|0o755)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"WARNING: a pre-push hook already exists at %s\n"+
					"The git-zf lock guard was NOT installed to avoid overwriting your hook.\n"+
					"To enable the guard, add this to your existing hook:\n\n"+
					"  git zf review guard \"$(echo \"$local_ref\" | sed 's|^refs/heads/||')\"\n",
				hookPath)
			return nil
		}
	}

	// Ensure the hooks directory exists (submodules and bare repos may not have it).
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	//nolint:gosec // hook script is a compile-time constant
	if err := os.WriteFile(hookPath, []byte(prePushHookScript), 0o755); err != nil {
		return fmt.Errorf("write pre-push hook: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Pre-push hook installed at %s\n", hookPath)
	return nil
}
