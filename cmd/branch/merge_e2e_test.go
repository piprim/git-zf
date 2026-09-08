package branch

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

// mergeRig bundles a real on-disk repo + seeded store for branch-merge E2E
// tests. The repo starts with one commit on master; helpers add local and
// origin-only branches. The client is rebuilt after remote changes so Remote()
// sees them.
type mergeRig struct {
	dir       string
	originDir string
	stdout    *bytes.Buffer
	stderr    *bytes.Buffer
	client    *git.Client
	store     *store.Store
	cfg       *config.AppConfig
}

func newMergeRig(t *testing.T) *mergeRig {
	t.Helper()

	dir := t.TempDir()
	r := &mergeRig{dir: dir, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	r.git(t, "init", "-q", "-b", "master")
	r.git(t, "config", "user.name", "Test User")
	r.git(t, "config", "user.email", "test@test.com")
	r.git(t, "config", "commit.gpgsign", "false")
	mergeWrite(t, dir, "base.txt", "base\n")
	r.git(t, "add", "base.txt")
	r.git(t, "commit", "-m", "chore: init")

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r.store = s

	r.cfg = &config.AppConfig{}
	r.cfg.Branch.Base = "master"
	r.cfg.Push.Propose = true // production default; enables the propose-push step

	r.rebuildClient(t)

	return r
}

func (r *mergeRig) rebuildClient(t *testing.T) {
	t.Helper()

	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: r.stdout, Err: r.stderr}
	c, err := git.NewClientAt(ioStreams, r.dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}
	r.client = c
}

func (r *mergeRig) git(t *testing.T, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *mergeRig) gitOK(t *testing.T, args ...string) bool {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = r.dir

	return cmd.Run() == nil
}

func (r *mergeRig) gitOut(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func (r *mergeRig) branchExists(t *testing.T, name string) bool {
	return r.gitOK(t, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
}

func (r *mergeRig) headSubject(t *testing.T, ref string) string {
	return r.gitOut(t, "log", "-1", "--format=%s", ref)
}

// addLocalBranch creates a local branch off master with one commit, then
// returns HEAD to master.
func (r *mergeRig) addLocalBranch(t *testing.T, name string) {
	t.Helper()

	r.git(t, "switch", "-c", name)
	mergeWrite(t, r.dir, name+".txt", name+"\n")
	r.git(t, "add", name+".txt")
	r.git(t, "commit", "-m", "feat: "+name)
	r.git(t, "switch", "master")
}

// addOrigin wires a bare remote and pushes master to it.
func (r *mergeRig) addOrigin(t *testing.T) {
	t.Helper()

	r.originDir = t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "-b", "master", r.originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	r.git(t, "remote", "add", "origin", r.originDir)
	r.git(t, "push", "-q", "origin", "master")
	r.rebuildClient(t)
}

// addOriginOnlyBranch creates a branch (off base) that exists only on origin.
func (r *mergeRig) addOriginOnlyBranch(t *testing.T, name, base string) {
	t.Helper()

	r.git(t, "switch", "-c", name, base)
	mergeWrite(t, r.dir, name+".txt", name+"\n")
	r.git(t, "add", name+".txt")
	r.git(t, "commit", "-m", "feat: "+name)
	r.git(t, "push", "-q", "origin", name)
	r.git(t, "switch", "master")
	r.git(t, "branch", "-D", name)
	r.rebuildClient(t)
}

func (r *mergeRig) deps(pushConfirm pushflow.ConfirmFunc) mergeDeps {
	return mergeDeps{client: r.client, store: r.store, cfg: r.cfg, pushConfirm: pushConfirm}
}

func mergeWrite(t *testing.T, dir, name, body string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func declinePush(context.Context, string) (bool, error) { return false, nil }
func acceptPush(context.Context, string) (bool, error)  { return true, nil }

func TestRunMerge_HappyPath_Squash(t *testing.T) {
	rig := newMergeRig(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:       SourceBranch{Name: "feature"},
		Strategy:     commit.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("chore: merge feature\n"),
		DeleteSource: false,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge feature" {
			t.Fatalf("master HEAD = %q", got)
		}
	})
	t.Run("source kept when delete declined", func(t *testing.T) {
		if !rig.branchExists(t, "feature") {
			t.Fatal("feature branch was deleted despite decline")
		}
	})
}

func TestRunMerge_HappyPath_Classic(t *testing.T) {
	rig := newMergeRig(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "feature"},
		Strategy: commit.MergeStrategyClassic,
		Confirm:  true,
		Message:  []byte("chore: classic merge feature\n"),
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("merge commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: classic merge feature" {
			t.Fatalf("master HEAD = %q", got)
		}
	})
}

