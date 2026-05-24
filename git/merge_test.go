package git

import (
	"bytes"
	"fmt"
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

	c, err := NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func TestCurrentBranchOnDisk(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	branch, err := client.CurrentBranch()
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

	branch2, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch after switch: %v", err)
	}

	if branch2 != "feature-x" {
		t.Errorf("CurrentBranch = %q, want %q", branch2, "feature-x")
	}
}

func TestMergeDryRun(t *testing.T) {
	t.Parallel()

	t.Run("reports no conflicts for non-overlapping changes", func(t *testing.T) {
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
	})

	t.Run("reports conflicting files and leaves working tree clean", func(t *testing.T) {
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
	})
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

	if err := client.MergeSquash(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeSquash: %v", err)
	}

	// After MergeSquash the working tree must show staged changes but no new commit.
	var statusBuf bytes.Buffer
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("git status: %v", err)
	}
	if !strings.Contains(statusBuf.String(), "feat.go") {
		t.Errorf("status missing feat.go after MergeSquash: %q", statusBuf.String())
	}

	// Caller materializes the commit (mirrors close.go's responsibility).
	run("commit", "-m", "feat: squash test")

	if _, err := os.Stat(filepath.Join(dir, "feat.go")); err != nil {
		t.Error("feat.go not found on main after squash merge")
	}

	var squashBuf bytes.Buffer
	logCmd := exec.Command("git", "log", "--oneline", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &squashBuf
	if err := logCmd.Run(); err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(squashBuf.String(), "feat: squash test") {
		t.Errorf("tip commit subject not found: %q", squashBuf.String())
	}
}


func TestDeleteLocalBranch(t *testing.T) {
	t.Parallel()

	t.Run("safe delete removes a merged branch", func(t *testing.T) {
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

		// Classic --no-ff merge so safe -d works.
		run("merge", "--no-ff", "--no-edit", "feature")

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
	})

	t.Run("force delete removes an unmerged branch", func(t *testing.T) {
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
		if err := client.MergeSquash(t.Context(), "feature", "main"); err != nil {
			t.Fatalf("MergeSquash: %v", err)
		}

		commitCmd := exec.Command("git", "commit", "-m", "feat: squash test")
		commitCmd.Dir = dir
		if out, err := commitCmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, out)
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
	})
}

