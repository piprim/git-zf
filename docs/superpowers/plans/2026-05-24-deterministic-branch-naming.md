# Deterministic Branch Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** [`docs/superpowers/specs/2026-05-24-deterministic-branch-naming-design.md`](../specs/2026-05-24-deterministic-branch-naming-design.md)

**Goal:** Drop the random 8-char hex suffix from branch names. Default branch names become deterministic 3-part (`<issueID>@<type>@<slug>`); a new opt-in `--variant=<label>` flag produces 4-part names for genuine N:1 cases. Collisions on the deterministic name surface as a TUI prompt (checkout existing / create variant / abort).

**Architecture:** The `branch` package becomes the source of truth for the name shape — no more `crypto/rand`. The store schema migrates to use `branches.name` as the primary key (dropping the `uuid` column entirely). A new `git.Client.BranchExists` enables collision detection. The conflict UX lives in a small `cmd/issue/conflict.go` orchestrator. All existing 4-part branches keep parsing forever; their trailing segment is exposed as `Variant()` without any legacy/operator distinction.

**Tech Stack:** Go 1.23+, `spf13/cobra`, `charmbracelet/huh`, `go-git/v6`, `modernc.org/sqlite`.

**Toolchain:** Go is managed by `mise`. **Always invoke Go via `mise exec -- go <command>`** (e.g. `mise exec -- go test ./...`). Never call bare `go`.

**Lint conventions to follow throughout the plan:**

- `wrapcheck` — every error returned from an external package must be wrapped with `fmt.Errorf("context: %w", err)`. Never return a bare external error.
- `nlreturn` — when a `return` statement is not the only statement in its block, put a blank line before it.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `store/migrations/0004_branches_name_pk.sql` | Drop old `branches` table, recreate with `name` as PK, re-attach `enforce_merged_at` trigger. |
| `cmd/issue/conflict.go` | `resolveBranchConflict` orchestrator + the pure decision helper that's actually unit-testable. |
| `cmd/issue/conflict_test.go` | Unit tests for the pure decision helper (variant validation + branch reconstruction). |

### Modified files

| Path | Change summary |
|---|---|
| `branch/branch.go` | Add `MaxSlugLen`; truncate in `Slug()`; rename `id` field to `variant`; change `New` to take a `variant` arg; drop `shortUUID()` and `ID()`; add `Variant()`; relax `Parse` to accept 3 OR 4 parts. |
| `branch/branch_test.go` | New slug-cap cases; `TestNew_variant`; `TestParse` for 3-part input + `Variant()` accessor; update `TestParse_invalid` (3-part is now valid). |
| `git/git.go` | Add `BranchExists`. |
| `git/git_test.go` | Add `TestBranchExists`. |
| `store/store.go` | Drop `UUID` field from `Branch` and `BranchRow`. Change `UpdateBranchStatus(uuid …)` and `DeleteBranch(uuid …)` to take `name`. Drop `b.uuid` from all SELECT statements and Scan calls. |
| `store/store_test.go` | Update existing tests to the new shape; add `TestMigration0004BranchesNamePK`. |
| `issue/issue.go` | Add `Variant` field to `IssueStartFlags`. |
| `tui/branch.go` | Add `BranchConflictPicker` and `VariantLabelInput` form constructors. |
| `tui/branch_test.go` (create if absent) or `tui/issue_test.go` | Smoke tests for the two new form constructors. |
| `cmd/issue/start.go` | Drop `UUID:` from `store.Branch` literal in `persist`. Thread `flags` through `prepareBranch`. Wire `--variant` flag into `getStartCmd`/`startRunE`. Call `resolveBranchConflict` from both `createBranch` and `createWorktree`. |
| `cmd/issue/start_test.go` | Adjust existing assertions to new branch-name shape and removed UUID. |
| `cmd/issue/close.go` | Switch `UpdateBranchStatus` call to use `picked.BranchName` instead of `picked.UUID`. |
| `cmd/branch/branch.go` | Switch `UpdateBranchStatus`/`DeleteBranch` calls in the prune executor to use `BranchName`. Wire `--variant` flag into `newCmd`/`newRunE`. |
| `cmd/commit/commit.go` | No code change required — `branch.Parse` now silently accepts 3-part names so this site benefits for free. (Verify in tests.) |
| `README.md` | Update "Branch naming" section + add "Parallel branches per issue" subsection + mention `--variant` in command summaries. |
| `ROADMAP.md` | Remove any line that treated the random suffix as a designed feature. |

---

## Task 1: Slug length cap

**Files:**
- Modify: `branch/branch.go`
- Modify: `branch/branch_test.go`

- [ ] **Step 1: Write the failing test cases for the cap**

Append to `branch/branch_test.go` inside the existing `TestSlug` slice (after the `{"!!!", ""}` entry, before the closing brace):

```go
		// MaxSlugLen behavior.
		{strings.Repeat("a", MaxSlugLen), strings.Repeat("a", MaxSlugLen)},          // exact cap, unchanged
		{strings.Repeat("a", MaxSlugLen+1), strings.Repeat("a", MaxSlugLen)},        // cap+1 cut mid-word, no dash to strip
		{"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj-x", "aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj"}, // 51 chars; s[:50] ends in '-' which is stripped, leaving 49 chars
		{"Δelta " + strings.Repeat("a", MaxSlugLen+10), strings.Repeat("a", MaxSlugLen)}, // unicode dropped, then trailing 'a's truncated
```

(`strings` import is already present in the test file.)

- [ ] **Step 2: Run the test to confirm it fails**

```bash
mise exec -- go test ./branch/... -run TestSlug -v
```

Expected: compile error (`MaxSlugLen` undefined).

- [ ] **Step 3: Implement the cap**

Edit `branch/branch.go`. Add the constant near the regex vars at the top of the file:

```go
// MaxSlugLen caps the slugged title length. Combined with a typical
// issue ID and type, this keeps the full branch name comfortably under
// ~100 chars even when an operator adds --variant.
const MaxSlugLen = 50
```

