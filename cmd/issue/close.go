package issue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/convert"
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

	// baseOverride is the --base flag value (empty ⇒ smart default + picker).
	// Set per-invocation by closeRunE; left empty by the E2E tests that drive
	// the default/picker paths.
	baseOverride string

	// push proposal wiring (Phase 1). pushConfirm is nil in tests that build
	// closeDeps directly, which disables the push step there.
	push, noPush bool
	pushConfirm  pushflow.ConfirmFunc
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

	client, err := cmdutil.NewClientForCmd(cmd, cfg)
	if err != nil {
		_ = s.Close()

		return closeDeps{}, err
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

// ErrBranchLockedForReview is returned by reviewPreflight when the branch is
// locked because a review is in progress. Use errors.Is to detect it.
var ErrBranchLockedForReview = errors.New("branch locked for review")

// ErrReviewChangesRequested is returned by reviewPreflight when the reviewer
// has requested changes. Use errors.Is to detect it.
var ErrReviewChangesRequested = errors.New("reviewer requested changes")

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
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Close an issue (merge branch, update store and tracker)",
		Long: `Pick an in-progress branch, merge it into the base branch (rebase, squash, or classic),
update the local store, update the remote tracker, then optionally delete the local branch.`,
		RunE: i.closeRunE,
	}

	cmd.Flags().String("base", "",
		"merge target branch (default: parent integration branch or base, with an interactive picker)")

	pushflow.AddFlags(cmd)

	return cmd
}

func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	baseOverride, err := cmd.Flags().GetString("base")
	if err != nil {
		return fmt.Errorf("read --base flag: %w", err)
	}

	deps, err := buildCloseDeps(ctx, cmd, i.appConfig)
	if err != nil {
		return err
	}
	defer func() { _ = deps.store.Close() }()

	deps.baseOverride = baseOverride
	deps.push, deps.noPush = pushflow.ReadFlags(cmd)
	deps.pushConfirm = pushflow.NewHuhConfirm()

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

	if err := reviewPreflight(ctx, deps, picked); err != nil {
		return err
	}

	base, err := resolveDefaultBase(ctx, deps, picked)
	if err != nil {
		return err
	}

	base, err = chooseMergeTarget(ctx, deps, picked, base, prompter)
	if err != nil {
		return err
	}

	// Reconcile child statuses from branch refs so closes done in sibling clones
	// (e.g. Bob closed X.2 in his repo) are visible before the guard runs.
	// Branch refs are already fetched above (FetchBranchRefs is called when
	// parentSlug is empty, which is always the case for a top-level parent issue).
	reconcileChildrenFromRefs(ctx, deps, picked.IssueSlug)

	// Parent issue: block close until all children are merged.
	if allDone, err := deps.store.ChildrenAllMerged(ctx, picked.IssueSlug); err != nil {
		return fmt.Errorf("check children: %w", err)
	} else if !allDone {
		children, listErr := deps.store.ListChildIssues(ctx, picked.IssueSlug)
		if listErr != nil {
			return fmt.Errorf("issue %q has open sub-tasks (list unavailable: %w) — close all sub-tasks before closing the parent",
				picked.IssueSlug, listErr)
		}
		return fmt.Errorf("issue %q has open sub-tasks: %v — close all sub-tasks before closing the parent",
			picked.IssueSlug, children)
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

	return proposeClosePush(ctx, deps, base)
}

// resolveDefaultBase computes the smart-default merge target: the configured
// base (or DefaultBaseBranch) redirected to the parent integration branch when
// the picked issue has a parent. This is the value pre-selected in the picker.
//
// The body lives in issueflow.ResolveParentBranch so the commit flow can reuse
// the identical resolution for its merge-vs-parent preview; this wrapper keeps
// the close call site (runClose) unchanged.
func resolveDefaultBase(ctx context.Context, deps closeDeps, picked *store.BranchRow) (string, error) {
	return issueflow.ResolveParentBranch(ctx, deps.store, deps.client, picked.IssueSlug, deps.cfg.Branch.Base)
}

