package issue

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
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
	stderr  io.Writer
	// client reads the branch chains and the issues stored in the repository.
	client *git.Client
	// mirror is non-nil when the issues are mirrored with the tracker: the
	// list then reads the repository, never the assigned-to-me listing.
	mirror *issuepkg.Mirror
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
	var (
		t   tracker.Tracker
		err error
	)
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
		stderr:  cmd.OutOrStderr(),
		client:  client,
		mirror:  mirrorOf(ir.appConfig, t),
	}

	return runList(ctx, os.Stdout, infra, flags)
}

func runList(ctx context.Context, w io.Writer, infra issueListInfra, flags issueListFlags) error {
	// The TUI filters by status inside the table model, so it needs every row.
	status := flags.status
	if !flags.jsonOut && !flags.stdout {
		status = ""
	}

	rows, err := buildRows(ctx, infra, status)
	if err != nil {
		return fmt.Errorf("build issue rows: %w", err)
	}

	if flags.jsonOut {
		if err := json.NewEncoder(w).Encode(normalizeRows(rows)); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return nil
	}

	if flags.stdout {
		tty.RenderIssueTable(w, rows)

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

// buildRows lists the issue rows matching status, as the TUI's status tab
// does (see issuepkg.Row.MatchesStatus); "" keeps every row.
func buildRows(ctx context.Context, infra issueListInfra, status string) ([]issuepkg.Row, error) {
	rows, err := buildAllRows(ctx, infra)
	if err != nil || status == "" {
		return rows, err
	}

	return slices.DeleteFunc(rows, func(r issuepkg.Row) bool { return !r.MatchesStatus(status) }), nil
}

func buildAllRows(ctx context.Context, infra issueListInfra) ([]issuepkg.Row, error) {
	if infra.tracker != nil && infra.mirror == nil {
		rows, err := buildFromTracker(ctx, infra)
		if err == nil {
			return rows, nil
		}

		fmt.Fprintf(infra.stderr, "warning: tracker unavailable, falling back to the repository: %v\n", err)
	}

	rows, err := buildFromBranches(ctx, infra.client)
	if err != nil {
		return rows, err
	}

	return mergeRepoIssues(ctx, infra, rows)
}

// mergeRepoIssues enriches the branch rows with the issues stored in the
// repository: a row whose issue has a record gets its title, labels and state, and
// every record without a branch row is appended, so the backlog shows up
// before anyone starts a branch.
func mergeRepoIssues(ctx context.Context, infra issueListInfra, rows []issuepkg.Row) ([]issuepkg.Row, error) {
	fetchIssues(ctx, infra.client)
	reconcileIssues(ctx, infra.client, infra.mirror)

	records, warnings, err := issuepkg.List(ctx, infra.client)
	if err != nil {
		return nil, fmt.Errorf("list repo issues: %w", err)
	}
	printWarnings(infra.stderr, warnings)

	// A branch names its issue by the full ID in its start op. A branch
	// started before that op carried one, or seeded by hand, falls back to
	// its slug, which is the display ID the branch was named after.
	byID := make(map[string]*issuepkg.Record, len(records))
	byDisplayID := make(map[string]*issuepkg.Record, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
		byDisplayID[records[i].DisplayID()] = &records[i]
	}

	out := rows
	started := make(map[string]bool, len(out))
	for i := range out {
		rec, ok := byID[out[i].Branch.IssueID]
		if !ok {
			rec, ok = byDisplayID[out[i].IssueSlug]
		}
		if !ok {
			continue
		}
		started[rec.ID] = true
		// The record's title, not the one the branch chain froze at start: it
		// follows `issue edit`.
		out[i].Title = rec.Title
		out[i].Labels, out[i].State, out[i].TrackerStatus = rec.Labels, rec.State, trackerStatusOf(rec)
		out[i].TrackerID = exportedNumber(rec)
	}

	for i := range records {
		rec := &records[i]
		if started[rec.ID] {
			continue
		}
		out = append(out, issuepkg.Row{
			IssueSlug: rec.DisplayID(), Title: rec.Title,
			Labels: rec.Labels, State: rec.State, TrackerStatus: trackerStatusOf(rec),
			TrackerID: exportedNumber(rec),
		})
	}

	return out, nil
}

// trackerStatusOf is the status shown for a repo issue: the tracker's status
// name when the issue is mirrored, its open/closed state otherwise.
func trackerStatusOf(rec *issuepkg.Record) *string {
	s := cmp.Or(rec.TrackerStatus, rec.State)

	return &s
}

// exportedNumber is the tracker number of a repo-born issue that was
// exported, "" otherwise.
func exportedNumber(rec *issuepkg.Record) string {
	if rec.Tracker == nil || rec.Tracker.Born {
		return ""
	}

	return rec.Tracker.ID
}

func buildFromTracker(ctx context.Context, infra issueListInfra) ([]issuepkg.Row, error) {
	issues, err := infra.tracker.ListIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracker issues: %w", err)
	}

	branches, err := branch.ListRows(ctx, infra.client, branch.StatusAll)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	// Rows come newest first: an issue with several branches shows its latest.
	branchMap := make(map[string]branch.Row, len(branches))
	for i := range branches {
		if _, seen := branchMap[branches[i].IssueSlug]; !seen {
			branchMap[branches[i].IssueSlug] = branches[i]
		}
	}

	rows := make([]issuepkg.Row, len(issues))
	for i, iss := range issues {
		status := iss.Status
		row := issuepkg.Row{
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

func buildFromBranches(ctx context.Context, c *git.Client) ([]issuepkg.Row, error) {
	branches, err := branch.ListRows(ctx, c, branch.StatusAll)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	rows := make([]issuepkg.Row, len(branches))
	for i := range branches {
		b := branches[i]
		rows[i] = issuepkg.Row{
			IssueSlug: b.IssueSlug,
			Title:     b.Title,
			Branch:    &b,
		}
	}

	return rows, nil
}

func normalizeRows(rows []issuepkg.Row) []issuepkg.Row {
	out := make([]issuepkg.Row, len(rows))
	for i, r := range rows {
		if r.TrackerStatus == nil {
			na := "N.A."
			r.TrackerStatus = &na
		}
		if r.Labels == nil {
			r.Labels = []string{}
		}
		out[i] = r
	}

	return out
}
