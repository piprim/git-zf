package branch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// scriptedTrackPrompter is the issueflow.StartPrompter of the track tests:
// only PickIssueFromUser matters.
type scriptedTrackPrompter struct {
	Issue          *issue.Issue
	Err            error
	Opened         bool
	OfferedSubject string
}

func (p *scriptedTrackPrompter) PickIssueFromUser(_ context.Context, _, subject string, _ []string) (*issue.Issue, error) {
	p.Opened, p.OfferedSubject = true, subject

	return p.Issue, p.Err
}

func (*scriptedTrackPrompter) PickIssueFromTracker(context.Context, []tracker.Issue, []string) (*issue.Issue, error) {
	return nil, nil
}
func (*scriptedTrackPrompter) PickUseWorktree(context.Context) (bool, error) { return false, nil }
func (*scriptedTrackPrompter) PickBaseBranch(context.Context, string, []string) (string, error) {
	return "", nil
}
func (*scriptedTrackPrompter) ConfirmCreateBranch(context.Context, string) (bool, error) {
	return false, nil
}
func (*scriptedTrackPrompter) ConfirmCreateWorktree(context.Context, string) (bool, error) {
	return false, nil
}
func (*scriptedTrackPrompter) PickTrackerStatus(context.Context, string, string, []string) (string, error) {
	return "", nil
}
func (*scriptedTrackPrompter) ResolveBranchConflict(context.Context, *git.Client, *branch.Branch, *issue.Issue) (*branch.Branch, error) {
	return nil, nil
}

var _ issueflow.StartPrompter = (*scriptedTrackPrompter)(nil)

func trackCfg() *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.CommitTypes = []config.CommitTypeOption{{Name: "feat"}, {Name: "fix"}}

	return cfg
}

