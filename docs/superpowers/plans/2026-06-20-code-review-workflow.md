# Code Review Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a full code review lifecycle in git-zf — `git zf review request/start/approve/reject/list/status/fetch/sync` — backed by SQLite + atomic git refs, with a review gate in `issue close` and sub-task parent support in `issue start`.

**Architecture:** Review state lives in two complementary stores: the local SQLite `reviews` table (rich history, fast queries) and a pushable git ref `refs/zf/reviews/<IssueID>` (JSON blob, cross-machine visibility via `git fetch`). All ref writes use CAS primitives (`git update-ref <new> <old>` + `git push --force-with-lease=<ref>:<sha>`) to prevent race conditions. The `issue close` flow gains a review preflight that fast-forwards the feature branch to include reviewer commits before merging.

**Tech Stack:** Go, Cobra, modernc SQLite (`database/sql`), system `git` binary (shelled out), `go-git` for ref resolution.

## Global Constraints

- Run all tests with: `mise exec -- go test ./...`
- Build binary with: `mise exec -- go build -o ./bin/git-zf .`
- Every `Test*` function wraps each assertion in `t.Run("descriptive name", func(t *testing.T) {...})` — no bare assertions at the top level.
- No new external dependencies beyond what is already in `go.mod`.
- Tracker sync calls are best-effort and non-fatal — network/API failure prints a warning but never aborts a git-zf operation.
- The review state in the git ref is always the source of truth; the SQLite store is a reconciled local cache.
- Force-push is never performed. All git operations are strictly additive.
- Module path: `github.com/piprim/git-zf`

### Branch naming convention

The project uses `@` as the separator: `{issue-id}@{type}@{slugified-title}` (e.g.
`42@feature@login-bug`). Review branches follow the same convention in a 2-part form —
no title slug: `{issue-id}@review` (e.g. `42@review`, `10.1@review`).

Review branches are not parsed by `branch.Parse()` (which requires 3–4 `@`-separated
parts). `cmd/review/` constructs and recognises them via
`strings.HasSuffix(name, "@review")`.

---

## File Map

### New files
| File | Responsibility |
|------|---------------|
| `store/migrations/0006_reviews_and_relations.sql` | Create `reviews` and `issue_relations` tables |
| `git/review_ref.go` | Atomic read/write/push/delete of `refs/zf/reviews/*` blobs |
| `git/review_ref_test.go` | Unit tests for ref operations |
| `cmd/review/review.go` | `Review` struct + `GetRootCmd()` wiring all subcommands |
| `cmd/review/deps.go` | `reviewDeps` struct + `buildReviewDeps()` constructor |
| `cmd/review/request.go` | `review request <IssueID>` — lock branch, push ref, install hook |
| `cmd/review/start.go` | `review start <IssueID>` — create `review/<IssueID>` from lock-time SHA |
| `cmd/review/approve.go` | `review approve <IssueID>` — set approved, push ref |
| `cmd/review/reject.go` | `review reject <IssueID>` — unlock, keep/delete review branch |
| `cmd/review/list.go` | `review list` — fetch refs, display in-review/approved issues |
| `cmd/review/status_cmd.go` | `review status <IssueID>` — show full review history |
| `cmd/review/fetch.go` | `review fetch` — fetch + reconcile all review refs |
| `cmd/review/sync.go` | `review sync <IssueID>` — merge-forward parent into drifted sub-task |
| `cmd/review/guard.go` | `review guard <branch>` — internal: pre-push hook check, exits 1 if locked |
| `cmd/review/review_e2e_test.go` | Full lifecycle E2E test (request→start→approve→close) |

### Modified files
| File | Change |
|------|--------|
| `store/store.go` | Add `ReviewRow`, `ReviewStatus`, `InsertReview`, `GetLatestReview`, `UpdateReviewStatus`, `ListReviews`, `InsertIssueRelation`, `GetParentIssue`, `ListChildIssues`, `ChildrenAllMerged` |
| `store/store_test.go` | Tests for all new store methods |
| `git/git.go` | Add `CommitsAhead`, `DeleteRemoteBranch` |
| `git/merge.go` | Add `MergeForward` |
| `git/git_test.go` | Tests for `CommitsAhead`, `DeleteRemoteBranch`, `MergeForward` |
| `cmd/issue/close.go` | Add `reviewPreflight` call at the top of `runClose` |
| `cmd/issue/close_e2e_test.go` | Add test: close blocked when in_review |
| `cmd/issue/start.go` | Add `--parent` flag; call `InsertIssueRelation` when set |
| `cmd/issue/start_e2e_test.go` | Add test: start with --parent populates issue_relations |
| `cmd/root.go` | Register `review.New(appConfig).GetRootCmd()` |
| `cmd/install/install.go` | Extract and export `InstallPrePushHook(gitDir string) error` |

---

## Task 1: Store — Migrations + New Types + Methods

**Files:**
- Create: `store/migrations/0006_reviews_and_relations.sql`
- Modify: `store/store.go` (add types + 8 methods)
- Modify: `store/store_test.go`

**Interfaces produced (consumed by Tasks 4–11):**
```go
type ReviewStatus string
const (
    ReviewStatusInReview         ReviewStatus = "in_review"
    ReviewStatusApproved         ReviewStatus = "approved"
    ReviewStatusChangesRequested ReviewStatus = "changes_requested"
)

type ReviewRow struct {
    ID         int64
    IssueSlug  string
    Round      int
    Reviewer   string
    Status     ReviewStatus
    HasCommits bool
    CreatedAt  time.Time
    ResolvedAt *time.Time
}

func (s *Store) InsertReview(ctx context.Context, issueSlug, reviewer string) (*ReviewRow, error)
func (s *Store) GetLatestReview(ctx context.Context, issueSlug string) (*ReviewRow, error)
func (s *Store) UpdateReviewStatus(ctx context.Context, id int64, status ReviewStatus, hasCommits bool) error
func (s *Store) ListReviews(ctx context.Context, issueSlug string) ([]ReviewRow, error)
func (s *Store) InsertIssueRelation(ctx context.Context, parentSlug, childSlug string) error
func (s *Store) GetParentIssue(ctx context.Context, childSlug string) (string, error)
func (s *Store) ListChildIssues(ctx context.Context, parentSlug string) ([]string, error)
func (s *Store) ChildrenAllMerged(ctx context.Context, parentSlug string) (bool, error)
```

- [ ] **Step 1: Write migration file**

Create `store/migrations/0006_reviews_and_relations.sql`:

```sql
CREATE TABLE reviews (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_slug  TEXT    NOT NULL,
    round       INTEGER NOT NULL DEFAULT 1,
    reviewer    TEXT    NOT NULL DEFAULT '',
    status      TEXT    NOT NULL,
    has_commits INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME
);

CREATE TABLE issue_relations (
    parent_issue_slug TEXT NOT NULL,
    child_issue_slug  TEXT NOT NULL,
    PRIMARY KEY (parent_issue_slug, child_issue_slug)
);
```

- [ ] **Step 2: Write failing tests for new store methods**

Add to `store/store_test.go` (after the existing tests):

```go
func TestReviewStore(t *testing.T) {
    ctx := context.Background()
    s := openTestStore(t, ctx)

    // Seed an issue so InsertReview can reference a real slug.
    issue := &Issue{IDSlug: "42", Title: "test issue", StatusID: StatusIDInProgress}
    branch := &Branch{Name: "feature/42", IssueID: 0, Type: "feature", StatusID: StatusIDInProgress}
    if err := s.InsertIssueWithBranch(ctx, issue, branch); err != nil {
        t.Fatalf("seed issue: %v", err)
    }

    t.Run("InsertReview returns round 1 for new issue", func(t *testing.T) {
        row, err := s.InsertReview(ctx, "42", "alice <alice@example.com>")
        if err != nil {
            t.Fatalf("InsertReview: %v", err)
        }
        if row.Round != 1 {
            t.Errorf("round: got %d, want 1", row.Round)
        }
        if row.Status != ReviewStatusInReview {
            t.Errorf("status: got %q, want %q", row.Status, ReviewStatusInReview)
        }
    })

    t.Run("GetLatestReview returns most recent row", func(t *testing.T) {
        got, err := s.GetLatestReview(ctx, "42")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if got == nil {
            t.Fatal("got nil, want review row")
        }
        if got.IssueSlug != "42" {
            t.Errorf("IssueSlug: got %q, want %q", got.IssueSlug, "42")
        }
    })

    t.Run("GetLatestReview returns nil for unknown issue", func(t *testing.T) {
        got, err := s.GetLatestReview(ctx, "999")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if got != nil {
            t.Errorf("expected nil, got %+v", got)
        }
    })

    t.Run("UpdateReviewStatus transitions to approved", func(t *testing.T) {
        row, _ := s.GetLatestReview(ctx, "42")
        if err := s.UpdateReviewStatus(ctx, row.ID, ReviewStatusApproved, false); err != nil {
            t.Fatalf("UpdateReviewStatus: %v", err)
        }
        updated, _ := s.GetLatestReview(ctx, "42")
        if updated.Status != ReviewStatusApproved {
            t.Errorf("status: got %q, want %q", updated.Status, ReviewStatusApproved)
        }
        if updated.ResolvedAt == nil {
            t.Error("resolved_at should be set")
        }
    })

    t.Run("InsertReview increments round on second call", func(t *testing.T) {
        row, err := s.InsertReview(ctx, "42", "bob <bob@example.com>")
        if err != nil {
            t.Fatalf("InsertReview round 2: %v", err)
        }
        if row.Round != 2 {
            t.Errorf("round: got %d, want 2", row.Round)
        }
    })

    t.Run("ListReviews returns all rounds newest first", func(t *testing.T) {
        rows, err := s.ListReviews(ctx, "42")
        if err != nil {
            t.Fatalf("ListReviews: %v", err)
        }
        if len(rows) != 2 {
            t.Fatalf("len: got %d, want 2", len(rows))
        }
        if rows[0].Round != 2 {
            t.Errorf("first row round: got %d, want 2", rows[0].Round)
        }
    })
}

func TestIssueRelationsStore(t *testing.T) {
    ctx := context.Background()
    s := openTestStore(t, ctx)

    t.Run("InsertIssueRelation links parent to child", func(t *testing.T) {
        if err := s.InsertIssueRelation(ctx, "10", "10.1"); err != nil {
            t.Fatalf("InsertIssueRelation: %v", err)
        }
        if err := s.InsertIssueRelation(ctx, "10", "10.2"); err != nil {
            t.Fatalf("InsertIssueRelation second child: %v", err)
        }
    })

    t.Run("GetParentIssue returns parent slug", func(t *testing.T) {
        parent, err := s.GetParentIssue(ctx, "10.1")
        if err != nil {
            t.Fatalf("GetParentIssue: %v", err)
        }
        if parent != "10" {
            t.Errorf("got %q, want %q", parent, "10")
        }
    })

    t.Run("GetParentIssue returns empty string for root issue", func(t *testing.T) {
        parent, err := s.GetParentIssue(ctx, "99")
        if err != nil {
            t.Fatalf("GetParentIssue: %v", err)
        }
        if parent != "" {
            t.Errorf("expected empty, got %q", parent)
        }
    })

    t.Run("ListChildIssues returns all children", func(t *testing.T) {
        children, err := s.ListChildIssues(ctx, "10")
        if err != nil {
            t.Fatalf("ListChildIssues: %v", err)
        }
        if len(children) != 2 {
            t.Fatalf("len: got %d, want 2", len(children))
        }
    })

    t.Run("ChildrenAllMerged returns false when no children merged", func(t *testing.T) {
        // seed issue 10.1 with in_progress branch
        iss := &Issue{IDSlug: "10.1", Title: "sub", StatusID: StatusIDInProgress}
        br := &Branch{Name: "feature/10.1", Type: "feature", StatusID: StatusIDInProgress}
        _ = s.InsertIssueWithBranch(ctx, iss, br)

        ok, err := s.ChildrenAllMerged(ctx, "10")
        if err != nil {
            t.Fatalf("ChildrenAllMerged: %v", err)
        }
        if ok {
            t.Error("expected false — child not yet merged")
        }
    })
}
```

