package issue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/go-git/go-git/v6/plumbing"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// shortSHALen is the number of hex characters used to abbreviate a commit SHA.
const shortSHALen = 7

type MergeStrategy string

const (
	StrategySquash  MergeStrategy = "squash"
	StrategyRebase  MergeStrategy = "rebase"
	StrategyClassic MergeStrategy = "classic"
)

// errFastForwardDeferred signals that the rebase commit landed on feature but
// local base could not fast-forward (diverged from origin/<base>). closeRunE
// uses it to skip post-merge bookkeeping while still exiting cleanly.
var errFastForwardDeferred = errors.New("commit created, fast-forward deferred")

// mergeContext bundles inputs for doMerge / doSquashCommit so each helper
// stays under the revive argument-limit while keeping inputs immutable.
type mergeContext struct {
	client       *git.Client
	pickedBranch *store.BranchRow
	baseBranch   string
	cfg          *config.AppConfig
	store        *store.Store
}

func (i Issue) getCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "Close an issue (merge branch, update store and tracker)",
		Long: `Pick an in-progress branch, merge it into the base branch (rebase, squash, or classic),
update the local store, update the remote tracker, then optionally delete the local branch.`,
		RunE: i.closeRunE,
	}
}

func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	if i.appConfig.Branch.Remote != "" {
		client.SetRemote(i.appConfig.Branch.Remote)
	}

	picked, err := getPickedBranch(ctx, s, client)
	if err != nil {
		return err
	}

	if picked == nil {
		return nil
	}

	base := i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	mc := mergeContext{
		client:       client,
		pickedBranch: picked,
		baseBranch:   base,
		cfg:          i.appConfig,
		store:        s,
	}

	strategy, aborted, err := doMerge(ctx, mc)
	if err != nil {
		if errors.Is(err, errFastForwardDeferred) {
			return nil
		}

		return err
	}

	if aborted {
		fmt.Fprintln(mc.client.IO().Out, "Aborted.")

		return nil
	}

	// warn on stderr but do not abort — the merge already succeeded.
	i.updateStatus(cmd, s, picked)

	if err := doDeleteBranch(cmd, client, picked, strategy); err != nil {
		return err
	}

	fmt.Fprintf(mc.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}

// getPickedBranch returns (nil, nil) when there are no in-progress branches.
func getPickedBranch(ctx context.Context, s *store.Store, client *git.Client) (*store.BranchRow, error) {
	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Println("No in-progress branches.")

		return nil, nil
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		currentBranch = ""
	}

	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, currentBranch, &picked)).Run(); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}

// doMerge runs the full merge flow: dry-run, strategy picker, confirm, then
// the actual merge. aborted is true when the user cancelled at the confirm prompt.
func doMerge(ctx context.Context, mc mergeContext) (strategy MergeStrategy, aborted bool, err error) {
	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, mc.baseBranch)
	if err != nil {
		return "", false, fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(mc.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(mc.client.IO().Out, "  "+f)
		}
		fmt.Fprintln(mc.client.IO().Out, "Aborting.")

		return "", false, fmt.Errorf("merge conflicts in branch %q", mc.pickedBranch.BranchName)
	}

	var picked string
	strategyForm := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{
			Value: string(StrategyRebase),
			Label: "Rebase",
			Hint:  "Single clean commit on local base, submodule-safe (recommended)",
		},
		{Value: string(StrategySquash), Label: "Squash", Hint: "git merge --squash — fast, but not submodule-safe"},
		{Value: string(StrategyClassic), Label: "Classic", Hint: "git merge --no-ff with commitizen message — preserves full history"},
	})
	if err := huh.NewForm(strategyForm).Run(); err != nil {
		return "", false, fmt.Errorf("strategy picker: %w", err)
	}

	strategy = MergeStrategy(picked)

	var confirmed bool
	confirmForm := tui.IssueMergeConfirm(mc.pickedBranch.BranchName, mc.baseBranch, string(strategy), &confirmed)
	if err := huh.NewForm(confirmForm).Run(); err != nil {
		return "", false, fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		return strategy, true, nil
	}

	switch strategy {
	case StrategyClassic:
		if err := doClassicClose(ctx, mc); err != nil {
			return strategy, false, err
		}
	case StrategySquash:
		if err := doSquashCommit(ctx, mc); err != nil {
			return strategy, false, err
		}
	case StrategyRebase:
		if err := doRebaseClose(ctx, mc); err != nil {
			return strategy, false, err
		}
	default:
		return strategy, false, fmt.Errorf("unknown strategy %q", strategy)
	}

	return strategy, false, nil
}

