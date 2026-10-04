# Issue Close Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `git zf issue close` — a TUI that picks an in-progress branch, dry-runs a merge, merges (squash or classic), updates the local store and remote tracker, then optionally deletes the local branch.

**Architecture:** All git operations use subprocess `exec.CommandContext` (never `exec.Command`). New methods live in `git/merge.go` (separate from `git/git.go`). The orchestration follows the same layered pattern as `issue start`: `cmd/issue/close.go` calls TUI helpers, git methods, and store methods in sequence.

**Tech Stack:** Go, cobra, huh (TUI forms), go-git v6, sqlite via store package, tracker package

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `store/store.go` | Modify | Add `IssueID int64` to `BranchRow`; update SQL queries |
| `store/store_test.go` | Modify | Add test verifying `IssueID > 0` in `ListBranches` result |
| `git/git.go` | Modify | Add `NewClientAt(dir string)` constructor |
| `git/merge.go` | Create | `CurrentBranch`, `MergeDryRun`, `MergeSquash`, `MergeNoFF`, `DeleteLocalBranch` |
| `git/merge_test.go` | Create | Tests for all five methods using real on-disk repos |
| `tui/issue.go` | Modify | Add `IssueBranchPicker`, `IssueMergeStrategy`, `IssueMergeAuthor`, `IssueMergeConfirm`, `IssueDeleteBranch` |
| `tui/issue_test.go` | Modify | Tests for pre-selection logic of the two non-trivial new components |
| `cmd/issue/close.go` | Create | Close command orchestration |
| `cmd/issue/issue.go` | Modify | Register `getCloseCmd()` + handle `IssueActionNameClose` in `runE` |

---

## Task 1 — Extend `BranchRow` with `IssueID`

The close flow needs the numeric `issue.id` to call `UpdateIssueStatus`. The current `BranchRow` only carries `IssueSlug` (the text slug). Add `IssueID int64` and update both queries.