Then update `Slug` to truncate and strip a dangling trailing hyphen:

```go
func Slug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = reSpaces.ReplaceAllString(s, "-")
	s = nonAlphanumHyphen.ReplaceAllString(s, "")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}

	return s
}
```

- [ ] **Step 4: Run the test to confirm it passes**

```bash
mise exec -- go test ./branch/... -run TestSlug -v
```

Expected: PASS for every subtest.

- [ ] **Step 5: Commit**

```bash
git add branch/branch.go branch/branch_test.go
git commit -m "feat(branch): cap slug length at 50 characters"
```

---

## Task 2: Parser accepts 3-part names

**Files:**
- Modify: `branch/branch.go`
- Modify: `branch/branch_test.go`

- [ ] **Step 1: Write the failing test for 3-part input**

Append to `branch/branch_test.go` after the existing `TestParse` function:

```go
func TestParse_threeParts(t *testing.T) {
	t.Parallel()

	b, err := Parse("ABC-42@feat@add-oauth-login")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	t.Run("issue ID", func(t *testing.T) {
		if b.issueID != "ABC-42" {
			t.Errorf("issueID = %q, want %q", b.issueID, "ABC-42")
		}
	})
	t.Run("branch type", func(t *testing.T) {
		if b.btype != "feat" {
			t.Errorf("branchType = %q, want %q", b.btype, "feat")
		}
	})
	t.Run("slug", func(t *testing.T) {
		if b.title != "add-oauth-login" {
			t.Errorf("title = %q, want %q", b.title, "add-oauth-login")
		}
	})
	t.Run("trailing segment field is empty", func(t *testing.T) {
		if b.id != "" {
			t.Errorf("id = %q, want empty", b.id)
		}
	})
}
```

Then update the `TestParse_invalid` cases slice — **remove** the `{"three parts", "one@two@three"}` entry (a 3-part name is now valid):

```go
	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no @ separators", "no-separators"},
		{"two parts", "only@two"},
		{"five parts", "one@two@three@four@five"},
	}
```

- [ ] **Step 2: Run the tests to confirm one fails and one would-have-failed is gone**

```bash
mise exec -- go test ./branch/... -run "TestParse" -v
```

Expected: `TestParse_threeParts` fails because the parser still rejects 3 parts. `TestParse_invalid/three_parts` no longer appears.

- [ ] **Step 3: Relax the parser**

Edit `branch/branch.go`. Replace the body of `Parse`:

```go
func Parse(name string) (*Branch, error) {
	parts := strings.Split(name, "@")
	switch len(parts) {
	case 3:
		return fromParts(parts[0], parts[1], parts[2], ""), nil
	case 4:
		return fromParts(parts[0], parts[1], parts[2], parts[3]), nil
	default:
		return nil, fmt.Errorf("branch name %q: expected 3 or 4 parts, got %d", name, len(parts))
	}
}
```

(`fromParts` keeps its current signature; for 3-part input the trailing field is empty.)

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
mise exec -- go test ./branch/... -v
```

Expected: every subtest PASS, including the new 3-part case and the unchanged 4-part case.

- [ ] **Step 5: Commit**

```bash
git add branch/branch.go branch/branch_test.go
git commit -m "feat(branch): parser accepts 3-part names (variant-less default)"
```

---

## Task 3: Store migration — drop UUID, key on name

This task is broad because the schema change ripples through `store.Branch`, `store.BranchRow`, `UpdateBranchStatus`, `DeleteBranch`, and every caller. We keep all changes in one task so the build stays green at commit time.

**Files:**
- Create: `store/migrations/0004_branches_name_pk.sql`
- Modify: `store/store.go`
- Modify: `store/store_test.go`
- Modify: `cmd/branch/branch.go`
- Modify: `cmd/issue/close.go`
- Modify: `cmd/issue/start.go`

- [ ] **Step 1: Write the failing migration test**

Append to `store/store_test.go`:

```go
func TestMigration0004BranchesNamePK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// PRAGMA user_version must have advanced to 4.
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version < 4 {
		t.Errorf("user_version = %d, want >= 4", version)
	}

	// The branches table must NOT have a uuid column and MUST have name as PK.
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(branches)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var hasUUID, namePK bool
	for rows.Next() {
		var (
			cid     int
			cname   string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cname == "uuid" {
			hasUUID = true
		}
		if cname == "name" && pk == 1 {
			namePK = true
		}
	}

	if hasUUID {
		t.Error("uuid column is still present after migration 0004")
	}
	if !namePK {
		t.Error("name should be PRIMARY KEY after migration 0004")
	}

	// enforce_merged_at trigger must still be in place: an UPDATE that sets
	// status_id = 2 without a merged_at must be rejected.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO issues (id_slug, title, status_id) VALUES (?, ?, ?)`,
		"ABC-1", "t", StatusIDInProgress,
	); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (name, issue_id, type, status_id) VALUES (?, (SELECT id FROM issues WHERE id_slug = ?), ?, ?)`,
		"ABC-1@feat@x", "ABC-1", "feat", StatusIDInProgress,
	); err != nil {
		t.Fatalf("insert branch: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE branches SET status_id = ? WHERE name = ?`,
		StatusIDMerged, "ABC-1@feat@x",
	); err == nil {
		t.Error("expected enforce_merged_at to reject UPDATE without merged_at")
	}
}
```

Add `"database/sql"` to the test file's imports if not already present.

- [ ] **Step 2: Run the test to confirm it fails**

```bash
mise exec -- go test ./store/... -run TestMigration0004BranchesNamePK -v
```

Expected: FAIL (no `0004_*.sql` file exists yet, so user_version stays at 3; uuid column still present).

- [ ] **Step 3: Write the migration**

Create `store/migrations/0004_branches_name_pk.sql`:

```sql
DROP TABLE branches;

