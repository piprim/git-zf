package issue

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/git"
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

	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Println("No in-progress branches.")

		return nil
	}

	currentBranch, err := client.CurrentBranch(ctx)
	if err != nil {
		currentBranch = ""
	}

	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, currentBranch, &picked)).Run(); err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}

	base := i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	conflicts, err := client.MergeDryRun(ctx, picked.BranchName, base)
	if err != nil {
		return fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Println("Conflicts detected:")
		for _, f := range conflicts {
			fmt.Println("  " + f)
		}
		fmt.Println("Aborting.")

		return fmt.Errorf("merge conflicts in branch %q", picked.BranchName)
	}

	var squash bool
	if err := huh.NewForm(tui.IssueMergeStrategy(&squash)).Run(); err != nil {
		return fmt.Errorf("strategy picker: %w", err)
	}

	var author string
	if squash {
		if err := i.pickSquashAuthor(client, &author); err != nil {
			return err
		}
	}

	strategy := "no-ff"
	if squash {
		strategy = "squash"
	}

	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(picked.BranchName, base, strategy, author, &confirmed)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if squash {
		if err := client.MergeSquash(ctx, picked.BranchName, base, author); err != nil {
			return fmt.Errorf("merge squash: %w", err)
		}
	} else {
		if err := client.MergeNoFF(ctx, picked.BranchName, base); err != nil {
			return fmt.Errorf("merge no-ff: %w", err)
		}
	}

	now := time.Now()
	if err := s.UpdateBranchStatus(ctx, picked.UUID, 2, &now); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update branch status: %v\n", err)
	}

	if err := s.UpdateIssueStatus(ctx, picked.IssueID, 2); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update issue status: %v\n", err)
	}

	if i.appConfig.IssueTracker.Type != "" {
		i.closeTrackerIssue(cmd, picked.IssueSlug)
	}

	var deleteBranch bool
	if err := huh.NewForm(tui.IssueDeleteBranch(picked.BranchName, &deleteBranch)).Run(); err != nil {
		return fmt.Errorf("delete branch form: %w", err)
	}

	if deleteBranch {
		if err := client.DeleteLocalBranch(ctx, picked.BranchName, squash); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: delete branch: %v\n", err)
		}
	}

	fmt.Printf("Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}

func (i Issue) pickSquashAuthor(client *git.Client, author *string) error {
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
