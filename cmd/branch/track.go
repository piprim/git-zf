package branch

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/review"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
)

// trackBranch is the table's track entry: a git-zf name goes through
// review.TrackBranch, any other name through the branch new form.
func trackBranch(ctx context.Context, d tableDeps, sp issueflow.StartPrompter, name string) error {
	err := review.TrackBranch(ctx, d.cfg, d.client, name)
	if errors.Is(err, review.ErrNotTrackable) {
		return trackPlain(ctx, d.client, d.cfg, sp, name)
	}

	return err //nolint:wrapcheck // the review package words it
}

// trackPlain renames a free-named branch to <id>@<type>@<slug> after the
// branch new form and records it in progress. An empty ID mints a repo issue:
// prepared before the rename (nothing left behind on an abort), published
// after it.
func trackPlain(ctx context.Context, c *git.Client, cfg *config.AppConfig, sp issueflow.StartPrompter, name string) error {
	base := cfg.Branch.Base
	if base == "" {
		base, _ = c.DefaultBaseBranch()
	}
	if base == name {
		return fmt.Errorf("branch %q is the base branch: it is not tracked", name)
	}
	if c.RemoteBranchExists(ctx, name) {
		remote, _ := c.Remote()

		return fmt.Errorf("branch %q exists on %s: delete %s/%s first or keep the branch plain", name, remote, remote, name)
	}

	var types []string
	for _, ct := range cfg.CommitTypes {
		types = append(types, ct.Name)
	}
	got, err := sp.PickIssueFromUser(ctx, "", branch.TitleFromName(name), types)
	if err != nil {
		return err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}
	if got == nil {
		return nil
	}

	slug, recID := got.ID, ""
	if slug == "" {
		if recID, err = issue.Prepare(ctx, c, issue.NewIssue{Title: got.Subject, BranchType: got.Type}); err != nil {
			return fmt.Errorf("prepare repo issue: %w", err)
		}
		slug = (&issue.Record{ID: recID}).DisplayID()
	}

	b, err := branch.New(slug, got.Type, got.Subject, "")
	if err != nil {
		return fmt.Errorf("branch name: %w", err)
	}
	if exists, err := c.BranchExists(b.Name()); err != nil || exists {
		return fmt.Errorf("branch %q already exists", b.Name())
	}
	if err := c.RenameBranch(ctx, name, b.Name()); err != nil {
		return err //nolint:wrapcheck // names both branches
	}

	if recID != "" {
		if err := issue.Publish(ctx, c, recID); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: branch renamed but the repo issue could not be saved: %v\n", err)
		} else if err := issue.Push(ctx, c, recID); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: issue saved locally but not pushed (run `git zf issue sync` later): %v\n", err)
		}
	}

	op := &branch.Op{Branch: b.Name(), BranchType: got.Type, Title: got.Subject, IssueID: recID}
	if err := branch.Start(ctx, c, slug, op); err != nil {
		return fmt.Errorf("track branch: %w", err)
	}
	if err := branch.Push(ctx, c, slug); err != nil {
		fmt.Fprintf(c.IO().Err, "warning: push branch ref: %v\n", err)
	}
	fmt.Fprintf(c.IO().Out, "Branch %q renamed to %q and tracked (issue %s).\n", name, b.Name(), slug)

	return nil
}
