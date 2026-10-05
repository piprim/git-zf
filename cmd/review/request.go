package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Submit an issue branch for code review (locks the branch)",
		Args:  cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewRequestInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
	pushflow.AddFlags(cmd)
	return cmd
}

func runReviewRequestInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// A branch closed in a sibling clone carries Merged=true on its branch ref
	// but may still show in_progress in this clone's store. Reconcile first so
	// the picker never offers an already-closed branch.
	issueflow.ReconcileMergedFromRefs(ctx, deps.store, deps.client)

	branches, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	// Filter out branches whose open review is in_review (locked) or approved
	// (developer should close, not re-request). With review.require-signed an
	// approved review stays offered: the close gate may refuse it (unsigned
	// approval, commits after it) and a new round is the way out. Sync first so
	// the local ref namespace reflects the current remote state.
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}
	states, _, _ := reviewpkg.List(ctx, deps.client)
	locked := make(map[string]bool, len(states))
	for _, st := range states {
		approvedLocks := st.Status == reviewpkg.StatusApproved && !deps.cfg.Review.RequireSigned
		if !st.Closed && (st.Status == reviewpkg.StatusInReview || approvedLocks) {
			locked[st.Slug] = true
		}
	}

	var submittable []store.BranchRow
	for _, b := range branches {
		if !locked[b.IssueSlug] {
			submittable = append(submittable, b)
		}
	}

	if len(submittable) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No in-progress branches to submit for review.")
		fmt.Fprintln(deps.client.IO().Out, "Tip: run 'git zf issue track'.")

		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select branch to submit for review:", submittable, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	// Best-effort branch fetch so origin/<slug>@review is visible for the
	// pending-review offer (review refs were already fetched above).
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.Fetch(ctx)
	}

	if pending, pErr := issueflow.PendingReviewCommits(ctx, deps.client, picked.IssueSlug, picked.BranchName); pErr == nil && pending != nil {
		ok, cErr := prompter.Confirm(ctx, fmt.Sprintf(
			"%s has %d reviewer commit(s) not in %q — merge now?",
			pending.EffectiveRef, pending.Commits, picked.BranchName))
		if cErr != nil {
			return fmt.Errorf("merge confirm: %w", cErr)
		}
		if !ok {
			return fmt.Errorf("request aborted: run 'git zf review sync' to incorporate reviewer commits first")
		}
		if conflicted, mErr := mergeReviewerCommits(ctx, deps, pending, picked.BranchName); mErr != nil || conflicted {
			return mErr
		}
	}

	if err := runReviewRequest(ctx, deps, picked.IssueSlug); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug string) error {
	// Sync first so Load reflects what the remote has, and so the request op
	// lands on the existing chain rather than beside it.
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	// A blob ref written by an older git-zf is not migrated: this request
	// replaces it, once every check below has passed.
	existing, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	legacy := errors.Is(err, reviewpkg.ErrLegacyReview)
	if err != nil && !legacy {
		return fmt.Errorf("read review ref: %w", err)
	}
	if existing != nil && existing.Status == reviewpkg.StatusInReview {
		return fmt.Errorf("issue %q is already in review (round %d) — awaiting reviewer decision",
			issueSlug, existing.Round)
	}

	// Find the feature branch for this issue.
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var featureBranch string
	for _, b := range branches {
		if b.IssueSlug == issueSlug && b.Status == store.BranchStatusInProgress {
			featureBranch = b.BranchName
			break
		}
	}
	if featureBranch == "" {
		return fmt.Errorf("no in-progress branch found for issue %q", issueSlug)
	}

	featureSHA, err := deps.client.ResolveRef("refs/heads/" + featureBranch)
	if err != nil {
		return fmt.Errorf("resolve feature branch HEAD: %w", err)
	}

	// Refuse to delete reviewer work that was never incorporated. This is the
	// safety net; the interactive wrapper offers an inline merge first.
	// A detection error must also refuse (fail closed) since we're about to
	// irreversibly delete the local and remote review branch below. The state
	// of a legacy review is unknown, so any reviewer commit counts.
	var pending *issueflow.PendingReview
	if legacy {
		effective, n, aErr := issueflow.ReviewBranchAhead(ctx, deps.client, issueSlug, featureBranch)
		if aErr != nil {
			return fmt.Errorf("detect pending review commits: %w", aErr)
		}
		if n > 0 {
			pending = &issueflow.PendingReview{EffectiveRef: effective, Commits: n}
		}
	} else {
		var pErr error
		pending, pErr = issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, featureBranch)
		if pErr != nil {
			return fmt.Errorf("detect pending review commits: %w", pErr)
		}
	}
	if pending != nil && legacy {
		// review sync cannot read a review written by an older git-zf.
		return fmt.Errorf(
			"%s has %d unincorporated reviewer commit(s) from the previous round.\n"+
				"Merge %s into %q by hand, or delete the review branch to discard those commits, then re-request",
			pending.EffectiveRef, pending.Commits, pending.EffectiveRef, featureBranch)
	}
	if pending != nil {
		return fmt.Errorf(
			"%s has %d unincorporated reviewer commit(s) from the previous round.\n"+
				"Run 'git zf review sync' to incorporate them first "+
				"(or delete the branch to discard them), then re-request",
			pending.EffectiveRef, pending.Commits)
	}

	// Delete any stale review branch from a previous rejected round.
	reviewBranch := branch.ReviewBranchName(issueSlug)
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete stale %s: %v\n", reviewBranch, err)
		}
		_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
	}

	// Write and push the request op (the chain is the source of truth), then
	// mirror the round in the store. A legacy blob is replaced only once the
	// op is written.
	op := &reviewpkg.Op{Type: reviewpkg.OpRequest, FeatureSHA: featureSHA.String()}
	write := reviewpkg.Append
	if legacy {
		write = reviewpkg.ReplaceLegacyWith
	}
	if err := write(ctx, deps.client, issueSlug, op, false); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, deps.client, issueSlug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
		if remote, _ := deps.client.Remote(); legacy && remote != "" {
			fmt.Fprintf(deps.client.IO().Err,
				"if the remote still holds the old review ref, delete it with: git push %s --delete refs/zf/reviews/%s\n",
				remote, issueSlug)
		}
	}

	st, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref after request: %w", err)
	}
	if st == nil {
		return fmt.Errorf("read review ref after request: review %s not found", issueSlug)
	}

	reviewRow, err := deps.store.InsertReview(ctx, issueSlug, "")
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}
	// InsertReview counts the rows of this clone; the chain knows the round.
	if reviewRow.Round != st.Round {
		if err := deps.store.SetReviewRound(ctx, reviewRow.ID, st.Round); err == nil {
			reviewRow.Round = st.Round
		}
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q is now in review (round %d). Branch %q is locked.\n"+
			"Share with your reviewer: git fetch && git zf review start\n",
		issueSlug, st.Round, featureBranch)

	if err := proposeReviewPush(ctx, deps, featureBranch); err != nil {
		return err
	}

	return nil
}
