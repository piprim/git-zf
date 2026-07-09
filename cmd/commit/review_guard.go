package commit

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// reviewConfirmFunc resolves the "merge reviewer commits now?" decision.
// Production uses newHuhReviewConfirm; tests inject canned answers.
type reviewConfirmFunc func(ctx context.Context, title string) (bool, error)

func newHuhReviewConfirm() reviewConfirmFunc {
	return func(ctx context.Context, title string) (bool, error) {
		confirmed := true
		form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&confirmed)))
		if err := form.RunWithContext(ctx); err != nil {
			return false, fmt.Errorf("review merge confirm: %w", err)
		}
		return confirmed, nil
	}
}

// guardPendingReview blocks the commit flow while reviewer commits on the
// issue's @review branch await incorporation (reviewer decision: approved or
// changes_requested). It offers to merge them inline; declining aborts with
// the sync hint. Detection errors fail open — a guard must never brick
// committing. Callers skip it entirely under --no-verify.
func guardPendingReview(ctx context.Context, client *git.Client, s *store.Store, confirm reviewConfirmFunc) error {
	pending, branchName, err := issueflow.PendingReviewForHEAD(ctx, client, s)
	if err != nil || pending == nil {
		return nil // fail-open
	}

	ok, err := confirm(ctx, fmt.Sprintf("%s has %d reviewer commit(s) not in your branch — merge now?",
		pending.EffectiveRef, pending.Commits))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("commit aborted: incorporate reviewer commits first.\n" +
			"Run 'git zf review sync' to incorporate them first.\n" +
			"To bypass (not recommended): git zf commit --no-verify")
	}

	dirty, err := client.IsDirty(ctx)
	if err != nil {
		return fmt.Errorf("status check: %w", err)
	}
	if dirty {
		return fmt.Errorf("cannot merge %s: working tree has uncommitted changes.\n"+
			"Run 'git stash', then 'git zf review sync', then 'git stash pop', then retry the commit",
			pending.EffectiveRef)
	}

	if err := client.MergeLeaveConflicts(ctx, pending.EffectiveRef, branchName); err != nil {
		if errors.Is(err, git.ErrMergeConflicts) {
			return fmt.Errorf("merge of %s left in progress with conflicts.\n"+
				"Resolve the conflict markers, then run 'git zf commit' to conclude the merge",
				pending.EffectiveRef)
		}
		return err
	}

	fmt.Fprintf(client.IO().Out, "Reviewer commits from %s incorporated into %q.\n",
		pending.EffectiveRef, branchName)
	return nil
}
