package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (r Review) getRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Submit an issue branch for code review (locks the branch)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewRequestInteractive(ctx, deps, newHuhReviewPrompter())
		},
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

	// Filter out branches whose review ref is already in_review (locked) or
	// approved (developer should close, not re-request). Fetch first so the
	// local ref namespace reflects the current remote state.
	_ = deps.client.FetchReviewRefs(ctx)
	allRefs, _ := deps.client.ListReviewRefs(ctx)
	var submittable []store.BranchRow
	for _, b := range branches {
		ref := allRefs[b.IssueSlug]
		if ref != nil {
			switch store.ReviewStatus(ref.Status) {
			case store.ReviewStatusInReview, store.ReviewStatusApproved:
				continue
			}
		}
		submittable = append(submittable, b)
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
		if dirty, dErr := deps.client.IsDirty(ctx); dErr == nil && dirty {
			return fmt.Errorf("working tree has uncommitted changes — cannot merge %s.\n"+
				"Run 'git stash', then 'git zf review sync', then 'git stash pop'", pending.EffectiveRef)
		}
		if mErr := deps.client.MergeLeaveConflicts(ctx, pending.EffectiveRef, picked.BranchName); mErr != nil {
			if errors.Is(mErr, git.ErrMergeConflicts) {
				fmt.Fprintf(deps.client.IO().Out,
					"Merge left in progress with conflicts.\n"+
						"Resolve the conflict markers, then run 'git zf commit' to conclude the merge.\n")
				return nil
			}
			return mErr
		}
		fmt.Fprintf(deps.client.IO().Out, "Reviewer commits incorporated into %q.\n", picked.BranchName)
	}

	if err := runReviewRequest(ctx, deps, picked.IssueSlug); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug string) error {
	// Fetch first so ReadReviewRef reflects the current remote state.
	// This is required for the CAS lease on subsequent review rounds.
	_ = deps.client.FetchReviewRefs(ctx)

	// Read the existing ref to get the current SHA (used as CAS lease) and to
	// guard against resubmitting when the reviewer has not yet decided.
	// Reading the ref (not the store) avoids stale-cache false positives.
	existingRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if existingRef != nil && existingRef.Status == string(store.ReviewStatusInReview) {
		round := 1
		if latest, _ := deps.store.GetLatestReview(ctx, issueSlug); latest != nil {
			round = latest.Round
		}
		return fmt.Errorf("issue %q is already in review (round %d) — awaiting reviewer decision", issueSlug, round)
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
	// irreversibly delete the local and remote review branch below.
	pending, pErr := issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, featureBranch)
	if pErr != nil {
		return fmt.Errorf("detect pending review commits: %w", pErr)
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

	// Create review record in store.
	reviewRow, err := deps.store.InsertReview(ctx, issueSlug, "")
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}

	// Write and push review ref (ref is the source of truth).
	// currentSHA is "" on the first-ever request (no prior ref), or the SHA of
	// the previous rejected/approved ref on subsequent rounds. Passing it to
	// both WriteReviewRef and PushReviewRef ensures CAS correctness: the local
	// write and the remote push both fail if something changed concurrently.
	newRef := git.ReviewRef{
		Status:     string(store.ReviewStatusInReview),
		Round:      reviewRow.Round,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if _, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}

	if err := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q is now in review (round %d). Branch %q is locked.\n"+
			"Share with your reviewer: git fetch && git zf review start\n",
		issueSlug, reviewRow.Round, featureBranch)

	if err := proposeReviewPush(ctx, deps, featureBranch); err != nil {
		return err
	}

	return nil
}
