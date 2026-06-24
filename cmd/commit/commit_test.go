package commit

import (
	"context"
	"os/exec"
	"testing"

	"github.com/piprim/git-zf/git"
)

const (
	testIssueID     = "ABC-1"
	testIssueBranch = "ABC-1@feat@my-feat@a1b2c3d4"
)

// initRepoOnDisk creates a real on-disk git repo at dir with one commit on master.
func initRepoOnDisk(t *testing.T, dir string) {
	t.Helper()

	ctx := t.Context()
	run := func(args ...string) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--initial-branch=master")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	run("commit", "--allow-empty", "-m", "chore: init")
}

// gitCheckoutNewBranch runs `git checkout -b name` in dir.
func gitCheckoutNewBranch(t *testing.T, ctx context.Context, dir, name string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "git", "checkout", "-b", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
}

func TestIssueHintFromClient(t *testing.T) {
	t.Parallel()

	t.Run("extracts issue ID and branch type from a well-formed issue branch", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		initRepoOnDisk(t, dir)
		gitCheckoutNewBranch(t, t.Context(), dir, testIssueBranch)

		client, err := git.NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		hint := issueHintFromClient(client)
		if hint.IssueID != testIssueID {
			t.Errorf("IssueID = %q, want %q", hint.IssueID, testIssueID)
		}
		if hint.BranchType != "feat" {
			t.Errorf("BranchType = %q, want %q", hint.BranchType, "feat")
		}
	})

	t.Run("extracts issue ID from a review branch", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		initRepoOnDisk(t, dir)
		gitCheckoutNewBranch(t, t.Context(), dir, testIssueID+"@review")

		client, err := git.NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		hint := issueHintFromClient(client)
		if hint.IssueID != testIssueID {
			t.Errorf("IssueID = %q, want %q", hint.IssueID, testIssueID)
		}
		// A review branch carries no commit type; the reviewer picks it.
		if hint.BranchType != "" {
			t.Errorf("BranchType = %q, want empty on a review branch", hint.BranchType)
		}
	})

	t.Run("returns zero hint on a plain non-issue branch", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		initRepoOnDisk(t, dir)

		client, err := git.NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		hint := issueHintFromClient(client)
		if hint.IssueID != "" || hint.BranchType != "" {
			t.Errorf("expected zero IssueHint on master, got %+v", hint)
		}
	})

	t.Run("returns zero hint gracefully when HEAD is unreadable (empty repo)", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		cmd := exec.CommandContext(t.Context(), "git", "init", "--initial-branch=master")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}

		client, err := git.NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		hint := issueHintFromClient(client)
		if hint.IssueID != "" || hint.BranchType != "" {
			t.Errorf("expected zero IssueHint when HEAD unreadable, got %+v", hint)
		}
	})
}
