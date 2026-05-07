package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// MergeDryRun checks whether branchName merges cleanly into baseBranch.
// It performs the check in a temporary git worktree so the main working tree
// is never modified. Returns the list of conflicting file paths, or nil if clean.
func (c *Client) MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "git-zf-dry-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// --detach avoids "already checked out" error when baseBranch is current.
	addOut, addErr := exec.CommandContext(ctx,
		"git", "-C", root, "worktree", "add", "--detach", tmpDir, baseBranch,
	).CombinedOutput()
	if addErr != nil {
		_ = os.RemoveAll(tmpDir)

		return nil, fmt.Errorf("add worktree: %w: %s", addErr, addOut)
	}

	defer func() {
		rmCmd := exec.CommandContext(context.Background(),
			"git", "-C", root, "worktree", "remove", "--force", tmpDir)
		_ = rmCmd.Run()
		_ = os.RemoveAll(tmpDir)
	}()

	mergeCmd := exec.CommandContext(ctx, "git", "merge", "--no-commit", "--no-ff", branchName)
	mergeCmd.Dir = tmpDir
	out, mergeErr := mergeCmd.CombinedOutput()

	// Always abort the in-progress merge to restore the worktree.
	abortCmd := exec.CommandContext(context.Background(), "git", "merge", "--abort")
	abortCmd.Dir = tmpDir
	if abortErr := abortCmd.Run(); abortErr != nil {
		// merge --abort fails when there were no conflicts (merge was staged but clean).
		// Fall back to hard reset.
		resetCmd := exec.CommandContext(context.Background(), "git", "reset", "--hard", "HEAD")
		resetCmd.Dir = tmpDir
		_ = resetCmd.Run()
	}

	if mergeErr == nil {
		return nil, nil
	}

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

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "checkout", baseBranch).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %w: %s", baseBranch, err, out)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "merge", "--squash", branchName).CombinedOutput(); err != nil {
		return fmt.Errorf("merge --squash %s: %w: %s", branchName, err, out)
	}

	msg := "squash merge '" + branchName + "'"
	commitArgs := []string{"-C", root, "commit", "-m", msg}
	if author != "" {
		commitArgs = append(commitArgs, "--author="+author)
	}

	if out, err := exec.CommandContext(ctx, "git", commitArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("commit squash: %w: %s", err, out)
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

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "checkout", baseBranch).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %w: %s", baseBranch, err, out)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "merge", "--no-ff", branchName).CombinedOutput(); err != nil {
		return fmt.Errorf("merge --no-ff %s: %w: %s", branchName, err, out)
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
