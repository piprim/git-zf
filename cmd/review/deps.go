package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
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

	client, err := cmdutil.NewClientForCmd(cmd, cfg)
	if err != nil {
		_ = s.Close()
		return reviewDeps{}, err
	}

	return reviewDeps{client: client, store: s, cfg: cfg}, nil
}

// inReviewBranches returns synthetic BranchRows for issues currently in_review,
// built from git refs rather than the local store. This works on fresh reviewer
// clones where the store is empty and no git zf issue start has been run.
func inReviewBranches(ctx context.Context, deps reviewDeps) ([]store.BranchRow, error) {
	// Fetch latest state (best-effort).
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}

	refs, err := deps.client.ListReviewRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list review refs: %w", err)
	}

	var result []store.BranchRow
	for issueID, ref := range refs {
		if ref.Status != string(store.ReviewStatusInReview) {
			continue
		}
		// Build a synthetic BranchRow from ref data. The reviewer's branch
		// follows the <IssueID>@review convention.
		result = append(result, store.BranchRow{
			IssueSlug:  issueID,
			BranchName: issueID + "@review",
			Title:      issueID,
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
	ref, _, refErr := deps.client.ReadReviewRef(ctx, issueSlug)
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
