# git cz issue — Local Flow (Step 2a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `git cz issue` — a TUI that takes issue ID, title, and type, creates a properly-named branch from the default base, and records the result in a per-repo SQLite database.

**Architecture:** Bottom-up — `branch/` (pure functions) first, then `store/` (SQLite), then `git.Client` extensions, then `cmd/issue.go` wiring everything together. Each task leaves the project in a buildable, tested state.

**Tech Stack:** `modernc.org/sqlite` (pure Go SQLite), `github.com/charmbracelet/huh` (TUI), `github.com/go-git/go-git/v6`, `github.com/spf13/viper` (config)

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `branch/branch.go` | Create | `Slug`, `ShortUUID`, `Name`, `Parse` — pure functions, no I/O |
| `branch/branch_test.go` | Create | Table-driven tests for all four functions |
| `store/migrations/0001_initial.sql` | Create | Normalized schema: `statuses`, `issues`, `branches`, trigger |
| `store/store.go` | Create | `Store`, `Open` (with migration runner), `InsertIssueWithBranch`, `UpdateBranchStatus`, `UpdateIssueStatus` |
| `store/store_test.go` | Create | In-process SQLite tests using `t.TempDir()` |
| `git/git.go` | Modify | Add `DefaultBaseBranch()` and `CreateBranch()` methods to `Client` |
| `git/git_test.go` | Modify | Add `TestDefaultBaseBranch_*` and `TestCreateBranch` |
| `cmd/issue.go` | Modify | Replace stub with full TUI flow wiring `branch/`, `store/`, `git.Client` |
| `go.mod` / `go.sum` | Modify | Add `modernc.org/sqlite` |

---

## Task 1: Add `modernc.org/sqlite` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

```bash
cd /workspace
go get modernc.org/sqlite
```

- [ ] **Step 2: Verify build still passes**

```bash
go build ./...
go test ./...
```

Expected: no errors, all existing tests pass.

---

## Task 2: `branch/` package (TDD)

**Files:**
- Create: `branch/branch_test.go`
- Create: `branch/branch.go`

### Step 2a — Write failing tests first

- [ ] **Step 1: Create `branch/branch_test.go`**

```go
package branch

import (
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Add OAuth Login", "add-oauth-login"},
		{"  leading and trailing  ", "leading-and-trailing"},
		{"special!@#chars", "specialchars"},
		{"multiple   spaces", "multiple-spaces"},
		{"already-kebab", "already-kebab"},
		{"UPPERCASE", "uppercase"},
		{"feat/scope", "featscope"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := Slug(tt.input); got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestShortUUID(t *testing.T) {
	t.Parallel()

	a := ShortUUID()
	b := ShortUUID()

	if len(a) != 8 {
		t.Errorf("ShortUUID len = %d, want 8", len(a))
	}
	if strings.ContainsAny(a, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("ShortUUID %q contains uppercase", a)
	}
	if a == b {
		t.Errorf("two consecutive ShortUUID calls returned the same value: %q", a)
	}
}

func TestName(t *testing.T) {
	t.Parallel()

	// Name calls ShortUUID internally, so we only check structure.
	n := Name("ABC-42", "feat", "Add OAuth Login")
	parts := strings.Split(n, "@")
	if len(parts) != 4 {
		t.Fatalf("Name produced %d parts, want 4: %q", len(parts), n)
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
	if len(parts[3]) != 8 {
		t.Errorf("parts[3] len = %d, want 8", len(parts[3]))
	}
}

func TestParse(t *testing.T) {
	t.Parallel()

	issueID, branchType, title, uuid, err := Parse("ABC-42@feat@add-oauth-login@550e8400")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if issueID != "ABC-42" {
		t.Errorf("issueID = %q, want %q", issueID, "ABC-42")
	}
	if branchType != "feat" {
		t.Errorf("branchType = %q, want %q", branchType, "feat")
	}
	if title != "add-oauth-login" {
		t.Errorf("title = %q, want %q", title, "add-oauth-login")
	}
	if uuid != "550e8400" {
		t.Errorf("uuid = %q, want %q", uuid, "550e8400")
	}
}

func TestParse_invalid(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"no-separators",
		"only@two",
		"one@two@three",
		"one@two@three@four@five",
	}
	for _, c := range cases {
		if _, _, _, _, err := Parse(c); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", c)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./branch/... 2>&1 | head -5
```

