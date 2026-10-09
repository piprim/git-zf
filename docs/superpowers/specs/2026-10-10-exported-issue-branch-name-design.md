# Design: an exported issue is named after its tracker number

**Date:** 2026-10-10
**Status:** draft, for review
**Amends:** `2026-10-06-issue-tracker-mirror-design.md`, section 2 ("Display ID and lookup")
**Precedes:** `2026-10-09-issue-mirror-several-projects-design.md` (its plan is adjusted below)

## Overview

With the mirror on, `git zf issue new` creates the tracker issue at once:
the record is linked to, say, `#11` before any branch exists. `git zf issue
start` then names the branch `edcac55@test@…`, after the record's short
hash, and `refs/zf/branches/edcac55` holds its chain. The first mirror spec
chose that on purpose, so that a record exported *after* a branch was
started keeps matching its branch. The common case is the other one, and
there the short hash is noise: the tracker, the teammates and the commit
messages all know the issue as `11`.

This spec makes a mirrored issue display, and name its branches, by its
tracker number whether it was born in the tracker or exported to it. The
only rule that changes is the record's display ID. A branch started before
the export keeps working through the record ID its chain carries.

The commit-message side of the same report (`Refs #edcac55`) is fixed
separately, by the tracker-number lookup in the commit hint: a branch named
before the export still refers to `#11`.

**Decisions:**

- The display ID of any linked record is its tracker number. "Born" no
  longer matters for naming; it still marks where the issue came from.
- A branch started under the short hash is not renamed. It is joined to its
  record by the record ID on its chain, as today.
- `issue close <id>` looks for in-progress branches under both slugs the
  record may have had.

## Section 1: display ID

`issue/record.go`:

```go
// DisplayID is the ID shown to users and used in branch names: the number
// of the tracker issue the record is mirrored with, born there or exported
// to it; the short hash otherwise.
func (r *Record) DisplayID() string {
	if r.Tracker != nil {
		return r.Tracker.ID
	}

	return r.ShortID()
}
```

A tracker number is unique in its project, and a record is linked to one
tracker issue, so two records cannot share a display ID within one project.

`Resolve` already finds a linked record by its number: no change.

## Section 2: what follows

- **`issue start`.** A pick from the repository's open issues goes through
  `issueFromRecord`, which already names the branch by `DisplayID`: an
  exported record now yields `11@test@…` and `refs/zf/branches/11`. The
  chain keeps recording the full record ID. A repo-born record has a branch
  type, so the type is not asked; the chain records no tracker type, as
  today, and the merge close keeps closing the tracker issue through the
  reconcile.
- **`issue new`.** The "Created issue …" line names the issue by the number
  the reconcile just gave it: the record is read again after the reconcile.
- **`issue list`.** A record row is keyed by its display ID, so an exported
  issue reads `11`. `Row.TrackerID` becomes the tracker number of any linked
  record (JSON `tracker_id`), and the ID cell appends ` (#11)` only when the
  slug differs from it: the case of a branch started before the export,
  whose row keeps the chain's slug `edcac55`. The join of a branch to its
  record is unchanged: record ID first, then display ID.
- **`issue close <id>`.** The in-progress check loads the branch chain under
  the display ID and, when it differs, under the short hash too, so a
  branch started before the export still blocks the close.
- **`issue show`, `edit`, `comment`, `label`, the record picker.** They
  print `DisplayID`: `11` instead of `edcac55`, with the full ID still on the
  `ID:` line of `show`.
- **The commit hint.** Already resolves the tracker number from the chain's
  record; on a branch named `11@…` the lookup and the slug agree.
- **The review branch** `<slug>@review` follows the branch it reviews, so a
  branch started after this change gets `11@review`.

## Section 3: compatibility

- A branch named `edcac55@…` from before the export, or from before this
  change, keeps its name and its chain. `issue list` joins it through the
  record ID, the close picker lists it, `issue close` (merge) closes its
  record through the chain's record ID, and the commit hint refers to
  `#11`.
- The only seam is the "second branch on the same issue" check, which
  compares exact names: `edcac55@feat@x` and `11@feat@x` could coexist on
  one issue. Known limit, shared with the several-projects spec.
- Nothing in the refs is rewritten.

## Section 4: the several-projects plan

`2026-10-09-issue-mirror-several-projects-design.md` and its plan build on
`DisplayID`; this spec lands first, and the plan takes it as the base:

- Task 5's `DisplayID(qualify)` qualifies any linked record, not only a born
  one: `if r.Tracker != nil { return QualifiedID(r.Tracker.Project,
  r.Tracker.ID, qualify) }`. The `TestDisplayID` case "an exported record
  keeps its short hash" becomes "an exported record is its number too".
- `FindByDisplayID` and `TrackerRef` gain nothing and lose nothing: an
  exported record now matches by display ID before the bare-number
  fallback.
- The list join's third step (bare number when one record has it) and the
  close-by-ID dual slug of the plan already cover branches named before a
  change of display ID; this spec's dual slug is the same loop, over
  `DisplayID` and `ShortID`, which the plan extends to the qualified form.

## Testing

| Level | File | Covers |
|---|---|---|
| Fold | `issue/record_test.go` | `TestDisplayID`: born, exported, unlinked |
| Rows | `issue/row_test.go` | the ID cell: slug equal to the number stands alone; a differing slug shows ` (#11)`; no number shows the slug |
| Reconcile | `issue/mirror_test.go` | `TestReconcile_Export`: the linked record displays its number |
| Start | `cmd/issue/start_record_e2e_test.go` | a pick of an exported record creates `11@feat@…` and a chain under `11` naming the record |
| Close by ID | `cmd/issue/record_ops_e2e_test.go` | a branch seeded under the short hash before the link still refuses `issue close 11` |
| New | `cmd/issue/mirror_e2e_test.go` | the "Created issue 1: …" line; the list row of an exported issue is keyed `1` with a bare ID cell |

Every assertion sits in its own `t.Run`.

## Documentation

- `docs/issue-refs.md`, "Display": an exported issue is shown and names its
  branches by its number; a branch started before the export keeps its slug
  and shows the number beside it in `issue list`.
- `2026-10-06-issue-tracker-mirror-design.md`, "Revisions": one line
  pointing here.
- `ROADMAP.md`: nothing; this is a fix.

## Out of scope

- Renaming existing branches or chains.
- A picker-side hint that a branch under the old slug exists.
