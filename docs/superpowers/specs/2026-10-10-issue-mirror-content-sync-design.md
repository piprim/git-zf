# Design: the issue mirror syncs title, description, labels and comments

**Date:** 2026-10-10
**Status:** approved design, not implemented
**Builds on:** `2026-10-06-issue-tracker-mirror-design.md`

## Overview

The mirror copies title and description once, when an issue is imported or
exported, and afterwards syncs open/closed only (roadmap item 1.b). This spec
makes the record a full copy of the tracker issue: title, description, labels
and comments travel both ways, with the same mechanism open/closed uses today.

For each linked record the reconcile compares three versions of the issue:

- **T**, the issue as the tracker shows it now, from the project listing, or
  read with one call when the record is open and not listed;
- **R**, the record, the fold of its chain;
- **L**, what the tracker showed the last time it was read: the last
  `tracker_seen` op of the chain, the snapshot.

The side that differs from L moved, and the other follows. That is the rule
`tracker_state` already applies to open/closed.

**Decisions taken during brainstorming:**

- **Direction:** both ways. The record is an offline copy of the tracker
  issue. Rejected: export only (the web UI's edits would stay there), import
  only (the local `edit`, `label` and `comment` would stay local).
- **Conflict on title or description** (both sides changed, to different
  values): the repo wins, with a warning naming the tracker's value that lost.
  First chosen: last writer wins; dropped because the tracker's updated time
  moves on any activity, a comment or a label, not on the edit of that field,
  and GitHub has no event for a description edit, so a precise per-field time
  is not available everywhere. Rejected: the tracker wins (a local edit
  silently undone), keep both and warn each run (needs a human to clear it).
- **Trigger:** `edit`, `label` and `comment` reach the tracker at once with a
  per-issue sync, like `new` and `close <id>`; the next full reconcile retries
  what failed. Rejected: waiting for the next `list` or `sync`.
- **A label the tracker repository lacks** is created there, with a default
  colour. Rejected: skip it with a warning.
- **Redmine has no labels: its status is the label.** The record of a Redmine
  issue carries exactly one label, the status name. `issue label +Resolved
  -New` is a status change. Rejected: no labels on Redmine.
- **The label picker offers the tracker's labels.** With the mirror on, the
  `issue label` form is a multi-select over the labels the tracker defines,
  united with the issue's own. On Redmine it is a single pick among the
  statuses, with no free text (added on review: a multi-select invites the
  user to break the one-status rule).
- **A label the tracker refuses by rule is dropped from the record**, with one
  warning (added on review: without it a `+urgent` on Redmine is pushed, and
  refused, on every run for ever). Only a rule rejection reverts; a transport
  or auth failure keeps the local change for the next run.
- **Who moved:** one snapshot op (approach 1). Rejected: timestamps only, no
  baseline (after a pull the local op is newer than the tracker, so the next
  run pushes it back: not idempotent); the tracker's event log (three APIs,
  Forgejo's timeline is thin, and the record side still needs a baseline).

## Section 1: data model

Three new things on the record, all ops in the issue's own chain, all skipped
by an older binary.

### `tracker_seen`, the snapshot

| Field | Content |
|---|---|
| `title` | the tracker's title |
| `description` | the tracker's description |
| `labels` | the tracker's labels, a new list field on `Op` |
| `updated` | the tracker's updated time, RFC 3339 |

Written when the reconcile reads the issue's content and the final values
differ from the last snapshot, and once at import and at export. Fold:
`Record.TrackerSeen *TrackerSnapshot` with the same four fields, nil for a
record that never got one. The existing `tracker_state` op stays as it is:
open/closed keeps its own baseline.

A record without a snapshot is treated as if the snapshot equalled the record:
on its first run the tracker's content is pulled wherever it differs.

### `add_comment` gains three optional fields

For a comment imported from the tracker: `tracker_comment_id`,
`tracker_author` (the tracker login), `tracker_at` (the tracker's time, RFC
3339). `at` and `author` keep meaning who wrote the op into the chain and
when.

Fold: a `Comment` shows the tracker author and time when present, and carries
`TrackerID`. A second `add_comment` with the same `tracker_comment_id` is
dropped, so two clones importing the same comment offline fold to one. A
comment with no `TrackerID` is one the tracker has not got yet.

### `link_comment`

For a local comment that reached the tracker: `comment` (the op ID of the
local `add_comment`), `tracker_comment_id`. Fold: sets `TrackerID` on that
comment.

### Tracker types

`tracker.Issue` gains `Labels []string`, `UpdatedAt time.Time` and `Closed
bool`. A new type:

```go
// Comment is one comment of a tracker issue.
type Comment struct {
	ID        string
	Author    string // the tracker login
	Body      string
	CreatedAt time.Time
}
```

Both are cleaned of control characters, as `Issue` is today.

### What a Redmine label is

On Redmine, `Issue.Labels` holds one name: the status. The record of a Redmine
issue therefore carries the status name as its only label, and a label change
is a status change. `issue list` shows the same name in the labels and in the
tracker status column.

## Section 2: the per-field rules

For each linked record. A record closed on both sides and not listed is
skipped, as today.

### Order of the steps within one run

1. **State**, the eight-row table of the first spec, unchanged. If it pushed
   (a close, a reopen), the issue is read again, so the content steps see the
   status it produced and, on Redmine, pull it as a label.
2. **Content**: title, description, labels, comments, below. If they pushed,
   the issue is read again.
3. **State again**, on the issue as read last. On Redmine a status pushed
   through a label may have closed or reopened the issue; the record follows
   in the same run. On GitHub and Forgejo this pass is a no-op and costs no
   call.
4. **Snapshot**: one `tracker_seen` is appended if the final title,
   description, labels or updated time differ from the last snapshot. It
   records the tracker as read last, after the final re-read, so that a value
   the tracker changed as a side effect is never pushed back.

The one change to the state step: for a not-listed open record, `GetIssue`
replaces `IsIssueClosed`, so that the issue's content is in hand too and a
closed-on-tracker issue still gets its last edits.

### Nothing moved, no cost

When T's updated time equals the snapshot's, the record's title, description
and labels equal the snapshot's, and every record comment has a tracker ID,
the content steps do nothing: no op, no call. A second run with nothing
changed appends no op and makes no call beyond the listing.

### Title and description, each on its own

| T vs L | R vs L | Action |
|---|---|---|
| equal | equal | nothing |
| differs | equal | pull: append `set_title` / `set_description` with T |
| equal | differs | push |
| differs | differs, R equals T | nothing but the snapshot |
| differs | differs, R differs from T | the repo wins: push, and one warning names the issue, the field and T's value that lost |

A push of either field is one `UpdateIssue` call carrying both final values.

### Labels, per name over the union of L, R and T

| In L | In R | In T | Action |
|---|---|---|---|
| no | no | yes | pull: `add_label` |
| yes | yes | no | pull: `remove_label` |
| no | yes | no | push |
| yes | no | yes | push |
| otherwise | | | nothing: both sides agree |

A push is one `SetLabels` call with the final set, after every name is
resolved. GitHub and Forgejo create the names the repository lacks. Redmine
requires exactly one name that is a status: anything else is
`tracker.ErrLabelRule`, wrapped with the reason.

When `SetLabels` returns `ErrLabelRule`, the record's labels cannot exist on
the tracker: the reconcile pulls T's labels instead (the `add_label` /
`remove_label` ops that make R equal T) and writes one warning naming the
issue, the rule and the names dropped. The loop closes in one run. Any other
error is a warning and leaves the record as it is: the next run pushes again.