// chooseMergeTarget refines the smart-default base into the final merge target.
// When deps.baseOverride is set (from --base) it is validated and used directly.
// Otherwise, when more than one candidate branch exists, the picker is shown
// with defaultBase pre-selected; with a single candidate the default is used
// unchanged. The branch being closed is never a candidate.
func chooseMergeTarget(
	ctx context.Context,
	deps closeDeps,
	picked *store.BranchRow,
	defaultBase string,
	prompter ClosePrompter) (string, error) {
	if deps.baseOverride != "" {
		if deps.baseOverride == picked.BranchName {
			return "", fmt.Errorf("--base %q is the branch being closed; choose a different merge target", deps.baseOverride)
		}

		ok, err := baseBranchResolves(deps.client, deps.baseOverride)
		if err != nil {
			return "", fmt.Errorf("validate --base %q: %w", deps.baseOverride, err)
		}
		if !ok {
			return "", fmt.Errorf("--base %q does not resolve to a local or remote branch", deps.baseOverride)
		}

		return deps.baseOverride, nil
	}

	locals, err := deps.client.LocalBranchNames()
	if err != nil {
		return "", fmt.Errorf("list local branches: %w", err)
	}

	candidates := mergeTargetCandidates(locals, picked.BranchName, defaultBase)
	if len(candidates) <= 1 {
		return defaultBase, nil
	}

	chosen, err := prompter.PickBaseBranch(ctx, defaultBase, candidates)
	if err != nil {
		return "", err //nolint:wrapcheck // prompter error already wrapped
	}

	return chosen, nil
}

// mergeTargetCandidates returns the branches offerable as merge targets: every
// local branch except the one being closed, with defaultBase guaranteed present
// (it may be a remote-only parent integration branch absent from locals).
// defaultBase is placed first so it leads the picker list.
func mergeTargetCandidates(locals []string, closing, defaultBase string) []string {
	out := make([]string, 0, len(locals)+1)
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || name == closing || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	add(defaultBase)
	for _, name := range locals {
		add(name)
	}

	return out
}

// baseBranchResolves reports whether name is a usable merge target: a local
// branch, or <remote>/<name> when a remote is configured.
func baseBranchResolves(c *git.Client, name string) (bool, error) {
	exists, err := c.BranchExists(name)
	if err != nil {
		return false, fmt.Errorf("branch exists %q: %w", name, err)
	}
	if exists {
		return true, nil
	}

	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote != "" {
		if _, err := c.ResolveRef("refs/remotes/" + remote + "/" + name); err == nil {
			return true, nil
		}
	}

	return false, nil
}