// newDiskRepoWithOrigin sets up a bare "origin" repo + a working clone.
// Returns the client (rooted at the clone), the clone dir, and the origin dir.
// The clone has one commit on "main" tracked against origin/main.
func newDiskRepoWithOrigin(t *testing.T) (*Client, string, string) {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	cloneDir := t.TempDir()

	mustRun := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}
	mustRun(originDir, "init", "--bare", "--initial-branch=main")

	seedDir := t.TempDir()
	mustRun(seedDir, "init", "--initial-branch=main")
	mustRun(seedDir, "config", "user.name", "Test User")
	mustRun(seedDir, "config", "user.email", "test@test.com")
	mustRun(seedDir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(seedDir, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	mustRun(seedDir, "add", "base.go")
	mustRun(seedDir, "commit", "-m", "chore: init")
	mustRun(seedDir, "remote", "add", "origin", originDir)
	mustRun(seedDir, "push", "origin", "main")

	mustRun(filepath.Dir(cloneDir), "clone", originDir, filepath.Base(cloneDir))
	mustRun(cloneDir, "config", "user.name", "Test User")
	mustRun(cloneDir, "config", "user.email", "test@test.com")
	mustRun(cloneDir, "config", "commit.gpgsign", "false")

	c, err := NewClientAt(nil, cloneDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, cloneDir, originDir
}

func TestFetch_noopWhenNoRemote(t *testing.T) {
	t.Parallel()

	// newDiskRepo creates a repo with no remotes.
	c, _ := newDiskRepo(t)

	if err := c.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch with no remote: %v", err)
	}
}

func TestFetch_updatesRemoteTrackingRef(t *testing.T) {
	t.Parallel()

	c, _, originDir := newDiskRepoWithOrigin(t)

	pusher := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = pusher
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("clone", originDir, ".")
	run("config", "user.name", "Pusher")
	run("config", "user.email", "pusher@test.com")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(pusher, "added.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	run("add", "added.go")
	run("commit", "-m", "feat: added")
	run("push", "origin", "main")

	if err := c.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	originTip, err := c.ResolveRef("refs/remotes/origin/main")
	if err != nil {
		t.Fatalf("resolve origin/main: %v", err)
	}

	mainTip, err := c.ResolveRef("refs/heads/main")
	if err != nil {
		t.Fatalf("resolve main: %v", err)
	}

	if originTip == mainTip {
		t.Errorf("expected origin/main to advance past local main; both = %s", originTip)
	}
}

func TestIsAncestor(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
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
	run("commit", "-m", "feat: f1")
	run("checkout", "main")

	if err := os.WriteFile(filepath.Join(dir, "main2.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main2.go: %v", err)
	}

	run("add", "main2.go")
	run("commit", "-m", "chore: m2")

	cases := []struct {
		name      string
		child     string
		ancestor  string
		want      bool
		wantError bool
	}{
		{"main is ancestor of itself", "main", "main", true, false},
		{"main is NOT ancestor of feature (siblings)", "main", "feature", false, false},
		{"feature is NOT ancestor of main (siblings)", "feature", "main", false, false},
		{"missing ref errors", "nonexistent", "main", false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.IsAncestor(t.Context(), tc.child, tc.ancestor)
			if tc.wantError {
				if err == nil {
					t.Error("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("IsAncestor: %v", err)
			}

			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResetHard_restoresTracked(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	origHash, err := c.ResolveRef("HEAD")
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package mutated\n"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	addCmd := exec.CommandContext(t.Context(), "git", "add", "staged.go")
	addCmd.Dir = dir
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	if err := c.ResetHard(t.Context(), origHash.String()); err != nil {
		t.Fatalf("ResetHard: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "base.go"))
	if err != nil {
		t.Fatalf("read base.go: %v", err)
	}

	if string(got) != "package main\n" {
		t.Errorf("base.go = %q, want %q", got, "package main\n")
	}

	if _, err := os.Stat(filepath.Join(dir, "staged.go")); !os.IsNotExist(err) {
		t.Errorf("expected staged.go to be removed after ResetHard, stat err = %v", err)
	}
}

func TestFastForwardOnly(t *testing.T) {
	t.Parallel()

	t.Run("advances the target branch when fast-forward is possible", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		run := func(args ...string) {
			t.Helper()

			cmd := exec.CommandContext(t.Context(), "git", args...)
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
		run("commit", "-m", "feat: f1")

		featTip, err := c.ResolveRef("refs/heads/feature")
		if err != nil {
			t.Fatalf("resolve feature: %v", err)
		}

		if err := c.FastForwardOnly(t.Context(), "feature", "main"); err != nil {
			t.Fatalf("FastForwardOnly: %v", err)
		}

		mainTip, err := c.ResolveRef("refs/heads/main")
		if err != nil {
			t.Fatalf("resolve main: %v", err)
		}

		if mainTip != featTip {
			t.Errorf("main = %s, want %s (FF should equalize)", mainTip, featTip)
		}
	})

	t.Run("returns error when branches have diverged", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		run := func(args ...string) {
			t.Helper()

			cmd := exec.CommandContext(t.Context(), "git", args...)
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
		run("commit", "-m", "feat: f1")
		run("checkout", "main")

		if err := os.WriteFile(filepath.Join(dir, "main2.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write main2.go: %v", err)
		}

		run("add", "main2.go")
		run("commit", "-m", "chore: m2")

		err := c.FastForwardOnly(t.Context(), "feature", "main")
		if err == nil {
			t.Fatal("expected FF on diverged branches to fail, got nil")
		}
	})
}

func TestMergeRebase_clean(t *testing.T) {
	t.Parallel()

	c, cloneDir, originDir := newDiskRepoWithOrigin(t)

	pusher := t.TempDir()
	run := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	run(filepath.Dir(pusher), "clone", originDir, filepath.Base(pusher))
	run(pusher, "config", "user.name", "Pusher")
	run(pusher, "config", "user.email", "pusher@test.com")
	run(pusher, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(pusher, "remote.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write remote.go: %v", err)
	}

	run(pusher, "add", "remote.go")
	run(pusher, "commit", "-m", "feat: remote change")
	run(pusher, "push", "origin", "main")

	run(cloneDir, "checkout", "-b", "feature")

	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("feat%d.go", i)
		if err := os.WriteFile(filepath.Join(cloneDir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}

		run(cloneDir, "add", name)
		run(cloneDir, "commit", "-m", fmt.Sprintf("feat: f%d", i))
	}

	if err := c.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeRebase: %v", err)
	}

	branch, err := c.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if branch != "feature" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "feature")
	}

	featTip, err := c.ResolveRef("refs/heads/feature")
	if err != nil {
		t.Fatalf("resolve feature: %v", err)
	}

	origTip, err := c.ResolveRef("refs/remotes/origin/main")
	if err != nil {
		t.Fatalf("resolve origin/main: %v", err)
	}

	if featTip != origTip {
		t.Errorf("after MergeRebase: feature=%s, origin/main=%s — should be equal", featTip, origTip)
	}

	if _, err := os.Stat(filepath.Join(cloneDir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("MERGE_HEAD should not exist after MergeRebase, stat err = %v", err)
	}

	statusCmd := exec.CommandContext(t.Context(), "git", "-C", cloneDir, "status", "--porcelain")
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, statusOut)
	}

	if !strings.Contains(string(statusOut), "feat1.go") || !strings.Contains(string(statusOut), "feat2.go") {
		t.Errorf("expected feat1.go and feat2.go staged after MergeRebase, got:\n%s", statusOut)
	}

	if strings.Contains(string(statusOut), "remote.go") {
		t.Errorf("remote.go should be in HEAD's tree (not staged), got:\n%s", statusOut)
	}
}

// newDiskRepoWithOrigin_andSubmodule extends newDiskRepoWithOrigin by:
//   - creating a separate "submodule" bare repo with 2 commits (subA, subB).
//   - adding the submodule to the SUT clone at path "sub", pinned to subA.
//   - pushing the submodule registration to origin/main.
//
// Returns the SUT client, clone dir, origin (bare parent) dir, and the two
// submodule commit SHAs (subA = initial pinning, subB = next pointer).
func newDiskRepoWithOrigin_andSubmodule(t *testing.T) (*Client, string, string, string, string) {
	t.Helper()

	c, cloneDir, originDir := newDiskRepoWithOrigin(t)

	run := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	capture := func(cwd string, args ...string) string {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, cwd, err)
		}

		return strings.TrimSpace(string(out))
	}

	subOriginDir := filepath.Join(t.TempDir(), "subm.git")
	if err := os.MkdirAll(subOriginDir, 0o755); err != nil {
		t.Fatalf("mkdir subm: %v", err)
	}

	run(subOriginDir, "init", "--bare", "--initial-branch=main")

	subSeed := t.TempDir()
	run(subSeed, "init", "--initial-branch=main")
	run(subSeed, "config", "user.name", "Sub Author")
	run(subSeed, "config", "user.email", "sub@test.com")
	run(subSeed, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(subSeed, "a.txt"), []byte("A\n"), 0o644); err != nil {
		t.Fatalf("write a.txt: %v", err)
	}

	run(subSeed, "add", "a.txt")
	run(subSeed, "commit", "-m", "subA")
	subA := capture(subSeed, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(subSeed, "b.txt"), []byte("B\n"), 0o644); err != nil {
		t.Fatalf("write b.txt: %v", err)
	}

	run(subSeed, "add", "b.txt")
	run(subSeed, "commit", "-m", "subB")
	subB := capture(subSeed, "rev-parse", "HEAD")

	run(subSeed, "remote", "add", "origin", subOriginDir)
	run(subSeed, "push", "origin", "main")

	run(cloneDir, "-c", "protocol.file.allow=always", "submodule", "add", subOriginDir, "sub")
	run(cloneDir, "-C", "sub", "checkout", subA)
	run(cloneDir, "add", ".gitmodules", "sub")
	run(cloneDir, "commit", "-m", "chore: add sub pinned to subA")
	run(cloneDir, "push", "origin", "main")

	return c, cloneDir, originDir, subA, subB
}

func TestMergeRebase_preservesSubmodulePointer(t *testing.T) {
	t.Parallel()

	c, cloneDir, originDir, subA, subB := newDiskRepoWithOrigin_andSubmodule(t)
	_ = subA

	run := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	pusher := t.TempDir()
	run(filepath.Dir(pusher), "clone", originDir, filepath.Base(pusher))
	run(pusher, "config", "user.name", "Pusher")
	run(pusher, "config", "user.email", "pusher@test.com")
	run(pusher, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(pusher, "remote.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write remote.go: %v", err)
	}

	run(pusher, "add", "remote.go")
	run(pusher, "commit", "-m", "feat: remote change")
	run(pusher, "push", "origin", "main")

	run(cloneDir, "checkout", "-b", "feature")
	run(cloneDir, "-C", "sub", "fetch", "origin")
	run(cloneDir, "-C", "sub", "checkout", subB)
	run(cloneDir, "add", "sub")
	run(cloneDir, "commit", "-m", "feat: bump sub to subB")

	if err := c.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeRebase: %v", err)
	}

	run(cloneDir, "commit", "-m", "feat: rebase close")

	lsTree := exec.CommandContext(t.Context(), "git", "-C", cloneDir, "ls-tree", "HEAD", "sub")
	out, err := lsTree.Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}

	if !strings.Contains(string(out), subB) {
		t.Errorf("submodule pointer wrong after MergeRebase + commit.\nls-tree: %s\nwant subB: %s", out, subB)
	}
}

func TestMergeRebase_noRemote(t *testing.T) {
	t.Parallel()

	// newDiskRepo: one commit on main, no remote.
	c, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Create a feature branch with one commit.
	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	run("add", "feat.go")
	run("commit", "-m", "feat: something")

	// MergeRebase against local main (no remote) must not error.
	if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeRebase with no remote: %v", err)
	}

	branch, err := c.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if branch != "feature" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "feature")
	}
}

