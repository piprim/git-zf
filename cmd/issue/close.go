package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

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

	// invokedFrom is the working-tree root the command was typed in. client is
	// always anchored on the main tree; when invokedFrom is a linked worktree
	// that gets removed, a cd hint back to the main tree is printed.
	invokedFrom string

	// invokedBranch is the branch checked out in the tree the command was typed
	// in ("" when that tree has a detached HEAD). client is anchored on the main
	// tree, so client.CurrentBranch() reports the MAIN checkout's branch — the
	// wrong pre-selection for the branch picker and the wrong subject for the
	// `branch merge` nudge whenever the user stands inside a linked worktree.
	invokedBranch string

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

	client, invokedFrom, err := cmdutil.NewMainClientForCmd(cmd, cfg)
	if err != nil {
		_ = s.Close()

		return closeDeps{}, err
	}

	deps := closeDeps{
		client:        client,
		store:         s,
		cfg:           cfg,
		invokedFrom:   invokedFrom,
		invokedBranch: invokedBranchFor(ctx, client, invokedFrom),
	}

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

// invokedBranchFor resolves the branch checked out in the working tree the
// command was typed in. client is anchored on the MAIN tree (see
// cmdutil.NewMainClientForCmd), so its own CurrentBranch() answers for the main
// checkout, not for the linked worktree the user may be standing in. Resolution
// is by worktree listing: the entry whose Path is the same directory as
// invokedFrom. Falls back to client.CurrentBranch() when invokedFrom is empty
// or matches no entry, and returns "" when the invoking tree is on a detached
// HEAD (no branch to pre-select).
func invokedBranchFor(ctx context.Context, client *git.Client, invokedFrom string) string {
	if invokedFrom != "" {
		if list, err := client.Worktrees(ctx); err == nil {
			for i := range list {
				if git.SamePath(list[i].Path, invokedFrom) {
					return list[i].Branch
				}
			}
		}
	}

	cur, err := client.CurrentBranch()
	if err != nil {
		return ""
	}

	return cur
}

// ErrBranchLockedForReview is returned by reviewPreflight when the branch is
// locked because a review is in progress. Use errors.Is to detect it.
var ErrBranchLockedForReview = errors.New("branch locked for review")

// ErrReviewChangesRequested is returned by reviewPreflight when the reviewer
// has requested changes. Use errors.Is to detect it.
var ErrReviewChangesRequested = errors.New("reviewer requested changes")