// reviewPreflight checks whether the issue has an active review. Returns an
// error if close should be refused. When status is approved and the review
// branch has reviewer commits, it fast-forwards the feature branch to
// incorporate them and cleans up the review branch and ref.
//
// The git ref (refs/zf/reviews/<IssueID>) is the source of truth. The local
// store is a cache that may lag behind the reviewer's machine. reviewPreflight
// always fetches and reads the ref first so the developer never has to run a
// manual git fetch before closing.
func reviewPreflight(ctx context.Context, deps closeDeps, picked *store.BranchRow) error {
	// Fetch review refs (best-effort) so we see the reviewer's latest decision
	// even if the developer has not fetched since submitting for review.
	_ = deps.client.FetchReviewRefs(ctx)

	// Read the ref — authoritative source of truth.
	ref, _, refErr := deps.client.ReadReviewRef(ctx, picked.IssueSlug)
	if refErr != nil {
		return fmt.Errorf("read review ref: %w", refErr)
	}

	if ref == nil {
		// No active review ref — either no review was submitted, or it was
		// already cleaned up after a previous close. Proceed.
		return nil
	}

	// Reconcile local store from ref so downstream store reads are consistent.
	if latest, _ := deps.store.GetLatestReview(ctx, picked.IssueSlug); latest != nil {
		if store.ReviewStatus(ref.Status) != latest.Status {
			_ = deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatus(ref.Status), latest.HasCommits)
		}
	}

	switch store.ReviewStatus(ref.Status) {
	case store.ReviewStatusInReview:
		return fmt.Errorf(
			"branch %q is locked for review (issue %q, round %d) — awaiting reviewer decision.\n"+
				"Run `git zf review list` to check review status: %w",
			picked.BranchName, picked.IssueSlug, ref.Round, ErrBranchLockedForReview)

	case store.ReviewStatusChangesRequested:
		return fmt.Errorf(
			"reviewer requested changes on issue %q (round %d).\n"+
				"Address feedback and run `git zf review request` for round %d: %w",
			picked.IssueSlug, ref.Round, ref.Round+1, ErrReviewChangesRequested)

	case store.ReviewStatusApproved:
		reviewBranch := picked.IssueSlug + "@review"

		// Resolve the effective review branch ref for CommitsAhead and
		// FastForwardOnly. The reviewer may have pushed their review branch to
		// origin without the developer ever checking it out locally; in that case
		// use the remote tracking ref (origin/<reviewBranch>) so the incorporation
		// is not silently skipped.
		localExists, _ := deps.client.BranchExists(reviewBranch)
		effectiveReview := reviewBranch
		if !localExists {
			if remote, _ := deps.client.Remote(); remote != "" {
				candidate := remote + "/" + reviewBranch
				if _, err := deps.client.ResolveRef("refs/remotes/" + candidate); err == nil {
					effectiveReview = candidate
				}
			}
		}

		if localExists || effectiveReview != reviewBranch {
			n, countErr := deps.client.CommitsAhead(ctx, effectiveReview, picked.BranchName)
			if countErr == nil && n > 0 {
				fmt.Fprintf(deps.client.IO().Out,
					"Incorporating %d reviewer commit(s) from %s into %s...\n",
					n, reviewBranch, picked.BranchName)
				if err := deps.client.FastForwardOnly(ctx, effectiveReview, picked.BranchName); err != nil {
					return fmt.Errorf("fast-forward %s to %s: %w", picked.BranchName, reviewBranch, err)
				}
			}
			if localExists {
				if err := deps.client.DeleteLocalBranchSafe(ctx, reviewBranch, true, deps.cfg.Branch.Base); err != nil {
					fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
				}
			}
			_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
		}
		// Always clean up the review ref (local + remote) on close, regardless
		// of whether a review branch existed.
		_ = deps.client.DeleteReviewRef(ctx, picked.IssueSlug)
		return nil
	}

	return nil
}

