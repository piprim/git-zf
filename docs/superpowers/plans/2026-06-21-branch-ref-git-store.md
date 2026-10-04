# Branch Metadata in Git Object Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store branch parent-child relationships in `refs/zf/branches/<issueSlug>` git refs so that any developer cloning the repo can find the correct merge target when closing a sub-task, without relying on the local SQLite store.

**Architecture:** Mirrors the existing `refs/zf/reviews/*` pattern exactly. `git zf issue start` writes a JSON blob (`BranchRef`) to `refs/zf/branches/<slug>` and pushes it to origin. `git zf issue close` fetches and reads this ref as an authoritative fallback when the local SQLite store has no parent-child record (cross-machine scenario).

**Tech Stack:** Go, `os/exec` git plumbing (`hash-object`, `update-ref`, `show-ref`, `cat-file`, `fetch`, `push`), encoding/json — same as `git/review_ref.go`.

## Global Constraints

- Go toolchain managed by mise: all `go` commands must be run as `mise exec -- go …`
- Branch separator is `@` (e.g. `X.1@feat@part-one`), never `/`
- No auto-commit; user handles all git operations
- Tests use `t.Run` for every distinct assertion
- No new abstractions beyond what the task requires (YAGNI)
- `git/branch_ref.go` must follow the exact plumbing style of `git/review_ref.go`

---

### Task 1: `git/branch_ref.go` — BranchRef struct + CRUD

**Files:**
- Create: `git/branch_ref.go`
- Create: `git/branch_ref_test.go`

**Interfaces:**
- Produces:
  - `type BranchRef struct { IssueSlug, BranchName, ParentSlug string; CreatedAt string }`
  - `func (c *Client) WriteBranchRef(ctx context.Context, issueSlug string, ref BranchRef) (string, error)`
  - `func (c *Client) ReadBranchRef(ctx context.Context, issueSlug string) (*BranchRef, error)` — returns `(nil, nil)` when ref absent
  - `func (c *Client) PushBranchRef(ctx context.Context, issueSlug string) error` — no-op when no remote
  - `func (c *Client) FetchBranchRefs(ctx context.Context) error` — no-op when no remote

- [ ] **Step 1: Write the failing test**

Create `/workspace/git/branch_ref_test.go`:

```go
package git

import (
	"testing"
)

func TestBranchRef_ReadWrite(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	ref := BranchRef{
		IssueSlug:  "X.1",
		BranchName: "X.1@feat@part-one",
		ParentSlug: "X",
		CreatedAt:  "2026-06-21T10:00:00Z",
	}

	t.Run("ReadBranchRef returns nil for non-existent ref", func(t *testing.T) {
		got, err := client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("WriteBranchRef creates ref", func(t *testing.T) {
		sha, err := client.WriteBranchRef(t.Context(), "X.1", ref)
		if err != nil {
			t.Fatalf("WriteBranchRef: %v", err)
		}
		if sha == "" {
			t.Fatal("expected non-empty SHA")
		}
	})

	t.Run("ReadBranchRef returns written ref", func(t *testing.T) {
		got, err := client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if got == nil {
			t.Fatal("expected ref, got nil")
		}
		if got.IssueSlug != "X.1" {
			t.Errorf("IssueSlug: got %q, want %q", got.IssueSlug, "X.1")
		}
		if got.BranchName != "X.1@feat@part-one" {
			t.Errorf("BranchName: got %q, want %q", got.BranchName, "X.1@feat@part-one")
		}
		if got.ParentSlug != "X" {
			t.Errorf("ParentSlug: got %q, want %q", got.ParentSlug, "X")
		}
		if got.CreatedAt != "2026-06-21T10:00:00Z" {
			t.Errorf("CreatedAt: got %q, want %q", got.CreatedAt, "2026-06-21T10:00:00Z")
		}
	})

	t.Run("WriteBranchRef overwrites existing ref", func(t *testing.T) {
		updated := BranchRef{
			IssueSlug:  "X.1",
			BranchName: "X.1@feat@part-one",
			ParentSlug: "X",
			CreatedAt:  "2026-06-21T11:00:00Z",
		}
		_, err := client.WriteBranchRef(t.Context(), "X.1", updated)
		if err != nil {
			t.Fatalf("WriteBranchRef (overwrite): %v", err)
		}
		got, _ := client.ReadBranchRef(t.Context(), "X.1")
		if got.CreatedAt != "2026-06-21T11:00:00Z" {
			t.Errorf("overwrite failed: CreatedAt = %q", got.CreatedAt)
		}
	})

	t.Run("ref without parent slug is valid", func(t *testing.T) {
		rootRef := BranchRef{
			IssueSlug:  "X",
			BranchName: "X@feat@big-feature",
			CreatedAt:  "2026-06-21T10:00:00Z",
		}
		if _, err := client.WriteBranchRef(t.Context(), "X", rootRef); err != nil {
			t.Fatalf("WriteBranchRef root: %v", err)
		}
		got, err := client.ReadBranchRef(t.Context(), "X")
		if err != nil {
			t.Fatalf("ReadBranchRef root: %v", err)
		}
		if got.ParentSlug != "" {
			t.Errorf("ParentSlug: got %q, want empty", got.ParentSlug)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /workspace && mise exec -- go test ./git/... -run TestBranchRef -v
```

