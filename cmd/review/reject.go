package review

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/piprim/git-zf/cmd/pushflow"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/spf13/cobra"
)

func (r Review) getRejectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reject",
		Short: "Request changes on a review — unlocks the branch for the next iteration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return err
			}
			reason, given, err := rejectReasonFromFlags(cmd)
			if err != nil {
				return err
			}

			return runReviewRejectInteractive(ctx, deps, &huhReviewPrompter{}, reason, !given)
		},
	}
	cmd.Flags().StringP("message", "m", "", "reason for requesting changes (skips the prompt)")
	cmd.Flags().StringP("file", "F", "", "read the reason from a file, e.g. a Markdown note (skips the prompt)")
	pushflow.AddFlags(cmd)
	return cmd
}

var errReasonFlagsExclusive = errors.New("--message and --file are mutually exclusive")

// rejectReasonFromFlags resolves -m / -F into a trimmed reason. given reports
// whether either flag was passed at all, so an explicitly empty reason still
// skips the interactive prompt.
func rejectReasonFromFlags(cmd *cobra.Command) (reason string, given bool, err error) {
	flags := cmd.Flags()
	if flags.Changed("message") && flags.Changed("file") {
		return "", true, errReasonFlagsExclusive
	}
	if flags.Changed("file") {
		path, _ := flags.GetString("file")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", true, fmt.Errorf("read reason file: %w", err)
		}
		return strings.TrimSpace(string(b)), true, nil
	}
	message, _ := flags.GetString("message")
	return strings.TrimSpace(message), flags.Changed("message"), nil
}

// runReviewRejectInteractive picks the branch, then collects the reason from
// the prompter when promptReason is set (no -m / -F given).
func runReviewRejectInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, reason string, promptReason bool) error {
	branches, err := inReviewBranches(ctx, deps)
	if err != nil {
		return err
	}
	if len(branches) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select issue to reject:", branches, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	if promptReason {
		if reason, err = prompter.Text(ctx, "Reason for requesting changes (optional):"); err != nil {
			return fmt.Errorf("reason prompt: %w", err)
		}
	}

	round, err := runReviewReject(ctx, deps, picked.IssueSlug, reason)
	if err != nil {
		return err
	}
	if trackerBornIssue(ctx, deps, picked.IssueSlug) {
		if reason = strings.TrimSpace(reason); reason != "" {
			addTrackerComment(ctx, deps, picked.IssueSlug, round, reason)
		}
		applyTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	}
	return nil
}

// runReviewReject flips the review to changes_requested and returns the round
// that was rejected.
func runReviewReject(ctx context.Context, deps reviewDeps, issueSlug, reason string) (int, error) {
	reason = strings.TrimSpace(reason)

	d, err := recordReviewDecision(ctx, deps, issueSlug, reviewpkg.StatusChangesRequested, reason)
	if err != nil {
		return 0, err
	}

	reviewBranch, featureBranch, hasCommits := d.reviewBranch, d.featureBranch, d.hasCommits

	// The rejection is recorded at this point whatever the branch cleanup or
	// push proposal below does, so the reason is printed here rather than deferred.
	if reason != "" {
		fmt.Fprintf(deps.client.IO().Out, "Reason:\n%s\n", indentLines(reason))
	}

	// Handle review branch: keep if reviewer pushed commits, delete if empty.
	if d.branchExists && !hasCommits {
		if err := deps.client.DeleteLocalBranchSafe(ctx, reviewBranch, deps.cfg.Branch.Base); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
		}
		// Only push --delete when the branch actually exists on the remote.
		if deps.client.RemoteBranchExists(ctx, reviewBranch) {
			_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
		}
	}

	if hasCommits {
		if err := proposeReviewPush(ctx, deps, reviewBranch); err != nil {
			return 0, err
		}
	}

	// Use issueSlug as fallback when the issue has no tracked branch here.
	branchLabel := cmp.Or(featureBranch, issueSlug)

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q: changes requested (round %d). Feature branch %q unlocked.\n",
		issueSlug, d.round, branchLabel)

	if hasCommits {
		n, _ := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
		fmt.Fprintf(deps.client.IO().Out,
			"%s has %d reviewer commit(s). Inspect with:\n"+
				"  git log %s..%s\n"+
				"Cherry-pick, adapt, or discard as needed, then:\n"+
				"  git zf review request\n",
			reviewBranch, n, branchLabel, reviewBranch)
	}

	return d.round, nil
}

// indentLines prefixes every line with two spaces for display under a heading.
func indentLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
