package mergeflow

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func accept(context.Context, string) (bool, error)  { return true, nil }
func decline(context.Context, string) (bool, error) { return false, nil }

// cdHint rebuilds the exact needle RemoveWorktreeStep prints for dir. The path
// comes from go-git's working-tree root, which is symlink-resolved (t.TempDir
// on macOS is /var/..., the resolved root /private/var/...), and it is quoted
// so paths containing spaces stay copy-pasteable.
func cdHint(t *testing.T, dir string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}

	return "cd " + strconv.Quote(resolved)
}

func TestSourceTree(t *testing.T) {
	t.Run("branch in a linked worktree yields a client on it", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.client, "feature")
		if err != nil {
			t.Fatalf("SourceTree: %v", err)
		}
		if src == nil || wt == nil {
			t.Fatalf("src=%v wt=%v, want both set", src, wt)
		}
		root, _ := src.WorkingTreeRoot()
		if got := gitOut(t, root, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
			t.Fatalf("source client HEAD = %q", got)
		}
	})
	t.Run("branch in the main tree yields nil", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.client, "master")
		if err != nil || src != nil || wt != nil {
			t.Fatalf("SourceTree = %v %v %v, want nil nil nil", src, wt, err)
		}
	})
	t.Run("prunable entry is an error naming git worktree prune", func(t *testing.T) {
		rig := newWorktreeRig(t)
		if err := os.RemoveAll(rig.wtDir); err != nil {
			t.Fatalf("rm: %v", err)
		}
		src, wt, err := SourceTree(t.Context(), rig.client, "feature")
		if err == nil {
			t.Fatalf("SourceTree = %v %v, want an error", src, wt)
		}
		if !strings.Contains(err.Error(), "git worktree prune") {
			t.Fatalf("err = %q, want prune instruction", err)
		}
		if src != nil || wt != nil {
			t.Fatalf("SourceTree returned %v %v alongside the error", src, wt)
		}
	})
	t.Run("caller in a linked worktree, source held by the main tree: client on the main tree", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.src, "master")
		if err != nil {
			t.Fatalf("SourceTree: %v", err)
		}
		if src == nil || wt == nil || !wt.Main {
			t.Fatalf("src=%v wt=%+v, want a client on the main entry", src, wt)
		}
		root, _ := src.WorkingTreeRoot()
		if got := gitOut(t, root, "rev-parse", "--abbrev-ref", "HEAD"); got != "master" {
			t.Fatalf("source client HEAD = %q, want master", got)
		}
	})
	t.Run("caller in a linked worktree, source is its own branch: nil", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.src, "feature")
		if err != nil || src != nil || wt != nil {
			t.Fatalf("SourceTree = %v %v %v, want nil nil nil", src, wt, err)
		}
	})
}

func TestRemoveWorktreeStep(t *testing.T) {
	t.Run("accepted: worktree removed", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", accept)
		if err != nil || !removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if _, statErr := os.Stat(rig.wtDir); !os.IsNotExist(statErr) {
			t.Fatalf("worktree dir still present: %v", statErr)
		}
	})
	t.Run("declined: worktree kept and reported", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", decline)
		if err != nil || removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if _, statErr := os.Stat(rig.wtDir); statErr != nil {
			t.Fatalf("worktree dir gone: %v", statErr)
		}
		if !strings.Contains(rig.stdout.String(), "kept") {
			t.Fatalf("stdout = %q, want 'kept'", rig.stdout.String())
		}
	})
	t.Run("untracked file: git refuses, force hint printed, no error", func(t *testing.T) {
		rig := newWorktreeRig(t)
		writeFile(t, rig.wtDir, "scratch.txt", "keep\n")
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		stderr := rig.client.IO().Err.(interface{ String() string })
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", accept)
		if err != nil || removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if !strings.Contains(stderr.String(), "git worktree remove --force") {
			t.Fatalf("stderr = %q, want --force hint", stderr.String())
		}
	})
	t.Run("invoked from inside the removed worktree: cd hint printed", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, rig.wtDir, accept)
		if err != nil || !removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if want := cdHint(t, rig.dir); !strings.Contains(rig.stdout.String(), want) {
			t.Fatalf("stdout = %q, want it to contain %q", rig.stdout.String(), want)
		}
	})
	t.Run("main working tree entry is never removed nor prompted", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, err := SourceTree(t.Context(), rig.src, "master")
		if err != nil || wt == nil || !wt.Main {
			t.Fatalf("SourceTree = %+v %v, want the main entry", wt, err)
		}
		calls := 0
		counting := func(context.Context, string) (bool, error) { calls++; return true, nil }
		removed, err := RemoveWorktreeStep(t.Context(), rig.src, wt, "", counting)
		if err != nil || removed {
			t.Fatalf("removed=%v err=%v, want false nil", removed, err)
		}
		if calls != 0 {
			t.Fatalf("confirm called %d times, want 0", calls)
		}
		if _, statErr := os.Stat(rig.dir); statErr != nil {
			t.Fatalf("main tree gone: %v", statErr)
		}
		if !strings.Contains(rig.stdout.String(), "main working tree") {
			t.Fatalf("stdout = %q, want main-tree notice", rig.stdout.String())
		}
	})
	t.Run("invoked from a symlinked path to the worktree: cd hint printed", func(t *testing.T) {
		rig := newWorktreeRig(t)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(rig.wtDir, link); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, link, accept)
		if err != nil || !removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if want := cdHint(t, rig.dir); !strings.Contains(rig.stdout.String(), want) {
			t.Fatalf("stdout = %q, want it to contain %q", rig.stdout.String(), want)
		}
	})
}
