package init_cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// prePushHookScript is the shell script written as the pre-push hook.
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

// preCommitHookScript is the shell script written as the pre-commit hook.
// It calls `git zf review guard-commit`, which blocks new commits on a feature
// branch while reviewer commits on <slug>@review await incorporation.
const preCommitHookScript = `#!/bin/sh
# git-zf review commit guard — installed by 'git zf init'
# Requires: git zf install (binary in git exec-path)
if ! git zf review guard-commit; then
    exit 1
fi
exit 0
`

// hookSpec describes one hook managed by `git zf init`.
type hookSpec struct {
	name    string // file name in the hooks directory
	script  string // full managed script body
	snippet string // line to suggest when a foreign hook already exists
}

var managedHooks = []hookSpec{
	{
		name:    "pre-push",
		script:  prePushHookScript,
		snippet: `  git zf review guard "$(echo "$local_ref" | sed 's|^refs/heads/||')"`,
	},
	{
		name:    "pre-commit",
		script:  preCommitHookScript,
		snippet: `  git zf review guard-commit || exit 1`,
	},
}

// Init is the `git zf init` command.
type Init struct{}

// New creates an Init command.
func New() Init { return Init{} }

// GetRootCmd returns the `init` cobra command.
func (i Init) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize git-zf in the current repository (installs the pre-push and pre-commit hooks)",
		Long: `Install the git-zf pre-push and pre-commit hooks into the current repository.

The pre-push hook calls 'git zf review guard' before every push, blocking pushes to
branches that are locked for code review. The pre-commit hook calls 'git zf review
guard-commit', which blocks new commits on a feature branch while reviewer commits
await incorporation.

The hooks are written where git reads them (resolved via 'git rev-parse
--git-path hooks'): the directory named by core.hooksPath when it is set,
otherwise the hooks directory shared by every worktree. In a git submodule that
is the submodule's own git directory, not the parent repo's.

It also adds two fetch refspecs to every remote, so that a plain 'git fetch'
brings the reviews and issues stored under refs/zf/ and 'git fetch --prune'
does not delete their tracking refs.

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

	// HooksDir is where git reads hooks from: core.hooksPath when set,
	// otherwise the hooks directory of the common git dir. The per-worktree
	// git dir of a linked worktree (--git-dir) is never searched, and in a
	// submodule (where <worktree>/.git is a gitlink file, not a directory) it
	// is the submodule's own git directory.
	hooksDir, err := client.HooksDir()
	if err != nil {
		return fmt.Errorf("resolve hooks dir: %w", err)
	}

	for _, h := range managedHooks {
		if err := installHook(cmd, hooksDir, h); err != nil {
			return err
		}
	}

	// Before branch records moved into the repository they lived in a SQLite
	// file, which no command reads any more.
	if commonDir, err := client.CommonDir(); err == nil {
		legacyDB := filepath.Join(commonDir, "git-zf.db")
		if _, statErr := os.Stat(legacyDB); statErr == nil {
			fmt.Fprintf(cmd.OutOrStdout(),
				"%s is no longer used and can be deleted: run `git zf issue track` on each branch still in progress\n",
				legacyDB)
		}
	}

	remotes, err := client.ConfigureChainFetch(cmd.Context())
	if err != nil {
		return fmt.Errorf("configure fetch refspecs: %w", err)
	}
	for _, remote := range remotes {
		fmt.Fprintf(cmd.OutOrStdout(),
			"remote %q: `git fetch` now brings reviews, issues and branch records (refs/zf/*)\n", remote)
	}

	return nil
}

// installHook writes one managed hook, preserving foreign hooks. Mirrors the
// original single-hook logic: byte-identical → up-to-date no-op; foreign hook
// → never overwrite, print the snippet to add manually; missing → write.
func installHook(cmd *cobra.Command, hooksDir string, h hookSpec) error {
	hookPath := filepath.Join(hooksDir, h.name)

	if info, err := os.Stat(hookPath); err == nil {
		existing, readErr := os.ReadFile(hookPath) //nolint:gosec
		if readErr == nil {
			if string(existing) == h.script {
				fmt.Fprintf(cmd.OutOrStdout(), "%s hook already up to date at %s\n", h.name, hookPath)
				return nil
			}
			if info.Mode()&0o111 == 0 {
				_ = os.Chmod(hookPath, info.Mode()|0o755)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"WARNING: a %s hook already exists at %s\n"+
					"The git-zf guard was NOT installed to avoid overwriting your hook.\n"+
					"To enable the guard, add this to your existing hook:\n\n%s\n",
				h.name, hookPath, h.snippet)
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	//nolint:gosec // hook script is a compile-time constant
	if err := os.WriteFile(hookPath, []byte(h.script), 0o755); err != nil {
		return fmt.Errorf("write %s hook: %w", h.name, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s hook installed at %s\n", h.name, hookPath)
	return nil
}