CREATE TABLE branches (
    name       TEXT PRIMARY KEY,
    issue_id   INTEGER NOT NULL REFERENCES issues(id),
    type       TEXT NOT NULL,
    status_id  INTEGER NOT NULL DEFAULT 1 REFERENCES statuses(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at  DATETIME
);

CREATE TRIGGER enforce_merged_at
BEFORE UPDATE OF status_id ON branches
WHEN NEW.status_id = 2
BEGIN
    SELECT CASE WHEN NEW.merged_at IS NULL
        THEN RAISE(ABORT, 'merged_at must not be null when status is merged')
    END;
END;
```

- [ ] **Step 4: Update Go-side types and store methods**

Edit `store/store.go`.

Replace the `Branch` struct definition:

```go
// Branch represents a tracked branch record. The branch's full git ref
// name is its primary key.
type Branch struct {
	Name      string
	IssueID   int64
	Type      string
	StatusID  int64
	CreatedAt time.Time
	MergedAt  *time.Time
}
```

Replace the `BranchRow` struct definition:

```go
// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
	IssueID    int64        `json:"issue_id"`
	IssueSlug  string       `json:"issue_slug"`
	Title      string       `json:"title"`
	BranchName string       `json:"branch_name"`
	Type       string       `json:"type"`
	Status     BranchStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
}
```

Replace the body of `InsertIssueWithBranch`'s branch insert to drop the `uuid` column:

```go
	_, err = tx.ExecContext(ctx,
		`INSERT INTO branches (name, issue_id, type, status_id) VALUES (?, ?, ?, ?)`,
		branch.Name, issueID, branch.Type, branch.StatusID,
	)
	if err != nil {
		return fmt.Errorf("insert branch: %w", err)
	}
