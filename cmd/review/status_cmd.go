package review

import (
	"context"
	"fmt"
	"slices"

	"github.com/piprim/git-zf/branch"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the full review history for an issue",
		Args:  cobra.NoArgs,
		RunE: withDeps(r.appConfig, func(ctx context.Context, deps reviewDeps) error {
			return runReviewStatusInteractive(ctx, deps, &huhReviewPrompter{})
		}),
	}
}

func runReviewStatusInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	withHistory, err := reviewBranches(ctx, deps, func(st *reviewpkg.State) bool { return len(st.Rounds) > 0 })
	if err != nil {
		return err
	}

	if len(withHistory) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No review history found.")
		return nil
	}

	// Show the issue's own branch and title when its branch is tracked.
	for i := range withHistory {
		st, err := branch.Load(ctx, deps.client, withHistory[i].IssueSlug)
		if err != nil || st == nil {
			continue
		}
		if rows := branch.Rows([]branch.State{*st}, branch.StatusAll); len(rows) > 0 {
			withHistory[i] = rows[0]
		}
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to view review history:", withHistory, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewStatus(ctx, deps, picked.IssueSlug)
}

func runReviewStatus(ctx context.Context, deps reviewDeps, issueSlug string) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil || len(ref.Rounds) == 0 {
		fmt.Fprintf(deps.client.IO().Out, "No review history for issue %q.\n", issueSlug)
		return nil
	}
	for _, w := range ref.Warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	fmt.Fprintf(deps.client.IO().Out, "Review history for issue %q:\n", issueSlug)
	for _, row := range slices.Backward(ref.Rounds) { // newest round first
		resolved := "pending"
		if !row.ResolvedAt.IsZero() {
			resolved = row.ResolvedAt.Format("2006-01-02 15:04")
		}
		commits := ""
		if row.HasCommits {
			commits = " [reviewer pushed commits]"
		}
		reviewer := row.Reviewer
		if reviewer == "" {
			reviewer = "(awaiting)"
		}
		fmt.Fprintf(deps.client.IO().Out, "  Round %-2d  %-20s  reviewer: %-30s  opened: %s  resolved: %s%s\n",
			row.Round, row.Status, reviewer,
			row.OpenedAt.Format("2006-01-02 15:04"), resolved, commits)
	}

	// The state only carries the current round's reason; older ones stay in
	// the chain's reject ops.
	if ref.Comment != "" && ref.Status == reviewpkg.StatusChangesRequested {
		fmt.Fprintf(deps.client.IO().Out, "\nRound %d reason:\n%s\n", ref.Round, indentLines(ref.Comment))
	}

	if len(ref.Approvals) > 0 {
		fmt.Fprintf(deps.client.IO().Out, "\nRound %d approvals:\n", ref.Round)
		for _, a := range ref.Approvals {
			// author is what the op declares; for a verified approval, also
			// show who git says signed it.
			state := reviewpkg.SignatureState(ctx, deps.client, a.Commit)
			if state == reviewpkg.SigVerified {
				if signer, _ := deps.client.CommitSigner(ctx, a.Commit); signer != "" {
					state += ", signed by " + signer
				}
			}
			fmt.Fprintf(deps.client.IO().Out, "  %s  %s\n", a.Author, state)
		}
	}

	return nil
}
