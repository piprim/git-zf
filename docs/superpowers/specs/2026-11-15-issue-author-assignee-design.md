# Issue author and assignee — design

## Intent

From git-zf, see who created an issue (in the tracker or in git-zf) and who
the tracker assigns it to, without opening the tracker.

## Goals

- `issue show` prints an `Author:` line for every issue and an
  `Assigned to:` line when someone is assigned.
- `issue list` has an `Assigned` column: the first 8 characters of the
  assignee.
- Both work offline: they read the repository, never the tracker.

## Non-goals

- No command to reassign or unassign. The existing "Assign to me?" of
  `issue new` and the export's `AssignToMe` stay as they are.
- No mapping between git identities and tracker logins.
- The `m` filter of `issue list` keeps reading `AssignedToMe`.

## Anti-goal

A read-only command (`issue show`, `issue list`) must not start calling the
tracker or reconciling.

## Approaches

- **A. In the chain, with the tracker snapshot (picked).** The `tracker_seen`
  op carries the tracker's author and assignees; `Fold` exposes them. Offline,
  no new op type, older binaries ignore the new fields.
- **B. Ask the tracker when showing.** No chain change, but `issue show` needs
  the network and breaks the rule above. Dropped.
- **C. New op types.** Same result as A with more ops and fold cases. Dropped.

## Design

### Tracker

`tracker.Issue` gains `Author string` and `Assignees []string`: logins on
GitHub and Forgejo, the user's name on Redmine. `Clean` passes both through
`text.Line`, cloning `Assignees` first as it does `Labels`. The three adapters and the fake fill them wherever they build an
`Issue` (listing and single read). `AssignedToMe` is unchanged.

### Chain

- `tracker_seen` op: `tracker_author` (the `Op` field `add_comment` already
  uses) and a new `assignees` array.
- `TrackerSnapshot` gains `Author` and `Assignees`; `sameSnapshot` compares
  them, so a reassignment in the tracker's UI writes a new snapshot on the
  next reconcile.
- `snapshotOf` copies the assignees in the tracker's order, unsorted (unlike
  the labels): the first one is the one the table shows.
- `DecodeOp` passes each assignee through `text.Line`, as it does the labels.
- Backfill needs no code of its own: `syncRecord` writes a snapshot whenever
  `sameSnapshot` says it differs, so an open issue imported before this
  change gets its author and assignees on the next `issue sync`. Do not
  touch `contentSettled`, `recordSettled` or `pullComments`: a settled issue
  must keep costing no `GetIssue` and no `ListComments`. Known limit: an
  issue already closed on the tracker is not in `ListIssues` and keeps an
  empty author.

### Fold

- `Record.Author`: the snapshot's author for an issue born in the tracker
  (`Tracker.Born`), else the `create` op's git identity. An issue created in
  git-zf and exported keeps its git identity.
- `Record.AssignedTo() []string`: the snapshot's assignees when there are
  any, else the local `Assignee` (the `assign` op) when set, else none.
  Known limit: an issue assigned locally and then unassigned in the tracker
  still shows the local identity.

### Display

`issue show` (`renderRecord`):

```
142  Crash on empty config
State: open   Type: fix   Created: 2026-11-02 10:14
Author: alice
Assigned to: bob, carol
```

An issue created in git-zf shows `Author: Pi <pi@example.org>`.
`renderTrackerIssue` (a tracker issue not stored in the repository) prints the
same two lines from the `tracker.Issue`.

The `Author:` line is left out when the author is unknown (an old import not
yet synced, a deleted tracker user), as `Labels:` and `Assigned to:` are.

`issue list`: `issue.Row` gains `Assignee string`, the first of
`AssignedTo()` (or of `tracker.Issue.Assignees` for a row that comes straight
from the tracker), in full: `issue list --json` prints the row as is.
`cmd/issueflow/rows.go` copies it wherever it fills a row: both places of
`mergeTrackerIssues` and the branch-row copy of `mergeRepoIssues`, which
does not go through `RecordRow`.

A new `Assigned` column, 8 wide, shows the first 8 characters of
`Row.Assignee`, cut where the cells are built (`RowCells`) and by runes, not
bytes: a Redmine name or a git identity may hold multi-byte characters. A
git identity is cut as it is (`Pi <pi@e`). Empty when nobody is assigned.
With several assignees only the first is shown. The stdout listing (no TTY)
gets the column too, headed `ASSIGNED` like its other headers, and the
column is added to the Long help of `issue list`.

`issue show --json` encodes the `Record`: `author` is new, `assignee` stays
the local `assign` identity and the tracker's assignees are under
`tracker_seen.assignees`.

## My calls (not discussed)

- Author line without the tracker type suffix: `Author: alice`.
- Several assignees: the table shows the first one only.
- Local `assign` is the fallback when the tracker names nobody.
- Column placed after `Issue Status`, before `Created`.

## Tests

- `TestFold`: author from create op, author from snapshot when born in the
  tracker, `AssignedTo` in its three cases.
- `issue/mirror_test.go`: import records author and assignees; a
  reassignment on the fake (`Edit`) reaches the record; a snapshot without
  author is backfilled; a reconcile that follows with nothing changed writes
  no op and makes no `GetIssue` or `ListComments` call.
- Adapter tests: `Author` and `Assignees` parsed.
- `cmd/issue`: `issue show` lines, with and without an author; a branch row
  merged with a repo issue or a tracker issue keeps `Row.Assignee`
  (`TestBuildRows_TrackerAndRepository`).
- `issue.RowCells`: the 8-character cut, with a multi-byte name; `tui`: the
  `Assigned` column.

## Left to the builder

Field naming inside the adapters and the fake's knobs for author and
assignees.
