package branch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tui"
)

// scriptedBranchMenuPrompter answers PickAction with Actions in order and
// ConfirmForceDelete with Force.
type scriptedBranchMenuPrompter struct {
	Actions    []branchAction
	Force      bool
	Titles     []string
	Offered    [][]branchAction
	ForceAsked bool
	// RemoveWorktree answers ConfirmRemoveWorktree; WorktreeAsked holds the
	// path it was asked about.
	RemoveWorktree bool
	WorktreeAsked  string
	// DeleteRemote answers ConfirmDeleteRemote; RemoteAsked holds the branch
	// it was asked about and RemoteMerged what it was told.
	DeleteRemote bool
	RemoteAsked  string
	RemoteBase   string
	RemoteMerged bool
	// Target answers PickTarget; TargetsOffered holds what it was offered.
	Target         string
	TargetsOffered []string
}

func (p *scriptedBranchMenuPrompter) PickTarget(_ context.Context, _ string, targets []string) (string, error) {
	p.TargetsOffered = targets

	return p.Target, nil
}

func (p *scriptedBranchMenuPrompter) ConfirmDeleteRemote(_ context.Context, name, _, base string, merged bool) (bool, error) {
	p.RemoteAsked, p.RemoteBase, p.RemoteMerged = name, base, merged

	return p.DeleteRemote, nil
}

func (p *scriptedBranchMenuPrompter) ConfirmRemoveWorktree(_ context.Context, _, path string) (bool, error) {
	p.WorktreeAsked = path

	return p.RemoveWorktree, nil
}

func (p *scriptedBranchMenuPrompter) PickAction(_ context.Context, title string, entries []branchAction) (branchAction, error) {
	p.Titles = append(p.Titles, title)
	p.Offered = append(p.Offered, entries)
	if len(p.Actions) == 0 {
		return "", errors.New("scripted prompter: no action left")
	}
	a := p.Actions[0]
	p.Actions = p.Actions[1:]

	return a, nil
}

func (p *scriptedBranchMenuPrompter) ConfirmForceDelete(_ context.Context, _ string) (bool, error) {
	p.ForceAsked = true

	return p.Force, nil
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// newTableRig is the prune rig with: spike (plain, unmerged), 42@feat@assign
// (in progress, unmerged), 51@fix@prune (merged, recorded merged) and
// remote-thing (on origin only).
func newTableRig(t *testing.T) *pruneTestRig {
	t.Helper()

	rig := newPruneRigWithOrigin(t, newPruneOrigin(t))
	rig.createGitBranch(t, "spike")
	rig.seedIssueAndBranch(t, "42", "42@feat@assign", "feat")
	rig.createGitBranch(t, "42@feat@assign")
	rig.mergeBranchIntoMaster(t, "51@fix@prune", "prune\n")
	branchtest.Seed(t, rig.client, branch.Op{Branch: "51@fix@prune", BranchType: "fix", Title: "51"}, branch.StatusMerged)
	rig.createGitBranch(t, "remote-thing")
	gitIn(t, rig.dir, "push", "-q", "origin", "remote-thing")
	gitIn(t, rig.dir, "branch", "-D", "remote-thing")

	return rig
}

// remoteOnlyInProgress seeds a tracked branch in progress, with a commit of
// its own, that exists on the remote alone, and returns its row.
func (r *pruneTestRig) remoteOnlyInProgress(t *testing.T) branch.Row {
	t.Helper()

	const name = "60@feat@gone"
	r.seedIssueAndBranch(t, "60", name, "feat")
	gitIn(t, r.dir, "branch", name)
	gitIn(t, r.dir, "checkout", "-q", name)
	gitIn(t, r.dir, "commit", "-q", "--allow-empty", "-m", "work")
	gitIn(t, r.dir, "push", "-q", "origin", name)
	gitIn(t, r.dir, "checkout", "-q", "master")
	gitIn(t, r.dir, "branch", "-D", name)

	return branch.Row{BranchName: name, IssueSlug: "60", Type: "feat", Status: branch.StatusInProgress, RemoteOnly: true}
}

func (r *pruneTestRig) deps() *tableDeps {
	return &tableDeps{client: r.client, stdout: r.stdout, stderr: r.stderr}
}

func (r *pruneTestRig) hasBranch(t *testing.T, name string) bool {
	t.Helper()

	ok, err := r.client.BranchExists(name)
	if err != nil {
		t.Fatalf("BranchExists(%s): %v", name, err)
	}

	return ok
}

func TestRunMenu(t *testing.T) {
	plain := branch.Row{BranchName: "spike", Plain: true}

	t.Run("the error of the picked action is printed and the table reopens", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "worktree", "add", "-q", t.TempDir()+"/wt", "spike")
		p := &scriptedBranchMenuPrompter{Actions: []branchAction{actionCheckout}}
		reopen, err := runMenu(t.Context(), rig.deps(), p, &plain)
		if err == nil || !afterAction(reopen, err, rig.stderr) || !strings.Contains(rig.stderr.String(), "error: ") {
			t.Errorf("err = %v, reopen = %v, stderr:\n%s", err, reopen, rig.stderr.String())
		}
	})
	t.Run("a picked action runs on the row", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Actions: []branchAction{actionDelete}, Force: true}
		reopen, err := runMenu(t.Context(), rig.deps(), p, &plain)
		if err != nil || !reopen || rig.hasBranch(t, "spike") || len(p.Titles) != 1 || p.Titles[0] != "spike" {
			t.Errorf("err = %v, reopen = %v, exists = %v, titles = %v", err, reopen, rig.hasBranch(t, "spike"), p.Titles)
		}
	})
}

