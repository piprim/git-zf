package review

import (
	"cmp"
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Begin reviewing an issue (checks out its branch, brought to the remote one)",
		Long: `Run git fetch first, then pick an issue awaiting review. The branch the
developer submitted is checked out and brought to the remote one: created,
fast-forwarded, or, when your local copy is an old one that does not hold the
submitted commit, reset to it (the commit it was on is printed). Commits of
your own that you have not pushed yet are kept. Your identity is recorded on
the review ref as the reviewer.

Nothing is changed when the command refuses: the developer has not pushed
what they submitted, or the branch must move and the working tree has
uncommitted changes.

Review the code there. You may commit fixes on the branch and push them.
When you are done, run git zf review approve or git zf review reject.`,
		Args: cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewStartInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
}

func runReviewStartInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	// inReviewBranches reads the review chains: the reviewer does not need
	// the branch tracked or checked out.
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently awaiting review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to review:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewStart(ctx, deps, picked.IssueSlug)
}

// localNeed is what the reviewer's local branch needs to be the reviewed one.
type localNeed int

const (
	needNothing localNeed = iota
	needCreate            // no local branch: created from the remote one
	needFastForward
	needReset        // an old copy: it does not hold the submitted commit
	needWarnDiverged // the reviewer's own commits, and new ones on the remote
)

// shortLen is how much of a SHA a message shows.
const shortLen = 7

func short(sha string) string { return sha[:min(len(sha), shortLen)] }

// startNeed checks, on refs alone, that branchName can be reviewed, and
// returns what the local branch needs. remoteRef is "" in a clone without a
// remote.
func startNeed(
	ctx context.Context, c *git.Client, branchName, featureSHA string,
) (need localNeed, remoteRef string, err error) {
	rel, remoteRef, err := issueflow.RemoteOf(ctx, c, branchName)
	if err != nil {
		return needNothing, "", err //nolint:wrapcheck // names the command
	}
	// holds reports whether ref contains the submitted commit.
	holds := func(ref string) (bool, error) {
		if !c.HasCommit(ctx, featureSHA) {
			return false, nil
		}

		return c.IsAncestor(ctx, featureSHA, ref)
	}
	local := "refs/heads/" + branchName

	switch rel {
	case issueflow.RemoteNoRemote:
		if ok, err := holds(local); err != nil || !ok {
			return needNothing, "", cmp.Or(err, fmt.Errorf(
				"branch %q does not hold the submitted commit %s", branchName, short(featureSHA)))
		}

		return needNothing, "", nil
	case issueflow.RemoteNotPushed:
		return needNothing, remoteRef, fmt.Errorf(
			"branch %s does not exist: the developer has not pushed the submitted commit %s",
			remoteRef, short(featureSHA))
	}

	if ok, err := holds("refs/remotes/" + remoteRef); err != nil || !ok {
		return needNothing, remoteRef, cmp.Or(err, fmt.Errorf(
			"branch %s does not hold the submitted commit %s: the developer has not pushed it",
			remoteRef, short(featureSHA)))
	}

	switch rel {
	case issueflow.RemoteLocalMissing:
		return needCreate, remoteRef, nil
	case issueflow.RemoteBehind:
		return needFastForward, remoteRef, nil
	case issueflow.RemoteDiverged:
		ok, err := holds(local)
		if err != nil {
			return needNothing, remoteRef, err
		}
		if !ok {
			return needReset, remoteRef, nil
		}

		return needWarnDiverged, remoteRef, nil
	}

	// The remote tip, or the reviewer's own commits on top of it.
	return needNothing, remoteRef, nil
}

