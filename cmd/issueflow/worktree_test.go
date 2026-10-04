package issueflow

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

func TestWorktreePath(t *testing.T) {
	t.Parallel()

	t.Run("uses sibling of repo root when worktreeDir is empty", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "", "myapp", "feat-123-login")
		want := "/home/user/code/myapp--feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("uses configured worktreeDir as base", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "/worktrees", "myapp", "feat-123-login")
		want := "/worktrees/myapp--feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("expands tilde in worktreeDir", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "~/worktrees", "myapp", "feat-123-login")
		// ~ expansion produces an absolute path that must not start with ~
		if got[:1] == "~" {
			t.Errorf("tilde was not expanded: %q", got)
		}
		if filepath.Base(got) != "myapp--feat-123-login" {
			t.Errorf("unexpected basename %q", filepath.Base(got))
		}
	})
}

// TestResolveParentSlug_fromLinkedWorktree guards the store location used by
// the parent lookup: a client opened inside a linked worktree must read the
// repo's shared store, not an empty per-worktree one. The base branch name is
// deliberately not a git-zf name so the branch.Parse fallback cannot mask a
// wrong store path.
func TestResolveParentSlug_fromLinkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainDir, "init", "-q", "-b", "main")
	runGit(mainDir, "config", "user.name", "Test")
	runGit(mainDir, "config", "user.email", "test@example.com")
	runGit(mainDir, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(mainDir, "branch", "integration", "main")

	mainStore, err := store.Open(t.Context(), filepath.Join(mainDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := mainStore.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "PARENT-1", Title: "Parent"},
		&store.Branch{Name: "integration", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = mainStore.Close()

	wtDir := filepath.Join(t.TempDir(), "repo--wt")
	runGit(mainDir, "worktree", "add", "-q", "-b", "wt-branch", wtDir, "main")

	wtClient, err := git.NewClientAt(nil, wtDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	t.Run("parent slug resolved through the shared store", func(t *testing.T) {
		if got := resolveParentSlug(t.Context(), wtClient, "integration"); got != "PARENT-1" {
			t.Fatalf("resolveParentSlug = %q, want PARENT-1", got)
		}
	})
}
