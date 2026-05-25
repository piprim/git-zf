package issue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/mitchellh/go-homedir"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
		RunE: i.startRunE,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")

	return cmd
}

func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
	variant, err := cmd.Flags().GetString("variant")
	if err != nil {
		return fmt.Errorf("read --variant flag: %w", err)
	}

	return i.RunIssueStart(cmd, issue.IssueStartFlags{
		TrackerFirst: true,
		Variant:      variant,
	})
}

// RunIssueStart contains the full issue-start flow. trackerFirst=true for
// `issue start` (tracker pre-selected); false for `branch new` (manual pre-selected).
func (i Issue) RunIssueStart(cmd *cobra.Command, flags issue.IssueStartFlags) error {
	ctx := cmd.Context()
	client, err := git.NewClient(nil)
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	if i.appConfig.Branch.Remote != "" {
		client.SetRemote(i.appConfig.Branch.Remote)
	}

	allowedBranchTypes := make([]string, 0, len(i.appConfig.CommitTypes))
	for _, t := range i.appConfig.CommitTypes {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}
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
		pickedIssue, err = issue.GetFromUser(ctx, allowedBranchTypes)
		if err != nil {
			return fmt.Errorf("failed to retreive issue from user: %w", err)
		}
	}

	useWorktree := false
	if i.appConfig.Branch.UseWorktree == nil {
		if err := huh.NewForm(tui.WorktreeToggle(&useWorktree)).RunWithContext(ctx); err != nil {
			return fmt.Errorf("worktree toggle: %w", err)
		}
	} else {
		useWorktree = *i.appConfig.Branch.UseWorktree
	}

	if useWorktree {
		return i.createWorktree(cmd, t, pickedIssue, client, flags)
	}

	return i.createBranch(cmd, t, pickedIssue, client, flags)
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
	if err = huh.NewForm(issueTrackerToggle).RunWithContext(ctx); err != nil {
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

// prepareBranch assembles the branch and resolves the base branch.
// Both createBranch and createWorktree share this helper so that the
// base-branch detection logic (config override or DefaultBaseBranch
// fallback) is not duplicated at each call site.
func (i Issue) prepareBranch(
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) (b *branch.Branch, base string, err error) {
	b, err = branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	base = i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return nil, "", fmt.Errorf("detect base branch: %w", err)
		}
	}

	return b, base, nil
}

func (i Issue) createBranch(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}

	ctx := cmd.Context()

	b, err = resolveBranchConflict(ctx, client, b, pickedIssue)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create branch %q based on %q?", branchName, base), &confirmed,
	)).RunWithContext(ctx); err != nil {
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

	if err := persist(cmd.Context(), b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if pickedIssue.TrackerType != "" {
		i.updateTrackerIssueStatus(cmd, t, pickedIssue.ID)
	}

	return nil
}

func (i Issue) createWorktree(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}

	b, err = resolveBranchConflict(cmd.Context(), client, b, pickedIssue)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	repoRoot, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	repoName, err := client.RepoName()
	if err != nil {
		return fmt.Errorf("resolve repo name: %w", err)
	}

	path := worktreePath(repoRoot, i.appConfig.Branch.WorktreeDir, repoName, branchName)

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create worktree %q at %q based on %q?", branchName, path, base), &confirmed,
	)).RunWithContext(cmd.Context()); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

		return nil
	}

	if err := client.CreateWorktree(cmd.Context(), branchName, base, path); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	var tt *string
	if pickedIssue.TrackerType != "" {
		tt = &i.appConfig.IssueTracker.Type
	}

	if err := persist(cmd.Context(), b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: worktree created but store record failed: %v\n", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Created worktree %q at %q (based on %q)\n", branchName, path, base)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	fmt.Fprintln(cmd.OutOrStdout(), hintStyle.Render("Run 'cd "+path+"' to begin working."))

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

	ctx := cmd.Context()

	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(
		issueID, i.appConfig.IssueTracker.Type, statuses, &selected,
	)).RunWithContext(ctx); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: status picker form: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := t.UpdateIssueStatus(ctx, issueID, selected); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: could not update tracker status: %v\n", err)
	}
}

func persist(ctx context.Context, b *branch.Branch, rawTitle string, trackerType *string) error {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: store.StatusIDInProgress, TrackerType: trackerType},
		&store.Branch{Name: b.Name(), Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}

	return nil
}

// worktreePath computes the absolute path for a new worktree.
// baseDir overrides the default (sibling of repoRoot) when non-empty; ~ is expanded.
func worktreePath(repoRoot, baseDir, repoName, branchName string) string {
	base := baseDir
	if base == "" {
		base = filepath.Dir(repoRoot)
	} else if expanded, err := homedir.Expand(base); err == nil {
		base = expanded
	}

	return filepath.Join(base, repoName+"--"+branchName)
}
