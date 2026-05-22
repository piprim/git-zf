package issue

import (
	"path/filepath"
	"testing"
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
