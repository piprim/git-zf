# Rebase Close Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third merge strategy ("Rebase") to `git zf issue close` that produces one clean, submodule-correct commit on the local base branch via a real `git merge origin/<base>` + `git reset --soft origin/<base>` mechanic, routed through the existing `tui.commit` form.

**Architecture:** Eight new methods on `git.Client` (one IO accessor, two general primitives, five merge-flow primitives), a `MergeStrategy` enum next to its consumer in `cmd/issue/close.go`, a generic `StrategyOption` list parameter on `tui.IssueMergeStrategy`, and a new `doRebaseClose` orchestrator with `defer`-based rollback keyed on the function's named return error.

**Tech Stack:** Go 1.x via `mise exec --`, `go-git`, `github.com/charmbracelet/huh`, standard `os/exec`, on-disk repo tests against the system `git` binary.

**User-driven git operations.** Per project convention (memory `feedback_no_git_commit`), the executor of this plan must **not** run `git add` or `git commit` itself. Each task ends with a suggested commit message; pause for the user to run the commit, then continue.

---

## File Structure

| Path | Action | Responsibility |
|---|---|---|
| `git/git.go` | Modify | Add `IO()`, `IsDirty()`, `Checkout()` methods. |
| `git/git_test.go` | Modify | Unit tests for the three new general-purpose methods. |
| `git/merge.go` | Modify | Add `FetchOrigin()`, `IsAncestor()`, `MergeRebase()`, `ResetHard()`, `FastForwardOnly()`. |
| `git/merge_test.go` | Modify | Unit tests including the submodule regression test for `MergeRebase`. |
| `tui/issue.go` | Modify | Replace `IssueMergeStrategy(squash *bool)` with the option-list form. |
| `cmd/issue/close.go` | Modify | Add `MergeStrategy` enum + `errFastForwardDeferred` sentinel, switch `doMerge` to ternary dispatch, update `doDeleteBranch`, add `doRebaseClose`. |

No new files. Tests live next to the code they cover.

---

## Conventions enforced in every code snippet below

- Wrap subprocess and library errors with `fmt.Errorf("context: %w", err)` — never return a bare external error (memory `feedback_wrapcheck`).
- Always pass `ctx` to `exec.CommandContext` — never `exec.Command` (memory `feedback_exec_command_context`).
- Add a blank line before `return` when it is not the only statement in its block (memory `feedback_nlreturn`).
- Run Go via `mise exec -- go …` (memory `feedback_mise_exec`).

---

## Task 1: Add `Client.IO()` accessor

**Files:**
- Modify: `git/git.go` (add method near line 67, after `WorkingTreeRoot`)
- Test: `git/git_test.go` (new file if absent — there is currently no `git_test.go` for `git.go`; place tests in `git/git_io_test.go` if you prefer to keep test files focused, otherwise add to the existing `git/merge_test.go`. This plan uses `git/git_test.go`.)

- [ ] **Step 1: Write the failing test**

Create `git/git_test.go`:

```go
package git

import (
	"bytes"
	"testing"

	"github.com/piprim/git-zf/internal/pkg"
)

func TestClientIO_returnsInjectedStreams(t *testing.T) {
	t.Parallel()

	in := bytes.NewBufferString("")
	out := &bytes.Buffer{}
	errW := &bytes.Buffer{}

	c, dir := newDiskRepo(t)
	_ = dir

	// Replace the IO with a fresh struct so the test owns the pointer identity.
	c.io = &pkg.IO{In: in, Out: out, Err: errW}

	got := c.IO()
	if got == nil {
		t.Fatal("IO() returned nil")
	}

	if got.In != in || got.Out != out || got.Err != errW {
		t.Errorf("IO() returned a different struct than was injected")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git -run TestClientIO_returnsInjectedStreams -v`
Expected: FAIL — `c.IO undefined`.

- [ ] **Step 3: Add `IO()` to `git/git.go`**

Insert after the existing `WorkingTreeRoot` method (currently around line 79):

```go
// IO returns the injected IO streams. Callers should write status/diagnostic
// messages through these instead of os.Stdout/os.Stderr so Cobra-aware
// redirection (tests, subcommand piping, future TUI capture) keeps working.
func (c *Client) IO() *pkg.IO {
	return c.io
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestClientIO_returnsInjectedStreams -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

Suggested commit:

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): expose injected IO streams via Client.IO()"
```

Pause for the user to commit before proceeding.

---

## Task 2: Add `Client.IsDirty()`

`git status --porcelain --untracked-files=no` — non-empty output ⇒ dirty. Untracked files are intentionally ignored (they survive `git reset --hard` rollback).

