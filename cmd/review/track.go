package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/cobra"
)

// TrackCmd returns the cobra command for both `git zf review track` and
// `git zf issue track`. Both command groups call this constructor — one
// implementation, two aliases in different namespaces.
func TrackCmd(appConfig *config.AppConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "track",
		Short: "Track the current branch with git-zf (for branches created with plain git checkout)",
		Long: `Register the current branch without creating a new branch: it is recorded
as in progress for its issue.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start'.

  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
  git zf review track
  git zf review request`,
		Args: cobra.NoArgs,
		RunE: withDeps(appConfig, runTrack),
	}
}

func runTrack(ctx context.Context, deps reviewDeps) error {
	currentBranch, err := deps.client.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if err := trackBranch(ctx, deps, currentBranch); errors.Is(err, ErrNotTrackable) {
		return fmt.Errorf(
			"current branch %q does not match the git-zf naming convention\n"+
				"(expected <ID>@<type>@<slug>)",
			currentBranch)
	} else if err != nil {
		return err
	}

	return nil
}

// ErrNotTrackable is TrackBranch's answer for a name that is not
// <id>@<type>@<slug>.
var ErrNotTrackable = errors.New("branch name is not <id>@<type>@<slug>")

// TrackBranch records name as review track does for the current branch: a
// feature branch is recorded in progress (a closed one reopened), any other
// name is ErrNotTrackable.
func TrackBranch(ctx context.Context, cfg *config.AppConfig, client *git.Client, name string) error {
	return trackBranch(ctx, newReviewDeps(client, cfg), name)
}

func trackBranch(ctx context.Context, deps reviewDeps, name string) error {
	if b, err := branch.Parse(name); err == nil {
		return runTrackDeveloper(ctx, deps, name, b)
	}

	return ErrNotTrackable
}

// runTrackDeveloper records a feature branch that was checked out with plain
// git as an in-progress branch of its issue.
func runTrackDeveloper(ctx context.Context, deps reviewDeps, branchName string, b *branch.Branch) error {
	// Idempotency: check if already tracked. The chains are fetched first:
	// another clone may have tracked or closed the branch. A closed branch is
	// tracked again below: checking it out and running track is how its owner
	// reopens a branch that a prune closed.
	fetchBranchRefs(ctx, deps)
	if _, e, err := branch.Find(ctx, deps.client, branchName); err != nil {
		return fmt.Errorf("list branches: %w", err)
	} else if e != nil && e.Status != branch.StatusClosed {
		fmt.Fprintf(deps.client.IO().Out,
			"Branch %q is already tracked (status: %s).\n", branchName, e.Status)

		return nil
	}

	// The title is derived from the branch name. A ref in the old blob format
	// is replaced, keeping its parent and tracker type.
	op := &branch.Op{Branch: branchName, BranchType: b.Type(), Title: branch.TitleFromName(branchName)}
	if err := branch.Start(ctx, deps.client, b.IssueID(), op); err != nil {
		return fmt.Errorf("track branch: %w", err)
	}
	if err := branch.Push(ctx, deps.client, b.IssueID()); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push branch ref: %v\n", err)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q is now tracked (issue %s, type %s).\n"+
			"You can run 'git zf review request'\n",
		branchName, b.IssueID(), b.Type())

	return nil
}