```

Replace `UpdateBranchStatus`:

```go
// UpdateBranchStatus updates a branch's status. mergedAt must be non-nil
// when statusID == 2 (merged); the enforce_merged_at trigger rejects nil.
func (s *Store) UpdateBranchStatus(ctx context.Context, name string, statusID int64, mergedAt *time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE branches SET status_id = ?, merged_at = ? WHERE name = ?`,
		statusID, mergedAt, name,
	)
	if err != nil {
		return fmt.Errorf("update branch status: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("update branch status: no branch with name %q", name)
	}

	return nil
}
```

Replace `DeleteBranch`:

```go
// DeleteBranch removes the branch record identified by name.
func (s *Store) DeleteBranch(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM branches WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("delete branch: no branch with name %q", name)
	}

	return nil
}
```

Update the two SELECT statements in `ListBranches` and `ListBranchesByIssueSlugs` — drop the leading `b.uuid` column and the matching `&r.UUID` argument from the `rows.Scan` call:

```go
	q := `
		SELECT i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id`
```

```go
		if err := rows.Scan(
			&r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}
```

Apply the same SELECT column-list and Scan changes to `ListBranchesByIssueSlugs`.

- [ ] **Step 5: Update store tests that reference UUID**

In `store/store_test.go`, search for every assertion or struct literal mentioning `UUID:`, `.UUID`, or a uuid column expectation. Replace with the new name-keyed shape. Where a test previously called `s.UpdateBranchStatus(ctx, "some-uuid", …)` or `s.DeleteBranch(ctx, "some-uuid")`, pass the branch name instead. Where a test built a `store.Branch{UUID: "...", Name: "..."}`, drop the `UUID:` field.

(The exact lines depend on the existing tests; the engineer should grep `UUID` in the file and update every occurrence.)

```bash
grep -n "UUID" store/store_test.go
```

- [ ] **Step 6: Update callers — `cmd/branch/branch.go`**

In `executePrune` (currently around lines 354 and 360), replace `result.toDelete[i].UUID` and `result.toMerge[i].UUID` with `result.toDelete[i].BranchName` and `result.toMerge[i].BranchName`:

```go
func executePrune(ctx context.Context, s *store.Store, result pruneResult) error {
	now := time.Now()

	for i := range result.toDelete {
		if err := s.DeleteBranch(ctx, result.toDelete[i].BranchName); err != nil {
			return fmt.Errorf("delete %q: %w", result.toDelete[i].BranchName, err)
		}
	}

	for i := range result.toMerge {
		if err := s.UpdateBranchStatus(ctx, result.toMerge[i].BranchName, 2, &now); err != nil {
			return fmt.Errorf("mark merged %q: %w", result.toMerge[i].BranchName, err)
		}
	}

	fmt.Printf("Pruned: %d deleted, %d marked merged.\n", len(result.toDelete), len(result.toMerge))

	return nil
}
```

- [ ] **Step 7: Update callers — `cmd/issue/close.go`**

In `updateStatus` (around line 287), replace `pickedBranch.UUID` with `pickedBranch.BranchName`:

```go
	if err := s.UpdateBranchStatus(cmd.Context(), pickedBranch.BranchName, store.StatusIDMerged, &now); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: update branch status: %v\n", err)
	}
```

- [ ] **Step 8: Update callers — `cmd/issue/start.go`**

In `persist` (around line 288), drop the `UUID:` field from the `store.Branch` literal:

```go
	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: store.StatusIDInProgress, TrackerType: trackerType},
		&store.Branch{Name: b.Name(), Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}
```

(`b.ID()` is still defined at this point; we drop it in Task 4.)

- [ ] **Step 9: Run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: build clean, all tests PASS (including the new `TestMigration0004BranchesNamePK`).

- [ ] **Step 10: Commit**

```bash
git add store/ cmd/branch/branch.go cmd/issue/close.go cmd/issue/start.go
git commit -m "refactor(store): drop branches.uuid, key on name"
```

---

## Task 4: `branch.New` accepts variant; rename id→variant; add `Variant()`; drop `ID()` and `shortUUID()`

After Task 3, nothing in the codebase still calls `b.ID()`. We can drop it cleanly and rename the field.

**Files:**
- Modify: `branch/branch.go`
- Modify: `branch/branch_test.go`
- Modify: `cmd/issue/start.go`

- [ ] **Step 1: Write the failing tests for the new constructor signature and `Variant()`**

Edit `branch/branch_test.go`.

Replace the body of `TestNew`'s "well-formed input produces a four-part branch name" subtest with a "3-part default" subtest:

```go
	t.Run("default (no variant) produces a three-part branch name", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Add OAuth Login", "")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		parts := strings.Split(b.Name(), "@")
		if len(parts) != 3 {
			t.Fatalf("Name produced %d parts, want 3: %q", len(parts), b.Name())
		}

		if parts[0] != "ABC-42" {
			t.Errorf("parts[0] = %q, want %q", parts[0], "ABC-42")
		}
		if parts[1] != "feat" {
			t.Errorf("parts[1] = %q, want %q", parts[1], "feat")
		}
		if parts[2] != "add-oauth-login" {
			t.Errorf("parts[2] = %q, want %q", parts[2], "add-oauth-login")
		}

		if got := b.Variant(); got != "" {
			t.Errorf("Variant() = %q, want empty", got)
		}
	})
```

Update the existing error-case subtests to use the 4-arg signature (pass `""` as the variant):

```go
	t.Run("all-punctuation title that produces empty slug returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "feat", "!!!", ""); err == nil {
			t.Error("expected error for all-punctuation title, got nil")
		}
	})

	t.Run("empty branch type returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "", "branch title", ""); err == nil {
			t.Error("expected error for empty type, got nil")
		}
	})
```

Add a new `TestNew_variant` function below:

```go
func TestNew_variant(t *testing.T) {
	t.Parallel()

	t.Run("variant produces a four-part branch name", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Add OAuth Login", "spike")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		want := "ABC-42@feat@add-oauth-login@spike"
		if got := b.Name(); got != want {
			t.Errorf("Name() = %q, want %q", got, want)
		}
		if got := b.Variant(); got != "spike" {
			t.Errorf("Variant() = %q, want %q", got, "spike")
		}
	})

	t.Run("variant is slugged", func(t *testing.T) {
		t.Parallel()

		b, err := New("ABC-42", "feat", "Title", "Approach B!")
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if got := b.Variant(); got != "approach-b" {
			t.Errorf("Variant() = %q, want %q", got, "approach-b")
		}
	})

	t.Run("variant that slugs to empty returns error", func(t *testing.T) {
		t.Parallel()

		if _, err := New("ABC-42", "feat", "Title", "!!!"); err == nil {
			t.Error("expected error for variant slugging to empty, got nil")
		}
	})
}
```

Update `TestParse` (which currently asserts `b.id`) — since we're about to rename the field, update the assertion:

```go
	t.Run("variant", func(t *testing.T) {
		if b.Variant() != "550e8400" {
			t.Errorf("Variant() = %q, want %q", b.Variant(), "550e8400")
		}
	})
```

(Replace the entire `t.Run("UUID", …)` subtest with the above.)

Update `TestParse_threeParts` (from Task 2) — replace the "trailing segment field is empty" subtest with:

```go
	t.Run("Variant() is empty", func(t *testing.T) {
		if got := b.Variant(); got != "" {
			t.Errorf("Variant() = %q, want empty", got)
		}
	})
```

Delete the existing `TestShortUUID` function entirely — `shortUUID` is being removed.

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
mise exec -- go test ./branch/... -v
```

Expected: compile error (new signature not yet present; `Variant()` undefined).

- [ ] **Step 3: Rewrite `branch/branch.go`**

Replace the file's content (header through end) with:

```go
package branch

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// MaxSlugLen caps the slugged title length. Combined with a typical
// issue ID and type, this keeps the full branch name comfortably under
// ~100 chars even when an operator adds --variant.
const MaxSlugLen = 50

var (
	reSpaces          = regexp.MustCompile(`\s+`)
	nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]+`)
	multiHyphen       = regexp.MustCompile(`-{2,}`)
)

// Branch is the parsed shape of a git-zf branch name.
//
//	3-part name: <issueID>@<type>@<slug>             — default
//	4-part name: <issueID>@<type>@<slug>@<variant>   — opt-in via --variant
//
// Legacy branches with an 8-hex random suffix parse as 4-part with the
// suffix exposed verbatim via Variant(). The system no longer distinguishes
// legacy hex from operator-supplied labels.
type Branch struct {
	issueID string
	btype   string
	title   string
	variant string
}

// New constructs a Branch.
//
//	variant == ""  → 3-part name
//	variant != ""  → 4-part name; the variant is slugged and rejected
//	                 if the result is empty.
func New(issueID, branchType, title, variant string) (*Branch, error) {
	if branchType == "" {
		return nil, errors.New("branch type is empty")
	}

	slug := Slug(title)
	if slug == "" {
		return nil, fmt.Errorf("branch.New: title %q produces an empty slug", title)
	}

	var v string
	if variant != "" {
		v = Slug(variant)
		if v == "" {
			return nil, fmt.Errorf("branch.New: variant %q produces an empty slug", variant)
		}
	}

	return &Branch{
		issueID: issueID,
		btype:   branchType,
		title:   slug,
		variant: v,
	}, nil
}

func (b Branch) Name() string {
	if b.variant == "" {
		return b.issueID + "@" + b.btype + "@" + b.title
	}

	return b.issueID + "@" + b.btype + "@" + b.title + "@" + b.variant
}

func (b Branch) IssueID() string { return b.issueID }
func (b Branch) Title() string   { return b.title }
func (b Branch) Type() string    { return b.btype }

// Variant returns the trailing segment of a 4-part name, or "" for a
// 3-part name. The returned value may be an operator-supplied label or
// a legacy random-hex suffix; the API treats both the same way.
func (b Branch) Variant() string { return b.variant }

// Slug normalises a free-form title for use as a branch-name segment.
// The result is lowercased, alphanumeric + single hyphens only, trimmed,
// and capped at MaxSlugLen with any trailing hyphen stripped.
func Slug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = reSpaces.ReplaceAllString(s, "-")
	s = nonAlphanumHyphen.ReplaceAllString(s, "")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if len(s) > MaxSlugLen {
		s = strings.TrimRight(s[:MaxSlugLen], "-")
	}

	return s
}

// Parse accepts 3- or 4-part branch names. For 3-part names, Variant() returns "".
func Parse(name string) (*Branch, error) {
	parts := strings.Split(name, "@")
	switch len(parts) {
	case 3:
		return fromParts(parts[0], parts[1], parts[2], ""), nil
	case 4:
		return fromParts(parts[0], parts[1], parts[2], parts[3]), nil
	default:
		return nil, fmt.Errorf("branch name %q: expected 3 or 4 parts, got %d", name, len(parts))
	}
}

// fromParts reconstructs a Branch from already-validated components.
// Unlike New, it does not re-slug the title or the variant.
func fromParts(issueID, branchType, title, variant string) *Branch {
	return &Branch{
		issueID: issueID,
		btype:   branchType,
		title:   title,
		variant: variant,
	}
}
```

(`crypto/rand` and `encoding/hex` imports are gone with `shortUUID`; the `errors` import stays for `errors.New`.)

- [ ] **Step 4: Update the call site in `cmd/issue/start.go`**

In `prepareBranch` (around line 127), pass `""` as the new fourth argument. (Task 7 will replace `""` with `flags.Variant`.)

```go
	b, err = branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, "")
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}
```

- [ ] **Step 5: Run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: build clean, all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add branch/ cmd/issue/start.go
git commit -m "feat(branch): make variant opt-in; drop random suffix from New()"
```

---

## Task 5: `git.Client.BranchExists`

**Files:**
- Modify: `git/git.go`
- Modify: `git/git_test.go`

- [ ] **Step 1: Write the failing test**

Append to `git/git_test.go`:

```go
func TestBranchExists(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := exec.Command("git", "init", "-q", dir).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	// Create an initial commit so HEAD is valid and branches can be created.
	for _, args := range [][]string{
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"commit", "--allow-empty", "-m", "init"},
		{"branch", "feat-x"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	c, err := NewClientFromPath(dir, nil)
	if err != nil {
		t.Fatalf("NewClientFromPath: %v", err)
	}

	t.Run("existing branch returns true", func(t *testing.T) {
		ok, err := c.BranchExists("feat-x")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !ok {
			t.Error("BranchExists(feat-x) = false, want true")
		}
	})

	t.Run("missing branch returns false", func(t *testing.T) {
		ok, err := c.BranchExists("does-not-exist")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if ok {
			t.Error("BranchExists(does-not-exist) = true, want false")
		}
	})
}
```

(If `NewClientFromPath` does not exist with that exact name, use whatever helper the existing `git/git_test.go` uses to construct a `*Client` against a temp repo. Grep the file first: `grep -n "func TestNew\|NewClient" git/git_test.go`.)

Add to the test file's imports if not already present:

```go
import (
	"os/exec"
)
```

- [ ] **Step 2: Run the test to confirm it fails**

```bash
mise exec -- go test ./git/... -run TestBranchExists -v
```

Expected: compile error (`BranchExists` undefined).

- [ ] **Step 3: Implement `BranchExists`**

Add to `git/git.go` (near `CreateBranch` around line 434):

```go
// BranchExists returns true if refs/heads/<name> resolves locally. It does
// not consult remotes — see resolveBranchConflict for the rationale (no
// fetch on the happy path of `issue start`).
func (c *Client) BranchExists(name string) (bool, error) {
	_, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), false)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return false, nil
	}

	return false, fmt.Errorf("lookup branch %q: %w", name, err)
}
```

(`plumbing` and `errors` are already imported in `git/git.go`. If `errors` is not, add it.)

- [ ] **Step 4: Run the test to confirm it passes**

```bash
mise exec -- go test ./git/... -run TestBranchExists -v
```

Expected: both subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add Client.BranchExists for local-ref collision check"
```

