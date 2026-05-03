package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tty"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type Issue struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Issue {
	return Issue{appConfig: appConfig}
}

func (i Issue) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE:  i.runE,
	}

	cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd())

	return cmd
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

	// TUI path: status filter form then interactive table.
	statusStr := flags.status
	if err := huh.NewForm(tui.IssueStatusFilter(&statusStr, statusStr)).Run(); err != nil {
		return fmt.Errorf("status filter: %w", err)
	}

	rows, err := buildRows(ctx, infra, statusStr)
	if err != nil {
		return fmt.Errorf("build issue rows: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return nil
	}

	m, err := tui.IssueTableModel(rows)
	if err != nil {
		return fmt.Errorf("failed to construct issue table: %w", err)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("run table: %w", err)
	}

	return nil
}

func (i Issue) runE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return i.startRunE(cmd, args)
	case tui.IssueActionNameList:
		return i.issueListRunE(cmd, issueListFlags{})
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}