func TestRunMerge_HappyPath_Rebase(t *testing.T) {
	rig := newMergeRig(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "feature"},
		Strategy: commit.MergeStrategyRebase,
		Confirm:  true,
		Message:  []byte("chore: rebase feature\n"),
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("master fast-forwarded to rebased commit", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: rebase feature" {
			t.Fatalf("master HEAD = %q", got)
		}
	})
}

func TestRunMerge_DeletesSource(t *testing.T) {
	rig := newMergeRig(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:       SourceBranch{Name: "feature"},
		Strategy:     commit.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("chore: merge feature\n"),
		DeleteSource: true,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("source branch deleted", func(t *testing.T) {
		if rig.branchExists(t, "feature") {
			t.Fatal("feature branch should have been deleted")
		}
	})
	t.Run("delete was confirmed once", func(t *testing.T) {
		if p.ConfirmDeleteCalls != 1 {
			t.Fatalf("ConfirmDeleteCalls = %d, want 1", p.ConfirmDeleteCalls)
		}
	})
}

func TestRunMerge_RefusesIssueBranchSource(t *testing.T) {
	t.Run("local issue branch is refused", func(t *testing.T) {
		rig := newMergeRig(t)
		rig.addLocalBranch(t, "ABC-1@feat@add-thing")
		p := &scriptedMergePrompter{
			Source:   SourceBranch{Name: "ABC-1@feat@add-thing"},
			Strategy: commit.MergeStrategySquash,
			Confirm:  true,
			Message:  []byte("should not commit\n"),
		}

		if err := runMerge(t.Context(), rig.deps(declinePush), p); err != nil {
			t.Fatalf("runMerge: %v", err)
		}

		if got := rig.stdout.String(); !strings.Contains(got, "git zf issue close") {
			t.Fatalf("stdout = %q, want redirect to issue close", got)
		}
		if p.PickStrategyCalls != 0 {
			t.Fatalf("strategy picked %d times, want 0 (refused before merge)", p.PickStrategyCalls)
		}
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master advanced to %q; refusal should not merge", got)
		}
	})

	t.Run("remote-only issue branch is refused without materializing", func(t *testing.T) {
		rig := newMergeRig(t)
		rig.addOrigin(t)
		rig.addOriginOnlyBranch(t, "ABC-2@feat@remote", "master")
		p := &scriptedMergePrompter{
			Source:   SourceBranch{Name: "ABC-2@feat@remote", RemoteOnly: true},
			Strategy: commit.MergeStrategySquash,
			Confirm:  true,
			Message:  []byte("should not commit\n"),
		}

		if err := runMerge(t.Context(), rig.deps(declinePush), p); err != nil {
			t.Fatalf("runMerge: %v", err)
		}

		if got := rig.stdout.String(); !strings.Contains(got, "git zf issue close") {
			t.Fatalf("stdout = %q, want redirect to issue close", got)
		}
		if rig.branchExists(t, "ABC-2@feat@remote") {
			t.Fatal("remote-only issue branch was materialized; refusal must happen before materialize")
		}
	})
}

func TestRunMerge_DetachedHead_Errors(t *testing.T) {
	rig := newMergeRig(t)
	rig.git(t, "checkout", "--detach")
	p := &scriptedMergePrompter{}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("returns an error about checking out a branch", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "checkout a branch") {
			t.Fatalf("err = %v, want a 'checkout a branch' error", err)
		}
	})
}

func TestRunMerge_NoOtherBranches(t *testing.T) {
	rig := newMergeRig(t)
	p := &scriptedMergePrompter{}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("prints the no-branches message", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "No other branches to merge") {
			t.Fatalf("stdout = %q", got)
		}
	})
	t.Run("never opened the source picker", func(t *testing.T) {
		if p.PickSourceCalls != 0 {
			t.Fatalf("PickSourceCalls = %d, want 0", p.PickSourceCalls)
		}
	})
}