Expected: compile error — package `branch` does not exist yet.

### Step 2b — Implement

- [ ] **Step 3: Create `branch/branch.go`**

```go
package branch

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

var nonAlphanumHyphen = regexp.MustCompile(`[^a-z0-9-]+`)
var multiHyphen = regexp.MustCompile(`-{2,}`)

// Slug converts a human title to kebab-case.
func Slug(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = regexp.MustCompile(`\s+`).ReplaceAllString(s, "-")
	s = nonAlphanumHyphen.ReplaceAllString(s, "")
	s = multiHyphen.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	return s
}

// ShortUUID returns the first 8 hex characters of a new random UUID v4.
func ShortUUID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("branch.ShortUUID: crypto/rand failed: %v", err))
	}

	return hex.EncodeToString(b)
}

// Name assembles the branch name: issueID@branchType@Slug(title)@ShortUUID().
func Name(issueID, branchType, title string) string {
	return issueID + "@" + branchType + "@" + Slug(title) + "@" + ShortUUID()
}

// Parse splits a branch name back into its four "@"-separated components.
func Parse(name string) (issueID, branchType, title, uuid string, err error) {
	parts := strings.Split(name, "@")
	if len(parts) != 4 {
		return "", "", "", "", fmt.Errorf("branch name %q: expected 4 parts, got %d", name, len(parts))
	}

	return parts[0], parts[1], parts[2], parts[3], nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./branch/... -v
```

Expected: all 5 tests pass.

---

## Task 3: `store/` package — migration + schema (TDD)

**Files:**
- Create: `store/migrations/0001_initial.sql`
- Create: `store/store.go`
- Create: `store/store_test.go`

### Step 3a — Write failing tests first

- [ ] **Step 1: Create `store/store_test.go`**

```go
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestOpen_createsMigrations(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	// Verify all three tables exist by querying sqlite_master.
	tables := []string{"statuses", "issues", "branches"}
	for _, tbl := range tables {
		var name string
		row := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q not found: %v", tbl, err)
		}
	}
}

func TestMigration_idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = s2.Close()
}

func TestInsertIssueWithBranch(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	issue := Issue{IDSlug: "ABC-42", Title: "Add OAuth Login", StatusID: 1}
	branch := Branch{
		UUID:     "550e8400",
		Name:     "ABC-42@feat@add-oauth-login@550e8400",
		Type:     "feat",
		StatusID: 1,
	}

	if err := s.InsertIssueWithBranch(issue, branch); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	// Verify the branch row exists.
	var name string
	row := s.db.QueryRow("SELECT name FROM branches WHERE uuid = ?", "550e8400")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("branch not found: %v", err)
	}
	if name != "ABC-42@feat@add-oauth-login@550e8400" {
		t.Errorf("name = %q, want %q", name, "ABC-42@feat@add-oauth-login@550e8400")
	}

	// Verify the issue row exists.
	var idSlug string
	row = s.db.QueryRow("SELECT id_slug FROM issues WHERE id = (SELECT issue_id FROM branches WHERE uuid = ?)", "550e8400")
	if err := row.Scan(&idSlug); err != nil {
		t.Fatalf("issue not found: %v", err)
	}
	if idSlug != "ABC-42" {
		t.Errorf("id_slug = %q, want %q", idSlug, "ABC-42")
	}
}

func TestUpdateBranchStatus_merged(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	issue := Issue{IDSlug: "ABC-1", Title: "Some issue", StatusID: 1}
	branch := Branch{UUID: "aabbccdd", Name: "ABC-1@fix@some-issue@aabbccdd", Type: "fix", StatusID: 1}
	if err := s.InsertIssueWithBranch(issue, branch); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	// Updating to merged without merged_at must fail (trigger).
	if err := s.UpdateBranchStatus("aabbccdd", 2, nil); err == nil {
		t.Error("expected error when merged_at is nil for merged status, got nil")
	}

	// Updating to merged with merged_at must succeed.
	now := time.Now()
	if err := s.UpdateBranchStatus("aabbccdd", 2, &now); err != nil {
		t.Errorf("UpdateBranchStatus merged: %v", err)
	}

	var statusID int64
	row := s.db.QueryRow("SELECT status_id FROM branches WHERE uuid = ?", "aabbccdd")
	if err := row.Scan(&statusID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if statusID != 2 {
		t.Errorf("status_id = %d, want 2", statusID)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./store/... 2>&1 | head -5
```