**Files:**
- Modify: `git/git.go`
- Test: `git/git_test.go`

- [ ] **Step 1: Write failing tests**

Append to `git/git_test.go`:

```go
import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

func TestIsDirty_clean(t *testing.T) {
	t.Parallel()

	c, _ := newDiskRepo(t)

	dirty, err := c.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}

	if dirty {
		t.Error("expected clean repo to report dirty=false")
	}
}

func TestIsDirty_modifiedTracked(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	dirty, err := c.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}

	if !dirty {
		t.Error("expected modified tracked file to report dirty=true")
	}
}

func TestIsDirty_stagedChange(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	stage := exec.Command("git", "add", "new.go")
	stage.Dir = dir
	if out, err := stage.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	dirty, err := c.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}

	if !dirty {
		t.Error("expected staged file to report dirty=true")
	}
}

func TestIsDirty_untrackedIgnored(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("note\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	dirty, err := c.IsDirty(context.Background())
	if err != nil {
		t.Fatalf("IsDirty: %v", err)
	}

	if dirty {
		t.Error("expected untracked-only repo to report dirty=false")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./git -run TestIsDirty -v`
Expected: FAIL — `c.IsDirty undefined`.

- [ ] **Step 3: Implement `IsDirty`**

Add to `git/git.go` (next to `IO`):

```go
// IsDirty reports whether the working tree has tracked-file modifications or
// staged-but-uncommitted changes. Wraps `git status --porcelain --untracked-files=no`.
// Untracked files are intentionally NOT counted as dirty: `git reset --hard`
// does not touch untracked content, so their presence does not put user work
// at risk during rollback.
func (c *Client) IsDirty(ctx context.Context) (bool, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=no")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}

	return len(out) > 0, nil
}
```

