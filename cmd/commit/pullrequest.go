package commit

import (
	"context"
	"fmt"
	"io"

	branchcmd "github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// forgeOf builds the tracker and returns its pull request capability: nil
// without a tracker, without the capability, or when the tracker cannot be
// built (a warning: the commit and the push do not depend on it).
func forgeOf(cfg *config.AppConfig, errOut io.Writer) tracker.PullRequests {
	if cfg.IssueTracker.Type == "" {
		return nil
	}
	t, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		fmt.Fprintf(errOut, "warning: init tracker: %v\n", err)

		return nil
	}

	return tracker.PullRequestsOf(t)
}

// pullRequestOffer is the push step's offer for branchName: nil without a
// tracker type, or when the branch is the base itself. The forge is built
// lazily by Open (a commit that never pushes never builds the tracker), which
// does nothing when it cannot be built. Open prefills from the branch chain
// and asks form, one yes/no question in production.
func pullRequestOffer(
	client *git.Client, cfg *config.AppConfig, branchName string, form branchcmd.PullRequestForm,
) *pushflow.PullRequestOffer {
	if cfg.IssueTracker.Type == "" {
		return nil
	}
	base := cfg.Branch.Base
	if base == "" {
		base, _ = client.DefaultBaseBranch()
	}
	if branchName == base {
		return nil
	}

	return &pushflow.PullRequestOffer{Open: func(ctx context.Context) error {
		forge := forgeOf(cfg, client.IO().Err)
		if forge == nil {
			return nil
		}
		draft := branchcmd.PullRequestDraftFor(ctx, client, cfg.Branch.Base, branchName)

		return branchcmd.OpenPullRequest(ctx, client.IO().Out, forge, branchName, draft, form)
	}}
}