**Files:**
- Modify: `store/store.go`
- Modify: `store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `store/store_test.go`:

```go
func TestListBranches_includesIssueID(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "X-1", Title: "IssueID test", StatusID: 1},
		&Branch{UUID: "uuid-x1", Name: "X-1@feat@issueid@uuid-x1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	if len(rows) == 0 {
		t.Fatal("expected at least one branch row")
	}

	if rows[0].IssueID <= 0 {
		t.Errorf("IssueID = %d, want > 0", rows[0].IssueID)
	}
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
mise exec -- go test ./store/... -run TestListBranches_includesIssueID -v
```

Expected: compile error — `BranchRow` has no field `IssueID`.

- [ ] **Step 3: Add `IssueID` to `BranchRow` and update queries**

In `store/store.go`, update the `BranchRow` struct:

```go
// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
	UUID       string       `json:"uuid"`
	IssueID    int64        `json:"issue_id"`
	IssueSlug  string       `json:"issue_slug"`
	Title      string       `json:"title"`
	BranchName string       `json:"branch_name"`
	Type       string       `json:"type"`
	Status     BranchStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
}
```

Update `ListBranches` — change the SELECT and Scan:

```go
func (s *Store) ListBranches(ctx context.Context, status BranchStatus) ([]BranchRow, error) {
	q := `
		SELECT b.uuid, i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id`

	var args []any
	if status != BranchStatusAll {
		q += " WHERE st.name = ?"
		args = append(args, string(status))
	}

	q += " ORDER BY b.created_at DESC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list branches query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []BranchRow

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.UUID, &r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		result = append(result, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	if result == nil {
		result = []BranchRow{}
	}

	return result, nil
}
```

Update `ListBranchesByIssueSlugs` — change the SELECT and Scan similarly:

```go
func (s *Store) ListBranchesByIssueSlugs(ctx context.Context, slugs []string) (map[string]BranchRow, error) {
	if len(slugs) == 0 {
		return make(map[string]BranchRow), nil
	}

	q := `
SELECT b.uuid, i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
FROM branches b
JOIN issues i ON b.issue_id = i.id
JOIN statuses st ON b.status_id = st.id
WHERE i.id_slug IN (SELECT value FROM json_each(?))
ORDER BY b.created_at DESC`

	args, err := json.Marshal(slugs)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to json: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, q, string(args))
	if err != nil {
		return nil, fmt.Errorf("list branches by slugs query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]BranchRow)

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.UUID, &r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		if _, exists := result[r.IssueSlug]; !exists {
			result[r.IssueSlug] = r
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	return result, nil
}
```

- [ ] **Step 4: Run all store tests — verify they pass**

```bash
mise exec -- go test ./store/... -v
```

Expected: all tests pass including `TestListBranches_includesIssueID`.

- [ ] **Step 5: Commit**

```bash
git add store/store.go store/store_test.go
git commit -m "feat(store): add IssueID to BranchRow"
```

---

## Task 2 — `NewClientAt` + `CurrentBranch` + `MergeDryRun`

The merge operations require subprocess git commands on real on-disk repos (go-git's in-memory storage doesn't support worktrees). `NewClientAt` opens a repo at a specific path for use in tests. `CurrentBranch` returns the checked-out branch name. `MergeDryRun` uses a temporary git worktree to test merge cleanness without touching the main working tree.

**Files:**
- Modify: `git/git.go`
- Create: `git/merge.go`
- Create: `git/merge_test.go`

- [ ] **Step 1: Write failing tests**

Create `git/merge_test.go`:

```go
package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
mise exec -- go test ./git/... -run "TestCurrentBranch|TestMergeDryRun" -v
```

Expected: compile errors — `NewClientAt`, `CurrentBranch`, `MergeDryRun` undefined.

- [ ] **Step 3: Add `NewClientAt` to `git/git.go`**

Add after the existing `NewClient` function:

```go
// NewClientAt opens the git repository rooted at dir.
// Used in tests and tooling that need to open a repo at a specific path.
func NewClientAt(dir string) (*Client, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open git repository at %s: %w", dir, err)
	}

	return &Client{repo: repo}, nil
}
```

- [ ] **Step 4: Create `git/merge.go` with `CurrentBranch` and `MergeDryRun`**

```go
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CurrentBranch returns the short name of the currently checked-out branch.
func (c *Client) CurrentBranch(ctx context.Context) (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("current branch: %w", err)
	}

	return strings.TrimSpace(string(out)), nil
}

// MergeDryRun checks whether branchName merges cleanly into baseBranch.
// It performs the check in a temporary git worktree so the main working tree
// is never modified. Returns the list of conflicting file paths, or nil if clean.
func (c *Client) MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "git-zf-dry-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	// --detach avoids "already checked out" error when baseBranch is current.
	addOut, addErr := exec.CommandContext(ctx,
		"git", "-C", root, "worktree", "add", "--detach", tmpDir, baseBranch,
	).CombinedOutput()
	if addErr != nil {
		_ = os.RemoveAll(tmpDir)

		return nil, fmt.Errorf("add worktree: %w: %s", addErr, addOut)
	}

	defer func() {
		rmCmd := exec.CommandContext(context.Background(),
			"git", "-C", root, "worktree", "remove", "--force", tmpDir)
		_ = rmCmd.Run()
		_ = os.RemoveAll(tmpDir)
	}()

	mergeCmd := exec.CommandContext(ctx, "git", "merge", "--no-commit", "--no-ff", branchName)
	mergeCmd.Dir = tmpDir
	out, mergeErr := mergeCmd.CombinedOutput()

	// Always abort the in-progress merge to restore the worktree.
	abortCmd := exec.CommandContext(context.Background(), "git", "merge", "--abort")
	abortCmd.Dir = tmpDir
	if abortErr := abortCmd.Run(); abortErr != nil {
		// merge --abort fails when there were no conflicts (merge was staged but clean).
		// Fall back to hard reset.
		resetCmd := exec.CommandContext(context.Background(), "git", "reset", "--hard", "HEAD")
		resetCmd.Dir = tmpDir
		_ = resetCmd.Run()
	}

	if mergeErr == nil {
		return nil, nil
	}

	conflicts := parseConflictFiles(string(out))
	if len(conflicts) == 0 {
		return nil, fmt.Errorf("merge dry-run failed: %w: %s", mergeErr, out)
	}

	return conflicts, nil
}

