# Design: `git cz branch`

**Date:** 2026-04-27
**Status:** Approved
**Scope:** New `git cz branch` command with `list` and `new` subcommands. `merge` subcommand is stubbed for a future session.

---

## Overview

`git cz branch` manages local branches. It follows the same action-selector pattern as `git cz issue`: running the command with no subcommand shows a TUI menu; subcommands bypass it.

`git cz branch list` queries the local SQLite store and displays branches in an interactive `bubbles/table`. A `huh` status filter appears first so the user can narrow by status. Row actions (merge, delete…) will be wired in a future session.

`git cz branch new` creates a new branch with manual input by default. It shares the same underlying form and SQLite write logic as `git cz issue start`, but with inverted ergonomics: manual-first, toggle to tracker-picker. The tracker-picker toggle is deferred to Step 2b; in this session the command presents the manual form only.

`git cz issue start` = tracker-first (current behavior), toggle to manual (already works). `git cz branch new` = manual-first, toggle to tracker (Step 2b). Both produce the same SQLite record and branch.

`git cz issue list` and `git cz issue close` are deferred to Step 2b (tracker integration): listing and closing issues requires a live tracker API, not local state.

---

## Section 1: Command Structure

```
git cz branch                         → TUI action selector (list / new / merge)
git cz branch list                    → status filter → interactive table
git cz branch list --status merged    → status filter (default: merged) → interactive table
git cz branch list --stdout           → lipgloss table printed to stdout, no TUI
git cz branch list --json             → JSON array to stdout, no TUI
git cz branch new                     → manual-input form → create branch + SQLite record
git cz branch merge                   → "Not yet implemented"
```

**Flags for `git cz branch list`:**

| Flag | Type | Default | Description |
|---|---|---|---|
| `--status` | string | `""` | Pre-selects the status filter in the TUI (`in_progress`, `merged`, `all`). `cmd/` converts to `store.BranchStatus`. TUI always shown unless `--stdout` or `--json`. |
| `--stdout` | bool | false | Skips TUI; renders a lipgloss table to stdout and exits. Useful for piping and non-TTY environments. |
| `--json` | bool | false | Skips TUI entirely; marshals `[]BranchRow` to stdout. |

`--stdout` and `--json` are mutually exclusive. When either is set, `--status` filters the query directly (no TUI).

---

## Section 2: Data Layer (`store/`)

New enum type, read type, and query method. No schema changes required.

```go
// BranchStatus is the typed string representation of the statuses table.
type BranchStatus string

const (
    BranchStatusInProgress BranchStatus = "in_progress"
    BranchStatusMerged     BranchStatus = "merged"
    BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)

// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
    IssueSlug  string       // issues.id_slug   e.g. "ABC-42"
    Title      string       // issues.title
    BranchName string       // branches.name
    Type       string       // branches.type    e.g. "feat"
    Status     BranchStatus // statuses.name
    CreatedAt  time.Time    // branches.created_at
}

// ListBranches returns all branches joined with their issue and status.
// BranchStatusAll (empty string) returns all rows regardless of status.
func (s *Store) ListBranches(ctx context.Context, status BranchStatus) ([]BranchRow, error)
```

Note: existing methods (`InsertIssueWithBranch`, `UpdateBranchStatus`, `UpdateIssueStatus`) use `int64` status IDs and are unchanged. `BranchStatus` is introduced only for the new read path.

SQL (three-table JOIN with optional WHERE):

```sql
SELECT i.id_slug, i.title, b.name, b.type, st.name, b.created_at
FROM branches b
JOIN issues i  ON b.issue_id  = i.id
JOIN statuses st ON b.status_id = st.id
[WHERE st.name = ?]
ORDER BY b.created_at DESC
```

---

## Section 3: TUI (`tui/branch.go`)

Two new functions, following existing patterns in `tui/issue.go` and `tui/commit.go`.

```go
const (
    BranchActionNameList  = "branchList"
    BranchActionNameNew   = "branchNew"
    BranchActionNameMerge = "branchMerge"
)

// BranchActionSelect presents the list of available branch actions.
func BranchActionSelect(action *string) *huh.Group

// BranchStatusFilter presents a status filter for the branch list.
// selected is the pre-selected value ("in_progress", "merged", or "all").
func BranchStatusFilter(status *string, selected string) *huh.Group
```

`BranchActionSelect` options: `list`, `new`, `merge`.

`BranchStatusFilter` options: `in_progress` (default), `merged`, `all`.

---

## Section 4: Command (`cmd/branch.go`)

New file following `cmd/issue.go` structure.

```go
func getBranchCmd() *cobra.Command   // registers list + new + merge subcommands
func branchRunE(...)                  // shows BranchActionSelect, dispatches
func getBranchListCmd() *cobra.Command
func branchListRunE(...)              // list logic
func getBranchNewCmd() *cobra.Command
func branchNewRunE(...)               // manual-input form; reuses issueStartRunE logic
func getBranchMergeCmd() *cobra.Command
func branchMergeRunE(...)             // prints "Not yet implemented."
```

