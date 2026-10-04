# Issue List Implementation Design

> **For agentic workers:** implement this spec via `superpowers:writing-plans` → `superpowers:subagent-driven-development`.

**Goal:** Implement `git zf issue list` — a command that shows open issues assigned to the user, enriched with local branch data, following the tracker-first / local-fallback pattern.

**Architecture:** Approach A — tracker is the primary data source when configured; local store is the fallback. A new `IssueRow` struct composes an issue (slug + title + tracker status) with a `*BranchRow` pointer (nil when not yet started locally). All merge logic lives in a single testable `buildIssueRows` function.

**Tech Stack:** Go, `charmbracelet/huh` (status filter form), `charmbracelet/bubbles/table` (interactive TUI), `charmbracelet/lipgloss/table` (stdout), `modernc.org/sqlite` (store).

---

## Assumptions

- One branch per issue is enforced at the application level (separate follow-up). This spec is designed assuming that invariant holds, so `ListBranchesByIssueSlugs` returns at most one `BranchRow` per slug.

---

## Data model

### `store.IssueRow`

```go
// IssueRow is the unified display row for issue list.
// It composes an issue identity with an optional local branch.
type IssueRow struct {
    IssueSlug     string
    Title         string
    TrackerStatus *string    // nil → display "N.A."
    Branch        *BranchRow // nil → not started locally; display "∅" for branch fields
}
```

`BranchRow` already carries `BranchName`, `Status` (local: `in_progress`/`merged`), and `CreatedAt`. No fields are duplicated.

### New store method

```go
// ListBranchesByIssueSlugs returns a map[id_slug]BranchRow for the given slugs.
// Uses a single SELECT … WHERE i.id_slug IN (…). Returns an empty map for an empty input.
func (s *Store) ListBranchesByIssueSlugs(ctx context.Context, slugs []string) (map[string]BranchRow, error)
```

Existing `ListBranches` is reused unchanged for the local fallback path.

---

## Command structure

### Flags

```
git zf issue list [flags]

--status string   filter: open, closed, all (default: open)
--stdout          print table to stdout without TUI
--json            print JSON array to stdout
```

In tracker mode `--status` maps to the tracker query filter. In local mode it maps to the store status filter (`in_progress` → open, `merged` → closed, empty → all).

### Display

Columns are always the same six regardless of whether a tracker is configured:

| Issue ID | Title | Branch | Local Status | Tracker Status | Created |
|----------|-------|--------|--------------|----------------|---------|

- `Branch`, `Local Status`, `Created` show `"∅"` when `Branch` is nil (issue not started locally).
- `Tracker Status` shows `"N.A."` when `TrackerStatus` is nil (no tracker configured).

---

## Data flow

### Tracker path (tracker configured)

1. `tracker.ListIssues(ctx)` → `[]tracker.Issue` (filtered by status flag).
2. Extract `id_slug` values → `store.ListBranchesByIssueSlugs(ctx, slugs)` → `map[string]BranchRow`.
3. Merge: one `IssueRow` per tracker issue, `Branch` set from map lookup (nil if absent).
4. `TrackerStatus` set to `&issue.Status` for each row.

### Local fallback path (no tracker, or tracker error)

1. `store.ListBranches(ctx, status)` → `[]BranchRow`.
2. Convert each to `IssueRow`: `IssueSlug` and `Title` from `BranchRow`, `TrackerStatus` left nil, `Branch` pointing to the row.

### Merge function

```go
// issueListInfra groups the dependencies of buildIssueRows.
// Tracker is nil when no tracker is configured.
// Stderr receives the fallback warning when the tracker call fails.
type issueListInfra struct {
    Tracker tracker.Tracker
    Store   *store.Store
    Stderr  io.Writer
}

// buildIssueRows fetches and merges tracker + store data into display rows.
// When deps.Tracker is nil the local path is used directly.
// Falls back to local path with a warning on deps.Stderr if the tracker call fails.
func buildIssueRows(ctx context.Context, deps issueListInfra, status string) ([]IssueRow, error)
```

---

## Command wiring (`cmd/issue.go`)

```
issueListRunE
  └─ runIssueList(ctx, w, deps issueListInfra, flags)
       ├─ --json   → json.Encode(rows)
       ├─ --stdout → renderIssueTable(w, rows)      lipgloss plain table
       └─ TUI      → huh status-filter form
                   → buildIssueRows(ctx, deps, status)
                   → runIssueTableTUI(rows)          bubbles/table
```

`issueListRunE` wires the Cobra command: opens the store, constructs the tracker (nil if not configured), assembles `issueListInfra`, delegates to `runIssueList`. `runIssueList` accepts `io.Writer` and the deps struct so it can be unit-tested without a real repo.

The interactive TUI follows the `branchTableModel` pattern from `cmd/branch.go` exactly.

---

## Error handling

| Situation | Behaviour |
|-----------|-----------|
| Tracker unavailable (network / auth) | Warn to stderr, fall back to local path |
| Local store open fails | Hard error returned to Cobra |
| Tracker returns issues, no local branches | Valid — all `Branch` fields show `"∅"` |
| `ListBranchesByIssueSlugs` called with empty slice | Return empty map, skip query |

---

## Testing

### `buildIssueRows` (unit, `cmd/issue_test.go`)

- **Tracker path, partial match:** stub tracker returns 3 issues, store has branches for 2 → verify correct `IssueRow` slice (2 non-nil `Branch`, 1 nil `Branch`, all `TrackerStatus` set).
- **Local fallback (nil tracker):** store returns branches → all `TrackerStatus` nil, all `Branch` non-nil.
- **Tracker error → fallback:** stub tracker returns error → result matches local path output, warning written to `deps.Stderr`.

### `runIssueList` stdout / JSON (table-driven, `cmd/issue_test.go`)

Inject a `bytes.Buffer` as `w`, verify output contains expected slugs and `"N.A."` / `"∅"` sentinels. Same pattern as `runBranchList` tests.

### `ListBranchesByIssueSlugs` (integration, `store/store_test.go`)

Real in-memory SQLite DB. Seed issues + branches, call with a mix of matching and non-matching slugs, verify map contents and empty-input short-circuit.