// parseConflictFiles extracts file paths from git merge conflict output lines.
// Each conflict line looks like: "CONFLICT (content): Merge conflict in path/to/file.go"
func parseConflictFiles(output string) []string {
	var files []string

	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "CONFLICT") {
			continue
		}

		if idx := strings.LastIndex(line, " in "); idx >= 0 {
			files = append(files, strings.TrimSpace(line[idx+4:]))
		}
	}

	return files
}
```

- [ ] **Step 5: Run tests — verify they pass**

```bash
mise exec -- go test ./git/... -run "TestCurrentBranch|TestMergeDryRun" -v
```

Expected: all three tests pass.

- [ ] **Step 6: Run all tests — verify no regressions**

```bash
mise exec -- go test ./... -v 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add git/git.go git/merge.go git/merge_test.go
git commit -m "feat(git): add NewClientAt, CurrentBranch, MergeDryRun"
```

---

## Task 3 — `MergeSquash`, `MergeNoFF`, `DeleteLocalBranch`

**Files:**
- Modify: `git/merge.go`
- Modify: `git/merge_test.go`

- [ ] **Step 1: Write failing tests**

Add to `git/merge_test.go`:

```go
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
	var buf bytes.Buffer
	logCmd := exec.Command("git", "log", "--oneline", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &buf
	_ = logCmd.Run()

	if !strings.Contains(buf.String(), "squash merge") {
		t.Errorf("squash commit message not found in: %q", buf.String())
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
	var buf bytes.Buffer
	logCmd := exec.Command("git", "log", "--pretty=%P", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &buf
	_ = logCmd.Run()

	parents := strings.Fields(strings.TrimSpace(buf.String()))
	if len(parents) != 2 {
		t.Errorf("expected merge commit with 2 parents, got %d: %s", len(parents), buf.String())
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
	var buf bytes.Buffer
	branchCmd := exec.Command("git", "branch")
	branchCmd.Dir = dir
	branchCmd.Stdout = &buf
	_ = branchCmd.Run()

	if strings.Contains(buf.String(), "feature") {
		t.Errorf("feature branch still exists after delete: %s", buf.String())
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

	var buf bytes.Buffer
	branchCmd := exec.Command("git", "branch")
	branchCmd.Dir = dir
	branchCmd.Stdout = &buf
	_ = branchCmd.Run()

	if strings.Contains(buf.String(), "feature") {
		t.Errorf("feature branch still exists after force delete: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
mise exec -- go test ./git/... -run "TestMergeSquash|TestMergeNoFF|TestDeleteLocalBranch" -v
```

Expected: compile errors — `MergeSquash`, `MergeNoFF`, `DeleteLocalBranch` undefined.

- [ ] **Step 3: Add `MergeSquash`, `MergeNoFF`, `DeleteLocalBranch` to `git/merge.go`**

Append to `git/merge.go`:

```go
// MergeSquash squash-merges branchName into baseBranch and commits.
// author is "Name <email>"; if empty the git config identity is used.
// After the merge the working directory is on baseBranch.
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch, author string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "checkout", baseBranch).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %w: %s", baseBranch, err, out)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "merge", "--squash", branchName).CombinedOutput(); err != nil {
		return fmt.Errorf("merge --squash %s: %w: %s", branchName, err, out)
	}

	msg := "squash merge '" + branchName + "'"
	commitArgs := []string{"-C", root, "commit", "-m", msg}
	if author != "" {
		commitArgs = append(commitArgs, "--author="+author)
	}

	if out, err := exec.CommandContext(ctx, "git", commitArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("commit squash: %w: %s", err, out)
	}

	return nil
}

// MergeNoFF runs a classic --no-ff merge of branchName into baseBranch.
// After the merge the working directory is on baseBranch.
func (c *Client) MergeNoFF(ctx context.Context, branchName, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "checkout", baseBranch).CombinedOutput(); err != nil {
		return fmt.Errorf("checkout %s: %w: %s", baseBranch, err, out)
	}

	if out, err := exec.CommandContext(ctx, "git", "-C", root, "merge", "--no-ff", branchName).CombinedOutput(); err != nil {
		return fmt.Errorf("merge --no-ff %s: %w: %s", branchName, err, out)
	}

	return nil
}

// DeleteLocalBranch deletes the local branch by name.
// force=true uses -D (required after squash merges); force=false uses -d (safe).
func (c *Client) DeleteLocalBranch(ctx context.Context, name string, force bool) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	flag := "-d"
	if force {
		flag = "-D"
	}

	out, err := exec.CommandContext(ctx, "git", "-C", root, "branch", flag, name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete branch %s: %w: %s", name, err, out)
	}

	return nil
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
mise exec -- go test ./git/... -run "TestMergeSquash|TestMergeNoFF|TestDeleteLocalBranch" -v
```

Expected: all four tests pass.

- [ ] **Step 5: Run all tests**

```bash
mise exec -- go test ./... 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add MergeSquash, MergeNoFF, DeleteLocalBranch"
```

---

## Task 4 — TUI Components

**Files:**
- Modify: `tui/issue.go`
- Modify: `tui/issue_test.go`

- [ ] **Step 1: Write failing tests**

Add to `tui/issue_test.go` (check the current imports first; add `"github.com/piprim/git-zf/store"` if not already present):

```go
func TestIssueBranchPicker_preselectsCurrentBranch(t *testing.T) {
	rows := []store.BranchRow{
		{UUID: "a", IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{UUID: "b", IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	var selected store.BranchRow
	IssueBranchPicker(rows, "feature-b", &selected)

	if selected.BranchName != "feature-b" {
		t.Errorf("pre-selected = %q, want %q", selected.BranchName, "feature-b")
	}
}

func TestIssueBranchPicker_defaultsToFirstWhenCurrentUnknown(t *testing.T) {
	rows := []store.BranchRow{
		{UUID: "a", IssueSlug: "A-1", Title: "First", BranchName: "feature-a"},
		{UUID: "b", IssueSlug: "B-1", Title: "Second", BranchName: "feature-b"},
	}

	var selected store.BranchRow
	IssueBranchPicker(rows, "not-in-list", &selected)

	if selected.BranchName != "feature-a" {
		t.Errorf("pre-selected = %q, want first row %q", selected.BranchName, "feature-a")
	}
}

func TestIssueMergeStrategy_defaultsToSquash(t *testing.T) {
	squash := false
	IssueMergeStrategy(&squash)

	if !squash {
		t.Error("IssueMergeStrategy should default squash to true")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
mise exec -- go test ./tui/... -run "TestIssueBranchPicker|TestIssueMergeStrategy" -v
```

Expected: compile errors — `IssueBranchPicker`, `IssueMergeStrategy` undefined.

- [ ] **Step 3: Add TUI components to `tui/issue.go`**

Add these functions (they use `store` which is already imported):

```go
// IssueBranchPicker presents in-progress branches for the close flow.
// The branch matching currentBranch is pre-selected; falls back to the first row.
func IssueBranchPicker(rows []store.BranchRow, currentBranch string, selected *store.BranchRow) *huh.Group {
	opts := make([]huh.Option[store.BranchRow], len(rows))
	for i, r := range rows {
		label := fmt.Sprintf("[%s] %s (%s)", r.IssueSlug, r.Title, r.BranchName)
		opts[i] = huh.NewOption(label, r)
	}

	// Pre-select current branch; default to first row if not found.
	*selected = rows[0]
	for _, r := range rows {
		if r.BranchName == currentBranch {
			*selected = r

			break
		}
	}

	return huh.NewGroup(
		huh.NewSelect[store.BranchRow]().
			Title("Select branch to close:").
			Options(opts...).
			Value(selected),
	)
}

// IssueMergeStrategy lets the user choose between squash (default) and classic merge.
func IssueMergeStrategy(squash *bool) *huh.Group {
	*squash = true

	return huh.NewGroup(
		huh.NewSelect[bool]().
			Title("Merge strategy:").
			Options(
				huh.NewOption("Squash — combine into one commit (default)", true),
				huh.NewOption("Classic — preserve full history (--no-ff)", false),
			).
			Value(squash),
	)
}

// IssueMergeAuthor lets the user pick the squash commit author.
// authors[0] is expected to be the git config identity (pre-filled by the caller).
func IssueMergeAuthor(authors []string, author *string) *huh.Group {
	opts := make([]huh.Option[string], 0, len(authors))
	for _, a := range authors {
		opts = append(opts, huh.NewOption(a, a))
	}

	if len(opts) == 0 {
		opts = []huh.Option[string]{huh.NewOption("(no authors found)", "")}
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Squash commit author:").
			Options(opts...).
			Value(author),
	)
}

// IssueMergeConfirm shows a merge summary and asks for final confirmation.
// author is empty for classic merges.
func IssueMergeConfirm(branchName, baseBranch, strategy, author string, confirmed *bool) *huh.Group {
	desc := fmt.Sprintf("%s → %s (%s)", branchName, baseBranch, strategy)
	if author != "" {
		desc += "\nAuthor: " + author
	}

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Merge %q into %q?", branchName, baseBranch)).
			Description(desc).
			Value(confirmed),
	)
}

// IssueDeleteBranch asks whether to delete the local branch after closing.
func IssueDeleteBranch(branchName string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete local branch %q?", branchName)).
			Value(confirmed),
	)
}
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
mise exec -- go test ./tui/... -run "TestIssueBranchPicker|TestIssueMergeStrategy" -v
```

Expected: all three tests pass.

- [ ] **Step 5: Run all tests**

```bash
mise exec -- go test ./... 2>&1 | tail -20
```

- [ ] **Step 6: Commit**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "feat(tui): add issue close TUI components"
```

---

## Task 5 — `cmd/issue/close.go` + wire into `issue.go`

**Files:**
- Create: `cmd/issue/close.go`
- Modify: `cmd/issue/issue.go`

- [ ] **Step 1: Create `cmd/issue/close.go`**

```go
package issue

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (i Issue) getCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close",
		Short: "Close an issue (merge branch, update store and tracker)",
		Long: `Pick an in-progress branch, merge it into the base branch (squash or classic),
update the local store, update the remote tracker, then optionally delete the local branch.`,
		RunE: i.closeRunE,
	}
}

func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Println("No in-progress branches.")

		return nil
	}

	currentBranch, err := client.CurrentBranch(ctx)
	if err != nil {
		currentBranch = ""
	}

	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, currentBranch, &picked)).Run(); err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}

	base := i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	conflicts, err := client.MergeDryRun(ctx, picked.BranchName, base)
	if err != nil {
		return fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Println("Conflicts detected:")
		for _, f := range conflicts {
			fmt.Println("  " + f)
		}
		fmt.Println("Aborting.")

		return fmt.Errorf("merge conflicts in branch %q", picked.BranchName)
	}

	var squash bool
	if err := huh.NewForm(tui.IssueMergeStrategy(&squash)).Run(); err != nil {
		return fmt.Errorf("strategy picker: %w", err)
	}

	var author string
	if squash {
		if err := i.pickSquashAuthor(client, &author); err != nil {
			return err
		}
	}

	strategy := "no-ff"
	if squash {
		strategy = "squash"
	}

	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(picked.BranchName, base, strategy, author, &confirmed)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if squash {
		if err := client.MergeSquash(ctx, picked.BranchName, base, author); err != nil {
			return fmt.Errorf("merge squash: %w", err)
		}
	} else {
		if err := client.MergeNoFF(ctx, picked.BranchName, base); err != nil {
			return fmt.Errorf("merge no-ff: %w", err)
		}
	}

	now := time.Now()
	if err := s.UpdateBranchStatus(ctx, picked.UUID, 2, &now); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update branch status: %v\n", err)
	}

	if err := s.UpdateIssueStatus(ctx, picked.IssueID, 2); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update issue status: %v\n", err)
	}

	if i.appConfig.IssueTracker.Type != "" {
		i.closeTrackerIssue(cmd, picked.IssueSlug)
	}

	var deleteBranch bool
	if err := huh.NewForm(tui.IssueDeleteBranch(picked.BranchName, &deleteBranch)).Run(); err != nil {
		return fmt.Errorf("delete branch form: %w", err)
	}

	if deleteBranch {
		if err := client.DeleteLocalBranch(ctx, picked.BranchName, squash); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: delete branch: %v\n", err)
		}
	}

	fmt.Printf("Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}

func (i Issue) pickSquashAuthor(client *git.Client, author *string) error {
	authors, err := client.Authors()
	if err != nil || len(authors) == 0 {
		authors = []string{}
	}

	if len(authors) > 0 {
		*author = authors[0]
	}

	if err := huh.NewForm(tui.IssueMergeAuthor(authors, author)).Run(); err != nil {
		return fmt.Errorf("author picker: %w", err)
	}

	return nil
}

func (i Issue) closeTrackerIssue(cmd *cobra.Command, issueSlug string) {
	t, err := tracker.New(i.appConfig.IssueTracker)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: init tracker: %v\n", err)

		return
	}

	i.updateTrackerIssueStatus(cmd, t, issueSlug)
}
```

- [ ] **Step 2: Build — verify no compile errors**

```bash
mise exec -- go build ./...
```

Expected: compiles cleanly.

- [ ] **Step 3: Wire `close` into `cmd/issue/issue.go`**

In `GetRootCmd`, add `i.getCloseCmd()` to `cmd.AddCommand`:

```go
cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd())
```

In `runE`, add the close case:

```go
case tui.IssueActionNameClose:
    return i.closeRunE(cmd, args)
```

The full updated `runE`:

```go
func (i Issue) runE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return i.startRunE(cmd, args)
	case tui.IssueActionNameList:
		return i.issueListRunE(cmd, issueListFlags{})
	case tui.IssueActionNameClose:
		return i.closeRunE(cmd, args)
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}
```

- [ ] **Step 4: Build and run all tests**

```bash
mise exec -- go build ./... && mise exec -- go test ./...
```

Expected: clean build, all tests pass.

- [ ] **Step 5: Smoke test the binary**

```bash
mise exec -- go build -o ./bin/git-zf . && ./bin/git-zf issue close --help
```

Expected: usage text shown with `Use: close` and the long description.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close.go cmd/issue/issue.go
git commit -m "feat(issue): add git zf issue close command"
```