func TestRunMerge_AbortAtConfirm(t *testing.T) {
	rig := newMergeRig(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "feature"},
		Strategy: commit.MergeStrategySquash,
		Confirm:  false,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("prints Aborted and leaves master unchanged", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
			t.Fatalf("stdout = %q, want 'Aborted.'", got)
		}
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master advanced to %q on abort", got)
		}
	})
}

func TestRunMerge_RemoteOnlySource_Materializes(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t)
	rig.addOriginOnlyBranch(t, "spike", "master")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "spike", RemoteOnly: true},
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: merge spike\n"),
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge spike" {
			t.Fatalf("master HEAD = %q", got)
		}
	})
	t.Run("remote-only source was materialized locally", func(t *testing.T) {
		if !rig.branchExists(t, "spike") {
			t.Fatal("spike should have been materialized as a local branch")
		}
	})
}

func TestRunMerge_RemoteOnlySource_AbortRollsBackMaterialized(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t)
	rig.addOriginOnlyBranch(t, "spike", "master")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "spike", RemoteOnly: true},
		Strategy: commit.MergeStrategySquash,
		Confirm:  false, // decline at confirm → engine aborts before committing
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("prints Aborted", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
			t.Fatalf("stdout = %q, want 'Aborted.'", got)
		}
	})
	t.Run("materialized branch rolled back (no orphan)", func(t *testing.T) {
		if rig.branchExists(t, "spike") {
			t.Fatal("materialized spike should have been rolled back on abort")
		}
	})
}

func TestRunMerge_DeletesRemoteSourceBranch(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t)
	// A branch present both locally and on origin.
	rig.addLocalBranch(t, "feature")
	rig.git(t, "push", "-q", "origin", "feature")
	rig.rebuildClient(t)

	p := &scriptedMergePrompter{
		Source:       SourceBranch{Name: "feature"},
		Strategy:     commit.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("chore: merge feature\n"),
		DeleteSource: true,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("local source deleted", func(t *testing.T) {
		if rig.branchExists(t, "feature") {
			t.Fatal("local feature should have been deleted")
		}
	})
	t.Run("remote source deleted", func(t *testing.T) {
		if out := rig.gitOut(t, "ls-remote", "origin", "refs/heads/feature"); out != "" {
			t.Fatalf("origin still has feature: %q", out)
		}
	})
}

func TestRunMerge_ProposesPush(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t)
	rig.addLocalBranch(t, "feature")
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "feature"},
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: merge feature\n"),
	}

	err := runMerge(t.Context(), rig.deps(acceptPush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("origin master advanced to the merged tip", func(t *testing.T) {
		local := rig.gitOut(t, "rev-parse", "master")
		remote := rig.gitOut(t, "ls-remote", "origin", "refs/heads/master")
		if !strings.HasPrefix(remote, local) {
			t.Fatalf("origin master = %q, want it to start with local master %q", remote, local)
		}
	})
}

func TestRunMerge_RemoteOnlyRebase_FFDeferred_KeepsMaterialized(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t) // origin/master = init
	// spike is based on origin/master and lives only on origin.
	rig.addOriginOnlyBranch(t, "spike", "origin/master")
	// Local master gains a commit NOT on origin/master, so the post-rebase
	// fast-forward of local master cannot land → FastForwardDeferred.
	mergeWrite(t, rig.dir, "local.txt", "local\n")
	rig.git(t, "add", "local.txt")
	rig.git(t, "commit", "-m", "chore: local-only master commit")
	rig.rebuildClient(t)

	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "spike", RemoteOnly: true},
		Strategy: commit.MergeStrategyRebase,
		Confirm:  true,
		Message:  []byte("chore: rebase spike\n"),
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error (FF-deferred is a clean exit)", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("reports the manual fast-forward", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "fast-forward") {
			t.Fatalf("stdout = %q, want a fast-forward hint", got)
		}
	})
	t.Run("materialized branch survives with its rebased commits", func(t *testing.T) {
		if !rig.branchExists(t, "spike") {
			t.Fatal("spike was deleted; FF-deferred must NOT roll back the materialized branch")
		}
	})
	t.Run("local master not advanced (fast-forward deferred)", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: local-only master commit" {
			t.Fatalf("master HEAD = %q, want it unchanged", got)
		}
	})
}
