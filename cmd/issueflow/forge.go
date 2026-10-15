package issueflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// ForgePullRequest returns the tracker's forge and branchName's open pull
// request on it. Without the capability, or without a pull request, the pull
// request is nil and the caller merges locally. A forge that cannot be asked
// is a warning and the same nil: the merge then runs locally, as it did
// before the forge existed.
func ForgePullRequest(
	ctx context.Context, t tracker.Tracker, errOut io.Writer, branchName string,
) (tracker.PullRequests, *tracker.PullRequest) {
	forge := tracker.PullRequestsOf(t)
	if forge == nil {
		return nil, nil
	}

	pr, err := forge.FindPullRequest(ctx, branchName)
	switch {
	case errors.Is(err, tracker.ErrNoPullRequest):
		return forge, nil
	case err != nil:
		fmt.Fprintf(errOut, "warning: pull request of %q not looked up, merging locally: %v\n", branchName, err)

		return nil, nil
	}

	return forge, &pr
}

// CheckPushed reports whether branchName's local tip is the remote's, after
// a fetch: the forge merges the branch it sees, so a close through it must
// not merge something else. Ahead, behind and absent are errors that say
// what to do.
func CheckPushed(ctx context.Context, c *git.Client, branchName string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return fmt.Errorf("branch %q has a pull request but this clone has no remote", branchName)
	}
	if err := c.Fetch(ctx); err != nil {
		return err //nolint:wrapcheck // names the remote
	}

	local, err := c.ResolveRef("refs/heads/" + branchName)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", branchName, err)
	}
	remoteRef := "refs/remotes/" + remote + "/" + branchName
	remoteTip, err := c.ResolveRef(remoteRef)
	if err != nil {
		return fmt.Errorf("branch %q is not on %s: push it first", branchName, remote)
	}
	if local == remoteTip {
		return nil
	}
	if ahead, _ := c.IsAncestor(ctx, remoteRef, "refs/heads/"+branchName); ahead {
		return fmt.Errorf("branch %q has unpushed commits: push it first", branchName)
	}

	return fmt.Errorf("branch %q is behind %s/%s: fetch and merge first", branchName, remote, branchName)
}
