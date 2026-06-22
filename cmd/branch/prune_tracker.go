package branch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

// defaultIssueIDPattern matches a leading issue ID at the start of a branch name.
// First non-empty capture wins.
//
//	Pattern A: leading numeric ID before a delimiter      ([0-9]+)[-@_|+=.]
//	Pattern B: alphanumeric ID before @ or |              ([^@|]+)[@|]
//
// Intentionally narrow for v1. Lift to AppConfig.Branch.IssueIDPattern in a
// follow-up if user-extensibility is needed.
var defaultIssueIDPattern = regexp.MustCompile(`^(?:(\d+)[-@_|+=.]|([^@|]+)[@|]).+`)

// extractIssueID returns the first non-empty regex capture from name.
// Returns ("", false) if the name does not match the pattern.
func extractIssueID(name string) (string, bool) {
	m := defaultIssueIDPattern.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}

	for _, g := range m[1:] {
		if g != "" {
			return g, true
		}
	}

	return "", false
}

// trackerCandidate is one branch-and-issue pair flagged by discovery for reaping.
type trackerCandidate struct {
	BranchName string
	IssueID    string
	StoreRow   *store.BranchRow // nil → branch unknown to git-zf; no store flip after delete.
}

// trackerPruner is the git surface area prune-tracker depends on.
// Same dependency-inversion shape as the existing `pruner` interface
// (see branch.go) — keeps tests off a real repo.
type trackerPruner interface {
	DefaultBaseBranch() (string, error)
	LocalBranchNames() ([]string, error)
	SafeDeleteBranch(name string) error
	ForceDeleteBranch(name string) error
}

// issueResolver is the tracker subset prune-tracker needs.
// Allows test fakes to stub IsIssueClosed without implementing the full tracker.Tracker.
type issueResolver interface {
	IsIssueClosed(ctx context.Context, issueID string) (bool, error)
}

// trackerPruneResult bundles the discovery output (and the warnings discovery emitted).
type trackerPruneResult struct {
	Candidates []trackerCandidate
	Warnings   []string
}

// runDiscoverTracker enumerates local branches, extracts issue IDs, asks the
// resolver which are closed, and produces a sorted candidate list. Warnings
// (tracker errors) are accumulated, not fatal.
//
// storeByName may be nil; entries are looked up by branch name and copied into
// the candidate's StoreRow field (nil → branch unknown to git-zf).
//
// w is the user-facing writer for inline warnings (matches the rest of cmd/branch).
func runDiscoverTracker(
	ctx context.Context,
	w io.Writer,
	pr trackerPruner,
	tr issueResolver,
	storeByName map[string]*store.BranchRow,
	base string,
) (trackerPruneResult, error) {
	locals, err := pr.LocalBranchNames()
	if err != nil {
		return trackerPruneResult{}, fmt.Errorf("list local branches: %w", err)
	}

	var result trackerPruneResult

	for _, name := range locals {
		if name == base {
			continue
		}

		id, ok := extractIssueID(name)
		if !ok {
			continue
		}

		closed, err := tr.IsIssueClosed(ctx, id)
		if err != nil {
			switch {
			case errors.Is(err, tracker.ErrIssueNotFound):
				line := fmt.Sprintf("WARN: %s not found in tracker — skipping", id)
				result.Warnings = append(result.Warnings, line)
				fmt.Fprintln(w, line)
			default:
				line := fmt.Sprintf("WARN: %s lookup failed: %v — skipping", id, err)
				result.Warnings = append(result.Warnings, line)
				fmt.Fprintln(w, line)
			}

			continue
		}

		if !closed {
			continue
		}

		result.Candidates = append(result.Candidates, trackerCandidate{
			BranchName: name,
			IssueID:    id,
			StoreRow:   storeByName[name],
		})
	}

	sort.Slice(result.Candidates, func(i, j int) bool {
		return result.Candidates[i].BranchName < result.Candidates[j].BranchName
	})

	return result, nil
}

// updateStatusFn is the small subset of store.Store reachable from execution.
// Decoupled to keep tests off SQLite.
type updateStatusFn func(ctx context.Context, name string, statusID int64) error