// ErrReviewSyncNeeded is returned by reviewPreflight when reviewer commits on
// the @review branch conflict with the feature branch (or the tree is dirty)
// and the developer must run `git zf review sync` before closing.
var ErrReviewSyncNeeded = errors.New("review sync needed")

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
	picked, err := getPickedBranch(ctx, deps.store, deps.client, deps.invokedBranch, prompter)
	if err != nil {
		return err
	}

	if picked == nil {
		return nil
	}

	// A ref-derived pick (reviewer/teammate closing a branch they never started)
	// has IssueID == 0: materialize the feature branch from origin so the rest
	// of the flow — reviewPreflight and the merge both need a local feature
	// branch — behaves exactly as for a locally-started branch. Store tracking
	// is deferred until the merge commit lands (see TrackCandidate below) so an
	// aborted close inserts no spurious in-progress rows.
	createdBranch := false
	if picked.IssueID == 0 {
		created, err := issueflow.MaterializeBranch(ctx, deps.client, *picked)
		if err != nil {
			return err
		}
		createdBranch = created
	}

	// Roll back the just-materialized branch when the close ends before the
	// merge commit lands (conflict dry-run, cancel at the confirm prompt, any
	// preflight error), so an aborted reviewer-initiated close leaves the
	// clone exactly as it found it. The abort may have left HEAD on the
	// materialized branch (rebase/classic preflight checkout, review
	// fast-forward), so delete via the base-switching helper; the abort may
	// also stem from a canceled context, so detach the cleanup from it.
	mergeCommitted := false
	defer func() {
		if !createdBranch || mergeCommitted {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		if delErr := deps.client.DeleteLocalBranchSafe(cleanupCtx, picked.BranchName, true, deps.cfg.Branch.Base); delErr != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: rollback materialized branch %q: %v\n",
				picked.BranchName, delErr)

			return
		}
		fmt.Fprintf(deps.client.IO().Err, "Rolled back: materialized branch %q removed\n", picked.BranchName)
	}()

	// A branch started as a worktree is checked out there, and git refuses to
	// check it out (or delete it) from the main tree. Hand the engine a client
	// on that worktree; the worktree itself is only touched after the commit.
	srcClient, wt, err := mergeflow.SourceTree(ctx, deps.client, picked.BranchName)
	if err != nil {
		return err
	}

	reviewCleanup, err := reviewPreflight(ctx, deps, picked, srcClient)
	if err != nil {
		return err
	}

	// The destructive review cleanup (local/remote review branch + review ref)
	// runs only once the merge commit has landed. Until then those refs are the
	// only durable home of the reviewer commits reviewPreflight just merged into
	// the feature branch — an abort must leave them intact so the rollback above
	// can safely force-delete the materialized branch and a retry can re-run the
	// incorporation from scratch.
	defer func() {
		if mergeCommitted && reviewCleanup != nil {
			reviewCleanup(context.WithoutCancel(ctx))
		}
	}()

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
			format := "issue %q has open sub-tasks (list unavailable: %w) — close all sub-tasks before closing the parent"
			return fmt.Errorf(format, picked.IssueSlug, listErr)
		}

		return fmt.Errorf("issue %q has open sub-tasks: %v — close all sub-tasks before closing the parent",
			picked.IssueSlug, children)
	}

	// The issue-flavored commit-message prefill is the one thing the shared
	// engine cannot know: it is built from this issue's slug/type/title.
	prefill := func(s commit.MergeStrategy, sourceTip, targetTip git.Hash) map[string]any {
		return commit.IssueHint{
			IssueID:      picked.IssueSlug,
			BranchType:   picked.Type,
			IssueSubject: picked.Title,
			Closing:      &commit.IssueCloseInfo{FromHash: sourceTip, ToHash: targetTip, Strategy: s},
		}.Prefill(deps.cfg.CommitMessage)
	}

	res, err := mergeflow.Run(ctx, deps.client, mergeflow.Params{
		Source:             picked.BranchName,
		Target:             base,
		SourceMaterialized: createdBranch,
		SourceClient:       srcClient,
	}, prompter, prefill)
	if err != nil {
		return err
	}

	if res.FastForwardDeferred {
		// The commit landed on the feature branch — keep it, and track the
		// ref-derived candidate so the store mirrors a locally-started
		// branch awaiting its manual fast-forward.
		mergeCommitted = true
		picked = trackPickedCandidate(ctx, deps, picked)

		return nil
	}

	if res.Aborted {
		fmt.Fprintln(deps.client.IO().Out, "Aborted.")

		return nil
	}

	// The merge commit landed — track the ref-derived candidate now (deferred
	// from the pick) so updateClosedStatus below has a real IssueID to mark
	// merged. Failures past this point are non-fatal: the merge is committed,
	// so warn and continue like the rest of the post-merge bookkeeping.
	mergeCommitted = true
	picked = trackPickedCandidate(ctx, deps, picked)

	updateClosedStatus(ctx, deps, picked, prompter)

	worktreeRemoved := false
	if wt != nil {
		worktreeRemoved, err = mergeflow.RemoveWorktreeStep(
			ctx, deps.client, wt, deps.invokedFrom, prompter.ConfirmRemoveWorktree)
		if err != nil {
			return err
		}
	}

	if err := doDeleteBranch(ctx, deps.client, picked, res.Strategy, prompter, wt != nil && !worktreeRemoved); err != nil {
		return err
	}

	fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return proposeClosePush(ctx, deps, base)
}

