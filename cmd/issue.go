package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tty"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type issueListFlags struct {
	status  string
	stdout  bool
	jsonOut bool
}

type issueListInfra struct {
	tracker tracker.Tracker
	store   *store.Store
	stderr  io.Writer
}

func buildIssueRows(ctx context.Context, infra issueListInfra, status string) ([]store.IssueRow, error) {
	if infra.tracker != nil {
		rows, err := buildFromTracker(ctx, infra)
		if err == nil {
			return rows, nil
		}

		fmt.Fprintf(infra.stderr, "warning: tracker unavailable, falling back to local store: %v\n", err)
	}

	return buildFromStore(ctx, infra.store, status)
}

func buildFromTracker(ctx context.Context, infra issueListInfra) ([]store.IssueRow, error) {
	issues, err := infra.tracker.ListIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracker issues: %w", err)
	}

	slugs := make([]string, len(issues))
	for i, iss := range issues {
		slugs[i] = iss.ID
	}

	branchMap, err := infra.store.ListBranchesByIssueSlugs(ctx, slugs)
	if err != nil {
		return nil, fmt.Errorf("list branches by slugs: %w", err)
	}

	rows := make([]store.IssueRow, len(issues))
	for i, iss := range issues {
		status := iss.Status
		row := store.IssueRow{
			IssueSlug:     iss.ID,
			Title:         iss.Subject,
			TrackerStatus: &status,
		}
		if b, ok := branchMap[iss.ID]; ok {
			row.Branch = &b
		}
		rows[i] = row
	}

	return rows, nil
}

func buildFromStore(ctx context.Context, s *store.Store, status string) ([]store.IssueRow, error) {
	branches, err := s.ListBranches(ctx, toIssueStoreStatus(status))
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	rows := make([]store.IssueRow, len(branches))
	for i := range branches {
		b := branches[i]
		rows[i] = store.IssueRow{
			IssueSlug: b.IssueSlug,
			Title:     b.Title,
			Branch:    &b,
		}
	}

	return rows, nil
}

func toIssueStoreStatus(s string) store.BranchStatus {
	switch s {
	case "open":
		return store.BranchStatusInProgress
	case "closed":
		return store.BranchStatusMerged
	default:
		return store.BranchStatusAll
	}
}

func normalizeIssueRows(rows []store.IssueRow) []store.IssueRow {
	out := make([]store.IssueRow, len(rows))
	for i, r := range rows {
		if r.TrackerStatus == nil {
			na := "N.A."
			r.TrackerStatus = &na
		}
		out[i] = r
	}

	return out
}

func runIssueList(ctx context.Context, w io.Writer, infra issueListInfra, flags issueListFlags) error {
	if flags.jsonOut {
		rows, err := buildIssueRows(ctx, infra, flags.status)
		if err != nil {
			return fmt.Errorf("build issue rows: %w", err)
		}
		if err := json.NewEncoder(w).Encode(normalizeIssueRows(rows)); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := buildIssueRows(ctx, infra, flags.status)
		if err != nil {
			return fmt.Errorf("build issue rows: %w", err)
		}
		if len(rows) == 0 {
			fmt.Fprintln(w, "No issues found.")

			return nil
		}

		tty.RenderIssueTable(w, rows)

		return nil
	}

	// TUI path: status filter form then interactive table.
	statusStr := flags.status
	if err := huh.NewForm(tui.IssueStatusFilter(&statusStr, statusStr)).Run(); err != nil {
		return fmt.Errorf("status filter: %w", err)
	}

	rows, err := buildIssueRows(ctx, infra, statusStr)
	if err != nil {
		return fmt.Errorf("build issue rows: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return nil
	}

	m, err := tui.IssueTableModel(rows)
	if err == nil {
		return fmt.Errorf("failed to construct issue table: %w", err)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("run table: %w", err)
	}

	return nil
}

func getIssueListCmd() *cobra.Command {
	var flags issueListFlags

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues",
	}

	f := cmd.Flags()
	f.StringVar(&flags.status, "status", "", "filter by status: open, closed, all")
	f.BoolVar(&flags.stdout, "stdout", false, "print table to stdout without TUI")
	f.BoolVar(&flags.jsonOut, "json", false, "print JSON array to stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return issueListRunE(cmd, flags)
	}

	return cmd
}

func issueListRunE(cmd *cobra.Command, flags issueListFlags) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(cmd.Context(), filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	var t tracker.Tracker
	if appConfig.IssueTracker.Type != "" {
		t, err = tracker.New(appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not initialize tracker: %v\n", err)
		}
	}

	infra := issueListInfra{
		tracker: t,
		store:   s,
		stderr:  cmd.OutOrStderr(),
	}

	return runIssueList(cmd.Context(), os.Stdout, infra, flags)
}

func getIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE:  issueRunE,
	}
	cmd.AddCommand(getIssueStartCmd(), getIssueListCmd())

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
	case tui.IssueActionNameList:
		return issueListRunE(cmd, issueListFlags{})
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
	statuses, err := t.ListStatuses(cmd.Context())
	if err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(
		issueID, appConfig.IssueTracker.Type, statuses, &selected,
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
