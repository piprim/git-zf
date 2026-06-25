package commit

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

func mustRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestResolveCommitMergeParent(t *testing.T) {
	// Not parallel: store.Open + on-disk repo; each subtest builds its own dir.

	t.Run("non-issue branch → no merge preview", func(t *testing.T) {
		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "main")
		mustRun(t, dir, "config", "user.email", "t@t.test")
		mustRun(t, dir, "config", "user.name", "T")
		mustRun(t, dir, "config", "commit.gpgsign", "false")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustRun(t, dir, "add", "f.txt")
		mustRun(t, dir, "commit", "-m", "init")

		client, err := git.NewClientAt(&pkg.IO{}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		defer func() { _ = s.Close() }()

		_, include := resolveCommitMergeParent(t.Context(), client, s, "main", "main")
		if include {
			t.Fatal("non-issue branch must not include a merge preview")
		}
	})

	t.Run("issue branch with parent → returns parent integration branch", func(t *testing.T) {
		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "main")
		mustRun(t, dir, "config", "user.email", "t@t.test")
		mustRun(t, dir, "config", "user.name", "T")
		mustRun(t, dir, "config", "commit.gpgsign", "false")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustRun(t, dir, "add", "f.txt")
		mustRun(t, dir, "commit", "-m", "init")
		// Parent integration branch (local) + child feature branch.
		mustRun(t, dir, "checkout", "-b", "X@feat@big")
		mustRun(t, dir, "commit", "--allow-empty", "-m", "feat: parent")
		mustRun(t, dir, "checkout", "-b", "X.2@feat@two")
		mustRun(t, dir, "commit", "--allow-empty", "-m", "feat: child")

		client, err := git.NewClientAt(&pkg.IO{}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		defer func() { _ = s.Close() }()

		// Seed the parent branch + the parent→child relation.
		if err := s.InsertIssueWithBranch(t.Context(),
			&store.Issue{IDSlug: "X", Title: "big", StatusID: store.StatusIDInProgress},
			&store.Branch{Name: "X@feat@big", Type: "feat", StatusID: store.StatusIDInProgress},
		); err != nil {
			t.Fatalf("seed parent: %v", err)
		}
		if err := s.InsertIssueRelation(t.Context(), "X", "X.2"); err != nil {
			t.Fatalf("seed relation: %v", err)
		}

		parent, include := resolveCommitMergeParent(t.Context(), client, s, "X.2@feat@two", "main")
		if !include {
			t.Fatal("issue branch with parent must include a merge preview")
		}
		if parent != "X@feat@big" {
			t.Fatalf("parent = %q, want %q", parent, "X@feat@big")
		}
	})
}