// runExecuteTracker iterates candidates in input order, performs the per-branch
// ref action requested by decisions[name], and flips the store row to closed
// when a StoreRow is present and the ref action succeeded (or was skipped).
//
// Returns warnings accumulated during execution (safe-delete refusals). A
// returned error means a fatal failure (e.g. force-delete failed against git).
//
// w receives inline warnings + the final summary line.
func runExecuteTracker(
	ctx context.Context,
	w io.Writer,
	pr trackerPruner,
	updateStatus updateStatusFn,
	candidates []trackerCandidate,
	decisions map[string]string,
) ([]string, error) {
	var (
		warnings                    []string
		nSafe, nForce, nSkip, nKept int
	)

	for _, c := range candidates {
		action := decisions[c.BranchName]
		flipStore := true

		switch action {
		case "safe":
			if err := pr.SafeDeleteBranch(c.BranchName); err != nil {
				var line string
				if errors.Is(err, git.ErrBranchNotMerged) {
					line = fmt.Sprintf("WARN: kept %s — git refused safe-delete (branch not fully merged into HEAD or upstream)", c.BranchName)
				} else {
					line = fmt.Sprintf("WARN: kept %s — safe-delete failed: %v", c.BranchName, err)
				}

				warnings = append(warnings, line)
				fmt.Fprintln(w, line)
				nKept++
				flipStore = false
			} else {
				nSafe++
			}
		case "force":
			if err := pr.ForceDeleteBranch(c.BranchName); err != nil {
				return warnings, fmt.Errorf("force-delete %s: %w", c.BranchName, err)
			}
			nForce++
		case "skip":
			nSkip++
		default:
			return warnings, fmt.Errorf("internal: unknown action %q for %s", action, c.BranchName)
		}

		if !flipStore || c.StoreRow == nil {
			continue
		}

		if err := updateStatus(ctx, c.BranchName, store.StatusIDClosed); err != nil {
			return warnings, fmt.Errorf("flip status for %s: %w", c.BranchName, err)
		}
	}

	fmt.Fprintf(w, "Tracker-pruned: %d safe, %d forced, %d skipped, %d kept (refused).\n",
		nSafe, nForce, nSkip, nKept)

	return warnings, nil
}

// pruneTrackerFlags mirrors the spec's flag list.
type pruneTrackerFlags struct {
	dryRun      bool
	base        string
	safeDelete  bool
	forceDelete bool
	skipDelete  bool
}

func (b Branch) pruneTrackerCmd() *cobra.Command {
	var flags pruneTrackerFlags

	cmd := &cobra.Command{
		Use:   "prune-tracker",
		Short: "Reap branches whose tracker issue is closed",
		Long: `Discover local branches whose issue ID (regex-extracted from the branch name)
is closed in the configured tracker, and offer per-branch reap actions
(safe-delete / force-delete / skip). Successful reaps flip the corresponding
store row to status='closed'.`,
	}

	f := cmd.Flags()
	f.BoolVar(&flags.dryRun, "dry-run", false, "show what would be done without prompting or mutating")
	f.StringVar(&flags.base, "base", "", "base branch name to exclude from candidate discovery (default: auto-detect)")
	f.BoolVar(&flags.safeDelete, "safe-delete", false, "non-interactive: apply `git branch -d` to every match")
	f.BoolVar(&flags.forceDelete, "force-delete", false, "non-interactive: apply `git branch -D` to every match")
	f.BoolVar(&flags.skipDelete, "skip-delete", false, "non-interactive: never touch refs; only flip store status")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		nSet := 0
		for _, name := range []string{"safe-delete", "force-delete", "skip-delete"} {
			if cmd.Flags().Changed(name) {
				nSet++
			}
		}
		if nSet > 1 {
			return fmt.Errorf("--safe-delete, --force-delete, and --skip-delete are mutually exclusive")
		}

		return b.pruneTrackerRunE(cmd, flags)
	}

	return cmd
}

func (b Branch) pruneTrackerRunE(cmd *cobra.Command, flags pruneTrackerFlags) error {
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

	tr, err := tracker.New(b.appConfig.IssueTracker)
	if err != nil {
		return fmt.Errorf("build tracker: %w", err)
	}

	var prompter TrackerPrunePrompter = newHuhTrackerPrunePrompter()

	switch {
	case flags.safeDelete:
		prompter = newFixedActionPrompter("safe")
	case flags.forceDelete:
		prompter = newFixedActionPrompter("force")
	case flags.skipDelete:
		prompter = newFixedActionPrompter("skip")
	}

	return runPruneTracker(ctx, os.Stdout, s, c, tr, prompter, flags)
}

// runPruneTracker is the top-level orchestrator. Split out so E2E tests can
// invoke it with a fake pruner + fake tracker + scripted prompter.
func runPruneTracker(
	ctx context.Context,
	w io.Writer,
	s *store.Store,
	pr trackerPruner,
	tr issueResolver,
	prompter TrackerPrunePrompter,
	flags pruneTrackerFlags,
) error {
	base := flags.base
	if base == "" {
		var err error
		base, err = pr.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	allRows, err := s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	storeByName := make(map[string]*store.BranchRow, len(allRows))
	for i := range allRows {
		storeByName[allRows[i].BranchName] = &allRows[i]
	}

	result, err := runDiscoverTracker(ctx, w, pr, tr, storeByName, base)
	if err != nil {
		return err
	}

	if len(result.Candidates) == 0 {
		fmt.Fprintln(w, "Nothing to prune from tracker.")

		return nil
	}

	fmt.Fprintln(w, "Tracker-closed candidates:")
	for _, c := range result.Candidates {
		fmt.Fprintf(w, "  ~ %s (issue %s)\n", c.BranchName, c.IssueID)
	}

	if flags.dryRun {
		return nil
	}

	decisions, err := prompter.DecideReap(ctx, result.Candidates)
	if err != nil {
		return fmt.Errorf("decide reap: %w", err)
	}

	updateStatus := func(ctx context.Context, name string, id int64) error {
		now := time.Now()

		return s.UpdateBranchStatus(ctx, name, id, &now)
	}

	if _, err := runExecuteTracker(ctx, w, pr, updateStatus, result.Candidates, decisions); err != nil {
		return err
	}

	return nil
}
