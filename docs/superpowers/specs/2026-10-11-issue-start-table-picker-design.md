# Design: the issue table picks the issue to start

**Date:** 2026-10-11
**Status:** draft, for review

## Overview

`git zf issue start` asks "fetch from the tracker?", then opens one of two
huh selects: the tracker's open issues, or the open issues stored in the
repository plus "New issue…". The select scrolls badly (huh pins the cursor
row at the top of a sized list, so typing a filter can leave one visible
match) and it shows less than `git zf issue list` does: no project, no
branch, no status, no "mine".

`git zf issue list` already has the table that does all of that, with show
and edit under the cursor. This spec makes that table the picker of `issue
start` and gives the list an `s` key that starts the issue under the cursor.
The two commands share one code path: build the rows, pick a row, start
from the row.

**Decisions:**

- The table replaces the tracker toggle and both huh pickers. The merged
  rows show tracker and repo issues together, as the list does. `branch
  new` uses the same table over the branches and the repo issues alone: it
  stays the offline, manual-first command, and never asks the tracker.
- "New issue…" becomes the `n` key of the table in start mode; it opens the
  manual form as today.
- A row that already has a branch is offered like any other. Starting it
  reaches the existing branch-conflict loop (checkout / variant / abort).
- The start-mode table has no show and no edit: their actions stay in
  `cmd/issue`. (Out of scope; can come later by moving `renderIssue` and
  `editIssue` next to the rows.)
- A tracker that cannot be listed is the warning `issue list` prints today;
  the table then shows the repository's rows. The "tracker error" note goes.

## Section 1: the table (`tui/issue.go`)

`IssueTableModel` takes a mode, start or list, through `IssueActions`:

```go
type IssueActions struct {
	Show func(slug string) (string, error)
	Load func(slug string) (title, description string, err error)
	Edit func(slug, title, description string) (string, error)
	// Start is set when the table picks the issue to start: enter picks
	// the row under the cursor, n picks "new issue". Both quit.
	Start bool
}
```

Keys, both modes: `s` records the slug of the row under the cursor and
quits. Start mode only: `enter` does what `s` does, `n` records
`IssueRecordNew` and quits; show and edit are off (their actions are nil).
List mode keeps enter for show and `e` for edit. The hint line names the
keys of the mode.

The picked slug is read after the program returns:

```go
// PickedIssue is the slug picked with s, enter or n, "" when the table was
// quit without picking. IssueRecordNew means "new issue".
func PickedIssue(m tea.Model) string
```

## Section 2: the flow (`cmd/issueflow`)

### Rows

`buildAllRows`, `mergeTrackerIssues`, `mergeRepoIssues`, `buildFromBranches`
and `buildRows` move from `cmd/issue/list.go` to `cmd/issueflow/rows.go`,
unchanged, as:

```go
// ListRows lists the tracked branches, the tracker's open issues and the
// issues stored in the repository, merged into one row per issue; status
// ("open", "closed", "all", "" for every row) filters them as the list's
// tab does. With deps.Flags.Tracker false the tracker is not asked, nor
// the mirror reconciled: the rows are the branches and the repo issues.
func ListRows(ctx context.Context, deps StartDeps, status string) ([]issue.Row, error)
```

`IssueStartFlags.TrackerFirst` is renamed `Tracker`: "list the tracker's
issues". `issue start` and `issue list` set it; `branch new` leaves it
false, and so works offline, as today.

`StartDeps` gains `Mirror *issue.Mirror`, built by `BuildStartDeps` from
the tracker it already builds (`mirrorOf` moves to `issueflow` with the
helpers it needs, `fetchIssues` and `reconcileIssues`; `cmd/issue` calls
them from there). `issueListInfra` keeps its tracker, stderr, client and
mirror, so the move is the file, not the data.

### Pick, then start

`RunIssueStart` splits in two:

```go
func RunIssueStart(ctx context.Context, deps StartDeps, p StartPrompter) error {
	// types, as today
	rows, err := ListRows(ctx, deps, issue.StateOpen)
	row, err := p.PickRow(ctx, rows)
	if row == nil {
		picked, err = newIssueFromUser(ctx, deps, p, types) // PickIssueFromUser + Prepare, as today
	} else {
		picked, err = issueFromRow(ctx, deps, p, row, types)
	}
	return StartIssue(ctx, deps, p, picked)
}

// StartRow starts the issue of a list row: `s` in `issue list`.
func StartRow(ctx context.Context, deps StartDeps, p StartPrompter, row *issue.Row) error

// StartIssue is the tail of today's RunIssueStart: base branch, worktree,
// confirm, create, record, push, tracker status.
func StartIssue(ctx context.Context, deps StartDeps, p StartPrompter, picked *issue.Issue) error
```

`issueFromRow` converts a row:

- `row.State != ""`: the row is a repo issue. `issue.Resolve` by its slug,
  then today's record path: an imported record goes through
  `fromImportedRecord` (the one-issue type form), any other through
  `checkRecordType` and `issueFromRecord`.
- otherwise the row is a tracker issue: `PickIssueFromTracker` with the one
  issue `{TrackerType: cfg type, ID: slug, Subject: title}`, which is the
  type form; the result starts as a tracker issue, as today.

A row's branch is ignored here: the branch name is built from the issue and
the conflict loop handles an existing one.

### The prompter

`StartPrompter` loses `PickUseTracker`, `PickIssueFromRepo` and
`NotifyTrackerError`, and gains:

```go
// PickRow opens the issue table over rows. A nil row with a nil error
// means "new issue": the flow falls through to PickIssueFromUser.
PickRow(ctx context.Context, rows []issue.Row) (*issue.Row, error)
```

`HuhStartPrompter.PickRow` runs `tui.IssueTableModel(rows, "open",
IssueActions{Start: true})` in a bubbletea program and maps the picked slug
back to its row. `branch new` and `issue start` differ by the `Tracker`
flag alone.

## Section 3: the commands

`issue start` and `branch new` call `RunIssueStart` as today.

`issue list`: `runList` returns the picked slug, "" when none. On a slug,
`issueListRunE` builds `StartDeps` and calls `StartRow` with the row, after
the table's program has exited: the huh forms that follow need the
terminal. The stdout and JSON outputs never pick.

Show and edit from the list are unchanged.

## Section 4: tests

- `tui/issue_test.go`: `s` quits with the row's slug in both modes; in
  start mode `enter` picks and `n` picks new; in list mode enter still
  shows. `PickedIssue` on a table quit with `q` is "".
- `cmd/issue/start_prompter_test.go`: `scriptedStartPrompter` scripts
  `PickRow` by slug (`PickSlug`, with `PickNew` for nil) and records the
  rows it was offered; `UseTracker`, `IssueFromRepo`, `NotifyErr*` go.
- The start E2E suites (`start_e2e_test.go`, `start_record_e2e_test.go`,
  `mirror_e2e_test.go`): the scenarios keep their names and assertions;
  their scripts pick by slug. The tracker-error fallback tests become "the
  tracker is down: the table is offered the repository's rows". A `branch
  new` case asserts the fake tracker was never called.
- `cmd/issueflow`: `StartRow` from a tracker row and from a repo row on the
  rig; `ListRows` keeps `list_record_test.go`'s cases, which move with it or
  stay in `cmd/issue` through `issueflow.ListRows`.
- `cmd/issue/list_record_test.go`: `runList` returns "" for stdout and JSON.

The CLAUDE.md "Testing the start flow" paragraph is updated: the picker is
the table, scripted by slug.

## Out of scope

- Show and edit inside the start-mode table.
- Starting an issue from the stdout or JSON listing.
- A fix for huh's `Select` scrolling itself. The remaining huh selects
  (type, base branch, status) are short, and the two issue pickers lose
  their fixed height (section 5).

## Section 5: the huh pickers that remain

huh v1.0.0 pins the cursor row at the top of a `Select` given a `Height`,
so the matches above the cursor are never visible: filtering with the
cursor low shows one row. `IssueRecordPicker` (`tui/issue_record.go`)
keeps that height and is the picker of `issue show`, `edit`, `comment`,
`label` and `close` run without an ID. Its `Height(...)` goes, with the
constant: the list renders in full and every match shows. `IssueTrackerPicker`
is offered one issue from now on; its `Height(...)` goes too, with the
constant, so the form no longer pads nine blank lines.