Expected: compile error — package `store` does not exist yet.

### Step 3b — Create the migration file

- [ ] **Step 3: Create `store/migrations/0001_initial.sql`**

```sql
CREATE TABLE statuses (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);
INSERT INTO statuses (id, name) VALUES (1, 'in_progress'), (2, 'merged');

CREATE TABLE issues (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    id_slug   TEXT NOT NULL,
    title     TEXT NOT NULL,
    status_id INTEGER NOT NULL DEFAULT 1 REFERENCES statuses(id)
);

CREATE TABLE branches (
    uuid       TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    issue_id   INTEGER NOT NULL REFERENCES issues(id),
    type       TEXT NOT NULL,
    status_id  INTEGER NOT NULL DEFAULT 1 REFERENCES statuses(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at  DATETIME
);

-- SQLite CHECK constraints cannot reference other tables,
-- so the merged_at invariant is enforced via a trigger.
CREATE TRIGGER enforce_merged_at
BEFORE UPDATE OF status_id ON branches
WHEN NEW.status_id = 2
BEGIN
    SELECT CASE WHEN NEW.merged_at IS NULL
        THEN RAISE(ABORT, 'merged_at must not be null when status is merged')
    END;
END;
```

### Step 3c — Implement `store/store.go`

- [ ] **Step 4: Create `store/store.go`**

```go
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a SQLite database for branch and issue persistence.
type Store struct {
	db *sql.DB
}

// Issue represents a tracked issue record.
type Issue struct {
	ID       int64
	IDSlug   string // tracker string ID: "ABC-42", "42", …
	Title    string
	StatusID int64
}

// Branch represents a tracked branch record.
type Branch struct {
	UUID      string
	Name      string
	IssueID   int64
	Type      string
	StatusID  int64
	CreatedAt time.Time
	MergedAt  *time.Time
}

// Open opens (or creates) the SQLite database at gitDir/git-cz.db and runs pending migrations.
func Open(gitDir string) (*Store, error) {
	path := filepath.Join(gitDir, "git-cz.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Enable foreign keys.
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// InsertIssueWithBranch inserts an issue and its linked branch in a single transaction.
func (s *Store) InsertIssueWithBranch(issue Issue, branch Branch) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`INSERT INTO issues (id_slug, title, status_id) VALUES (?, ?, ?)`,
		issue.IDSlug, issue.Title, issue.StatusID,
	)
	if err != nil {
		return fmt.Errorf("insert issue: %w", err)
	}

	issueID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	_, err = tx.Exec(
		`INSERT INTO branches (uuid, name, issue_id, type, status_id) VALUES (?, ?, ?, ?, ?)`,
		branch.UUID, branch.Name, issueID, branch.Type, branch.StatusID,
	)
	if err != nil {
		return fmt.Errorf("insert branch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// UpdateBranchStatus updates a branch's status. mergedAt must be non-nil when statusID == 2 (merged).
func (s *Store) UpdateBranchStatus(uuid string, statusID int64, mergedAt *time.Time) error {
	_, err := s.db.Exec(
		`UPDATE branches SET status_id = ?, merged_at = ? WHERE uuid = ?`,
		statusID, mergedAt, uuid,
	)
	if err != nil {
		return fmt.Errorf("update branch status: %w", err)
	}

	return nil
}

// UpdateIssueStatus updates an issue's status.
func (s *Store) UpdateIssueStatus(issueID int64, statusID int64) error {
	_, err := s.db.Exec(
		`UPDATE issues SET status_id = ? WHERE id = ?`,
		statusID, issueID,
	)
	if err != nil {
		return fmt.Errorf("update issue status: %w", err)
	}

	return nil
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for i, name := range names {
		if i < version {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		if _, err := s.db.Exec(string(content)); err != nil {
			return fmt.Errorf("exec migration %s: %w", name, err)
		}

		if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("bump user_version: %w", err)
		}
	}

	return nil
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./store/... -v
```

