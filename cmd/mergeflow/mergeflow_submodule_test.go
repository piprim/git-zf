package mergeflow

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
)

// A merge moves the commit recorded for a submodule and leaves the
// submodule's checkout where it was: Run brings it along, and only it.
// Not parallel: t.Setenv lets git clone a submodule from a local path.
func TestRun_UpdatesMovedSubmodules(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	rig := initRepo(t)
	lib, other := initRepo(t).dir, initRepo(t).dir
	gitRun(t, rig.dir, "submodule", "add", lib, "lib")
	gitRun(t, rig.dir, "submodule", "add", other, "other")
	gitRun(t, rig.dir, "commit", "-m", "chore: add submodules")

	// Both sources get a new commit; only feature records lib's.
	for _, src := range []string{lib, other} {
		writeFile(t, src, "new.txt", "new\n")
		gitRun(t, src, "add", "new.txt")
		gitRun(t, src, "commit", "-m", "feat: new")
	}
	libNew := gitOut(t, lib, "rev-parse", "HEAD")
	otherNew := gitOut(t, other, "rev-parse", "HEAD")

	gitRun(t, rig.dir, "switch", "-c", "feature")
	gitRun(t, filepath.Join(rig.dir, "lib"), "pull", "-q", "origin", "master")
	gitRun(t, rig.dir, "commit", "-am", "feat: bump lib")
	gitRun(t, rig.dir, "switch", "master")
	gitRun(t, rig.dir, "submodule", "update")
	// The user's own, uncommitted move of an unrelated submodule.
	gitRun(t, filepath.Join(rig.dir, "other"), "pull", "-q", "origin", "master")

	_, err := Run(t.Context(), rig.client, Params{Source: "feature", Target: "master"},
		&scriptedMergePrompter{
			Strategy: commit.MergeStrategySquash,
			Confirm:  true,
			Message:  []byte("chore: squash feature into master\n"),
		}, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("the submodule the merge moved is checked out at its new commit", func(t *testing.T) {
		if got := gitOut(t, filepath.Join(rig.dir, "lib"), "rev-parse", "HEAD"); got != libNew {
			t.Fatalf("lib HEAD = %s, want %s", got, libNew)
		}
	})
	t.Run("a submodule the merge did not touch stays where the user put it", func(t *testing.T) {
		if got := gitOut(t, filepath.Join(rig.dir, "other"), "rev-parse", "HEAD"); got != otherNew {
			t.Fatalf("other HEAD = %s, want %s", got, otherNew)
		}
	})
	t.Run("only the user's own move shows in the status", func(t *testing.T) {
		if got := strings.TrimSpace(rig.statusPorcelain(t)); got != "M other" {
			t.Fatalf("status = %q", got)
		}
	})
}

// The rebase merges the target into the source where the source is checked
// out: a submodule the target moved goes stale in that worktree, which then
// reads as modified and cannot be removed.
// Not parallel: t.Setenv lets git clone a submodule from a local path.
func TestRun_UpdatesMovedSubmodules_SourceWorktree(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	rig := initRepo(t)
	lib := initRepo(t).dir
	gitRun(t, rig.dir, "submodule", "add", lib, "lib")
	gitRun(t, rig.dir, "commit", "-m", "chore: add submodule")

	wtDir := filepath.Join(t.TempDir(), "repo--feature")
	gitRun(t, rig.dir, "worktree", "add", "-q", "-b", "feature", wtDir, "master")
	gitRun(t, wtDir, "submodule", "update", "--init")
	writeFile(t, wtDir, "base.txt", "base\nfeature\n")
	gitRun(t, wtDir, "commit", "-am", "feat: feature work")

	// master moves lib after the feature branched.
	writeFile(t, lib, "new.txt", "new\n")
	gitRun(t, lib, "add", "new.txt")
	gitRun(t, lib, "commit", "-m", "feat: new")
	libNew := gitOut(t, lib, "rev-parse", "HEAD")
	gitRun(t, filepath.Join(rig.dir, "lib"), "pull", "-q", "origin", "master")
	gitRun(t, rig.dir, "commit", "-am", "chore: bump lib")

	src, err := git.NewClientAt(rig.client.IO(), wtDir)
	if err != nil {
		t.Fatalf("NewClientAt(worktree): %v", err)
	}

	_, err = Run(t.Context(), rig.client, Params{Source: "feature", Target: "master", SourceClient: src},
		&scriptedMergePrompter{
			Strategy: commit.MergeStrategyRebase,
			Confirm:  true,
			Message:  []byte("chore: rebase feature into master\n"),
		}, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("the source worktree's submodule follows the target's move", func(t *testing.T) {
		if got := gitOut(t, filepath.Join(wtDir, "lib"), "rev-parse", "HEAD"); got != libNew {
			t.Fatalf("lib HEAD = %s, want %s", got, libNew)
		}
	})
	t.Run("the source worktree is clean", func(t *testing.T) {
		if got := gitOut(t, wtDir, "status", "--porcelain"); got != "" {
			t.Fatalf("status = %q", got)
		}
	})
}
