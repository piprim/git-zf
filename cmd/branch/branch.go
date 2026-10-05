package branch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tty"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type Branch struct {
	appConfig *config.AppConfig
}

func New(appConfig *config.AppConfig) Branch {
	return Branch{appConfig: appConfig}
}

type listFlags struct {
	status  string
	stdout  bool
	jsonOut bool
}

func (b Branch) GetRootCmd() *cobra.Command {
	// The registration order is also the menu order.
	subs := []*cobra.Command{b.listCmd(), b.newCmd(), b.pruneCmd(), b.pruneTrackerCmd(), b.mergeCmd()}

	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Manage local branches",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmdutil.RunMenu(cmd, "Branch action:", subs, cmdutil.NewHuhMenuPrompter())
		},
	}
	cmd.AddCommand(subs...)
	// Not in the menu: it needs a branch name.
	cmd.AddCommand(b.closeCmd())

	return cmd
}

func (b Branch) listCmd() *cobra.Command {
	var flags listFlags

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List branches",
	}

	f := cmd.Flags()
	f.StringVar(&flags.status, "status", "", "filter by status: in_progress, merged, closed, all")
	f.BoolVar(&flags.stdout, "stdout", false, "print table to stdout without TUI")
	f.BoolVar(&flags.jsonOut, "json", false, "print JSON array to stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return b.listRunE(cmd, flags)
	}

	return cmd
}

func (b Branch) listRunE(cmd *cobra.Command, flags listFlags) error {
	c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
	if err != nil {
		return err //nolint:wrapcheck // already names the cause
	}

	return runList(cmd.Context(), os.Stdout, c, flags)
}

// runList executes the branch list logic. w receives stdout/non-TUI output.
// When neither --json nor --stdout is set, runList runs the interactive TUI.
func runList(ctx context.Context, w io.Writer, c *git.Client, flags listFlags) error {
	queryStatus := toBranchStatus(flags.status)

	if flags.jsonOut {
		rows, err := branch.ListRows(ctx, c, queryStatus)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := branch.ListRows(ctx, c, queryStatus)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if len(rows) == 0 {
			fmt.Fprintln(w, "No branches found.")

			return nil
		}

		tty.RenderBranchTable(w, rows)

		return nil
	}

	// TUI path: status filter then interactive table.
	statusStr := flags.status
	if err := huh.NewForm(tui.BranchStatusFilter(&statusStr, statusStr)).RunWithContext(ctx); err != nil {
		return fmt.Errorf("status filter: %w", err)
	}

	queryStatus = toBranchStatus(statusStr)

	rows, err := branch.ListRows(ctx, c, queryStatus)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No branches found.")

		return nil
	}

	m, err := tui.BranchTableModel(rows)
	if err != nil {
		return fmt.Errorf("failed to construct branch table: %w", err)
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return fmt.Errorf("failed to run table: %w", err)
	}

	return nil
}

func toBranchStatus(s string) string {
	switch s {
	case "in_progress":
		return branch.StatusInProgress
	case "merged":
		return branch.StatusMerged
	case "closed":
		return branch.StatusClosed
	default:
		return branch.StatusAll
	}
}

func (b Branch) newCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new branch (manual input)",
		Long:  "Enter issue details manually, then a named branch is created and checked out.",
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		// StringFlag, not GetString: the branch menu runs this with the root
		// command, which defines no --variant flag.
		return b.newRunE(cmd, cmdutil.StringFlag(cmd, "variant"))
	}

	return cmd
}

// newRunE delegates to RunIssueStart with manual-first (tracker toggle defaults to NO).
// variant carries the --variant flag value, "" when run from the branch menu.
func (b Branch) newRunE(cmd *cobra.Command, variant string) error {
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: variant}
	deps, err := issueflow.BuildStartDeps(cmd, b.appConfig, flags)
	if err != nil {
		return fmt.Errorf("build start deps: %w", err)
	}

	if err := issueflow.RunIssueStart(cmd.Context(), deps, issueflow.NewHuhStartPrompter()); err != nil {
		return fmt.Errorf("run issue start: %w", err)
	}

	return nil
}

