package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

// reviewDeps bundles the long-lived dependencies shared by all review subcommands.
type reviewDeps struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
}

func buildReviewDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig) (reviewDeps, error) {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return reviewDeps{}, fmt.Errorf("open store: %w", err)
	}

	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		_ = s.Close()
		return reviewDeps{}, fmt.Errorf("not a git repository: %w", err)
	}

	if cfg.Branch.Remote != "" {
		client.SetRemote(cfg.Branch.Remote)
	}

	return reviewDeps{client: client, store: s, cfg: cfg}, nil
}

// inReviewBranches returns branches whose latest review status is in_review.
func inReviewBranches(ctx context.Context, deps reviewDeps) ([]store.BranchRow, error) {
	all, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	var result []store.BranchRow
	for _, b := range all {
		latest, err := deps.store.GetLatestReview(ctx, b.IssueSlug)
		if err != nil || latest == nil {
			continue
		}
		if latest.Status == store.ReviewStatusInReview {
			result = append(result, b)
		}
	}
	return result, nil
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
	if strings.HasSuffix(name, "@review") {
		return strings.TrimSuffix(name, "@review")
	}
	// Feature branch: "<issueSlug>@<type>@<slug>[@<variant>]"
	if b, err := branch.Parse(name); err == nil {
		return b.IssueID()
	}
	return ""
}