- [ ] **Step 3: Run tests to verify they fail**

```
mise exec -- go test ./store/... -run "TestReviewStore|TestIssueRelationsStore" -v
```
Expected: compile errors or FAIL — methods not yet defined.

- [ ] **Step 4: Add ReviewStatus type and ReviewRow struct to store/store.go**

Add after the existing `BranchStatus` block:

```go
// ReviewStatus is the typed status of a review round.
type ReviewStatus string

const (
    ReviewStatusInReview         ReviewStatus = "in_review"
    ReviewStatusApproved         ReviewStatus = "approved"
    ReviewStatusChangesRequested ReviewStatus = "changes_requested"
)

// ReviewRow is one round of review from the reviews table.
type ReviewRow struct {
    ID         int64
    IssueSlug  string
    Round      int
    Reviewer   string
    Status     ReviewStatus
    HasCommits bool
    CreatedAt  time.Time
    ResolvedAt *time.Time
}
```

- [ ] **Step 5: Implement InsertReview**

Add to `store/store.go`:

```go
// InsertReview opens a new review round for issueSlug. The round number is
// one greater than the highest existing round for that slug (or 1 if none).
// The SELECT MAX + INSERT is wrapped in a transaction so the round counter
// is incremented atomically — safe even if background tooling ever calls
// this concurrently on the same SQLite file.
func (s *Store) InsertReview(ctx context.Context, issueSlug, reviewer string) (*ReviewRow, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, fmt.Errorf("begin tx: %w", err)
    }
    defer func() { _ = tx.Rollback() }()

    var maxRound int
    if err := tx.QueryRowContext(ctx,
        `SELECT COALESCE(MAX(round), 0) FROM reviews WHERE issue_slug = ?`, issueSlug,
    ).Scan(&maxRound); err != nil {
        return nil, fmt.Errorf("get max round: %w", err)
    }

    round := maxRound + 1
    now := time.Now().UTC().Format(time.RFC3339)

    res, err := tx.ExecContext(ctx,
        `INSERT INTO reviews (issue_slug, round, reviewer, status, created_at)
         VALUES (?, ?, ?, ?, ?)`,
        issueSlug, round, reviewer, string(ReviewStatusInReview), now,
    )
    if err != nil {
        return nil, fmt.Errorf("insert review: %w", err)
    }

    id, err := res.LastInsertId()
    if err != nil {
        return nil, fmt.Errorf("last insert id: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return nil, fmt.Errorf("commit tx: %w", err)
    }

    createdAt, _ := parseSQLiteTime(now)

    return &ReviewRow{
        ID:        id,
        IssueSlug: issueSlug,
        Round:     round,
        Reviewer:  reviewer,
        Status:    ReviewStatusInReview,
        CreatedAt: createdAt,
    }, nil
}
```

- [ ] **Step 6: Implement GetLatestReview**

```go
// GetLatestReview returns the most recent review row for issueSlug, or nil if none.
func (s *Store) GetLatestReview(ctx context.Context, issueSlug string) (*ReviewRow, error) {
    row := s.db.QueryRowContext(ctx,
        `SELECT id, issue_slug, round, reviewer, status, has_commits, created_at, resolved_at
         FROM reviews WHERE issue_slug = ? ORDER BY round DESC LIMIT 1`,
        issueSlug,
    )
    return scanReviewRow(row)
}

func scanReviewRow(row *sql.Row) (*ReviewRow, error) {
    var r ReviewRow
    var createdAtStr string
    var resolvedAtStr *string

    err := row.Scan(&r.ID, &r.IssueSlug, &r.Round, &r.Reviewer,
        (*string)(&r.Status), &r.HasCommits, &createdAtStr, &resolvedAtStr)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, fmt.Errorf("scan review row: %w", err)
    }

    t, parseErr := parseSQLiteTime(createdAtStr)
    if parseErr != nil {
        return nil, fmt.Errorf("parse created_at: %w", parseErr)
    }
    r.CreatedAt = t

    if resolvedAtStr != nil {
        rt, parseErr := parseSQLiteTime(*resolvedAtStr)
        if parseErr != nil {
            return nil, fmt.Errorf("parse resolved_at: %w", parseErr)
        }
        r.ResolvedAt = &rt
    }

    return &r, nil
}
```

Note: `scanReviewRow` takes `*sql.Row` — add `"errors"` to the store's imports if not already present.

- [ ] **Step 7: Implement UpdateReviewStatus**

```go
// UpdateReviewStatus sets the status and resolved_at of an existing review row.
// has_commits records whether the reviewer pushed commits to the review branch.
func (s *Store) UpdateReviewStatus(ctx context.Context, id int64, status ReviewStatus, hasCommits bool) error {
    now := time.Now().UTC().Format(time.RFC3339)
    hasCommitsInt := 0
    if hasCommits {
        hasCommitsInt = 1
    }

    res, err := s.db.ExecContext(ctx,
        `UPDATE reviews SET status = ?, has_commits = ?, resolved_at = ? WHERE id = ?`,
        string(status), hasCommitsInt, now, id,
    )
    if err != nil {
        return fmt.Errorf("update review status: %w", err)
    }

    n, err := res.RowsAffected()
    if err != nil {
        return fmt.Errorf("rows affected: %w", err)
    }
    if n == 0 {
        return fmt.Errorf("update review status: no review with id %d", id)
    }

    return nil
}
```

- [ ] **Step 8: Implement ListReviews**

```go
// ListReviews returns all review rounds for issueSlug, newest first.
func (s *Store) ListReviews(ctx context.Context, issueSlug string) ([]ReviewRow, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT id, issue_slug, round, reviewer, status, has_commits, created_at, resolved_at
         FROM reviews WHERE issue_slug = ? ORDER BY round DESC`,
        issueSlug,
    )
    if err != nil {
        return nil, fmt.Errorf("list reviews query: %w", err)
    }
    defer func() { _ = rows.Close() }()

    var result []ReviewRow
    for rows.Next() {
        var r ReviewRow
        var createdAtStr string
        var resolvedAtStr *string

        if err := rows.Scan(&r.ID, &r.IssueSlug, &r.Round, &r.Reviewer,
            (*string)(&r.Status), &r.HasCommits, &createdAtStr, &resolvedAtStr); err != nil {
            return nil, fmt.Errorf("scan review row: %w", err)
        }

        t, parseErr := parseSQLiteTime(createdAtStr)
        if parseErr != nil {
            return nil, fmt.Errorf("parse created_at: %w", parseErr)
        }
        r.CreatedAt = t

        if resolvedAtStr != nil {
            rt, _ := parseSQLiteTime(*resolvedAtStr)
            r.ResolvedAt = &rt
        }

        result = append(result, r)
    }

    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("iterate reviews: %w", err)
    }

    if result == nil {
        result = []ReviewRow{}
    }
    return result, nil
}
```

- [ ] **Step 9: Implement issue_relations methods**

```go
// InsertIssueRelation records a parent-child relationship between two issue slugs.
// Silently succeeds if the relation already exists (idempotent).
func (s *Store) InsertIssueRelation(ctx context.Context, parentSlug, childSlug string) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT OR IGNORE INTO issue_relations (parent_issue_slug, child_issue_slug) VALUES (?, ?)`,
        parentSlug, childSlug,
    )
    if err != nil {
        return fmt.Errorf("insert issue relation: %w", err)
    }
    return nil
}

// GetParentIssue returns the parent issue slug for childSlug, or "" if it has no parent.
func (s *Store) GetParentIssue(ctx context.Context, childSlug string) (string, error) {
    var parent string
    err := s.db.QueryRowContext(ctx,
        `SELECT parent_issue_slug FROM issue_relations WHERE child_issue_slug = ?`,
        childSlug,
    ).Scan(&parent)
    if errors.Is(err, sql.ErrNoRows) {
        return "", nil
    }
    if err != nil {
        return "", fmt.Errorf("get parent issue: %w", err)
    }
    return parent, nil
}

// ListChildIssues returns the slugs of all direct children of parentSlug.
func (s *Store) ListChildIssues(ctx context.Context, parentSlug string) ([]string, error) {
    rows, err := s.db.QueryContext(ctx,
        `SELECT child_issue_slug FROM issue_relations WHERE parent_issue_slug = ?`,
        parentSlug,
    )
    if err != nil {
        return nil, fmt.Errorf("list children query: %w", err)
    }
    defer func() { _ = rows.Close() }()

    var result []string
    for rows.Next() {
        var slug string
        if err := rows.Scan(&slug); err != nil {
            return nil, fmt.Errorf("scan child slug: %w", err)
        }
        result = append(result, slug)
    }
    if err := rows.Err(); err != nil {
        return nil, fmt.Errorf("iterate children: %w", err)
    }
    return result, nil
}

// ChildrenAllMerged reports whether every child issue of parentSlug has a
// branch with BranchStatusMerged. Returns true when parentSlug has no children.
func (s *Store) ChildrenAllMerged(ctx context.Context, parentSlug string) (bool, error) {
    children, err := s.ListChildIssues(ctx, parentSlug)
    if err != nil {
        return false, err
    }
    if len(children) == 0 {
        return true, nil
    }

    for _, child := range children {
        var status string
        err := s.db.QueryRowContext(ctx,
            `SELECT st.name FROM branches b
             JOIN issues i ON b.issue_id = i.id
             JOIN statuses st ON b.status_id = st.id
             WHERE i.id_slug = ?`,
            child,
        ).Scan(&status)
        if errors.Is(err, sql.ErrNoRows) {
            return false, nil // child branch not started — not merged
        }
        if err != nil {
            return false, fmt.Errorf("check child %q status: %w", child, err)
        }
        if BranchStatus(status) != BranchStatusMerged {
            return false, nil
        }
    }
    return true, nil
}
```