### Comments, append-only

- The tracker's comments are fetched with `ListComments` only when T's
  updated time moved since the snapshot.
- **Pull:** each tracker comment whose ID no record comment carries is
  appended as an `add_comment` with the tracker's author, time and ID, oldest
  first.
- **Push:** each record comment without a tracker ID is posted with
  `AddComment`, then linked with `link_comment`.
- A comment edited or deleted on the tracker is ignored. A comment the review
  flow posted on the tracker is a tracker comment like any other and gets
  pulled.

### Counts

`MirrorResult.Pulled` and `Pushed` count issues with at least one pulled or
pushed field, state and comments included. The `issue sync` line stays one
line.

### Properties

- **Idempotent.** A second run with nothing changed appends no op and makes no
  tracker write.
- **Convergent.** Two clones that see the same tracker change append the same
  ops; the chain merge and the comment dedupe fold them to one record.
- **Restartable.** Each step can be redone after a failure.

## Section 3: tracker interface

Five new methods and one changed, implemented by GitHub, Forgejo, Redmine and
the fake.

| Method | GitHub / Forgejo | Redmine |
|---|---|---|
| `GetIssue(ctx, id) (Issue, error)` | `GET …/issues/{n}`: title, description, state, labels, updated time | `GET /issues/{id}.json`: subject, description, `status.is_closed`, updated time; `Labels` is the status name |
| `UpdateIssue(ctx, id, title, description) error` | `PATCH …/issues/{n}` | `PUT /issues/{id}.json` with subject and description |
| `SetLabels(ctx, id, labels []string) error` | creates the missing labels with a default colour, then `PUT …/issues/{n}/labels` with the full set | exactly one name, a status: `UpdateIssueStatus`; else `ErrLabelRule` naming the rule |
| `ListLabels(ctx) ([]string, error)` | `GET …/labels`, every page | `ListStatuses` |
| `ListComments(ctx, id) ([]Comment, error)` | `GET …/issues/{n}/comments`, every page, oldest first | `GET /issues/{id}.json?include=journals`, the journals with notes |
| `AddComment(ctx, id, body) (Comment, error)` | as today, now returning the comment with its ID | as today; the reply has no journal ID, so the adapter reads the journals back and takes the last one with that body |

