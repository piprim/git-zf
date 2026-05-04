package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
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

func (ir Issue) getIssueListCmd() *cobra.Command {
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
		return ir.issueListRunE(cmd, flags)
	}

	return cmd
}

func (ir Issue) issueListRunE(cmd *cobra.Command, flags issueListFlags) error {
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
	if ir.appConfig.IssueTracker.Type != "" {
		t, err = tracker.New(ir.appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not initialize tracker: %v\n", err)
		}
	}

	infra := issueListInfra{
		tracker: t,
		store:   s,
		stderr:  cmd.OutOrStderr(),
	}

	return runList(cmd.Context(), os.Stdout, infra, flags)
}

func runList(ctx context.Context, w io.Writer, infra issueListInfra, flags issueListFlags) error {
	if flags.jsonOut {
		rows, err := buildRows(ctx, infra, flags.status)
		if err != nil {
			return fmt.Errorf("build issue rows: %w", err)
		}
		if err := json.NewEncoder(w).Encode(normalizeRows(rows)); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := buildRows(ctx, infra, flags.status)
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

	// TUI path: fetch all rows; status filter lives inside the table model.
	rows, err := buildRows(ctx, infra, "")
	if err != nil {
		return fmt.Errorf("build issue rows: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return nil
	}

	m, err := tui.IssueTableModel(rows, flags.status)
	if err != nil {
		return fmt.Errorf("failed to construct issue table: %w", err)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("run table: %w", err)
	}

	return nil
}

func buildRows(ctx context.Context, infra issueListInfra, status string) ([]store.IssueRow, error) {
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
	branches, err := s.ListBranches(ctx, toStoreStatus(status))
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

func toStoreStatus(s string) store.BranchStatus {
	switch s {
	case "open":
		return store.BranchStatusInProgress
	case "closed":
		return store.BranchStatusMerged
	default:
		return store.BranchStatusAll
	}
}

func normalizeRows(rows []store.IssueRow) []store.IssueRow {
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