- [ ] **Step 10: Run tests**

```
mise exec -- go test ./store/... -run "TestReviewStore|TestIssueRelationsStore" -v
```
Expected: all subtests PASS.

- [ ] **Step 11: Run full test suite to check for regressions**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add store/migrations/0006_reviews_and_relations.sql store/store.go store/store_test.go
git commit -m "feat(store): add reviews + issue_relations tables and store methods"
```

---

## Task 2: Git Ref Operations (`git/review_ref.go`)

**Files:**
- Create: `git/review_ref.go`
- Create: `git/review_ref_test.go`

**Interfaces produced (consumed by Tasks 4–8):**
```go
type ReviewRef struct {
    Status     string `json:"status"`
    Round      int    `json:"round"`
    FeatureSHA string `json:"feature_sha"`
    CreatedAt  string `json:"created_at"` // RFC3339
}

func (c *Client) WriteReviewRef(ctx context.Context, issueID string, ref ReviewRef, oldSHA string) (newSHA string, err error)
func (c *Client) ReadReviewRef(ctx context.Context, issueID string) (ref *ReviewRef, currentSHA string, err error)
func (c *Client) FetchReviewRefs(ctx context.Context) error
func (c *Client) PushReviewRef(ctx context.Context, issueID, expectedOldSHA string) error
func (c *Client) DeleteReviewRef(ctx context.Context, issueID string) error
```

- [ ] **Step 1: Write failing tests**

Create `git/review_ref_test.go`:

```go
package git_test

import (
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "testing"

    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/internal/pkg"
)

func newTestRepo(t *testing.T) (dir string, client *git.Client) {
    t.Helper()
    dir = t.TempDir()

    run := func(args ...string) {
        t.Helper()
        cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }

    run("init")
    run("config", "user.email", "test@example.com")
    run("config", "user.name", "Test")
    run("commit", "--allow-empty", "-m", "init")

    c, err := git.NewClientAt(&pkg.IO{
        In:  os.Stdin,
        Out: os.Stdout,
        Err: os.Stderr,
    }, dir)
    if err != nil {
        t.Fatalf("NewClientAt: %v", err)
    }

    return dir, c
}

func TestReviewRef(t *testing.T) {
    ctx := context.Background()
    _, client := newTestRepo(t)

    ref := git.ReviewRef{
        Status:     "in_review",
        Round:      1,
        FeatureSHA: "abc1234",
        CreatedAt:  "2026-06-20T10:00:00Z",
    }

    t.Run("ReadReviewRef returns nil for non-existent ref", func(t *testing.T) {
        got, sha, err := client.ReadReviewRef(ctx, "42")
        if err != nil {
            t.Fatalf("ReadReviewRef: %v", err)
        }
        if got != nil {
            t.Errorf("expected nil, got %+v", got)
        }
        if sha != "" {
            t.Errorf("expected empty sha, got %q", sha)
        }
    })

    var writtenSHA string

    t.Run("WriteReviewRef creates ref when oldSHA is empty", func(t *testing.T) {
        sha, err := client.WriteReviewRef(ctx, "42", ref, "")
        if err != nil {
            t.Fatalf("WriteReviewRef: %v", err)
        }
        if sha == "" {
            t.Fatal("expected non-empty sha")
        }
        writtenSHA = sha
    })

    t.Run("ReadReviewRef returns written ref", func(t *testing.T) {
        got, sha, err := client.ReadReviewRef(ctx, "42")
        if err != nil {
            t.Fatalf("ReadReviewRef: %v", err)
        }
        if got == nil {
            t.Fatal("expected ref, got nil")
        }
        if got.Status != "in_review" {
            t.Errorf("status: got %q, want %q", got.Status, "in_review")
        }
        if sha != writtenSHA {
            t.Errorf("sha mismatch: got %q, want %q", sha, writtenSHA)
        }
    })

    t.Run("WriteReviewRef with wrong oldSHA fails", func(t *testing.T) {
        updated := ref
        updated.Status = "approved"
        _, err := client.WriteReviewRef(ctx, "42", updated, "wrongsha")
        if err == nil {
            t.Error("expected CAS failure, got nil error")
        }
    })

    t.Run("WriteReviewRef with correct oldSHA succeeds", func(t *testing.T) {
        updated := ref
        updated.Status = "approved"
        newSHA, err := client.WriteReviewRef(ctx, "42", updated, writtenSHA)
        if err != nil {
            t.Fatalf("WriteReviewRef update: %v", err)
        }
        if newSHA == writtenSHA {
            t.Error("new sha should differ from old sha")
        }
    })

    t.Run("DeleteReviewRef removes local ref", func(t *testing.T) {
        _, currentSHA, _ := client.ReadReviewRef(ctx, "42")
        if err := client.DeleteReviewRef(ctx, "42"); err != nil {
            t.Fatalf("DeleteReviewRef: %v", err)
        }
        got, _, err := client.ReadReviewRef(ctx, "42")
        if err != nil {
            t.Fatalf("ReadReviewRef after delete: %v", err)
        }
        if got != nil {
            t.Errorf("expected nil after delete, got %+v (sha was %s)", got, currentSHA)
        }
    })
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
mise exec -- go test ./git/... -run TestReviewRef -v
```
Expected: compile error — `ReviewRef` type and methods not defined.

- [ ] **Step 3: Implement git/review_ref.go**

Create `git/review_ref.go`:

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

const reviewRefPrefix = "refs/zf/reviews/"

// ReviewRef is the JSON payload stored as a git blob at refs/zf/reviews/<IssueID>.
type ReviewRef struct {
    Status     string `json:"status"`
    Round      int    `json:"round"`
    FeatureSHA string `json:"feature_sha"`
    CreatedAt  string `json:"created_at"`
}

// WriteReviewRef atomically writes a ReviewRef as a git blob and updates
// refs/zf/reviews/<issueID> using CAS. oldSHA must be the current ref SHA
// (pass "" for the first write). Returns the new blob SHA.
func (c *Client) WriteReviewRef(ctx context.Context, issueID string, ref ReviewRef, oldSHA string) (string, error) {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return "", fmt.Errorf("working tree root: %w", err)
    }

    data, err := json.Marshal(ref)
    if err != nil {
        return "", fmt.Errorf("marshal review ref: %w", err)
    }

    // Write blob object.
    hashCmd := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "-w", "--stdin")
    hashCmd.Stdin = bytes.NewReader(data)
    out, err := hashCmd.Output()
    if err != nil {
        return "", fmt.Errorf("git hash-object: %w", err)
    }
    newSHA := strings.TrimSpace(string(out))

    // Atomic CAS update of the ref.
    refName := reviewRefPrefix + issueID
    args := []string{"-C", root, "update-ref", refName, newSHA}
    if oldSHA != "" {
        args = append(args, oldSHA)
    }

    updateCmd := exec.CommandContext(ctx, "git", args...)
    if out, err := updateCmd.CombinedOutput(); err != nil {
        return "", fmt.Errorf("git update-ref (CAS): %w: %s", err, out)
    }

    return newSHA, nil
}

// ReadReviewRef reads the ReviewRef for issueID from the local ref store.
// Returns (nil, "", nil) when the ref does not exist.
// The returned currentSHA is suitable as oldSHA in the next WriteReviewRef call.
func (c *Client) ReadReviewRef(ctx context.Context, issueID string) (*ReviewRef, string, error) {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return nil, "", fmt.Errorf("working tree root: %w", err)
    }

    refName := reviewRefPrefix + issueID

    // Resolve ref to SHA.
    showCmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--hash", refName)
    shaOut, err := showCmd.Output()
    if err != nil {
        // show-ref exits 1 when ref does not exist — not an error.
        return nil, "", nil
    }
    currentSHA := strings.TrimSpace(string(shaOut))

    // Read blob contents.
    catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", currentSHA)
    blobOut, err := catCmd.Output()
    if err != nil {
        return nil, "", fmt.Errorf("git cat-file blob %s: %w", currentSHA, err)
    }

    var ref ReviewRef
    if err := json.Unmarshal(blobOut, &ref); err != nil {
        return nil, "", fmt.Errorf("unmarshal review ref: %w", err)
    }

    return &ref, currentSHA, nil
}

// FetchReviewRefs fetches refs/zf/reviews/* from the remote into the local
// ref namespace. No-op when no remote is configured.
func (c *Client) FetchReviewRefs(ctx context.Context) error {
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

    refspec := reviewRefPrefix + "*:" + reviewRefPrefix + "*"
    if err := c.runInteractive(ctx, root, "fetch", remote, refspec); err != nil {
        return fmt.Errorf("fetch review refs: %w", err)
    }

    return nil
}

// PushReviewRef pushes refs/zf/reviews/<issueID> to the remote using
// --force-with-lease to prevent overwriting a concurrently updated ref.
// expectedOldSHA is the SHA the caller last observed locally; pass "" to
// allow any previous value (first push).
func (c *Client) PushReviewRef(ctx context.Context, issueID, expectedOldSHA string) error {
    remote, err := c.Remote()
    if err != nil {
        return fmt.Errorf("resolve remote: %w", err)
    }
    if remote == "" {
        return nil // local-only repo; no push needed
    }

    root, err := c.WorkingTreeRoot()
    if err != nil {
        return fmt.Errorf("working tree root: %w", err)
    }

    refName := reviewRefPrefix + issueID
    lease := refName
    if expectedOldSHA != "" {
        lease = refName + ":" + expectedOldSHA
    }

    if err := c.runInteractive(ctx, root,
        "push", "--force-with-lease="+lease, remote, refName,
    ); err != nil {
        return fmt.Errorf("push review ref %s: %w", issueID, err)
    }

    return nil
}

// DeleteReviewRef deletes refs/zf/reviews/<issueID> locally. If a remote is
// configured, also deletes it there. Errors deleting the remote ref are
// non-fatal (logged as warnings).
func (c *Client) DeleteReviewRef(ctx context.Context, issueID string) error {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return fmt.Errorf("working tree root: %w", err)
    }

    refName := reviewRefPrefix + issueID

    // Delete local ref.
    delCmd := exec.CommandContext(ctx, "git", "-C", root, "update-ref", "-d", refName)
    if out, err := delCmd.CombinedOutput(); err != nil {
        return fmt.Errorf("delete local review ref %s: %w: %s", refName, err, out)
    }

    // Delete remote ref (best-effort).
    remote, _ := c.Remote()
    if remote != "" {
        _ = c.runInteractive(ctx, root, "push", remote, "--delete", refName)
    }

    return nil
}
```

- [ ] **Step 4: Run tests**

```
mise exec -- go test ./git/... -run TestReviewRef -v
```
Expected: all subtests PASS.

- [ ] **Step 5: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add git/review_ref.go git/review_ref_test.go
git commit -m "feat(git): atomic review ref read/write/push/delete for refs/zf/reviews/*"
```

---

## Task 3: Git Client Additions (`CommitsAhead`, `DeleteRemoteBranch`, `MergeForward`)

**Files:**
- Modify: `git/git.go` (add `CommitsAhead`, `DeleteRemoteBranch`)
- Modify: `git/merge.go` (add `MergeForward`)
- Modify: `git/git_test.go` (add tests)

**Interfaces produced (consumed by Tasks 6, 7, 9, 10):**
```go
func (c *Client) CommitsAhead(ctx context.Context, branchName, baseBranch string) (int, error)
func (c *Client) DeleteRemoteBranch(ctx context.Context, branchName string) error
func (c *Client) MergeForward(ctx context.Context, sourceBranch, targetBranch string) error
```

- [ ] **Step 1: Write failing tests**

Add to `git/git_test.go` (find the existing test file and append):

```go
func TestCommitsAhead(t *testing.T) {
    ctx := context.Background()
    dir, client := newTestGitRepo(t) // reuse the existing helper in git_test.go

    run := func(args ...string) {
        t.Helper()
        cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }

    t.Run("returns 0 when branches are equal", func(t *testing.T) {
        n, err := client.CommitsAhead(ctx, "master", "master")
        if err != nil {
            t.Fatalf("CommitsAhead: %v", err)
        }
        if n != 0 {
            t.Errorf("got %d, want 0", n)
        }
    })

    t.Run("returns count of extra commits", func(t *testing.T) {
        run("checkout", "-b", "feature/99")
        run("commit", "--allow-empty", "-m", "commit A")
        run("commit", "--allow-empty", "-m", "commit B")

        n, err := client.CommitsAhead(ctx, "feature/99", "master")
        if err != nil {
            t.Fatalf("CommitsAhead: %v", err)
        }
        if n != 2 {
            t.Errorf("got %d, want 2", n)
        }
        run("checkout", "master")
    })
}

func TestMergeForward(t *testing.T) {
    ctx := context.Background()
    dir, client := newTestGitRepo(t)

    run := func(args ...string) {
        t.Helper()
        cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }

    t.Run("merges source into target without force-push", func(t *testing.T) {
        run("checkout", "-b", "integration")
        run("commit", "--allow-empty", "-m", "integration base")
        run("checkout", "-b", "subtask")
        run("commit", "--allow-empty", "-m", "subtask work")
        run("checkout", "integration")
        run("commit", "--allow-empty", "-m", "another integration commit")

        if err := client.MergeForward(ctx, "integration", "subtask"); err != nil {
            t.Fatalf("MergeForward: %v", err)
        }

        // After merge, subtask should be ahead of integration.
        n, err := client.CommitsAhead(ctx, "subtask", "integration")
        if err != nil {
            t.Fatalf("CommitsAhead post-merge: %v", err)
        }
        if n == 0 {
            t.Error("expected subtask to have merge commit ahead of integration")
        }
    })
}
```

- [ ] **Step 2: Run to verify failure**

```
mise exec -- go test ./git/... -run "TestCommitsAhead|TestMergeForward" -v
```
Expected: FAIL — methods not defined.

- [ ] **Step 3: Implement CommitsAhead and DeleteRemoteBranch in git/git.go**

Add to `git/git.go`:

```go
// CommitsAhead returns the number of commits in branchName that are not reachable
// from baseBranch. Uses `git rev-list --count <baseBranch>..<branchName>`.
func (c *Client) CommitsAhead(ctx context.Context, branchName, baseBranch string) (int, error) {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return 0, fmt.Errorf("working tree root: %w", err)
    }

    cmd := exec.CommandContext(ctx, "git", "-C", root,
        "rev-list", "--count", baseBranch+".."+branchName)
    out, err := cmd.Output()
    if err != nil {
        return 0, fmt.Errorf("rev-list --count %s..%s: %w", baseBranch, branchName, err)
    }

    var n int
    if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &n); err != nil {
        return 0, fmt.Errorf("parse rev-list count %q: %w", strings.TrimSpace(string(out)), err)
    }

    return n, nil
}