func TestMergeNoFFNoCommit_clean(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
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

	// Base advances independently while feature is alive.
	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n// base advance\n"), 0o644); err != nil {
		t.Fatalf("write base.go: %v", err)
	}
	run("add", "base.go")
	run("commit", "-m", "chore: base advances")

	// Pre-state: switch back to feature so the helper has to checkout base itself.
	run("checkout", "feature")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}

	// HEAD must be on main (helper checked out base).
	var headBuf bytes.Buffer
	headCmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--abbrev-ref", "HEAD")
	headCmd.Dir = dir
	headCmd.Stdout = &headBuf
	if err := headCmd.Run(); err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(headBuf.String()); got != "main" {
		t.Errorf("HEAD after MergeNoFFNoCommit = %q, want %q", got, "main")
	}

	// MERGE_HEAD must exist (commit pending).
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Errorf("MERGE_HEAD missing after MergeNoFFNoCommit: %v", err)
	}

	// feat.go must be staged on main.
	var statusBuf bytes.Buffer
	statusCmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusBuf.String(), "feat.go") {
		t.Errorf("status missing feat.go: %q", statusBuf.String())
	}

	// No new commit on main yet.
	var logBuf bytes.Buffer
	logCmd := exec.CommandContext(t.Context(), "git", "log", "--oneline", "main")
	logCmd.Dir = dir
	logCmd.Stdout = &logBuf
	if err := logCmd.Run(); err != nil {
		t.Fatalf("log: %v", err)
	}
	if strings.Contains(logBuf.String(), "Merge") {
		t.Errorf("unexpected merge commit on main: %q", logBuf.String())
	}
}