Expected: all 4 tests pass.

---

## Task 4: `git.Client` extensions (TDD)

**Files:**
- Modify: `git/git_test.go`
- Modify: `git/git.go`

### Step 4a — Write failing tests first

- [ ] **Step 1: Append to `git/git_test.go`**

Add these three tests after the existing `TestCommit_amend` function:

```go
func TestDefaultBaseBranch_fallback(t *testing.T) {
	t.Parallel()

	// newTestRepo creates a repo with no remotes and commits on the default branch.
	// go-git initializes with "master" by default.
	repo := newTestRepo(t)
	client := &Client{repo: repo}

	base, err := client.DefaultBaseBranch()
	if err != nil {
		t.Fatalf("DefaultBaseBranch: %v", err)
	}
	if base != "master" && base != "main" {
		t.Errorf("DefaultBaseBranch = %q, want master or main", base)
	}
}

func TestDefaultBaseBranch_main(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)

	// Rename the current branch to "main".
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Branch: "refs/heads/main",
		Create: true,
	}); err != nil {
		t.Fatalf("checkout main: %v", err)
	}

	client := &Client{repo: repo}
	base, err := client.DefaultBaseBranch()
	if err != nil {
		t.Fatalf("DefaultBaseBranch: %v", err)
	}
	if base != "main" {
		t.Errorf("DefaultBaseBranch = %q, want main", base)
	}
}

func TestCreateBranch(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	if err := client.CreateBranch("ABC-42@feat@add-oauth-login@550e8400", "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	// Verify HEAD points to the new branch.
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.Name().Short() != "ABC-42@feat@add-oauth-login@550e8400" {
		t.Errorf("HEAD = %q, want new branch", head.Name().Short())
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./git/... -run "TestDefaultBaseBranch|TestCreateBranch" 2>&1 | head -10
```

Expected: compile error — `DefaultBaseBranch` and `CreateBranch` not defined yet.

### Step 4b — Implement

- [ ] **Step 3: Append to `git/git.go`**

Add these two methods after `parseAuthor`:

```go
// DefaultBaseBranch resolves the default base branch in priority order:
//  1. refs/remotes/origin/HEAD
//  2. "main" if the local ref exists
//  3. "master" if the local ref exists
func (c *Client) DefaultBaseBranch() (string, error) {
	// Try remote HEAD first.
	if ref, err := c.repo.Reference("refs/remotes/origin/HEAD", true); err == nil {
		// ref.Name() is e.g. "refs/remotes/origin/main" — extract the short name.
		parts := strings.Split(ref.Name().String(), "/")

		return parts[len(parts)-1], nil
	}

	// Fall back to local branches.
	for _, name := range []string{"main", "master"} {
		if _, err := c.repo.Reference("refs/heads/"+name, false); err == nil {
			return name, nil
		}
	}

	return "", fmt.Errorf("could not detect default base branch")
}

// CreateBranch creates a new branch from baseBranch and checks it out.
func (c *Client) CreateBranch(name, baseBranch string) error {
	baseRef, err := c.repo.Reference("refs/heads/"+baseBranch, true)
	if err != nil {
		return fmt.Errorf("resolve base branch %q: %w", baseBranch, err)
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	if err := wt.Checkout(&gogit.CheckoutOptions{
		Hash:   baseRef.Hash(),
		Branch: "refs/heads/" + name,
		Create: true,
	}); err != nil {
		return fmt.Errorf("create branch %q: %w", name, err)
	}

	return nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./git/... -v
```