// getPickedBranch returns (nil, nil) when there are no in-progress branches.
func getPickedBranch(
	ctx context.Context,
	s *store.Store,
	client *git.Client, prompter ClosePrompter) (*store.BranchRow, error) {
	// A branch closed in a sibling clone carries Merged=true on its
	// refs/zf/branches/<slug> ref (pushed by updateClosedStatus) but may still
	// show in_progress in this clone's store. Reconcile from the refs first so
	// the picker never offers a branch that was already closed elsewhere.
	issueflow.ReconcileMergedFromRefs(ctx, s, client)

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
	// The base branch may not exist locally (e.g. a parent integration branch
	// that Bob never checked out). LocalOrRemoteRef falls back to origin/<base>
	// so merge-tree can resolve it from the remote tracking ref.
	dryRunBase := mc.client.LocalOrRemoteRef(mc.baseBranch)
	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, dryRunBase)
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

	// Fast-forward local base to origin/<base> before squashing so the squash
	// commit lands on the current remote tip. Without this, a teammate's push
	// to the integration branch (e.g. Bob closing X.2) leaves the local base
	// stale; the squash would diverge from origin and the post-close push fails.
	if remote, _ := mc.client.Remote(); remote != "" {
		_ = mc.client.FastForwardOnly(ctx, remote+"/"+mc.baseBranch, mc.baseBranch)
	}

	baseHash, err := mc.client.ResolveBranchRef(mc.baseBranch)
	if err != nil {
		return fmt.Errorf("resolve base %q: %w", mc.baseBranch, err)
	}

	if err := mc.client.MergeSquash(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge squash: %w", err)
	}

	subject := fmt.Sprintf("Squashed merge of %s into %s.",
		branchHash.String()[:shortSHALen], baseHash.String()[:shortSHALen])

	return composeAndCommit(ctx, mc, prompter, subject, "squash")
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

	// Stamp the branch ref as merged and push so sibling developers on other
	// clones can detect this close without querying each other's stores. The
	// same ref also carries the tracker-origin signal used to gate the prompt
	// below, so read it once here.
	existing, _ := deps.client.ReadBranchRef(ctx, picked.IssueSlug)
	if existing != nil {
		merged := *existing
		merged.Merged = true
		if _, err := deps.client.WriteBranchRef(ctx, picked.IssueSlug, merged); err == nil {
			_ = deps.client.PushBranchRef(ctx, picked.IssueSlug)
		}
	}

	// Only offer a tracker status update for tracker-born issues. The origin
	// lives in the git object (BranchRef.TrackerType), not the local store, so
	// this is correct on a reviewer's clone too. A manual issue (ref absent or
	// TrackerType == "") must not prompt even when a tracker is configured.
	if existing == nil || existing.TrackerType == "" {
		return
	}

	issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err, picked.IssueSlug, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
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

	subject := fmt.Sprintf("Squashed close of %s into %s.",
		plan.featureOrigSHA.String()[:shortSHALen], baseOriginSHA.String()[:shortSHALen])

	if err := composeAndCommit(ctx, mc, prompter, subject, "rebase"); err != nil {
		return err
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
	subject := fmt.Sprintf("Merge %s into %s.",
		plan.featureOrigSHA.String()[:shortSHALen], baseSHA.String()[:shortSHALen])

	// No post-commit fast-forward: unlike Rebase, the merge commit lands
	// directly on base (MergeNoFFNoCommit checked out base before merging),
	// so base is already at the new HEAD. No errFastForwardDeferred path.
	return composeAndCommit(ctx, mc, prompter, subject, "classic")
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
		return rebasePlan{}, fmt.Errorf("%q has no commits ahead of %s",
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

// reconcileChildrenFromRefs reads refs/zf/branches/<childSlug> for every
// in-progress child of parentSlug. When a ref has Merged=true (written by the
// child's close in another clone), the local store is updated to merged so the
// ChildrenAllMerged guard doesn't block the parent close.
func reconcileChildrenFromRefs(ctx context.Context, deps closeDeps, parentSlug string) {
	children, err := deps.store.ListChildIssues(ctx, parentSlug)
	if err != nil || len(children) == 0 {
		return
	}

	branches, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return
	}

	isChild := make(map[string]bool, len(children))
	for _, childSlug := range children {
		isChild[childSlug] = true
	}

	now := time.Now()
	for _, b := range branches {
		if isChild[b.IssueSlug] {
			issueflow.MarkMergedFromRef(ctx, deps.store, deps.client, b, now)
		}
	}
}

// composeAndCommit builds the prefill from issue context + a strategy subject,
// drives the commit form, and commits. strategy labels the commit error (e.g.
// "squash", "rebase", "classic").
func composeAndCommit(ctx context.Context, mc mergeContext, prompter ClosePrompter, subject, strategy string) error {
	hint := commitpkg.IssueHint{IssueID: mc.pickedBranch.IssueSlug, BranchType: mc.pickedBranch.Type, Closing: true}
	prefill := hint.Prefill(mc.cfg.CommitMessage)
	prefill["subject"] = subject

	msg, opts, err := prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter error already wrapped
	}

	if err := mc.client.Commit(ctx, msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("commit %s: %w", strategy, err)
	}

	return nil
}

// proposeClosePush offers to push the merge target (base) after a successful
// close. No-op when no confirm was wired (tests) or when gating/skip applies.
func proposeClosePush(ctx context.Context, deps closeDeps, base string) error {
	if deps.pushConfirm == nil {
		return nil
	}
	skip, auto, err := pushflow.ResolveFlags(deps.push, deps.noPush, deps.cfg.Push.Propose)
	if err != nil {
		return err
	}
	return pushflow.Propose(ctx, deps.client, pushflow.Opts{
		Branch:      base,
		Skip:        skip,
		AutoConfirm: auto,
	}, deps.pushConfirm)
}
