package mergeflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
)

// worktreeRig: master in the main tree, "feature" one commit ahead checked out
// in a linked worktree, and a second client opened on that worktree.
type worktreeRig struct {
	*engineRig
	wtDir string
	src   *git.Client
}

func newWorktreeRig(t *testing.T) *worktreeRig {
	t.Helper()

	rig := initRepo(t)
	wtDir := filepath.Join(t.TempDir(), "repo--feature")
	gitRun(t, rig.dir, "worktree", "add", "-q", "-b", "feature", wtDir, "master")
	writeFile(t, wtDir, "base.txt", "base\nfeature\n")
	gitRun(t, wtDir, "add", "base.txt")
	gitRun(t, wtDir, "commit", "-m", "feat: feature work")

	src, err := git.NewClientAt(rig.client.IO(), wtDir)
	if err != nil {
		t.Fatalf("NewClientAt(worktree): %v", err)
	}

	return &worktreeRig{engineRig: rig, wtDir: wtDir, src: src}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func hasMergeHead(t *testing.T, dir string) bool {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	cmd.Dir = dir

	return cmd.Run() == nil
}

func (r *worktreeRig) assertWorktreeIntact(t *testing.T) {
	t.Helper()

	t.Run("worktree directory still exists", func(t *testing.T) {
		if _, err := os.Stat(r.wtDir); err != nil {
			t.Fatalf("worktree gone: %v", err)
		}
	})
	t.Run("worktree still on feature", func(t *testing.T) {
		if got := gitOut(t, r.wtDir, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
			t.Fatalf("worktree HEAD = %q", got)
		}
	})
	t.Run("main tree still on master", func(t *testing.T) {
		if got := gitOut(t, r.dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "master" {
			t.Fatalf("main HEAD = %q", got)
		}
	})
	t.Run("no merge in progress in either tree", func(t *testing.T) {
		if hasMergeHead(t, r.dir) || hasMergeHead(t, r.wtDir) {
			t.Fatal("MERGE_HEAD left behind")
		}
	})
}

func TestRun_Worktree_Squash(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash, Confirm: true,
		Message: []byte("chore: squash feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("strategy reported", func(t *testing.T) {
		if res.Strategy != commit.MergeStrategySquash {
			t.Fatalf("Strategy = %q", res.Strategy)
		}
	})
	t.Run("master carries the squash commit", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: squash feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Classic(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyClassic, Confirm: true,
		Message: []byte("chore: merge feature into master\n"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("master carries a merge commit with two parents", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
		if parents := gitOut(t, rig.dir, "log", "-1", "--format=%P", "master"); len(strings.Fields(parents)) != 2 {
			t.Fatalf("parents = %q, want two", parents)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Rebase(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("not fast-forward-deferred", func(t *testing.T) {
		if res.FastForwardDeferred {
			t.Fatal("unexpected FastForwardDeferred")
		}
	})
	t.Run("master fast-forwarded to the commit made in the worktree", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: rebase feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
		if a, b := gitOut(t, rig.dir, "rev-parse", "master"), gitOut(t, rig.wtDir, "rev-parse", "feature"); a != b {
			t.Fatalf("master %s != feature %s", a, b)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_RebaseAbortRestoresWorktree(t *testing.T) {
	rig := newWorktreeRig(t)
	origTip := gitOut(t, rig.wtDir, "rev-parse", "feature")
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		MessageErr: errors.New("compose cancelled"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("compose error propagates", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected the compose error")
		}
	})
	t.Run("feature restored to its original tip", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "rev-parse", "feature"); got != origTip {
			t.Fatalf("feature = %s, want %s", got, origTip)
		}
	})
	t.Run("worktree is clean", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "status", "--porcelain"); got != "" {
			t.Fatalf("worktree dirty:\n%s", got)
		}
	})
	t.Run("master untouched", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Rebase_DirtyMainTreeAborts(t *testing.T) {
	rig := newWorktreeRig(t)
	origTip := gitOut(t, rig.wtDir, "rev-parse", "feature")
	writeFile(t, rig.dir, "base.txt", "base\nlocal edit\n")
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("error mentions uncommitted modifications", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "uncommitted modifications") {
			t.Fatalf("err = %v, want uncommitted modifications", err)
		}
	})
	t.Run("master untouched", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	t.Run("feature still at its original tip", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "rev-parse", "feature"); got != origTip {
			t.Fatalf("feature = %s, want %s", got, origTip)
		}
	})
	t.Run("main tree edit preserved", func(t *testing.T) {
		if got := gitOut(t, rig.dir, "status", "--porcelain"); !strings.Contains(got, "base.txt") {
			t.Fatalf("status = %q, want base.txt modified", got)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Rebase_WithPinnedRemote(t *testing.T) {
	rig := newWorktreeRig(t)
	for _, name := range []string{"origin", "upstream"} {
		bare := filepath.Join(t.TempDir(), name+".git")
		gitRun(t, rig.dir, "init", "-q", "--bare", "-b", "master", bare)
		gitRun(t, rig.dir, "remote", "add", name, bare)
		gitRun(t, rig.dir, "push", "-q", name, "master")
	}

	// Remote() caches: rebuild both clients now that the remotes exist.
	client, err := git.NewClientAt(rig.client.IO(), rig.dir)
	if err != nil {
		t.Fatalf("NewClientAt(main): %v", err)
	}
	src, err := git.NewClientAt(rig.client.IO(), rig.wtDir)
	if err != nil {
		t.Fatalf("NewClientAt(worktree): %v", err)
	}
	client.SetRemote("upstream")

	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	res, err := Run(t.Context(), client,
		Params{Source: "feature", Target: "master", SourceClient: src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("not fast-forward-deferred", func(t *testing.T) {
		if res.FastForwardDeferred {
			t.Fatal("unexpected FastForwardDeferred")
		}
	})
	t.Run("source client adopted the pinned remote", func(t *testing.T) {
		if got, _ := src.Remote(); got != "upstream" {
			t.Fatalf("src remote = %q, want upstream", got)
		}
	})
	t.Run("master fast-forwarded to feature tip", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: rebase feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
		if a, b := gitOut(t, rig.dir, "rev-parse", "master"), gitOut(t, rig.wtDir, "rev-parse", "feature"); a != b {
			t.Fatalf("master %s != feature %s", a, b)
		}
	})
}

// Spec Part 4, first row: tracked modifications in the SOURCE worktree must
// abort Rebase in preflight. rebasePreflight runs its dirty check on r.src, a
// different argument than the main-tree check covered by
// TestRun_Worktree_Rebase_DirtyMainTreeAborts.
func TestRun_Worktree_Rebase_DirtySourceWorktreeAborts(t *testing.T) {
	rig := newWorktreeRig(t)
	origTip := gitOut(t, rig.wtDir, "rev-parse", "feature")
	writeFile(t, rig.wtDir, "base.txt", "base\nfeature\nwip\n")
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("error mentions uncommitted modifications", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "uncommitted modifications") {
			t.Fatalf("err = %v, want uncommitted modifications", err)
		}
	})
	t.Run("master untouched", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	t.Run("feature still at its original tip", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "rev-parse", "feature"); got != origTip {
			t.Fatalf("feature = %s, want %s", got, origTip)
		}
	})
	t.Run("uncommitted edit preserved in the worktree", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "status", "--porcelain"); !strings.Contains(got, "base.txt") {
			t.Fatalf("worktree status = %q, want base.txt still modified", got)
		}
	})
	rig.assertWorktreeIntact(t)
}

// Spec Part 4: the fast-forward-deferred path with the source in a worktree.
//
// Constructing it deterministically needs a remote. With no remote, MergeRebase
// merges LOCAL master into the source, so after the commit local master is
// always an ancestor of the source and the post-commit `merge --ff-only` can
// only fail if master moves between the preflight and the fast-forward — not
// reachable from outside the engine. With a remote, MergeRebase merges (and
// soft-resets onto) origin/master instead, so making LOCAL master carry a
// commit that origin/master does not have leaves master off the source's
// ancestry and the fast-forward is refused after the commit has landed.
func TestRun_Worktree_Rebase_FastForwardDeferred(t *testing.T) {
	rig := newWorktreeRig(t)

	bare := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, rig.dir, "init", "-q", "--bare", "-b", "master", bare)
	gitRun(t, rig.dir, "remote", "add", "origin", bare)
	gitRun(t, rig.dir, "push", "-q", "origin", "master")

	// Local master now moves ahead of origin/master, touching a file neither
	// the feature branch nor origin/master knows about (so nothing conflicts).
	writeFile(t, rig.dir, "local.txt", "local\n")
	gitRun(t, rig.dir, "add", "local.txt")
	gitRun(t, rig.dir, "commit", "-m", "chore: local-only commit")

	// Remote() caches: rebuild both clients now that origin exists.
	client, err := git.NewClientAt(rig.client.IO(), rig.dir)
	if err != nil {
		t.Fatalf("NewClientAt(main): %v", err)
	}
	src, err := git.NewClientAt(rig.client.IO(), rig.wtDir)
	if err != nil {
		t.Fatalf("NewClientAt(worktree): %v", err)
	}

	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	res, err := Run(t.Context(), client,
		Params{Source: "feature", Target: "master", SourceClient: src}, prompter, plainPrefill)

	t.Run("no error: the deferral is a Result flag, not a failure", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("fast-forward deferred", func(t *testing.T) {
		if !res.FastForwardDeferred {
			t.Fatal("FastForwardDeferred = false, want true")
		}
	})
	t.Run("the commit landed on feature in the worktree", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "log", "-1", "--format=%s", "feature"); got != "chore: rebase feature into master" {
			t.Fatalf("feature HEAD subject = %q", got)
		}
	})
	t.Run("local master did not move", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: local-only commit" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	t.Run("recovery instructions printed", func(t *testing.T) {
		stderr, ok := client.IO().Err.(interface{ String() string })
		if !ok {
			t.Fatal("stderr is not a buffer")
		}
		if !strings.Contains(stderr.String(), "merge --ff-only") {
			t.Fatalf("stderr = %q, want the recovery hint", stderr.String())
		}
	})
	rig.assertWorktreeIntact(t)
}
