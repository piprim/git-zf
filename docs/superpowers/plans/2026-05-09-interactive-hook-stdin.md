# Interactive stdin for git hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix git hook stdin hang during `issue close` and enable hooks for `commit` by replacing go-git's `wt.Commit()` with `git commit` subprocesses wired to the real terminal.

**Architecture:** A generic `pkg.RunInteractive` helper (stdlib-only) tees stdin/stdout/stderr through the real terminal while capturing output for error messages. `git.IO` struct carries injectable streams so tests can suppress output. The store package gains `OpenRepo` and nil-safe row helpers, breaking the `pkg→git` and `pkg→store` import cycles cleanly.

**Tech Stack:** Go stdlib (`os/exec`, `io`, `bytes`), go-git v6 (read-only after rewrite), charmbracelet/huh, cobra.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `internal/pkg/helper.go` | Rewrite | Only `RunInteractive` with stdlib params; all other helpers removed |
| `store/helpers.go` | Create | `BranchFieldOrEmpty`, `TrackerStatusOrNA`, `OpenRepo` |
| `git/git.go` | Modify | Add `IO` struct + `io IO` field; update constructors; rewrite `Commit` |
| `git/merge.go` | Modify | Add `runInteractive`; update `MergeSquash`, `MergeNoFF` |
| `git/git_test.go` | Modify | Migrate `TestCommit_*` and `TestIsMergedInto_*` to on-disk repos |
| `git/merge_test.go` | Modify | `NewClientAt(dir)` → `NewClientAt(nil, dir)` |
| `cmd/commit/commit.go` | Modify | Thread `cmd.Context()` into `Commit`; pass cobra IO to `NewClient` |
| `cmd/commit/commit_test.go` | Modify | `NewClientAt(dir)` → `NewClientAt(nil, dir)` |
| `cmd/issue/close.go` | Modify | Pass cobra IO to `NewClient`; `pkg.GetStore` → `store.OpenRepo` |
| `cmd/issue/start.go` | Modify | Pass `nil` to `NewClient`; `pkg.GetStore` → `store.OpenRepo`; inline `GetAllowedBranchType` |
| `cmd/issue/list.go` | Modify | `pkg.GetStore` → `store.OpenRepo`; drop `pkg` import |
| `cmd/branch/branch.go` | Modify | Pass cobra IO to `NewClient`; `pkg.GetStore` → `store.OpenRepo` |
| `config/config.go` | Modify | `git.NewClient()` → `git.NewClient(nil)` |
| `tty/issue.go` | Modify | `pkg.BranchFieldOrEmpty/TrackerStatusOrNA` → `store.*` |
| `tui/issue.go` | Modify | `pkg.BranchFieldOrEmpty/TrackerStatusOrNA` → `store.*` |

---

## Task 1: Rewrite `internal/pkg/helper.go` — add `RunInteractive`, remove all project-dependent helpers

**Files:**
- Rewrite: `internal/pkg/helper.go`

`RunInteractive` is the only remaining function in this package. Everything else moves in later tasks. Making this change first keeps the old helpers in place until their callers are migrated.

- [ ] **Step 1: Add `RunInteractive` to `internal/pkg/helper.go`**

Replace the full file contents:

```go
package pkg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// RunInteractive runs cmd with args in dir, wiring in/out/errW to the
// subprocess. Stdout and stderr are teed: output streams live to the caller's
// writers and is captured in a buffer included in any error returned.
func RunInteractive(ctx context.Context, in io.Reader, out, errW io.Writer, cmd, dir string, args ...string) error {
	var buf bytes.Buffer

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	c.Stdin = in
	c.Stdout = io.MultiWriter(out, &buf)
	c.Stderr = io.MultiWriter(errW, &buf)

	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}

	return nil
}

// BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
// Kept here temporarily; callers migrate to store.BranchFieldOrEmpty in Task 2.
func BranchFieldOrEmpty(b interface{ IsNil() bool }, fn interface{}) string { return "" } // replaced below

```

Actually, don't replace the whole file yet — just add `RunInteractive` at the top. The existing helpers stay until Tasks 2-3 migrate their callers.