// doSquashCommit resolves the source and base tip SHAs, runs `git merge --squash`
// (which stages the merge but does not commit), then opens the commit form
// pre-filled with type/scope and a "Squashed merge of <bsha> into <basesha>."
// subject. The author dropdown defaults to the current git identity. Esc/Ctrl+C
// in the form aborts the close; staged changes are left in place so the operator
// can inspect or `git reset` them.
func doSquashCommit(ctx context.Context, mc mergeContext) error {
	branchHash, err := mc.client.ResolveRef("refs/heads/" + mc.pickedBranch.BranchName)
	if err != nil {
		return fmt.Errorf("resolve branch %q: %w", mc.pickedBranch.BranchName, err)
	}

	baseHash, err := mc.client.ResolveRef("refs/heads/" + mc.baseBranch)
	if err != nil {
		return fmt.Errorf("resolve base %q: %w", mc.baseBranch, err)
	}

	if err := mc.client.MergeSquash(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge squash: %w", err)
	}

	hint := commitpkg.IssueHint{
		IssueID:    mc.pickedBranch.IssueSlug,
		BranchType: mc.pickedBranch.Type,
	}
	prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Squashed merge of %s into %s.",
		branchHash.String()[:shortSHALen], baseHash.String()[:shortSHALen])

	authors, authorsErr := mc.client.Authors()
	if authorsErr != nil {
		slog.Warn("could not load author list", "error", authorsErr)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}

	if err := mc.client.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit squash: %w", err)
	}

	return nil
}

// updateStatus marks the branch and issue as merged/closed in the store and
// optionally updates the remote tracker. Errors are non-fatal — the merge has
// already been committed, so we warn rather than fail.
func (i Issue) updateStatus(cmd *cobra.Command, s *store.Store, pickedBranch *store.BranchRow) {
	now := time.Now()
	if err := s.UpdateBranchStatus(cmd.Context(), pickedBranch.BranchName, store.StatusIDMerged, &now); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update branch status: %v\n", err)
	}

	if err := s.UpdateIssueStatus(cmd.Context(), pickedBranch.IssueID, store.StatusIDMerged); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update issue status: %v\n", err)
	}

	if i.appConfig.IssueTracker.Type != "" {
		i.closeTrackerIssue(cmd, pickedBranch.IssueSlug)
	}
}

func doDeleteBranch(cmd *cobra.Command, c *git.Client, pickedBranch *store.BranchRow, strategy MergeStrategy) error {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(pickedBranch.BranchName, &shouldDelete)).Run(); err != nil {
		return fmt.Errorf("delete branch form: %w", err)
	}

	if shouldDelete {
		force := strategy == StrategySquash || strategy == StrategyRebase
		if err := c.DeleteLocalBranch(cmd.Context(), pickedBranch.BranchName, force); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: delete branch: %v\n", err)
		}
	}

	return nil
}

func (i Issue) closeTrackerIssue(cmd *cobra.Command, issueSlug string) {
	t, err := tracker.New(i.appConfig.IssueTracker)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: init tracker: %v\n", err)

		return
	}

	i.updateTrackerIssueStatus(cmd, t, issueSlug)
}

