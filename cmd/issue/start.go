package issue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (i Issue) getStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
		RunE: i.startRunE,
	}
}

func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
	return i.RunIssueStart(cmd, issue.IssueStartFlags{TrackerFirst: true})
}

// RunIssueStart contains the full issue-start flow. trackerFirst=true for
// `issue start` (tracker pre-selected); false for `branch new` (manual pre-selected).
func (i Issue) RunIssueStart(cmd *cobra.Command, flags issue.IssueStartFlags) error {
	ctx := cmd.Context()
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	allowedBranchTypes := pkg.GetAllowedBranchType(i.appConfig.CommitTypes)
	if len(allowedBranchTypes) == 0 {
		return errors.New("config: no commit types found")
	}

	trackerCfg := i.appConfig.IssueTracker
	var pickedIssue *issue.Issue
	var t tracker.Tracker

	if trackerCfg.Type != "" {
		t, err = tracker.New(trackerCfg)
		if err != nil {
			return fmt.Errorf("failed get tracker: %w", err)
		}

		pickedIssue, err = i.getFromTracker(ctx, t, flags, allowedBranchTypes)
		if err != nil {
			return fmt.Errorf("failed to retreive issue from tracker: %w", err)
		}
	} else {
		pickedIssue, err = issue.GetFromUser(allowedBranchTypes)
		if err != nil {
			return fmt.Errorf("failed to retreive issue from user: %w", err)
		}
	}

	return i.createBranch(cmd, t, pickedIssue, client)
}

func (i Issue) getFromTracker(
	ctx context.Context,
	t tracker.Tracker,
	flags issue.IssueStartFlags,
	allowedBranchTypes []string,
) (*issue.Issue, error) {
	var useTracker bool
	var pickedIssue *issue.Issue
	var err error

	issueTrackerToggle := tui.IssueTrackerToggle(&useTracker, flags.TrackerFirst, i.appConfig.IssueTracker.Type)
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

func (i Issue) createBranch(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client) error {
	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject)
	if err != nil {
		return fmt.Errorf("assemble branch name: %w", err)
	}

	branchName := b.Name()
	base := i.appConfig.Branch.Base
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
		tt = &i.appConfig.IssueTracker.Type
	}

	if err := persist(cmd.Context(), client, b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if pickedIssue.TrackerType != "" {
		i.updateTrackerIssueStatus(cmd, t, pickedIssue.ID)
	}

	return nil
}

func (i Issue) updateTrackerIssueStatus(cmd *cobra.Command, t tracker.Tracker, issueID string) {
	statuses, err := t.ListStatuses(cmd.Context())
	if err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(
		issueID, i.appConfig.IssueTracker.Type, statuses, &selected,
	)).Run(); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: status picker form: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := t.UpdateIssueStatus(cmd.Context(), issueID, selected); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: could not update tracker status: %v\n", err)
	}
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