---

## Task 6: TUI form constructors for conflict picker and variant input

**Files:**
- Modify: `tui/branch.go`
- Modify: `tui/branch_test.go` (create if it does not exist)

- [ ] **Step 1: Check whether `tui/branch_test.go` exists**

```bash
ls tui/branch_test.go 2>/dev/null || echo "create it"
```

If missing, create `tui/branch_test.go` with a package header:

```go
package tui

import (
	"testing"

	"github.com/charmbracelet/huh"
)
```

(Adjust imports as needed once test bodies are written. The exact list will be apparent after Step 3.)

- [ ] **Step 2: Write failing smoke tests**

`huh.Group` does not expose its internal fields, so unit tests can only confirm the constructor returns a non-nil `*huh.Group`. That nil check is enough to catch a broken constructor signature; real behaviour validation lives in `cmd/issue/conflict_test.go` (Task 8), which tests the pure decision helper the TUI feeds into.

Append to `tui/branch_test.go`:

```go
func TestBranchConflictPicker_returnsGroup(t *testing.T) {
	t.Parallel()

	var action string
	g := BranchConflictPicker("ABC-42@feat@x", &action)

	if g == nil {
		t.Fatal("BranchConflictPicker returned nil")
	}
}

func TestVariantLabelInput_returnsGroup(t *testing.T) {
	t.Parallel()

	var label string
	g := VariantLabelInput(&label)

	if g == nil {
		t.Fatal("VariantLabelInput returned nil")
	}
}
```

(Drop the `"github.com/charmbracelet/huh"` import suggested in Step 1 if these are the only tests in the file — only `testing` is required.)

- [ ] **Step 3: Run the tests to confirm they fail**

```bash
mise exec -- go test ./tui/... -run "TestBranchConflictPicker|TestVariantLabelInput" -v
```

