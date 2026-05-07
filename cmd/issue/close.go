package issue

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (i Issue) getCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "Close an issue (merge branch, update store and tracker)",
		Long: `Pick an in-progress branch, merge it into the base branch (squash or classic),
update the local store, update the remote tracker, then optionally delete the local branch.`,
		RunE: i.closeRunE,
	}
}

func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	s, err := pkg.GetStore(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	picked, err := getPickedBranch(ctx, s, client)
	if err != nil {
		return err
	}

	if picked == nil {
		return nil
	}

	base := i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	squash, aborted, err := doMerge(ctx, client, picked, base)
	if err != nil {
		return err
	}

	if aborted {
		fmt.Println("Aborted.")

		return nil
	}

	// Best-effort: warn on stderr but do not abort — the merge already succeeded.
	i.updateStatus(cmd, s, picked)

	if err := doDeleteBranch(cmd, client, picked, squash); err != nil {
		return err
	}

	fmt.Printf("Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}

// getPickedBranch returns (nil, nil) when there are no in-progress branches.
func getPickedBranch(ctx context.Context, s *store.Store, client *git.Client) (*store.BranchRow, error) {
	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Println("No in-progress branches.")

		return nil, nil
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		currentBranch = ""
	}

	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, currentBranch, &picked)).Run(); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}

// doMerge runs the full merge flow: dry-run, strategy picker, author picker,
// confirm, then the actual merge. aborted is true when the user cancelled.
func doMerge(
	ctx context.Context,
	c *git.Client,
	pickedBranch *store.BranchRow,
	baseBranch string,
) (squash, aborted bool, err error) {
	conflicts, err := c.MergeDryRun(ctx, pickedBranch.BranchName, baseBranch)
	if err != nil {
		return false, false, fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Println("Conflicts detected:")
		for _, f := range conflicts {
			fmt.Println("  " + f)
		}
		fmt.Println("Aborting.")

		return false, false, fmt.Errorf("merge conflicts in branch %q", pickedBranch.BranchName)
	}

	if err := huh.NewForm(tui.IssueMergeStrategy(&squash)).Run(); err != nil {
		return false, false, fmt.Errorf("strategy picker: %w", err)
	}

	var author string
	if squash {
		if err := pickSquashAuthor(c, &author); err != nil {
			return false, false, err
		}
	}

	strategy := "no-ff"
	if squash {
		strategy = "squash"
	}

	var confirmed bool
	form := tui.IssueMergeConfirm(pickedBranch.BranchName, baseBranch, strategy, author, &confirmed)
	if err := huh.NewForm(form).Run(); err != nil {
		return false, false, fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		return squash, true, nil
	}

	if squash {
		if err := c.MergeSquash(ctx, pickedBranch.BranchName, baseBranch, author); err != nil {
			return false, false, fmt.Errorf("merge squash: %w", err)
		}
	} else {
		if err := c.MergeNoFF(ctx, pickedBranch.BranchName, baseBranch); err != nil {
			return false, false, fmt.Errorf("merge no-ff: %w", err)
		}
	}

	return squash, false, nil
}

// updateStatus marks the branch and issue as merged/closed in the store and
// optionally updates the remote tracker. Errors are non-fatal — the merge has
// already been committed, so we warn rather than fail.
func (i Issue) updateStatus(cmd *cobra.Command, s *store.Store, pickedBranch *store.BranchRow) {
	now := time.Now()
	if err := s.UpdateBranchStatus(cmd.Context(), pickedBranch.UUID, store.StatusIDMerged, &now); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update branch status: %v\n", err)
	}

	if err := s.UpdateIssueStatus(cmd.Context(), pickedBranch.IssueID, store.StatusIDMerged); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update issue status: %v\n", err)
	}

	if i.appConfig.IssueTracker.Type != "" {
		i.closeTrackerIssue(cmd, pickedBranch.IssueSlug)
	}
}

func doDeleteBranch(cmd *cobra.Command, c *git.Client, pickedBranch *store.BranchRow, squashed bool) error {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(pickedBranch.BranchName, &shouldDelete)).Run(); err != nil {
		return fmt.Errorf("delete branch form: %w", err)
	}

	if shouldDelete {
		if err := c.DeleteLocalBranch(cmd.Context(), pickedBranch.BranchName, squashed); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: delete branch: %v\n", err)
		}
	}

	return nil
}

func pickSquashAuthor(client *git.Client, author *string) error {
	authors, err := client.Authors()
	if err != nil || len(authors) == 0 {
		authors = []string{}
	}

	if len(authors) > 0 {
		*author = authors[0]
	}

	if err := huh.NewForm(tui.IssueMergeAuthor(authors, author)).Run(); err != nil {
		return fmt.Errorf("author picker: %w", err)
	}

	return nil
}

func (i Issue) closeTrackerIssue(cmd *cobra.Command, issueSlug string) {
	t, err := tracker.New(i.appConfig.IssueTracker)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: init tracker: %v\n", err)

		return
	}

	i.updateTrackerIssueStatus(cmd, t, issueSlug)
}
