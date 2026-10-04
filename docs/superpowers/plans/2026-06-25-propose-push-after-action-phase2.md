# Propose-to-push (Phase 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich `git zf commit`'s post-commit push proposal with a merge-vs-parent preview (fast-forward / merge-commit / conflicts / already-merged) by reusing the close flow's parent-branch resolution.

**Architecture:** Extract close.go's `resolveDefaultBase` body into a shared, behaviour-preserving `issueflow.ResolveParentBranch` (close keeps a one-line wrapper); teach `cmd/pushflow.Propose` to render a merge-vs-parent line (gated by `Opts.IncludeMergePreview` + `Opts.Parent`, computed from `IsAncestor` + `MergeDryRun`); wire `commit` to resolve the current branch's parent integration branch and pass it through.

**Tech Stack:** Go (managed by mise), cobra, go-git v6. Spec: `docs/superpowers/specs/2026-06-25-propose-push-after-action-design.md`. Phase 1 is already merged (commits `77c2f20`, `7b8a8d0`).

## Global Constraints

- Run Go via mise: `mise exec -- go test ./...`, `mise exec -- go build ./...`, `mise exec -- go vet ./...`.
- **Tests use `t.Run` for every distinct assertion or scenario** (project + user convention).
- golangci-lint cannot run locally here (v2 config vs v1 binary) — verify with `go vet` / `go build` / `go test`; lint is CI-only.
- **The user performs all git commits.** Do NOT run `git add`/`git commit`. Each task ends with verification + a "ready for the user to commit" hand-off (files + suggested message).
- Never `--force` push. Merge preview is read-only (`IsAncestor`, `MergeDryRun` via `git merge-tree`); it never mutates the working tree.
- The `resolveDefaultBase` → `ResolveParentBranch` extraction is the **one HIGH-risk edit** (GitNexus: risk HIGH, single direct caller `runClose` on the critical close path). Mitigation: behaviour-preserving delegation (call site byte-for-byte unchanged), the existing `cmd/issue/close_e2e_test.go` suite as the regression net, and `detect_changes` vs `main` before the user commits.

---

### Task 1: Extract `issueflow.ResolveParentBranch` (behaviour-preserving refactor)

**Files:**
- Create: `cmd/issueflow/parent.go`
- Create: `cmd/issueflow/parent_test.go`
- Modify: `cmd/issue/close.go:240-281` (replace `resolveDefaultBase` body with a one-line delegation)

**Interfaces:**
- Consumes: `store.BranchStatus`, `store.BranchRow`, `git.BranchRef` (existing types).
- Produces:
  - `type issueflow.ParentStore interface { GetParentIssue(ctx, childSlug string) (string, error); ListBranches(ctx, status store.BranchStatus) ([]store.BranchRow, error) }`
  - `type issueflow.ParentClient interface { DefaultBaseBranch() (string, error); FetchBranchRefs(ctx) error; ReadBranchRef(ctx, issueSlug string) (*git.BranchRef, error) }`
  - `func issueflow.ResolveParentBranch(ctx context.Context, s ParentStore, c ParentClient, issueSlug, cfgBase string) (string, error)`

This moves logic only; close's behaviour must not change. `cmd/issue/close.go` already imports `github.com/piprim/git-zf/cmd/issueflow`.

- [ ] **Step 1: Write the failing unit test** — create `cmd/issueflow/parent_test.go`

