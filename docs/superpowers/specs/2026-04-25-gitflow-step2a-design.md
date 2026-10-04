# Design: git cz issue — Local Flow (Step 2a)

**Date:** 2026-04-25  
**Status:** Approved  
**Scope:** Step 2a of 3 — branch naming, SQLite store, and `git cz issue` TUI with manual issue entry. Step 2b (live tracker integration) is a separate spec.

---

## Overview

Step 2a delivers the full `git cz issue` workflow without any network calls:

- User enters issue ID, title, and type manually in a TUI form.
- A branch name is assembled as `{issue-id}@{type}@{slugified-title}@{short-uuid}`.
- The branch is cut from the repo's default base branch (configurable) and checked out.
- The branch record is saved to a per-repo SQLite database at `.git/git-cz.db`.

---

## Section 1: TUI Flow

`git cz issue` presents a two-group `huh` form.

### Group 1 — Issue details

| Field | Widget | Notes |
|---|---|---|
| Issue ID | `huh.NewInput` | required; free-form string (e.g. `ABC-42`, `42`) |
| Title | `huh.NewInput` | required; slugified to produce the branch title segment |
| Type | `huh.NewSelect` | reuses commit types from `MessageConfig.Items[0]` (feat, fix, chore, …) |

### Group 2 — Confirmation

A single `huh.NewConfirm` whose title is assembled dynamically once Group 1 is complete:

> `Create branch "ABC-42@feat@add-oauth-login@550e8400" based on "master"?`

The short UUID is generated and the base branch is resolved before Group 2 renders so the full message is displayed.

**On confirm → yes:**
1. Resolve base branch (see Section 4).
2. `client.CreateBranch(name, base)` — creates and checks out the branch.
3. `store.Insert(Branch{...})` — persists the record with `status = "in_progress"`.

**On confirm → no:** exit cleanly, nothing created.

---

## Section 2: `branch/` Package

Pure functions — no I/O, no external dependencies, fully testable in isolation.

```go
// Slug converts a human title to kebab-case:
// lowercase, spaces→hyphens, non-alphanumeric chars stripped.
func Slug(title string) string

// ShortUUID returns the first 8 hex characters of a new random UUID v4.
func ShortUUID() string

// Name assembles the branch name from its four components.
// Format: issueID@branchType@Slug(title)@ShortUUID()
func Name(issueID, branchType, title string) string

// Parse splits a branch name back into its four components.
// Returns an error if the name does not contain exactly 4 "@"-separated parts.
func Parse(name string) (issueID, branchType, title, uuid string, err error)
```

---

## Section 3: `store/` Package

Uses `modernc.org/sqlite` (pure Go, no CGO). Database file: `.git/git-cz.db`.

### Migration strategy

Migrations are plain `.sql` files embedded via `//go:embed migrations/*.sql`. On `Open`, the runner reads all files sorted by name, checks `PRAGMA user_version`, executes pending migrations in order, and bumps the version. Adding a future migration is a new `000N_*.sql` file — no Go code change required.

### Schema (`0001_initial.sql`)

```sql
CREATE TABLE statuses (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);
INSERT INTO statuses (id, name) VALUES (1, 'in_progress'), (2, 'merged');

CREATE TABLE issues (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    id_slug   TEXT NOT NULL,   -- tracker string ID: "ABC-42", "42", …
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

### Public API

```go
type Store struct { /* unexported db handle */ }

type Issue struct {
    ID       int64
    IDSlug   string  // tracker string ID: "ABC-42", "42", …
    Title    string
    StatusID int64
}

type Branch struct {
    UUID      string
    Name      string
    IssueID   int64  // FK to issues.id
    Type      string
    StatusID  int64
    CreatedAt time.Time
    MergedAt  *time.Time
}

// Open opens (or creates) the database at gitDir/git-cz.db and runs pending migrations.
func Open(gitDir string) (*Store, error)

func (s *Store) Close() error
// InsertIssueWithBranch inserts both records in a single transaction.
func (s *Store) InsertIssueWithBranch(issue Issue, branch Branch) error
func (s *Store) UpdateBranchStatus(uuid string, statusID int64, mergedAt *time.Time) error
func (s *Store) UpdateIssueStatus(issueID int64, statusID int64) error
```

`gitDir` is obtained from `git.Client.WorkingTreeRoot()` + `"/.git"`.

---

## Section 4: `git.Client` Extensions

Two new methods on the existing `Client` struct. No configuration dependency — all config decisions are made in `cmd/` and passed in as plain strings.

```go
// DefaultBaseBranch resolves the base branch in priority order:
//  1. refs/remotes/origin/HEAD
//  2. "main" if the local ref exists
//  3. "master" if the local ref exists
// Returns an error if none of the above resolve.
func (c *Client) DefaultBaseBranch() (string, error)

// CreateBranch creates a new branch from baseBranch and checks it out.
func (c *Client) CreateBranch(name, baseBranch string) error
```

**Base branch resolution in `cmd/issue.go`:**
```go
base := viper.GetString("branch.base") // config override
if base == "" {
    base, err = client.DefaultBaseBranch()
}
```

---

## Section 5: Package & File Structure

```
commitizen-go/
├── cmd/
│   ├── issue.go        # full git cz issue TUI — wires branch/, store/, git.Client
│   └── ...             # unchanged
├── branch/
│   ├── branch.go       # Slug, ShortUUID, Name, Parse
│   └── branch_test.go
├── store/
│   ├── store.go        # Store struct, Open, Close, Insert, UpdateStatus
│   ├── store_test.go
│   └── migrations/
│       └── 0001_initial.sql
└── git/
    ├── git.go          # + DefaultBaseBranch, CreateBranch methods
    └── git_test.go     # + tests for the two new methods
```

`.git-zf.json` gains one optional section:

```json
{
  "branch": {
    "base": "develop"
  }
}
```

---

## Section 6: Testing

### `branch/` package

| Test | Verifies |
|---|---|
| `TestSlug` | spaces→hyphens, lowercase, special chars stripped |
| `TestName` | correct `@`-separated assembly |
| `TestParse` | round-trips `Name` output back to 4 components |
| `TestParse_invalid` | returns error on malformed branch names |
| `TestShortUUID` | 8 hex chars, unique across calls |

### `store/` package

Tests use a temp-file SQLite DB (`t.TempDir()`), no mocks:

| Test | Verifies |
|---|---|
| `TestOpen_createsMigrations` | all three tables exist after `Open` |
| `TestInsertIssueWithBranch` | both rows retrievable; transaction rolls back on error |
| `TestUpdateBranchStatus_merged` | `merged_at` required; trigger fires on null |
| `TestMigration_idempotent` | calling `Open` twice does not error |

### `git/` package

Extends existing in-memory repo tests:

| Test | Verifies |
|---|---|
| `TestDefaultBaseBranch_remote` | resolves from `refs/remotes/origin/HEAD` |
| `TestDefaultBaseBranch_fallback` | falls back to `main` then `master` |
| `TestCreateBranch` | branch exists and is checked out after call |

---

## Update the README.md

Check that the README.md reflects the actual behavior.

## Out of Scope (Step 2b)

- `tracker/` package (Plane, Redmine adapters, pluggable registry)
- Live issue picker in `git cz issue` TUI
- `git cz issue list` command