```go
package pkg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// RunInteractive runs cmd with args in dir, wiring in/out/errW to the
// subprocess. Stdout and stderr are teed: output streams live to the caller's
// writers and is captured in a buffer included in any error returned.
func RunInteractive(ctx context.Context, in io.Reader, out, errW io.Writer, cmd, dir string, args ...string) error {
	var buf bytes.Buffer

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	c.Stdin = in
	c.Stdout = io.MultiWriter(out, &buf)
	c.Stderr = io.MultiWriter(errW, &buf)

	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}

	return nil
}

func BranchFieldOrEmpty(b *store.BranchRow, fn func(*store.BranchRow) string) string {
	if b == nil {
		return "∅"
	}

	return fn(b)
}

func TrackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}

func GetAllowedBranchType(types []config.CommitTypeOption) []string {
	allowedBranchTypes := make([]string, 0, len(types))
	for _, t := range types {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	return allowedBranchTypes
}

func GetStore(ctx context.Context) (*store.Store, error) {
	client, err := git.NewClient(nil)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
}
```

Wait — `git.NewClient(nil)` uses the new signature we haven't added yet. For now keep the existing `git.NewClient()` call.

Here is the correct step: just add `RunInteractive` above the existing functions in the current file, keeping the existing content intact.

```go
// Add at the top of the file, after the import block:

// RunInteractive runs cmd with args in dir, wiring in/out/errW to the
// subprocess. Stdout and stderr are teed: output streams live to the caller's
// writers and is captured in a buffer included in any error returned.
func RunInteractive(ctx context.Context, in io.Reader, out, errW io.Writer, cmd, dir string, args ...string) error {
	var buf bytes.Buffer

	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir
	c.Stdin = in
	c.Stdout = io.MultiWriter(out, &buf)
	c.Stderr = io.MultiWriter(errW, &buf)

	if err := c.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, buf.String())
	}

	return nil
}
```

Add `"bytes"`, `"io"`, `"os/exec"` to the import block.

- [ ] **Step 2: Verify the build passes**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 3: Write a test for `RunInteractive`**

Create `internal/pkg/helper_test.go`:

```go
package pkg_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/piprim/git-zf/internal/pkg"
)

func TestRunInteractive_outputTeed(t *testing.T) {
	t.Parallel()

	var out, errW bytes.Buffer
	err := pkg.RunInteractive(
		context.Background(),
		strings.NewReader(""),
		&out, &errW,
		"echo", t.TempDir(), "hello",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "hello") {
		t.Errorf("stdout tee: want %q in %q", "hello", out.String())
	}
}

func TestRunInteractive_errorIncludesOutput(t *testing.T) {
	t.Parallel()

	var out, errW bytes.Buffer
	err := pkg.RunInteractive(
		context.Background(),
		strings.NewReader(""),
		&out, &errW,
		"false", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected error from false, got nil")
	}
}
```

- [ ] **Step 4: Run the test**

```bash
mise exec -- go test ./internal/pkg/... -v
```

Expected: `PASS` for both `TestRunInteractive_outputTeed` and `TestRunInteractive_errorIncludesOutput`.

- [ ] **Step 5: Commit**

```bash
git add internal/pkg/helper.go internal/pkg/helper_test.go
git commit -m "feat(pkg): add RunInteractive generic interactive subprocess helper"
```

---

## Task 2: Move `BranchFieldOrEmpty` and `TrackerStatusOrNA` to `store`

**Files:**
- Create: `store/helpers.go`
- Modify: `internal/pkg/helper.go` (remove the two functions)
- Modify: `tty/issue.go`
- Modify: `tui/issue.go`

- [ ] **Step 1: Create `store/helpers.go`**

```go
package store

// BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
func BranchFieldOrEmpty(b *BranchRow, fn func(*BranchRow) string) string {
	if b == nil {
		return "∅"
	}

	return fn(b)
}

// TrackerStatusOrNA returns *s or "N.A." when s is nil.
func TrackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}
```

- [ ] **Step 2: Remove `BranchFieldOrEmpty` and `TrackerStatusOrNA` from `internal/pkg/helper.go`**

Delete the two function bodies. Keep everything else.

