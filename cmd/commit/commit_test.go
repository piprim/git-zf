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
// Uses the system git binary because git.NewClientAt opens an on-disk repo,
// and CurrentBranch reads HEAD via go-git.
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

func TestIssueHintFromClient_issueBranch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initRepoOnDisk(t, dir)
	gitCheckoutNewBranch(t, t.Context(), dir, testIssueBranch)

	client, err := git.NewClientAt(dir)
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
}

func TestIssueHintFromClient_nonIssueBranch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initRepoOnDisk(t, dir)

	client, err := git.NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	hint := issueHintFromClient(client)
	if hint.IssueID != "" || hint.BranchType != "" {
		t.Errorf("expected zero IssueHint on master, got %+v", hint)
	}
}

func TestIssueHintFromClient_emptyRepo(t *testing.T) {
	t.Parallel()

	// Init a repo with no commits — CurrentBranch will fail to read HEAD,
	// exercising issueHintFromClient's graceful-degradation path.
	dir := t.TempDir()
	ctx := t.Context()

	cmd := exec.CommandContext(ctx, "git", "init", "--initial-branch=master")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	client, err := git.NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	hint := issueHintFromClient(client)
	if hint.IssueID != "" || hint.BranchType != "" {
		t.Errorf("expected zero IssueHint when HEAD unreadable, got %+v", hint)
	}
}