```go
package issueflow

import (
	"context"
	"errors"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// fakeParentStore / fakeParentClient implement the ParentStore / ParentClient
// role interfaces with canned data — no real repo or DB.
type fakeParentStore struct {
	parentOf map[string]string      // childSlug → parentSlug ("" = none)
	branches []store.BranchRow      // returned by ListBranches
	listErr  error
}

func (f *fakeParentStore) GetParentIssue(_ context.Context, childSlug string) (string, error) {
	return f.parentOf[childSlug], nil
}
func (f *fakeParentStore) ListBranches(_ context.Context, _ store.BranchStatus) ([]store.BranchRow, error) {
	return f.branches, f.listErr
}

type fakeParentClient struct {
	defaultBase string
	refs        map[string]*git.BranchRef // issueSlug → ref (nil = absent)
}

func (f *fakeParentClient) DefaultBaseBranch() (string, error) { return f.defaultBase, nil }
func (f *fakeParentClient) FetchBranchRefs(_ context.Context) error { return nil }
func (f *fakeParentClient) ReadBranchRef(_ context.Context, issueSlug string) (*git.BranchRef, error) {
	return f.refs[issueSlug], nil
}

func TestResolveParentBranch(t *testing.T) {
	t.Parallel()

	t.Run("no parent → cfg base", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "main" {
			t.Fatalf("got %q, want %q", got, "main")
		}
	})

	t.Run("empty cfg base → DefaultBaseBranch", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{defaultBase: "master", refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X", "")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "master" {
			t.Fatalf("got %q, want %q", got, "master")
		}
	})

	t.Run("parent from store → parent branch name", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{
			parentOf: map[string]string{"X.2": "X"},
			branches: []store.BranchRow{{IssueSlug: "X", BranchName: "X@feat@big"}},
		}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "X@feat@big" {
			t.Fatalf("got %q, want %q", got, "X@feat@big")
		}
	})

	t.Run("cross-clone: parent slug from branch ref, name from parent ref", func(t *testing.T) {
		t.Parallel()
		// Store has no parent relation and no parent branch row (fresh clone);
		// the child's ref carries ParentSlug, and the parent's ref carries the name.
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{
			"X.2": {ParentSlug: "X"},
			"X":   {BranchName: "X@feat@big"},
		}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "X@feat@big" {
			t.Fatalf("got %q, want %q", got, "X@feat@big")
		}
	})

	t.Run("ListBranches error is wrapped", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{
			parentOf: map[string]string{"X.2": "X"},
			listErr:  errors.New("db down"),
		}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		if _, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main"); err == nil {
			t.Fatal("want error when ListBranches fails")
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issueflow/ -run TestResolveParentBranch -v`
Expected: FAIL — `ResolveParentBranch` / `ParentStore` / `ParentClient` undefined (build error).

- [ ] **Step 3: Create `cmd/issueflow/parent.go`**

```go
package issueflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// ParentStore is the slice of *store.Store that ResolveParentBranch needs.
type ParentStore interface {
	GetParentIssue(ctx context.Context, childSlug string) (string, error)
	ListBranches(ctx context.Context, status store.BranchStatus) ([]store.BranchRow, error)
}

// ParentClient is the slice of *git.Client that ResolveParentBranch needs.
type ParentClient interface {
	DefaultBaseBranch() (string, error)
	FetchBranchRefs(ctx context.Context) error
	ReadBranchRef(ctx context.Context, issueSlug string) (*git.BranchRef, error)
}

// ResolveParentBranch computes the merge target for an issue: the configured
// base (cfgBase, or DefaultBaseBranch when empty), redirected to the parent
// integration branch when issueSlug has a parent.
//
// The store is checked first for the parent relation; on a cross-machine clone
// where the store has no record, the refs/zf/branches/<slug> git ref is the
// fallback (FetchBranchRefs runs best-effort first so a later read sees fresh
// refs). The parent's branch *name* is resolved from the store, then from the
// parent's own branch ref. Extracted verbatim from cmd/issue/close.go's
// resolveDefaultBase so close and commit share one implementation.
func ResolveParentBranch(ctx context.Context, s ParentStore, c ParentClient, issueSlug, cfgBase string) (string, error) {
	base := cfgBase
	if base == "" {
		detected, err := c.DefaultBaseBranch()
		if err != nil {
			return "", fmt.Errorf("detect base branch: %w", err)
		}
		base = detected
	}

	parentSlug, err := s.GetParentIssue(ctx, issueSlug)
	if err != nil {
		return "", fmt.Errorf("check parent issue: %w", err)
	}
	if parentSlug == "" {
		// One fetch retrieves all refs/zf/branches/* atomically.
		_ = c.FetchBranchRefs(ctx)
		if br, _ := c.ReadBranchRef(ctx, issueSlug); br != nil {
			parentSlug = br.ParentSlug
		}
	}
	if parentSlug == "" {
		return base, nil
	}

	// Try store first for the parent branch name.
	parentBranches, listErr := s.ListBranches(ctx, store.BranchStatusAll)
	if listErr != nil {
		return "", fmt.Errorf("list branches for parent %q: %w", parentSlug, listErr)
	}
	for _, b := range parentBranches {
		if b.IssueSlug == parentSlug {
			return b.BranchName, nil
		}
	}
	// Store miss — read the parent's branch ref for the branch name.
	if parentBR, _ := c.ReadBranchRef(ctx, parentSlug); parentBR != nil {
		return parentBR.BranchName, nil
	}

	return base, nil
}
```