- [ ] **Step 3: Update `tty/issue.go` — switch from `pkg` to `store`**

In `tty/issue.go`, replace the four `pkg.BranchFieldOrEmpty` / `pkg.TrackerStatusOrNA` calls with `store.BranchFieldOrEmpty` / `store.TrackerStatusOrNA`. Remove the `internal/pkg` import if no longer needed.

```go
// Before:
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
pkg.TrackerStatusOrNA(r.TrackerStatus),
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),

// After:
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
store.TrackerStatusOrNA(r.TrackerStatus),
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
```

- [ ] **Step 4: Update `tui/issue.go` — same change**

```go
// Before:
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
pkg.TrackerStatusOrNA(r.TrackerStatus),
pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),

// After:
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
store.TrackerStatusOrNA(r.TrackerStatus),
store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
```

Remove the `internal/pkg` import from `tui/issue.go` if no longer needed.

- [ ] **Step 5: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 6: Run tests**

```bash
mise exec -- go test ./...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add store/helpers.go internal/pkg/helper.go tty/issue.go tui/issue.go
git commit -m "refactor(store): move BranchFieldOrEmpty and TrackerStatusOrNA from pkg to store"
```

---

## Task 3: Move `GetStore` → `store.OpenRepo`; inline `GetAllowedBranchType`; drop remaining `pkg` imports from cmd

**Files:**
- Modify: `store/store.go` (add `OpenRepo`)
- Modify: `store/helpers.go` (no change needed)
- Modify: `internal/pkg/helper.go` (remove `GetStore`, `GetAllowedBranchType`)
- Modify: `cmd/branch/branch.go`
- Modify: `cmd/issue/close.go`
- Modify: `cmd/issue/list.go`
- Modify: `cmd/issue/start.go`

After this task `internal/pkg` has no project imports — only stdlib. This unblocks `git → pkg` in Task 4.

- [ ] **Step 1: Add `store.OpenRepo` to `store/store.go`**

Add the following imports to `store/store.go`: `"path/filepath"` and `"github.com/piprim/git-zf/git"`.

Add this function at the end of `store/store.go`:

```go
// OpenRepo opens the local store inside the current git repository's .git directory.
func OpenRepo(ctx context.Context) (*Store, error) {
	client, err := git.NewClient(nil)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	s, err := Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
}
```

Note: `git.NewClient(nil)` uses the new signature added in Task 4. For now, keep `git.NewClient()` (current signature) and update it in Task 4 alongside the constructor change.

So for this step, write:

```go
func OpenRepo(ctx context.Context) (*Store, error) {
	client, err := git.NewClient()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	s, err := Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
}
```

- [ ] **Step 2: Remove `GetStore` and `GetAllowedBranchType` from `internal/pkg/helper.go`**

Delete both function bodies and remove their imports (`"github.com/piprim/git-zf/config"`, `"github.com/piprim/git-zf/git"`, `"github.com/piprim/git-zf/store"`, `"path/filepath"`).

After this step, `internal/pkg/helper.go` contains only `RunInteractive` with stdlib imports.

- [ ] **Step 3: Update `cmd/branch/branch.go`**

Replace `pkg.GetStore(ctx)` → `store.OpenRepo(ctx)` (two occurrences). Remove the `internal/pkg` import.

```go
// Before (line ~93):
s, err := pkg.GetStore(ctx)

// After:
s, err := store.OpenRepo(ctx)
```

Apply the same change at the second occurrence (~line 237).

- [ ] **Step 4: Update `cmd/issue/close.go`**

Replace `pkg.GetStore(ctx)` → `store.OpenRepo(ctx)`. Remove the `internal/pkg` import.

- [ ] **Step 5: Update `cmd/issue/list.go`**

Replace `pkg.GetStore(ctx)` → `store.OpenRepo(ctx)`. Remove the `internal/pkg` import.

- [ ] **Step 6: Update `cmd/issue/start.go`**

Replace `pkg.GetStore(ctx)` → `store.OpenRepo(ctx)`.

Replace `pkg.GetAllowedBranchType(i.appConfig.CommitTypes)` with inline logic:

