package branch

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
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

func (r *pruneTestRig) deps() tableDeps {
	return tableDeps{client: r.client, stdout: r.stdout, stderr: r.stderr}
}

func (r *pruneTestRig) hasBranch(t *testing.T, name string) bool {
	t.Helper()

	ok, err := r.client.BranchExists(name)
	if err != nil {
		t.Fatalf("BranchExists(%s): %v", name, err)
	}

	return ok
}

func TestRunBranchAction(t *testing.T) {
	plain := branch.Row{BranchName: "spike", Plain: true}
	inProgress := branch.Row{BranchName: "42@feat@assign", IssueSlug: "42", Status: branch.StatusInProgress}
	merged := branch.Row{BranchName: "51@fix@prune", IssueSlug: "51", Status: branch.StatusMerged}

	t.Run("checkout switches to a plain branch and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, plain)
		cur, _ := rig.client.CurrentBranch()
		if err != nil || reopen || cur != "spike" {
			t.Errorf("err = %v, reopen = %v, current = %q", err, reopen, cur)
		}
	})
	t.Run("checkout refuses a branch held by a worktree", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "worktree", "add", "-q", t.TempDir()+"/wt", "spike")
		_, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, plain)
		if err == nil || !strings.Contains(err.Error(), "worktree") {
			t.Errorf("err = %v, want a worktree refusal", err)
		}
	})
	t.Run("checkout of a remote-only branch creates it with an upstream", func(t *testing.T) {
		rig := newTableRig(t)
		row := branch.Row{BranchName: "remote-thing", Plain: true, RemoteOnly: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionCheckout, row); err != nil {
			t.Fatalf("checkout: %v", err)
		}
		if up := gitIn(t, rig.dir, "rev-parse", "--abbrev-ref", "remote-thing@{upstream}"); up != "origin/remote-thing" {
			t.Errorf("upstream = %q", up)
		}
	})
	t.Run("delete removes a merged branch", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{}
		reopen, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, merged)
		if err != nil || !reopen || rig.hasBranch(t, "51@fix@prune") || p.ForceAsked {
			t.Errorf("err = %v, reopen = %v, exists = %v, asked = %v", err, reopen, rig.hasBranch(t, "51@fix@prune"), p.ForceAsked)
		}
	})
	t.Run("delete of an unmerged branch asks and force-deletes on yes", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, plain)
		if err != nil || !p.ForceAsked || rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, asked = %v, exists = %v", err, p.ForceAsked, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("delete of an unmerged branch keeps it on no", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: false}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, plain)
		if err != nil || !p.ForceAsked || !rig.hasBranch(t, "spike") {
			t.Errorf("err = %v, asked = %v, exists = %v", err, p.ForceAsked, rig.hasBranch(t, "spike"))
		}
	})
	t.Run("delete of a worktree-held branch does not ask", func(t *testing.T) {
		rig := newTableRig(t)
		gitIn(t, rig.dir, "worktree", "add", "-q", t.TempDir()+"/wt", "51@fix@prune")
		p := &scriptedBranchMenuPrompter{Force: true}
		_, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, merged)
		if err == nil || p.ForceAsked || !rig.hasBranch(t, "51@fix@prune") {
			t.Errorf("err = %v, asked = %v, exists = %v", err, p.ForceAsked, rig.hasBranch(t, "51@fix@prune"))
		}
	})
	t.Run("delete of an in-progress tracked branch keeps its record and names prune", func(t *testing.T) {
		rig := newTableRig(t)
		p := &scriptedBranchMenuPrompter{Force: true}
		if _, err := runBranchAction(t.Context(), rig.deps(), p, actionDelete, inProgress); err != nil {
			t.Fatalf("delete: %v", err)
		}
		out := rig.stdout.String()
		if st := rig.statusOf(t, "42@feat@assign"); st != branch.StatusInProgress || !strings.Contains(out, "still in progress") || !strings.Contains(out, "prune") {
			t.Errorf("status = %q, stdout:\n%s", st, out)
		}
	})
	t.Run("delete of a plain branch prints no prune reminder", func(t *testing.T) {
		rig := newTableRig(t)
		if _, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{Force: true}, actionDelete, plain); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if out := rig.stdout.String(); strings.Contains(out, "still in progress") || !strings.Contains(out, "deleted") {
			t.Errorf("stdout:\n%s", out)
		}
	})
	t.Run("abandon records the row closed without merging", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runBranchAction(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, actionAbandon, inProgress)
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
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionPR, plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("merge into current merges the plain row", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got SourceBranch
		d.merge = func(_ context.Context, src SourceBranch) error { got = src; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionMerge, plain)
		if err != nil || !reopen || got != (SourceBranch{Name: "spike"}) {
			t.Errorf("err = %v, reopen = %v, got = %+v", err, reopen, got)
		}
	})
	t.Run("issue close calls its func with the slug and branch and does not reopen", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var gotSlug, gotName string
		d.closeIssue = func(_ context.Context, slug, name string) error { gotSlug, gotName = slug, name; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionIssueClose, inProgress)
		if err != nil || reopen || gotSlug != "42" || gotName != "42@feat@assign" {
			t.Errorf("err = %v, reopen = %v, slug = %q, name = %q", err, reopen, gotSlug, gotName)
		}
	})
	t.Run("log calls its func with the branch's full ref and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.log = func(_ context.Context, ref string) error { got = ref; return nil }
		reopen, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionLog, plain)
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
		if _, err := runBranchAction(t.Context(), d, &scriptedBranchMenuPrompter{}, actionLog, row); err != nil || got != "refs/remotes/origin/remote-thing" {
			t.Errorf("err = %v, got = %q", err, got)
		}
	})
	t.Run("an entry with no func is not offered", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		if got := offeredEntries(d, plain); slices.Contains(got, actionTrack) || slices.Contains(got, actionMerge) || !slices.Contains(got, actionDelete) {
			t.Errorf("offered without funcs = %v", got)
		}
		d.track = func(context.Context, string) error { return nil }
		if got := offeredEntries(d, plain); !slices.Contains(got, actionTrack) {
			t.Errorf("offered with track = %v", got)
		}
	})
}

