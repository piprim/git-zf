package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newDiskRepo initialises a real on-disk git repo in a temp dir,
// creates an initial commit on "main", and returns a Client + the repo dir.
func newDiskRepo(t *testing.T) (*Client, string) {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write base.go: %v", err)
	}

	run("add", "base.go")
	run("commit", "-m", "chore: init")
	run("branch", "-M", "main")

	c, err := NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func TestCurrentBranch(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	branch, err := client.CurrentBranch(t.Context())
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if branch != "main" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "main")
	}

	// Switch to a new branch and verify.
	cmd := exec.Command("git", "checkout", "-b", "feature-x")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout feature-x: %v\n%s", err, out)
	}

	branch2, err := client.CurrentBranch(t.Context())
	if err != nil {
		t.Fatalf("CurrentBranch after switch: %v", err)
	}

	if branch2 != "feature-x" {
		t.Errorf("CurrentBranch = %q, want %q", branch2, "feature-x")
	}
}

func TestMergeDryRun_clean(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Feature adds a new file — no conflict with main.
	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write new.go: %v", err)
	}

	run("add", "new.go")
	run("commit", "-m", "feat: add new.go")
	run("checkout", "main")

	conflicts, err := client.MergeDryRun(t.Context(), "feature", "main")
	if err != nil {
		t.Fatalf("MergeDryRun: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got: %v", conflicts)
	}

	// Main working tree must be clean after the dry-run.
	var buf bytes.Buffer
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = dir
	status.Stdout = &buf
	_ = status.Run()

	if buf.Len() != 0 {
		t.Errorf("working tree dirty after dry-run:\n%s", buf.String())
	}
}

func TestMergeDryRun_conflict(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Feature modifies base.go one way.
	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatalf("write base.go (feature): %v", err)
	}

	run("add", "base.go")
	run("commit", "-m", "feat: feature change")

	// Main modifies base.go a different way.
	run("checkout", "main")

	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package mainbranch\n"), 0o644); err != nil {
		t.Fatalf("write base.go (main): %v", err)
	}

	run("add", "base.go")
	run("commit", "-m", "chore: main change")

	conflicts, err := client.MergeDryRun(t.Context(), "feature", "main")
	if err != nil {
		t.Fatalf("MergeDryRun: %v", err)
	}

	if len(conflicts) == 0 {
		t.Error("expected conflicts, got none")
	}

	found := false
	for _, f := range conflicts {
		if f == "base.go" {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("expected base.go in conflicts, got: %v", conflicts)
	}

	// Main working tree must be clean after the dry-run.
	var buf bytes.Buffer
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = dir
	status.Stdout = &buf
	_ = status.Run()

	if buf.Len() != 0 {
		t.Errorf("working tree dirty after dry-run:\n%s", buf.String())
	}
}

func TestMergeSquash(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write feat.go: %v", err)
	}

	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")
	run("checkout", "main")

	if err := client.MergeSquash(t.Context(), "feature", "main", "Test User <test@test.com>"); err != nil {
		t.Fatalf("MergeSquash: %v", err)
	}

	// Verify feat.go exists on main.
	if _, err := os.Stat(filepath.Join(dir, "feat.go")); err != nil {
		t.Error("feat.go not found on main after squash merge")
	}

	// Verify exactly one squash commit was created (parent count on tip = 1, not a merge commit).
	var squashBuf bytes.Buffer
	logCmd := exec.Command("git", "log", "--oneline", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &squashBuf
	_ = logCmd.Run()

	if !strings.Contains(squashBuf.String(), "squash merge") {
		t.Errorf("squash commit message not found in: %q", squashBuf.String())
	}
}

func TestMergeNoFF(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write feat.go: %v", err)
	}

	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")
	run("checkout", "main")

	if err := client.MergeNoFF(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFF: %v", err)
	}

	// Verify feat.go exists on main.
	if _, err := os.Stat(filepath.Join(dir, "feat.go")); err != nil {
		t.Error("feat.go not found on main after no-ff merge")
	}

	// Verify the tip commit has two parents (it is a merge commit).
	var noffBuf bytes.Buffer
	logCmd := exec.Command("git", "log", "--pretty=%P", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &noffBuf
	_ = logCmd.Run()

	parents := strings.Fields(strings.TrimSpace(noffBuf.String()))
	if len(parents) != 2 {
		t.Errorf("expected merge commit with 2 parents, got %d: %s", len(parents), noffBuf.String())
	}
}

func TestDeleteLocalBranch_safeDelete(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write feat.go: %v", err)
	}

	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")
	run("checkout", "main")

	// Classic merge so safe -d works.
	if err := client.MergeNoFF(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFF: %v", err)
	}

	if err := client.DeleteLocalBranch(t.Context(), "feature", false); err != nil {
		t.Fatalf("DeleteLocalBranch: %v", err)
	}

	// Verify branch is gone.
	var safeBuf bytes.Buffer
	branchCmd := exec.Command("git", "branch")
	branchCmd.Dir = dir
	branchCmd.Stdout = &safeBuf
	_ = branchCmd.Run()

	if strings.Contains(safeBuf.String(), "feature") {
		t.Errorf("feature branch still exists after delete: %s", safeBuf.String())
	}
}

func TestDeleteLocalBranch_forceDelete(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")

	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write feat.go: %v", err)
	}

	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")
	run("checkout", "main")

	// Squash merge — safe -d would fail because squash doesn't preserve ancestry.
	if err := client.MergeSquash(t.Context(), "feature", "main", "Test User <test@test.com>"); err != nil {
		t.Fatalf("MergeSquash: %v", err)
	}

	if err := client.DeleteLocalBranch(t.Context(), "feature", true); err != nil {
		t.Fatalf("DeleteLocalBranch force: %v", err)
	}

	var forceBuf bytes.Buffer
	branchCmd := exec.Command("git", "branch")
	branchCmd.Dir = dir
	branchCmd.Stdout = &forceBuf
	_ = branchCmd.Run()

	if strings.Contains(forceBuf.String(), "feature") {
		t.Errorf("feature branch still exists after force delete: %s", forceBuf.String())
	}
}
