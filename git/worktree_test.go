package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/internal/pkg"
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

// addSubmodule adds the repository at url as submodule name of the repo at
// repoDir and commits it on the current branch.
func addSubmodule(t *testing.T, repoDir, url, name string) {
	t.Helper()

	mustGit(t, repoDir, "submodule", "add", url, name)
	mustGit(t, repoDir, "commit", "-m", "chore: add submodule")
}

// Not parallel: t.Setenv lets git clone a submodule from a local path.
func TestCreateWorktree_Submodules(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	// repo returns a client writing Err to the returned buffer, and its dir.
	repo := func(t *testing.T) (*Client, string, *bytes.Buffer) {
		t.Helper()

		_, dir := newDiskRepo(t)
		errBuf := &bytes.Buffer{}

		c, err := NewClientAt(&pkg.IO{Out: io.Discard, Err: errBuf}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		return c, dir, errBuf
	}

	t.Run("submodule on base branch is checked out even when main tree has no .gitmodules", func(t *testing.T) {
		c, dir, errBuf := repo(t)
		_, src := newDiskRepo(t)

		mustGit(t, dir, "checkout", "-q", "-b", "with-sub")
		addSubmodule(t, dir, src, "lib")
		mustGit(t, dir, "checkout", "-q", "main")

		if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err == nil {
			t.Fatal("main tree still has .gitmodules")
		}

		wt := filepath.Join(t.TempDir(), "wt")
		if err := c.CreateWorktree(t.Context(), "feat/sub", "with-sub", wt); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
		if _, err := os.Stat(filepath.Join(wt, "lib", "base.go")); err != nil {
			t.Errorf("submodule not checked out: %v", err)
		}
		if strings.Contains(errBuf.String(), "warning:") {
			t.Errorf("unexpected warning:\n%s", errBuf)
		}
	})

	t.Run("no submodules: nothing runs, no warning is printed", func(t *testing.T) {
		c, _, errBuf := repo(t)

		if err := c.CreateWorktree(t.Context(), "feat/sub", "main", filepath.Join(t.TempDir(), "wt")); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
		if got := errBuf.String(); strings.Contains(got, "warning:") || strings.Contains(got, "submodule") {
			t.Errorf("unexpected output on Err:\n%s", got)
		}
	})

	t.Run("unreachable submodule warns, keeps the worktree, and still excludes in-repo dir", func(t *testing.T) {
		c, dir, errBuf := repo(t)
		_, src := newDiskRepo(t)

		addSubmodule(t, dir, src, "lib")
		if err := os.RemoveAll(src); err != nil {
			t.Fatalf("remove submodule source: %v", err)
		}

		wt := filepath.Join(dir, ".worktrees", "feat-sub")
		if err := c.CreateWorktree(t.Context(), "feat/sub", "main", wt); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
		if _, err := os.Stat(wt); err != nil {
			t.Errorf("worktree dir missing: %v", err)
		}
		for _, want := range []string{"warning:", "git -C " + wt + " submodule update --init --recursive"} {
			if !strings.Contains(errBuf.String(), want) {
				t.Errorf("Err lacks %q:\n%s", want, errBuf)
			}
		}

		exclude, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
		if err != nil {
			t.Fatalf("read exclude: %v", err)
		}
		if !strings.Contains(string(exclude), "/.worktrees/\n") {
			t.Errorf("exclude lacks /.worktrees/:\n%s", exclude)
		}
	})

	t.Run("unreachable submodule with nil client IO does not panic", func(t *testing.T) {
		c, dir := newDiskRepo(t)
		_, src := newDiskRepo(t)

		addSubmodule(t, dir, src, "lib")
		if err := os.RemoveAll(src); err != nil {
			t.Fatalf("remove submodule source: %v", err)
		}

		if err := c.CreateWorktree(t.Context(), "feat/sub", "main", filepath.Join(t.TempDir(), "wt")); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}
	})
}

// git refuses to remove a worktree holding submodules, clean or not:
// RemoveWorktree forces it, unless the worktree holds work that exists
// nowhere else. Not parallel: t.Setenv lets git clone a local submodule.
func TestRemoveWorktree_Submodules(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	// rig returns a client, and a worktree of it holding the submodule "lib".
	rig := func(t *testing.T) (*Client, string) {
		t.Helper()

		c, dir := newDiskRepo(t)
		_, src := newDiskRepo(t)
		addSubmodule(t, dir, src, "lib")

		wt := filepath.Join(t.TempDir(), "wt")
		if err := c.CreateWorktree(t.Context(), "feat/sub", "main", wt); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}

		return c, wt
	}
	// commitInLib commits a file in the worktree's submodule.
	commitInLib := func(t *testing.T, wt string) {
		t.Helper()

		lib := filepath.Join(wt, "lib")
		if err := os.WriteFile(filepath.Join(lib, "new.go"), []byte("package lib\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, lib, "add", "new.go")
		mustGit(t, lib, "-c", "user.name=T", "-c", "user.email=t@t", "commit", "-q", "-m", "feat: new")
	}
	refused := func(t *testing.T, err error, wt, want string) {
		t.Helper()

		if !errors.Is(err, ErrWorktreeHoldsWork) || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want ErrWorktreeHoldsWork naming %q", err, want)
		}
		if _, statErr := os.Stat(wt); statErr != nil {
			t.Errorf("worktree dir gone: %v", statErr)
		}
	}

	t.Run("a clean worktree whose submodule commits are on the remote is removed", func(t *testing.T) {
		c, wt := rig(t)

		if err := c.RemoveWorktree(t.Context(), wt); err != nil {
			t.Fatalf("RemoveWorktree: %v", err)
		}
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Errorf("worktree dir still there: %v", err)
		}
	})

	t.Run("a submodule commit that is on no remote keeps the worktree", func(t *testing.T) {
		c, wt := rig(t)
		commitInLib(t, wt)
		mustGit(t, wt, "commit", "-q", "-am", "feat: bump lib")

		refused(t, c.RemoveWorktree(t.Context(), wt), wt, "lib")
	})

	t.Run("an untracked file keeps the worktree", func(t *testing.T) {
		c, wt := rig(t)
		if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		refused(t, c.RemoveWorktree(t.Context(), wt), wt, "scratch.txt")
	})
}
