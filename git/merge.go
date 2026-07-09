package git

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/piprim/git-zf/internal/pkg"
)

// runInteractive runs a git command in dir with the client's configured IO
// streams. Stdout/stderr are teed to the terminal live and captured for errors.
func (c *Client) runInteractive(ctx context.Context, dir string, args ...string) error {
	err := pkg.RunInteractive(ctx, c.io, "git", dir, args...)
	if err != nil {
		return fmt.Errorf("git command failed: %w", err)
	}

	return nil
}

// MergeDryRun checks whether branchName merges cleanly into baseBranch.
// It uses `git merge-tree --write-tree` (git 2.38+) to perform a 3-way merge
// in-memory: the working tree is never touched, no hooks run, and submodules
// are not traversed. Returns the list of conflicting file paths, or nil if clean.
func (c *Client) MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error) {
	slog.Debug("Git dry run merge…")
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"merge-tree", "--write-tree", baseBranch, branchName)
	slog.Debug("git merge-tree --write-tree…")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil, nil
	}

	// merge-tree exits 1 on conflicts; any other exit code is a real error.
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		return nil, fmt.Errorf("merge-tree: %w: %s", err, out)
	}

	slog.Debug("Parsing git merge conflict files…")
	conflicts := parseConflictFiles(string(out))
	if len(conflicts) == 0 {
		return nil, fmt.Errorf("merge-tree reported conflicts but no files parsed: %s", out)
	}

	return conflicts, nil
}

// MergeSquash checks out baseBranch and squash-merges branchName into it,
// leaving the squashed changes staged. The caller is responsible for the
// follow-up commit (so it can drive an interactive commit form).
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--squash", branchName); err != nil {
		return fmt.Errorf("merge --squash %s: %w", branchName, err)
	}

	return nil
}

// MergeRebase prepares featureBranch for a single-commit close. When a remote
// is configured, it merges against remote/<baseBranch> and soft-resets to the
// same ref. When no remote is available (local-only repo), it uses the local
// baseBranch directly.
//
// The mechanic is a real `git merge --no-edit <remoteBase>` (submodule-safe —
// handles gitlinks correctly, unlike `merge --squash`) followed by `git reset
// --soft <remoteBase>`, leaving HEAD at <remoteBase>, the working tree at the
// merged state, and the index staged with the consolidated diff. The transient
// merge commit produced by the merge step is unreachable after the reset and is
// eventually garbage-collected — `--no-edit` is what prevents git from opening
// $EDITOR for that throwaway commit message.
//
// Caller is responsible for the final commit (typically via the commitizen TUI
// form) and for rollback on failure.
func (c *Client) MergeRebase(ctx context.Context, featureBranch, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	var remoteBase string
	if remote != "" {
		remoteBase = remote + "/" + baseBranch
	} else {
		remoteBase = baseBranch
	}

	if err := c.Checkout(ctx, featureBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", featureBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-edit", remoteBase); err != nil {
		return fmt.Errorf("merge %s: %w", remoteBase, err)
	}

	if err := c.runInteractive(ctx, root, "reset", "--soft", remoteBase); err != nil {
		return fmt.Errorf("reset --soft %s: %w", remoteBase, err)
	}

	return nil
}

// MergeNoFFNoCommit checks out baseBranch and runs `git merge --no-ff
// --no-commit <featureBranch>`. Leaves MERGE_HEAD + MERGE_MSG in place
// so the caller can drive the commit step itself (typically via the
// commitizen TUI form). Caller is responsible for `git merge --abort`
// on TUI abort or commit failure.
func (c *Client) MergeNoFFNoCommit(ctx context.Context, featureBranch, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-ff", "--no-commit", featureBranch); err != nil {
		return fmt.Errorf("merge --no-ff --no-commit %s: %w", featureBranch, err)
	}

	return nil
}

// AbortMerge runs `git merge --abort`. Used after a TUI abort or
// commit failure in the Classic close flow to clear MERGE_HEAD /
// MERGE_MSG and restore the working tree. Returns a wrapped error
// so callers can decide whether to treat a no-active-merge failure
// as fatal (e.g. by ignoring it when the orchestrator isn't sure
// whether MergeNoFFNoCommit actually started a merge).
func (c *Client) AbortMerge(ctx context.Context) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--abort"); err != nil {
		return fmt.Errorf("merge --abort: %w", err)
	}

	return nil
}

// FastForwardOnly checks out targetBranch and runs `git merge --ff-only sourceBranch`.
// Returns a wrapped error when the FF is refused (diverged history) so the
// caller can render an actionable message.
func (c *Client) FastForwardOnly(ctx context.Context, sourceBranch, targetBranch string) error {
	if err := c.Checkout(ctx, targetBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--ff-only", sourceBranch); err != nil {
		return fmt.Errorf("merge --ff-only %s: %w", sourceBranch, err)
	}

	return nil
}

