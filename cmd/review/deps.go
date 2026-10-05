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

// inReviewBranches returns synthetic BranchRows for issues currently in_review.
func inReviewBranches(ctx context.Context, deps reviewDeps) ([]store.BranchRow, error) {
	return reviewBranches(ctx, deps, func(st *reviewpkg.State) bool {
		return !st.Closed && st.Status == reviewpkg.StatusInReview
	})
}

// reviewBranches returns a synthetic BranchRow for every review keep accepts,
// built from git refs rather than the local store. This works on fresh reviewer
// clones where the store is empty and no git zf issue start has been run.
func reviewBranches(
	ctx context.Context, deps reviewDeps, keep func(*reviewpkg.State) bool,
) ([]store.BranchRow, error) {
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
	for i := range states {
		st := &states[i]
		if !keep(st) {
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
// It writes the decision op on the review chain and pushes it. status is
// reviewpkg.StatusApproved or reviewpkg.StatusChangesRequested.
func recordReviewDecision(
	ctx context.Context, deps reviewDeps, issueSlug, status, comment string,
) (reviewDecision, error) {
	st, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return reviewDecision{}, fmt.Errorf("read review ref: %w", err)
	}
	if st == nil {
		return reviewDecision{}, fmt.Errorf(
			"no review found for issue %q — has the developer run `git zf review request`?", issueSlug)
	}
	for _, w := range st.Warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}
	if st.Status != reviewpkg.StatusInReview {
		return reviewDecision{}, fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, st.Status)
	}

	d := reviewDecision{round: st.Round, reviewBranch: branch.ReviewBranchName(issueSlug)}

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

	op := &reviewpkg.Op{Type: reviewpkg.OpReject, Comment: comment, HasCommits: d.hasCommits, Round: st.Round}
	if status == reviewpkg.StatusApproved {
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
	sign := status == reviewpkg.StatusApproved && deps.cfg.Review.RequireSigned
	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, sign); err != nil {
		return reviewDecision{}, fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, deps.client, issueSlug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
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