Expected: compile error (constructors undefined).

- [ ] **Step 4: Implement the form constructors**

Append to `tui/branch.go`:

```go
// BranchConflictPicker shows a 3-option picker when a deterministic branch
// name already exists locally. The selected value is stored in *action and
// is one of: "checkout", "variant", "abort".
func BranchConflictPicker(branchName string, action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Branch %q already exists.", branchName)).
			Options(
				huh.NewOption("Checkout the existing branch", "checkout"),
				huh.NewOption("Create a variant (you'll be asked for a label)", "variant"),
				huh.NewOption("Abort", "abort"),
			).
			Value(action),
	)
}

// VariantLabelInput prompts for a variant label and validates inline that
// the input slugs to a non-empty value.
func VariantLabelInput(label *string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Variant label (e.g. spike, approach-b):").
			Validate(func(s string) error {
				if branch.Slug(s) == "" {
					return errors.New("label is empty after slugging — use letters, digits, or hyphens")
				}

				return nil
			}).
			Value(label),
	)
}
```

Ensure `tui/branch.go`'s import block includes (add any that are missing):

```go
import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
)
```

- [ ] **Step 5: Run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: build clean, all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add tui/
git commit -m "feat(tui): add BranchConflictPicker and VariantLabelInput forms"
```

---

## Task 7: Plumb `--variant` flag through `IssueStartFlags`, `prepareBranch`, and both entry points

**Files:**
- Modify: `issue/issue.go`
- Modify: `cmd/issue/start.go`
- Modify: `cmd/branch/branch.go`

- [ ] **Step 1: Extend `IssueStartFlags`**

Edit `issue/issue.go`:

```go
type IssueStartFlags struct {
	TrackerFirst bool
	Variant      string
}
```

- [ ] **Step 2: Thread `flags` through `prepareBranch`, `createBranch`, `createWorktree`**

Edit `cmd/issue/start.go`.

Change `prepareBranch`'s signature and body to accept `flags` and pass the variant:

```go
func (i Issue) prepareBranch(
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) (b *branch.Branch, base string, err error) {
	b, err = branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	base = i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return nil, "", fmt.Errorf("detect base branch: %w", err)
		}
	}

	return b, base, nil
}
```

Change `createBranch`'s signature and its first internal call:

```go
func (i Issue) createBranch(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}

	// (conflict resolution wired in Task 8 — leave the rest of this function unchanged for now)
	...
}
```

Change `createWorktree`'s signature and first internal call the same way:

```go
func (i Issue) createWorktree(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}
	...
}
```

Update both call sites at the bottom of `RunIssueStart`:

```go
	if useWorktree {
		return i.createWorktree(cmd, t, pickedIssue, client, flags)
	}

	return i.createBranch(cmd, t, pickedIssue, client, flags)
```

- [ ] **Step 3: Register the `--variant` flag on `issue start`**

Replace `getStartCmd` and `startRunE` in `cmd/issue/start.go`:

```go
func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
		RunE: i.startRunE,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")

	return cmd
}

func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
	variant, err := cmd.Flags().GetString("variant")
	if err != nil {
		return fmt.Errorf("read --variant flag: %w", err)
	}

	return i.RunIssueStart(cmd, issue.IssueStartFlags{
		TrackerFirst: true,
		Variant:      variant,
	})
}
```

- [ ] **Step 4: Register the `--variant` flag on `branch new`**

Replace `newCmd` and `newRunE` in `cmd/branch/branch.go`:

```go
func (b Branch) newCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new branch (manual input)",
		Long:  "Enter issue details manually, then a named branch is created and checked out.",
		RunE:  b.newRunE,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")

	return cmd
}

// newRunE delegates to runIssueStart with manual-first (tracker toggle defaults to NO).
func (b Branch) newRunE(cmd *cobra.Command, _ []string) error {
	variant, err := cmd.Flags().GetString("variant")
	if err != nil {
		return fmt.Errorf("read --variant flag: %w", err)
	}

	ir := issuecmd.New(b.appConfig)
	if err := ir.RunIssueStart(cmd, issue.IssueStartFlags{TrackerFirst: false, Variant: variant}); err != nil {
		return fmt.Errorf("failed to run issueStart: %w", err)
	}

	return nil
}
```

- [ ] **Step 5: Build and run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: build clean, all tests PASS. An invalid value like `--variant=!!!` will surface at runtime via `branch.New` returning an error; we don't need a flag-level test here yet because Task 8 adds the integration path.

Smoke-test by hand if a repo is available:

```bash
./bin/git-zf issue start --variant=spike --help    # should show the flag in help output
```

- [ ] **Step 6: Commit**

```bash
git add issue/issue.go cmd/issue/start.go cmd/branch/branch.go
git commit -m "feat(issue,branch): add --variant flag for parallel branches per issue"
```

---

## Task 8: `resolveBranchConflict` orchestrator + pure decision helper

This task is the heart of the conflict UX. The TUI orchestration goes in `cmd/issue/conflict.go`; a pure decision helper goes in the same file and gets unit-tested in `cmd/issue/conflict_test.go`.

**Files:**
- Create: `cmd/issue/conflict.go`
- Create: `cmd/issue/conflict_test.go`
- Modify: `cmd/issue/start.go`

- [ ] **Step 1: Write failing tests for the pure decision helper**

Create `cmd/issue/conflict_test.go`:

```go
package issue

import (
	"testing"

	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

func samplePickedIssue() *issue.Issue {
	return &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-42",
			Subject: "Add OAuth Login",
		},
	}
}

