package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

// reviewDeps bundles the long-lived dependencies shared by all review subcommands.
//
// client stays the concrete *git.Client: the review subcommands collectively
// touch ~27 of its methods (merge, ref read/write, remote, branch ops), so a
// role interface here would be a near-clone of the whole client — ceremony
// without decoupling.
type reviewDeps struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
	// tracker is the originating issue tracker, or nil when none is configured
	// (or it failed to initialise). A nil tracker disables the status-update
	// prompt — see maybeUpdateTrackerStatus.
	tracker tracker.Tracker
	// push proposal wiring (Phase 1). pushConfirm is nil in tests that build
	// reviewDeps literals, disabling the push step there.
	push, noPush bool
	pushConfirm  pushflow.ConfirmFunc
}

func buildReviewDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig) (reviewDeps, error) {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return reviewDeps{}, fmt.Errorf("open store: %w", err)
	}

	client, err := cmdutil.NewClientForCmd(cmd, cfg)
	if err != nil {
		_ = s.Close()
		return reviewDeps{}, err
	}

	deps := reviewDeps{client: client, store: s, cfg: cfg}
	deps.push, deps.noPush = pushflow.ReadFlags(cmd)
	deps.pushConfirm = pushflow.NewHuhConfirm()

	// Build the tracker so the review-lifecycle commands can offer to update the
	// originating issue status (mirrors buildCloseDeps). Non-fatal: warn and
	// continue with a nil tracker, which disables the prompt.
	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.tracker = t
		}
	}

	return deps, nil
}

// withDeps returns the RunE of a review subcommand: it builds the shared
// dependencies, runs fn, and closes the store.
func withDeps(
	cfg *config.AppConfig, fn func(ctx context.Context, deps reviewDeps) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		deps, err := buildReviewDeps(ctx, cmd, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = deps.store.Close() }()

		return fn(ctx, deps)
	}
}

// branchNameForIssue returns the name of issueSlug's most recent branch in the
// store, or "" when the store has none.
func branchNameForIssue(ctx context.Context, s *store.Store, issueSlug string) (string, error) {
	rows, err := s.ListBranchesByIssueSlugs(ctx, []string{issueSlug})
	if err != nil {
		return "", fmt.Errorf("list branches: %w", err)
	}

	return rows[issueSlug].BranchName, nil
}

// inReviewBranches returns synthetic BranchRows for issues currently in_review,
// built from git refs rather than the local store. This works on fresh reviewer
// clones where the store is empty and no git zf issue start has been run.
func inReviewBranches(ctx context.Context, deps reviewDeps) ([]store.BranchRow, error) {
	// Fetch latest state and push anything still local (best-effort).
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	states, warnings, err := reviewpkg.List(ctx, deps.client)
	if err != nil {
		return nil, fmt.Errorf("list review refs: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	var result []store.BranchRow
	for _, st := range states {
		if st.Closed || st.Status != reviewpkg.StatusInReview {
			continue
		}
		// Build a synthetic BranchRow from the review. The reviewer's branch
		// follows the <IssueID>@review convention.
		result = append(result, store.BranchRow{
			IssueSlug:  st.Slug,
			BranchName: branch.ReviewBranchName(st.Slug),
			Title:      st.Slug,
		})
	}
	return result, nil
}

// ensureReviewRecord returns a store ReviewRow for issueSlug that matches the
// current ref (the source of truth). It handles three cases:
//
//  1. Store empty / ref is ahead by round: inserts a new store record.
//  2. Store round matches ref round but status differs: updates the store row.
//  3. Store and ref agree: returns the existing row as-is.
//
// This allows approve/reject to work correctly even when the reviewer's store
// is stale (e.g. they rejected round 1 and the developer has since submitted
// round 2 — the store still shows round 1 / changes_requested).
func ensureReviewRecord(ctx context.Context, deps reviewDeps, issueSlug string) (*store.ReviewRow, error) {
	// Ref is always authoritative — read it first.
	ref, refErr := reviewpkg.Load(ctx, deps.client, issueSlug)
	if refErr != nil {
		return nil, fmt.Errorf("read review ref: %w", refErr)
	}
	if ref == nil {
		return nil, fmt.Errorf("no review found for issue %q — has the developer run `git zf review request`?", issueSlug)
	}

	latest, err := deps.store.GetLatestReview(ctx, issueSlug)
	if err != nil {
		return nil, fmt.Errorf("get latest review: %w", err)
	}

	// If store is current (same round as ref), reconcile status/reviewer and return.
	if latest != nil && latest.Round >= ref.Round {
		if store.ReviewStatus(ref.Status) != latest.Status {
			_ = deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatus(ref.Status), latest.HasCommits)
			latest.Status = store.ReviewStatus(ref.Status)
		}
		if ref.Reviewer != "" && latest.Reviewer == "" {
			_ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, ref.Reviewer)
			latest.Reviewer = ref.Reviewer
		}
		return latest, nil
	}

	// Store is behind (empty or stale round) — insert a record for the current
	// round. InsertReview auto-computes round as (existing count + 1).
	reviewer := ref.Reviewer
	if reviewer == "" {
		reviewer, _ = deps.client.ConfigUser(ctx)
	}
	inserted, insertErr := deps.store.InsertReview(ctx, issueSlug, reviewer)
	if insertErr != nil {
		return nil, fmt.Errorf("auto-register review record: %w", insertErr)
	}
	// InsertReview always sets status to in_review; sync from ref when different.
	if store.ReviewStatus(ref.Status) != inserted.Status {
		_ = deps.store.UpdateReviewStatus(ctx, inserted.ID, store.ReviewStatus(ref.Status), false)
		inserted.Status = store.ReviewStatus(ref.Status)
	}
	// Sync round if InsertReview computed the wrong round (store was empty
	// but ref is at round N > 1).
	if inserted.Round != ref.Round {
		if err := deps.store.SetReviewRound(ctx, inserted.ID, ref.Round); err == nil {
			inserted.Round = ref.Round
		}
	}
	return inserted, nil
}