// DeleteRemoteBranch deletes branchName on the configured remote.
// No-op when no remote is configured.
func (c *Client) DeleteRemoteBranch(ctx context.Context, branchName string) error {
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

    if err := c.runInteractive(ctx, root, "push", remote, "--delete", branchName); err != nil {
        return fmt.Errorf("delete remote branch %s: %w", branchName, err)
    }

    return nil
}
```

- [ ] **Step 4: Implement MergeForward in git/merge.go**

Add to `git/merge.go`:

```go
// MergeForward checks out targetBranch and runs `git merge --no-edit sourceBranch`,
// creating a merge commit. Used by `review sync` to integrate a parent integration
// branch into a drifted sub-task branch without rewriting history.
// Returns a wrapped error on conflict; caller should run AbortMerge to clean up.
func (c *Client) MergeForward(ctx context.Context, sourceBranch, targetBranch string) error {
    if err := c.Checkout(ctx, targetBranch); err != nil {
        return fmt.Errorf("checkout %s: %w", targetBranch, err)
    }

    root, err := c.WorkingTreeRoot()
    if err != nil {
        return fmt.Errorf("working tree root: %w", err)
    }

    if err := c.runInteractive(ctx, root, "merge", "--no-edit", sourceBranch); err != nil {
        return fmt.Errorf("merge --no-edit %s: %w", sourceBranch, err)
    }

    return nil
}
```

- [ ] **Step 5: Run tests**

```
mise exec -- go test ./git/... -run "TestCommitsAhead|TestMergeForward" -v
```
Expected: PASS.

- [ ] **Step 6: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add git/git.go git/merge.go git/git_test.go
git commit -m "feat(git): add CommitsAhead, DeleteRemoteBranch, MergeForward"
```

---

## Task 4: `review request` Command

**Files:**
- Create: `cmd/review/review.go`
- Create: `cmd/review/deps.go`
- Create: `cmd/review/request.go`

**Interfaces consumed:** Task 1 (`InsertReview`, `GetLatestReview`), Task 2 (`WriteReviewRef`, `PushReviewRef`)

- [ ] **Step 1: Create cmd/review/review.go**

```go
package review

import (
    "github.com/piprim/git-zf/config"
    "github.com/spf13/cobra"
)

// Review is the `git zf review` command group.
type Review struct {
    appConfig *config.AppConfig
}

// New creates a Review command group.
func New(appConfig *config.AppConfig) Review {
    return Review{appConfig: appConfig}
}

// GetRootCmd returns the `review` cobra command with all subcommands registered.
func (r Review) GetRootCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "review",
        Short: "Manage the code review lifecycle for an issue branch",
    }

    cmd.AddCommand(
        r.getRequestCmd(),
        r.getStartCmd(),
        r.getApproveCmd(),
        r.getRejectCmd(),
        r.getListCmd(),
        r.getStatusCmd(),
        r.getFetchCmd(),
        r.getSyncCmd(),
        r.getGuardCmd(),
    )

    return cmd
}
```

- [ ] **Step 2: Create cmd/review/deps.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/piprim/git-zf/config"
    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/internal/pkg"
    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

// reviewDeps bundles the long-lived dependencies shared by all review subcommands.
type reviewDeps struct {
    client *git.Client
    store  *store.Store
    cfg    *config.AppConfig
}

func buildReviewDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig) (reviewDeps, error) {
    s, err := store.OpenRepo(ctx)
    if err != nil {
        return reviewDeps{}, fmt.Errorf("open store: %w", err)
    }

    client, err := git.NewClient(&pkg.IO{
        In:  cmd.InOrStdin(),
        Out: cmd.OutOrStdout(),
        Err: cmd.ErrOrStderr(),
    })
    if err != nil {
        _ = s.Close()
        return reviewDeps{}, fmt.Errorf("not a git repository: %w", err)
    }

    if cfg.Branch.Remote != "" {
        client.SetRemote(cfg.Branch.Remote)
    }

    return reviewDeps{client: client, store: s, cfg: cfg}, nil
}
```

- [ ] **Step 3: Create cmd/review/request.go**

```go
package review

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

const prePushHookScript = `#!/bin/sh
# git-zf: warn when pushing to a branch locked for code review
while IFS=' ' read -r local_ref local_sha remote_ref remote_sha; do
    branch=$(echo "$local_ref" | sed 's|^refs/heads/||')
    if ! git zf review guard "$branch" 2>&1; then
        exit 1
    fi
done
exit 0
`

func (r Review) getRequestCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "request <IssueID>",
        Short: "Submit an issue branch for code review (locks the branch)",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewRequest(ctx, deps, args[0])
        },
    }
}

