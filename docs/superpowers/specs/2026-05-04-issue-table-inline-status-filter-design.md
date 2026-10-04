# Design: Inline Status Filter in Issue Table TUI

**Date:** 2026-05-04
**Status:** Approved

## Problem

`git zf issue list` currently shows a `huh` status-filter form *before* the interactive table. This is a two-step flow that breaks immersion: the user picks a status, waits for the table to load, and cannot change their mind without re-running the command.

## Goal

Collapse the status filter into the issue table TUI itself, alongside the existing text filter, so the user can switch between Open / Closed / All without leaving the table.

## Approach

Fetch all rows once (no status pre-filter). The `issueTableModel` holds the full dataset and applies status + text filters together in memory on every keystroke.

## Data Flow Changes (`cmd/issue/list.go`)

The TUI path in `runList` drops the huh pre-form:

```
// before
huh.NewForm(tui.IssueStatusFilter(&statusStr, statusStr)).Run()
rows = buildRows(ctx, infra, statusStr)
tui.IssueTableModel(rows)

// after
rows = buildRows(ctx, infra, "")            // always fetch all
tui.IssueTableModel(rows, flags.status)     // seed initial tab from --status flag
```

- `IssueStatusFilter` in `tui/issue.go` becomes dead code and is deleted.
- The `--stdout` and `--json` paths are **unchanged**: they keep their own `buildRows(ctx, infra, flags.status)` calls so the `--status` flag still works for scripting.

## Model State (`tui/issue.go`)

`issueTableModel` gains a `statusFilter` field and changes `allRows` to hold the original structs:

```go
type issueTableModel struct {
    table        btable.Model
    allRows      []store.IssueRow   // was []btable.Row
    filter       textinput.Model
    filtering    bool
    statusFilter string             // "open" | "closed" | "all"
}
```

`IssueTableModel` signature:

```go
func IssueTableModel(rows []store.IssueRow, initialStatus string) (tea.Model, error)
```

`initialStatus` empty → defaults to `"open"`.

## Filtering Logic

A single `applyFilters(rows []store.IssueRow, status, text string) []btable.Row` replaces `filterIssueRows`:

| `status` | Matches row when… |
|---|---|
| `"open"` | `r.Branch == nil` or `r.Branch.Status == BranchStatusInProgress` |
| `"closed"` | `r.Branch != nil && r.Branch.Status == BranchStatusMerged` |
| `"all"` | always |

The text filter is applied after the status filter (case-insensitive substring match across all columns, same as today).

Tracker-sourced rows with no local branch fall under `"open"` — correct, since the tracker returned them as active issues.

## View & Keyboard

Footer renders status tabs with lipgloss (active tab bold, inactive dimmed):

```
[ Open ]  Closed  All      Press / to filter · tab: status · q to quit
```

While text-filtering:

```
[ Open ]  Closed  All      /query_here  (esc: clear  enter: confirm)
```

| Key | Action |
|---|---|
| `tab` | Cycle status: open → closed → all → open |
| `/` | Enter text-filter mode |
| `esc` | Clear text filter / exit filter mode |
| `enter` | Confirm text filter |
| `q` / `ctrl+c` | Quit |

`tab` is a no-op while in filtering mode.

## Files Affected

| File | Change |
|---|---|
| `cmd/issue/list.go` | Remove huh pre-form; pass `""` status to `buildRows`; pass `flags.status` to `IssueTableModel` |
| `tui/issue.go` | Update `issueTableModel`, `IssueTableModel` signature, add `applyFilters`, delete `IssueStatusFilter` |

## Out of Scope

- Applying the same pattern to `BranchTableModel` (separate task if desired).
- Re-fetching from tracker/store on status change (in-memory filtering is sufficient).
