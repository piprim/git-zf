package issueflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
)

// CloseCandidates returns the branches a close picker should offer: every
// in-progress tracked branch that resolves locally or on the remote. A branch
// started in another clone is a candidate as soon as its commits were fetched;
// the close flow creates the local branch with MaterializeBranch.
//
// Reads local chains only: callers run branch.Fetch first.
func CloseCandidates(ctx context.Context, c *git.Client) ([]branch.Row, error) {
	rows, err := branch.ListRows(ctx, c, branch.StatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list in-progress branches: %w", err)
	}

	out := rows[:0]
	for i := range rows {
		if _, resolveErr := c.ResolveBranchRef(rows[i].BranchName); resolveErr == nil {
			out = append(out, rows[i])
		}
	}

	return out, nil
}

// LocalInProgress returns the in-progress tracked branches that exist as local
// branches: what a picker that acts on a checked-out branch offers. Reads
// local chains only: callers run branch.Fetch first.
func LocalInProgress(ctx context.Context, c *git.Client) ([]branch.Row, error) {
	rows, err := branch.ListRows(ctx, c, branch.StatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list in-progress branches: %w", err)
	}

	out := rows[:0]
	for i := range rows {
		if exists, _ := c.BranchExists(rows[i].BranchName); exists {
			out = append(out, rows[i])
		}
	}

	return out, nil
}

// MaterializeBranch creates the local branch branchName when it is absent,
// starting from the ref it resolves against (origin). Returns created=true
// when this call created the branch, so the caller can roll it back if its
// flow aborts before the merge commit lands.
func MaterializeBranch(ctx context.Context, c *git.Client, branchName string) (created bool, err error) {
	exists, err := c.BranchExists(branchName)
	if err != nil {
		return false, fmt.Errorf("check branch %q exists: %w", branchName, err)
	}
	if exists {
		return false, nil
	}

	h, err := c.ResolveBranchRef(branchName)
	if err != nil {
		return false, fmt.Errorf("resolve feature branch %q: %w", branchName, err)
	}
	if err := c.CreateLocalBranch(ctx, branchName, h.String()); err != nil {
		return false, fmt.Errorf("materialize feature branch %q: %w", branchName, err)
	}

	return true, nil
}