// bringLocal checks out branchName and applies what startNeed worked out.
func bringLocal(ctx context.Context, c *git.Client, need localNeed, branchName, remoteRef string) error {
	root, out := c.WorkingTreeRoot(), c.IO().Out
	old, _ := c.ResolveRef("refs/heads/" + branchName)

	// One git command per case: git checks the move from the current HEAD to
	// where the branch ends (a file in the way, a branch held by another
	// worktree) and, when it refuses, has changed nothing.
	var err error

	switch need {
	case needCreate:
		err = c.RunGitAt(ctx, root, "checkout", "-q", "-b", branchName, remoteRef)
	case needFastForward, needReset:
		err = c.RunGitAt(ctx, root, "checkout", "-q", "-B", branchName, remoteRef)
	case needNothing, needWarnDiverged:
		err = c.Checkout(ctx, branchName)
	}
	if err != nil {
		return fmt.Errorf("checkout %q: %w", branchName, err)
	}

	switch need {
	case needReset:
		fmt.Fprintf(out,
			"Your branch %q was an old copy: it did not hold the submitted commit. It is now at %s.\n"+
				"It was at %s; to bring that back: git branch <name> %s\n",
			branchName, remoteRef, short(old.String()), short(old.String()))
	case needWarnDiverged:
		fmt.Fprintf(out,
			"%s holds commits your branch lacks. Merge them before you approve:\n  git merge %s\n",
			remoteRef, remoteRef)
	case needNothing, needCreate, needFastForward:
	}

	return nil
}

// runReviewStart checks out the reviewed branch of issueSlug, brought to the
// remote one, and records the reviewer. Every check that can refuse runs
// before the first change. The caller is responsible for fetching review refs
// before calling this function.
func runReviewStart(ctx context.Context, deps reviewDeps, issueSlug string) error {
	c := deps.client
	ref, err := reviewpkg.Load(ctx, c, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil || ref.Closed {
		return fmt.Errorf("no review found for issue %q — has the developer run `git zf review request`?", issueSlug)
	}
	if ref.Status != reviewpkg.StatusInReview {
		return fmt.Errorf("issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	// The reviewer's clone may hold neither the commits nor, for a request
	// written before it named its branch, the branch chain.
	if remote, _ := c.Remote(); remote != "" {
		_ = c.Fetch(ctx)
	}
	if ref.Branch == "" {
		fetchBranchRefs(ctx, deps)
	}
	branchName, err := issueflow.ReviewedBranch(ctx, c, ref)
	if err != nil {
		return err //nolint:wrapcheck // names the step
	}
	if branchName == "" {
		return fmt.Errorf(
			"the review of issue %q names no branch: ask the developer to run `git zf review request` again", issueSlug)
	}

	need, remoteRef, err := startNeed(ctx, c, branchName, ref.FeatureSHA)
	if err != nil {
		return err
	}
	if need == needFastForward || need == needReset {
		if dirty, err := c.IsDirty(ctx); err != nil || dirty {
			return cmp.Or(err, fmt.Errorf(
				"branch %q must be brought to %s and the working tree has uncommitted changes: commit or stash them first",
				branchName, remoteRef))
		}
	}

	if err := bringLocal(ctx, c, need, branchName, remoteRef); err != nil {
		return err
	}

	recordReviewer(ctx, deps, ref)

	fmt.Fprintf(c.IO().Out,
		"On branch %q, submitted at %s (round %d).\n"+
			"Review the code, then run:\n"+
			"  git zf review approve\n"+
			"  git zf review reject\n",
		branchName, short(ref.FeatureSHA), ref.Round)

	return nil
}

// recordReviewer names this clone's user as the reviewer of ref's round: it
// writes the start op and pushes it, so the developer sees who started the
// review. No-op when the round already has a reviewer or git has no user
// configured. A failure is a warning: the review goes on without the name.
func recordReviewer(ctx context.Context, deps reviewDeps, ref *reviewpkg.State) {
	if reviewer, _ := deps.client.ConfigUser(ctx); reviewer == "" || ref.Reviewer != "" {
		return
	}

	startOp := &reviewpkg.Op{Type: reviewpkg.OpStart, Round: ref.Round}
	if err := reviewpkg.Append(ctx, deps.client, ref.Slug, startOp, false); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: record reviewer: %v\n", err)
	} else if err := reviewpkg.Push(ctx, deps.client, ref.Slug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push reviewer identity: %v\n", err)
	}
}
