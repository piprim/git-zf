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

// The chain lookup behind the hint: the title comes from the branch chain,
// and whatever cannot be read only leaves the hint as the branch name says.
func TestIssueHintFromClient_ChainLookup(t *testing.T) {
	// Not parallel: on-disk repo; each subtest builds its own dir.

	const name = "ABC-1@feat@add-oauth-login"

	newClient := func(t *testing.T, checkout string) *git.Client {
		t.Helper()

		dir := t.TempDir()
		mustRun(t, dir, "init", "-q", "-b", "main")
		mustRun(t, dir, "config", "user.email", "t@t.test")
		mustRun(t, dir, "config", "user.name", "T")
		mustRun(t, dir, "config", "commit.gpgsign", "false")
		mustRun(t, dir, "commit", "-q", "--allow-empty", "-m", "chore: init")
		mustRun(t, dir, "checkout", "-q", "-b", checkout)

		client, err := git.NewClientAt(&pkg.IO{}, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		return client
	}

	t.Run("the recorded title of a known slug fills the subject", func(t *testing.T) {
		c := newClient(t, name)
		branchtest.Seed(t, c, branch.Op{Branch: name, Title: "Add OAuth login"}, branch.StatusInProgress)

		hint := issueHintFromClient(t.Context(), c)
		if hint.IssueID != "ABC-1" || hint.IssueSubject != "Add OAuth login" {
			t.Errorf("hint = %+v", hint)
		}
	})

	t.Run("an issue that is not tracked keeps the slug and no title", func(t *testing.T) {
		hint := issueHintFromClient(t.Context(), newClient(t, "NOPE-9@feat@x"))
		if hint.IssueID != "NOPE-9" || hint.IssueSubject != "" {
			t.Errorf("hint = %+v", hint)
		}
	})

	t.Run("a branch without an issue gives no lookup and no hint", func(t *testing.T) {
		hint := issueHintFromClient(t.Context(), newClient(t, "plain"))
		if hint.IssueID != "" || hint.IssueSubject != "" {
			t.Errorf("hint = %+v", hint)
		}
	})

	t.Run("a chain that cannot be read keeps the slug and no title", func(t *testing.T) {
		c := newClient(t, name)
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

		hint := issueHintFromClient(t.Context(), c)
		if hint.IssueID != "ABC-1" || hint.IssueSubject != "" {
			t.Errorf("hint on a legacy ref = %+v", hint)
		}
	})
}