- `ListProjectIssues` and `ListIssues` fill `Labels`, `UpdatedAt` and
  `Closed` from the payloads they already decode. `IsIssueClosed` stays for
  `branch prune`.
- `tracker.ErrLabelRule` is the sentinel a `SetLabels` returns when the set
  breaks a rule of the tracker, as opposed to a transport or auth failure.
- The single-label rule is an optional interface, `interface{ SingleLabel()
  bool }`, that Redmine and the fake implement; `tracker.SingleLabel(t)` does
  the assertion and is false for GitHub and Forgejo. The label command reads
  it to choose its form.
- The `AddComment` signature change touches one caller, the review
  transition, which ignores the result.
- The fake keeps a comment list per issue and a label catalogue, records
  `UpdateIssue`, `SetLabels` and `AddComment` calls, and hands out increasing
  comment IDs. A test mutates its issues directly to play the tracker's web
  UI. A `StatusIsLabel` switch makes it behave like Redmine: `Labels` is the
  status, `SetLabels` sets it or returns `ErrLabelRule`, `SingleLabel` is
  true, and a closing status closes the issue.

## Section 4: commands

| Command | Mirror on |
|---|---|
| `issue edit` | after the push, one per-issue sync: the tracker gets the new title or description at once |
| `issue comment` | same per-issue sync: the comment is posted and linked at once |
| `issue label` | the form is a multi-select over the tracker's labels united with the issue's own, the issue's labels preselected, plus a text field for a new name; the diff gives the `+`/`-` ops; then the same per-issue sync. On a single-label tracker (Redmine) the form is one pick among the statuses, the current one preselected, no free text, and `+`/`-` tokens that leave the record with anything but exactly one status are refused before any op is written. When the tracker does not answer, today's text form, unchecked: the reconcile applies the rule later |
| `issue show` | prints the labels and comments as today; a comment from the tracker shows its tracker author and time |
| `issue list`, `new`, `close <id>`, `close`, `sync` | the full reconcile, which now carries content. Nothing changes in what triggers it |
| `issue start`, hooks, `commit` | no change |