func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug string) error {
    // Guard: refuse if already in_review.
    latest, err := deps.store.GetLatestReview(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("get latest review: %w", err)
    }
    if latest != nil && latest.Status == store.ReviewStatusInReview {
        return fmt.Errorf("issue %q is already in review (round %d) — awaiting reviewer decision", issueSlug, latest.Round)
    }

    // Resolve the feature branch and its current HEAD.
    branches, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
    if err != nil {
        return fmt.Errorf("list branches: %w", err)
    }

    var featureBranch string
    for _, b := range branches {
        if b.IssueSlug == issueSlug {
            featureBranch = b.BranchName
            break
        }
    }
    if featureBranch == "" {
        return fmt.Errorf("no in-progress branch found for issue %q", issueSlug)
    }

    featureSHA, err := deps.client.ResolveRef("refs/heads/" + featureBranch)
    if err != nil {
        return fmt.Errorf("resolve feature branch HEAD: %w", err)
    }

    // Delete any stale review branch from a previous rejected round.
    reviewBranch := issueSlug + "@review"
    if exists, _ := deps.client.BranchExists(reviewBranch); exists {
        if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
            fmt.Fprintf(deps.client.IO().Err, "warning: delete stale %s: %v\n", reviewBranch, err)
        }
        _ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
    }

    // Create review record in store.
    reviewer := "" // not yet assigned — recorded when reviewer runs `review start`
    reviewRow, err := deps.store.InsertReview(ctx, issueSlug, reviewer)
    if err != nil {
        return fmt.Errorf("insert review: %w", err)
    }

    // Write and push review ref.
    ref := git.ReviewRef{
        Status:     string(store.ReviewStatusInReview),
        Round:      reviewRow.Round,
        FeatureSHA: featureSHA.String(),
        CreatedAt:  time.Now().UTC().Format(time.RFC3339),
    }

    newSHA, err := deps.client.WriteReviewRef(ctx, issueSlug, ref, "")
    if err != nil {
        return fmt.Errorf("write review ref: %w", err)
    }

    if err := deps.client.PushReviewRef(ctx, issueSlug, newSHA); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
    }

    // Install pre-push hook.
    gitDir, err := deps.client.GitDir()
    if err == nil {
        if err := installPrePushHook(gitDir); err != nil {
            fmt.Fprintf(deps.client.IO().Err, "warning: install pre-push hook: %v\n", err)
        }
    }

    fmt.Fprintf(deps.client.IO().Out,
        "Issue %q is now in review (round %d). Branch %q is locked.\n"+
            "Share with your reviewer: git fetch && git zf review start %s\n",
        issueSlug, reviewRow.Round, featureBranch, issueSlug)

    return nil
}

func installPrePushHook(gitDir string) error {
    hookPath := filepath.Join(gitDir, "hooks", "pre-push")

    if info, err := os.Stat(hookPath); err == nil {
        existing, readErr := os.ReadFile(hookPath) //nolint:gosec
        if readErr == nil && len(existing) > 0 && string(existing) != prePushHookScript {
            // Foreign hook — leave content alone but ensure it is executable.
            if info.Mode()&0111 == 0 {
                if chmodErr := os.Chmod(hookPath, info.Mode()|0755); chmodErr != nil {
                    return fmt.Errorf("chmod existing pre-push hook: %w", chmodErr)
                }
            }
            return nil
        }
    }

    //nolint:gosec // hook script is a compile-time constant
    return os.WriteFile(hookPath, []byte(prePushHookScript), 0755)
}
```

- [ ] **Step 4: Build to check compilation**

```
mise exec -- go build ./cmd/review/...
```
Expected: PASS (no compile errors).

- [ ] **Step 5: Commit**

```bash
git add cmd/review/review.go cmd/review/deps.go cmd/review/request.go
git commit -m "feat(review): add review request command — locks branch, pushes ref, installs hook"
```

---

## Task 5: `review start` Command

**Files:**
- Create: `cmd/review/start.go`

**Interfaces consumed:** Task 2 (`FetchReviewRefs`, `ReadReviewRef`), `git.Client.CreateBranch`

- [ ] **Step 1: Create cmd/review/start.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

func (r Review) getStartCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "start <IssueID>",
        Short: "Begin reviewing an issue (creates <IssueID>/review branch from the locked snapshot)",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewStart(ctx, deps, args[0])
        },
    }
}

func runReviewStart(ctx context.Context, deps reviewDeps, issueSlug string) error {
    // Fetch latest review refs from remote so we see up-to-date lock state.
    if err := deps.client.FetchReviewRefs(ctx); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
    }

    ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("read review ref: %w", err)
    }
    if ref == nil {
        return fmt.Errorf("no review found for issue %q — has the developer run `git zf review request %s`?", issueSlug, issueSlug)
    }
    if ref.Status != string(store.ReviewStatusInReview) {
        return fmt.Errorf("issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
    }

    reviewBranch := issueSlug + "@review"
    if exists, _ := deps.client.BranchExists(reviewBranch); exists {
        return fmt.Errorf("branch %q already exists — review already started", reviewBranch)
    }

    // Create review branch from the exact feature HEAD at lock time.
    featureSHA := ref.FeatureSHA
    root, err := deps.client.WorkingTreeRoot()
    if err != nil {
        return fmt.Errorf("working tree root: %w", err)
    }

    // Create branch at specific SHA using git checkout -b.
    if err := deps.client.RunGitAt(ctx, root, "checkout", "-b", reviewBranch, featureSHA); err != nil {
        return fmt.Errorf("create review branch at %s: %w", featureSHA, err)
    }

    // Record reviewer identity in local store (best-effort — reviewer may not have the issue).
    reviewer, _ := deps.client.ConfigUser()
    latest, err := deps.store.GetLatestReview(ctx, issueSlug)
    if err == nil && latest != nil && latest.Reviewer == "" {
        _ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, reviewer)
    }

    fmt.Fprintf(deps.client.IO().Out,
        "Created branch %q at %s (round %d).\n"+
            "Review the code, then run:\n"+
            "  git zf review approve %s\n"+
            "  git zf review reject %s\n",
        reviewBranch, featureSHA[:7], ref.Round, issueSlug, issueSlug)

    return nil
}
```

Note: `RunGitAt` and `ConfigUser` are new thin helpers needed on `git.Client`. Also `UpdateReviewerIdentity` is a new store method. Add these:

**Add to `git/git.go`:**
```go
// RunGitAt runs an arbitrary git command in dir with the client's IO streams.
// Exported for use by review subcommands that need git operations not covered
// by the higher-level methods.
func (c *Client) RunGitAt(ctx context.Context, dir string, args ...string) error {
    return c.runInteractive(ctx, dir, args...)
}

// ConfigUser returns the git config user identity as "Name <email>".
// Returns an empty string when not configured.
func (c *Client) ConfigUser() (string, error) {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return "", fmt.Errorf("working tree root: %w", err)
    }

    nameCmd := exec.CommandContext(context.Background(), "git", "-C", root, "config", "user.name")
    nameOut, err := nameCmd.Output()
    if err != nil {
        return "", nil
    }

    emailCmd := exec.CommandContext(context.Background(), "git", "-C", root, "config", "user.email")
    emailOut, _ := emailCmd.Output()

    name := strings.TrimSpace(string(nameOut))
    email := strings.TrimSpace(string(emailOut))
    if email != "" {
        return name + " <" + email + ">", nil
    }
    return name, nil
}
```

**Add to `store/store.go`:**
```go
// UpdateReviewerIdentity sets the reviewer field on an existing review row.
// Used by `review start` when the reviewer's identity is known at start time.
func (s *Store) UpdateReviewerIdentity(ctx context.Context, id int64, reviewer string) error {
    _, err := s.db.ExecContext(ctx,
        `UPDATE reviews SET reviewer = ? WHERE id = ?`, reviewer, id)
    if err != nil {
        return fmt.Errorf("update reviewer identity: %w", err)
    }
    return nil
}
```

- [ ] **Step 2: Build to check compilation**

```
mise exec -- go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/review/start.go git/git.go store/store.go
git commit -m "feat(review): add review start command — creates review branch at lock-time SHA"
```

---

## Task 6: `review approve` Command

**Files:**
- Create: `cmd/review/approve.go`

**Interfaces consumed:** Task 1 (`GetLatestReview`, `UpdateReviewStatus`), Task 2 (`ReadReviewRef`, `WriteReviewRef`, `PushReviewRef`), Task 3 (`CommitsAhead`)

- [ ] **Step 1: Create cmd/review/approve.go**

```go
package review

import (
    "context"
    "fmt"
    "time"

    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

func (r Review) getApproveCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "approve <IssueID>",
        Short: "Approve a review — signals the branch is ready to close",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewApprove(ctx, deps, args[0])
        },
    }
}

func runReviewApprove(ctx context.Context, deps reviewDeps, issueSlug string) error {
    latest, err := deps.store.GetLatestReview(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("get latest review: %w", err)
    }
    if latest == nil {
        return fmt.Errorf("no review found for issue %q", issueSlug)
    }
    if latest.Status != store.ReviewStatusInReview {
        return fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
    }

    // Detect whether reviewer pushed commits to <issueSlug>/review.
    reviewBranch := issueSlug + "@review"
    featureBranch := ""
    branches, _ := deps.store.ListBranches(ctx, store.BranchStatusAll)
    for _, b := range branches {
        if b.IssueSlug == issueSlug {
            featureBranch = b.BranchName
            break
        }
    }

    hasCommits := false
    if featureBranch != "" {
        if exists, _ := deps.client.BranchExists(reviewBranch); exists {
            n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
            if countErr == nil && n > 0 {
                hasCommits = true
            }
        }
    }

    // Update store.
    if err := deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusApproved, hasCommits); err != nil {
        return fmt.Errorf("update review status: %w", err)
    }

    // Update and push review ref atomically.
    currentRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("read review ref: %w", err)
    }

    newRef := git.ReviewRef{
        Status:     string(store.ReviewStatusApproved),
        Round:      latest.Round,
        FeatureSHA: func() string {
            if currentRef != nil {
                return currentRef.FeatureSHA
            }
            return ""
        }(),
        CreatedAt: time.Now().UTC().Format(time.RFC3339),
    }

    newSHA, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA)
    if err != nil {
        return fmt.Errorf("write review ref: %w", err)
    }

    if err := deps.client.PushReviewRef(ctx, issueSlug, newSHA); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
    }

    msg := fmt.Sprintf("Issue %q approved (round %d).", issueSlug, latest.Round)
    if hasCommits {
        msg += fmt.Sprintf(" Reviewer pushed commits to %s — they will be incorporated on close.", reviewBranch)
    }
    fmt.Fprintln(deps.client.IO().Out, msg)
    fmt.Fprintf(deps.client.IO().Out, "Developer can now run: git zf issue close\n")

    return nil
}
```

- [ ] **Step 2: Build**

