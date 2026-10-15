package mergeflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/tracker"
)

// ForgeMerge asks the forge to merge Source's open pull request instead of
// merging locally: the forge composes the commit, Target is then
// fast-forwarded onto the remote's.
type ForgeMerge struct {
	Forge  tracker.PullRequests
	Number string
}

// methodOf maps the picked strategy to the forge's merge method.
func methodOf(s commit.MergeStrategy) tracker.MergeMethod {
	switch s {
	case commit.MergeStrategySquash:
		return tracker.MergeMethodSquash
	case commit.MergeStrategyRebase:
		return tracker.MergeMethodRebase
	default:
		return tracker.MergeMethodMerge
	}
}

// forge merges on the forge, then fetches and fast-forwards the local target.
// A refusal of the forge is the error: nothing changed locally. A target that
// cannot be fast-forwarded (local commits of its own) is a warning: the forge
// merge is done.
func (r *run) forge(ctx context.Context, f *ForgeMerge, s commit.MergeStrategy) error {
	if err := f.Forge.MergePullRequest(ctx, f.Number, methodOf(s)); err != nil {
		return fmt.Errorf("merge pull request #%s on the forge: %w", f.Number, err)
	}
	fmt.Fprintf(r.client.IO().Out, "Pull request #%s merged on the forge (%s).\n", f.Number, s)

	remote, err := r.client.Remote()
	if err != nil || remote == "" {
		return nil
	}
	if err := r.client.Fetch(ctx); err != nil {
		fmt.Fprintf(r.client.IO().Err, "warning: fetch after the merge: %v\n", err)

		return nil
	}
	if err := r.client.FastForwardOnly(ctx, remote+"/"+r.target, r.target); err != nil {
		fmt.Fprintf(r.client.IO().Err,
			"warning: %q was not fast-forwarded to %s/%s (merge it yourself): %v\n", r.target, remote, r.target, err)
	}

	return nil
}