func TestRunBranchAction(t *testing.T) {
	plain := branch.Row{BranchName: "spike", Plain: true}
	inProgress := branch.Row{BranchName: "42@feat@assign", IssueSlug: "42", Status: branch.StatusInProgress}
	merged := branch.Row{BranchName: "51@fix@prune", IssueSlug: "51", Status: branch.StatusMerged}

	t.Run("checkout switches to a plain branch and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, &plain)
		cur, _ := rig.client.CurrentBranch()
		if err != nil || reopen || cur != "spike" {
			t.Errorf("err = %v, reopen = %v, current = %q", err, reopen, cur)
		}
	})
	t.Run("checkout refuses a branch held by a worktree", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "worktree", "add", "-q", t.TempDir()+"/wt", "spike")
		_, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, &plain)
		if err == nil || !strings.Contains(err.Error(), "worktree") {
			t.Errorf("err = %v, want a worktree refusal", err)
		}
	})
	t.Run("checkout of a remote-only branch creates it with an upstream", func(t *testing.T) {
		rig := newTableRig(t)
		row := branch.Row{BranchName: "remote-thing", Plain: true, RemoteOnly: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, &row); err != nil {
			t.Fatalf("checkout: %v", err)
		}
		if up := gitIn(t, rig.dir, "rev-parse", "--abbrev-ref", "remote-thing@{upstream}"); up != "origin/remote-thing" {
			t.Errorf("upstream = %q", up)
		}
	})
	t.Run("delete of a remote-only branch in progress deletes it on the remote and closes its record", func(t *testing.T) {
		rig := newTableRig(t)
		row := rig.remoteOnlyInProgress(t)
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		reopen, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row)
		if err != nil || !reopen || p.RemoteAsked != row.BranchName || p.RemoteMerged {
			t.Fatalf("err = %v, reopen = %v, asked = %q, merged = %v", err, reopen, p.RemoteAsked, p.RemoteMerged)
		}
		if rig.client.RemoteBranchExists(t.Context(), row.BranchName) {
			t.Error("the branch is still on the remote")
		}
		if _, e, err := branch.Find(t.Context(), rig.client, row.BranchName); err != nil || e == nil || e.Status != branch.StatusClosed {
			t.Errorf("record = %+v, err = %v, want closed", e, err)
		}
		if out := rig.stdout.String(); !strings.Contains(out, "recorded as closed") || strings.Contains(out, "untouched") {
			t.Errorf("output must say the record is closed and not that the branch is untouched:\n%s", out)
		}
	})
	t.Run("delete of a remote-only branch already gone from the remote drops the stale ref and closes its record", func(t *testing.T) {
		rig := newTableRig(t)
		row := rig.remoteOnlyInProgress(t)
		// Deleted on the forge: this clone still holds the remote-tracking ref.
		gitIn(t, gitIn(t, rig.dir, "remote", "get-url", "origin"), "update-ref", "-d", "refs/heads/"+row.BranchName)
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := rig.client.ResolveRef("refs/remotes/origin/" + row.BranchName); err == nil {
			t.Error("the stale remote-tracking ref is still there")
		}
		if got := rig.statusOf(t, row.BranchName); got != branch.StatusClosed {
			t.Errorf("record = %q, want closed", got)
		}
		if out := rig.stdout.String(); !strings.Contains(out, "already gone") {
			t.Errorf("output does not say the branch was already gone:\n%s", out)
		}
	})
	t.Run("delete of a remote-only branch is refused when the remote moved since the last fetch", func(t *testing.T) {
		rig := newTableRig(t)
		row := rig.remoteOnlyInProgress(t)
		// A teammate pushes: the remote's tip is no longer the one this clone fetched.
		origin := gitIn(t, rig.dir, "remote", "get-url", "origin")
		gitIn(t, origin, "update-ref", "refs/heads/"+row.BranchName, gitIn(t, rig.dir, "rev-parse", "origin/remote-thing"))
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row)
		if err == nil || !strings.Contains(err.Error(), "fetch") {
			t.Errorf("err = %v, want a refusal naming the fetch", err)
		}
		if !rig.client.RemoteBranchExists(t.Context(), row.BranchName) {
			t.Error("the branch is gone from the remote")
		}
		if got := rig.statusOf(t, row.BranchName); got != branch.StatusInProgress {
			t.Errorf("record = %q, want in progress", got)
		}
	})
	t.Run("delete of a remote-only branch reads the base on the remote when there is no local one", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "push", "-q", "origin", "master", "master:stale")
		gitIn(t, rig.dir, "remote", "set-head", "origin", "master")
		gitIn(t, rig.dir, "checkout", "-q", "--orphan", "elsewhere")
		gitIn(t, rig.dir, "commit", "-q", "--allow-empty", "-m", "unrelated")
		gitIn(t, rig.dir, "branch", "-D", "master")
		row := branch.Row{BranchName: "stale", Plain: true, RemoteOnly: true}
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row); err != nil || !p.RemoteMerged || p.RemoteBase != "master" {
			t.Fatalf("err = %v, merged = %v, base = %q", err, p.RemoteMerged, p.RemoteBase)
		}
	})
	t.Run("delete of a remote-only branch on a dead remote does not say the branch moved", func(t *testing.T) {
		rig := newTableRig(t)
		row := rig.remoteOnlyInProgress(t)
		gitIn(t, rig.dir, "remote", "set-url", "origin", "/nonexistent/origin.git")
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row)
		if err == nil || strings.Contains(err.Error(), "moved") {
			t.Errorf("err = %v, want a failure that does not blame a moved branch", err)
		}
		if got := rig.statusOf(t, row.BranchName); got != branch.StatusInProgress {
			t.Errorf("record = %q, want in progress", got)
		}
	})
	t.Run("delete of a remote-only branch keeps the branch and its record on no", func(t *testing.T) {
		rig := newTableRig(t)
		row := rig.remoteOnlyInProgress(t)
		p := &scriptedBranchMenuPrompter{}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row); err != nil || p.RemoteAsked == "" {
			t.Fatalf("err = %v, asked = %q", err, p.RemoteAsked)
		}
		if !rig.client.RemoteBranchExists(t.Context(), row.BranchName) {
			t.Error("the branch is gone from the remote")
		}
		if _, e, err := branch.Find(t.Context(), rig.client, row.BranchName); err != nil || e == nil || e.Status != branch.StatusInProgress {
			t.Errorf("record = %+v, err = %v, want in progress", e, err)
		}
	})
	t.Run("delete of a plain remote-only branch whose tip is in the base says so and deletes it", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "push", "-q", "origin", "master:stale")
		// On another branch: the question is about the base, not HEAD.
		gitIn(t, rig.dir, "checkout", "-q", "--orphan", "elsewhere")
		gitIn(t, rig.dir, "commit", "-q", "--allow-empty", "-m", "unrelated")
		row := branch.Row{BranchName: "stale", Plain: true, RemoteOnly: true}
		p := &scriptedBranchMenuPrompter{DeleteRemote: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &row); err != nil || !p.RemoteMerged || p.RemoteBase != "master" {
			t.Fatalf("err = %v, merged = %v, base = %q", err, p.RemoteMerged, p.RemoteBase)
		}
		if rig.client.RemoteBranchExists(t.Context(), "stale") {
			t.Error("the branch is still on the remote")
		}
	})
	t.Run("the menu of a record without a branch names who started it", func(t *testing.T) {
		rig := newTableRig(t)
		row := branch.Row{BranchName: "77@feat@nowhere", Status: branch.StatusInProgress, NoBranch: true, Author: "bob"}
		p := &scriptedBranchMenuPrompter{}
		_, _ = runMenu(t.Context(), rig.deps(), p, &row)
		if len(p.Titles) != 1 || !strings.Contains(p.Titles[0], "started by bob") {
			t.Errorf("titles = %q", p.Titles)
		}
	})
	t.Run("abandon of a record without a branch closes it", func(t *testing.T) {
		rig := newTableRig(t)
		rig.seedIssueAndBranch(t, "77", "77@feat@nowhere", "feat")
		row := branch.Row{BranchName: "77@feat@nowhere", IssueSlug: "77", Status: branch.StatusInProgress, NoBranch: true}
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "a", &row)
		if err != nil || !reopen || rig.statusOf(t, row.BranchName) != branch.StatusClosed {
			t.Errorf("err = %v, reopen = %v, status = %q", err, reopen, rig.statusOf(t, row.BranchName))
		}
	})
	t.Run("delete removes a merged branch", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{}
		reopen, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &merged)
		if err != nil || !reopen || rig.hasBranch(t, "51@fix@prune") || p.ForceAsked {
			t.Errorf("err = %v, reopen = %v, exists = %v, asked = %v", err, reopen, rig.hasBranch(t, "51@fix@prune"), p.ForceAsked)
		}
	})
	t.Run("delete of an unmerged branch asks and force-deletes on yes", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &plain)
		if err != nil || !p.ForceAsked || rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, asked = %v, exists = %v", err, p.ForceAsked, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("delete of an unmerged branch keeps it on no", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: false}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &plain)
		if err != nil || !p.ForceAsked || !rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, asked = %v, exists = %v", err, p.ForceAsked, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("delete of a branch held by a clean worktree asks, then removes the worktree too", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "51@fix@prune")
		p := &scriptedBranchMenuPrompter{RemoveWorktree: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &merged)
		_, statErr := os.Stat(wt)
		if err != nil || !git.SamePath(p.WorktreeAsked, wt) || p.ForceAsked || rig.hasBranch(t, "51@fix@prune") || !os.IsNotExist(statErr) {
			t.Errorf("err = %v, asked about %q, force asked = %v, exists = %v, worktree stat = %v",
				err, p.WorktreeAsked, p.ForceAsked, rig.hasBranch(t, "51@fix@prune"), statErr)
		}
	})
	t.Run("delete of a worktree-held branch keeps both on no", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "51@fix@prune")
		p := &scriptedBranchMenuPrompter{Force: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &merged)
		_, statErr := os.Stat(wt)
		if err != nil || p.WorktreeAsked == "" || p.ForceAsked || !rig.hasBranch(t, "51@fix@prune") || statErr != nil ||
			!strings.Contains(rig.stdout.String(), "kept") {
			t.Errorf("err = %v, asked about %q, force asked = %v, exists = %v, worktree stat = %v",
				err, p.WorktreeAsked, p.ForceAsked, rig.hasBranch(t, "51@fix@prune"), statErr)
		}
	})
	t.Run("delete of a branch held by a dirty worktree keeps both", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "51@fix@prune")
		if err := os.WriteFile(wt+"/untracked", []byte("work\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &scriptedBranchMenuPrompter{Force: true, RemoveWorktree: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &merged)
		_, statErr := os.Stat(wt)
		if err == nil || p.ForceAsked || !rig.hasBranch(t, "51@fix@prune") || statErr != nil {
			t.Errorf("err = %v, force asked = %v, exists = %v, worktree stat = %v", err, p.ForceAsked, rig.hasBranch(t, "51@fix@prune"), statErr)
		}
	})
	t.Run("delete of an unmerged worktree-held branch asks to force first and keeps both on no", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "spike")
		p := &scriptedBranchMenuPrompter{Force: false, RemoveWorktree: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &plain)
		_, statErr := os.Stat(wt)
		if err != nil || !p.ForceAsked || p.WorktreeAsked != "" || !rig.hasBranch(t, "spike") || statErr != nil {
			t.Errorf("err = %v, force asked = %v, asked about %q, exists = %v, worktree stat = %v",
				err, p.ForceAsked, p.WorktreeAsked, rig.hasBranch(t, "spike"), statErr)
		}
	})
	t.Run("delete of an unmerged worktree-held branch removes both on two yeses", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "spike")
		p := &scriptedBranchMenuPrompter{Force: true, RemoveWorktree: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &plain)
		_, statErr := os.Stat(wt)
		if err != nil || !p.ForceAsked || rig.hasBranch(t, "spike") || !os.IsNotExist(statErr) {
			t.Errorf("err = %v, force asked = %v, exists = %v, worktree stat = %v", err, p.ForceAsked, rig.hasBranch(t, "spike"), statErr)
		}
	})
	t.Run("delete from a linked worktree refuses the branch of the main tree without asking", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "spike")
		wtClient, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: rig.stdout, Err: rig.stderr}, wt)
		if err != nil {
			t.Fatalf("git.NewClientAt(worktree): %v", err)
		}
		d := rig.deps()
		d.client = wtClient
		p := &scriptedBranchMenuPrompter{Force: true, RemoveWorktree: true}
		_, err = runBranchAction(t.Context(), d, p, actionDelete, &branch.Row{BranchName: "master", Plain: true})
		if err == nil || !strings.Contains(err.Error(), "main working tree") || p.WorktreeAsked != "" || p.ForceAsked {
			t.Errorf("err = %v, asked about %q, force asked = %v", err, p.WorktreeAsked, p.ForceAsked)
		}
	})
	t.Run("delete of a branch held by a stale worktree entry names git worktree prune", func(t *testing.T) {
		rig := newTableRig(t)
		wt := t.TempDir() + "/wt"
		gitIn(t, rig.dir, "worktree", "add", "-q", wt, "51@fix@prune")
		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}
		p := &scriptedBranchMenuPrompter{RemoveWorktree: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &merged)
		if err == nil || !strings.Contains(err.Error(), "git worktree prune") || p.WorktreeAsked != "" || !rig.hasBranch(t, "51@fix@prune") {
			t.Errorf("err = %v, asked about %q, exists = %v", err, p.WorktreeAsked, rig.hasBranch(t, "51@fix@prune"))
		}
	})
	t.Run("delete of an in-progress tracked branch keeps its record and names prune", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, &inProgress); err != nil {
			t.Fatalf("delete: %v", err)
		}
		out := rig.stdout.String()
		if st := rig.statusOf(t, "42@feat@assign"); st != branch.StatusInProgress || !strings.Contains(out, "still in progress") || !strings.Contains(out, "prune") {
			t.Errorf("status = %q, stdout:\n%s", st, out)
		}
	})
	t.Run("delete of a plain branch prints no prune reminder", func(t *testing.T) {
		rig := newTableRig(t)
		if _, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{Force: true}, actionDelete, &plain); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if out := rig.stdout.String(); strings.Contains(out, "still in progress") || !strings.Contains(out, "deleted") {
			t.Errorf("stdout:\n%s", out)
		}
	})
	t.Run("abandon records the row closed without merging", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionAbandon, &inProgress)
		if err != nil || !reopen || rig.statusOf(t, "42@feat@assign") != branch.StatusClosed || !rig.hasBranch(t, "42@feat@assign") {
			t.Errorf("err = %v, reopen = %v, status = %q", err, reopen, rig.statusOf(t, "42@feat@assign"))
		}
	})
	t.Run("new, prune and prune-tracker call their funcs", func(t *testing.T) {
		rig := newTableRig(t)
		var called []string
		d := rig.deps()
		rec := func(name string) func(context.Context) error {
			return func(context.Context) error { called = append(called, name); return nil }
		}
		d.newBranch, d.prune, d.pruneTracker = rec("new"), rec("prune"), rec("prune-tracker")
		var reopens []bool
		for _, pick := range []tui.BranchPick{tui.BranchPickNew, tui.BranchPickPrune, tui.BranchPickPruneTracker} {
			reopen, err := runPick(t.Context(), d, pick)
			if err != nil {
				t.Fatalf("runPick(%v): %v", pick, err)
			}
			reopens = append(reopens, reopen)
		}
		if strings.Join(called, ",") != "new,prune,prune-tracker" || reopens[0] || !reopens[1] || !reopens[2] {
			t.Errorf("called = %v, reopens = %v", called, reopens)
		}
	})
	t.Run("pr calls its func with the branch", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.pr = func(_ context.Context, name string) error { got = name; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionPR, &plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("merge into current merges the plain row", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got SourceBranch
		d.merge = func(_ context.Context, src SourceBranch) error { got = src; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionMerge, &plain)
		if err != nil || !reopen || got != (SourceBranch{Name: "spike"}) {
			t.Errorf("err = %v, reopen = %v, got = %+v", err, reopen, got)
		}
	})
	t.Run("issue close calls its func with the slug and branch and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var gotSlug, gotName string
		d.closeIssue = func(_ context.Context, slug, name string) error { gotSlug, gotName = slug, name; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionIssueClose, &inProgress)
		if err != nil || reopen || gotSlug != "42" || gotName != "42@feat@assign" {
			t.Errorf("err = %v, reopen = %v, slug = %q, name = %q", err, reopen, gotSlug, gotName)
		}
	})
	t.Run("log calls its func with the branch's full ref and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.log = func(_ context.Context, ref string) error { got = ref; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionLog, &plain)
		if err != nil || !reopen || got != "refs/heads/spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("log of a remote-only row names the remote-tracking ref", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		d.remote = "origin"
		var got string
		d.log = func(_ context.Context, ref string) error { got = ref; return nil }
		row := branch.Row{BranchName: "remote-thing", Plain: true, RemoteOnly: true}
		if _, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionLog, &row); err != nil || got != "refs/remotes/origin/remote-thing" {
			t.Errorf("err = %v, got = %q", err, got)
		}
	})
	t.Run("an entry with no func is not offered", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		if got := offeredEntries(d, &plain); slices.Contains(got, actionTrack) || slices.Contains(got, actionMerge) || !slices.Contains(got, actionDelete) {
			t.Errorf("offered without funcs = %v", got)
		}
		d.track = func(context.Context, string) error { return nil }
		if got := offeredEntries(d, &plain); !slices.Contains(got, actionTrack) {
			t.Errorf("offered with track = %v", got)
		}
	})
}