- The per-issue sync is `Mirror.SyncOne(ctx, c, id)`: `GetIssue`, then the
  function the reconcile applies to one record (the four steps of section 2),
  then `Push`. The reconcile is that function in a loop over the listing. One
  code path for both.
- A failed per-issue sync is a warning; the next reconcile retries from the
  chains.
- `recordPrompter.LabelChanges` becomes `LabelChanges(ctx, choices, current
  []string, single bool) ([]string, error)` and returns the final set; the
  scripted prompter of the tests returns a canned set. With the mirror off,
  or when `ListLabels` fails, `choices` is nil and the form is today's text
  line.

## Section 5: errors and known limits

- A tracker that cannot be listed or read ends the run before any write, as
  today. A failure on one issue is one warning line; the run goes on.
- A content conflict is one warning naming the issue, the field and the value
  that lost. A label set the tracker refuses by rule is one warning naming
  the rule and the names dropped, and the record takes the tracker's labels:
  a local `+urgent` on Redmine is lost, once, with its warning.
- A comment posted whose `link_comment` then fails to be written locally is
  posted again by the next run: a duplicate on the tracker, same class as the
  orphaned export of the first spec. Needs a local git write to fail.
- A comment edited or deleted on the tracker is not reflected. A local
  comment cannot be edited either.
- A label renamed on GitHub or Forgejo is seen as one removed and one added.
  The record follows.
- A record from before this change has no snapshot: on its first run the
  tracker's content is pulled wherever it differs from the record, and a
  local edit made before that run on a field the tracker also shows
  differently is lost to the pull. The README says to run `issue sync` once
  after upgrading, before editing.
- The listing is still fetched in full on every run.
- On Redmine, posting a comment costs one extra call to read its journal ID
  back.

## Testing

| Level | File | Covers |
|---|---|---|
| Fold | `issue/record_test.go` | `tracker_seen`; `add_comment` with tracker fields and the dedupe by tracker comment ID; `link_comment`; unknown fields ignored |
| Reconcile | `issue/mirror_test.go` | title and description: the five rows each, the conflict warning; labels: the four moves and the agreed case, a rule rejection reverts the record in one run and a transport failure does not; comments: pull, push and link, nothing twice; the shortcut when nothing moved; the snapshot after a push; a record without snapshot; two clones pulling the same comment offline then merging to one; state, content, state on the fake with `StatusIsLabel` |
| Per-issue sync | `issue/mirror_test.go` | `SyncOne` on one record, the others untouched |
| Adapters | each adapter's test file | the six calls against `httptest`; Forgejo and GitHub label creation; Redmine status-as-label, the "exactly one" error and the journal read-back |
| Fake | `tracker/fake` | comments, labels, `StatusIsLabel` and the recorded calls |
| Commands | `cmd/issue/mirror_e2e_test.go` | `edit`, `comment` and `label` reach the fake at once; the label picker's choices and preselection; on the single-label fake the single pick and the refused `+`/`-` tokens; `show` prints a tracker comment's author |
| Close flow | `close_mirror_e2e_test.go` | a close on the fake with `StatusIsLabel` gives the record the closing status as its label in one run |

Every assertion sits in its own `t.Run`.

## Documentation

- `docs/issue-refs.md`: the three ops, the snapshot, the comment link.
- README: what is mirrored, the Redmine status-as-label rule, the label
  picker, the one-time `issue sync` after upgrading.
- `CLAUDE.md`: the new cases in "Testing the tracker mirror".
- ROADMAP: item 1.b done.

## Out of scope

- Editing or deleting a comment, on either side.
- Author and assignee (roadmap item 2).
- An "updated since" query on the listing.