// DeleteLocalBranch deletes the local branch by name.
// force=true uses -D (required after squash merges); force=false uses -d (safe).
func (c *Client) DeleteLocalBranch(ctx context.Context, name string, force bool) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	flag := "-d"
	if force {
		flag = "-D"
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "branch", flag, name)
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	out, err := cmd.CombinedOutput()
	if err != nil {
		if !force && strings.Contains(string(out), "not fully merged") {
			return fmt.Errorf("delete branch %s: %w (%s)", name, ErrBranchNotMerged, strings.TrimSpace(string(out)))
		}

		return fmt.Errorf("delete branch %s: %w: %s", name, err, out)
	}

	return nil
}

// Fetch runs `git fetch <remote>`. Returns nil immediately when no remote is
// configured (local-only repo). Returns a wrapped error when the remote is
// unreachable or auth fails.
func (c *Client) Fetch(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "fetch", remote); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}

	return nil
}

// IsAncestor reports whether child is an ancestor of ancestor (or equal).
// Wraps `git merge-base --is-ancestor child ancestor`: exit code 0 → true,
// exit code 1 → false, any other exit code → wrapped error.
func (c *Client) IsAncestor(ctx context.Context, child, ancestor string) (bool, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", child, ancestor)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	return false, fmt.Errorf("merge-base --is-ancestor %s %s: %w: %s", child, ancestor, err, out)
}

// ResetHard runs `git reset --hard <target>`. Used by the close orchestrator
// to atomically roll the current branch back to its original tip on TUI abort
// or commit failure. Does not touch untracked files.
func (c *Client) ResetHard(ctx context.Context, target string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "reset", "--hard", target); err != nil {
		return fmt.Errorf("reset --hard %s: %w", target, err)
	}

	return nil
}

// MergeForward checks out targetBranch and runs `git merge --no-edit sourceBranch`,
// creating a merge commit. Used by `review sync` to integrate a parent integration
// branch into a drifted sub-task branch without rewriting history (no force-push needed).
// Returns a wrapped error on conflict; the caller should run AbortMerge to clean up.
func (c *Client) MergeForward(ctx context.Context, sourceBranch, targetBranch string) error {
	if err := c.Checkout(ctx, targetBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-edit", sourceBranch); err != nil {
		return fmt.Errorf("merge --no-edit %s: %w", sourceBranch, err)
	}

	return nil
}

// parseConflictFiles extracts file paths from git merge conflict output lines.
// Each conflict line looks like: "CONFLICT (content): Merge conflict in path/to/file.go"
func parseConflictFiles(output string) []string {
	var files []string

	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "CONFLICT") {
			continue
		}

		if idx := strings.LastIndex(line, " in "); idx >= 0 {
			files = append(files, strings.TrimSpace(line[idx+4:]))
		}
	}

	return files
}

// ErrMergeConflicts marks a merge stopped by content conflicts. The merge is
// intentionally left in progress (MERGE_HEAD present) so the user can resolve
// the markers and conclude it. Detect with errors.Is.
var ErrMergeConflicts = errors.New("merge conflicts")

// MergeInProgress reports whether a merge is currently in progress, i.e.
// MERGE_HEAD exists in the repository's git directory.
func (c *Client) MergeInProgress() (bool, error) {
	gitDir, err := c.GitDir()
	if err != nil {
		return false, fmt.Errorf("git dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat MERGE_HEAD: %w", err)
	}
	return true, nil
}

// MergeLeaveConflicts checks out targetBranch and merges sourceBranch into it
// with `git merge --no-edit`. Unlike MergeForward, a conflicted merge is left
// in progress (no abort) and the returned error wraps ErrMergeConflicts, so
// callers can tell "resolve and conclude" apart from a hard failure. Failure
// classification never parses git's message text: non-zero exit with
// MERGE_HEAD present is a conflict; without MERGE_HEAD the raw error passes
// through.
func (c *Client) MergeLeaveConflicts(ctx context.Context, sourceBranch, targetBranch string) error {
	if err := c.Checkout(ctx, targetBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-edit", sourceBranch); err != nil {
		if inProgress, mhErr := c.MergeInProgress(); mhErr == nil && inProgress {
			return fmt.Errorf("merge %s into %s: %w", sourceBranch, targetBranch, ErrMergeConflicts)
		}
		return fmt.Errorf("merge --no-edit %s: %w", sourceBranch, err)
	}

	return nil
}
