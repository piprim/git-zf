package issueflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// ParentStore is the slice of *store.Store that ResolveParentBranch needs.
type ParentStore interface {
	GetParentIssue(ctx context.Context, childSlug string) (string, error)
	ListBranches(ctx context.Context, status store.BranchStatus) ([]store.BranchRow, error)
}

// ParentClient is the slice of *git.Client that ResolveParentBranch needs.
type ParentClient interface {
	DefaultBaseBranch() (string, error)
	FetchBranchRefs(ctx context.Context) error
	ReadBranchRef(ctx context.Context, issueSlug string) (*git.BranchRef, error)
}

// ResolveParentBranch computes the merge target for an issue: the configured
// base (cfgBase, or DefaultBaseBranch when empty), redirected to the parent
// integration branch when issueSlug has a parent.
//
// The store is checked first for the parent relation; on a cross-machine clone
// where the store has no record, the refs/zf/branches/<slug> git ref is the
// fallback (FetchBranchRefs runs best-effort first so a later read sees fresh
// refs). The parent's branch *name* is resolved from the store, then from the
// parent's own branch ref. Extracted verbatim from cmd/issue/close.go's
// resolveDefaultBase so close and commit share one implementation.
func ResolveParentBranch(ctx context.Context, s ParentStore, c ParentClient, issueSlug, cfgBase string) (string, error) {
	base := cfgBase
	if base == "" {
		detected, err := c.DefaultBaseBranch()
		if err != nil {
			return "", fmt.Errorf("detect base branch: %w", err)
		}
		base = detected
	}

	parentSlug, err := s.GetParentIssue(ctx, issueSlug)
	if err != nil {
		return "", fmt.Errorf("check parent issue: %w", err)
	}
	if parentSlug == "" {
		// One fetch retrieves all refs/zf/branches/* atomically.
		_ = c.FetchBranchRefs(ctx)
		if br, _ := c.ReadBranchRef(ctx, issueSlug); br != nil {
			parentSlug = br.ParentSlug
		}
	}
	if parentSlug == "" {
		return base, nil
	}

	// Try store first for the parent branch name.
	parentBranches, listErr := s.ListBranches(ctx, store.BranchStatusAll)
	if listErr != nil {
		return "", fmt.Errorf("list branches for parent %q: %w", parentSlug, listErr)
	}
	for _, b := range parentBranches {
		if b.IssueSlug == parentSlug {
			return b.BranchName, nil
		}
	}
	// Store miss — read the parent's branch ref for the branch name.
	if parentBR, _ := c.ReadBranchRef(ctx, parentSlug); parentBR != nil {
		return parentBR.BranchName, nil
	}

	return base, nil
}

// Compile-time checks that the production types satisfy the roles.
var (
	_ ParentStore  = (*store.Store)(nil)
	_ ParentClient = (*git.Client)(nil)
)
