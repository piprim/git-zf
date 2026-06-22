package branch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
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
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Manage local branches",
		RunE:  b.runE,
	}
	cmd.AddCommand(listCmd(), b.newCmd(), b.pruneCmd(), b.pruneTrackerCmd(), mergeCmd())

	return cmd
}

func (b Branch) runE(cmd *cobra.Command, _ []string) error {
	var action string
	if err := huh.NewForm(tui.BranchActionSelect(&action)).RunWithContext(cmd.Context()); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.BranchActionNameList:
		// zero flags → TUI path (status filter presented interactively)
		return listRunE(cmd, listFlags{})
	case tui.BranchActionNameNew:
		// Interactive path: no --variant flag exists on the branch root
		// command, so pass an empty variant (variants are chosen via the
		// branch-conflict prompter, not a CLI flag).
		return b.newRunE(cmd, "")
	case tui.BranchActionNamePrune:
		return b.pruneRunE(cmd, pruneFlags{})
	case tui.BranchActionNamePruneTracker:
		return b.pruneTrackerRunE(cmd, pruneTrackerFlags{})
	default:
		fmt.Fprintln(cmd.OutOrStdout(), "Not yet implemented.")

		return nil
	}
}

func listCmd() *cobra.Command {
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
		return listRunE(cmd, flags)
	}

	return cmd
}

func listRunE(cmd *cobra.Command, flags listFlags) error {
	ctx := cmd.Context()
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	return runList(ctx, os.Stdout, s, flags)
}

// runList executes the branch list logic. w receives stdout/non-TUI output.
// When neither --json nor --stdout is set, runList runs the interactive TUI.
func runList(ctx context.Context, w io.Writer, s *store.Store, flags listFlags) error {
	queryStatus := toStoreStatus(flags.status)

	if flags.jsonOut {
		rows, err := s.ListBranches(ctx, queryStatus)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := s.ListBranches(ctx, queryStatus)
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

	queryStatus = toStoreStatus(statusStr)

	rows, err := s.ListBranches(ctx, queryStatus)
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

func toStoreStatus(s string) store.BranchStatus {
	switch s {
	case "in_progress":
		return store.BranchStatusInProgress
	case "merged":
		return store.BranchStatusMerged
	case "closed":
		return store.BranchStatusClosed
	default:
		return store.BranchStatusAll
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
		variant, err := cmd.Flags().GetString("variant")
		if err != nil {
			return fmt.Errorf("read --variant flag: %w", err)
		}

		return b.newRunE(cmd, variant)
	}

	return cmd
}

// newRunE delegates to RunIssueStart with manual-first (tracker toggle defaults to NO).
// variant carries the --variant flag value; the interactive dispatcher (runE)
// passes "" because the branch root command defines no such flag.
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
}

// pruneResult holds branches categorised by prune action.
type pruneResult struct {
	toDelete []store.BranchRow // local ref gone — remove DB record
	toMerge  []store.BranchRow // tip reachable from base — mark merged
}

func (b Branch) pruneCmd() *cobra.Command {
	var flags pruneFlags

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove DB records for branches deleted or merged outside " + b.appConfig.ProgName,
		Long: `Compare each in-progress branch in the store against the local refs and
remove store rows whose branches are either gone or already merged into the
base branch. Use --dry-run to preview, --yes to skip the confirm prompt.`,
	}

	cmd.Flags().BoolVar(&flags.dryRun, "dry-run", false, "show what would be pruned without executing")
	cmd.Flags().StringVar(&flags.base, "base", "", "base branch for merge detection (default: auto-detected)")
	cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "skip the confirmation prompt (CI-friendly)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return b.pruneRunE(cmd, flags)
	}

	return cmd
}

// pruner is the subset of git.Client that branchPrune needs,
// allowing tests to inject a fake without a real git repository.
type pruner interface {
	DefaultBaseBranch() (string, error)
	LocalBranchNames() ([]string, error)
	IsMergedInto(branchName, base string) (bool, error)
}