Make sure `os/exec` is imported in `git/git.go` (it's already in `git/merge.go`; add to `git/git.go` if absent).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./git -run TestIsDirty -v`
Expected: all four pass.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add IsDirty client method"
```

---

## Task 3: Add `Client.Checkout()`

**Files:**
- Modify: `git/git.go`
- Test: `git/git_test.go`

- [ ] **Step 1: Write failing test**

Append to `git/git_test.go`:

```go
func TestCheckout_switchesBranch(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	newBranch := exec.Command("git", "checkout", "-b", "feature-x")
	newBranch.Dir = dir
	if out, err := newBranch.CombinedOutput(); err != nil {
		t.Fatalf("create feature-x: %v\n%s", err, out)
	}

	if err := c.Checkout(context.Background(), "main"); err != nil {
		t.Fatalf("Checkout main: %v", err)
	}

	got, err := c.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if got != "main" {
		t.Errorf("CurrentBranch = %q, want %q", got, "main")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git -run TestCheckout_switchesBranch -v`
Expected: FAIL — `c.Checkout undefined`.

- [ ] **Step 3: Implement `Checkout`**

Add to `git/git.go`:

```go
// Checkout switches the working tree to branchName. Wraps `git checkout <name>`.
// Returns a wrapped error from the git CLI on failure (e.g. unknown branch,
// untracked file collision).
func (c *Client) Checkout(ctx context.Context, branchName string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", branchName); err != nil {
		return fmt.Errorf("checkout %s: %w", branchName, err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestCheckout_switchesBranch -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add Checkout client method"
```

---

## Task 4: Add `Client.FetchOrigin()` + bare-origin test helper

**Files:**
- Modify: `git/merge.go`
- Modify: `git/merge_test.go` (add helper `newDiskRepoWithOrigin` + test)

- [ ] **Step 1: Add the origin helper and the failing test**

Append to `git/merge_test.go`:

```go
// newDiskRepoWithOrigin sets up a bare "origin" repo + a working clone.
// Returns the client (rooted at the clone), the clone dir, and the origin dir.
// The clone has one commit on "main" tracked against origin/main.
func newDiskRepoWithOrigin(t *testing.T) (*Client, string, string) {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	cloneDir := t.TempDir()

	mustRun := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	// Init bare origin.
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}
	mustRun(originDir, "init", "--bare", "--initial-branch=main")

	// Seed a temporary working dir to populate origin.
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

	// Clone for the SUT.
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

func TestFetchOrigin_updatesRemoteTrackingRef(t *testing.T) {
	t.Parallel()

	c, _, originDir := newDiskRepoWithOrigin(t)

	// Push a second commit to origin from an unrelated working dir.
	pusher := t.TempDir()
	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
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

	// Origin tip changed; SUT's origin/main still points at the original commit.
	if err := c.FetchOrigin(t.Context()); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git -run TestFetchOrigin -v`
Expected: FAIL — `c.FetchOrigin undefined`.

- [ ] **Step 3: Implement `FetchOrigin` in `git/merge.go`**

Add (preserving existing imports):

```go
// FetchOrigin runs `git fetch origin`. Returns a wrapped error when the remote
// is unreachable or auth fails.
func (c *Client) FetchOrigin(ctx context.Context) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "fetch", "origin"); err != nil {
		return fmt.Errorf("fetch origin: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestFetchOrigin -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add FetchOrigin client method"
```

---

## Task 5: Add `Client.IsAncestor()`

**Files:**
- Modify: `git/merge.go`
- Test: `git/merge_test.go`

- [ ] **Step 1: Write failing tests**

Append to `git/merge_test.go`:

```go
func TestIsAncestor(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Build: main (1 commit) → main (2 commits); feature branches off after commit 1.
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./git -run TestIsAncestor -v`
Expected: FAIL — `c.IsAncestor undefined`.

- [ ] **Step 3: Implement `IsAncestor`**

Add to `git/merge.go`:

```go
// IsAncestor reports whether child is an ancestor of ancestor (or equal).
// Wraps `git merge-base --is-ancestor child ancestor`: exit code 0 → true,
// exit code 1 → false, any other exit code → wrapped error.
func (c *Client) IsAncestor(ctx context.Context, child, ancestor string) (bool, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", child, ancestor)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}

	return false, fmt.Errorf("merge-base --is-ancestor %s %s: %w: %s", child, ancestor, err, out)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./git -run TestIsAncestor -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add IsAncestor client method"
```

---

## Task 6: Add `Client.ResetHard()`

**Files:**
- Modify: `git/merge.go`
- Test: `git/merge_test.go`

- [ ] **Step 1: Write failing test**

Append to `git/merge_test.go`:

```go
func TestResetHard_restoresTracked(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	origHash, err := c.ResolveRef("HEAD")
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}

	// Mutate tracked file + stage a new file.
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package mutated\n"), 0o644); err != nil {
		t.Fatalf("mutate: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "staged.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write staged: %v", err)
	}

	addCmd := exec.Command("git", "add", "staged.go")
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git -run TestResetHard -v`
Expected: FAIL — `c.ResetHard undefined`.

- [ ] **Step 3: Implement `ResetHard`**

Add to `git/merge.go`:

```go
// ResetHard runs `git reset --hard <target>`. Used by the close orchestrator
// to atomically roll the current branch back to its original tip on TUI abort
// or commit failure. Does not touch untracked files.
func (c *Client) ResetHard(ctx context.Context, target string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "reset", "--hard", target); err != nil {
		return fmt.Errorf("reset --hard %s: %w", target, err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestResetHard -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add ResetHard client method"
```

---

## Task 7: Add `Client.FastForwardOnly()`

**Files:**
- Modify: `git/merge.go`
- Test: `git/merge_test.go`

- [ ] **Step 1: Write failing tests**

Append to `git/merge_test.go`:

```go
func TestFastForwardOnly_clean(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

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
}

func TestFastForwardOnly_diverged(t *testing.T) {
	t.Parallel()

	c, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// feature: 1 own commit; main: 1 own commit (diverged).
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
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./git -run TestFastForwardOnly -v`
Expected: FAIL — `c.FastForwardOnly undefined`.

- [ ] **Step 3: Implement `FastForwardOnly`**

Add to `git/merge.go`:

```go
// FastForwardOnly checks out targetBranch and runs `git merge --ff-only sourceBranch`.
// Returns a wrapped error when the FF is refused (diverged history) so the
// caller can render an actionable message.
func (c *Client) FastForwardOnly(ctx context.Context, sourceBranch, targetBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", targetBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--ff-only", sourceBranch); err != nil {
		return fmt.Errorf("merge --ff-only %s: %w", sourceBranch, err)
	}

	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./git -run TestFastForwardOnly -v`
Expected: PASS for both subtests.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add FastForwardOnly client method"
```

---

## Task 8: Add `Client.MergeRebase()` (clean case)

The submodule regression case is its own task (Task 9). This task covers the basic plumbing test.

**Files:**
- Modify: `git/merge.go`
- Test: `git/merge_test.go`

- [ ] **Step 1: Write failing test**

Append to `git/merge_test.go`:

```go
func TestMergeRebase_clean(t *testing.T) {
	t.Parallel()

	c, cloneDir, originDir := newDiskRepoWithOrigin(t)

	// Advance origin/main with an independent commit, then refresh remote.
	pusher := t.TempDir()
	run := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
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

	// Local feature with two commits on top of the original main tip.
	run(cloneDir, "checkout", "-b", "feature")

	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("feat%d.go", i)
		if err := os.WriteFile(filepath.Join(cloneDir, name), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}

		run(cloneDir, "add", name)
		run(cloneDir, "commit", "-m", fmt.Sprintf("feat: f%d", i))
	}

	if err := c.FetchOrigin(t.Context()); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
	}

	if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeRebase: %v", err)
	}

	// HEAD is on feature.
	branch, err := c.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}

	if branch != "feature" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "feature")
	}

	// feature tip == origin/main tip (soft-reset moved it back).
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

	// MERGE_HEAD must not exist (a real merge completed and was cleaned up).
	if _, err := os.Stat(filepath.Join(cloneDir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("MERGE_HEAD should not exist after MergeRebase, stat err = %v", err)
	}

	// Index has staged changes; working tree is at the merged state.
	statusCmd := exec.Command("git", "-C", cloneDir, "status", "--porcelain")
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git -run TestMergeRebase_clean -v`
Expected: FAIL — `c.MergeRebase undefined`.

- [ ] **Step 3: Implement `MergeRebase`**

Add to `git/merge.go`:

```go
// MergeRebase prepares featureBranch for a single-commit close. The mechanic
// is a real `git merge origin/<baseBranch>` (submodule-safe — handles gitlinks
// correctly, unlike `merge --squash`) followed by `git reset --soft origin/<baseBranch>`,
// leaving HEAD at origin/<baseBranch>, the working tree at the merged state, and
// the index staged with the consolidated diff. The transient merge commit
// produced by the merge step is unreachable after the reset and is eventually
// garbage-collected.
//
// Caller is responsible for the final commit (typically via the commitizen TUI
// form) and for rollback on failure.
func (c *Client) MergeRebase(ctx context.Context, featureBranch, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	remoteBase := "origin/" + baseBranch

	if err := c.runInteractive(ctx, root, "checkout", featureBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", featureBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", remoteBase); err != nil {
		return fmt.Errorf("merge %s: %w", remoteBase, err)
	}

	if err := c.runInteractive(ctx, root, "reset", "--soft", remoteBase); err != nil {
		return fmt.Errorf("reset --soft %s: %w", remoteBase, err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestMergeRebase_clean -v`
Expected: PASS.

- [ ] **Step 5: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add MergeRebase client method"
```

---

## Task 9: Submodule regression test for `MergeRebase`

This is the test that justifies the entire feature: it demonstrates that the new mechanism preserves a submodule pointer correctly where `merge --squash` would have lost or mis-staged it.

**Files:**
- Modify: `git/merge_test.go` (add helper `newDiskRepoWithOrigin_andSubmodule` + test)

- [ ] **Step 1: Add helper and failing test**

Append to `git/merge_test.go`:

```go
// newDiskRepoWithOrigin_andSubmodule extends newDiskRepoWithOrigin by:
//   - creating a separate "submodule" bare repo with 2 commits (subA, subB).
//   - adding the submodule to the SUT clone at path "sub", pinned to subA.
//   - pushing the submodule registration to origin/main.
// Returns the SUT client, clone dir, origin (bare parent) dir, and the two
// submodule commit SHAs (subA = initial pinning, subB = next pointer).
func newDiskRepoWithOrigin_andSubmodule(t *testing.T) (*Client, string, string, string, string) {
	t.Helper()

	c, cloneDir, originDir := newDiskRepoWithOrigin(t)

	run := func(cwd string, args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	capture := func(cwd string, args ...string) string {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v in %s: %v", args, cwd, err)
		}

		return strings.TrimSpace(string(out))
	}

	// Build a submodule repo with two commits.
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

	// Register submodule in the SUT clone pinned to subA.
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

		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}

	// On origin/main: advance an unrelated regular file (no submodule change).
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

	// On feature: advance the submodule pointer from subA to subB.
	run(cloneDir, "checkout", "-b", "feature")
	run(cloneDir, "-C", "sub", "fetch", "origin")
	run(cloneDir, "-C", "sub", "checkout", subB)
	run(cloneDir, "add", "sub")
	run(cloneDir, "commit", "-m", "feat: bump sub to subB")

	if err := c.FetchOrigin(t.Context()); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
	}

	if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeRebase: %v", err)
	}

	// Commit the staged changes to materialize the close result on feature.
	run(cloneDir, "commit", "-m", "feat: rebase close")

	// Inspect the resulting tree: the submodule entry must be subB, NOT subA.
	lsTree := exec.Command("git", "-C", cloneDir, "ls-tree", "HEAD", "sub")
	out, err := lsTree.Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}

	if !strings.Contains(string(out), subB) {
		t.Errorf("submodule pointer wrong after MergeRebase + commit.\nls-tree: %s\nwant subB: %s", out, subB)
	}
}
```

- [ ] **Step 2: Run the test to verify it passes**

Run: `mise exec -- go test ./git -run TestMergeRebase_preservesSubmodulePointer -v`
Expected: PASS. (The implementation from Task 8 already supports this case; this task is purely the regression test.)

If the test fails:
- Inspect the `ls-tree HEAD sub` output: it should show the `commit <sha>` line where `<sha>` equals `subB`. If it shows `subA`, the merge mechanic is regressing — investigate the merge step and re-check that we are *not* using `merge --squash`.

- [ ] **Step 3: Commit (user runs)**

```bash
git add git/merge_test.go
git commit -m "test(git): submodule regression case for MergeRebase"
```

---

## Task 10: Update `tui.IssueMergeStrategy` to a generic option list

**Files:**
- Modify: `tui/issue.go` (replace `IssueMergeStrategy` near line 558)
- Test: `tui/issue_test.go` (create if absent; if exists, append)

- [ ] **Step 1: Write failing test**

Create or append to `tui/issue_test.go`:

```go
package tui

import (
	"testing"
)

func TestIssueMergeStrategy_rendersGivenOptions(t *testing.T) {
	t.Parallel()

	selected := ""
	opts := []StrategyOption{
		{Value: "a", Label: "A", Hint: "first"},
		{Value: "b", Label: "B", Hint: "second"},
		{Value: "c", Label: "C", Hint: ""},
	}

	group := IssueMergeStrategy(&selected, opts)
	if group == nil {
		t.Fatal("IssueMergeStrategy returned nil group")
	}

	// Default selection should be the first option's value.
	if selected != "a" {
		t.Errorf("selected = %q, want %q (first option's value)", selected, "a")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./tui -run TestIssueMergeStrategy_rendersGivenOptions -v`
Expected: FAIL — `StrategyOption undefined` and/or signature mismatch.

- [ ] **Step 3: Replace the `IssueMergeStrategy` function in `tui/issue.go`**

Find the existing definition (currently around line 558) and replace it with:

```go
// StrategyOption is one entry rendered by IssueMergeStrategy. The picker does
// not know what the strategies mean — callers own the option list.
type StrategyOption struct {
	Value string // returned in *selected when this option is chosen
	Label string // shown in the picker
	Hint  string // optional one-line description rendered under the label
}

// IssueMergeStrategy renders a single-select picker from the given options and
// writes the chosen Value to *selected. *selected is pre-populated with the
// first option's Value as the default.
func IssueMergeStrategy(selected *string, options []StrategyOption) *huh.Group {
	if len(options) > 0 {
		*selected = options[0].Value
	}

	huhOpts := make([]huh.Option[string], len(options))
	for i, o := range options {
		label := o.Label
		if o.Hint != "" {
			label = o.Label + "\n" + descStyle.Render(o.Hint)
		}

		huhOpts[i] = huh.NewOption(label, o.Value)
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Merge strategy:").
			Options(huhOpts...).
			Value(selected),
	)
}
```

If `descStyle` is not already defined in this file, look for it in the package (it appears in the existing `IssueActionSelect` at line 51 — it is package-scoped, so it should be accessible from this function).

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./tui -run TestIssueMergeStrategy_rendersGivenOptions -v`
Expected: PASS.

Run also: `mise exec -- go build ./...`
Expected: build failure in `cmd/issue/close.go` because it still calls the old `IssueMergeStrategy(squash *bool)` signature. **That is expected and fixed in Task 11.**

- [ ] **Step 5: Commit (user runs)**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "refactor(tui): make IssueMergeStrategy take generic option list"
```

---

## Task 11: Strategy enum + `doMerge` dispatch + `doDeleteBranch` signature

This task makes `cmd/issue/close.go` compile again against the new `tui.IssueMergeStrategy` signature. The actual `doRebaseClose` body is stubbed; Task 12 fills it in.

**Files:**
- Modify: `cmd/issue/close.go`

- [ ] **Step 1: Replace the strategy block in `doMerge`**

In `cmd/issue/close.go`:

1. Add near the top of the file (after the `shortSHALen` constant):

```go
type MergeStrategy string

const (
	StrategySquash  MergeStrategy = "squash"
	StrategyRebase  MergeStrategy = "rebase"
	StrategyClassic MergeStrategy = "classic"
)

// errFastForwardDeferred signals that the rebase commit landed on feature but
// local base could not fast-forward (diverged from origin/<base>). closeRunE
// uses it to skip post-merge bookkeeping while still exiting cleanly.
var errFastForwardDeferred = errors.New("commit created, fast-forward deferred")
```

Make sure `errors` is imported in `cmd/issue/close.go`.

2. Replace the `doMerge` function (currently lines 137–183) with:

```go
// doMerge runs the full merge flow: dry-run, strategy picker, confirm, then
// the actual merge. aborted is true when the user cancelled at the confirm prompt.
func doMerge(ctx context.Context, mc mergeContext) (strategy MergeStrategy, aborted bool, err error) {
	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, mc.baseBranch)
	if err != nil {
		return "", false, fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(mc.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(mc.client.IO().Out, "  "+f)
		}
		fmt.Fprintln(mc.client.IO().Out, "Aborting.")

		return "", false, fmt.Errorf("merge conflicts in branch %q", mc.pickedBranch.BranchName)
	}

	var picked string
	strategyForm := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{Value: string(StrategyRebase), Label: "Rebase", Hint: "Single clean commit on local base, submodule-safe (recommended)"},
		{Value: string(StrategySquash), Label: "Squash", Hint: "git merge --squash — fast, but not submodule-safe"},
		{Value: string(StrategyClassic), Label: "Classic", Hint: "git merge --no-ff — preserves full history"},
	})
	if err := huh.NewForm(strategyForm).Run(); err != nil {
		return "", false, fmt.Errorf("strategy picker: %w", err)
	}

	strategy = MergeStrategy(picked)

	var confirmed bool
	confirmForm := tui.IssueMergeConfirm(mc.pickedBranch.BranchName, mc.baseBranch, string(strategy), &confirmed)
	if err := huh.NewForm(confirmForm).Run(); err != nil {
		return "", false, fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		return strategy, true, nil
	}

	switch strategy {
	case StrategyClassic:
		if err := mc.client.MergeNoFF(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
			return strategy, false, fmt.Errorf("merge no-ff: %w", err)
		}
	case StrategySquash:
		if err := doSquashCommit(ctx, mc); err != nil {
			return strategy, false, err
		}
	case StrategyRebase:
		if err := doRebaseClose(ctx, mc); err != nil {
			return strategy, false, err
		}
	default:
		return strategy, false, fmt.Errorf("unknown strategy %q", strategy)
	}

	return strategy, false, nil
}
```

3. Add the stub `doRebaseClose` at the bottom of the file (Task 12 replaces this body):

```go
// doRebaseClose is implemented in Task 12.
func doRebaseClose(_ context.Context, _ mergeContext) error {
	return errors.New("doRebaseClose not yet implemented")
}
```

4. Replace `doDeleteBranch` to take the strategy:

```go
func doDeleteBranch(cmd *cobra.Command, c *git.Client, pickedBranch *store.BranchRow, strategy MergeStrategy) error {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(pickedBranch.BranchName, &shouldDelete)).Run(); err != nil {
		return fmt.Errorf("delete branch form: %w", err)
	}

	if shouldDelete {
		// Squash and Rebase rewrite history relative to the base branch, so the
		// safe -d would refuse. Classic preserves ancestry → safe -d works.
		force := strategy == StrategySquash || strategy == StrategyRebase
		if err := c.DeleteLocalBranch(cmd.Context(), pickedBranch.BranchName, force); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: delete branch: %v\n", err)
		}
	}

	return nil
}
```

5. Update `closeRunE` to:
   - receive the strategy instead of the bool;
   - skip `updateStatus` and `doDeleteBranch` when `err == errFastForwardDeferred`.

Replace the existing tail (from `squash, aborted, err := doMerge…`) with:

```go
strategy, aborted, err := doMerge(ctx, mc)
if err != nil {
	if errors.Is(err, errFastForwardDeferred) {
		// Commit landed on feature; local base couldn't FF. Leave the user
		// to reconcile manually — do not update store/tracker, do not prompt
		// for branch deletion.
		return nil
	}

	return err
}

if aborted {
	fmt.Fprintln(mc.client.IO().Out, "Aborted.")

	return nil
}

i.updateStatus(cmd, s, picked)

if err := doDeleteBranch(cmd, client, picked, strategy); err != nil {
	return err
}

fmt.Fprintf(mc.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

return nil
```

Note the existing `fmt.Println("Aborted.")` is replaced with `fmt.Fprintln(mc.client.IO().Out, "Aborted.")` to respect IO injection (see memory `feedback_respect_io_injection`). The final success message follows the same pattern.

- [ ] **Step 2: Verify the package builds**

Run: `mise exec -- go build ./...`
Expected: build succeeds.

- [ ] **Step 3: Run the existing test suite**

Run: `mise exec -- go test ./...`
Expected: all tests pass. (The rebase orchestrator is stubbed — only existing classic/squash paths exercise their existing tests.)

- [ ] **Step 4: Commit (user runs)**

```bash
git add cmd/issue/close.go
git commit -m "refactor(issue): add MergeStrategy enum and ternary dispatch in doMerge"
```

---

## Task 12: Implement `doRebaseClose`

Replace the stub from Task 11 with the full orchestrator.

**Files:**
- Modify: `cmd/issue/close.go`

- [ ] **Step 1: Replace the stub `doRebaseClose`**

Replace the stub with:

```go
// doRebaseClose runs the Rebase strategy: pre-flights the working tree, fetches
// origin, validates the merge endpoint with merge-tree, performs a real
// `git merge origin/<base>` (submodule-safe), soft-resets feature back to
// origin/<base> so the merged diff is staged, drives the commitizen TUI form,
// commits, and fast-forwards local base. Rollback uses a named-return closure:
// any failure between the soft-reset and a successful commit triggers
// `git reset --hard <featureOrigSHA>` to atomically restore the feature ref.
// The post-commit FF failure is signalled with errFastForwardDeferred so the
// caller can skip post-merge bookkeeping without rolling back the new commit.
func doRebaseClose(ctx context.Context, mc mergeContext) (err error) {
	dirty, err := mc.client.IsDirty(ctx)
	if err != nil {
		return fmt.Errorf("dirty check: %w", err)
	}

	if dirty {
		return errors.New("working tree has uncommitted modifications — commit or stash before closing")
	}

	if err := mc.client.Checkout(ctx, mc.pickedBranch.BranchName); err != nil {
		return fmt.Errorf("checkout %s: %w", mc.pickedBranch.BranchName, err)
	}

	featureOrigSHA, err := mc.client.ResolveRef("HEAD")
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}

	if err := mc.client.FetchOrigin(ctx); err != nil {
		return fmt.Errorf("fetch origin: %w", err)
	}

	remoteBase := "origin/" + mc.baseBranch

	integrated, err := mc.client.IsAncestor(ctx, mc.pickedBranch.BranchName, remoteBase)
	if err != nil {
		return fmt.Errorf("ancestor check: %w", err)
	}

	if integrated {
		return fmt.Errorf("%q has no commits ahead of %s — already integrated?",
			mc.pickedBranch.BranchName, remoteBase)
	}

	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, remoteBase)
	if err != nil {
		return fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(mc.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(mc.client.IO().Out, "  "+f)
		}

		return fmt.Errorf("merge conflicts vs %s in %q", remoteBase, mc.pickedBranch.BranchName)
	}

	if err := mc.client.MergeRebase(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge rebase: %w", err)
	}

	// Rollback guard. Skip rollback for the post-commit FF-deferred sentinel:
	// the clean commit already landed on feature, rolling back would destroy it.
	// Use a named return so the closure observes the actual err at function exit.
	defer func() {
		if err == nil || errors.Is(err, errFastForwardDeferred) {
			return
		}

		if rbErr := mc.client.ResetHard(ctx, featureOrigSHA.String()); rbErr != nil {
			err = fmt.Errorf("rollback after %w failed: %v", err, rbErr)

			return
		}

		fmt.Fprintf(mc.client.IO().Err,
			"Rolled back: feature branch %q restored to %s\n",
			mc.pickedBranch.BranchName, featureOrigSHA.String()[:shortSHALen])
	}()

	baseOriginSHA, err := mc.client.ResolveRef(remoteBase)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", remoteBase, err)
	}

	hint := commitpkg.IssueHint{
		IssueID:    mc.pickedBranch.IssueSlug,
		BranchType: mc.pickedBranch.Type,
	}
	prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Squashed close of %s into %s.",
		featureOrigSHA.String()[:shortSHALen], baseOriginSHA.String()[:shortSHALen])

	authors, authorsErr := mc.client.Authors()
	if authorsErr != nil {
		slog.Warn("could not load author list", "error", authorsErr)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}

	if _, err := mc.client.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if ffErr := mc.client.FastForwardOnly(ctx, mc.pickedBranch.BranchName, mc.baseBranch); ffErr != nil {
		fmt.Fprintf(mc.client.IO().Err,
			"Commit created on %q but local %s has diverged from %s.\n"+
				"Run `git pull --ff-only` on %s, then `git merge --ff-only %s` to land it.\n",
			mc.pickedBranch.BranchName, mc.baseBranch, remoteBase,
			mc.baseBranch, mc.pickedBranch.BranchName)

		return errFastForwardDeferred
	}

	return nil
}
```

- [ ] **Step 2: Verify the package builds**

Run: `mise exec -- go build ./...`
Expected: build succeeds.

- [ ] **Step 3: Run the full test suite**

Run: `mise exec -- go test ./...`
Expected: all tests pass.

- [ ] **Step 4: Commit (user runs)**

```bash
git add cmd/issue/close.go
git commit -m "feat(issue): add submodule-safe rebase close strategy"
```

---

## Task 13: Manual end-to-end verification

These scenarios are the spec's "Manual end-to-end" section. Run them after Task 12 lands.

- [ ] **Step 1: Build and install**

```bash
mise exec -- go test ./...
mise exec -- go build -o ./bin/git-zf .
make install
```

Expected: all green, binary on `$PATH` via `$(git --exec-path)/git-zf`.

- [ ] **Step 2: Scenario 1 — clean rebase close on a repo with submodules**

In a real repo with at least one submodule, on an in-progress feature branch:

```bash
git zf -d issue close
```

In the picker: **Rebase**. Confirm. The TUI commit form opens pre-filled (`type`, `scope`, `subject = "Squashed close of <orig> into <base>."`). Submit. Expect:

- One new commit on the local base.
- `git ls-tree HEAD <submodule-path>` shows the submodule SHA from feature (not the base's stale pointer).
- `git log --oneline -2` shows the new clean commit at the tip.

- [ ] **Step 3: Scenario 2 — TUI abort (Esc) rollback**

In the same setup as Scenario 1, run `git zf issue close` with Rebase. In the commit form, press Esc.

Expect on stderr:
```
Rolled back: feature branch "<name>" restored to <sha>
```

`git log <feature> -1` should show the **original** feature tip, not any new commit. `git status` should be clean.

- [ ] **Step 4: Scenario 3 — pre-commit hook rejection**

Install a hook that always fails:

```bash
echo -e '#!/bin/sh\nexit 1' > .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
```

Run `git zf issue close` with Rebase. The commit will fail. Expect the same rollback message as Scenario 2 and feature restored to its original tip. Remove the hook after testing:

```bash
rm .git/hooks/pre-commit
```

- [ ] **Step 5: Scenario 4 — FF-deferred path**

Manufacture a divergence between local base and origin/base:

```bash
git checkout main
git commit --allow-empty -m "local-only commit"
```

Run `git zf issue close` with Rebase on a feature branch. Expect:

- The commit lands on feature.
- Stderr:
  ```
  Commit created on "<feature>" but local main has diverged from origin/main.
  Run `git pull --ff-only` on main, then `git merge --ff-only <feature>` to land it.
  ```
- Local base is **unchanged** (still has the local-only commit, not the new clean commit).
- No "Branch …merged" success message.
- No delete-branch prompt.
- Store/tracker NOT updated (verify via `git zf issue list` showing the issue still in_progress).

Reset for further testing:
```bash
git reset --hard HEAD~1   # drop the local-only commit
```

- [ ] **Step 6: Scenario 5 — already-integrated abort**

On a feature branch whose tip is already an ancestor of `origin/<base>` (e.g., a freshly-pulled merged branch), run `git zf issue close` with Rebase. Expect immediate abort with:

```
"<feature>" has no commits ahead of origin/main — already integrated?
```

No mutation. `git status` should show no change.

- [ ] **Step 7: Scenario 6 — dirty tree pre-flight**

With a tracked-file modification staged or unstaged, run `git zf issue close` with Rebase. Expect:

```
working tree has uncommitted modifications — commit or stash before closing
```

No mutation. `git status` still shows the modification.

Repeat with **only an untracked file** present (e.g., `echo > scratch.tmp`) — expect the close to proceed normally (untracked files do not block).

- [ ] **Step 8: Report any deviations**

Each scenario above has a precise expected outcome. If any deviates, capture:
- The exact command sequence.
- The full output (stdout + stderr).
- `git log --oneline -5` and `git status` snapshots before and after.

Surface these as a follow-up bug — do **not** edit the implementation until the user has reviewed.

---

## Notes for the implementer

- The plan creates `git/git_test.go`. The project did not previously have one. If you prefer to split git client tests into multiple files (e.g., `git_io_test.go`, `git_dirty_test.go`), do so consistently; the test names above don't depend on file location.
- All new tests use `t.Context()` for the cancellation context — this matches the pattern in the existing `merge_test.go`.
- The `descStyle` reference in Task 10 is the same package-level variable used by `IssueActionSelect` (search `tui/` for its definition if you need to confirm).
- The `commitpkg` and `tui` imports in `cmd/issue/close.go` already exist; no new package-level imports are needed beyond `errors` (added in Task 11).
- Don't run `git add` or `git commit` yourself — at each "Commit (user runs)" step, suggest the commit and pause for the user to run it before continuing to the next task.