func TestTrackPlain(t *testing.T) {
	typed := &issue.Issue{Type: "feat"}
	typed.ID, typed.Subject = "X7", "spike"

	t.Run("renames with a typed ID and records the branch", func(t *testing.T) {
		rig := newTableRig(t)
		if err := trackPlain(t.Context(), rig.client, trackCfg(), &scriptedTrackPrompter{Issue: typed}, "spike"); err != nil {
			t.Fatalf("trackPlain: %v", err)
		}
		_, e, _ := branch.Find(t.Context(), rig.client, "X7@feat@spike")
		if !rig.hasBranch(t, "X7@feat@spike") || rig.hasBranch(t, "spike") || e == nil || e.Status != branch.StatusInProgress {
			t.Errorf("new = %v, old = %v, entry = %+v", rig.hasBranch(t, "X7@feat@spike"), rig.hasBranch(t, "spike"), e)
		}
		if st, _, _ := branch.Find(t.Context(), rig.client, "X7@feat@spike"); st.Slug != "X7" {
			t.Errorf("slug = %q", st.Slug)
		}
	})
	t.Run("prefills the subject from the name", func(t *testing.T) {
		rig := newTableRig(t)
		rig.createGitBranch(t, "spike-log-colors")
		p := &scriptedTrackPrompter{Err: huh.ErrUserAborted}
		_ = trackPlain(t.Context(), rig.client, trackCfg(), p, "spike-log-colors")
		if p.OfferedSubject != "spike log colors" {
			t.Errorf("offered subject = %q", p.OfferedSubject)
		}
	})
	t.Run("an empty ID mints a repo issue linked to the branch", func(t *testing.T) {
		rig := newTableRig(t)
		minted := &issue.Issue{Type: "feat"}
		minted.Subject = "Spike it"
		if err := trackPlain(t.Context(), rig.client, trackCfg(), &scriptedTrackPrompter{Issue: minted}, "spike"); err != nil {
			t.Fatalf("trackPlain: %v", err)
		}
		recs, _, err := issue.List(t.Context(), rig.client)
		if err != nil || len(recs) != 1 || recs[0].Title != "Spike it" {
			t.Fatalf("records = %+v, %v", recs, err)
		}
		rows, _ := branch.ListRows(t.Context(), rig.client, branch.StatusInProgress)
		var row *branch.Row
		for i := range rows {
			if strings.HasPrefix(rows[i].BranchName, recs[0].DisplayID()+"@feat@") {
				row = &rows[i]
			}
		}
		if row == nil || row.IssueID != recs[0].ID || !rig.hasBranch(t, row.BranchName) {
			t.Errorf("row = %+v, record = %s", row, recs[0].ID)
		}
	})
	t.Run("a branch on the remote is refused untouched", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "push", "-q", "origin", "spike")
		p := &scriptedTrackPrompter{Issue: typed}
		err := trackPlain(t.Context(), rig.client, trackCfg(), p, "spike")
		if err == nil || !strings.Contains(err.Error(), "exists on origin") || !rig.hasBranch(t, "spike") || p.Opened || rig.statusOf(t, "X7@feat@spike") != "" {
			t.Errorf("err = %v, spike = %v, opened = %v", err, rig.hasBranch(t, "spike"), p.Opened)
		}
	})
	t.Run("a name clash changes nothing and publishes no issue", func(t *testing.T) {
		rig := newTableRig(t)
		rig.createGitBranch(t, "X7@feat@spike")
		clash := &issue.Issue{Type: "feat"}
		clash.Subject = "spike"
		err := trackPlain(t.Context(), rig.client, trackCfg(), &scriptedTrackPrompter{Issue: typed}, "spike")
		recs, _, _ := issue.List(t.Context(), rig.client)
		if err == nil || !strings.Contains(err.Error(), "already exists") || !rig.hasBranch(t, "spike") || len(recs) != 0 || rig.statusOf(t, "X7@feat@spike") != "" {
			t.Errorf("err = %v, spike = %v, records = %d", err, rig.hasBranch(t, "spike"), len(recs))
		}
	})
	t.Run("a branch held by a worktree is renamed in place", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "worktree", "add", "-q", t.TempDir()+"/wt", "spike")
		if err := trackPlain(t.Context(), rig.client, trackCfg(), &scriptedTrackPrompter{Issue: typed}, "spike"); err != nil {
			t.Fatalf("trackPlain: %v", err)
		}
		if out := gitIn(t, rig.dir, "worktree", "list"); !strings.Contains(out, "[X7@feat@spike]") {
			t.Errorf("worktree list:\n%s", out)
		}
	})
	t.Run("the base branch is refused before the form", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedTrackPrompter{Issue: typed}
		err := trackPlain(t.Context(), rig.client, trackCfg(), p, "master")
		if err == nil || !strings.Contains(err.Error(), "base branch") || p.Opened || !rig.hasBranch(t, "master") {
			t.Errorf("err = %v, opened = %v, master = %v", err, p.Opened, rig.hasBranch(t, "master"))
		}
	})
	t.Run("the configured base branch is refused before the form", func(t *testing.T) {
		rig := newTableRig(t)
		cfg := trackCfg()
		cfg.Branch.Base = "spike"
		p := &scriptedTrackPrompter{Issue: typed}
		err := trackPlain(t.Context(), rig.client, cfg, p, "spike")
		if err == nil || !strings.Contains(err.Error(), "base branch") || p.Opened || !rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, opened = %v, spike = %v", err, p.Opened, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("esc on the form leaves the branch alone", func(t *testing.T) {
		rig := newTableRig(t)
		err := trackPlain(t.Context(), rig.client, trackCfg(), &scriptedTrackPrompter{Err: huh.ErrUserAborted}, "spike")
		if !errors.Is(err, huh.ErrUserAborted) || !rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, spike = %v", err, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("a parseable name is tracked without the form", func(t *testing.T) {
		rig := newTableRig(t)
		rig.createGitBranch(t, "9@feat@thing")
		p := &scriptedTrackPrompter{Issue: typed}
		d := rig.deps()
		d.cfg = trackCfg()
		if err := trackBranch(t.Context(), *d, p, "9@feat@thing"); err != nil {
			t.Fatalf("trackBranch: %v", err)
		}
		if p.Opened || rig.statusOf(t, "9@feat@thing") != branch.StatusInProgress {
			t.Errorf("opened = %v, status = %q", p.Opened, rig.statusOf(t, "9@feat@thing"))
		}
	})
}
