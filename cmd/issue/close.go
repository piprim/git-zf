package issue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

// shortSHALen is the number of hex characters used to abbreviate a commit SHA.
const shortSHALen = 7

// closeDeps bundles the long-lived dependencies the close flow needs.
// Production code builds it via buildCloseDeps; tests inject directly.
type closeDeps struct {
	client  *git.Client
	store   *store.Store
	cfg     *config.AppConfig
	tracker tracker.Tracker // nil ⇒ no tracker update will be attempted
}

// buildCloseDeps constructs the production closeDeps from a cobra command.
// Returns an error if the repo cannot be opened or the store cannot be
// initialised. When cfg.IssueTracker.Type == "" the returned deps.tracker is
// nil (runClose treats that as "skip tracker update").
func buildCloseDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig) (closeDeps, error) {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return closeDeps{}, fmt.Errorf("failed to get store: %w", err)
	}

	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		_ = s.Close()

		return closeDeps{}, fmt.Errorf("not a git repository: %w", err)
	}

	if cfg.Branch.Remote != "" {
		client.SetRemote(cfg.Branch.Remote)
	}

	deps := closeDeps{client: client, store: s, cfg: cfg}

	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			// Non-fatal: warn and continue with a nil tracker.
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.tracker = t
		}
	}

	return deps, nil
}

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

	deps, err := buildCloseDeps(ctx, cmd, i.appConfig)
	if err != nil {
		return err
	}
	defer func() { _ = deps.store.Close() }()

	return runClose(ctx, deps, newHuhPrompter(deps.client, deps.store, i.appConfig))
}

// runClose runs the full merge → store → tracker → delete-branch pipeline
// without opening any huh forms directly. All user-facing decisions are
// resolved by prompter. Used by both closeRunE (production) and the E2E
// tests (with a scripted prompter).
//
// Returns nil on the errFastForwardDeferred path — the commit landed and
// the operator just needs to fast-forward the local base manually; runClose
// has already printed the recovery instructions.
//
// Unexported because closeDeps is unexported (no cross-package caller).
func runClose(ctx context.Context, deps closeDeps, prompter ClosePrompter) error {
	picked, err := getPickedBranch(ctx, deps.store, deps.client, prompter)
	if err != nil {
		return err
	}

	if picked == nil {
		return nil
	}

	base := deps.cfg.Branch.Base
	if base == "" {
		base, err = deps.client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	mc := mergeContext{
		client:       deps.client,
		pickedBranch: picked,
		baseBranch:   base,
		cfg:          deps.cfg,
		store:        deps.store,
	}

	strategy, aborted, err := doMerge(ctx, mc, prompter)
	if err != nil {
		if errors.Is(err, errFastForwardDeferred) {
			return nil
		}

		return err
	}

	if aborted {
		fmt.Fprintln(deps.client.IO().Out, "Aborted.")

		return nil
	}

	updateClosedStatus(ctx, deps, picked, prompter)

	if err := doDeleteBranch(ctx, deps.client, picked, strategy, prompter); err != nil {
		return err
	}

	fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}

// getPickedBranch returns (nil, nil) when there are no in-progress branches.
func getPickedBranch(
	ctx context.Context,
	s *store.Store,
	client *git.Client, prompter ClosePrompter) (*store.BranchRow, error) {
	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Fprintln(client.IO().Out, "No in-progress branches.")

		return nil, nil
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		currentBranch = ""
	}

	picked, err := prompter.PickBranch(ctx, branches, currentBranch)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return nil, err
	}

	return picked, nil
}

// doMerge runs the full merge flow: dry-run, strategy picker, confirm, then
// the actual merge. aborted is true when the user cancelled at the confirm prompt.
func doMerge(
	ctx context.Context,
	mc mergeContext,
	prompter ClosePrompter) (strategy MergeStrategy, aborted bool, err error) {
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

	strategy, err = prompter.PickStrategy(ctx)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return "", false, err
	}

	confirmed, err := prompter.ConfirmMerge(ctx, mc.pickedBranch.BranchName, mc.baseBranch, strategy)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return "", false, err
	}

	if !confirmed {
		return strategy, true, nil
	}

	switch strategy {
	case StrategyClassic:
		if err := doClassicClose(ctx, mc, prompter); err != nil {
			return strategy, false, err
		}
	case StrategySquash:
		if err := doSquashCommit(ctx, mc, prompter); err != nil {
			return strategy, false, err
		}
	case StrategyRebase:
		if err := doRebaseClose(ctx, mc, prompter); err != nil {
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
func doSquashCommit(ctx context.Context, mc mergeContext, prompter ClosePrompter) error {
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

	msg, opts, err := prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter error already wrapped
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

// updateClosedStatus marks the branch and issue as merged in the store and,
// when a tracker is configured, drives the status-picker form. Every error
// here is non-fatal — the merge already committed, so the operator must be
// able to clean up store/tracker drift manually.
func updateClosedStatus(ctx context.Context, deps closeDeps, picked *store.BranchRow, prompter ClosePrompter) {
	now := time.Now()
	if err := deps.store.UpdateBranchStatus(ctx, picked.BranchName, store.StatusIDMerged, &now); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update branch status: %v\n", err)
	}

	if err := deps.store.UpdateIssueStatus(ctx, picked.IssueID, store.StatusIDMerged); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update issue status: %v\n", err)
	}

	if deps.tracker == nil {
		return
	}

	statuses, err := deps.tracker.ListStatuses(ctx)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	selected, err := prompter.PickTrackerStatus(ctx, picked.IssueSlug, deps.cfg.IssueTracker.Type, statuses)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: status picker: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := deps.tracker.UpdateIssueStatus(ctx, picked.IssueSlug, selected); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update tracker status: %v\n", err)
	}
}

func doDeleteBranch(
	ctx context.Context,
	c *git.Client,
	picked *store.BranchRow,
	strategy MergeStrategy, prompter ClosePrompter) error {
	shouldDelete, err := prompter.ConfirmDeleteBranch(ctx, picked.BranchName)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return err
	}

	if !shouldDelete {
		return nil
	}

	force := strategy == StrategySquash || strategy == StrategyRebase
	if err := c.DeleteLocalBranch(ctx, picked.BranchName, force); err != nil {
		fmt.Fprintf(c.IO().Err, "warning: delete branch: %v\n", err)
	}

	return nil
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
func doRebaseClose(ctx context.Context, mc mergeContext, prompter ClosePrompter) (err error) {
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

	msg, opts, err := prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter error already wrapped
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
func doClassicClose(ctx context.Context, mc mergeContext, prompter ClosePrompter) (err error) {
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

	msg, opts, err := prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter error already wrapped
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
