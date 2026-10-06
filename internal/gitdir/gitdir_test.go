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

func TestCommon(t *testing.T) {
	t.Run("main tree: returns the repository's .git", func(t *testing.T) {
		mainDir := t.TempDir()
		initGitRepo(t, mainDir)
		t.Chdir(mainDir)

		got, err := gitdir.Common()
		if err != nil {
			t.Fatalf("Common() error = %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("Common() = %q, want absolute path", got)
		}
		gotReal, _ := filepath.EvalSymlinks(got)
		wantReal, _ := filepath.EvalSymlinks(filepath.Join(mainDir, ".git"))
		if gotReal != wantReal {
			t.Errorf("Common() = %q, want %q", gotReal, wantReal)
		}
	})

	t.Run("outside a repository: returns an error", func(t *testing.T) {
		t.Chdir(t.TempDir()) // plain directory, no git repo

		if _, err := gitdir.Common(); err == nil {
			t.Fatal("Common() error = nil, want non-nil outside a git repo")
		}
	})

	t.Run("linked worktree: returns the main .git", func(t *testing.T) {
		mainDir := t.TempDir()
		initGitRepo(t, mainDir)

		worktreeDir := t.TempDir()
		cmd := exec.Command("git", "worktree", "add", "-b", "wt-branch", worktreeDir, "main")
		cmd.Dir = mainDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v\n%s", err, out)
		}

		t.Chdir(worktreeDir)

		got, err := gitdir.Common()
		if err != nil {
			t.Fatalf("Common() error = %v", err)
		}
		gotReal, _ := filepath.EvalSymlinks(got)
		wantReal, _ := filepath.EvalSymlinks(filepath.Join(mainDir, ".git"))
		if gotReal != wantReal {
			t.Errorf("Common() = %q, want %q", gotReal, wantReal)
		}
		if strings.Contains(got, "worktrees") {
			t.Errorf("Common() = %q, must not be the per-worktree dir", got)
		}
	})
}