- [ ] **Step 4: Add compile-time role checks** — append to `cmd/issueflow/parent.go`

```go
// Compile-time checks that the production types satisfy the roles.
var (
	_ ParentStore  = (*store.Store)(nil)
	_ ParentClient = (*git.Client)(nil)
)
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `mise exec -- go test ./cmd/issueflow/ -run TestResolveParentBranch -v`
Expected: PASS (all subtests).

- [ ] **Step 6: Replace `resolveDefaultBase` body with delegation** in `cmd/issue/close.go`

Replace the whole function (currently `cmd/issue/close.go:240-281`) — keep its exact signature and doc intent, delegate the body:

```go
// resolveDefaultBase computes the smart-default merge target: the configured
// base (or DefaultBaseBranch) redirected to the parent integration branch when
// the picked issue has a parent. This is the value pre-selected in the picker.
//
// The body lives in issueflow.ResolveParentBranch so the commit flow can reuse
// the identical resolution for its merge-vs-parent preview; this wrapper keeps
// the close call site (runClose) unchanged.
func resolveDefaultBase(ctx context.Context, deps closeDeps, picked *store.BranchRow) (string, error) {
	return issueflow.ResolveParentBranch(ctx, deps.store, deps.client, picked.IssueSlug, deps.cfg.Branch.Base)
}
```

- [ ] **Step 7: Verify the close suite is unchanged (regression net)**

Run: `mise exec -- go test ./cmd/issue/ -run '^TestClose_' -count=1 -v`
Expected: PASS — every pre-existing `TestClose_*` still passes (behaviour preserved).

- [ ] **Step 8: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/issueflow/ ./cmd/issue/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/issueflow/parent.go`, `cmd/issueflow/parent_test.go`, `cmd/issue/close.go`. Suggested message: `refactor(issue): extract resolveDefaultBase into issueflow.ResolveParentBranch`.

---

### Task 2: Merge-vs-parent preview in `cmd/pushflow.Propose`

**Files:**
- Modify: `cmd/pushflow/pushflow.go` (extend `Pusher` + `Opts`; add `mergePreviewLine`; call it in `Propose`)
- Modify: `cmd/pushflow/pushflow_test.go` (extend `fakePusher`; add merge-preview tests)

**Interfaces:**
- Consumes: `git.PushOutcome` (existing); `IsAncestor`/`MergeDryRun` semantics from `*git.Client`.
- Produces (additions, existing fields unchanged):
  - `Pusher` gains `IsAncestor(ctx, child, ancestor string) (bool, error)` and `MergeDryRun(ctx, branchName, baseBranch string) ([]string, error)`.
  - `Opts` gains `IncludeMergePreview bool` and `Parent string`.
  - unexported `mergePreviewLine(ctx, c Pusher, current, parent string) string`.

- [ ] **Step 1: Write the failing tests** — append to `cmd/pushflow/pushflow_test.go`

```go
func TestPropose_MergePreview(t *testing.T) {
	t.Parallel()

	// withMerge returns a fake set up for a merge-preview run: a fast-forward
	// push to origin, plus the IsAncestor results the test overrides per case.
	newMergeFake := func() *fakePusher {
		f := newFake()
		f.isAncestor = map[[2]string]bool{}
		return f
	}
	mergeOpts := Opts{Branch: "b", IncludeMergePreview: true, Parent: "p"}

	t.Run("already merged into parent", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"b", "p"}] = true // current is ancestor of parent
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Already merged into p") {
			t.Fatalf("output missing already-merged line; got %q", f.out.String())
		}
	})

	t.Run("fast-forwards into parent", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"p", "b"}] = true // parent is ancestor of current
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Fast-forwards into p") {
			t.Fatalf("output missing fast-forward line; got %q", f.out.String())
		}
	})

	t.Run("diverged, no conflicts → merge commit", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake() // both IsAncestor false, no conflicts
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Merges into p with a merge commit (no conflicts)") {
			t.Fatalf("output missing merge-commit line; got %q", f.out.String())
		}
	})

	t.Run("diverged with conflicts", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.mergeConflicts = []string{"a.go", "b.go"}
		if err := Propose(t.Context(), f, mergeOpts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "Conflicts with p: a.go, b.go") {
			t.Fatalf("output missing conflicts line; got %q", f.out.String())
		}
	})

	t.Run("not included → no merge line, push still proceeds", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		f.isAncestor[[2]string{"p", "b"}] = true
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if strings.Contains(f.out.String(), "into p") {
			t.Fatalf("merge line shown when IncludeMergePreview=false; got %q", f.out.String())
		}
		if len(f.pushed) != 1 {
			t.Fatalf("push did not proceed; pushed=%v", f.pushed)
		}
	})

	t.Run("parent equal to branch → no merge line", func(t *testing.T) {
		t.Parallel()
		f := newMergeFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b", IncludeMergePreview: true, Parent: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if strings.Contains(f.out.String(), "into b") {
			t.Fatalf("merge line shown when parent==branch; got %q", f.out.String())
		}
	})
}
```