// reviewDecision is what recordReviewDecision resolved while recording.
type reviewDecision struct {
	round         int
	reviewBranch  string // <issueSlug>@review
	featureBranch string // "" when the issue's branch is not in the local store
	branchExists  bool   // reviewBranch exists locally
	hasCommits    bool   // the reviewer pushed commits to reviewBranch
}

// recordReviewDecision flips the in-review issue to status (approved or
// changes requested). comment is the reviewer's reason, "" when approving.
//
// It writes and pushes the review ref FIRST (the ref is the source of truth)
// and updates the store after: a store failure leaves the ref correct, a ref
// failure leaves the store unchanged.
func recordReviewDecision(
	ctx context.Context, deps reviewDeps, issueSlug string, status store.ReviewStatus, comment string,
) (reviewDecision, error) {
	latest, err := ensureReviewRecord(ctx, deps, issueSlug)
	if err != nil {
		return reviewDecision{}, err
	}
	if latest.Status != store.ReviewStatusInReview {
		return reviewDecision{}, fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
	}

	d := reviewDecision{round: latest.Round, reviewBranch: branch.ReviewBranchName(issueSlug)}

	var branchErr error
	if d.featureBranch, branchErr = branchNameForIssue(ctx, deps.store, issueSlug); branchErr != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: %v (has_commits will be false)\n", branchErr)
	}

	// Detect reviewer commits on <issueSlug>@review.
	d.branchExists, _ = deps.client.BranchExists(d.reviewBranch)
	if d.branchExists && d.featureBranch != "" {
		n, countErr := deps.client.CommitsAhead(ctx, d.reviewBranch, d.featureBranch)
		d.hasCommits = countErr == nil && n > 0
	}

	st, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return reviewDecision{}, fmt.Errorf("read review ref: %w", err)
	}
	for _, w := range st.Warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	op := &reviewpkg.Op{Type: reviewpkg.OpReject, Comment: comment, HasCommits: d.hasCommits, Round: st.Round}
	if status == store.ReviewStatusApproved {
		// What the reviewer approved: their review branch when they have one,
		// else the commit the developer submitted.
		approved := st.FeatureSHA
		if d.branchExists {
			tip, tipErr := deps.client.ResolveRef("refs/heads/" + d.reviewBranch)
			if tipErr != nil {
				return reviewDecision{}, fmt.Errorf("resolve %s: %w", d.reviewBranch, tipErr)
			}
			approved = tip.String()
		}
		op = &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: approved, HasCommits: d.hasCommits, Round: st.Round}
	}

	// review.require-signed: the approval is signed whatever commit.gpgsign
	// says, and the command fails when it cannot be.
	sign := status == store.ReviewStatusApproved && deps.cfg.Review.RequireSigned
	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, sign); err != nil {
		return reviewDecision{}, fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, deps.client, issueSlug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	if err := deps.store.UpdateReviewStatus(ctx, latest.ID, status, d.hasCommits); err != nil {
		return reviewDecision{}, fmt.Errorf("update review status: %w", err)
	}

	return d, nil
}

// proposeReviewPush offers to push branch after a review transition. No-op when
// no confirm was wired (tests) or when gating/skip applies.
func proposeReviewPush(ctx context.Context, deps reviewDeps, branch string) error {
	if deps.pushConfirm == nil {
		return nil
	}
	skip, auto, err := pushflow.ResolveFlags(deps.push, deps.noPush, deps.cfg.Push.Propose)
	if err != nil {
		return err
	}
	return pushflow.Propose(ctx, deps.client, pushflow.Opts{
		Branch:      branch,
		Skip:        skip,
		AutoConfirm: auto,
	}, deps.pushConfirm)
}

// currentIssueSlug returns the IssueID of the current git branch, or "" if it
// cannot be determined. Works for both feature branches (42@feat@title → "42")
// and review branches (42@review → "42").
func currentIssueSlug(client *git.Client) string {
	name, err := client.CurrentBranch()
	if err != nil || name == "" {
		return ""
	}
	// Review branch: "<issueSlug>@review"
	if slug, ok := branch.CutReviewSuffix(name); ok {
		return slug
	}
	// Feature branch: "<issueSlug>@<type>@<slug>[@<variant>]"
	if b, err := branch.Parse(name); err == nil {
		return b.IssueID()
	}
	return ""
}