Expected: all 8 tests pass.

---

## Task 5: `cmd/issue.go` — full TUI flow

**Files:**
- Modify: `cmd/issue.go`

- [ ] **Step 1: Replace `cmd/issue.go` with the full implementation**

```go
package cmd

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func getIssueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "issue",
		Short: "Start work on an issue (pick issue → create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-cz.db.`,
		RunE: issueRunE,
	}
}

func issueRunE(_ *cobra.Command, _ []string) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	// Load commit types from config to reuse in the type selector.
	msgCfg, err := loadMessageConfig()
	if err != nil {
		return fmt.Errorf("load message config: %w", err)
	}

	// Resolve base branch before rendering Group 2.
	base := viper.GetString("branch.base")
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	// --- Group 1: issue details ---
	var issueID, title, branchType string

	typeOpts := make([]huh.Option[string], 0)
	if len(msgCfg.Items) > 0 {
		for _, opt := range msgCfg.Items[0].Options {
			typeOpts = append(typeOpts, huh.NewOption(opt.Name, opt.Name))
		}
	}

	group1 := huh.NewGroup(
		huh.NewInput().
			Title("Issue ID:").
			Placeholder("ABC-42").
			Validate(func(s string) error {
				if s == "" {
					return fmt.Errorf("required")
				}

				return nil
			}).
			Value(&issueID),
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(func(s string) error {
				if s == "" {
					return fmt.Errorf("required")
				}

				return nil
			}).
			Value(&title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(&branchType),
	)

	// Run Group 1 first so we can assemble the branch name for the confirmation.
	if err := huh.NewForm(group1).Run(); err != nil {
		return fmt.Errorf("issue form: %w", err)
	}

	branchName := branch.Name(issueID, branchType, title)

	// --- Group 2: confirmation ---
	var confirmed bool
	confirmTitle := fmt.Sprintf("Create branch %q based on %q?", branchName, base)

	group2 := huh.NewGroup(
		huh.NewConfirm().
			Title(confirmTitle).
			Value(&confirmed),
	)

	if err := huh.NewForm(group2).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	// Create the git branch.
	if err := client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	// Persist to store.
	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	_, _, slugTitle, uuid, _ := parseBranchName(branchName)
	if err := s.InsertIssueWithBranch(
		store.Issue{IDSlug: issueID, Title: title, StatusID: 1},
		store.Branch{UUID: uuid, Name: branchName, Type: branchType, StatusID: 1},
	); err != nil {
		log.Printf("store insert failed: %v", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q, slug: %q)\n", branchName, base, slugTitle)

	return nil
}

// parseBranchName is a thin wrapper around branch.Parse that suppresses the
// error (branch name was just generated so format is guaranteed valid).
func parseBranchName(name string) (issueID, branchType, title, uuid string, err error) {
	return branch.Parse(name)
}
```

> **Note on `loadMessageConfig`:** this function is already defined in `cmd/commit.go` and is accessible within the `cmd` package — no duplication needed.

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -race
```

Expected: all tests pass.

- [ ] **Step 4: Smoke test help output**

```bash
go run . issue --help
```

Expected: shows usage with description about branch creation.

---

## Task 6: Final verification

- [ ] **Step 1: Run full test suite with race detector**

```bash
go test ./... -race -v
```

Expected: all tests pass, no data races.

- [ ] **Step 2: Build binary and smoke-test**

```bash
go build -o commitizen-go .
./commitizen-go --help
./commitizen-go issue --help
```

Expected: `issue` subcommand shows the new long description (not "not yet implemented").

- [ ] **Step 3: Clean up test binary**

```bash
rm commitizen-go
```
