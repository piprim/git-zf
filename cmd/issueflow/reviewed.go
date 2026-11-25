package issueflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
)

// BranchNameForIssue returns the name of issueSlug's most recent tracked
// branch, or "" when the issue has none (a ref in the old blob format counts
// as none).
func BranchNameForIssue(ctx context.Context, c *git.Client, issueSlug string) (string, error) {
	st, err := branch.Load(ctx, c, issueSlug)
	if (err == nil && st == nil) || errors.Is(err, branch.ErrLegacyBranch) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("list branches: %w", err)
	}

	if rows := branch.Rows([]branch.State{*st}, branch.StatusAll); len(rows) > 0 {
		return rows[0].BranchName, nil
	}

	return "", nil
}

// ReviewedBranch returns the branch st's round reviews: the one its request
// recorded, else (a request written before the field existed) the issue's
// most recent tracked branch.
func ReviewedBranch(ctx context.Context, c *git.Client, st *review.State) (string, error) {
	if st.Branch != "" {
		return st.Branch, nil
	}

	return BranchNameForIssue(ctx, c, st.Slug)
}

// RemoteRelation is how a local branch stands against its remote one.
type RemoteRelation int

const (
	RemoteNoRemote     RemoteRelation = iota // the clone has no remote
	RemoteNotPushed                          // a remote, but the branch is not on it
	RemoteLocalMissing                       // on the remote, no local branch
	RemoteSame
	RemoteLocalAhead
	RemoteBehind
	RemoteDiverged
)

// RemoteOf compares refs/heads/<branchName> with
// refs/remotes/<remote>/<branchName>. remoteRef is "<remote>/<branchName>"
// whenever the clone has a remote, even for RemoteNotPushed (messages name
// it); "" for RemoteNoRemote. It reads refs only: no fetch. A branch that is
// neither local nor on the remote is RemoteNotPushed.
func RemoteOf(ctx context.Context, c *git.Client, branchName string) (rel RemoteRelation, remoteRef string, err error) {
	remote, err := c.Remote()
	if err != nil {
		return RemoteNoRemote, "", fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return RemoteNoRemote, "", nil
	}

	remoteRef = remote + "/" + branchName
	fullRemote, fullLocal := "refs/remotes/"+remoteRef, "refs/heads/"+branchName
	remoteTip, err := c.ResolveRef(fullRemote)
	if err != nil {
		return RemoteNotPushed, remoteRef, nil //nolint:nilerr // no such ref: not pushed
	}
	localTip, err := c.ResolveRef(fullLocal)
	if err != nil {
		return RemoteLocalMissing, remoteRef, nil //nolint:nilerr // no such ref: no local branch
	}
	if localTip == remoteTip {
		return RemoteSame, remoteRef, nil
	}

	ahead, err := c.IsAncestor(ctx, fullRemote, fullLocal)
	if err != nil {
		return RemoteDiverged, remoteRef, err //nolint:wrapcheck // names the command
	}
	if ahead {
		return RemoteLocalAhead, remoteRef, nil
	}
	behind, err := c.IsAncestor(ctx, fullLocal, fullRemote)
	if err != nil {
		return RemoteDiverged, remoteRef, err //nolint:wrapcheck // names the command
	}
	if behind {
		return RemoteBehind, remoteRef, nil
	}

	return RemoteDiverged, remoteRef, nil
}

// What rule A refuses. The errors returned wrap these with the message the
// user reads.
var (
	ErrApprovalMissing      = errors.New("approved commit not in this clone")
	ErrApprovedAhead        = errors.New("approved commit is ahead of the local branch")
	ErrCommitsAfterApproval = errors.New("commits made after the approval")
	ErrNotApprovedBranch    = errors.New("local branch is not the approved one")
	ErrApprovalsDiverged    = errors.New("approvals name diverged commits")
)

// shortLen is how much of a SHA a message shows.
const shortLen = 10

func shortSHA(sha string) string { return sha[:min(len(sha), shortLen)] }

// ApprovedCommit returns the approved_sha that contains every other one of
// the round. Several approvals may name it (two reviewers on one commit): the
// caller checks signatures over all of those. Its errors carry the message
// the user reads: ErrApprovalMissing when a SHA is empty or not in the clone,
// ErrApprovalsDiverged when none contains the others.
func ApprovedCommit(ctx context.Context, c *git.Client, approvals []review.Approval) (approvedSHA string, err error) {
	if len(approvals) == 0 {
		return "", fmt.Errorf("%w: the review holds no approval", ErrApprovalMissing)
	}
	for _, a := range approvals {
		if !c.HasCommit(ctx, a.ApprovedSHA) {
			return "", fmt.Errorf("%w (%q): fetch the branch first", ErrApprovalMissing, shortSHA(a.ApprovedSHA))
		}
	}

	// A handful of approvals at most: every pair is cheap.
	for _, candidate := range approvals {
		containsAll := true
		for _, other := range approvals {
			ok, err := c.IsAncestor(ctx, other.ApprovedSHA, candidate.ApprovedSHA)
			if err != nil {
				return "", err //nolint:wrapcheck // names the command
			}
			if !ok {
				containsAll = false

				break
			}
		}
		if containsAll {
			return candidate.ApprovedSHA, nil
		}
	}

	return "", fmt.Errorf("%w: run git zf review request for a new round", ErrApprovalsDiverged)
}

// CheckApprovedTip applies rule A to branchName's local tip: nil means the
// tip is approvedSHA. Its errors wrap the sentinels above with the message.
func CheckApprovedTip(ctx context.Context, c *git.Client, branchName, approvedSHA string) error {
	short := shortSHA(approvedSHA)
	// First, and explicitly: IsAncestor errors on a missing object.
	if !c.HasCommit(ctx, approvedSHA) {
		return fmt.Errorf("%w (%s): fetch the branch first", ErrApprovalMissing, short)
	}

	tip, err := c.ResolveRef("refs/heads/" + branchName)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", branchName, err)
	}
	if tip.String() == approvedSHA {
		return nil
	}

	behind, err := c.IsAncestor(ctx, tip.String(), approvedSHA)
	if err != nil {
		return err //nolint:wrapcheck // names the command
	}
	if behind {
		return fmt.Errorf("%w %q: move to it with git merge --ff-only %s", ErrApprovedAhead, branchName, short)
	}
	after, err := c.IsAncestor(ctx, approvedSHA, tip.String())
	if err != nil {
		return err //nolint:wrapcheck // names the command
	}
	if after {
		return fmt.Errorf("%w of %s on %q: run git zf review request for a new round",
			ErrCommitsAfterApproval, short, branchName)
	}

	return fmt.Errorf("%w: %q is not at the approved commit %s", ErrNotApprovedBranch, branchName, short)
}