type pruneFlags struct {
	dryRun bool
	base   string
	yes    bool // when true, skip the confirmation prompt (CI-friendly)
	others bool // when true, also close vanished branches someone else started
}

// pruneResult holds branches categorised by prune action.
type pruneResult struct {
	toClose []branch.Row // gone locally and on the remote; someone else's only with --others
	toMerge []branch.Row // tip reachable from base — mark merged
	skipped []branch.Row // gone locally and on the remote, started by someone else
	// remoteErr is why the remote's branches could not be listed. When set,
	// nothing is closed: "gone on the remote" cannot be established.
	remoteErr error
}

func (b Branch) pruneCmd() *cobra.Command {
	var flags pruneFlags

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Record branches merged or deleted outside " + b.appConfig.ProgName,
		Long: `Compare each in-progress branch against the local and remote branches:
a local branch already merged into the base is marked merged; a branch gone
locally and on the remote is closed, when you started it. A branch that still
exists on the remote, or that someone else started, is left alone.

A branch someone else started and that exists nowhere may be work they have
not pushed yet. When you know it is abandoned (its author left, or you changed
your git name or email and it is yours), --others closes those too.
'git zf issue track' on a closed branch reopens it.

Use --dry-run to preview, --yes to skip the confirm prompt.`,
	}

	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "show what would be pruned without executing")
	cmd.Flags().StringVar(&flags.base, "base", "", "base branch for merge detection (default: auto-detected)")
	cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "skip the confirmation prompt (CI-friendly)")
	cmd.Flags().BoolVar(&flags.others, "others", false,
		"also close branches gone everywhere that someone else started")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return b.pruneRunE(cmd, flags)
	}

	return cmd
}

func (b Branch) pruneRunE(cmd *cobra.Command, flags pruneFlags) error {
	ctx := cmd.Context()
	c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
	if err != nil {
		return err
	}

	var prompter PrunePrompter = &huhPrunePrompter{}
	if flags.yes {
		prompter = &autoConfirmPrunePrompter{}
	}

	return runPrune(ctx, os.Stdout, c, prompter, flags)
}