func TestRunBranchTable_NoTTY(t *testing.T) {
	rig := newTableRig(t)
	gone := rig.remoteOnlyInProgress(t)
	rig.seedIssueAndBranch(t, "77", "77@feat@nowhere", "feat")
	err := runBranchTable(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, &tui.BranchTableOptions{Remote: "origin"}, true)
	if err != nil {
		t.Fatalf("runBranchTable: %v", err)
	}
	out := rig.stdout.String()

	t.Run("prints every local branch with the seven columns", func(t *testing.T) {
		for _, want := range []string{"BRANCH", "LAST COMMIT", "spike", "42@feat@assign", "51@fix@prune", "* master"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("does not print remote-only rows", func(t *testing.T) {
		if strings.Contains(out, "remote-thing") {
			t.Errorf("output shows remote-thing:\n%s", out)
		}
	})
	t.Run("prints a record in progress without a branch, marked", func(t *testing.T) {
		if want := "∅ 77@feat@nowhere"; !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	})
	t.Run("prints a remote-only branch recorded in progress", func(t *testing.T) {
		if want := "origin/" + gone.BranchName; !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	})
	t.Run("a plain row has a blank Created cell", func(t *testing.T) {
		if strings.Contains(out, "0001-01-01") {
			t.Errorf("output shows a zero date:\n%s", out)
		}
	})
}

func TestRunKey(t *testing.T) {
	plain := branch.Row{BranchName: "spike", Plain: true}
	inProgress := branch.Row{BranchName: "42@feat@assign", IssueSlug: "42", Status: branch.StatusInProgress}
	merged := branch.Row{BranchName: "51@fix@prune", IssueSlug: "51", Status: branch.StatusMerged}

	t.Run("C checks out and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "C", &plain)
		cur, _ := rig.client.CurrentBranch()
		if err != nil || reopen || cur != "spike" {
			t.Errorf("err = %v, reopen = %v, current = %q", err, reopen, cur)
		}
	})
	t.Run("c calls closeIssue with the row's slug and branch and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var gotSlug, gotName string
		d.closeIssue = func(_ context.Context, slug, name string) error { gotSlug, gotName = slug, name; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "c", &inProgress)
		if err != nil || reopen || gotSlug != "42" || gotName != "42@feat@assign" {
			t.Errorf("err = %v, reopen = %v, slug = %q, name = %q", err, reopen, gotSlug, gotName)
		}
	})
	t.Run("m merges the plain row and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got SourceBranch
		d.merge = func(_ context.Context, src SourceBranch) error { got = src; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "m", &plain)
		if err != nil || !reopen || got != (SourceBranch{Name: "spike"}) {
			t.Errorf("err = %v, reopen = %v, got = %+v", err, reopen, got)
		}
	})
	t.Run("m on the current row asks the target, checks it out and merges the row into it", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "checkout", "-q", "spike")
		d := rig.deps()
		d.cfg = &config.AppConfig{} // no [branch] base, as in the default config: detected
		var got SourceBranch
		var target string
		d.merge = func(_ context.Context, src SourceBranch) error {
			got = src
			target, _ = rig.client.CurrentBranch()

			return nil
		}
		p := &scriptedBranchMenuPrompter{Target: "master"}
		current := branch.Row{BranchName: "spike", Plain: true, Current: true}
		reopen, err := runKey(t.Context(), d, p, "m", &current)
		if err != nil || !reopen || got != (SourceBranch{Name: "spike"}) || target != "master" {
			t.Errorf("err = %v, reopen = %v, got = %+v, merged into %q", err, reopen, got, target)
		}
		if len(p.TargetsOffered) == 0 || p.TargetsOffered[0] != "master" || slices.Contains(p.TargetsOffered, "spike") {
			t.Errorf("targets offered = %v, want master first and no spike", p.TargetsOffered)
		}
	})
	t.Run("u calls pr and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.pr = func(_ context.Context, name string) error { got = name; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "u", &plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("a records the branch closed and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "a", &inProgress)
		if err != nil || !reopen || rig.statusOf(t, "42@feat@assign") != branch.StatusClosed {
			t.Errorf("err = %v, reopen = %v, status = %q", err, reopen, rig.statusOf(t, "42@feat@assign"))
		}
	})
	t.Run("t calls track and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.track = func(_ context.Context, name string) error { got = name; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "t", &plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("l calls log and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.log = func(_ context.Context, ref string) error { got = ref; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "l", &plain)
		if err != nil || !reopen || got != "refs/heads/spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("D deletes the branch and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "D", &merged)
		if err != nil || !reopen || rig.hasBranch(t, "51@fix@prune") {
			t.Errorf("err = %v, reopen = %v, exists = %v", err, reopen, rig.hasBranch(t, "51@fix@prune"))
		}
	})
	t.Run("a key the row does not offer runs nothing", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "m", &inProgress)
		if !reopen || err == nil || !strings.Contains(err.Error(), "m") {
			t.Errorf("reopen = %v, err = %v", reopen, err)
		}
	})
}
