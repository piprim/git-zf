package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	t.Parallel()

	const porcelain = "worktree /home/u/repo\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /home/u/repo--feat\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/ABC-1@feat@thing\n" +
		"\n" +
		"worktree /home/u/repo--detached\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"detached\n" +
		"\n" +
		"worktree /home/u/repo--gone\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/old\n" +
		"prunable gitdir file points to non-existent location\n" +
		"\n"

	got := parseWorktreeList(porcelain)

	t.Run("four entries", func(t *testing.T) {
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4: %+v", len(got), got)
		}
	})
	t.Run("first entry is the main tree on main", func(t *testing.T) {
		if !got[0].Main || got[0].Path != "/home/u/repo" || got[0].Branch != "main" {
			t.Fatalf("entry 0 = %+v", got[0])
		}
	})
	t.Run("linked entry carries the short branch name", func(t *testing.T) {
		if got[1].Main || got[1].Branch != "ABC-1@feat@thing" || got[1].Path != "/home/u/repo--feat" {
			t.Fatalf("entry 1 = %+v", got[1])
		}
	})
	t.Run("detached entry has an empty branch", func(t *testing.T) {
		if got[2].Branch != "" {
			t.Fatalf("entry 2 = %+v", got[2])
		}
	})
	t.Run("prunable flag is set", func(t *testing.T) {
		if !got[3].Prunable || got[3].Branch != "old" {
			t.Fatalf("entry 3 = %+v", got[3])
		}
	})
	t.Run("empty input yields no entries", func(t *testing.T) {
		if n := len(parseWorktreeList("")); n != 0 {
			t.Fatalf("len = %d, want 0", n)
		}
	})
}

// newRepoWithWorktree returns the main-tree client, the repo dir and the path
// of a linked worktree holding branch "feat/x" cut from main.
func newRepoWithWorktree(t *testing.T) (*Client, string, string) {
	t.Helper()

	c, dir := newDiskRepo(t)
	wt := filepath.Join(t.TempDir(), "repo--feat-x")
	if err := c.CreateWorktree(t.Context(), "feat/x", "main", wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	return c, dir, wt
}

func TestCommonDir(t *testing.T) {
	t.Parallel()

	c, dir, wt := newRepoWithWorktree(t)
	want := filepath.Join(dir, ".git")

	t.Run("main tree", func(t *testing.T) {
		got, err := c.CommonDir()
		if err != nil {
			t.Fatalf("CommonDir: %v", err)
		}
		if !SamePath(got, want) {
			t.Fatalf("CommonDir = %q, want %q", got, want)
		}
	})
	t.Run("linked worktree resolves to the main .git", func(t *testing.T) {
		src, err := NewClientAt(nil, wt)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		got, err := src.CommonDir()
		if err != nil {
			t.Fatalf("CommonDir: %v", err)
		}
		if !SamePath(got, want) {
			t.Fatalf("CommonDir = %q, want %q", got, want)
		}
	})
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	t.Run("removes a clean worktree", func(t *testing.T) {
		t.Parallel()

		c, _, wt := newRepoWithWorktree(t)
		if err := c.RemoveWorktree(t.Context(), wt); err != nil {
			t.Fatalf("RemoveWorktree: %v", err)
		}
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Fatalf("worktree dir still present: %v", err)
		}
		got, err := c.HoldingWorktree(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("HoldingWorktree: %v", err)
		}
		if got != nil {
			t.Fatalf("entry still listed: %+v", got)
		}
	})
	t.Run("refuses a worktree with an untracked file", func(t *testing.T) {
		t.Parallel()

		c, _, wt := newRepoWithWorktree(t)
		writeFile(t, wt, "scratch.txt", "keep me\n")
		err := c.RemoveWorktree(t.Context(), wt)
		if err == nil {
			t.Fatal("expected git to refuse, got nil")
		}
		if _, statErr := os.Stat(filepath.Join(wt, "scratch.txt")); statErr != nil {
			t.Fatalf("untracked file was lost: %v", statErr)
		}
	})
}

func TestMainTree(t *testing.T) {
	t.Parallel()

	c, dir, wt := newRepoWithWorktree(t)

	t.Run("main-tree client returns itself", func(t *testing.T) {
		m, err := c.MainTree()
		if err != nil {
			t.Fatalf("MainTree: %v", err)
		}
		if m != c {
			t.Fatal("expected the same client instance")
		}
	})
	t.Run("worktree client re-anchors on the main tree", func(t *testing.T) {
		src, err := NewClientAt(nil, wt)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		m, err := src.MainTree()
		if err != nil {
			t.Fatalf("MainTree: %v", err)
		}
		root := m.WorkingTreeRoot()
		if !SamePath(root, dir) {
			t.Fatalf("root = %q, want %q", root, dir)
		}
		cur, err := m.CurrentBranch()
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}
		if cur != "main" {
			t.Fatalf("current branch = %q, want main", cur)
		}
	})
}

func TestHoldingWorktree(t *testing.T) {
	t.Parallel()

	c, dir, wt := newRepoWithWorktree(t)

	t.Run("branch held by the main tree returns the main entry", func(t *testing.T) {
		got, err := c.HoldingWorktree(t.Context(), "main")
		if err != nil {
			t.Fatalf("HoldingWorktree: %v", err)
		}
		if got == nil || !got.Main || !SamePath(got.Path, dir) {
			t.Fatalf("HoldingWorktree(main) = %+v, want main entry at %s", got, dir)
		}
	})
	t.Run("branch held by a linked worktree returns that entry", func(t *testing.T) {
		got, err := c.HoldingWorktree(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("HoldingWorktree: %v", err)
		}
		if got == nil || got.Main || !SamePath(got.Path, wt) {
			t.Fatalf("HoldingWorktree(feat/x) = %+v, want linked entry at %s", got, wt)
		}
	})
	t.Run("branch checked out nowhere returns nil", func(t *testing.T) {
		if err := c.CreateBranch("idle", "main"); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}
		// CreateBranch switches the main tree to the new branch; switch back so
		// "idle" is held by no tree.
		runGitInDir(t, dir, "checkout", "-q", "main")
		got, err := c.HoldingWorktree(t.Context(), "idle")
		if err != nil {
			t.Fatalf("HoldingWorktree: %v", err)
		}
		if got != nil {
			t.Fatalf("HoldingWorktree(idle) = %+v, want nil", got)
		}
	})
	t.Run("reports a prunable entry once the directory is gone", func(t *testing.T) {
		if err := os.RemoveAll(wt); err != nil {
			t.Fatalf("remove worktree dir: %v", err)
		}
		got, err := c.HoldingWorktree(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("HoldingWorktree: %v", err)
		}
		if got == nil || !got.Prunable {
			t.Fatalf("HoldingWorktree after rm = %+v, want Prunable", got)
		}
	})
}