```
mise exec -- go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/review/approve.go
git commit -m "feat(review): add review approve command"
```

---

## Task 7: `review reject` Command

**Files:**
- Create: `cmd/review/reject.go`

**Interfaces consumed:** Task 1 (`GetLatestReview`, `UpdateReviewStatus`), Task 2 (`ReadReviewRef`, `WriteReviewRef`, `PushReviewRef`), Task 3 (`CommitsAhead`, `DeleteLocalBranch`, `DeleteRemoteBranch`)

- [ ] **Step 1: Create cmd/review/reject.go**

```go
package review

import (
    "context"
    "fmt"
    "time"

    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

func (r Review) getRejectCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "reject <IssueID>",
        Short: "Request changes on a review — unlocks the branch for the next iteration",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewReject(ctx, deps, args[0])
        },
    }
}

func runReviewReject(ctx context.Context, deps reviewDeps, issueSlug string) error {
    latest, err := deps.store.GetLatestReview(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("get latest review: %w", err)
    }
    if latest == nil {
        return fmt.Errorf("no review found for issue %q", issueSlug)
    }
    if latest.Status != store.ReviewStatusInReview {
        return fmt.Errorf("issue %q is not in review (current status: %s)", issueSlug, latest.Status)
    }

    // Detect reviewer commits on <issueSlug>/review.
    reviewBranch := issueSlug + "@review"
    featureBranch := ""
    branches, _ := deps.store.ListBranches(ctx, store.BranchStatusAll)
    for _, b := range branches {
        if b.IssueSlug == issueSlug {
            featureBranch = b.BranchName
            break
        }
    }

    hasCommits := false
    reviewBranchExists := false
    if exists, _ := deps.client.BranchExists(reviewBranch); exists {
        reviewBranchExists = true
        if featureBranch != "" {
            n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
            if countErr == nil && n > 0 {
                hasCommits = true
            }
        }
    }

    // Update store — status returns to changes_requested (== in_progress for next round).
    if err := deps.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusChangesRequested, hasCommits); err != nil {
        return fmt.Errorf("update review status: %w", err)
    }

    // Update and push review ref.
    currentRef, currentSHA, _ := deps.client.ReadReviewRef(ctx, issueSlug)
    newRef := git.ReviewRef{
        Status: string(store.ReviewStatusChangesRequested),
        Round:  latest.Round,
        CreatedAt: time.Now().UTC().Format(time.RFC3339),
    }
    if currentRef != nil {
        newRef.FeatureSHA = currentRef.FeatureSHA
    }

    newSHA, err := deps.client.WriteReviewRef(ctx, issueSlug, newRef, currentSHA)
    if err != nil {
        return fmt.Errorf("write review ref: %w", err)
    }
    if err := deps.client.PushReviewRef(ctx, issueSlug, newSHA); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
    }

    // Handle review branch: keep if has reviewer commits, delete if empty.
    if reviewBranchExists && !hasCommits {
        if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
            fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
        }
        _ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
        fmt.Fprintf(deps.client.IO().Out,
            "Issue %q: changes requested (round %d). Branch %q unlocked.\n",
            issueSlug, latest.Round, featureBranch)
    } else if hasCommits {
        fmt.Fprintf(deps.client.IO().Out,
            "Issue %q: changes requested (round %d). Branch %q unlocked.\n"+
                "%s has %d reviewer commit(s). Inspect with:\n"+
                "  git log %s..%s\n"+
                "Cherry-pick, adapt, or discard as needed, then:\n"+
                "  git zf review request %s\n",
            issueSlug, latest.Round, featureBranch,
            reviewBranch, func() int {
                n, _ := deps.client.CommitsAhead(ctx, reviewBranch, featureBranch)
                return n
            }(),
            featureBranch, reviewBranch, issueSlug)
    } else {
        fmt.Fprintf(deps.client.IO().Out,
            "Issue %q: changes requested (round %d). Branch %q unlocked.\n",
            issueSlug, latest.Round, featureBranch)
    }

    return nil
}
```

- [ ] **Step 2: Build**

```
mise exec -- go build ./...
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/review/reject.go
git commit -m "feat(review): add review reject command — unlocks branch, preserves reviewer commits"
```

---

## Task 8: `review list`, `review status`, `review fetch` Commands

**Files:**
- Create: `cmd/review/list.go`
- Create: `cmd/review/status_cmd.go`
- Create: `cmd/review/fetch.go`

- [ ] **Step 1: Create cmd/review/list.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

func (r Review) getListCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "list",
        Short: "List all issues currently in review or approved",
        RunE: func(cmd *cobra.Command, _ []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewList(ctx, deps)
        },
    }
}

func runReviewList(ctx context.Context, deps reviewDeps) error {
    // Fetch latest state from remote.
    if err := deps.client.FetchReviewRefs(ctx); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
    }

    branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
    if err != nil {
        return fmt.Errorf("list branches: %w", err)
    }

    printed := 0
    for _, b := range branches {
        latest, err := deps.store.GetLatestReview(ctx, b.IssueSlug)
        if err != nil || latest == nil {
            continue
        }
        if latest.Status != store.ReviewStatusInReview && latest.Status != store.ReviewStatusApproved {
            continue
        }

        fmt.Fprintf(deps.client.IO().Out, "%-12s  %-20s  round %-2d  %s\n",
            b.IssueSlug, b.BranchName, latest.Round, latest.Status)
        printed++
    }

    if printed == 0 {
        fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
    }

    return nil
}
```

- [ ] **Step 2: Create cmd/review/status_cmd.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
)

func (r Review) getStatusCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "status <IssueID>",
        Short: "Show the full review history for an issue",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewStatus(ctx, deps, args[0])
        },
    }
}

func runReviewStatus(ctx context.Context, deps reviewDeps, issueSlug string) error {
    if err := deps.client.FetchReviewRefs(ctx); err != nil {
        fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
    }

    rows, err := deps.store.ListReviews(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("list reviews: %w", err)
    }

    if len(rows) == 0 {
        fmt.Fprintf(deps.client.IO().Out, "No review history for issue %q.\n", issueSlug)
        return nil
    }

    fmt.Fprintf(deps.client.IO().Out, "Review history for issue %q:\n", issueSlug)
    for _, row := range rows {
        resolved := "pending"
        if row.ResolvedAt != nil {
            resolved = row.ResolvedAt.Format("2006-01-02 15:04")
        }
        commits := ""
        if row.HasCommits {
            commits = " [reviewer pushed commits]"
        }
        fmt.Fprintf(deps.client.IO().Out, "  Round %-2d  %-20s  reviewer: %-30s  opened: %s  resolved: %s%s\n",
            row.Round, row.Status, row.Reviewer,
            row.CreatedAt.Format("2006-01-02 15:04"), resolved, commits)
    }

    return nil
}
```

- [ ] **Step 3: Create cmd/review/fetch.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
)

func (r Review) getFetchCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "fetch",
        Short: "Fetch all review refs from remote and reconcile local store",
        RunE: func(cmd *cobra.Command, _ []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewFetch(ctx, deps)
        },
    }
}

func runReviewFetch(ctx context.Context, deps reviewDeps) error {
    if err := deps.client.FetchReviewRefs(ctx); err != nil {
        return fmt.Errorf("fetch review refs: %w", err)
    }
    fmt.Fprintln(deps.client.IO().Out, "Review refs fetched.")
    return nil
}
```

- [ ] **Step 4: Build**

```
mise exec -- go build ./...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/review/list.go cmd/review/status_cmd.go cmd/review/fetch.go
git commit -m "feat(review): add review list, status, fetch commands"
```

---

## Task 9: `review sync` + `review guard` Commands

**Files:**
- Create: `cmd/review/sync.go`
- Create: `cmd/review/guard.go`

- [ ] **Step 1: Create cmd/review/sync.go**

```go
package review

import (
    "context"
    "fmt"

    "github.com/spf13/cobra"
)

func (r Review) getSyncCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "sync <IssueID>",
        Short: "Merge the parent integration branch into a drifted sub-task branch",
        Args:  cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                return err
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewSync(ctx, deps, args[0])
        },
    }
}

func runReviewSync(ctx context.Context, deps reviewDeps, issueSlug string) error {
    parentSlug, err := deps.store.GetParentIssue(ctx, issueSlug)
    if err != nil {
        return fmt.Errorf("get parent issue: %w", err)
    }
    if parentSlug == "" {
        return fmt.Errorf("issue %q has no parent — sync is only for sub-tasks", issueSlug)
    }

    // Resolve branch names.
    var childBranch, parentBranch string
    branches, err := deps.store.ListBranches(ctx, "")
    if err != nil {
        return fmt.Errorf("list branches: %w", err)
    }
    for _, b := range branches {
        if b.IssueSlug == issueSlug {
            childBranch = b.BranchName
        }
        if b.IssueSlug == parentSlug {
            parentBranch = b.BranchName
        }
    }
    if childBranch == "" {
        return fmt.Errorf("no branch found for sub-task issue %q", issueSlug)
    }
    if parentBranch == "" {
        return fmt.Errorf("no branch found for parent issue %q", parentSlug)
    }

    // Check drift.
    behind, err := deps.client.CommitsAhead(ctx, parentBranch, childBranch)
    if err != nil {
        return fmt.Errorf("check drift: %w", err)
    }
    if behind == 0 {
        fmt.Fprintf(deps.client.IO().Out, "Branch %q is already up to date with %q.\n",
            childBranch, parentBranch)
        return nil
    }

    fmt.Fprintf(deps.client.IO().Out, "Merging %q (%d new commit(s)) into %q...\n",
        parentBranch, behind, childBranch)

    if err := deps.client.MergeForward(ctx, parentBranch, childBranch); err != nil {
        _ = deps.client.AbortMerge(ctx)
        return fmt.Errorf("merge %s into %s: %w (conflicts detected — merge aborted)", parentBranch, childBranch, err)
    }

    fmt.Fprintf(deps.client.IO().Out, "Branch %q synced with %q.\n", childBranch, parentBranch)
    return nil
}
```

- [ ] **Step 2: Create cmd/review/guard.go**

```go
package review

import (
    "context"
    "fmt"
    "strings"

    "github.com/piprim/git-zf/store"
    "github.com/spf13/cobra"
)