// runPrune executes the prune logic. w receives non-TUI output.
// When flags.dryRun is true it prints the summary and returns without writing
// anything. Otherwise it delegates the confirmation to prompter (huh-driven in
// production, auto-confirm under --yes, scripted in tests) and then calls
// executePrune.
func runPrune(ctx context.Context, w io.Writer, client *git.Client, prompter PrunePrompter, flags pruneFlags) error {
	base := flags.base
	if base == "" {
		var err error
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	// What other clones started, merged or closed is on the remote's chains.
	if err := branch.Fetch(ctx, client); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: fetch branch refs: %v\n", err)
	}

	rows, err := branch.ListRows(ctx, client, branch.StatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	result, err := classifyPrune(ctx, client, rows, base, flags.others)
	if err != nil {
		return err
	}

	if result.remoteErr != nil {
		fmt.Fprintf(client.IO().Err,
			"warning: cannot list the remote's branches, no branch will be closed: %v\n", result.remoteErr)
	}

	if len(result.toClose) == 0 && len(result.toMerge) == 0 {
		renderPruneSkipped(w, result)
		fmt.Fprintln(w, "Nothing to prune.")

		return nil
	}

	renderPruneSummary(w, result)

	if flags.dryRun {
		return nil
	}

	confirmed, err := prompter.ConfirmPrune(ctx, len(result.toClose), len(result.toMerge))
	if err != nil {
		return fmt.Errorf("confirm prune: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(w, "Aborted.")

		return nil
	}

	return executePrune(ctx, w, client, result)
}

// classifyPrune sorts the in-progress rows by prune action.
//
// A branch present locally is marked merged when its tip is reachable from
// base. A branch gone locally is closed only when it is gone on the remote
// too and this clone's user started it: a branch someone else started may be
// work that was never pushed (issue start pushes the record, not the branch),
// or one its author deleted and will close. others lifts that guard, for
// branches known to be abandoned.
//
// ponytail: merge detection looks at local branches only. A branch that exists
// only on the remote is left to the clones that have it: a branch just pushed
// with no commit of its own has the base's tip and would read as merged.
func classifyPrune(
	ctx context.Context, client *git.Client, rows []branch.Row, base string, others bool,
) (pruneResult, error) {
	var result pruneResult

	localNames, err := client.LocalBranchNames()
	if err != nil {
		return result, fmt.Errorf("list local branches: %w", err)
	}

	var remote map[string]bool
	remote, result.remoteErr = client.LsRemoteBranches(ctx)

	me, _ := client.ConfigUser(ctx)

	for i := range rows {
		if slices.Contains(localNames, rows[i].BranchName) {
			merged, mergeErr := client.IsMergedInto(rows[i].BranchName, base)
			if mergeErr != nil {
				slog.Warn("merge check failed", "branch", rows[i].BranchName, "error", mergeErr)

				continue
			}
			if merged {
				result.toMerge = append(result.toMerge, rows[i])
			}

			continue
		}

		switch {
		case result.remoteErr != nil || remote[rows[i].BranchName]:
			// Unknown, or still someone's work in progress.
		case rows[i].Author != me && !others:
			result.skipped = append(result.skipped, rows[i])
		default:
			result.toClose = append(result.toClose, rows[i])
		}
	}

	return result, nil
}

// renderPruneSkipped lists the branches prune leaves to whoever started them.
func renderPruneSkipped(w io.Writer, result pruneResult) {
	if len(result.skipped) == 0 {
		return
	}

	fmt.Fprintln(w, "Skipped (gone locally and on the remote, started by someone else):")
	for i := range result.skipped {
		fmt.Fprintf(w, "  ? %s (%s)\n", result.skipped[i].BranchName, result.skipped[i].Author)
	}
	fmt.Fprintln(w, "If they are abandoned, close them with: git zf branch prune --others")
}

func renderPruneSummary(w io.Writer, result pruneResult) {
	if len(result.toClose) > 0 {
		fmt.Fprintln(w, "Will close (gone locally and on the remote):")
		for i := range result.toClose {
			fmt.Fprintf(w, "  - %s (started by %s)\n", result.toClose[i].BranchName, result.toClose[i].Author)
		}
	}

	if len(result.toMerge) > 0 {
		fmt.Fprintln(w, "Will mark merged (tip reachable from base):")
		for i := range result.toMerge {
			fmt.Fprintf(w, "  ~ %s\n", result.toMerge[i].BranchName)
		}
	}

	renderPruneSkipped(w, result)
}

// executePrune records the statuses gathered into result on the branch chains
// and pushes them. The caller is responsible for obtaining confirmation
// beforehand. w receives the final "Pruned: N closed, M marked merged." line.
func executePrune(ctx context.Context, w io.Writer, client *git.Client, result pruneResult) error {
	var slugs []string

	set := func(rows []branch.Row, status string) error {
		for i := range rows {
			if err := branch.SetStatus(ctx, client, rows[i].IssueSlug, rows[i].BranchName, status); err != nil {
				return fmt.Errorf("mark %q %s: %w", rows[i].BranchName, status, err)
			}
			if !slices.Contains(slugs, rows[i].IssueSlug) {
				slugs = append(slugs, rows[i].IssueSlug)
			}
		}

		return nil
	}

	if err := set(result.toClose, branch.StatusClosed); err != nil {
		return err
	}
	if err := set(result.toMerge, branch.StatusMerged); err != nil {
		return err
	}

	for _, slug := range slugs {
		if err := branch.Push(ctx, client, slug); err != nil {
			fmt.Fprintf(client.IO().Err, "warning: push branch ref: %v\n", err)
		}
	}

	fmt.Fprintf(w, "Pruned: %d closed, %d marked merged.\n", len(result.toClose), len(result.toMerge))

	return nil
}