func (b Branch) pruneRunE(cmd *cobra.Command, flags pruneFlags) error {
	ctx := cmd.Context()
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
	if err != nil {
		return err
	}

	var prompter PrunePrompter = newHuhPrunePrompter()
	if flags.yes {
		prompter = newAutoConfirmPrunePrompter()
	}

	return runPrune(ctx, os.Stdout, s, c, prompter, flags)
}

// runPrune executes the prune logic. w receives non-TUI output.
// When flags.dryRun is true it prints the summary and returns without mutating
// the store. Otherwise it delegates the destructive confirmation to prompter
// (huh-driven in production, auto-confirm under --yes, scripted in tests) and
// then calls executePrune.
func runPrune(ctx context.Context, w io.Writer, s *store.Store, pruner pruner, prompter PrunePrompter, flags pruneFlags) error {
	base := flags.base
	if base == "" {
		var err error
		base, err = pruner.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	localNames, err := pruner.LocalBranchNames()
	if err != nil {
		return fmt.Errorf("list local branches: %w", err)
	}

	localSet := make(map[string]struct{}, len(localNames))
	for _, n := range localNames {
		localSet[n] = struct{}{}
	}

	rows, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var result pruneResult

	for i := range rows {
		if _, exists := localSet[rows[i].BranchName]; !exists {
			result.toDelete = append(result.toDelete, rows[i])

			continue
		}

		merged, mergeErr := pruner.IsMergedInto(rows[i].BranchName, base)
		if mergeErr != nil {
			slog.Warn("merge check failed", "branch", rows[i].BranchName, "error", mergeErr)

			continue
		}

		if merged {
			result.toMerge = append(result.toMerge, rows[i])
		}
	}

	if len(result.toDelete) == 0 && len(result.toMerge) == 0 {
		fmt.Fprintln(w, "Nothing to prune.")

		return nil
	}

	renderPruneSummary(w, result)

	if flags.dryRun {
		return nil
	}

	confirmed, err := prompter.ConfirmPrune(ctx, len(result.toDelete), len(result.toMerge))
	if err != nil {
		return fmt.Errorf("confirm prune: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(w, "Aborted.")

		return nil
	}

	return executePrune(ctx, w, s, result)
}

func renderPruneSummary(w io.Writer, result pruneResult) {
	if len(result.toDelete) > 0 {
		fmt.Fprintln(w, "Will delete (local ref gone):")
		for i := range result.toDelete {
			fmt.Fprintf(w, "  - %s\n", result.toDelete[i].BranchName)
		}
	}

	if len(result.toMerge) == 0 {
		return
	}

	fmt.Fprintln(w, "Will mark merged (tip reachable from base):")
	for i := range result.toMerge {
		fmt.Fprintf(w, "  ~ %s\n", result.toMerge[i].BranchName)
	}
}

// executePrune performs the destructive store mutations gathered into result:
// it deletes rows whose local ref is gone and marks the remaining ones merged.
// The caller is responsible for obtaining confirmation beforehand. w receives
// the final "Pruned: N deleted, M marked merged." success line.
func executePrune(ctx context.Context, w io.Writer, s *store.Store, result pruneResult) error {
	now := time.Now()

	for i := range result.toDelete {
		if err := s.DeleteBranch(ctx, result.toDelete[i].BranchName); err != nil {
			return fmt.Errorf("delete %q: %w", result.toDelete[i].BranchName, err)
		}
	}

	for i := range result.toMerge {
		if err := s.UpdateBranchStatus(ctx, result.toMerge[i].BranchName, store.StatusIDMerged, &now); err != nil {
			return fmt.Errorf("mark merged %q: %w", result.toMerge[i].BranchName, err)
		}
	}

	fmt.Fprintf(w, "Pruned: %d deleted, %d marked merged.\n", len(result.toDelete), len(result.toMerge))

	return nil
}

func mergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge",
		Short: "Merge a branch",
		RunE:  mergeRunE,
	}
}

func mergeRunE(cmd *cobra.Command, _ []string) error {
	fmt.Fprintln(cmd.OutOrStdout(), "Not yet implemented.")

	return nil
}