func TestRunBranchTable_NoTTY(t *testing.T) {
	rig := newTableRig(t)
	err := runBranchTable(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, tui.BranchTableOptions{Remote: "origin"}, true)
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
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "C", plain)
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
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "c", inProgress)
		if err != nil || reopen || gotSlug != "42" || gotName != "42@feat@assign" {
			t.Errorf("err = %v, reopen = %v, slug = %q, name = %q", err, reopen, gotSlug, gotName)
		}
	})
	t.Run("m merges the plain row and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got SourceBranch
		d.merge = func(_ context.Context, src SourceBranch) error { got = src; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "m", plain)
		if err != nil || !reopen || got != (SourceBranch{Name: "spike"}) {
			t.Errorf("err = %v, reopen = %v, got = %+v", err, reopen, got)
		}
	})
	t.Run("u calls pr and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.pr = func(_ context.Context, name string) error { got = name; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "u", plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("a records the branch closed and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "a", inProgress)
		if err != nil || !reopen || rig.statusOf(t, "42@feat@assign") != branch.StatusClosed {
			t.Errorf("err = %v, reopen = %v, status = %q", err, reopen, rig.statusOf(t, "42@feat@assign"))
		}
	})
	t.Run("t calls track and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.track = func(_ context.Context, name string) error { got = name; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "t", plain)
		if err != nil || !reopen || got != "spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("l calls log and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		d := rig.deps()
		var got string
		d.log = func(_ context.Context, ref string) error { got = ref; return nil }
		reopen, err := runKey(t.Context(), d, &scriptedBranchMenuPrompter{}, "l", plain)
		if err != nil || !reopen || got != "refs/heads/spike" {
			t.Errorf("err = %v, reopen = %v, got = %q", err, reopen, got)
		}
	})
	t.Run("D deletes the branch and reopens", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "D", merged)
		if err != nil || !reopen || rig.hasBranch(t, "51@fix@prune") {
			t.Errorf("err = %v, reopen = %v, exists = %v", err, reopen, rig.hasBranch(t, "51@fix@prune"))
		}
	})
	t.Run("a key the row does not offer runs nothing", func(t *testing.T) {
		rig := newTableRig(t)
		reopen, err := runKey(t.Context(), rig.deps(), &scriptedBranchMenuPrompter{}, "m", inProgress)
		if !reopen || err == nil || !strings.Contains(err.Error(), "m") {
			t.Errorf("reopen = %v, err = %v", reopen, err)
		}
	})
}