- [ ] **Step 2: Extend `fakePusher`** in `cmd/pushflow/pushflow_test.go`

Add fields to the `fakePusher` struct (after `out *bytes.Buffer`):

```go
	// merge-preview inputs
	isAncestor     map[[2]string]bool // [child, ancestor] → result
	mergeConflicts []string
	mergeErr       error
```

Add the two methods (after the existing `IO()` method):

```go
func (f *fakePusher) IsAncestor(_ context.Context, child, ancestor string) (bool, error) {
	return f.isAncestor[[2]string{child, ancestor}], nil
}
func (f *fakePusher) MergeDryRun(_ context.Context, _, _ string) ([]string, error) {
	return f.mergeConflicts, f.mergeErr
}
```

(Reads from a nil `isAncestor` map return false, so the Phase-1 `newFake()` needs no change.)

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/pushflow/ -run TestPropose_MergePreview -v`
Expected: FAIL — `Opts` has no `IncludeMergePreview`/`Parent`; `mergePreviewLine` not called yet. (The `fakePusher` additions make it compile; the assertions fail.)

- [ ] **Step 4: Extend `Pusher` and `Opts`** in `cmd/pushflow/pushflow.go`

Replace the `Pusher` interface:

```go
// Pusher is the slice of *git.Client the proposal step needs.
type Pusher interface {
	Remote() (string, error)
	PushDryRun(ctx context.Context, branch string) (git.PushOutcome, bool, error)
	PushBranch(ctx context.Context, branch string) error
	IsAncestor(ctx context.Context, child, ancestor string) (bool, error)
	MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error)
	IO() *pkg.IO
}
```

Replace the `Opts` struct:

```go
// Opts configures one Propose call.
type Opts struct {
	Branch         string // branch to push
	Skip           bool   // --no-push or config push.propose=false
	AutoConfirm    bool   // --push: push without prompting
	NonInteractive bool   // -y / no TTY: skip unless AutoConfirm

	// Merge-vs-parent preview (commit). When IncludeMergePreview is true and
	// Parent is a non-empty branch/ref distinct from Branch, Propose prints how
	// Branch would merge into Parent (fast-forward / merge commit / conflicts /
	// already merged) alongside the push preview.
	IncludeMergePreview bool
	Parent              string
}
```

- [ ] **Step 5: Add `mergePreviewLine` and call it in `Propose`** in `cmd/pushflow/pushflow.go`

Add the helper (after `Propose`):

```go
// mergePreviewLine describes how current would merge into parent, using the same
// read-only primitives as the close flow (IsAncestor + MergeDryRun via
// git merge-tree). Returns "" when the relationship cannot be determined (e.g.
// MergeDryRun errors), so the caller simply omits the line.
func mergePreviewLine(ctx context.Context, c Pusher, current, parent string) string {
	if merged, err := c.IsAncestor(ctx, current, parent); err == nil && merged {
		return fmt.Sprintf("Already merged into %s", parent)
	}
	if ff, err := c.IsAncestor(ctx, parent, current); err == nil && ff {
		return fmt.Sprintf("Fast-forwards into %s", parent)
	}
	conflicts, err := c.MergeDryRun(ctx, current, parent)
	if err != nil {
		return ""
	}
	if len(conflicts) > 0 {
		return fmt.Sprintf("⚠ Conflicts with %s: %s", parent, strings.Join(conflicts, ", "))
	}
	return fmt.Sprintf("Merges into %s with a merge commit (no conflicts)", parent)
}
```

Add the import `"strings"` to the import block.

In `Propose`, immediately AFTER the push-preview print line
(`fmt.Fprintf(c.IO().Out, "Push %q to %s — %s\n", opts.Branch, remote, outcome.Summary)`)
and BEFORE the `if opts.NonInteractive && !opts.AutoConfirm` check, insert:

```go
	if opts.IncludeMergePreview && opts.Parent != "" && opts.Parent != opts.Branch {
		if line := mergePreviewLine(ctx, c, opts.Branch, opts.Parent); line != "" {
			fmt.Fprintln(c.IO().Out, line)
		}
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/pushflow/ -v`
Expected: PASS — the new `TestPropose_MergePreview` subtests plus all pre-existing `TestPropose`/`TestResolveFlags` subtests.

- [ ] **Step 7: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/pushflow/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/pushflow/pushflow.go`, `cmd/pushflow/pushflow_test.go`. Suggested message: `feat(pushflow): add merge-vs-parent preview to the push proposal`.

---

### Task 3: Wire `commit`'s merge-vs-parent preview

**Files:**
- Modify: `cmd/commit/commit.go` (`proposeCommitPush` signature + new `resolveCommitMergeParent`; update the `runE` call site)
- Test: `cmd/commit/commit_merge_parent_test.go` (new)

**Interfaces:**
- Consumes: `issueflow.ResolveParentBranch` (Task 1), `pushflow.Opts{IncludeMergePreview, Parent}` (Task 2), `branch.Parse`/`(Branch).IssueID()`, `*git.Client.{CurrentBranch,BranchExists,Remote}`, `*store.Store`.
- Produces: `resolveCommitMergeParent(ctx, client *git.Client, s *store.Store, currentBranch, cfgBase string) (parent string, include bool)`.

`branch.Parse(name) (*branch.Branch, error)` and `(branch.Branch).IssueID() string` already exist. `cmd/commit/commit.go` already imports `git` and `store`; add `branch`, `issueflow`, and (for the helper) `context` if not present.

- [ ] **Step 1: Write the failing test** — create `cmd/commit/commit_merge_parent_test.go`

This test builds an origin-backed repo with a parent integration branch and a seeded store relation, then checks `resolveCommitMergeParent` returns the parent (local or `origin/<parent>`) on an issue branch, and `("", false)` on a non-issue branch.

```go
package commit

import (
	"context"
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/commit/ -run TestResolveCommitMergeParent -v`
Expected: FAIL — `resolveCommitMergeParent` undefined (build error).

- [ ] **Step 3: Add `resolveCommitMergeParent` and update `proposeCommitPush`** in `cmd/commit/commit.go`

Add the imports `"context"`, `"github.com/piprim/git-zf/branch"`, and `"github.com/piprim/git-zf/cmd/issueflow"` to the import block (keep the existing ones).

Replace `proposeCommitPush` (currently `cmd/commit/commit.go:132-153`):

```go
// proposeCommitPush offers to push the current branch after a successful commit,
// enriched (on a git-zf issue branch) with a merge-vs-parent preview.
func proposeCommitPush(cmd *cobra.Command, client *git.Client, s *store.Store, cfg *config.AppConfig) error {
	push, noPush := pushflow.ReadFlags(cmd)
	skip, auto, err := pushflow.ResolveFlags(push, noPush, cfg.Push.Propose)
	if err != nil {
		return err
	}

	branchName, err := client.CurrentBranch()
	if err != nil {
		return nil // detached/unknown HEAD → nothing to offer
	}

	parent, includeMerge := resolveCommitMergeParent(cmd.Context(), client, s, branchName, cfg.Branch.Base)

	yes, _ := cmd.Flags().GetBool("yes")

	return pushflow.Propose(cmd.Context(), client, pushflow.Opts{
		Branch:              branchName,
		Skip:                skip,
		AutoConfirm:         auto,
		NonInteractive:      yes,
		IncludeMergePreview: includeMerge,
		Parent:              parent,
	}, pushflow.NewHuhConfirm())
}

// resolveCommitMergeParent returns the parent integration branch for the
// merge-vs-parent preview (as a ref IsAncestor/MergeDryRun can use), and whether
// to show the preview at all. include is false when the current branch is not a
// git-zf issue branch, when no distinct parent/base resolves, or on any error —
// in those cases commit shows the push preview only.
func resolveCommitMergeParent(ctx context.Context, client *git.Client, s *store.Store, currentBranch, cfgBase string) (string, bool) {
	parsed, err := branch.Parse(currentBranch)
	if err != nil {
		return "", false // not a git-zf issue branch
	}

	parentBranch, err := issueflow.ResolveParentBranch(ctx, s, client, parsed.IssueID(), cfgBase)
	if err != nil || parentBranch == "" || parentBranch == currentBranch {
		return "", false
	}

	// Resolve to a ref the preview primitives can read: prefer the local head,
	// fall back to the remote-tracking ref when the parent is remote-only
	// (mirrors the close flow's dryRunBase fallback).
	if exists, _ := client.BranchExists(parentBranch); !exists {
		if remote, _ := client.Remote(); remote != "" {
			return remote + "/" + parentBranch, true
		}
	}
	return parentBranch, true
}
```

Update the single call site in `runE` (currently `cmd/commit/commit.go:129`):

```go
	return proposeCommitPush(cmd, client, s, c.appConfig)
```

(`s` is the `*store.Store` already opened earlier in `runE`; `c.appConfig` is `*config.AppConfig`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./cmd/commit/ -run TestResolveCommitMergeParent -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Run the full commit suite (regression)**

Run: `mise exec -- go test ./cmd/commit/ -count=1 -v`
Expected: PASS — the new test plus the pre-existing `TestCommitPushDecision` and `TestIssueHintFromClient`.

- [ ] **Step 6: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/commit/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/commit/commit.go`, `cmd/commit/commit_merge_parent_test.go`. Suggested message: `feat(commit): show how the branch merges into its parent before pushing`.

---

## Final verification (all tasks)

- [ ] `mise exec -- go build ./...` — clean build.
- [ ] `mise exec -- go vet ./...` — no findings.
- [ ] `mise exec -- go test -count=1 ./...` — full suite green.
- [ ] Run GitNexus `detect_changes({scope: "all"})` (working tree vs HEAD; there is no local `main` ref) and confirm the only changed symbols are: `resolveDefaultBase` (now a wrapper) + new `ResolveParentBranch`/`ParentStore`/`ParentClient`; `pushflow.Propose`/`Opts`/`Pusher` + `mergePreviewLine`; `commit`'s `proposeCommitPush` + `resolveCommitMergeParent`. The close execution flow must show no behavioural change beyond the delegation. Report the blast radius before the user commits.
- [ ] Hand off to the user for commit; do not commit automatically.

## Self-Review

- **Spec coverage (Phase 2):** `ResolveParentBranch` extraction + behaviour-preserving `resolveDefaultBase` wrapper (Task 1, spec "Shared parent resolution (refactor) — Phase 2"); merge-vs-parent preview with the four cases FF / merge-commit / ⚠ conflicts / already-merged built from `IsAncestor` + `MergeDryRun`, gated by `Opts.IncludeMergePreview` + `Opts.Parent`, `Pusher` extended (Task 2, spec "Merge-vs-parent preview (Phase 2)"); commit wiring that resolves the parent and omits the preview for non-issue branches / no parent (Task 3, spec "Per-command placement → commit"). The HIGH-risk note and its mitigation are in Global Constraints + the final `detect_changes` step.
- **Placeholder scan:** none — every code step contains complete code; every command has expected output.
- **Type consistency:** `ResolveParentBranch(ctx, ParentStore, ParentClient, issueSlug, cfgBase) (string, error)` is defined in Task 1 and consumed unchanged in Tasks 1 (close) and 3 (commit); `Opts.IncludeMergePreview`/`Opts.Parent` and the `Pusher.IsAncestor`/`MergeDryRun` additions are defined in Task 2 and consumed in Task 3; `resolveCommitMergeParent(ctx, *git.Client, *store.Store, string, string) (string, bool)` is defined and called within Task 3; `fakePusher`'s `isAncestor`/`mergeConflicts`/`mergeErr` are added and used within Task 2.