**`branchListRunE` flow:**

```
queryStatus := toBranchStatus(flags.status)  // converts "all" → BranchStatusAll

if --json:
    rows := store.ListBranches(ctx, queryStatus)
    json.NewEncoder(os.Stdout).Encode(rows)
    return

if --stdout:
    rows := store.ListBranches(ctx, queryStatus)
    if len(rows) == 0:
        fmt.Println("No branches found.")
        return
    renderLipglossTable(os.Stdout, rows)
    return

// TUI path
status := flags.status (default "in_progress")
huh filter → updates status (string value from tui option)
queryStatus = toBranchStatus(status)
rows := store.ListBranches(ctx, queryStatus)
if len(rows) == 0:
    fmt.Println("No branches found.")
    return
render bubbles/table → Run()
```

`toBranchStatus` is a small private helper in `cmd/branch.go` that maps `"in_progress"` → `BranchStatusInProgress`, `"merged"` → `BranchStatusMerged`, anything else (including `"all"` and `""`) → `BranchStatusAll`.

**`branchNewRunE` flow:**

`branchNewRunE` delegates directly to `issueStartRunE` (or the shared helper it calls). The form, SQLite write, and branch creation are identical to `git cz issue start`. No new form logic is introduced.

The toggle to tracker-picker is deferred to Step 2b. In this session `branchNewRunE` is purely manual-input. A `// Step 2b: add toggle to tracker-picker` comment marks the future insertion point.

`cmd/root.go` gains one line: `rootCmd.AddCommand(getBranchCmd())`.

---

## Section 5: Output

### TUI table (default)

Columns (in order): **Issue ID** · **Title** · **Branch** · **Type** · **Status** · **Created**

`created_at` is formatted as `2006-01-02` (date only).

Rendered with `charmbracelet/bubbles/table` (already an indirect dep). Styled headers via lipgloss. The table supports keyboard navigation (`↑/↓`, `q` to quit) out of the box. Row actions will be added in a future session.

### Plain output (`--stdout`)

Same columns and date format as the TUI table, rendered as a static lipgloss table written to stdout. No interaction, no alternate screen — output flows normally so it can be piped or captured.

### JSON output (`--json`)

Array of `BranchRow` objects, marshalled with `encoding/json`. Field names snake_case via struct tags:

```json
[
  {
    "issue_slug": "ABC-42",
    "title": "Add OAuth login",
    "branch_name": "ABC-42@feat@add-oauth-login@550e8400",
    "type": "feat",
    "status": "in_progress",
    "created_at": "2026-04-27T10:00:00Z"
  }
]
```

### Empty state

Both paths print `No branches found.` and exit cleanly (exit code 0).

---

## Section 6: File Structure

```
commitizen-go/
├── cmd/
│   ├── branch.go        ← new: getBranchCmd, branchListRunE, branchNewRunE, branchMergeRunE
│   ├── issue.go         ← extract shared issueStart logic reused by branchNewRunE
│   └── root.go          ← +1 line: register getBranchCmd()
├── store/
│   └── store.go         ← add BranchStatus, BranchRow, ListBranches
└── tui/
    └── branch.go        ← new: BranchActionSelect, BranchStatusFilter
```

---

## Section 7: Testing

### `store/` — `ListBranches`

Tests use a temp-file SQLite DB (same pattern as existing store tests):

| Test | Verifies |
|---|---|
| `TestListBranches_all` | returns all rows when status is `BranchStatusAll` |
| `TestListBranches_filterInProgress` | returns only `BranchStatusInProgress` rows |
| `TestListBranches_filterMerged` | returns only `BranchStatusMerged` rows |
| `TestListBranches_empty` | returns empty slice, no error |
| `TestListBranches_order` | rows ordered by `created_at DESC` |

### `cmd/` — non-TUI output paths

| Test | Verifies |
|---|---|
| `TestBranchList_json` | `--json` outputs valid JSON array with correct fields |
| `TestBranchList_json_emptyStore` | `--json` outputs `[]` for empty store |
| `TestBranchList_stdout` | `--stdout` outputs a non-empty string containing column headers |
| `TestBranchList_stdout_emptyStore` | `--stdout` prints "No branches found." |

### `cmd/` — `git cz branch new`

`branchNewRunE` delegates to the same helper as `issueStartRunE`, so existing `TestIssueStart_*` tests cover the shared path. No new tests are added for `branchNewRunE` beyond a smoke test confirming the subcommand is registered and wired correctly.

---

## Out of Scope

- `git cz branch merge` — deferred
- `git cz branch new` tracker-picker toggle — deferred to Step 2b
- `git cz issue list` — deferred to Step 2b (requires tracker integration)
- `git cz issue close` — deferred to Step 2b (requires tracker integration + branch merge)
- Row actions in the interactive table — wired in a future session