Expected: FAIL — `BranchRef undefined`, `WriteBranchRef undefined`, `ReadBranchRef undefined`

- [ ] **Step 3: Implement `git/branch_ref.go`**

Create `/workspace/git/branch_ref.go`:

```go
package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const branchRefPrefix = "refs/zf/branches/"

// BranchRef is the JSON payload stored as a git blob at refs/zf/branches/<issueSlug>.
// It records the branch name and optional parent slug so any clone can resolve
// the merge target without querying the local SQLite store.
type BranchRef struct {
	IssueSlug  string `json:"issue_slug"`
	BranchName string `json:"branch_name"`
	ParentSlug string `json:"parent_slug,omitempty"`
	CreatedAt  string `json:"created_at"` // RFC3339
}

// WriteBranchRef writes a BranchRef as a git blob and updates the local ref
// refs/zf/branches/<issueSlug>. No CAS — branch metadata is write-once; an
// overwrite (e.g. re-running issue start) simply replaces the blob.
// Returns the new blob SHA.
func (c *Client) WriteBranchRef(ctx context.Context, issueSlug string, ref BranchRef) (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	data, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("marshal branch ref: %w", err)
	}

	hashCmd := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "-w", "--stdin")
	hashCmd.Stdin = bytes.NewReader(data)
	out, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	newSHA := strings.TrimSpace(string(out))

	refName := branchRefPrefix + issueSlug
	updateCmd := exec.CommandContext(ctx, "git", "-C", root, "update-ref", refName, newSHA)
	if out, err := updateCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git update-ref: %w: %s", err, out)
	}

	return newSHA, nil
}

// ReadBranchRef reads the BranchRef for issueSlug from the local ref store.
// Returns (nil, nil) when the ref does not exist.
func (c *Client) ReadBranchRef(ctx context.Context, issueSlug string) (*BranchRef, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	refName := branchRefPrefix + issueSlug

	showCmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--hash", refName)
	shaOut, err := showCmd.Output()
	if err != nil {
		return nil, nil // ref does not exist
	}
	sha := strings.TrimSpace(string(shaOut))

	catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", sha)
	blobOut, err := catCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w", sha, err)
	}

	var ref BranchRef
	if err := json.Unmarshal(blobOut, &ref); err != nil {
		return nil, fmt.Errorf("unmarshal branch ref: %w", err)
	}

	return &ref, nil
}

// FetchBranchRefs fetches refs/zf/branches/* from the remote into the local
// ref namespace. No-op when no remote is configured.
func (c *Client) FetchBranchRefs(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	refspec := branchRefPrefix + "*:" + branchRefPrefix + "*"
	if err := c.runInteractive(ctx, root, "fetch", remote, refspec); err != nil {
		return fmt.Errorf("fetch branch refs: %w", err)
	}

	return nil
}

// PushBranchRef pushes refs/zf/branches/<issueSlug> to the remote.
// No-op when no remote is configured. Plain push (no lease) — branch metadata
// is immutable after creation; concurrent writes are safe.
func (c *Client) PushBranchRef(ctx context.Context, issueSlug string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	refName := branchRefPrefix + issueSlug
	if err := c.runInteractive(ctx, root, "push", remote, refName); err != nil {
		return fmt.Errorf("push branch ref %s: %w", issueSlug, err)
	}

	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /workspace && mise exec -- go test ./git/... -run TestBranchRef -v
```

