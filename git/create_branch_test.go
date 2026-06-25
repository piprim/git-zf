package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushBranchFromFreshClone clones originDir into a throwaway working copy,
// creates branchName with one commit, and pushes it to origin — so a *separate*
// clone sees branchName only as a remote-tracking ref after it fetches.
func pushBranchFromFreshClone(t *testing.T, originDir, branchName string) {
	t.Helper()

	work := t.TempDir()
	mustGit(t, filepath.Dir(work), "clone", originDir, filepath.Base(work))
	mustGit(t, work, "config", "user.email", "side@test.com")
	mustGit(t, work, "config", "user.name", "Side")
	mustGit(t, work, "config", "commit.gpgsign", "false")
	mustGit(t, work, "checkout", "-b", branchName)
	if err := os.WriteFile(filepath.Join(work, "side.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write side.go: %v", err)
	}
	mustGit(t, work, "add", "side.go")
	mustGit(t, work, "commit", "-m", "feat: "+branchName)
	mustGit(t, work, "push", "origin", branchName)
}

func TestCreateBranch_RemoteOnlyBase(t *testing.T) {
	t.Parallel()

	c, cloneDir, originDir := newDiskRepoWithOrigin(t)

	// A teammate pushes the parent integration branch; our clone only fetches it,
	// so locally it exists solely as origin/parent (no refs/heads/parent).
	pushBranchFromFreshClone(t, originDir, "parent")
	mustGit(t, cloneDir, "fetch", "origin")

	t.Run("precondition: local parent head is absent", func(t *testing.T) {
		if exists, _ := c.BranchExists("parent"); exists {
			t.Fatal("local 'parent' should not exist, only origin/parent")
		}
	})

	t.Run("creates the feature branch from the remote-only base", func(t *testing.T) {
		if err := c.CreateBranch("feature", "parent"); err != nil {
			t.Fatalf("CreateBranch from remote-only base: %v", err)
		}

		featHash, err := c.ResolveRef("refs/heads/feature")
		if err != nil {
			t.Fatalf("resolve feature: %v", err)
		}
		baseHash, err := c.ResolveRef("refs/remotes/origin/parent")
		if err != nil {
			t.Fatalf("resolve origin/parent: %v", err)
		}
		if featHash != baseHash {
			t.Fatalf("feature tip %s != origin/parent tip %s", featHash, baseHash)
		}
	})
}

func TestRemoteBranchNames(t *testing.T) {
	t.Parallel()

	t.Run("no remote → empty", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t)
		names, err := c.RemoteBranchNames()
		if err != nil {
			t.Fatalf("RemoteBranchNames: %v", err)
		}
		if len(names) != 0 {
			t.Fatalf("got %v, want empty (no remote)", names)
		}
	})

	t.Run("lists tracking branches without the remote prefix, skipping HEAD", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, originDir := newDiskRepoWithOrigin(t)
		pushBranchFromFreshClone(t, originDir, "feat-x")
		mustGit(t, cloneDir, "fetch", "origin")

		names, err := c.RemoteBranchNames()
		if err != nil {
			t.Fatalf("RemoteBranchNames: %v", err)
		}

		got := make(map[string]bool, len(names))
		for _, n := range names {
			got[n] = true
			if strings.HasPrefix(n, "origin/") {
				t.Errorf("name %q still carries the remote prefix", n)
			}
			if n == "HEAD" {
				t.Errorf("HEAD must be skipped, got it in %v", names)
			}
		}
		if !got["main"] || !got["feat-x"] {
			t.Fatalf("got %v, want to include both main and feat-x", names)
		}
	})
}
