package branch

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
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

// prDeps bundles what runPr needs; the E2E rig builds it directly.
type prDeps struct {
	client            *git.Client
	cfg               *config.AppConfig
	forge             tracker.PullRequests // nil ⇒ the tracker has no pull requests
	trackerType       string               // "" ⇒ no tracker configured
	push, noPush, yes bool
	pushConfirm       pushflow.ConfirmFunc
	form              PullRequestForm
}

func (b Branch) prCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr [branch]",
		Short: "Open a pull request for a branch on the forge",
		Long: `Open a pull request for the branch (the current one by default) on the
issue tracker's forge, GitHub or Forgejo. The branch is pushed first when it
is not on the remote. A branch that already has an open pull request gets its
URL printed.

The form is prefilled from the branch's issue: its title, its parent branch as
the base, and "Closes #N" when the issue is mirrored with the tracker.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
			if err != nil {
				return err //nolint:wrapcheck // already names the cause
			}

			d := prDeps{
				client: c, cfg: b.appConfig, trackerType: b.appConfig.IssueTracker.Type,
				pushConfirm: pushflow.NewHuhConfirm(), form: HuhPullRequestForm(),
			}
			// Tolerant readers: the branch menu runs this with the root command.
			d.push, d.noPush = pushflow.ReadFlags(cmd)
			d.yes = cmdutil.StringFlag(cmd, "yes") == "true"
			if d.trackerType != "" {
				t, err := tracker.New(b.appConfig.IssueTracker)
				if err != nil {
					return fmt.Errorf("init tracker: %w", err)
				}
				d.forge = tracker.PullRequestsOf(t)
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			return runPr(cmd.Context(), d, name)
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "non-interactive: refused, a pull request needs a title")
	pushflow.AddFlags(cmd)

	return cmd
}

// runPr opens a pull request for branchName (the current branch when "").
func runPr(ctx context.Context, d prDeps, branchName string) error {
	switch {
	case d.trackerType == "":
		return errors.New("no issue tracker is configured: a pull request needs a forge (github or forgejo)")
	case d.forge == nil:
		return fmt.Errorf("the issue tracker %q has no pull requests", d.trackerType)
	case d.yes:
		return errors.New("a pull request needs a title: run without --yes")
	}

	if branchName == "" {
		cur, err := d.client.CurrentBranch()
		if err != nil || cur == "" || cur == "HEAD" {
			return errors.New("checkout a branch, or name one: git zf branch pr <branch>")
		}
		branchName = cur
	}
	if ok, err := d.client.BranchExists(branchName); err != nil || !ok {
		return fmt.Errorf("branch %q does not exist in this clone", branchName)
	}

	draft := PullRequestDraftFor(ctx, d.client, d.cfg.Branch.Base, branchName)
	if branchName == draft.Base {
		return fmt.Errorf("%q is the base branch: a pull request needs a feature branch", branchName)
	}

	remote, err := d.client.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return errors.New("this clone has no remote: a pull request needs the branch on the forge")
	}

	// The pull request needs the branch on the forge.
	if _, toPush, err := d.client.PushDryRun(ctx, branchName); err != nil {
		return fmt.Errorf("check %q against %s: %w", branchName, remote, err)
	} else if toPush {
		skip, auto, err := pushflow.ResolveFlags(d.push, d.noPush, true)
		if err != nil {
			return err //nolint:wrapcheck // pushflow already wraps
		}
		if skip {
			return fmt.Errorf("branch %q is not on %s yet: push it first", branchName, remote)
		}
		if err := pushflow.Propose(ctx, d.client, pushflow.Opts{Branch: branchName, AutoConfirm: auto}, d.pushConfirm); err != nil {
			return err //nolint:wrapcheck // pushflow already wraps
		}
		if _, still, _ := d.client.PushDryRun(ctx, branchName); still {
			fmt.Fprintln(d.client.IO().Out, "Aborted.")

			return nil
		}
	}

	return OpenPullRequest(ctx, d.client.IO().Out, d.forge, branchName, draft, d.form)
}