Expected: all subtests PASS.

- [ ] **Step 5: Commit**

```
git add git/branch_ref.go git/branch_ref_test.go
git commit -m "feat(git): add BranchRef — branch metadata in refs/zf/branches/*"
```

---

### Task 2: `cmd/issue/start.go` — write + push BranchRef at branch creation

**Files:**
- Modify: `cmd/issue/start.go` (two call sites + one helper function)

**Interfaces:**
- Consumes: `git.BranchRef`, `(*git.Client).WriteBranchRef`, `(*git.Client).PushBranchRef` (from Task 1)
- Produces: nothing new (internal change)

- [ ] **Step 1: Write the failing test**

In `/workspace/cmd/issue/start_e2e_test.go`, add a new test at the end of the file:

```go
func TestRunIssueStart_WritesBranchRef(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)

	prompter := &scriptedStartPrompter{
		Issue:         &issue.Issue{ID: "X", Subject: "big-feature"},
		BranchType:    "feat",
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	deps := StartDeps{
		Client: rig.client,
		Cfg:    rig.cfg,
		Flags:  issue.IssueStartFlags{},
	}

	if err := RunIssueStart(t.Context(), deps, prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("BranchRef written for root branch", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(t.Context(), "X")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if ref == nil {
			t.Fatal("expected BranchRef to be written, got nil")
		}
		if ref.BranchName != "X@feat@big-feature" {
			t.Errorf("BranchName: got %q, want %q", ref.BranchName, "X@feat@big-feature")
		}
		if ref.ParentSlug != "" {
			t.Errorf("ParentSlug: got %q, want empty", ref.ParentSlug)
		}
	})
}

func TestRunIssueStart_WritesBranchRef_WithParent(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)

	// Seed parent branch in store so --parent X can resolve.
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X", Title: "big-feature", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X@feat@big-feature", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	// Create parent branch in git.
	runGitInRig(t, rig, "checkout", "-b", "X@feat@big-feature")
	runGitInRig(t, rig, "checkout", "main")

	prompter := &scriptedStartPrompter{
		Issue:         &issue.Issue{ID: "X.1", Subject: "part-one"},
		BranchType:    "feat",
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	deps := StartDeps{
		Client: rig.client,
		Cfg:    rig.cfg,
		Flags:  issue.IssueStartFlags{ParentIssueSlug: "X"},
	}

	if err := RunIssueStart(t.Context(), deps, prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("BranchRef written with parent slug", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(t.Context(), "X.1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if ref == nil {
			t.Fatal("expected BranchRef, got nil")
		}
		if ref.BranchName != "X.1@feat@part-one" {
			t.Errorf("BranchName: got %q, want %q", ref.BranchName, "X.1@feat@part-one")
		}
		if ref.ParentSlug != "X" {
			t.Errorf("ParentSlug: got %q, want %q", ref.ParentSlug, "X")
		}
	})
}
```

You also need a helper `runGitInRig` — check if it already exists in the test file; if not, add:

```go
func runGitInRig(t *testing.T, rig *startRig, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = rig.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /workspace && mise exec -- go test ./cmd/issue/... -run "TestRunIssueStart_WritesBranchRef" -v
```

Expected: FAIL — `ReadBranchRef` is called and returns nil (not written yet).

- [ ] **Step 3: Implement — add helper + two call sites in `start.go`**

At the bottom of `/workspace/cmd/issue/start.go`, add the helper (before the closing brace of the file, after `prepareBranch`):

