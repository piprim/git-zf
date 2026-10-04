# Reviewer-Initiated Close Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a reviewer (or any team member) run `git zf issue close` on an issue they did not start, without first running `git zf issue track`.

**Architecture:** When the local store has no matching in-progress row, `issue close` falls back to the `refs/zf/branches/*` git refs (already fetched by the close flow) to discover closeable issues, materializes the feature branch from `origin/<feature>` so the merge can resolve it, and auto-tracks the branch into the store before running the existing close pipeline unchanged. This mirrors the cross-machine ref fallback already used by `issueflow.ResolveParentBranch`.

**Tech Stack:** Go (managed by `mise`), `go-git/v6`, git plumbing via `exec.CommandContext`, SQLite store, `cobra`/`huh` (unchanged here).

## Global Constraints

- **Run Go via mise:** tests `mise exec -- go test ./...`; single package `mise exec -- go test ./cmd/issueflow/... -run TestX -v`; build `mise exec -- go build -o ./bin/git-zf .`; vet `mise exec -- go vet ./...`.
- **golangci-lint does not run locally** (v2 config vs v1 binary). Verify with `go vet` / `go build` / `go test`; treat lint as CI-only.
- **Every distinct assertion or table row is wrapped in its own `t.Run("label", func(t *testing.T){…})`.** Applies even to single-assertion tests — the `t.Run` name documents what's checked.
- **The user performs all git commits, not the agent.** Where a task ends with a "Checkpoint" step, STOP and let the user commit; do not run `git add`/`git commit` yourself. The staged-file list in each Checkpoint tells the user what to include.
- **No new CLI flags.** Selection is automatic. The `issue close` README description gets a one-line behavior note (Task 6) — docs are not optional.
- **Package import path root:** `github.com/piprim/git-zf`.

---

### Task 1: `git.ListBranchRefs` — enumerate all branch refs

**Files:**
- Modify: `git/branch_ref.go` (append one method)
- Test: `git/branch_ref_test.go` (append one test function)

**Interfaces:**
- Consumes: existing `Client.WorkingTreeRoot()`, the package const `branchRefPrefix = "refs/zf/branches/"`, and the `BranchRef` struct.
- Produces: `func (c *Client) ListBranchRefs(ctx context.Context) ([]BranchRef, error)` — returns every locally available branch ref; empty slice (not error) when none exist; malformed blobs skipped.

- [ ] **Step 1: Write the failing test**

Append to `git/branch_ref_test.go` (it already `import "testing"`; add `"reflect"`? No — assert on fields directly). The helper `newDiskRepo(t)` returns `(*Client, dir)`.

```go
func TestListBranchRefs(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	t.Run("empty namespace returns empty slice", func(t *testing.T) {
		got, err := client.ListBranchRefs(t.Context())
		if err != nil {
			t.Fatalf("ListBranchRefs: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 refs, got %d: %+v", len(got), got)
		}
	})

	t.Run("returns every written ref", func(t *testing.T) {
		if _, err := client.WriteBranchRef(t.Context(), "X.1", BranchRef{
			IssueSlug: "X.1", BranchName: "X.1@feat@one", CreatedAt: "2026-07-21T10:00:00Z",
		}); err != nil {
			t.Fatalf("WriteBranchRef X.1: %v", err)
		}
		if _, err := client.WriteBranchRef(t.Context(), "X.2", BranchRef{
			IssueSlug: "X.2", BranchName: "X.2@fix@two", Merged: true, TrackerType: "github",
		}); err != nil {
			t.Fatalf("WriteBranchRef X.2: %v", err)
		}

		got, err := client.ListBranchRefs(t.Context())
		if err != nil {
			t.Fatalf("ListBranchRefs: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 refs, got %d: %+v", len(got), got)
		}

		bySlug := map[string]BranchRef{}
		for _, r := range got {
			bySlug[r.IssueSlug] = r
		}
		if bySlug["X.1"].BranchName != "X.1@feat@one" {
			t.Errorf("X.1 BranchName = %q, want X.1@feat@one", bySlug["X.1"].BranchName)
		}
		if !bySlug["X.2"].Merged || bySlug["X.2"].TrackerType != "github" {
			t.Errorf("X.2 = %+v, want Merged=true TrackerType=github", bySlug["X.2"])
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./git/... -run TestListBranchRefs -v`
Expected: FAIL — `client.ListBranchRefs undefined (type *Client has no field or method ListBranchRefs)`.

- [ ] **Step 3: Write minimal implementation**

Append to `git/branch_ref.go` (its import block already has `context`, `encoding/json`, `fmt`, `os/exec`, `strings`):

