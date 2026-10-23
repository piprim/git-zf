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
	// PushFirst pushes Source right before the merge: the caller merged
	// reviewer commits into it after checking it against the remote. The
	// push is deferred to here so that a close declined at the confirm, or
	// stopped by a conflict or an open sub-task, leaves the remote untouched.
	PushFirst bool
	// Upstream is the URL of the project the pull request lives on when the
	// remote is a fork of it (issueflow.ForkUpstream): Target is then
	// fetched from there, since the fork does not hold the merge, and pushed
	// to the remote so the fork catches up. "" ⇒ the remote is the project.
	Upstream string
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

// forge pushes Source when asked, merges on the forge the head this clone
// holds, then fetches and fast-forwards the local target (from Upstream when
// the remote is a fork, and pushes it to the fork). A refusal of the forge
// (the head moved included) is the error: nothing changed locally. A target
// that cannot be fast-forwarded (local commits of its own) is a warning: the
// forge merge is done.
func (r *run) forge(ctx context.Context, f *ForgeMerge, s commit.MergeStrategy) error {
	if f.PushFirst {
		if err := r.client.PushBranch(ctx, r.source); err != nil {
			return fmt.Errorf("push %q before the merge: %w", r.source, err)
		}
	}
	head, err := r.client.ResolveRef("refs/heads/" + r.source)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", r.source, err)
	}
	if err := f.Forge.MergePullRequest(ctx, f.Number, methodOf(s), string(head)); err != nil {
		return fmt.Errorf("merge pull request #%s on the forge: %w", f.Number, err)
	}
	fmt.Fprintf(r.client.IO().Out, "Pull request #%s merged on the forge (%s).\n", f.Number, s)

	remote, err := r.client.Remote()
	if err != nil || remote == "" {
		return nil
	}
	if f.Upstream != "" {
		return r.catchUpFork(ctx, f.Upstream, remote)
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

// catchUpFork fetches the target from upstream, where the forge merged,
// fast-forwards the local target onto it and pushes it to remote, the fork.
// Each step that fails is a warning naming what to do: the merge is done.
func (r *run) catchUpFork(ctx context.Context, upstream, remote string) error {
	errW := r.client.IO().Err
	if err := r.client.FetchRef(ctx, upstream, r.target); err != nil {
		fmt.Fprintf(errW, "warning: %q not fetched from the upstream (fetch it yourself): %v\n", r.target, err)

		return nil
	}
	if err := r.client.FastForwardOnly(ctx, "FETCH_HEAD", r.target); err != nil {
		fmt.Fprintf(errW, "warning: %q was not fast-forwarded to the upstream's (merge it yourself): %v\n", r.target, err)

		return nil
	}
	if err := r.client.PushBranch(ctx, r.target); err != nil {
		fmt.Fprintf(errW, "warning: %q not pushed to %s (push it yourself): %v\n", r.target, remote, err)
	}

	return nil
}