```go
// writePushBranchRef writes a BranchRef to refs/zf/branches/<issueSlug> and
// pushes it to the remote (best-effort). Called after every successful branch
// or worktree creation so the parent-child relationship is available cross-machine.
func writePushBranchRef(ctx context.Context, deps StartDeps, issueSlug, branchName string) error {
	ref := git.BranchRef{
		IssueSlug:  issueSlug,
		BranchName: branchName,
		ParentSlug: deps.Flags.ParentIssueSlug,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := deps.Client.WriteBranchRef(ctx, issueSlug, ref); err != nil {
		return err
	}
	return deps.Client.PushBranchRef(ctx, issueSlug)
}
```

`time` is not currently imported in `start.go`. Add it to the import block:

```go
import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"           // ← add this

	"github.com/charmbracelet/lipgloss"
	...
)
```

In `createBranchFlow`, after the `InsertIssueRelation` block (after line 306), add:

```go
	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
	}
```

In `createWorktreeFlow`, after the `InsertIssueRelation` block (after line 389), add the same call:

```go
	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
	}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /workspace && mise exec -- go test ./cmd/issue/... -run "TestRunIssueStart_WritesBranchRef" -v
```

Expected: all subtests PASS.

- [ ] **Step 5: Run full test suite to check for regressions**

```bash
cd /workspace && mise exec -- go test ./...
```

Expected: all packages PASS.

- [ ] **Step 6: Commit**

```
git add cmd/issue/start.go cmd/issue/start_e2e_test.go
git commit -m "feat(issue/start): write refs/zf/branches/<slug> at branch creation"
```

---

### Task 3: `cmd/issue/close.go` — read BranchRef as cross-machine fallback

**Files:**
- Modify: `cmd/issue/close.go` (parent-lookup block, lines 157–171)
- Modify: `cmd/issue/close_e2e_test.go` (new cross-machine subtest)

**Interfaces:**
- Consumes: `(*git.Client).FetchBranchRefs`, `(*git.Client).ReadBranchRef` (from Task 1)

- [ ] **Step 1: Write the failing test**

In `/workspace/cmd/issue/close_e2e_test.go`, add a new test function:

```go
// TestClose_CrossMachine_UsesParentBranchRef verifies that close correctly
// merges a sub-task into its parent integration branch even when the local
// SQLite store has no parent-child relation record (cross-machine scenario:
// a developer who fetched and checked out the branch without running issue start).
func TestClose_CrossMachine_UsesParentBranchRef(t *testing.T) {
	t.Parallel()

	// Build a repo that looks like Bob's machine:
	//   - main branch with one commit
	//   - X@feat@big-feature (parent integration branch), one commit ahead of main
	//   - X.1@feat@part-one (sub-task), one commit ahead of X@feat@big-feature
	//   - refs/zf/branches/X    → {branch_name: "X@feat@big-feature"}
	//   - refs/zf/branches/X.1  → {branch_name: "X.1@feat@part-one", parent_slug: "X"}
	//   - SQLite store: only X.1 branch row (no issue_relations record)

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Bob")
	runGit("config", "user.email", "bob@example.com")
	runGit("config", "commit.gpgsign", "false")

	// main commit
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	// X@feat@big-feature
	runGit("checkout", "-b", "X@feat@big-feature")
	if err := os.WriteFile(filepath.Join(dir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runGit("add", "parent.txt")
	runGit("commit", "-m", "feat(X): parent commit")

	// X.1@feat@part-one (branched from X@feat@big-feature)
	runGit("checkout", "-b", "X.1@feat@part-one")
	if err := os.WriteFile(filepath.Join(dir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	runGit("add", "subtask.txt")
	runGit("commit", "-m", "feat(X.1): subtask commit")

	runGit("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	// Write branch refs (as Alice would have pushed them; Bob fetched them).
	parentRef := git.BranchRef{
		IssueSlug:  "X",
		BranchName: "X@feat@big-feature",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X", parentRef); err != nil {
		t.Fatalf("WriteBranchRef X: %v", err)
	}

	childRef := git.BranchRef{
		IssueSlug:  "X.1",
		BranchName: "X.1@feat@part-one",
		ParentSlug: "X",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X.1", childRef); err != nil {
		t.Fatalf("WriteBranchRef X.1: %v", err)
	}

	// Store: only X.1 branch row; no issue_relations.
	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.1", Title: "part-one", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X.1@feat@part-one", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	// Seed review ref so reviewPreflight passes (approved, no reviewer commits).
	reviewRef := git.ReviewRef{
		Status:     "approved",
		Round:      1,
		FeatureSHA: "ignored",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteReviewRef(t.Context(), "X.1", reviewRef, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "X.1",
		Title:      "part-one",
		BranchName: "X.1@feat@part-one",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:       pickedRow,
		Strategy:     StrategySquash,
		Confirm:      true,
		Message:      []byte("feat(X.1): close\n"),
		DeleteBranch: true,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("merged into parent branch not main", func(t *testing.T) {
		// X@feat@big-feature should carry the squash commit; main should not.
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "X@feat@big-feature")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log X@feat@big-feature: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "feat(X.1): close" {
			t.Errorf("X@feat@big-feature HEAD subject = %q, want %q", got, "feat(X.1): close")
		}
	})

	t.Run("main is unchanged", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "main")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log main: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "chore: init" {
			t.Errorf("main HEAD = %q, expected it to be unchanged (%q)", got, "chore: init")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /workspace && mise exec -- go test ./cmd/issue/... -run "TestClose_CrossMachine" -v
```

