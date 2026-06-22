package gitdir_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/internal/gitdir"
)

// initGitRepo runs git init in dir and makes one commit so the repo is valid.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestGet_insideGitRepo(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	t.Chdir(dir)

	got, err := gitdir.Get()
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	t.Run("returns an absolute path", func(t *testing.T) {
		if !filepath.IsAbs(got) {
			t.Errorf("Get() = %q, want absolute path", got)
		}
	})

	t.Run("path ends with .git", func(t *testing.T) {
		if filepath.Base(got) != ".git" {
			t.Errorf("Get() = %q, want path ending in .git", got)
		}
	})

	t.Run("path is under the repo root", func(t *testing.T) {
		if !strings.HasPrefix(got, dir) {
			t.Errorf("Get() = %q, want path under %q", got, dir)
		}
	})
}

func TestGet_outsideGitRepo(t *testing.T) {
	t.Chdir(t.TempDir()) // plain directory, no git repo

	_, err := gitdir.Get()
	if err == nil {
		t.Fatal("Get() error = nil, want non-nil outside a git repo")
	}
}

func TestGet_linkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	initGitRepo(t, mainDir)

	worktreeDir := t.TempDir()

	// Add a linked worktree on a new branch.
	cmd := exec.Command("git", "worktree", "add", "--orphan", "-b", "wt-branch", worktreeDir)
	cmd.Dir = mainDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed (may need git >= 2.15): %v\n%s", err, out)
	}

	t.Chdir(worktreeDir)

	got, err := gitdir.Get()
	if err != nil {
		t.Fatalf("Get() inside linked worktree error = %v, want nil", err)
	}

	if !filepath.IsAbs(got) {
		t.Errorf("Get() = %q, want absolute path", got)
	}

	// A linked worktree's git dir lives under the main repo's .git/worktrees/.
	if !strings.Contains(got, ".git") {
		t.Errorf("Get() = %q, want path containing .git", got)
	}
}