// getGuardCmd returns the internal `review guard <branch>` command used by the
// pre-push hook. It exits 1 with a message when the branch is locked for review.
// This command is intentionally hidden from the help output.
func (r Review) getGuardCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:    "guard <branch>",
        Short:  "Internal: check whether a branch is locked for review (used by pre-push hook)",
        Hidden: true,
        Args:   cobra.ExactArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            ctx := cmd.Context()
            deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
            if err != nil {
                // If we can't open the store, allow the push (fail-open).
                return nil
            }
            defer func() { _ = deps.store.Close() }()

            return runReviewGuard(ctx, deps, args[0])
        },
    }

    return cmd
}

func runReviewGuard(ctx context.Context, deps reviewDeps, branchName string) error {
    // Ignore <IssueID>/review branches — the reviewer can always push there.
    if strings.HasSuffix(branchName, "@review") {
        return nil
    }

    // Look up the branch in the store to get the issue slug.
    branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
    if err != nil {
        return nil // fail-open on store error
    }

    var issueSlug string
    for _, b := range branches {
        if b.BranchName == branchName {
            issueSlug = b.IssueSlug
            break
        }
    }
    if issueSlug == "" {
        return nil // not a tracked branch — allow
    }

    // Check review ref (fast, local).
    ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
    if err != nil || ref == nil {
        return nil
    }

    if ref.Status == string(store.ReviewStatusInReview) {
        return fmt.Errorf(
            "push blocked: branch %q is locked for code review (issue %q, round %d).\n"+
                "Wait for the reviewer to approve or reject before pushing.\n"+
                "To bypass (not recommended): git push --no-verify",
            branchName, issueSlug, ref.Round)
    }

    return nil
}
```

- [ ] **Step 3: Build**

```
mise exec -- go build ./...
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/review/sync.go cmd/review/guard.go
git commit -m "feat(review): add review sync (sub-task drift) and guard (pre-push hook check)"
```

---

## Task 10: `issue close` Review Preflight

**Files:**
- Modify: `cmd/issue/close.go`
- Modify: `cmd/issue/close_e2e_test.go`

- [ ] **Step 1: Write failing test**

Add to `cmd/issue/close_e2e_test.go`:

```go
func TestClose_BlockedWhenInReview(t *testing.T) {
    ctx := context.Background()
    repo := newCloseTestRepo(t, ctx)  // reuse existing helper

    // Seed a branch and insert an in-review review record.
    issue := &store.Issue{IDSlug: "55", Title: "review test", StatusID: store.StatusIDInProgress}
    branch := &store.Branch{Name: "feature/55", Type: "feature", StatusID: store.StatusIDInProgress}
    if err := repo.store.InsertIssueWithBranch(ctx, issue, branch); err != nil {
        t.Fatalf("seed: %v", err)
    }
    if _, err := repo.store.InsertReview(ctx, "55", "alice"); err != nil {
        t.Fatalf("insert review: %v", err)
    }

    deps := closeDeps{
        client: repo.client,
        store:  repo.store,
        cfg:    repo.cfg,
    }

    prompter := &scriptedPrompter{
        pickBranch: func(_ []store.BranchRow, _ string) (*store.BranchRow, error) {
            return &store.BranchRow{
                IssueSlug:  "55",
                BranchName: "feature/55",
            }, nil
        },
    }

    t.Run("close is refused when branch is in_review", func(t *testing.T) {
        err := runClose(ctx, deps, prompter)
        if err == nil {
            t.Fatal("expected error, got nil")
        }
        if !errors.Is(err, ErrBranchLockedForReview) {
            t.Errorf("expected ErrBranchLockedForReview, got: %v", err)
        }
    })

    t.Run("close is refused with changes_requested", func(t *testing.T) {
        // Transition to changes_requested to test that sentinel too.
        latest, _ := repo.store.GetLatestReview(ctx, "55")
        _ = repo.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusChangesRequested, false)

        err := runClose(ctx, deps, prompter)
        if err == nil {
            t.Fatal("expected error, got nil")
        }
        if !errors.Is(err, ErrReviewChangesRequested) {
            t.Errorf("expected ErrReviewChangesRequested, got: %v", err)
        }
    })
}
```

- [ ] **Step 2: Run to verify failure**

```
mise exec -- go test ./cmd/issue/... -run TestClose_BlockedWhenInReview -v
```
Expected: FAIL — `reviewPreflight` not yet implemented.

- [ ] **Step 3: Add sentinel errors and reviewPreflight to cmd/issue/close.go**

First add sentinel errors near the top of `cmd/issue/close.go` (after the existing `errFastForwardDeferred`):

```go
// ErrBranchLockedForReview is returned by reviewPreflight when the branch is
// locked because a review is in progress. Use errors.Is to detect it.
var ErrBranchLockedForReview = errors.New("branch locked for review")

// ErrReviewChangesRequested is returned by reviewPreflight when the reviewer
// has requested changes. Use errors.Is to detect it.
var ErrReviewChangesRequested = errors.New("reviewer requested changes")
```

Then add this function to `cmd/issue/close.go`:

```go
// reviewPreflight checks whether the issue associated with picked has an
// active or blocked review. Returns an error if close should be refused,
// or handles reviewer-commit incorporation if approved.
func reviewPreflight(ctx context.Context, deps closeDeps, picked *store.BranchRow) error {
    latest, err := deps.store.GetLatestReview(ctx, picked.IssueSlug)
    if err != nil {
        return fmt.Errorf("check review status: %w", err)
    }

    if latest == nil {
        return nil // no review record — proceed
    }

    switch latest.Status {
    case store.ReviewStatusInReview:
        return fmt.Errorf(
            "branch %q is locked for review (issue %q, round %d) — awaiting reviewer decision.\n"+
                "Run `git zf review list` to check review status: %w",
            picked.BranchName, picked.IssueSlug, latest.Round, ErrBranchLockedForReview)

    case store.ReviewStatusChangesRequested:
        return fmt.Errorf(
            "reviewer requested changes on issue %q (round %d).\n"+
                "Address feedback and run `git zf review request %s` for round %d: %w",
            picked.IssueSlug, latest.Round, picked.IssueSlug, latest.Round+1, ErrReviewChangesRequested)

    case store.ReviewStatusApproved:
        // Incorporate reviewer commits from <IssueSlug>/review if present.
        reviewBranch := picked.IssueSlug + "@review"
        if exists, _ := deps.client.BranchExists(reviewBranch); exists {
            n, countErr := deps.client.CommitsAhead(ctx, reviewBranch, picked.BranchName)
            if countErr == nil && n > 0 {
                fmt.Fprintf(deps.client.IO().Out,
                    "Incorporating %d reviewer commit(s) from %s into %s...\n",
                    n, reviewBranch, picked.BranchName)
                if err := deps.client.FastForwardOnly(ctx, reviewBranch, picked.BranchName); err != nil {
                    return fmt.Errorf("fast-forward %s to %s: %w", picked.BranchName, reviewBranch, err)
                }
            }
            // Clean up review branch and ref.
            if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
                fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
            }
            _ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
            _ = deps.client.DeleteReviewRef(ctx, picked.IssueSlug)
        }
        return nil
    }

    return nil
}
```

Then call it at the top of `runClose`, immediately after `getPickedBranch` and before `doMerge`:

```go
// In runClose, after `picked, err := getPickedBranch(...)` and the nil check:
if err := reviewPreflight(ctx, deps, picked); err != nil {
    return err
}
```

- [ ] **Step 4: Run tests**

```
mise exec -- go test ./cmd/issue/... -run TestClose_BlockedWhenInReview -v
```
Expected: PASS.

- [ ] **Step 5: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(issue): review preflight in issue close — blocks when in_review, incorporates reviewer commits"
```

---

## Task 11: `issue start --parent` Sub-task Support

**Files:**
- Modify: `cmd/issue/start.go`
- Modify: `cmd/issue/start_e2e_test.go`

- [ ] **Step 1: Write failing test**

Add to `cmd/issue/start_e2e_test.go`:

```go
func TestRunIssueStart_WithParent(t *testing.T) {
    ctx := context.Background()
    repo := newStartTestRepo(t, ctx) // reuse existing helper

    // Seed parent issue + integration branch.
    parentIssue := &store.Issue{IDSlug: "10", Title: "parent", StatusID: store.StatusIDInProgress}
    parentBranch := &store.Branch{Name: "feature/10", Type: "feature", StatusID: store.StatusIDInProgress}
    if err := repo.store.InsertIssueWithBranch(ctx, parentIssue, parentBranch); err != nil {
        t.Fatalf("seed parent: %v", err)
    }

    t.Run("creates child branch from parent integration branch", func(t *testing.T) {
        prompter := &scriptedStartPrompter{
            getFromUser: func() (*issue.Issue, error) {
                return &issue.Issue{IDSlug: "10.1", Title: "sub-task"}, nil
            },
            confirmBranch: func(_ string) (bool, error) { return true, nil },
        }

        deps := StartDeps{
            Client: repo.client,
            Cfg:    repo.cfg,
            Flags:  issue.IssueStartFlags{ParentIssueSlug: "10"},
        }

        if err := RunIssueStart(ctx, deps, repo.store, prompter); err != nil {
            t.Fatalf("RunIssueStart with parent: %v", err)
        }

        // Verify issue_relations populated.
        parent, err := repo.store.GetParentIssue(ctx, "10.1")
        if err != nil {
            t.Fatalf("GetParentIssue: %v", err)
        }
        if parent != "10" {
            t.Errorf("parent: got %q, want %q", parent, "10")
        }
    })
}
```

- [ ] **Step 2: Run to verify failure**

```
mise exec -- go test ./cmd/issue/... -run TestRunIssueStart_WithParent -v
```
Expected: FAIL — `--parent` flag not defined, `ParentIssueSlug` not in `IssueStartFlags`.

- [ ] **Step 3: Add ParentIssueSlug to IssueStartFlags**

In `issue/issue.go`, add to the `IssueStartFlags` struct:

```go
type IssueStartFlags struct {
    // existing fields ...
    ParentIssueSlug string // non-empty when this is a sub-task; value is the parent's IssueSlug
}
```

- [ ] **Step 4: Add --parent flag in cmd/issue/start.go**

In `getStartCmd()`, add:

```go
cmd.Flags().String("parent", "", "parent issue slug — creates this as a sub-task branching from the parent integration branch")
```

In the `RunE` closure, read the flag and pass it to `BuildStartDeps` via `IssueStartFlags`:

```go
parent, err := cmd.Flags().GetString("parent")
if err != nil {
    return fmt.Errorf("get --parent flag: %w", err)
}
flags.ParentIssueSlug = parent
```

