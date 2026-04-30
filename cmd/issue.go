package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func getIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE:  issueRunE,
	}
	cmd.AddCommand(getIssueStartCmd())

	return cmd
}

func issueRunE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return issueStartRunE(cmd, args)
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}

func getIssueStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
		RunE: issueStartRunE,
	}
}

func issueStartRunE(cmd *cobra.Command, _ []string) error {
	return runIssueStart(cmd, issue.IssueStartFlags{TrackerFirst: true})
}

// runIssueStart contains the full issue-start flow. trackerFirst=true for
// `issue start` (tracker pre-selected); false for `branch new` (manual pre-selected).
func runIssueStart(cmd *cobra.Command, flags issue.IssueStartFlags) error {
	ctx := cmd.Context()
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	allowedBranchTypes := getAllowedBranchType(appConfig.CommitTypes)
	if len(allowedBranchTypes) == 0 {
		return errors.New("config: no commit types found")
	}

	trackerCfg := appConfig.IssueTracker
	var pickedIssue *issue.Issue
	var t tracker.Tracker

	if trackerCfg.Type != "" {
		t, err = tracker.New(trackerCfg)
		if err != nil {
			return fmt.Errorf("failed get tracker: %w", err)
		}

		pickedIssue, err = getFromTracker(ctx, t, flags, allowedBranchTypes)
		if err != nil {
			return fmt.Errorf("failed to retreive issue from tracker: %w", err)
		}
	} else {
		pickedIssue, err = issue.GetFromUser(allowedBranchTypes)
		if err != nil {
			return fmt.Errorf("failed to retreive issue from user: %w", err)
		}
	}

	return createBranch(cmd, t, pickedIssue, client)
}

func createBranch(cmd *cobra.Command, t tracker.Tracker, pickedIssue *issue.Issue, client *git.Client) error {
	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject)
	if err != nil {
		return fmt.Errorf("assemble branch name: %w", err)
	}

	branchName := b.Name()
	base := appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create branch %q based on %q?", branchName, base), &confirmed,
	)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if err := client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	var tt *string
	if pickedIssue.TrackerType != "" {
		tt = &appConfig.IssueTracker.Type
	}

	if err := persist(cmd.Context(), client, b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if pickedIssue.TrackerType != "" {
		updateTrackerIssueStatus(cmd, t, pickedIssue.ID)
	}

	return nil
}

func updateTrackerIssueStatus(cmd *cobra.Command, t tracker.Tracker, issueID string) {
	var updateStatus bool
	if err := huh.NewForm(tui.IssueUpdateStatusConfirm(
		issueID, appConfig.IssueTracker.InProgressStatus, appConfig.IssueTracker.Type, &updateStatus,
	)).Run(); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: status confirm form: %v\n", err)
	} else if updateStatus {
		if err := t.UpdateIssueStatus(cmd.Context(), issueID, appConfig.IssueTracker.InProgressStatus); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not update tracker status: %v\n", err)
		}
	}
}

func getFromTracker(
	ctx context.Context,
	t tracker.Tracker,
	flags issue.IssueStartFlags,
	allowedBranchTypes []string,
) (*issue.Issue, error) {
	var useTracker bool
	var pickedIssue *issue.Issue
	var err error

	issueTrackerToggle := tui.IssueTrackerToggle(&useTracker, flags.TrackerFirst, appConfig.IssueTracker.Type)
	if err = huh.NewForm(issueTrackerToggle).Run(); err != nil {
		return nil, fmt.Errorf("tracker toggle error: %w", err)
	}

	if useTracker {
		pickedIssue, err = issue.GetFromTracker(ctx, t, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("failed to retreive issue from tracker: %w", err)
		}
	}

	return pickedIssue, nil
}

func getAllowedBranchType(types []config.CommitTypeOption) []string {
	allowedBranchTypes := make([]string, 0, len(types))
	for _, t := range types {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	return allowedBranchTypes
}

func persist(ctx context.Context, client *git.Client, b *branch.Branch, rawTitle string, trackerType *string) error {
	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: 1, TrackerType: trackerType},
		&store.Branch{UUID: b.ID(), Name: b.Name(), Type: b.Type(), StatusID: 1},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}

	return nil
}
