package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
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
	// client reads the issues stored in the repository. nil skips them, which
	// leaves the store-only listing.
	client *git.Client
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
	ctx := cmd.Context()
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	var t tracker.Tracker
	if ir.appConfig.IssueTracker.Type != "" {
		t, err = tracker.New(ir.appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not initialize tracker: %v\n", err)
		}
	}

	client, err := cmdutil.NewClientForCmd(cmd, ir.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	infra := issueListInfra{
		tracker: t,
		store:   s,
		stderr:  cmd.OutOrStderr(),
		client:  client,
	}

	return runList(ctx, os.Stdout, infra, flags)
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

	rows, err := buildFromStore(ctx, infra.store, status)
	if err != nil || infra.client == nil {
		return rows, err
	}

	return mergeRepoIssues(ctx, infra, rows, status)
}

// mergeRepoIssues enriches the store rows with the issues stored in the
// repository: a row whose issue has a record gets its labels and state, and
// every record without a branch row is appended, so the backlog shows up
// before anyone starts a branch. status filters the appended rows on the
// issue state ("open" / "closed"; anything else keeps all).
func mergeRepoIssues(
	ctx context.Context, infra issueListInfra, rows []store.IssueRow, status string,
) ([]store.IssueRow, error) {
	fetchIssues(ctx, infra.client)

	records, warnings, err := issuepkg.List(ctx, infra.client)
	if err != nil {
		return nil, fmt.Errorf("list repo issues: %w", err)
	}
	printWarnings(infra.stderr, warnings)

	byDisplayID := make(map[string]*issuepkg.Record, len(records))
	for i := range records {
		byDisplayID[records[i].DisplayID()] = &records[i]
	}

	out := rows
	started := make(map[string]bool, len(out))
	for i := range out {
		rec, ok := byDisplayID[out[i].IssueSlug]
		if !ok {
			continue
		}
		started[rec.ID] = true
		out[i].Labels, out[i].State, out[i].TrackerStatus = rec.Labels, rec.State, &rec.State
	}

	for i := range records {
		rec := &records[i]
		if started[rec.ID] {
			continue
		}
		if (status == issuepkg.StateOpen || status == issuepkg.StateClosed) && rec.State != status {
			continue
		}
		out = append(out, store.IssueRow{
			IssueSlug: rec.DisplayID(), Title: rec.Title,
			Labels: rec.Labels, State: rec.State, TrackerStatus: &rec.State,
		})
	}

	return out, nil
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
			Project:       iss.Project,
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