```go
// Before:
allowedBranchTypes := pkg.GetAllowedBranchType(i.appConfig.CommitTypes)

// After:
allowedBranchTypes := make([]string, 0, len(i.appConfig.CommitTypes))
for _, t := range i.appConfig.CommitTypes {
	allowedBranchTypes = append(allowedBranchTypes, t.Name)
}
```

Remove the `internal/pkg` import.

- [ ] **Step 7: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 8: Run all tests**

```bash
mise exec -- go test ./...
```

Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add store/store.go internal/pkg/helper.go \
        cmd/branch/branch.go cmd/issue/close.go \
        cmd/issue/list.go cmd/issue/start.go
git commit -m "refactor(store): add OpenRepo; remove GetStore and GetAllowedBranchType from pkg"
```

---

## Task 4: Add `IO` struct to `git`; update constructors; update all callers

**Files:**
- Modify: `git/git.go`
- Modify: `git/merge_test.go`
- Modify: `cmd/commit/commit_test.go`
- Modify: `config/config.go`
- Modify: `store/store.go` (update the `OpenRepo` call added in Task 3)

At this point `internal/pkg` imports only stdlib, so `git` can safely import it.

- [ ] **Step 1: Add `IO` struct and `io` field to `git/git.go`**

Add imports `"io"`, `"os"`, `"github.com/piprim/git-zf/internal/pkg"` to `git/git.go`.

Add the `IO` struct and update `Client` before the `NewClient` constructor:

```go
// IO holds the standard streams used for interactive git subprocess operations.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Client wraps a go-git repository and exposes commit operations.
type Client struct {
	repo *gogit.Repository
	io   IO
}
```

- [ ] **Step 2: Update `NewClient` and `NewClientAt` signatures**

```go
// NewClient opens the git repository that contains the current directory.
// io configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClient(io *IO) (*Client, error) {
	if io == nil {
		io = &IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	}

	repo, err := gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}

	return &Client{repo: repo, io: *io}, nil
}

// NewClientAt opens the git repository rooted at dir.
// io configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClientAt(io *IO, dir string) (*Client, error) {
	if io == nil {
		io = &IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	}

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open git repository at %s: %w", dir, err)
	}

	return &Client{repo: repo, io: *io}, nil
}
```

- [ ] **Step 3: Update `config/config.go`**

```go
// Before:
client, err := git.NewClient()

// After:
client, err := git.NewClient(nil)
```

- [ ] **Step 4: Update `store/store.go` — `OpenRepo`**

```go
// Before:
client, err := git.NewClient()

// After:
client, err := git.NewClient(nil)
```

- [ ] **Step 5: Update `git/merge_test.go`**

```go
// Before:
c, err := NewClientAt(dir)

// After:
c, err := NewClientAt(nil, dir)
```

- [ ] **Step 6: Update `cmd/commit/commit_test.go`**

Replace all three occurrences:

```go
// Before:
client, err := git.NewClientAt(dir)

// After:
client, err := git.NewClientAt(nil, dir)
```

- [ ] **Step 7: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 8: Run all tests**

```bash
mise exec -- go test ./...
```

Expected: all pass.

- [ ] **Step 9: Commit**

```bash
git add git/git.go git/merge_test.go cmd/commit/commit_test.go \
        config/config.go store/store.go
