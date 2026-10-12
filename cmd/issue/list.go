package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tty"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
	// No terminal on stdin (a script, a test): the table is printed, the TUI
	// cannot open /dev/tty.
	flags.stdout = flags.stdout || !term.IsTerminal(int(os.Stdin.Fd()))
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
		mirror:  issueflow.MirrorOf(ir.appConfig, t),
	}

	for {
		pick, row, err := runList(ctx, cmd.OutOrStdout(), infra, flags)
		if err != nil {
			return err
		}
		reopen, err := ir.listAction(ctx, infra, huhRecordPrompter{}, issueflow.NewHuhStartPrompter(), pick, row)
		if !reopen {
			return err
		}
		if err != nil {
			fmt.Fprintln(infra.stderr, "error:", err)
		}
	}
}

// listAction runs the command behind a key of the table once its program
// has exited and the forms get the terminal. It reports whether the table
// reopens: a start leaves the operator on the new branch, and a form left
// with esc is no error.
func (ir Issue) listAction(
	ctx context.Context, infra issueListInfra, p recordPrompter, sp issueflow.StartPrompter,
	pick tui.IssuePick, row *issuepkg.Row,
) (reopen bool, err error) {
	switch pick {
	case tui.IssuePickRow:
		deps := infra.deps()
		deps.Cfg = ir.appConfig
		if err = issueflow.StartRow(ctx, deps, sp, row); !errors.Is(err, huh.ErrUserAborted) {
			return false, err
		}
	case tui.IssuePickNew:
		err = runNew(ctx, infra.client, ir.appConfig, issuepkg.NewIssue{}, p, infra.mirror)
	case tui.IssuePickClose:
		err = runCloseByID(ctx, infra.client, row.IssueSlug, infra.mirror)
	case tui.IssuePickLabel:
		err = runLabel(ctx, infra.client, p, []string{row.IssueSlug}, infra.mirror)
	case tui.IssuePickSync:
		err = runSync(ctx, infra.client, infra.mirror)
	case tui.IssuePickNone:
		return false, nil
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return true, nil
	}

	return true, err
}

// runList prints or shows the issues. It returns how the table was quit
// and, for a key that acts on a row, the row under the cursor; the stdout
// and JSON outputs never pick.
func runList(
	ctx context.Context, w io.Writer, infra issueListInfra, flags issueListFlags,
) (tui.IssuePick, *issuepkg.Row, error) {
	// The TUI filters by status inside the table model, so it needs every row.
	status := flags.status
	if !flags.jsonOut && !flags.stdout {
		status = ""
	}

	rows, err := buildRows(ctx, infra, status)
	if err != nil {
		return tui.IssuePickNone, nil, fmt.Errorf("build issue rows: %w", err)
	}

	if flags.jsonOut {
		if err := json.NewEncoder(w).Encode(normalizeRows(rows)); err != nil {
			return tui.IssuePickNone, nil, fmt.Errorf("encode json: %w", err)
		}

		return tui.IssuePickNone, nil, nil
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return tui.IssuePickNone, nil, nil
	}

	if flags.stdout {
		tty.RenderIssueTable(w, rows)

		return tui.IssuePickNone, nil, nil
	}

	actions := tui.IssueActions{
		Show: func(slug string) (string, error) { return renderIssue(ctx, infra, slug) },
		Load: func(slug string) (string, string, error) {
			rec, err := resolveRow(ctx, infra.client, slug)

			return rec.Title, rec.Description, err
		},
		Edit: func(slug, title, description string) (string, error) {
			return editIssue(ctx, infra, slug, title, description)
		},
		Start: true, // s starts the issue under the cursor
	}
	m, err := tui.IssueTableModel(rows, flags.status, actions)
	if err != nil {
		return tui.IssuePickNone, nil, fmt.Errorf("failed to construct issue table: %w", err)
	}
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return tui.IssuePickNone, nil, fmt.Errorf("run table: %w", err)
	}
	pick, slug := tui.PickedIssue(final)
	if slug == "" {
		return pick, nil, nil
	}
	i := slices.IndexFunc(rows, func(r issuepkg.Row) bool { return r.IssueSlug == slug })
	if i < 0 {
		return tui.IssuePickNone, nil, nil
	}

	return pick, &rows[i], nil
}