// doRebaseClose runs the Rebase strategy: pre-flights the working tree, fetches
// the configured remote (no-op when none), validates the merge endpoint with
// merge-tree, performs a real `git merge <remote>/<base>` (submodule-safe —
// falls back to local <base> when no remote), soft-resets feature back to the
// same ref so the merged diff is staged, drives the commitizen TUI form,
// commits, and fast-forwards local base. Rollback uses a named-return closure:
// any failure between the soft-reset and a successful commit triggers
// `git reset --hard <featureOrigSHA>` to atomically restore the feature ref.
// The post-commit FF failure is signalled with errFastForwardDeferred so the
// caller can skip post-merge bookkeeping without rolling back the new commit.
func doRebaseClose(ctx context.Context, mc mergeContext) (err error) {
	plan, err := rebasePreflight(ctx, mc)
	if err != nil {
		return err
	}

	if err := mc.client.MergeRebase(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge rebase: %w", err)
	}

	defer func() {
		if err == nil || errors.Is(err, errFastForwardDeferred) {
			return
		}

		if rbErr := mc.client.ResetHard(ctx, plan.featureOrigSHA.String()); rbErr != nil {
			err = fmt.Errorf("rollback after %w failed: %v", err, rbErr)

			return
		}

		fmt.Fprintf(mc.client.IO().Err,
			"Rolled back: feature branch %q restored to %s\n",
			mc.pickedBranch.BranchName, plan.featureOrigSHA.String()[:shortSHALen])
	}()

	baseRef := "refs/remotes/" + plan.remoteBase
	if plan.remoteName == "" {
		baseRef = "refs/heads/" + mc.baseBranch
	}
	baseOriginSHA, err := mc.client.ResolveRef(baseRef)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", baseRef, err)
	}

	hint := commitpkg.IssueHint{
		IssueID:    mc.pickedBranch.IssueSlug,
		BranchType: mc.pickedBranch.Type,
	}
	prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Squashed close of %s into %s.",
		plan.featureOrigSHA.String()[:shortSHALen], baseOriginSHA.String()[:shortSHALen])

	authors, authorsErr := mc.client.Authors()
	if authorsErr != nil {
		slog.Warn("could not load author list", "error", authorsErr)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}

	if err := mc.client.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if ffErr := mc.client.FastForwardOnly(ctx, mc.pickedBranch.BranchName, mc.baseBranch); ffErr != nil {
		fmt.Fprintf(mc.client.IO().Err,
			"Commit created on %q but local %s has diverged from %s.\n"+
				"Run `git pull --ff-only` on %s, then `git merge --ff-only %s` to land it.\n",
			mc.pickedBranch.BranchName, mc.baseBranch, plan.remoteBase,
			mc.baseBranch, mc.pickedBranch.BranchName)

		return errFastForwardDeferred
	}

	return nil
}

// doClassicClose drives the Classic strategy: shared rebasePreflight,
// FF-sync of local base against origin/<base> (or direct checkout when no
// remote), real --no-ff --no-commit merge on base, commitizen form,
// commit. Rollback on any failure between MergeNoFFNoCommit and a
// successful Commit runs `git merge --abort` to clear MERGE_HEAD /
// MERGE_MSG and restore the working tree. The defer uses a named return
// + closure so it observes the actual err at function exit.
func doClassicClose(ctx context.Context, mc mergeContext) (err error) {
	// rebasePreflight is reused verbatim: dirty check, feature checkout +
	// SHA, remote detection, fetch, remoteBase computation, ancestor check,
	// dry-run — all needed by Classic too. The rebasePlan's featureOrigSHA
	// is used here only for the prefill subject (no rollback role since
	// Classic uses AbortMerge instead of ResetHard).
	plan, err := rebasePreflight(ctx, mc)
	if err != nil {
		return err
	}

	// Step 2: sync local base with origin/<base> (no-op when no remote).
	if plan.remoteName != "" {
		if err := mc.client.FastForwardOnly(ctx, plan.remoteBase, mc.baseBranch); err != nil {
			return fmt.Errorf("local %s diverged from %s — `git pull --ff-only` first: %w",
				mc.baseBranch, plan.remoteBase, err)
		}
	} else {
		if err := mc.client.Checkout(ctx, mc.baseBranch); err != nil {
			return fmt.Errorf("checkout %s: %w", mc.baseBranch, err)
		}
	}

	// Step 3: resolve integration target SHA for the prefill subject.
	baseSHA, err := mc.client.ResolveRef("refs/heads/" + mc.baseBranch)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", mc.baseBranch, err)
	}

	// Step 4: stage the merge without committing.
	if err := mc.client.MergeNoFFNoCommit(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge --no-ff --no-commit: %w", err)
	}

	defer func() {
		if err == nil {
			return
		}

		if abErr := mc.client.AbortMerge(ctx); abErr != nil {
			err = fmt.Errorf("merge --abort after %w failed: %v", err, abErr)

			return
		}

		fmt.Fprintf(mc.client.IO().Err,
			"Rolled back: working tree on %q restored to pre-merge state\n",
			mc.baseBranch)
	}()

	// Steps 5 + 6: TUI form (pre-filled) → commit.
	hint := commitpkg.IssueHint{
		IssueID:    mc.pickedBranch.IssueSlug,
		BranchType: mc.pickedBranch.Type,
	}
	prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Merge %s into %s.",
		plan.featureOrigSHA.String()[:shortSHALen], baseSHA.String()[:shortSHALen])

	authors, authorsErr := mc.client.Authors()
	if authorsErr != nil {
		slog.Warn("could not load author list", "error", authorsErr)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}

	if err := mc.client.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// No post-commit fast-forward: unlike Rebase, the merge commit lands
	// directly on base (MergeNoFFNoCommit checked out base before merging),
	// so base is already at the new HEAD. No errFastForwardDeferred path.
	return nil
}