- [ ] **Step 5: Use parent branch as base in RunIssueStart**

In `RunIssueStart` (or `BuildStartDeps`), when `deps.Flags.ParentIssueSlug != ""`:

1. Look up the parent's branch name from the store.
2. Use the parent branch as `baseBranch` instead of `cfg.Branch.Base`.
3. After the branch is created and persisted, call `store.InsertIssueRelation(ctx, parentSlug, childSlug)`.

Add this logic near the start of `RunIssueStart` in `cmd/issue/start.go`:

```go
if deps.Flags.ParentIssueSlug != "" {
    parentBranches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
    if err != nil {
        return fmt.Errorf("list parent branches: %w", err)
    }
    var parentBranch string
    for _, b := range parentBranches {
        if b.IssueSlug == deps.Flags.ParentIssueSlug {
            parentBranch = b.BranchName
            break
        }
    }
    if parentBranch == "" {
        return fmt.Errorf("no in-progress branch found for parent issue %q", deps.Flags.ParentIssueSlug)
    }
    // Override base branch to be the parent integration branch.
    deps.Cfg = deps.Cfg.WithBaseBranch(parentBranch) // see note below
}
```

Note: `AppConfig` is a struct — add a helper or pass `baseBranchOverride` as a parameter rather than mutating the config. The simplest approach is to add an optional `BaseBranchOverride string` field to `StartDeps` and check it in the branch creation logic.

After the branch and issue are persisted to the store:

```go
if deps.Flags.ParentIssueSlug != "" {
    if err := s.InsertIssueRelation(ctx, deps.Flags.ParentIssueSlug, newIssueSlug); err != nil {
        fmt.Fprintf(deps.Client.IO().Err, "warning: record parent relation: %v\n", err)
    }
}
```

- [ ] **Step 6: Run tests**

```
mise exec -- go test ./cmd/issue/... -run TestRunIssueStart_WithParent -v
```
Expected: PASS.

- [ ] **Step 7: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add cmd/issue/start.go issue/issue.go store/store.go cmd/issue/start_e2e_test.go
git commit -m "feat(issue): add --parent flag to issue start for sub-task composition"
```

---

## Task 12: Wire `review` Command Group + Close Sub-task Guard + E2E Lifecycle Test

**Files:**
- Modify: `cmd/root.go`
- Modify: `cmd/issue/close.go` (sub-task guard)
- Create: `cmd/review/review_e2e_test.go`

- [ ] **Step 1: Register review command in cmd/root.go**

In `GetRootCmd()`, add:

```go
import "github.com/piprim/git-zf/cmd/review"

// ... in GetRootCmd():
rv := review.New(appConfig)

rootCmd.AddCommand(
    // existing commands ...
    rv.GetRootCmd(),
)
```

- [ ] **Step 2: Add sub-task guard in issue close**

In `runClose`, after `reviewPreflight` and before `doMerge`, add:

```go
// Guard: if this is a sub-task, close into the parent integration branch.
parentSlug, err := deps.store.GetParentIssue(ctx, picked.IssueSlug)
if err != nil {
    return fmt.Errorf("check parent issue: %w", err)
}
if parentSlug != "" {
    // Override base to be the parent integration branch.
    parentBranches, _ := deps.store.ListBranches(ctx, store.BranchStatusAll)
    for _, b := range parentBranches {
        if b.IssueSlug == parentSlug {
            base = b.BranchName
            break
        }
    }
}

// Guard: if this is a parent issue, all children must be merged.
allDone, err := deps.store.ChildrenAllMerged(ctx, picked.IssueSlug)
if err != nil {
    return fmt.Errorf("check children: %w", err)
}
if !allDone {
    children, _ := deps.store.ListChildIssues(ctx, picked.IssueSlug)
    return fmt.Errorf(
        "issue %q has open sub-tasks: %v\nClose all sub-tasks before closing the parent",
        picked.IssueSlug, children)
}
```

Note: `base` is already declared earlier in `runClose` — this block must come after it is set.

- [ ] **Step 3: Write E2E lifecycle test**

Create `cmd/review/review_e2e_test.go`:

```go
package review_test

import (
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    "github.com/piprim/git-zf/cmd/review"
    "github.com/piprim/git-zf/config"
    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/internal/pkg"
    "github.com/piprim/git-zf/store"
)

type reviewTestEnv struct {
    dir    string
    client *git.Client
    store  *store.Store
    cfg    *config.AppConfig
}

func newReviewTestEnv(t *testing.T) *reviewTestEnv {
    t.Helper()
    dir := t.TempDir()

    run := func(args ...string) {
        t.Helper()
        cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }

    run("init")
    run("config", "user.email", "test@example.com")
    run("config", "user.name", "Test")
    run("commit", "--allow-empty", "-m", "init")
    run("checkout", "-b", "feature/77")
    run("commit", "--allow-empty", "-m", "work")

    io := &pkg.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
    c, err := git.NewClientAt(io, dir)
    if err != nil {
        t.Fatalf("NewClientAt: %v", err)
    }

    gitDir := filepath.Join(dir, ".git")
    s, err := store.Open(context.Background(), gitDir)
    if err != nil {
        t.Fatalf("Open store: %v", err)
    }
    t.Cleanup(func() { _ = s.Close() })

    iss := &store.Issue{IDSlug: "77", Title: "e2e review test", StatusID: store.StatusIDInProgress}
    br := &store.Branch{Name: "feature/77", Type: "feature", StatusID: store.StatusIDInProgress}
    if err := s.InsertIssueWithBranch(context.Background(), iss, br); err != nil {
        t.Fatalf("seed: %v", err)
    }

    cfg := &config.AppConfig{}
    cfg.Branch.Base = "master"

    return &reviewTestEnv{dir: dir, client: c, store: s, cfg: cfg}
}

func TestReviewLifecycle_RequestApprove(t *testing.T) {
    ctx := context.Background()
    env := newReviewTestEnv(t)

    rv := review.New(env.cfg)
    _ = rv // commands are exercised via runReview* helpers called through reviewDeps

    // We test the public functions indirectly through the store + ref state.
    // Direct function testing via exported runReview* is preferred but they are
    // package-internal; use the cobra command execution path instead.
    cmd := rv.GetRootCmd()
    cmd.SetContext(ctx)

    t.Run("review request sets in_review status", func(t *testing.T) {
        cmd.SetArgs([]string{"request", "77"})
        if err := cmd.Execute(); err != nil {
            // Expected when no remote is configured — ref push is best-effort.
            if !strings.Contains(err.Error(), "remote") {
                t.Fatalf("review request: %v", err)
            }
        }

        latest, err := env.store.GetLatestReview(ctx, "77")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if latest == nil {
            t.Fatal("expected review row, got nil")
        }
        if latest.Status != store.ReviewStatusInReview {
            t.Errorf("status: got %q, want %q", latest.Status, store.ReviewStatusInReview)
        }
    })

    t.Run("review approve sets approved status", func(t *testing.T) {
        cmd.SetArgs([]string{"approve", "77"})
        if err := cmd.Execute(); err != nil {
            t.Fatalf("review approve: %v", err)
        }

        latest, err := env.store.GetLatestReview(ctx, "77")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if latest.Status != store.ReviewStatusApproved {
            t.Errorf("status: got %q, want %q", latest.Status, store.ReviewStatusApproved)
        }
    })
}

func TestReviewLifecycle_RequestReject(t *testing.T) {
    ctx := context.Background()
    env := newReviewTestEnv(t)

    cmd := review.New(env.cfg).GetRootCmd()
    cmd.SetContext(ctx)

    t.Run("review request then reject unlocks branch", func(t *testing.T) {
        cmd.SetArgs([]string{"request", "77"})
        _ = cmd.Execute()

        cmd.SetArgs([]string{"reject", "77"})
        if err := cmd.Execute(); err != nil {
            t.Fatalf("review reject: %v", err)
        }

        latest, err := env.store.GetLatestReview(ctx, "77")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if latest.Status != store.ReviewStatusChangesRequested {
            t.Errorf("status: got %q, want %q", latest.Status, store.ReviewStatusChangesRequested)
        }
    })

    t.Run("second review request increments round", func(t *testing.T) {
        cmd.SetArgs([]string{"request", "77"})
        _ = cmd.Execute()

        latest, err := env.store.GetLatestReview(ctx, "77")
        if err != nil {
            t.Fatalf("GetLatestReview: %v", err)
        }
        if latest.Round != 2 {
            t.Errorf("round: got %d, want 2", latest.Round)
        }
    })
}
```

- [ ] **Step 4: Run E2E test**

```
mise exec -- go test ./cmd/review/... -run TestReviewLifecycle -v
```
Expected: PASS.

- [ ] **Step 5: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

- [ ] **Step 6: Build final binary**

```
mise exec -- go build -o ./bin/git-zf .
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/root.go cmd/issue/close.go cmd/review/review_e2e_test.go
git commit -m "feat: wire review command group, add sub-task guards in issue close, E2E lifecycle tests"
```

---

## Self-Review Checklist

After completing all tasks, verify coverage against the spec:

| Spec requirement | Task |
|-----------------|------|
| `reviews` + `issue_relations` tables | Task 1 |
| `ReviewStatus` constants | Task 1 |
| Atomic ref write (CAS) | Task 2 |
| `--force-with-lease` push | Task 2 |
| CAS conflict resolution | Task 2 |
| `CommitsAhead` | Task 3 |
| `DeleteRemoteBranch` | Task 3 |
| `MergeForward` (no force-push) | Task 3 |
| `review request` — lock, push ref, hook | Task 4 |
| `review start` — branch from feature_sha | Task 5 |
| `review approve` — detect reviewer commits | Task 6 |
| `review reject` — keep/delete review branch | Task 7 |
| `review list` / `review status` / `review fetch` | Task 8 |
| `review sync` — merge-forward, no rebase | Task 9 |
| `review guard` — pre-push hook check | Task 9 |
| `issue close` review preflight | Task 10 |
| `issue close` fast-forward reviewer commits | Task 10 |
| `issue close` clean up review branch + ref | Task 10 |
| `issue start --parent` | Task 11 |
| `issue close` sub-task → parent branch target | Task 12 |
| `issue close` parent blocked until children merged | Task 12 |
| `review` command wired in root | Task 12 |
| Pre-push hook installed by `review request` | Task 4 |
| Tracker sync (best-effort, non-fatal) | ⚠️ Not implemented in v1 — deferred per spec section 12 |
