package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPushDryRun(t *testing.T) {
	t.Parallel()

	t.Run("no remote → ok=false, no error", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t) // no remote configured
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if ok {
			t.Fatalf("ok = true, want false (no remote)")
		}
		_ = out
	})

	t.Run("up-to-date main → Kind UpToDate, ok=false", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newDiskRepoWithOrigin(t)
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if ok || out.Kind != PushUpToDate {
			t.Fatalf("got kind=%v ok=%v, want UpToDate/false", out.Kind, ok)
		}
	})

	t.Run("new local branch → Kind NewBranch, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, _ := newDiskRepoWithOrigin(t)
		mustGit(t, cloneDir, "checkout", "-b", "feature-x")
		out, ok, err := c.PushDryRun(t.Context(), "feature-x")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushNewBranch {
			t.Fatalf("got kind=%v ok=%v, want NewBranch/true", out.Kind, ok)
		}
	})

	t.Run("local ahead of origin → Kind FastForward, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, _ := newDiskRepoWithOrigin(t)
		if err := os.WriteFile(filepath.Join(cloneDir, "ahead.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "ahead.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: ahead")
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushFastForward {
			t.Fatalf("got kind=%v ok=%v, want FastForward/true", out.Kind, ok)
		}
	})

	t.Run("diverged from origin → Kind Rejected, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, originDir := newDiskRepoWithOrigin(t)
		// Advance origin/main from a second clone so our clone diverges.
		other := t.TempDir()
		mustGit(t, filepath.Dir(other), "clone", originDir, filepath.Base(other))
		mustGit(t, other, "config", "user.email", "o@o.test")
		mustGit(t, other, "config", "user.name", "Other")
		mustGit(t, other, "config", "commit.gpgsign", "false")
		if err := os.WriteFile(filepath.Join(other, "remote.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, other, "add", "remote.go")
		mustGit(t, other, "commit", "-m", "feat: remote-side")
		mustGit(t, other, "push", "origin", "main")
		// Our clone makes a different commit without fetching → diverged.
		if err := os.WriteFile(filepath.Join(cloneDir, "local.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "local.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: local-side")
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushRejected {
			t.Fatalf("got kind=%v ok=%v, want Rejected/true", out.Kind, ok)
		}
	})
}

func TestPushBranch(t *testing.T) {
	t.Parallel()

	t.Run("advances the branch on origin", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, originDir := newDiskRepoWithOrigin(t)
		mustGit(t, cloneDir, "checkout", "-b", "feature-y")
		if err := os.WriteFile(filepath.Join(cloneDir, "y.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "y.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: y")
		if err := c.PushBranch(t.Context(), "feature-y"); err != nil {
			t.Fatalf("PushBranch: %v", err)
		}
		// origin now has refs/heads/feature-y.
		got := exec.Command("git", "-C", originDir, "rev-parse", "refs/heads/feature-y")
		if out, err := got.CombinedOutput(); err != nil {
			t.Fatalf("origin missing feature-y: %v\n%s", err, out)
		}
	})

	t.Run("no remote → no-op, nil error", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t)
		if err := c.PushBranch(t.Context(), "main"); err != nil {
			t.Fatalf("PushBranch with no remote: %v", err)
		}
	})
}

// mustGit runs a git command in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
