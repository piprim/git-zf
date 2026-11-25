package review

import (
	"cmp"
	"context"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	reviewpkg "github.com/piprim/git-zf/review"
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
	client, err := cmdutil.NewClientForCmd(cmd, cfg)
	if err != nil {
		return reviewDeps{}, err
	}

	deps := newReviewDeps(client, cfg)
	deps.push, deps.noPush = pushflow.ReadFlags(cmd)
	deps.pushConfirm = pushflow.NewHuhConfirm()

	return deps, nil
}

// newReviewDeps is reviewDeps without the push wiring (zero: no push step).
// The tracker lets the review-lifecycle commands offer to update the
// originating issue status (mirrors buildCloseDeps). Non-fatal: warn and
// continue with a nil tracker, which disables the prompt.
func newReviewDeps(client *git.Client, cfg *config.AppConfig) reviewDeps {
	deps := reviewDeps{client: client, cfg: cfg}
	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.tracker = t
		}
	}

	return deps
}

// withDeps returns the RunE of a review subcommand: it builds the shared
// dependencies and runs fn.
func withDeps(
	cfg *config.AppConfig, fn func(ctx context.Context, deps reviewDeps) error,
) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		deps, err := buildReviewDeps(ctx, cmd, cfg)
		if err != nil {
			return err
		}
		return fn(ctx, deps)
	}
}

// fetchBranchRefs brings the remote's branch chains, best-effort: what another
// clone started or closed is only there until fetched.
func fetchBranchRefs(ctx context.Context, deps reviewDeps) {
	if err := branch.Fetch(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch branch refs: %v\n", err)
	}
}

// inReviewBranches returns synthetic BranchRows for issues currently in_review.
func inReviewBranches(ctx context.Context, deps reviewDeps) ([]branch.Row, error) {
	return reviewBranches(ctx, deps, func(st *reviewpkg.State) bool {
		return !st.Closed && st.Status == reviewpkg.StatusInReview
	})
}

// reviewBranches returns a synthetic BranchRow for every review keep accepts,
// built from the review chains: a reviewer's clone may know the issue through
// nothing else.
func reviewBranches(
	ctx context.Context, deps reviewDeps, keep func(*reviewpkg.State) bool,
) ([]branch.Row, error) {
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

	var result []branch.Row
	for i := range states {
		st := &states[i]
		if !keep(st) {
			continue
		}
		// A row built from the review: the reviewer needs no tracked branch.
		result = append(result, branch.Row{IssueSlug: st.Slug, BranchName: st.Branch, Title: st.Slug})
	}
	if len(result) == 0 {
		return nil, nil
	}

	titles, names := issueTitles(ctx, deps)
	for i := range result {
		slug := result[i].IssueSlug
		result[i].Title = cmp.Or(titles[slug], slug)
		// A request written before it named its branch.
		result[i].BranchName = cmp.Or(result[i].BranchName, names[slug])
	}
	return result, nil
}

// issueTitles returns the title and the most recent branch name of every
// issue that has a branch chain, by slug, after a best-effort fetch of the
// chains: the review chains hold no title. An issue whose chain has none gets
// the one its first branch name gives.
func issueTitles(ctx context.Context, deps reviewDeps) (titles, names map[string]string) {
	fetchBranchRefs(ctx, deps)

	titles, names = map[string]string{}, map[string]string{}
	states, warnings, err := branch.List(ctx, deps.client)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: %v\n", err)
		return titles, names
	}
	for _, w := range warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	for i := range states {
		st := &states[i]
		title := st.Title
		if title == "" && len(st.Entries) > 0 {
			title = branch.TitleFromName(st.Entries[0].Name)
		}
		titles[st.Slug] = title
		if rows := branch.Rows([]branch.State{*st}, branch.StatusAll); len(rows) > 0 {
			names[st.Slug] = rows[0].BranchName
		}
	}
	return titles, names
}

// reviewDecision is what recordReviewDecision resolved while recording.
type reviewDecision struct {
	round         int
	featureBranch string // the reviewed branch, "" when none can be named
	featureSHA    string // the commit the developer submitted
	hasCommits    bool   // the local branch holds commits on top of featureSHA
}