// rebasePlan captures the state computed by rebasePreflight and consumed by
// doRebaseClose. remoteBase is "<remote>/<base>" when a remote is configured,
// otherwise "<base>"; remoteName is "" in the no-remote case so callers can
// pick the correct ref namespace.
type rebasePlan struct {
	featureOrigSHA plumbing.Hash
	remoteName     string
	remoteBase     string
}

// rebasePreflight runs the read-only checks that precede MergeRebase: dirty
// tree, checkout, resolve HEAD + remote, fetch, compute the merge endpoint,
// ancestor check, and merge dry-run.
func rebasePreflight(ctx context.Context, mc mergeContext) (rebasePlan, error) {
	dirty, err := mc.client.IsDirty(ctx)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("dirty check: %w", err)
	}

	if dirty {
		return rebasePlan{},
			errors.New("working tree has uncommitted modifications — commit or stash before closing")
	}

	if err := mc.client.Checkout(ctx, mc.pickedBranch.BranchName); err != nil {
		return rebasePlan{}, fmt.Errorf("checkout %s: %w", mc.pickedBranch.BranchName, err)
	}

	featureOrigSHA, err := mc.client.ResolveRef("HEAD")
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve HEAD: %w", err)
	}

	remoteName, err := mc.client.Remote()
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve remote: %w", err)
	}

	if err := mc.client.Fetch(ctx); err != nil {
		return rebasePlan{}, fmt.Errorf("fetch: %w", err)
	}

	remoteBase := mc.baseBranch
	if remoteName != "" {
		remoteBase = remoteName + "/" + mc.baseBranch
	}

	integrated, err := mc.client.IsAncestor(ctx, mc.pickedBranch.BranchName, remoteBase)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("ancestor check: %w", err)
	}

	if integrated {
		//nolint:revive // Question mark
		return rebasePlan{}, fmt.Errorf("%q has no commits ahead of %s — already integrated?",
			mc.pickedBranch.BranchName, remoteBase)
	}

	if err := mergeDryRun(ctx, mc, remoteBase); err != nil {
		return rebasePlan{}, err
	}

	return rebasePlan{
		featureOrigSHA: featureOrigSHA,
		remoteName:     remoteName,
		remoteBase:     remoteBase,
	}, nil
}

func mergeDryRun(ctx context.Context, mc mergeContext, remoteBase string) error {
	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, remoteBase)
	if err != nil {
		return fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(mc.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(mc.client.IO().Out, "  "+f)
		}

		return fmt.Errorf("merge conflicts vs %s in %q", remoteBase, mc.pickedBranch.BranchName)
	}

	return nil
}
