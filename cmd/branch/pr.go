package branch

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)

// PullRequestDraft is what a pull request is opened with.
type PullRequestDraft struct {
	Title, Body, Base string
}

// PullRequestForm shows the draft of branchName's pull request and reports
// whether to open it. branch pr lets the operator edit the draft; the push
// step only asks. Esc is a no.
type PullRequestForm func(ctx context.Context, branchName string, draft *PullRequestDraft) (open bool, err error)

// OpenPullRequest prints the URL of branchName's open pull request, or runs
// form and opens one from the draft. It is the step behind branch pr and
// behind the push proposal of commit.
func OpenPullRequest(
	ctx context.Context, out io.Writer, forge tracker.PullRequests,
	branchName string, draft PullRequestDraft, form PullRequestForm,
) error {
	pr, err := forge.FindPullRequest(ctx, branchName)
	switch {
	case err == nil:
		fmt.Fprintf(out, "Pull request #%s is open for %q: %s\n", pr.Number, branchName, pr.URL)

		return nil
	case !errors.Is(err, tracker.ErrNoPullRequest):
		return fmt.Errorf("look up the pull request of %q: %w", branchName, err)
	}

	open, err := form(ctx, branchName, &draft)
	if err != nil {
		return err
	}
	if !open {
		return nil
	}

	pr, err = forge.CreatePullRequest(ctx, branchName, draft.Base, draft.Title, draft.Body)
	if err != nil {
		return fmt.Errorf("open a pull request for %q: %w", branchName, err)
	}
	fmt.Fprintf(out, "Opened pull request #%s for %q against %q: %s\n", pr.Number, branchName, pr.Base, pr.URL)

	return nil
}

// PullRequestDraftFor prefills from what the branch chain records: the issue
// title (else the branch name), the parent branch (else cfgBase, else the
// default base branch) and "Closes #N" when the issue has a tracker number.
func PullRequestDraftFor(ctx context.Context, c *git.Client, cfgBase, branchName string) PullRequestDraft {
	d := PullRequestDraft{Title: branchName, Base: cfgBase}
	if d.Base == "" {
		if b, err := c.DefaultBaseBranch(); err == nil {
			d.Base = b
		}
	}

	st, e, err := branch.Find(ctx, c, branchName)
	if err != nil || e == nil {
		return d
	}
	if st.Title != "" {
		d.Title = st.Title
	}
	if parent, err := issueflow.ResolveParentBranch(ctx, c, st.Slug, cfgBase); err == nil && parent != "" {
		d.Base = parent
	}
	if n := issue.TrackerNumber(ctx, c, st.IssueID); n != "" {
		d.Body = "Closes #" + n
	}

	return d
}

// HuhPullRequestForm is the production form of branch pr: the three fields
// and a confirm. Esc declines.
func HuhPullRequestForm() PullRequestForm {
	return func(ctx context.Context, branchName string, d *PullRequestDraft) (bool, error) {
		confirmed := true
		err := tui.Form(tui.PullRequestForm(branchName, &d.Title, &d.Body, &d.Base, &confirmed)).RunWithContext(ctx)
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("pull request form: %w", err)
		}

		return confirmed, nil
	}
}

// HuhPullRequestConfirm is the push step's offer: one question naming the
// branch and the base, no edit. Esc declines.
func HuhPullRequestConfirm() PullRequestForm {
	return func(ctx context.Context, branchName string, d *PullRequestDraft) (bool, error) {
		confirmed := true
		err := tui.Form(tui.PullRequestConfirm(branchName, d.Base, &confirmed)).RunWithContext(ctx)
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("pull request confirm: %w", err)
		}

		return confirmed, nil
	}
}