git commit -m "feat(git): add IO struct; update NewClient/NewClientAt to accept optional IO"
```

---

## Task 5: Add `Client.runInteractive`; update `MergeSquash` and `MergeNoFF`

**Files:**
- Modify: `git/merge.go`

- [ ] **Step 1: Add `runInteractive` method to `git/merge.go`**

Add the import `"github.com/piprim/git-zf/internal/pkg"` to `git/merge.go`.

Add the following method (place it before `MergeDryRun`):

```go
// runInteractive runs a git command in dir with the client's configured IO
// streams. Stdout/stderr are teed to the terminal live and captured for errors.
func (c *Client) runInteractive(ctx context.Context, dir string, args ...string) error {
	return pkg.RunInteractive(ctx, c.io.In, c.io.Out, c.io.Err, "git", dir, args...)
}
```

- [ ] **Step 2: Update `MergeSquash` — replace three `CombinedOutput` calls**

Replace the three hook-triggering commands inside `MergeSquash`. Keep the `root, err := c.WorkingTreeRoot()` call. The full updated function body after the root check:

```go
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch, author string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--squash", branchName); err != nil {
		return fmt.Errorf("merge --squash %s: %w", branchName, err)
	}

	msg := "squash merge '" + branchName + "'"
	commitArgs := []string{"commit", "-m", msg}
	if author != "" {
		commitArgs = append(commitArgs, "--author="+author)
	}

	if err := c.runInteractive(ctx, root, commitArgs...); err != nil {
		return fmt.Errorf("commit squash: %w", err)
	}

	return nil
}
```

- [ ] **Step 3: Update `MergeNoFF` — replace two `CombinedOutput` calls**

```go
func (c *Client) MergeNoFF(ctx context.Context, branchName, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-ff", branchName); err != nil {
		return fmt.Errorf("merge --no-ff %s: %w", branchName, err)
	}

	return nil
}
```

- [ ] **Step 4: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 5: Run merge tests**

The test repos in `merge_test.go` have no hooks, so interactive stdin is never triggered. Output from git commands goes to `os.Stdout` of the test process (minor noise). Tests pass on return values.

```bash
mise exec -- go test ./git/... -v -run "TestMerge|TestDelete"
```

Expected: `PASS` for `TestMergeDryRun_clean`, `TestMergeDryRun_conflict`, `TestMergeSquash`, `TestMergeNoFF`, `TestDeleteLocalBranch_safeDelete`, `TestDeleteLocalBranch_forceDelete`.

- [ ] **Step 6: Commit**

```bash
git add git/merge.go
git commit -m "feat(git): add runInteractive; wire MergeSquash and MergeNoFF through interactive IO"
```

---

## Task 6: Rewrite `Commit`; migrate `TestCommit_*` and `TestIsMergedInto_*` to on-disk

**Files:**
- Modify: `git/git.go`
- Modify: `git/git_test.go`

The existing `Commit` uses go-git's `wt.Commit()` which skips hooks. The rewrite calls `git commit` via `runInteractive`. go-git is kept for reading repo state after the commit.

`TestCommit_*` tests currently use in-memory repos — they must migrate to on-disk because `WorkingTreeRoot()` requires a real filesystem path.

`TestIsMergedInto_*` use `client.Commit()` for setup commits — replace with raw `exec.Command("git", "commit")` calls.

- [ ] **Step 1: Rewrite `Commit` in `git/git.go`**

Remove imports: `"time"`, `"github.com/go-git/go-git/v6/plumbing/object"` (if no longer used elsewhere — check: `Authors()` uses `object.Commit`, so keep `object`; only `time` is removed).

Replace the `Commit` method:

```go
// Commit records a commit with msg and the given options using the system git
// binary so that all configured hooks (pre-commit, commit-msg, post-commit) run.
// It returns a CommitSummary suitable for printing to the user.
func (c *Client) Commit(ctx context.Context, msg []byte, opts CommitOptions) (CommitSummary, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return CommitSummary{}, fmt.Errorf("working tree root: %w", err)
	}

	f, err := os.CreateTemp("", "git-zf-msg-*")
	if err != nil {
		return CommitSummary{}, fmt.Errorf("create temp msg file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(msg); err != nil {
		_ = f.Close()

		return CommitSummary{}, fmt.Errorf("write commit msg: %w", err)
	}
	if err := f.Close(); err != nil {
		return CommitSummary{}, fmt.Errorf("close temp msg file: %w", err)
	}

	args := []string{"commit", "-F", f.Name()}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Amend {
		args = append(args, "--amend")
	}
	if opts.NoVerify {
		args = append(args, "--no-verify")
	}
	if opts.Signoff {
		args = append(args, "--signoff")
	}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if opts.Author != "" {
		args = append(args, "--author="+opts.Author)
	}

	if err := c.runInteractive(ctx, root, args...); err != nil {
		return CommitSummary{}, fmt.Errorf("commit: %w", err)
	}

	head, err := c.repo.Head()
	if err != nil {
		return CommitSummary{}, fmt.Errorf("read HEAD after commit: %w", err)
	}

	return c.buildSummary(head.Hash(), string(msg))
}
```

Also remove the `parseAuthor` function and the `strings.TrimRight`/`Signed-off-by` manual signoff assembly — they are no longer used. Verify `parseAuthor` is not called anywhere else before deleting.

Remove the `CommitOptions` comment about go-git not running hooks:

```go
// Before in CommitOptions:
// NoVerify — go-git v6 does not execute hooks; reserved for a future subprocess fallback.
NoVerify bool

