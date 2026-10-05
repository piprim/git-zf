package issueflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
)

// ResolveParentBranch computes the merge target for an issue: the configured
// base (cfgBase, or DefaultBaseBranch when empty), redirected to the parent
// integration branch when issueSlug has a parent.
//
// The parent relation is the one recorded on the issue's branch chain; the
// parent's branch is the newest one its own chain knows. It reads local chains
// only: callers that want what the remote has run branch.Fetch first. Close and
// commit share this one implementation.
//
// A parent whose ref is still in the old blob format is an error, not "no
// parent": falling back to the base would silently change the merge target.
func ResolveParentBranch(ctx context.Context, c *git.Client, issueSlug, cfgBase string) (string, error) {
	base := cfgBase
	if base == "" {
		detected, err := c.DefaultBaseBranch()
		if err != nil {
			return "", fmt.Errorf("detect base branch: %w", err)
		}
		base = detected
	}

	st, err := branch.Load(ctx, c, issueSlug)
	if err != nil && !errors.Is(err, branch.ErrLegacyBranch) {
		return "", fmt.Errorf("check parent issue: %w", err)
	}
	if st == nil || st.Parent == "" {
		return base, nil
	}

	parent, err := branch.Load(ctx, c, st.Parent)
	if errors.Is(err, branch.ErrLegacyBranch) {
		return "", fmt.Errorf(
			"parent issue %q of %q is tracked in the old format.\n"+
				"Check out its branch and run `git zf issue track`: %w", st.Parent, issueSlug, err)
	}
	if err != nil {
		return "", fmt.Errorf("read branches of parent %q: %w", st.Parent, err)
	}
	if parent == nil {
		return base, nil
	}

	if rows := branch.Rows([]branch.State{*parent}, branch.StatusAll); len(rows) > 0 {
		return rows[0].BranchName, nil
	}

	return base, nil
}