Expected: FAIL — `runClose` merges into `main` instead of `X@feat@big-feature`.

- [ ] **Step 3: Implement — update parent-lookup block in `close.go`**

In `/workspace/cmd/issue/close.go`, replace lines 157–171 (the "Sub-task: redirect merge target" block):

**Before:**
```go
	// Sub-task: redirect merge target to parent integration branch.
	if parentSlug, err := deps.store.GetParentIssue(ctx, picked.IssueSlug); err != nil {
		return fmt.Errorf("check parent issue: %w", err)
	} else if parentSlug != "" {
		parentBranches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
		if err != nil {
			return fmt.Errorf("list branches for parent %q: %w", parentSlug, err)
		}
		for _, b := range parentBranches {
			if b.IssueSlug == parentSlug {
				base = b.BranchName
				break
			}
		}
	}
```

**After:**
```go
	// Sub-task: redirect merge target to parent integration branch.
	// The store is checked first; on a cross-machine clone where the store has
	// no relation record, the refs/zf/branches/<slug> git ref is the fallback.
	parentSlug, err := deps.store.GetParentIssue(ctx, picked.IssueSlug)
	if err != nil {
		return fmt.Errorf("check parent issue: %w", err)
	}
	if parentSlug == "" {
		_ = deps.client.FetchBranchRefs(ctx)
		if br, _ := deps.client.ReadBranchRef(ctx, picked.IssueSlug); br != nil {
			parentSlug = br.ParentSlug
		}
	}
	if parentSlug != "" {
		// Try store first for the parent branch name.
		parentBranches, listErr := deps.store.ListBranches(ctx, store.BranchStatusAll)
		if listErr != nil {
			return fmt.Errorf("list branches for parent %q: %w", parentSlug, listErr)
		}
		found := false
		for _, b := range parentBranches {
			if b.IssueSlug == parentSlug {
				base = b.BranchName
				found = true
				break
			}
		}
		// Store miss — read the parent's branch ref for the branch name.
		if !found {
			if parentBR, _ := deps.client.ReadBranchRef(ctx, parentSlug); parentBR != nil {
				base = parentBR.BranchName
			}
		}
	}
```

No new imports needed — `store` is already imported.

- [ ] **Step 4: Run the new test to verify it passes**

```bash
cd /workspace && mise exec -- go test ./cmd/issue/... -run "TestClose_CrossMachine" -v
```

Expected: PASS.

- [ ] **Step 5: Run full test suite**

```bash
cd /workspace && mise exec -- go test ./...
```

Expected: all packages PASS.

- [ ] **Step 6: Commit**

```
git add cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "fix(issue/close): read refs/zf/branches/ as fallback for cross-machine parent lookup"
```

---

## Verification

```bash
# All unit + E2E tests
mise exec -- go test ./...

# Build check
mise exec -- go build ./...
```

End-to-end manual verification: run `docs/manual-test-parallel-review.sh` — Phase 8 (Bob closes X.2) should merge into `X@feat@big-feature`, not `main`.