// trackPickedCandidate promotes a ref-derived pick (IssueID == 0) into tracked
// store rows and returns the row carrying the real IssueID. Called only once
// the merge commit has landed, so a tracking failure is a warning, not an
// abort — picked is returned unchanged, the operator can re-track manually,
// and updateClosedStatus degrades to per-step warnings on the untracked row.
// No-op for picks that were already tracked (TrackCandidate returns them
// unchanged).
func trackPickedCandidate(ctx context.Context, deps closeDeps, picked *store.BranchRow) *store.BranchRow {
	promoted, err := issueflow.TrackCandidate(ctx, deps.store, deps.client, *picked)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: track branch %q: %v\n", picked.BranchName, err)

		return picked
	}

	return &promoted
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
			format := "--base %q is the branch being closed; choose a different merge target"
			return "", fmt.Errorf(format, deps.baseOverride)
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
// local branch except the one being closed and any @review branch (review
// branches are merge sources, never targets — and one may still exist here
// because its cleanup is deferred until the merge commit lands), with
// defaultBase guaranteed present (it may be a remote-only parent integration
// branch absent from locals). defaultBase is placed first so it leads the
// picker list.
func mergeTargetCandidates(locals []string, closing, defaultBase string) []string {
	out := make([]string, 0, len(locals)+1)
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || name == closing || seen[name] || branch.IsReviewBranch(name) {
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
// incorporate them.
//
// The destructive cleanup (local/remote review branch + review ref) is NOT
// performed here: it is returned as a closure (nil when there is nothing to
// clean up) that runClose invokes only after the merge commit lands. Running
// it earlier would destroy the only remaining source of the just-incorporated
// reviewer commits if the close subsequently aborts and rolls back the
// feature branch.
//
// The git ref (refs/zf/reviews/<IssueID>) is the source of truth. The local
// store is a cache that may lag behind the reviewer's machine. reviewPreflight
// always fetches and reads the ref first so the developer never has to run a
// manual git fetch before closing.
func reviewPreflight(
	ctx context.Context, deps closeDeps, picked *store.BranchRow, src *git.Client,
) (func(context.Context), error) {
	// The feature branch is checked out in src when it lives in a linked
	// worktree; the fast-forward / merge below must run there.
	tree := deps.client
	if src != nil {
		tree = src
	}

	// Fetch review refs (best-effort) so we see the reviewer's latest decision
	// even if the developer has not fetched since submitting for review.
	_ = deps.client.FetchReviewRefs(ctx)

	// Read the ref — authoritative source of truth.
	ref, _, refErr := deps.client.ReadReviewRef(ctx, picked.IssueSlug)
	if refErr != nil {
		return nil, fmt.Errorf("read review ref: %w", refErr)
	}

	if ref == nil {
		// No active review ref — either no review was submitted, or it was
		// already cleaned up after a previous close. Proceed.
		return nil, nil
	}

	// Reconcile local store from ref so downstream store reads are consistent.
	if latest, _ := deps.store.GetLatestReview(ctx, picked.IssueSlug); latest != nil {
		if store.ReviewStatus(ref.Status) != latest.Status {
			_ = deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatus(ref.Status), latest.HasCommits)
		}
	}

	switch store.ReviewStatus(ref.Status) {
	case store.ReviewStatusInReview:
		return nil, fmt.Errorf(
			"branch %q is locked for review (issue %q, round %d) — awaiting reviewer decision.\n"+
				"Run `git zf review list` to check review status: %w",
			picked.BranchName, picked.IssueSlug, ref.Round, ErrBranchLockedForReview)

	case store.ReviewStatusChangesRequested:
		return nil, fmt.Errorf(
			"reviewer requested changes on issue %q (round %d).\n"+
				"Address feedback and run `git zf review request` for round %d: %w",
			picked.IssueSlug, ref.Round, ref.Round+1, ErrReviewChangesRequested)

	case store.ReviewStatusApproved:
		reviewBranch := branch.ReviewBranchName(picked.IssueSlug)

		// Resolve pending reviewer commits through the shared helper so a stale
		// local <slug>@review never shadows a fresher origin/<slug>@review
		// (reviewer pushed or force-pushed after the developer's checkout).
		pending, pendErr := issueflow.PendingReviewCommits(ctx, deps.client, picked.IssueSlug, picked.BranchName)
		if pendErr != nil {
			return nil, fmt.Errorf("detect pending review commits: %w", pendErr)
		}

		localExists, _ := deps.client.BranchExists(reviewBranch)
		remoteTrackingExists := false
		if remote, _ := deps.client.Remote(); remote != "" {
			if _, err := deps.client.ResolveRef("refs/remotes/" + remote + "/" + reviewBranch); err == nil {
				remoteTrackingExists = true
			}
		}

		if pending != nil {
			m, mErr := deps.client.CommitsAhead(ctx, picked.BranchName, pending.EffectiveRef)
			if mErr != nil {
				return nil, fmt.Errorf("check review divergence: %w", mErr)
			}
			switch {
			case m == 0:
				// Feature branch has not moved — plain fast-forward as before.
				fmt.Fprintf(deps.client.IO().Out,
					"Incorporating %d reviewer commit(s) from %s into %s...\n",
					pending.Commits, pending.EffectiveRef, picked.BranchName)
				if err := tree.FastForwardOnly(ctx, pending.EffectiveRef, picked.BranchName); err != nil {
					return nil, fmt.Errorf("fast-forward %s to %s: %w", picked.BranchName, pending.EffectiveRef, err)
				}
			default:
				// Diverged: dry-run first; close never leaves MERGE_HEAD behind.
				conflicts, dryErr := deps.client.MergeDryRun(ctx, pending.EffectiveRef, picked.BranchName)
				if dryErr != nil {
					return nil, fmt.Errorf("review merge dry-run: %w", dryErr)
				}
				if len(conflicts) > 0 {
					return nil, fmt.Errorf(
						"reviewer commits on %s conflict with %q (%s).\n"+
							"Run 'git zf review sync', resolve the conflicts, then close: %w",
						pending.EffectiveRef, picked.BranchName, strings.Join(conflicts, ", "), ErrReviewSyncNeeded)
				}
				if dirty, dErr := tree.IsDirty(ctx); dErr == nil && dirty {
					return nil, fmt.Errorf(
						"working tree has uncommitted changes — cannot incorporate %s.\n"+
							"Run 'git stash', then retry the close: %w", pending.EffectiveRef, ErrReviewSyncNeeded)
				}
				fmt.Fprintf(deps.client.IO().Out,
					"Merging %d reviewer commit(s) from %s into %s...\n",
					pending.Commits, pending.EffectiveRef, picked.BranchName)
				if err := tree.MergeForward(ctx, pending.EffectiveRef, picked.BranchName); err != nil {
					_ = tree.AbortMerge(ctx)
					return nil, fmt.Errorf("merge %s into %s: %w", pending.EffectiveRef, picked.BranchName, err)
				}
			}
		}

		// Cleanup mirrors the pre-deferral behavior: delete the local review
		// branch when it exists, push a remote delete when any review branch was
		// known, and always drop the review ref. Deferred to the caller (post
		// merge-commit) so an aborted close keeps the reviewer commits' source.
		return func(ctx context.Context) {
			if localExists {
				if err := deps.client.DeleteLocalBranchSafe(ctx, reviewBranch, true, deps.cfg.Branch.Base); err != nil {
					fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
				}
			}
			if localExists || remoteTrackingExists {
				_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
			}
			// Always clean up the review ref (local + remote) on close, regardless
			// of whether a review branch existed.
			_ = deps.client.DeleteReviewRef(ctx, picked.IssueSlug)
		}, nil
	}

	return nil, nil
}

// getPickedBranch returns (nil, nil) when there are no closable branches
// (neither store-tracked in-progress rows nor ref-derived candidates).
//
// invokedBranch is the branch checked out in the tree the command was typed in
// (closeDeps.invokedBranch). It drives the picker's pre-selection and the
// `branch merge` nudge; client is the main-tree client, so its CurrentBranch()
// would name the wrong branch when the user is inside a linked worktree.
func getPickedBranch(
	ctx context.Context,
	s *store.Store,
	client *git.Client, invokedBranch string, prompter ClosePrompter) (*store.BranchRow, error) {
	// A branch closed in a sibling clone carries Merged=true on its
	// refs/zf/branches/<slug> ref (pushed by updateClosedStatus) but may still
	// show in_progress in this clone's store. Reconcile from the refs first so
	// the picker never offers a branch that was already closed elsewhere.
	issueflow.ReconcileMergedFromRefs(ctx, s, client)

	branches, err := issueflow.CloseCandidates(ctx, s, client)
	if err != nil {
		return nil, fmt.Errorf("list close candidates: %w", err)
	}

	if len(branches) == 0 {
		// Best-effort nudge: a user standing on a non-issue branch that still
		// has unmerged commits almost certainly wants `branch merge`, not
		// `issue close`. Any probe error degrades to the plain message.
		if cur := invokedBranch; cur != "" {
			// Only nudge when cur is genuinely NOT an issue branch. An issue
			// branch with no store row and no ref (fresh local branch, or a
			// reviewer clone pre-reconciliation) also lands here with zero
			// candidates — but the nudge's claim would be false, and `branch
			// merge` refuses issue branches on the same branch.Parse gate,
			// bouncing the user straight back to `issue close`. Match that gate.
			if _, parseErr := branch.Parse(cur); parseErr != nil {
				if base, baseErr := client.DefaultBaseBranch(); baseErr == nil {
					if merged, mErr := client.IsMergedInto(cur, base); mErr == nil && !merged {
						fmt.Fprintf(client.IO().Out,
							"You're on %q, which has unmerged commits but isn't an issue branch.\n"+
								"To merge it:  git zf branch merge\n", cur)

						return nil, nil
					}
				}
			}
		}

		fmt.Fprintln(client.IO().Out, "No branches available to close.")

		return nil, nil
	}

	picked, err := prompter.PickBranch(ctx, branches, invokedBranch)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return nil, err
	}

	return picked, nil
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

// doDeleteBranch offers to delete the merged feature branch locally and on the
// remote (mirrors branch merge). heldByWorktree is true when the branch is
// still checked out in a linked worktree that was kept: git would refuse the
// local delete, so it is skipped with a warning while the remote delete still
// runs.
func doDeleteBranch(
	ctx context.Context,
	c *git.Client,
	picked *store.BranchRow,
	strategy commit.MergeStrategy, prompter ClosePrompter, heldByWorktree bool) error {
	shouldDelete, err := prompter.ConfirmDeleteBranch(ctx, picked.BranchName)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return err
	}

	if !shouldDelete {
		return nil
	}

	if heldByWorktree {
		fmt.Fprintf(c.IO().Err,
			"warning: branch %q is still checked out in its worktree; delete it after `git worktree remove`\n",
			picked.BranchName)
	} else {
		force := strategy == commit.MergeStrategySquash || strategy == commit.MergeStrategyRebase
		if err := c.DeleteLocalBranch(ctx, picked.BranchName, force); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: delete branch: %v\n", err)
		}
	}

	if c.RemoteBranchExists(ctx, picked.BranchName) {
		if err := c.DeleteRemoteBranch(ctx, picked.BranchName); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: delete remote branch: %v\n", err)
		}
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