func TestRebuildVariantBranch_valid(t *testing.T) {
	t.Parallel()

	b, err := rebuildVariantBranch(samplePickedIssue(), "spike")
	if err != nil {
		t.Fatalf("rebuildVariantBranch: %v", err)
	}

	if got, want := b.Name(), "ABC-42@feat@add-oauth-login@spike"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestRebuildVariantBranch_invalidLabel(t *testing.T) {
	t.Parallel()

	if _, err := rebuildVariantBranch(samplePickedIssue(), "!!!"); err == nil {
		t.Error("expected error for label slugging to empty, got nil")
	}
}

func TestRebuildVariantBranch_emptyLabel(t *testing.T) {
	t.Parallel()

	if _, err := rebuildVariantBranch(samplePickedIssue(), ""); err == nil {
		t.Error("expected error for empty label, got nil")
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
mise exec -- go test ./cmd/issue/... -run TestRebuildVariantBranch -v
```

Expected: compile error (`rebuildVariantBranch` undefined).

- [ ] **Step 3: Create `cmd/issue/conflict.go`**

```go
package issue

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tui"
)

// resolveBranchConflict checks whether b's name already exists locally and,
// if so, drives the operator through a picker:
//
//   - Checkout existing: switches to the colliding branch and returns (nil, nil).
//   - Create a variant:  prompts for a label, rebuilds the Branch with it, and
//                        loops back to the existence check (the variant itself
//                        could collide).
//   - Abort:              prints "Aborted." and returns (nil, nil).
//
// On a clean no-collision path, returns (b, nil). The caller must treat a
// (nil, nil) return as "stop here, do not create or persist".
func resolveBranchConflict(
	ctx context.Context,
	client *git.Client,
	b *branch.Branch,
	pickedIssue *issue.Issue,
) (*branch.Branch, error) {
	for {
		exists, err := client.BranchExists(b.Name())
		if err != nil {
			return nil, fmt.Errorf("check branch exists: %w", err)
		}

		if !exists {
			return b, nil
		}

		var action string
		if err := huh.NewForm(tui.BranchConflictPicker(b.Name(), &action)).Run(); err != nil {
			return nil, fmt.Errorf("conflict picker: %w", err)
		}

		switch action {
		case "checkout":
			if err := client.Checkout(ctx, b.Name()); err != nil {
				return nil, fmt.Errorf("checkout existing: %w", err)
			}
			fmt.Fprintf(client.IO().Out, "Switched to existing branch %q\n", b.Name())

			return nil, nil
		case "abort":
			fmt.Fprintln(client.IO().Out, "Aborted.")

			return nil, nil
		case "variant":
			var label string
			if err := huh.NewForm(tui.VariantLabelInput(&label)).Run(); err != nil {
				return nil, fmt.Errorf("variant input: %w", err)
			}

			newB, err := rebuildVariantBranch(pickedIssue, label)
			if err != nil {
				return nil, err
			}
			b = newB
		default:
			return nil, fmt.Errorf("unknown conflict action %q", action)
		}
	}
}

// rebuildVariantBranch is the pure half of the variant flow: it takes the
// operator's picked issue and the label they typed, and returns a freshly
// constructed *branch.Branch. Extracted so it can be unit-tested without
// the TUI.
func rebuildVariantBranch(pickedIssue *issue.Issue, label string) (*branch.Branch, error) {
	if label == "" {
		return nil, errors.New("variant label is empty")
	}

	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, label)
	if err != nil {
		return nil, fmt.Errorf("rebuild branch with variant: %w", err)
	}

	return b, nil
}
```

- [ ] **Step 4: Run the helper tests to confirm they pass**

```bash
mise exec -- go test ./cmd/issue/... -run TestRebuildVariantBranch -v
```

Expected: all three subtests PASS.

- [ ] **Step 5: Wire the resolver into `createBranch` and `createWorktree`**

Edit `cmd/issue/start.go`. In `createBranch`, after the existing `prepareBranch` call and before the `branchName := b.Name()` line:

```go
func (i Issue) createBranch(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}

	b, err = resolveBranchConflict(cmd.Context(), client, b, pickedIssue)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	// existing confirm + CreateBranch flow continues unchanged below…
```

In `createWorktree`, do the same insertion between `prepareBranch` and `branchName := b.Name()`:

```go
func (i Issue) createWorktree(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
	flags issue.IssueStartFlags,
) error {
	b, base, err := i.prepareBranch(pickedIssue, client, flags)
	if err != nil {
		return err
	}

	b, err = resolveBranchConflict(cmd.Context(), client, b, pickedIssue)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	// existing logic continues unchanged…
```

- [ ] **Step 6: Run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: build clean, all tests PASS.

- [ ] **Step 7: Manual smoke test (if a sandbox repo is available)**

In a scratch git repo with `.git-zf.toml`:

```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf issue start
# Create a branch (e.g. TEST-1 / feat / hello).
./bin/git-zf issue start
# Re-enter the same details. Confirm the conflict picker appears.
# Test each option: Checkout existing / Create a variant / Abort.
```

This step is observational — there is no automated assertion. If the flows behave per the spec ("Conflict handling" section), proceed.

- [ ] **Step 8: Commit**

```bash
git add cmd/issue/conflict.go cmd/issue/conflict_test.go cmd/issue/start.go
git commit -m "feat(issue): resolve branch-name collisions via interactive picker"
```

---

## Task 9: Documentation

This task is mandatory — implementation does not "ship" until each item in the spec's "Documentation updates" checklist is satisfied.

**Files:**
- Modify: `README.md`
- Modify: `ROADMAP.md` (only if it currently references the random suffix as a feature)
- Modify: `branch/branch.go` (docstrings — most already added in earlier tasks; verify completeness)
- Modify: `git/git.go` (docstring on `BranchExists`)
- Modify: `store/store.go` (docstring updates on `UpdateBranchStatus`, `DeleteBranch`)
- Modify: `cmd/issue/conflict.go` (docstrings — already added in Task 8; verify completeness)

- [ ] **Step 1: Replace the "Branch naming" section in `README.md`**

Locate the section around line 367 (`### Branch naming`). Replace:

```markdown
Branch names follow the format `{issue-id}@{type}@{slugified-title}@{short-uuid}`, e.g.:

```
ABC-42@feat@add-oauth-login@550e8400
```
```

with:

```markdown
Branch names follow the format `{issue-id}@{type}@{slugified-title}`, e.g.:

```
ABC-42@feat@add-oauth-login
```

The slugged title is capped at 50 characters (with any dangling trailing hyphen
stripped) so the full ref stays comfortably under 100 characters in the worst case.
```

- [ ] **Step 2: Add the "Parallel branches per issue" subsection to `README.md`**

Insert the following block immediately after the new "Branch naming" example block (before the existing "To override the base branch…" paragraph):

```markdown
#### Parallel branches per issue

The default branch name is unique per issue. When you genuinely need two branches
on the same issue (a throwaway spike, parallel approach exploration, etc.), pass
`--variant=<label>`:

```bash
git zf issue start --variant=spike
# → ABC-42@feat@add-oauth-login@spike

git zf branch new --variant=approach-b
# → ABC-42@feat@add-oauth-login@approach-b
```

The label is lowercased and slugged (letters, digits, and hyphens only); it must
be non-empty after slugging.

When a default name collides with an existing branch, an interactive prompt
offers three choices: **Checkout** the existing branch, **Create a variant**
(asks for a label), or **Abort**. Legacy branches with random-hex suffixes (from
git-zf versions before this change) keep parsing without any conversion needed.
```

- [ ] **Step 3: Mention `--variant` in the command summaries**

In `README.md`, locate the `**issue start**` paragraph around line 79 and append at the end:

```
Pass `--variant=<label>` to create a parallel branch on an issue that already has one (see [Parallel branches per issue](#parallel-branches-per-issue)).
```

Locate the `branch new` command paragraph (if not present nearby, search with `grep -n "branch new" README.md`) and append the same sentence.

- [ ] **Step 4: Update `ROADMAP.md` if it references the random suffix**

```bash
grep -n -i "uuid\|short-uuid\|random suffix\|short_uuid" ROADMAP.md
```

If any matches treat the random suffix as an intended feature, remove or update them. If no matches, this step is a no-op — confirm explicitly in the commit message.

- [ ] **Step 5: Verify Go docstrings**

```bash
mise exec -- go doc ./branch
mise exec -- go doc ./git    # check BranchExists is documented
mise exec -- go doc ./store  # check UpdateBranchStatus, DeleteBranch param names match
mise exec -- go doc ./cmd/issue  # check resolveBranchConflict is documented
```

For each function listed in the spec's "Go docstrings" checklist, confirm the doc comment exists and matches the function's actual behaviour:

- `branch.New` — full doc covering 3-part vs 4-part rules, slug enforcement, error conditions. (Added in Task 4.)
- `branch.Parse` — accepts 3 or 4 parts; no legacy/operator distinction. (Added in Task 4.)
- `branch.Variant` — returns `""` for 3-part names. (Added in Task 4.)
- `branch.Slug` — describes `MaxSlugLen` truncation and trailing-hyphen stripping. (Added in Task 4.)
- `branch.MaxSlugLen` — one-liner. (Added in Task 1.)
- `git.Client.BranchExists` — local-ref only. (Added in Task 5.)
- `store.UpdateBranchStatus`, `store.DeleteBranch` — parameter name `name` (not `uuid`) in the comment. (Edit if Task 3's comments still say `uuid`.)
- `cmd/issue.resolveBranchConflict` — flow summary and (nil, nil) return contract. (Added in Task 8.)
- `cmd/issue.rebuildVariantBranch` — pure helper note. (Added in Task 8.)

Any missing or inaccurate comment: fix inline.

- [ ] **Step 6: Verify the `--variant` flag help text**

```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf issue start --help | grep -A 1 variant
./bin/git-zf branch new --help | grep -A 1 variant
```

Expected (one for each command):

```
      --variant string   create a parallel branch for the same issue (e.g. --variant=spike)
```

If the spec calls for a longer multi-line help string and you've shipped the short version, update the `cmd.Flags().String("variant", "", "<text>")` call in `cmd/issue/start.go` and `cmd/branch/branch.go`. The current short form is acceptable.

- [ ] **Step 7: Commit**

```bash
git add README.md ROADMAP.md branch/ git/ store/ cmd/issue/
git commit -m "docs: deterministic branch naming, --variant flag, parallel branches"
```

---

## Final verification

- [ ] **Run the full suite cleanly**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
mise exec -- go vet ./...
```

Expected: clean build, all tests PASS, no vet warnings related to this change.

- [ ] **Run the project's linter if configured**

```bash
ls .golangci.yml .golangci.yaml 2>/dev/null && mise exec -- golangci-lint run ./...
```

If a golangci-lint config is present, address `wrapcheck` and `nlreturn` findings introduced by this change. (Existing pre-change findings are out of scope.)

- [ ] **Re-verify the spec checklist**

Open `docs/superpowers/specs/2026-05-24-deterministic-branch-naming-design.md` § "Documentation updates" and tick each checkbox locally (or mentally) — every item should be satisfied by the commits above.

---

## Notes for the executor

- **Worktree branches.** If you are executing this plan inside a git worktree created via `superpowers:using-git-worktrees`, the test repo at `t.TempDir()` will not interfere; both are isolated.
- **`mise exec` is non-negotiable.** Calling bare `go` may pick up a stale Go from `$PATH` and produce subtle test failures.
- **Do not skip the manual TUI smoke test in Task 8 Step 7.** The TUI paths are not exercised by automated tests — your eyes are the final layer of verification.
- **One commit per task.** Each commit message above is a single conventional-commit line; preserve the prefix (`feat`, `refactor`, `docs`) so the project's existing log style is maintained.
- **No `git add -A`.** Always pass explicit paths, as the project rules instruct.
- **The user runs git themselves.** When an executor encounters a "Commit" step, surface the exact commands to the user rather than running them autonomously.