// reviewedTip returns the local tip of branchName, or "" when the branch is
// missing or does not hold featureSHA (an old copy).
func reviewedTip(ctx context.Context, c *git.Client, branchName, featureSHA string) string {
	if branchName == "" || !c.HasCommit(ctx, featureSHA) {
		return ""
	}
	tip, err := c.ResolveRef("refs/heads/" + branchName)
	if err != nil {
		return ""
	}
	if ok, _ := c.IsAncestor(ctx, featureSHA, tip.String()); !ok {
		return ""
	}

	return tip.String()
}

// checkApprovable refuses an approval that the close would refuse: tip is ""
// (no local branch holding the submitted commit), or the remote branch holds
// commits the local one lacks.
func checkApprovable(ctx context.Context, c *git.Client, st *reviewpkg.State, branchName, tip string) error {
	if tip == "" {
		return fmt.Errorf(
			"no local branch holds the submitted commit %s of issue %q: run git zf review start",
			short(st.FeatureSHA), st.Slug)
	}

	rel, remoteRef, err := issueflow.RemoteOf(ctx, c, branchName)
	if err != nil {
		return err //nolint:wrapcheck // names the command
	}
	if rel == issueflow.RemoteBehind || rel == issueflow.RemoteDiverged {
		return fmt.Errorf(
			"%s holds commits your branch %q lacks: the approval would not cover them. "+
				"Take them (git merge %s), look at them, then approve",
			remoteRef, branchName, remoteRef)
	}

	return nil
}

// recordReviewDecision flips the in-review issue to status (approved or
// changes requested). comment is the reviewer's reason, "" when approving.
//
// It writes the decision op on the review chain and pushes it. status is
// reviewpkg.StatusApproved or reviewpkg.StatusChangesRequested. An approval
// needs the local reviewed branch, up to date with the remote one; a
// rejection needs no branch.
func recordReviewDecision(
	ctx context.Context, deps reviewDeps, issueSlug, status, comment string,
) (reviewDecision, error) {
	c := deps.client
	st, err := reviewpkg.Load(ctx, c, issueSlug)
	if err != nil {
		return reviewDecision{}, fmt.Errorf("read review ref: %w", err)
	}
	if st == nil {
		return reviewDecision{}, fmt.Errorf(
			"no review found for issue %q — has the developer run `git zf review request`?", issueSlug)
	}
	for _, w := range st.Warnings {
		fmt.Fprintln(c.IO().Err, w)
	}
	if st.Status != reviewpkg.StatusInReview {
		return reviewDecision{}, fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, st.Status)
	}

	approving := status == reviewpkg.StatusApproved
	if approving {
		// Best effort: unreachable, the check below uses the refs the clone has.
		stop := c.IO().Spin("Fetching the remote")
		err := c.Fetch(ctx)
		stop()
		if err != nil {
			fmt.Fprintf(c.IO().Err, "warning: %v\n", err)
		}
	}

	d := reviewDecision{round: st.Round, featureSHA: st.FeatureSHA}

	var branchErr error
	if d.featureBranch, branchErr = issueflow.ReviewedBranch(ctx, c, st); branchErr != nil {
		fmt.Fprintf(c.IO().Err, "warning: %v (has_commits will be false)\n", branchErr)
	}
	tip := reviewedTip(ctx, c, d.featureBranch, st.FeatureSHA)
	d.hasCommits = tip != "" && tip != st.FeatureSHA

	op := &reviewpkg.Op{Type: reviewpkg.OpReject, Comment: comment, HasCommits: d.hasCommits, Round: st.Round}
	if approving {
		if err := checkApprovable(ctx, c, st, d.featureBranch, tip); err != nil {
			return reviewDecision{}, err
		}
		op = &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: tip, HasCommits: d.hasCommits, Round: st.Round}
	}

	// review.require-signed: the approval is signed whatever commit.gpgsign
	// says, and the command fails when it cannot be.
	sign := approving && deps.cfg.Review.RequireSigned
	if err := reviewpkg.Append(ctx, c, issueSlug, op, sign); err != nil {
		return reviewDecision{}, fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, c, issueSlug); err != nil {
		fmt.Fprintf(c.IO().Err, "warning: push review ref: %v\n", err)
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
// cannot be determined (42@feat@title → "42").
func currentIssueSlug(client *git.Client) string {
	name, err := client.CurrentBranch()
	if err != nil || name == "" {
		return ""
	}
	// "<issueSlug>@<type>@<slug>[@<variant>]"
	if b, err := branch.Parse(name); err == nil {
		return b.IssueID()
	}
	return ""
}