func TestMergeNoFFNoCommit_conflict(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "conflict.go"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "conflict.go")
	run("commit", "-m", "feat: feature side")

	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "conflict.go"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "conflict.go")
	run("commit", "-m", "feat: base side")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err == nil {
		t.Fatal("MergeNoFFNoCommit: expected conflict error, got nil")
	}

	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Errorf("MERGE_HEAD missing after conflicting MergeNoFFNoCommit: %v", err)
	}

	contents, readErr := os.ReadFile(filepath.Join(dir, "conflict.go"))
	if readErr != nil {
		t.Fatalf("read conflict.go: %v", readErr)
	}
	if !strings.Contains(string(contents), "<<<<<<<") {
		t.Errorf("conflict.go missing conflict markers: %q", string(contents))
	}
}

func TestMergeNoFFNoCommit_ffOnly(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "ahead.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "ahead.go")
	run("commit", "-m", "feat: only on feature")

	// main is now an ancestor of feature — no divergence.
	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}

	// --no-ff must force MERGE_HEAD even in a fast-forwardable situation.
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Errorf("MERGE_HEAD missing in FF-able scenario: %v", err)
	}
}

func TestAbortMerge_active(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")

	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n// base advance\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "base.go")
	run("commit", "-m", "chore: base advances")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("MERGE_HEAD missing before abort: %v", err)
	}

	if err := client.AbortMerge(t.Context()); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}

	// MERGE_HEAD must be gone.
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("MERGE_HEAD still present after AbortMerge: %v", err)
	}

	// Working tree must be clean.
	var statusBuf bytes.Buffer
	statusCmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.TrimSpace(statusBuf.String()) != "" {
		t.Errorf("dirty status after AbortMerge: %q", statusBuf.String())
	}
}

func TestAbortMerge_noActiveMerge(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	if err := client.AbortMerge(t.Context()); err == nil {
		t.Error("AbortMerge with no active merge: expected error, got nil")
	}
}
