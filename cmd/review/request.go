package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Submit an issue branch for code review",
		Long: `Pick an in-progress branch (the current one is pre-selected) and submit it
for review. Its review ref becomes "in_review" and records the branch and the
commit submitted. The branch is not locked: the reviewer works on it too, and
git zf issue close merges it only when its tip is the commit approved.

On a branch that was already reviewed, approved or not, this starts the next
round: the round counter goes up. It is the way out when commits were made
after an approval.

The review record is pushed (--push / --no-push skip the proposal), and when
the issue came from a tracker a status update is proposed.`,
		Args: cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewRequestInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
	pushflow.AddFlags(cmd)
	return cmd
}

func runReviewRequestInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// A branch closed in a sibling clone is still in progress here until the
	// chains are fetched: do it first so the picker never offers it.
	fetchBranchRefs(ctx, deps)

	branches, err := issueflow.LocalInProgress(ctx, deps.client)
	if err != nil {
		return err //nolint:wrapcheck // already says what was listed
	}

	// Filter out branches whose open review awaits the reviewer's decision. An
	// approved review stays offered: the close may refuse it (commits after the
	// approval, unsigned approval) and a new round is the way out. Sync first so
	// the local ref namespace reflects the current remote state.
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}
	states, _, _ := reviewpkg.List(ctx, deps.client)
	inReview := make(map[string]bool, len(states))
	for _, st := range states {
		if !st.Closed && st.Status == reviewpkg.StatusInReview {
			inReview[st.Slug] = true
		}
	}

	var submittable []branch.Row
	for _, b := range branches {
		if !inReview[b.IssueSlug] {
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

	if err := runReviewRequest(ctx, deps, picked.IssueSlug, picked.BranchName); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

// runReviewRequest submits featureBranch, a branch of issueSlug, for review.
func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug, featureBranch string) error {
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

	featureSHA, err := deps.client.ResolveRef("refs/heads/" + featureBranch)
	if err != nil {
		return fmt.Errorf("resolve feature branch HEAD: %w", err)
	}

	// Write and push the request op. A legacy blob is replaced only once the
	// op is written.
	op := &reviewpkg.Op{Type: reviewpkg.OpRequest, FeatureSHA: featureSHA.String(), Branch: featureBranch}
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

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q is now in review (round %d), on branch %q.\n"+
			"Share with your reviewer: git fetch && git zf review start\n",
		issueSlug, st.Round, featureBranch)

	if err := proposeReviewPush(ctx, deps, featureBranch); err != nil {
		return err
	}

	return nil
}
