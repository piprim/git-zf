package init_cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// The three scripts below are the hooks an older `git zf init` installed, kept
// byte for byte: init deletes a hook file that still holds one of them. The
// review no longer locks a branch nor has a branch of its own, so none of them
// has anything to guard.

// prePushHookScript called `git zf review guard` for each pushed branch.
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

// preCommitHookScript called `git zf review guard-commit`.
const preCommitHookScript = `#!/bin/sh
# git-zf review commit guard — installed by 'git zf init'
# Requires: git zf install (binary in git exec-path)
if ! git zf review guard-commit; then
    exit 1
fi
exit 0
`

// preRebaseHookScript refused to rebase a <slug>@review branch.
const preRebaseHookScript = `#!/bin/sh
# git-zf review rebase guard — installed by 'git zf init'
branch=${2:-$(git symbolic-ref --quiet --short HEAD)}
case "$branch" in
*@review)
    echo "git-zf: $branch is a review branch: do not rebase it, 'git zf issue close' would refuse it." >&2
    echo "Merge the base into it instead (bypass: git rebase --no-verify)." >&2
    exit 1
    ;;
esac
exit 0
`

// hookSpec is one hook an older `git zf init` installed.
type hookSpec struct {
	name   string // file name in the hooks directory
	script string // the exact script once installed
}

var retiredHooks = []hookSpec{
	{name: "pre-push", script: prePushHookScript},
	{name: "pre-commit", script: preCommitHookScript},
	{name: "pre-rebase", script: preRebaseHookScript},
}

// Init is the `git zf init` command.
type Init struct{}

// New creates an Init command.
func New() Init { return Init{} }

// GetRootCmd returns the `init` cobra command.
func (i Init) GetRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize git-zf in the current repository (fetch refspecs for refs/zf/)",
		Long: `Add fetch refspecs to every remote, so that a plain 'git fetch' brings the
reviews, issues and branch records stored under refs/zf/ and 'git fetch --prune'
does not delete their tracking refs.

git-zf installs no hook. An older 'git zf init' installed a pre-push, a
pre-commit and a pre-rebase hook: a hook file that still holds one of those
scripts unchanged is deleted, where git reads hooks (the directory named by
core.hooksPath when it is set, otherwise the hooks directory shared by every
worktree). A hook of your own is never touched; when it calls 'git zf review
guard', init says that the call can be removed.

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

	for _, h := range retiredHooks {
		if err := retireHook(cmd, hooksDir, h); err != nil {
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

// retireHook deletes a hook file that still holds the script an older init
// installed. A hook with other content is the user's: it is left alone, with
// a note when it still calls the guard, which does nothing now.
func retireHook(cmd *cobra.Command, hooksDir string, h hookSpec) error {
	hookPath := filepath.Join(hooksDir, h.name)

	existing, err := os.ReadFile(hookPath) //nolint:gosec
	if err != nil {
		return nil //nolint:nilerr // no hook, or one we cannot read: nothing to retire
	}

	if string(existing) != h.script {
		if strings.Contains(string(existing), "git zf review guard") {
			fmt.Fprintf(cmd.OutOrStdout(),
				"%s calls `git zf review guard`, which does nothing any more: the call can be removed\n",
				hookPath)
		}

		return nil
	}

	if err := os.Remove(hookPath); err != nil {
		return fmt.Errorf("delete the %s hook: %w", h.name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s deleted: git-zf no longer uses a %s hook\n", hookPath, h.name)

	return nil
}