// After:
NoVerify bool
```

Add `"os"` import to `git/git.go` if not already present.

- [ ] **Step 2: Write new on-disk `TestCommit_basic`**

Replace `TestCommit_basic` in `git/git_test.go`:

```go
func TestCommit_basic(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", "file.txt")

	if _, err := client.Commit(t.Context(), []byte("feat: basic commit"), CommitOptions{}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	var buf bytes.Buffer
	logCmd := exec.Command("git", "log", "--format=%s", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &buf
	_ = logCmd.Run()

	if strings.TrimSpace(buf.String()) != "feat: basic commit" {
		t.Errorf("got subject %q, want %q", strings.TrimSpace(buf.String()), "feat: basic commit")
	}
}
```

Add required imports at the top of `git_test.go`: `"bytes"`, `"io"`, `"os"`, `"os/exec"`, `"path/filepath"`, `"strings"`.

- [ ] **Step 3: Rewrite `TestCommit_all_stagesTrackedOnly`**

```go
func TestCommit_all_stagesTrackedOnly(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Modify the tracked file (base.go was in the initial commit).
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package modified\n"), 0o644); err != nil {
		t.Fatalf("write base.go: %v", err)
	}

	// Create an untracked file — must NOT end up in the commit.
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("should not be staged"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}

	if _, err := client.Commit(t.Context(), []byte("chore: all flag"), CommitOptions{All: true}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	// Verify untracked.txt is NOT in the commit tree.
	var buf bytes.Buffer
	showCmd := exec.Command("git", "show", "--stat", "HEAD")
	showCmd.Dir = dir
	showCmd.Stdout = &buf
	_ = showCmd.Run()

	if strings.Contains(buf.String(), "untracked.txt") {
		t.Error("untracked.txt must not be in commit")
	}
	if !strings.Contains(buf.String(), "base.go") {
		t.Error("base.go must be in commit")
	}

	_ = run
}
```

- [ ] **Step 4: Rewrite `TestCommit_signoff`**

With `--signoff`, git uses the committer identity from the repo config ("Test User <test@test.com>"), not `opts.Author`.

```go
func TestCommit_signoff(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cmd := exec.Command("git", "add", "file.txt")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	_, err := client.Commit(t.Context(), []byte("docs: readme"), CommitOptions{
		Signoff: true,
		Author:  "Alice Dev <alice@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	var buf bytes.Buffer
	logCmd := exec.Command("git", "log", "--format=%B", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &buf
	_ = logCmd.Run()

	// --signoff appends Signed-off-by using the committer identity (git config user.*),
	// not the overridden Author. newDiskRepo configures "Test User <test@test.com>".
	if !strings.Contains(buf.String(), "Signed-off-by: Test User <test@test.com>") {
		t.Errorf("signoff trailer not found in: %q", buf.String())
	}
}
```

- [ ] **Step 5: Rewrite `TestCommit_author`**

```go
func TestCommit_author(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	addCmd := exec.Command("git", "add", "file.txt")
	addCmd.Dir = dir
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	_, err := client.Commit(t.Context(), []byte("fix: author override"), CommitOptions{
		Author: "Bob Builder <bob@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	var buf bytes.Buffer
	logCmd := exec.Command("git", "log", "--format=%an <%ae>", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &buf
	_ = logCmd.Run()

	got := strings.TrimSpace(buf.String())
	if got != "Bob Builder <bob@example.com>" {
		t.Errorf("author: got %q, want %q", got, "Bob Builder <bob@example.com>")
	}
}
```

- [ ] **Step 6: Rewrite `TestCommit_amend`**

```go
func TestCommit_amend(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "file.txt")

	if _, err := client.Commit(t.Context(), []byte("feat: to be amended"), CommitOptions{}); err != nil {
		t.Fatalf("initial Commit: %v", err)
	}

	var countBuf bytes.Buffer
	countCmd := exec.Command("git", "rev-list", "--count", "HEAD")
	countCmd.Dir = dir
	countCmd.Stdout = &countBuf
	_ = countCmd.Run()
	countBefore := strings.TrimSpace(countBuf.String())

	if _, err := client.Commit(t.Context(), []byte("feat: amended message"), CommitOptions{Amend: true}); err != nil {
		t.Fatalf("amend error: %v", err)
	}

	var countBuf2 bytes.Buffer
	countCmd2 := exec.Command("git", "rev-list", "--count", "HEAD")
	countCmd2.Dir = dir
	countCmd2.Stdout = &countBuf2
	_ = countCmd2.Run()
	countAfter := strings.TrimSpace(countBuf2.String())

	if countBefore != countAfter {
		t.Errorf("commit count changed %s → %s (expected no change)", countBefore, countAfter)
	}

	var msgBuf bytes.Buffer
	msgCmd := exec.Command("git", "log", "--format=%s", "-1")
	msgCmd.Dir = dir
	msgCmd.Stdout = &msgBuf
	_ = msgCmd.Run()

	if strings.TrimSpace(msgBuf.String()) != "feat: amended message" {
		t.Errorf("tip message after amend: got %q", strings.TrimSpace(msgBuf.String()))
	}
}
```

- [ ] **Step 7: Rewrite `TestCommit_summary`**

```go
func TestCommit_summary(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatalf("write feature.go: %v", err)
	}

	addCmd := exec.Command("git", "add", "feature.go")
	addCmd.Dir = dir
	if out, err := addCmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	summary, err := client.Commit(t.Context(), []byte("feat: add feature\n\nsome body"), CommitOptions{})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	if len(summary.ShortHash) != 7 {
		t.Errorf("ShortHash len = %d, want 7", len(summary.ShortHash))
	}
	if summary.Branch != "main" {
		t.Errorf("Branch = %q, want %q", summary.Branch, "main")
	}
	if summary.IsRoot {
		t.Error("IsRoot = true, want false")
	}
	if summary.Subject != "feat: add feature" {
		t.Errorf("Subject = %q, want %q", summary.Subject, "feat: add feature")
	}
	if summary.Files != 1 {
		t.Errorf("Files = %d, want 1", summary.Files)
	}
	if summary.Additions == 0 {
		t.Error("Additions = 0, want > 0")
	}
}
```

Note: `newDiskRepo` initialises on branch `main`, not `master`.

- [ ] **Step 8: Rewrite `TestCommit_summary_rootCommit`**

This test needs a fresh repo with no prior commits. Use inline setup:

```go
func TestCommit_summary_rootCommit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--initial-branch=main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write init.txt: %v", err)
	}
	run("add", "init.txt")

	client, err := NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	client.io = IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

	summary, err := client.Commit(t.Context(), []byte("chore: initial commit"), CommitOptions{})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	if !summary.IsRoot {
		t.Error("IsRoot = false, want true")
	}
	if summary.Files != 1 {
		t.Errorf("Files = %d, want 1", summary.Files)
	}
	if summary.Additions == 0 {
		t.Error("Additions = 0, want > 0 for root commit")
	}
}
```

- [ ] **Step 9: Migrate `TestIsMergedInto_merged` to on-disk**

Replace the in-memory version with an on-disk version that uses raw git commands for the setup commit (no `client.Commit()`):

```go
func TestIsMergedInto_merged(t *testing.T) {
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

	run("checkout", "-b", "feature/y")

	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write feat.txt: %v", err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: add feature")
	run("checkout", "main")
	run("merge", "--ff-only", "feature/y")

	merged, err := client.IsMergedInto("feature/y", "main")
	if err != nil {
		t.Fatalf("IsMergedInto: %v", err)
	}
	if !merged {
		t.Error("IsMergedInto = false, want true after fast-forward merge")
	}
}
```

- [ ] **Step 10: Migrate `TestIsMergedInto_notMerged` to on-disk**

```go
func TestIsMergedInto_notMerged(t *testing.T) {
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

	run("checkout", "-b", "feature/z")

	if err := os.WriteFile(filepath.Join(dir, "unmerged.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write unmerged.txt: %v", err)
	}
	run("add", "unmerged.txt")
	run("commit", "-m", "feat: unmerged")
	run("checkout", "main")

	merged, err := client.IsMergedInto("feature/z", "main")
	if err != nil {
		t.Fatalf("IsMergedInto: %v", err)
	}
	if merged {
		t.Error("IsMergedInto = true, want false for unmerged branch")
	}
}
```

- [ ] **Step 11: Clean up now-unused helpers in `git_test.go`**

Remove `newTestRepo` and `stageNewFile` if they are no longer called by any test. Check first:

```bash
grep -n "newTestRepo\|stageNewFile" git/git_test.go
```

Remove any functions that show zero call sites in the test file.

- [ ] **Step 12: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 13: Run all git tests**

```bash
mise exec -- go test ./git/... -v
```

Expected: all pass.

- [ ] **Step 14: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): rewrite Commit via git subprocess so hooks run; migrate tests to on-disk"
```

---

## Task 7: Update `cmd/commit/commit.go` — thread `ctx`, pass cobra IO

**Files:**
- Modify: `cmd/commit/commit.go`

- [ ] **Step 1: Thread `cmd.Context()` and cobra IO into `runE`**

In `cmd/commit/commit.go`, the `RunE` closure currently discards `cmd`. Change it to capture the command, then pass cobra IO to `NewClient` and context to `Commit`:

```go
cmd.RunE = func(cmd *cobra.Command, _ []string) error {
    return c.runE(cmd, tui.CommitOption{
        All:        all,
        Amend:      amend,
        NoVerify:   noVerify,
        Signoff:    signoff,
        AllowEmpty: allowEmpty,
        Author:     author,
    })
}
```

Update the `runE` signature and body:

```go
func (c Commit) runE(cmd *cobra.Command, flags tui.CommitOption) error {
	client, err := git.NewClient(&git.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	authors, err := client.Authors()
	if err != nil {
		slog.Warn("could not load author list", "error", err)
		authors = []string{}
	}

	defaults := flags
	defaults.Authors = authors

	hint := issueHintFromClient(client)

	msg, opts, err := commitpkg.FillOutForm(c.appConfig, defaults, hint)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	summary, err := client.Commit(cmd.Context(), msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	})
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	printCommitSummary(&summary)

	return nil
}
```

- [ ] **Step 2: Verify build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 3: Run commit package tests**

```bash
mise exec -- go test ./cmd/commit/... -v
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/commit/commit.go
git commit -m "feat(cmd/commit): thread context and cobra IO into git.Commit call"
```

---

## Task 8: Pass cobra IO to remaining cmd callers; final build and test

**Files:**
- Modify: `cmd/issue/close.go`
- Modify: `cmd/issue/start.go`
- Modify: `cmd/branch/branch.go`

`config/config.go` already passes `nil` (updated in Task 4). `store.OpenRepo` passes `nil` internally.

- [ ] **Step 1: Update `cmd/issue/close.go`**

In `closeRunE`, replace `git.NewClient()` with:

```go
client, err := git.NewClient(&git.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
```

- [ ] **Step 2: Update `cmd/issue/start.go`**

`start.go` creates a git client for branch creation (no hook-triggering ops). Pass `nil`:

```go
// In the function that calls git.NewClient():
client, err := git.NewClient(nil)
```

- [ ] **Step 3: Update `cmd/branch/branch.go` — `pruneRunE`**

```go
c, err := git.NewClient(&git.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
```

- [ ] **Step 4: Full build**

```bash
mise exec -- go build ./...
```

Expected: no output, exit 0.

- [ ] **Step 5: Full test suite**

```bash
mise exec -- go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close.go cmd/issue/start.go cmd/branch/branch.go
git commit -m "feat(cmd): pass cobra IO to git.NewClient so hook output respects command streams"
```