```go
// ListBranchRefs returns all locally available branch refs (refs/zf/branches/*).
// Call FetchBranchRefs first to refresh the namespace from the remote. Returns
// an empty slice (not an error) when none exist; malformed blobs are skipped.
// Mirrors ListReviewRefs.
func (c *Client) ListBranchRefs(ctx context.Context) ([]BranchRef, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"for-each-ref", "--format=%(objectname) %(refname)", branchRefPrefix)
	out, err := cmd.Output()
	if err != nil {
		// No refs exist yet — return empty slice, not an error.
		return []BranchRef{}, nil
	}

	result := []BranchRef{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha := parts[0]

		catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", sha)
		blobOut, catErr := catCmd.Output()
		if catErr != nil {
			continue // skip malformed ref
		}

		var ref BranchRef
		if jsonErr := json.Unmarshal(blobOut, &ref); jsonErr != nil {
			continue
		}
		result = append(result, ref)
	}

	return result, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./git/... -run TestListBranchRefs -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Checkpoint (user commits)**

Staged files: `git/branch_ref.go`, `git/branch_ref_test.go`.
Suggested message: `feat(git): add ListBranchRefs to enumerate refs/zf/branches/*`

---

### Task 2: `git.CreateLocalBranch` — materialize a branch from a start point

**Files:**
- Modify: `git/git.go` (append one method)
- Test: `git/git_test.go` (append one test function)

**Interfaces:**
- Consumes: existing `Client.WorkingTreeRoot()`, and (in tests) `newDiskRepoWithOrigin(t) (*Client, cloneDir, originDir string)` plus `Client.BranchExists`, `Client.ResolveRef`.
- Produces: `func (c *Client) CreateLocalBranch(ctx context.Context, name, startPoint string) error` — creates `refs/heads/<name>` at `startPoint` (branch name, remote-tracking ref, or SHA) without switching the working tree.

- [ ] **Step 1: Write the failing test**

Append to `git/git_test.go`. `newDiskRepoWithOrigin` returns a clone whose only local branch is `main`; we push a second branch to origin, fetch it, then materialize it locally from the remote-tracking ref.

```go
func TestCreateLocalBranch(t *testing.T) {
	t.Parallel()

	client, cloneDir, originDir := newDiskRepoWithOrigin(t)

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Create a feature branch on origin (via a throwaway seed working tree),
	// then make it visible to the clone as a remote-tracking ref only.
	seed := t.TempDir()
	runIn(filepath.Dir(seed), "clone", originDir, filepath.Base(seed))
	runIn(seed, "config", "user.name", "Seed")
	runIn(seed, "config", "user.email", "seed@test.com")
	runIn(seed, "config", "commit.gpgsign", "false")
	runIn(seed, "checkout", "-b", "ABC-1@feat@thing")
	if err := os.WriteFile(filepath.Join(seed, "f.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seed, "add", "f.go")
	runIn(seed, "commit", "-m", "feat: thing")
	runIn(seed, "push", "origin", "ABC-1@feat@thing")

	runIn(cloneDir, "fetch", "origin")

	t.Run("before: local branch absent", func(t *testing.T) {
		exists, err := client.BranchExists("ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if exists {
			t.Fatalf("expected local branch absent before materialize")
		}
	})

	t.Run("CreateLocalBranch materializes from origin ref", func(t *testing.T) {
		if err := client.CreateLocalBranch(t.Context(), "ABC-1@feat@thing", "origin/ABC-1@feat@thing"); err != nil {
			t.Fatalf("CreateLocalBranch: %v", err)
		}
		local, err := client.ResolveRef("refs/heads/ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("resolve new local branch: %v", err)
		}
		remote, err := client.ResolveRef("refs/remotes/origin/ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("resolve origin ref: %v", err)
		}
		if local != remote {
			t.Errorf("local %s != origin %s", local, remote)
		}
	})
}
```

Note: `git_test.go` must already import `os`, `exec`, `filepath`. If any is missing after adding the test, add it to that file's import block — `mise exec -- go build` will name the missing one.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./git/... -run TestCreateLocalBranch -v`
Expected: FAIL — `client.CreateLocalBranch undefined`.

- [ ] **Step 3: Write minimal implementation**

Append to `git/git.go` (its import block already has `context`, `fmt`, `os/exec`):

```go
// CreateLocalBranch creates refs/heads/<name> pointing at startPoint (a branch
// name, remote-tracking ref like "origin/X.1@feat@slug", or a SHA). It does not
// switch the working tree. Used to materialize a feature branch that exists only
// as a remote-tracking ref on a reviewer's clone so the merge strategies can
// resolve it by bare name.
func (c *Client) CreateLocalBranch(ctx context.Context, name, startPoint string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "branch", name, startPoint)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch %s %s: %w: %s", name, startPoint, err, out)
	}

	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./git/... -run TestCreateLocalBranch -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Checkpoint (user commits)**

Staged files: `git/git.go`, `git/git_test.go`.
Suggested message: `feat(git): add CreateLocalBranch to materialize a branch from a start point`

---

### Task 3: `issueflow.CloseCandidates` — union store rows with branch refs

**Files:**
- Create: `cmd/issueflow/closecandidates.go`
- Test: `cmd/issueflow/closecandidates_test.go`

**Interfaces:**
- Consumes: `git.ListBranchRefs` (Task 1), existing `git.Client.ResolveBranchRef(name) (plumbing.Hash, error)`, `store.Store.ListBranches`, `branch.Parse`.
- Produces:
  - `type CandidateStore interface` and `type CandidateClient interface` (role interfaces; used by this task and Task 4).
  - `func CloseCandidates(ctx context.Context, s CandidateStore, c CandidateClient) ([]store.BranchRow, error)`.
  - Unexported `synthRow(ref git.BranchRef) store.BranchRow`.

- [ ] **Step 1: Write the failing test**

Create `cmd/issueflow/closecandidates_test.go`. Uses role-interface fakes (house style — see `parent_test.go`). `plumbing.ZeroHash` stands in for "resolves"; a sentinel error stands in for "does not resolve".

```go
package issueflow

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// fakeCandStore implements CandidateStore with canned data.
type fakeCandStore struct {
	inProgress []store.BranchRow
	all        []store.BranchRow
	listErr    error
	inserted   []insertedIssue
}

type insertedIssue struct {
	issue  store.Issue
	branch store.Branch
}

func (f *fakeCandStore) ListBranches(_ context.Context, status store.BranchStatus) ([]store.BranchRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if status == store.BranchStatusAll {
		return f.all, nil
	}
	return f.inProgress, nil
}

func (f *fakeCandStore) InsertIssueWithBranch(_ context.Context, issue *store.Issue, b *store.Branch) error {
	f.inserted = append(f.inserted, insertedIssue{issue: *issue, branch: *b})
	// Mirror a real insert: the branch becomes visible with a fresh IssueID.
	f.all = append(f.all, store.BranchRow{
		IssueID: int64(len(f.all) + 1), IssueSlug: issue.IDSlug, Title: issue.Title,
		BranchName: b.Name, Type: b.Type, Status: store.BranchStatusInProgress,
	})
	return nil
}

// fakeCandClient implements CandidateClient with canned data.
type fakeCandClient struct {
	refs         []git.BranchRef
	refsErr      error
	unresolvable map[string]bool          // branch names that fail ResolveBranchRef
	localExists  map[string]bool          // branch names present as local refs
	created      []string                 // names passed to CreateLocalBranch
	bySlug       map[string]*git.BranchRef // for ReadBranchRef
}

func (f *fakeCandClient) ListBranchRefs(_ context.Context) ([]git.BranchRef, error) {
	return f.refs, f.refsErr
}
func (f *fakeCandClient) ReadBranchRef(_ context.Context, issueSlug string) (*git.BranchRef, error) {
	return f.bySlug[issueSlug], nil
}
func (f *fakeCandClient) ResolveBranchRef(name string) (plumbing.Hash, error) {
	if f.unresolvable[name] {
		return plumbing.ZeroHash, errors.New("not found")
	}
	return plumbing.ZeroHash, nil
}
func (f *fakeCandClient) BranchExists(name string) (bool, error) { return f.localExists[name], nil }
func (f *fakeCandClient) CreateLocalBranch(_ context.Context, name, _ string) error {
	f.created = append(f.created, name)
	return nil
}

func TestCloseCandidates(t *testing.T) {
	t.Parallel()

	t.Run("store-only when no refs", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: []store.BranchRow{{IssueID: 1, IssueSlug: "A", BranchName: "A@feat@x"}}}
		c := &fakeCandClient{refs: nil}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 || got[0].IssueSlug != "A" {
			t.Fatalf("got %+v, want single store row A", got)
		}
	})

	t.Run("ref-only synthesizes untracked rows", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: nil}
		c := &fakeCandClient{refs: []git.BranchRef{
			{IssueSlug: "B", BranchName: "B@feat@thing", CreatedAt: "2026-07-21T10:00:00Z"},
		}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d rows, want 1: %+v", len(got), got)
		}
		if got[0].IssueID != 0 {
			t.Errorf("IssueID = %d, want 0 (untracked marker)", got[0].IssueID)
		}
		if got[0].Type != "feat" || got[0].Title != "thing" {
			t.Errorf("Type/Title = %q/%q, want feat/thing", got[0].Type, got[0].Title)
		}
	})

	t.Run("union dedupes by slug (store wins)", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: []store.BranchRow{{IssueID: 7, IssueSlug: "C", BranchName: "C@feat@x"}}}
		c := &fakeCandClient{refs: []git.BranchRef{{IssueSlug: "C", BranchName: "C@feat@x"}}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 || got[0].IssueID != 7 {
			t.Fatalf("got %+v, want single tracked row with IssueID 7", got)
		}
	})

	t.Run("merged ref excluded", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{refs: []git.BranchRef{{IssueSlug: "D", BranchName: "D@feat@x", Merged: true}}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no candidates (merged excluded)", got)
		}
	})

	t.Run("unresolvable feature branch excluded", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			refs:         []git.BranchRef{{IssueSlug: "E", BranchName: "E@feat@x"}},
			unresolvable: map[string]bool{"E@feat@x": true},
		}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no candidates (unresolvable excluded)", got)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/issueflow/... -run TestCloseCandidates -v`
Expected: FAIL — undefined: `CloseCandidates`, `CandidateStore`, `CandidateClient`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/issueflow/closecandidates.go`:

```go
package issueflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// CandidateStore is the slice of *store.Store the close-candidate helpers need.
type CandidateStore interface {
	ListBranches(ctx context.Context, status store.BranchStatus) ([]store.BranchRow, error)
	InsertIssueWithBranch(ctx context.Context, issue *store.Issue, b *store.Branch) error
}

// CandidateClient is the slice of *git.Client the close-candidate helpers need.
type CandidateClient interface {
	ListBranchRefs(ctx context.Context) ([]git.BranchRef, error)
	ReadBranchRef(ctx context.Context, issueSlug string) (*git.BranchRef, error)
	ResolveBranchRef(name string) (plumbing.Hash, error)
	BranchExists(name string) (bool, error)
	CreateLocalBranch(ctx context.Context, name, startPoint string) error
}

// CloseCandidates returns the branches a close picker should offer: every
// in-progress branch from the store, unioned with every refs/zf/branches/*
// entry (merged=false) that has no store row and whose feature branch resolves
// locally or via origin. Ref-derived rows carry IssueID == 0 as the
// "not yet tracked in this store" marker; MaterializeAndTrack promotes them.
//
// Reads local refs only — callers should FetchBranchRefs first (getPickedBranch
// does so via ReconcileMergedFromRefs). Mirrors the cross-machine ref fallback
// in ResolveParentBranch. A ListBranchRefs failure degrades to store-only
// candidates (never fatal to close).
func CloseCandidates(ctx context.Context, s CandidateStore, c CandidateClient) ([]store.BranchRow, error) {
	inProgress, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list in-progress branches: %w", err)
	}

	seen := make(map[string]bool, len(inProgress))
	for _, b := range inProgress {
		seen[b.IssueSlug] = true
	}

	refs, err := c.ListBranchRefs(ctx)
	if err != nil {
		return inProgress, nil //nolint:nilerr // best-effort: degrade to store-only
	}

	out := inProgress
	for _, ref := range refs {
		if ref.Merged || seen[ref.IssueSlug] {
			continue
		}
		if _, resolveErr := c.ResolveBranchRef(ref.BranchName); resolveErr != nil {
			continue // feature branch not present locally or on origin — cannot close
		}
		seen[ref.IssueSlug] = true
		out = append(out, synthRow(ref))
	}

	return out, nil
}

// synthRow builds a BranchRow from a branch ref for an untracked candidate.
// IssueID is left 0 (marker). Type/Title come from the parsed branch name; on a
// parse failure Type is "" and Title falls back to the issue slug.
func synthRow(ref git.BranchRef) store.BranchRow {
	row := store.BranchRow{
		IssueID:    0,
		IssueSlug:  ref.IssueSlug,
		BranchName: ref.BranchName,
		Title:      ref.IssueSlug,
		Status:     store.BranchStatusInProgress,
	}
	if b, err := branch.Parse(ref.BranchName); err == nil {
		row.Type = b.Type()
		row.Title = strings.ReplaceAll(b.Title(), "-", " ")
	}
	if t, terr := time.Parse(time.RFC3339, ref.CreatedAt); terr == nil {
		row.CreatedAt = t
	}
	return row
}

// Compile-time checks that the production types satisfy the roles.
var (
	_ CandidateStore  = (*store.Store)(nil)
	_ CandidateClient = (*git.Client)(nil)
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/issueflow/... -run TestCloseCandidates -v`
Expected: PASS (all five subtests).

- [ ] **Step 5: Verify the compile-time role assertions hold**

Run: `mise exec -- go build ./...`
Expected: builds cleanly (confirms `*store.Store` and `*git.Client` satisfy the new interfaces — `ResolveBranchRef`, `BranchExists`, `CreateLocalBranch`, `ListBranchRefs`, `ReadBranchRef`, `ListBranches`, `InsertIssueWithBranch` all exist with matching signatures).

- [ ] **Step 6: Checkpoint (user commits)**

Staged files: `cmd/issueflow/closecandidates.go`, `cmd/issueflow/closecandidates_test.go`.
Suggested message: `feat(issueflow): add CloseCandidates union of store rows and branch refs`

---

### Task 4: `issueflow.MaterializeAndTrack` — promote a ref-derived pick

**Files:**
- Modify: `cmd/issueflow/closecandidates.go` (append two functions)
- Test: `cmd/issueflow/closecandidates_test.go` (append one test function; reuses the Task 3 fakes)

**Interfaces:**
- Consumes: `CandidateStore`, `CandidateClient` (Task 3); `store.Issue`, `store.Branch`, `store.StatusIDInProgress`, `store.BranchStatusAll`.
- Produces:
  - `func MaterializeAndTrack(ctx context.Context, s CandidateStore, c CandidateClient, picked store.BranchRow) (store.BranchRow, error)` — a pick with `IssueID != 0` is returned unchanged; a pick with `IssueID == 0` is materialized (local branch from origin) and inserted, then re-read and returned with a real `IssueID`.
  - Unexported `findByBranchName(rows []store.BranchRow, name string) *store.BranchRow`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/issueflow/closecandidates_test.go`:

```go
func TestMaterializeAndTrack(t *testing.T) {
	t.Parallel()

	t.Run("store-derived pick returned unchanged", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{}
		picked := store.BranchRow{IssueID: 5, IssueSlug: "A", BranchName: "A@feat@x", Type: "feat"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if got.IssueID != 5 {
			t.Errorf("IssueID = %d, want 5 (unchanged)", got.IssueID)
		}
		if len(s.inserted) != 0 || len(c.created) != 0 {
			t.Errorf("expected no insert/create for tracked pick; inserted=%v created=%v", s.inserted, c.created)
		}
	})

	t.Run("ref-derived pick materializes and tracks", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{}, // feature branch absent locally
			bySlug: map[string]*git.BranchRef{
				"B": {IssueSlug: "B", BranchName: "B@feat@thing", TrackerType: "github"},
			},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if got.IssueID == 0 {
			t.Errorf("IssueID = 0, want non-zero after track")
		}
		if len(c.created) != 1 || c.created[0] != "B@feat@thing" {
			t.Errorf("created = %v, want [B@feat@thing]", c.created)
		}
		if len(s.inserted) != 1 {
			t.Fatalf("inserted len = %d, want 1", len(s.inserted))
		}
		if got.IssueID != s.all[len(s.all)-1].IssueID {
			t.Errorf("returned IssueID %d does not match tracked row", got.IssueID)
		}
	})

	t.Run("tracker type carried from ref", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{"B@feat@thing": true}, // already local; skip create
			bySlug:      map[string]*git.BranchRef{"B": {IssueSlug: "B", BranchName: "B@feat@thing", TrackerType: "github"}},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		if _, err := MaterializeAndTrack(t.Context(), s, c, picked); err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if len(s.inserted) != 1 {
			t.Fatalf("inserted len = %d, want 1", len(s.inserted))
		}
		tt := s.inserted[0].issue.TrackerType
		if tt == nil || *tt != "github" {
			t.Errorf("issue TrackerType = %v, want *github", tt)
		}
	})

	t.Run("empty tracker type inserts nil (manual)", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{"B@feat@thing": true},
			bySlug:      map[string]*git.BranchRef{"B": {IssueSlug: "B", BranchName: "B@feat@thing"}},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		if _, err := MaterializeAndTrack(t.Context(), s, c, picked); err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if s.inserted[0].issue.TrackerType != nil {
			t.Errorf("issue TrackerType = %v, want nil (manual)", *s.inserted[0].issue.TrackerType)
		}
	})

	t.Run("idempotent: already-tracked branch not double-inserted", func(t *testing.T) {
		t.Parallel()
		existing := store.BranchRow{IssueID: 3, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat"}
		s := &fakeCandStore{all: []store.BranchRow{existing}}
		c := &fakeCandClient{localExists: map[string]bool{"B@feat@thing": true}}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if len(s.inserted) != 0 {
			t.Errorf("expected no insert; inserted=%v", s.inserted)
		}
		if got.IssueID != 3 {
			t.Errorf("IssueID = %d, want 3 (existing row)", got.IssueID)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/issueflow/... -run TestMaterializeAndTrack -v`
Expected: FAIL — undefined: `MaterializeAndTrack`.

- [ ] **Step 3: Write minimal implementation**

Append to `cmd/issueflow/closecandidates.go` (imports already include `context`, `fmt`, `store`, `git`):

```go
// MaterializeAndTrack promotes a ref-derived close candidate (IssueID == 0) into
// a tracked branch: it materializes the local feature branch from origin when
// absent, inserts the issue + branch rows, then returns the now-tracked
// BranchRow (with a real IssueID) for the rest of the close flow. A candidate
// that is already tracked (IssueID != 0) is returned unchanged.
//
// The inserted issue's TrackerType is read from the branch ref so
// `git zf issue list` classifies it correctly; the tracker-update gate in the
// close flow reads TrackerType from the ref + config, not this row, so an empty
// value here does not change tracker behavior.
func MaterializeAndTrack(
	ctx context.Context, s CandidateStore, c CandidateClient, picked store.BranchRow,
) (store.BranchRow, error) {
	if picked.IssueID != 0 {
		return picked, nil // already tracked; developer started it locally
	}

	// 1. Materialize the local feature branch from origin when it is absent.
	exists, err := c.BranchExists(picked.BranchName)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("check branch %q exists: %w", picked.BranchName, err)
	}
	if !exists {
		h, resolveErr := c.ResolveBranchRef(picked.BranchName)
		if resolveErr != nil {
			return store.BranchRow{}, fmt.Errorf("resolve feature branch %q: %w", picked.BranchName, resolveErr)
		}
		if createErr := c.CreateLocalBranch(ctx, picked.BranchName, h.String()); createErr != nil {
			return store.BranchRow{}, fmt.Errorf("materialize feature branch %q: %w", picked.BranchName, createErr)
		}
	}

	// 2. Auto-track (idempotent): skip the insert when the branch is already tracked.
	all, err := s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("list branches: %w", err)
	}
	if tracked := findByBranchName(all, picked.BranchName); tracked != nil {
		return *tracked, nil
	}

	var trackerType *string
	if ref, _ := c.ReadBranchRef(ctx, picked.IssueSlug); ref != nil && ref.TrackerType != "" {
		tt := ref.TrackerType
		trackerType = &tt
	}
	if insErr := s.InsertIssueWithBranch(ctx,
		&store.Issue{
			IDSlug: picked.IssueSlug, Title: picked.Title,
			StatusID: store.StatusIDInProgress, TrackerType: trackerType,
		},
		&store.Branch{Name: picked.BranchName, Type: picked.Type, StatusID: store.StatusIDInProgress},
	); insErr != nil {
		return store.BranchRow{}, fmt.Errorf("track branch %q: %w", picked.BranchName, insErr)
	}

	// Re-read so IssueID is populated for downstream (updateClosedStatus).
	all, err = s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return store.BranchRow{}, fmt.Errorf("re-list branches: %w", err)
	}
	tracked := findByBranchName(all, picked.BranchName)
	if tracked == nil {
		return store.BranchRow{}, fmt.Errorf("tracked branch %q not found after insert", picked.BranchName)
	}
	return *tracked, nil
}

// findByBranchName returns a pointer to the row whose BranchName matches, or nil.
func findByBranchName(rows []store.BranchRow, name string) *store.BranchRow {
	for i := range rows {
		if rows[i].BranchName == name {
			return &rows[i]
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/issueflow/... -run TestMaterializeAndTrack -v`
Expected: PASS (all five subtests).

- [ ] **Step 5: Checkpoint (user commits)**

Staged files: `cmd/issueflow/closecandidates.go`, `cmd/issueflow/closecandidates_test.go`.
Suggested message: `feat(issueflow): add MaterializeAndTrack to promote ref-derived close picks`

---

### Task 5: Wire the fallback into the close flow + E2E test

**Files:**
- Modify: `cmd/issue/close.go:459-493` (`getPickedBranch` — swap the store list for `CloseCandidates`) and `cmd/issue/close.go:152-160` (`runClose` — insert the promote step after the pick)
- Test: `cmd/issue/close_e2e_test.go` (append `TestClose_ReviewerInitiated`)

**Interfaces:**
- Consumes: `issueflow.CloseCandidates` (Task 3), `issueflow.MaterializeAndTrack` (Task 4).
- Produces: no new exported symbols — behavior change to `runClose`.

- [ ] **Step 1: Write the failing E2E test**

Append to `cmd/issue/close_e2e_test.go` (imports `bytes`, `os`, `exec`, `filepath`, `strings`, `time`, `commitpkg`, `config`, `git`, `pkg`, `store` are already present):

```go
// TestClose_ReviewerInitiated verifies a reviewer/teammate can close an issue
// they did not start: an empty local store, the feature branch present only as
// origin/<feature>, and only refs/zf/branches/<slug> to go on. The close flow
// must surface the ref-derived candidate, materialize the feature branch, track
// it, merge it, and stamp the ref merged=true.
func TestClose_ReviewerInitiated(t *testing.T) {
	t.Parallel()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	carolDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// ----- origin + seed (developer) -----
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runIn(originDir, "init", "--bare", "--initial-branch=main")

	seedDir := t.TempDir()
	runIn(seedDir, "init", "--initial-branch=main")
	runIn(seedDir, "config", "user.name", "Seed")
	runIn(seedDir, "config", "user.email", "seed@example.com")
	runIn(seedDir, "config", "commit.gpgsign", "false")
	writeFileAt(t, seedDir, "base.txt", "base\n")
	runIn(seedDir, "add", "base.txt")
	runIn(seedDir, "commit", "-m", "chore: init")
	runIn(seedDir, "remote", "add", "origin", originDir)
	runIn(seedDir, "push", "origin", "main")

	runIn(seedDir, "checkout", "-b", "ABC-1@feat@thing")
	writeFileAt(t, seedDir, "feature.txt", "feature\n")
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "feat: implement")
	runIn(seedDir, "push", "origin", "ABC-1@feat@thing")
	runIn(seedDir, "checkout", "main")

	// Developer publishes the branch ref (manual issue: no tracker origin).
	seedClient, err := git.NewClientAt(nil, seedDir)
	if err != nil {
		t.Fatalf("seed NewClientAt: %v", err)
	}
	if _, err := seedClient.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug: "ABC-1", BranchName: "ABC-1@feat@thing",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		t.Fatalf("seed WriteBranchRef: %v", err)
	}
	runIn(seedDir, "push", "origin", "refs/zf/branches/ABC-1")

	// ----- Carol's clone: empty store, no local feature branch -----
	runIn(carolDir, "init", "--initial-branch=main")
	runIn(carolDir, "config", "user.name", "Carol")
	runIn(carolDir, "config", "user.email", "carol@example.com")
	runIn(carolDir, "config", "commit.gpgsign", "false")
	runIn(carolDir, "remote", "add", "origin", originDir)
	runIn(carolDir, "fetch", "origin")
	runIn(carolDir, "checkout", "main")
	// ABC-1@feat@thing deliberately NOT checked out locally.

	stdout := &bytes.Buffer{}
	carolClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{},
	}, carolDir)
	if err != nil {
		t.Fatalf("carol NewClientAt: %v", err)
	}

	carolStore, err := store.Open(t.Context(), filepath.Join(carolDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = carolStore.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	deps := closeDeps{client: carolClient, store: carolStore, cfg: cfg}

	// The picker returns the ref-derived candidate (IssueID 0). runClose must
	// promote it via MaterializeAndTrack before merging.
	prompter := &scriptedPrompter{
		Branch: &store.BranchRow{
			IssueID: 0, IssueSlug: "ABC-1", Title: "thing",
			BranchName: "ABC-1@feat@thing", Type: "feat", Status: store.BranchStatusInProgress,
		},
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): reviewer closes ABC-1\n"),
		DeleteBranch: false,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was offered the ref-derived candidate", func(t *testing.T) {
		var found bool
		for _, b := range prompter.PickBranchSeen {
			if b.IssueSlug == "ABC-1" && b.BranchName == "ABC-1@feat@thing" && b.IssueID == 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("PickBranchSeen = %+v, want an ABC-1 ref-derived (IssueID 0) row", prompter.PickBranchSeen)
		}
	})

	t.Run("feature branch materialized locally", func(t *testing.T) {
		exists, err := carolClient.BranchExists("ABC-1@feat@thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("expected local ABC-1@feat@thing to be materialized")
		}
	})

	t.Run("main HEAD carries the close commit", func(t *testing.T) {
		assertHeadSubject(t, carolDir, "main", "feat(thing): reviewer closes ABC-1")
	})

	t.Run("store now tracks ABC-1 as merged", func(t *testing.T) {
		merged, err := carolStore.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(merged) != 1 || merged[0].BranchName != "ABC-1@feat@thing" {
			t.Errorf("merged rows = %+v, want one ABC-1@feat@thing", merged)
		}
	})

	t.Run("branch ref stamped merged=true", func(t *testing.T) {
		ref, err := carolClient.ReadBranchRef(t.Context(), "ABC-1")
		if err != nil {
			t.Fatalf("ReadBranchRef: %v", err)
		}
		if ref == nil || !ref.Merged {
			t.Errorf("branch ref = %+v, want Merged=true", ref)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run TestClose_ReviewerInitiated -v`
Expected: FAIL — with the store empty, today's `getPickedBranch` prints "No in-progress branches" and `runClose` returns nil, so the merge never happens (e.g. "main HEAD carries the close commit" fails: subject is still `chore: init`).

- [ ] **Step 3: Wire `CloseCandidates` into `getPickedBranch`**

In `cmd/issue/close.go`, inside `getPickedBranch`, replace:

```go
	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
```

with:

```go
	branches, err := issueflow.CloseCandidates(ctx, s, client)
	if err != nil {
		return nil, fmt.Errorf("list close candidates: %w", err)
	}
```

(`issueflow` and `store` are already imported in this file.)

- [ ] **Step 4: Insert the promote step into `runClose`**

In `cmd/issue/close.go`, inside `runClose`, immediately after:

```go
	if picked == nil {
		return nil
	}
```

insert:

```go
	// A ref-derived pick (reviewer/teammate closing a branch they never started)
	// has IssueID == 0: materialize the feature branch from origin and track it
	// so the rest of the flow — which reads the store and needs a local feature
	// branch — behaves exactly as for a locally-started branch.
	promoted, err := issueflow.MaterializeAndTrack(ctx, deps.store, deps.client, *picked)
	if err != nil {
		return err
	}
	picked = &promoted
```

- [ ] **Step 5: Run the new test to verify it passes**

Run: `mise exec -- go test ./cmd/issue/... -run TestClose_ReviewerInitiated -v`
Expected: PASS (all five subtests).

- [ ] **Step 6: Run the full close E2E suite to check for regressions**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v`
Expected: PASS — existing tests use `pickedBranchRow()` with `IssueID: 1`, so `MaterializeAndTrack` is a no-op for them and behavior is unchanged.

- [ ] **Step 7: Full build, vet, and test sweep**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: all green.

- [ ] **Step 8: Checkpoint (user commits)**

Staged files: `cmd/issue/close.go`, `cmd/issue/close_e2e_test.go`.
Suggested message: `feat(issue): close teammate/reviewer branches via refs/zf/branches fallback`

---

### Task 6: Document the behavior in README

**Files:**
- Modify: `README.md:114` (the `issue close` command description)

**Interfaces:** none (docs only).

- [ ] **Step 1: Update the `issue close` description**

In `README.md`, replace the line at 114:

```markdown
**`issue close`** — close an in-progress issue: pick from the list of in-progress branches (the currently checked-out branch is pre-selected), merge into the base branch, update the local store, and optionally update the tracker status and delete the local branch.
```

with:

```markdown
**`issue close`** — close an in-progress issue: pick from the list of in-progress branches (the currently checked-out branch is pre-selected), merge into the base branch, update the local store, and optionally update the tracker status and delete the local branch.

The picker also lists branches known only from fetched `refs/zf/branches/*` refs, so a reviewer or teammate can close an issue they did not start without running `git zf issue track` first — the feature branch is materialized from `origin/<branch>` and tracked automatically before the merge.
```

- [ ] **Step 2: Verify the doc renders and nothing else changed**

Run: `git diff README.md`
Expected: only the two-paragraph change at the `issue close` entry.

- [ ] **Step 3: Checkpoint (user commits)**

Staged files: `README.md`.
Suggested message: `docs(readme): note reviewer-initiated close in issue close`

---

## Self-Review

**1. Spec coverage:**
- Picker = union of store + branch refs → Task 3 (`CloseCandidates`) + Task 5 (wired into `getPickedBranch`). ✓
- Merged-ref excluded, no-store-row only, feature-branch-resolves filter → Task 3 tests. ✓
- Auto-track on pick with `IssueID==0` sentinel → Task 4 (`MaterializeAndTrack`). ✓
- Materialize feature branch from origin → Task 2 (`CreateLocalBranch`) + Task 4. ✓
- `TrackerType` carried from ref into the store row (nil when empty) → Task 4 tests "tracker type carried" / "empty tracker type inserts nil". ✓
- Runs before `reviewPreflight` → Task 5 Step 4 inserts the promote step immediately after the pick, before the rest of `runClose`. ✓
- New primitive `ListBranchRefs` mirroring `ListReviewRefs` → Task 1. ✓
- Downstream unchanged / cross-machine tolerant, ref stamped merged=true and pushed → Task 5 E2E asserts store-merged + ref merged. ✓
- Error handling: no remote / unresolvable feature → filtered (Task 3); `CreateLocalBranch` failure aborts (Task 4); `ListBranchRefs` failure degrades to store-only (Task 3). ✓
- Testing: git unit (Tasks 1–2), issueflow unit (Tasks 3–4), E2E `TestClose_ReviewerInitiated` (Task 5), README (Task 6). ✓
- YAGNI: no parent/child relation inserts, no new flags — respected (nothing adds them). ✓

**2. Placeholder scan:** No TBD/TODO; every code step shows complete code; every test step gives the exact `mise exec` command and expected outcome. ✓

**3. Type consistency:**
- `ListBranchRefs(ctx) ([]git.BranchRef, error)` — defined Task 1, consumed by `CandidateClient` (Task 3) and the fake. ✓
- `CreateLocalBranch(ctx, name, startPoint string) error` — defined Task 2, in `CandidateClient` (Task 3), called by `MaterializeAndTrack` (Task 4). ✓
- `ResolveBranchRef(name) (plumbing.Hash, error)` — existing, in `CandidateClient`; the fake returns `plumbing.ZeroHash`. ✓
- `CloseCandidates(ctx, CandidateStore, CandidateClient) ([]store.BranchRow, error)` — Task 3, called in `getPickedBranch` (Task 5). ✓
- `MaterializeAndTrack(ctx, CandidateStore, CandidateClient, store.BranchRow) (store.BranchRow, error)` — Task 4, called in `runClose` (Task 5). ✓
- `store.Issue.TrackerType` is `*string`; Task 4 maps ref string → pointer (nil when empty), matching the struct. ✓
- Sentinel `IssueID == 0` used consistently in Tasks 3 (produce), 4 (branch), 5 (prompter row + assertion). ✓
