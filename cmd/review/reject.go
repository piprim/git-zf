package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
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
			defer func() { _ = deps.store.Close() }()

			reason, given, err := rejectReasonFromFlags(cmd)
			if err != nil {
				return err
			}
			return runReviewRejectInteractive(ctx, deps, newHuhReviewPrompter(), reason, !given)
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

	if err := runReviewReject(ctx, deps, picked.IssueSlug, reason); err != nil {
		return err
	}
	maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)
	return nil
}

func runReviewReject(ctx context.Context, deps reviewDeps, issueSlug, reason string) error {
	reason = strings.TrimSpace(reason)
	latest, err := ensureReviewRecord(ctx, deps, issueSlug)
	if err != nil {
		return err
	}
	if latest.Status != store.ReviewStatusInReview {
		return fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
	}

	// Detect reviewer commits on <issueSlug>@review.
	reviewBranch := branch.ReviewBranchName(issueSlug)
	var featureBranch string

	branches, branchErr := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if branchErr != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: list branches: %v (has_commits will be false)\n", branchErr)
	}
	for _, b := range branches {
		if b.IssueSlug == issueSlug {
			featureBranch = b.BranchName
			break
		}
	}

	hasCommits := false
	reviewBranchExists := false
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		reviewBranchExists = true
		if featureBranch != "" {
			n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
			if countErr == nil && n > 0 {
				hasCommits = true
			}
		}
	}

	// Write and push the ref FIRST (ref is the source of truth).
	currentRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	featureSHA := ""
	if currentRef != nil {
		featureSHA = currentRef.FeatureSHA
	}

	reviewer := ""
	if currentRef != nil {
		reviewer = currentRef.Reviewer
	}
	newRef := git.ReviewRef{
		Status:     string(store.ReviewStatusChangesRequested),
		Round:      latest.Round,
		FeatureSHA: featureSHA,
		Reviewer:   reviewer,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Comment:    reason,
	}

	if _, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}
	// expectedOldSHA is currentSHA — the value the remote currently has.
	if err := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	if err := deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusChangesRequested, hasCommits); err != nil {
		return fmt.Errorf("update review status: %w", err)
	}

	// The rejection is recorded at this point whatever the branch cleanup or
	// push proposal below does, so the reason is printed here rather than deferred.
	if reason != "" {
		fmt.Fprintf(deps.client.IO().Out, "Reason:\n%s\n", indentLines(reason))
	}

	// Handle review branch: keep if reviewer pushed commits, delete if empty.
	if reviewBranchExists && !hasCommits {
		if err := deps.client.DeleteLocalBranchSafe(ctx, reviewBranch, true, deps.cfg.Branch.Base); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
		}
		// Only push --delete when the branch actually exists on the remote.
		if deps.client.RemoteBranchExists(ctx, reviewBranch) {
			_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
		}
		// Use issueSlug as fallback when the feature branch is not in the local store.
		branchLabel := featureBranch
		if branchLabel == "" {
			branchLabel = issueSlug
		}
		fmt.Fprintf(deps.client.IO().Out,
			"Issue %q: changes requested (round %d). Feature branch %q unlocked.\n",
			issueSlug, latest.Round, branchLabel)
		return nil
	}

	// Use issueSlug as fallback when feature branch not in local store.
	branchLabel := featureBranch
	if branchLabel == "" {
		branchLabel = issueSlug
	}

	if reviewBranchExists && hasCommits {
		if err := proposeReviewPush(ctx, deps, reviewBranch); err != nil {
			return err
		}
	}

	if hasCommits {
		n, _ := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
		fmt.Fprintf(deps.client.IO().Out,
			"Issue %q: changes requested (round %d). Feature branch %q unlocked.\n"+
				"%s has %d reviewer commit(s). Inspect with:\n"+
				"  git log %s..%s\n"+
				"Cherry-pick, adapt, or discard as needed, then:\n"+
				"  git zf review request\n",
			issueSlug, latest.Round, branchLabel,
			reviewBranch, n, branchLabel, reviewBranch)
		return nil
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q: changes requested (round %d). Feature branch %q unlocked.\n",
		issueSlug, latest.Round, branchLabel)

	return nil
}

// indentLines prefixes every line with two spaces for display under a heading.
func indentLines(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
