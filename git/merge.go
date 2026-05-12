package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
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
// It performs the check in a temporary git worktree so the main working tree
// is never modified. Returns the list of conflicting file paths, or nil if clean.
func (c *Client) MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error) {
	slog.Debug("Git dry run merge…")
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "git-zf-dry-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// --detach avoids "already checked out" error when baseBranch is current.
	// addOut, addErr :=  c.runInteractive(ctx, root, "merge", "--squash", branchName)
	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "add", "--detach", tmpDir, baseBranch)
	buf := new(bytes.Buffer)
	cmd.Stdin = os.Stdin
	cmd.Stdout = buf
	slog.Debug("creating git worktree…")
	addErr := cmd.Run()

	if addErr != nil {
		_ = os.RemoveAll(tmpDir)

		return nil, fmt.Errorf("add worktree: %w: %s", addErr, buf.String())
	}

	slog.Debug(buf.String())

	defer func() {
		rmCmd := exec.CommandContext(context.Background(),
			"git", "-C", root, "worktree", "remove", "--force", tmpDir)
		slog.Debug("Removing git worktree")
		_ = rmCmd.Run()
		_ = os.RemoveAll(tmpDir)
	}()

	mergeCmd := exec.CommandContext(ctx, "git", "merge", "--no-commit", "--no-ff", branchName)
	mergeCmd.Stdin = os.Stdin
	mergeCmd.Stdout = os.Stdout
	mergeCmd.Dir = tmpDir
	slog.Debug("git merge --no-commit in the worktree…")
	out, mergeErr := mergeCmd.CombinedOutput()

	// Always abort the in-progress merge to restore the worktree.
	slog.Debug("Aborting git merge --no-commit in the worktree…")
	abortCmd := exec.CommandContext(context.Background(), "git", "merge", "--abort")
	abortCmd.Dir = tmpDir
	if abortErr := abortCmd.Run(); abortErr != nil {
		// merge --abort fails when there were no conflicts (merge was staged but clean).
		// Fall back to hard reset.
		slog.Debug("Fall back to hard reset!")
		resetCmd := exec.CommandContext(context.Background(), "git", "reset", "--hard", "HEAD")
		resetCmd.Dir = tmpDir
		_ = resetCmd.Run()
	}

	if mergeErr == nil {
		return nil, nil
	}

	slog.Debug("Parsing git merge conflict files…")
	conflicts := parseConflictFiles(string(out))
	if len(conflicts) == 0 {
		return nil, fmt.Errorf("merge dry-run failed: %w: %s", mergeErr, out)
	}

	return conflicts, nil
}

// MergeSquash squash-merges branchName into baseBranch and commits.
// author is "Name <email>"; if empty the git config identity is used.
// After the merge the working directory is on baseBranch.
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch, author string) error {
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

	msg := "squash merge '" + branchName + "'"
	commitArgs := []string{"commit", "-m", msg}
	if author != "" {
		commitArgs = append(commitArgs, "--author="+author)
	}

	if err := c.runInteractive(ctx, root, commitArgs...); err != nil {
		return fmt.Errorf("commit squash: %w", err)
	}

	return nil
}

// MergeNoFF runs a classic --no-ff merge of branchName into baseBranch.
// After the merge the working directory is on baseBranch.
func (c *Client) MergeNoFF(ctx context.Context, branchName, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-ff", branchName); err != nil {
		return fmt.Errorf("merge --no-ff %s: %w", branchName, err)
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

	out, err := exec.CommandContext(ctx, "git", "-C", root, "branch", flag, name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete branch %s: %w: %s", name, err, out)
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
