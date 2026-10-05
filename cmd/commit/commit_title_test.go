package commit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

func TestIssueTitle(t *testing.T) {
	// Not parallel: on-disk repo; each subtest builds its own dir.

	newClient := func(t *testing.T) *git.Client {
		t.Helper()

		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "main")
		mustRun(t, dir, "config", "user.email", "t@t.test")
		mustRun(t, dir, "config", "user.name", "T")
		mustRun(t, dir, "config", "commit.gpgsign", "false")

		client, err := git.NewClientAt(&pkg.IO{}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		return client
	}

	t.Run("returns the recorded title for a known slug", func(t *testing.T) {
		c := newClient(t)
		branchtest.Seed(t, c,
			branch.Op{Branch: "ABC-1@feat@add-oauth-login", Title: "Add OAuth login"}, branch.StatusInProgress)

		if got := issueTitle(t.Context(), c, "ABC-1"); got != "Add OAuth login" {
			t.Errorf("issueTitle(ABC-1) = %q, want %q", got, "Add OAuth login")
		}
	})

	t.Run("returns empty for an issue that is not tracked", func(t *testing.T) {
		if got := issueTitle(t.Context(), newClient(t), "NOPE-9"); got != "" {
			t.Errorf("issueTitle(NOPE-9) = %q, want empty", got)
		}
	})

	t.Run("returns empty for an empty slug", func(t *testing.T) {
		if got := issueTitle(t.Context(), newClient(t), ""); got != "" {
			t.Errorf(`issueTitle("") = %q, want empty`, got)
		}
	})

	t.Run("returns empty when the lookup fails", func(t *testing.T) {
		c := newClient(t)
		// A ref in the old blob format cannot be loaded.
		dir := c.WorkingTreeRoot()
		if err := os.WriteFile(filepath.Join(dir, "old.json"), []byte(`{"issue_slug":"ABC-1"}`), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		blob, err := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "old.json").Output()
		if err != nil {
			t.Fatalf("hash-object: %v", err)
		}
		mustRun(t, dir, "update-ref", "refs/zf/branches/ABC-1", strings.TrimSpace(string(blob)))

		if got := issueTitle(t.Context(), c, "ABC-1"); got != "" {
			t.Errorf("issueTitle on a legacy ref = %q, want empty", got)
		}
	})
}