// resolveRow loads the issue of a table row; a row that is not an issue
// stored in the repository is an error.
func resolveRow(ctx context.Context, client *git.Client, slug string) (issuepkg.Record, error) {
	rec, err := issuepkg.Resolve(ctx, client, slug)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("resolve issue %s: %w", slug, err)
	}

	return rec, nil
}

// renderIssue renders the issue of a table row as `issue show` does, for the
// list's dialog. A row with no issue in the repository is a tracker issue:
// it is read from the tracker, with its comments.
func renderIssue(ctx context.Context, infra issueListInfra, slug string) (string, error) {
	var b strings.Builder

	rec, err := resolveRow(ctx, infra.client, slug)
	if err == nil {
		renderRecord(&b, &rec)

		return b.String(), nil
	}
	if !errors.Is(err, git.ErrIssueNotFound) || infra.tracker == nil {
		return "", err
	}

	iss, err := infra.tracker.GetIssue(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("tracker issue %s: %w", slug, err)
	}
	comments, err := infra.tracker.ListComments(ctx, slug)
	if err != nil {
		return "", fmt.Errorf("tracker issue %s comments: %w", slug, err)
	}
	renderTrackerIssue(&b, &iss, comments)

	return b.String(), nil
}

// renderTrackerIssue prints a tracker issue the way renderRecord prints a
// record.
func renderTrackerIssue(w io.Writer, iss *tracker.Issue, comments []tracker.Comment) {
	fmt.Fprintf(w, "%s  %s\n", iss.ID, iss.Subject)
	fmt.Fprintf(w, "Status: %s   Project: %s   Created: %s\n",
		iss.Status, iss.Project, iss.CreatedAt.Local().Format(showTimeLayout))
	if len(iss.Labels) > 0 {
		fmt.Fprintf(w, "Labels: %s\n", strings.Join(iss.Labels, ", "))
	}
	fmt.Fprintf(w, "Tracker: %s %s #%s (not stored in the repository)\n", iss.TrackerType, iss.Project, iss.ID)

	if iss.Description != "" {
		fmt.Fprintf(w, "\n%s\n", iss.Description)
	}

	for _, c := range comments {
		fmt.Fprintf(w, "\n--- %s  %s\n%s\n", c.CreatedAt.Local().Format(showTimeLayout), c.Author, c.Body)
	}
}

// editIssue saves the edit dialog of a table row as `issue edit` does and
// returns the line the table shows.
func editIssue(ctx context.Context, infra issueListInfra, slug, title, description string) (string, error) {
	rec, err := resolveRow(ctx, infra.client, slug)
	if err != nil {
		return "", err
	}

	changed, err := applyEdit(ctx, infra.client, &rec, title, description, infra.mirror)
	if err != nil {
		return "", err
	}
	if !changed {
		return fmt.Sprintf("Issue %s unchanged", rec.DisplayID()), nil
	}

	return fmt.Sprintf("Edited issue %s: %s", rec.DisplayID(), strings.TrimSpace(title)), nil
}

// deps is the start flow's view of the list's infrastructure: the rows come
// from issueflow.ListRows, with the tracker listed.
func (infra issueListInfra) deps() issueflow.StartDeps {
	return issueflow.StartDeps{
		Client: infra.client, Tracker: infra.tracker, Mirror: infra.mirror, Stderr: infra.stderr,
		Flags: issuepkg.IssueStartFlags{Tracker: true},
	}
}

// buildRows lists the issue rows matching status, as the TUI's status tab
// does; "" keeps every row.
func buildRows(ctx context.Context, infra issueListInfra, status string) ([]issuepkg.Row, error) {
	return issueflow.ListRows(ctx, infra.deps(), status)
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
