# Design: repo issues mirrored with the tracker

**Date:** 2026-10-06
**Status:** approved design, not implemented

## Overview

An issue lives in one of two places today, never both:

- a **tracker-born** issue lives in GitHub, Forgejo or Redmine. Starting a
  branch stores the tracker type on the branch chain; `issue close` and the
  review transitions then offer a tracker status. It has no repo record;
- a **repo-born** issue lives in `refs/zf/issues/<id>`. `issue close` writes
  `set_state closed`. No tracker is told.

`issue list` shows one world or the other: the tracker when it answers, the
repository otherwise.

This spec links the two. With the mirror on, every open issue of the tracker
project has a repo record, every open repo record has a tracker issue, and
open/closed travels both ways. The repository becomes the place git-zf reads
issues from; the tracker stays the web view of the same set.

It is the prerequisite of the roadmap entry "issues carry author and assignee,
`issue list` filters mine": once the list shows the whole project, a "mine"
filter has something to filter.

**Decisions taken during brainstorming:**

- **Scope of the mirror:** all issues, both directions (import and export).
- **Fields kept in sync:** open/closed only. Title and description are copied
  once, when the issue is mirrored.
- **Trigger:** `issue list`, the commands that create an issue or change its
  state, and `issue sync`. First chosen as "every command that fetches or
  pushes issue refs", then narrowed on review: a reconcile lists the whole
  project, which `show`, `edit`, `comment` and `label` do not need to act on
  one issue.
- **Linkage:** in the issue's own chain, with a deterministic root for imports
  (approach A). Rejected: a link op with dedupe on read (duplicate chains stay
  forever), a separate mapping ref (a fourth chain family).
- **Projects:** one project per repository in this version. Several projects
  go to the roadmap.
- **Project config:** an array of `{near-slug, far-slug}` instead of an array
  of strings, so that renaming the project in the tracker does not change the
  identity of its issues.
- **Backward compatibility of the config is not required.**

## Section 1: activation and scope

### Config

```toml
[issue-tracker]
type   = "forgejo"
url    = "https://forge.example.org"
token  = "…"
mirror = true

[[issue-tracker.projects]]
near-slug = "zf"              # stable local name, part of the issue identity
far-slug  = "piprim/git-zf"   # what the tracker calls the project
```

- `config.IssueTrackerConfig.Projects` becomes `[]TrackerProject`
  (`NearSlug`, `FarSlug`). `Mirror bool` is new, default `false`.
- `near-slug` matches `^[A-Za-z0-9][A-Za-z0-9-]*$`. The config may write it
  in any case; `Load` lowercases it, and every internal use (the import root,
  `TrackerLink.Project`, comparisons, display) sees the lowercased form.
  `near-slug = "ZF"` and `near-slug = "zf"` are the same project. It is unique
  in the array after lowercasing. `far-slug` is not empty and is passed to the
  tracker as written.
- The old form `projects = ["owner/repo"]` is an error at load. The message
  shows the new form. Nothing is converted silently. The empty `projects = []`
  that older default configs wrote is dropped at load: it means no project.
- `mirror = true` requires a tracker type and exactly one project. Anything
  else is an error at load that names the fix.
- Adapters read `FarSlug` wherever they read a project string today. Their
  "exactly one project" checks do not change.

The mirror is opt-in because its first run writes to the tracker: it creates
one tracker issue per open repo-born record.

### What is mirrored

- **Imported:** every open issue of the project, whoever it is assigned to.
  An issue closed in the tracker before it was ever imported is not imported.
- **Exported:** every open repo-born record. A record closed before it was
  ever exported stays local.
- **Synced afterwards:** open/closed, both ways, plus the tracker's own status
  name for display.

### What changes for the user

- `issue list` with the mirror on reads the repository. It shows the whole
  project's issues instead of the ones assigned to the token's user.
- With the mirror off, every command behaves as before this spec.

## Section 2: data model

### Record

`issue/record.go`:

```go
// TrackerLink names the tracker issue a record is mirrored with.
type TrackerLink struct {
	Type    string `json:"type"`    // tracker type, e.g. "forgejo"
	Project string `json:"project"` // near slug
	ID      string `json:"id"`      // the tracker's issue number
	// Born is true when the link comes from the create op: the issue was
	// imported from the tracker.
	Born bool `json:"born"`
}

type Record struct {
	// … existing fields …
	Tracker       *TrackerLink `json:"tracker"`        // nil: not mirrored
	TrackerState  string       `json:"tracker_state"`  // last open/closed seen on the tracker; "" never seen
	TrackerStatus string       `json:"tracker_status"` // the tracker's status name, for display
	// DuplicateTrackerIDs lists the tracker issues named by link_tracker ops
	// that lost to an earlier link.
	DuplicateTrackerIDs []string `json:"-"`
}
```

### Ops

`Op` gains four optional fields: `tracker_type`, `project`, `tracker_id`,
`status`.

| Op | Fields | Fold |
|---|---|---|
| `create` (existing) | + `tracker_type`, `project`, `tracker_id` | on the root, sets `Tracker` with `Born = true` |
| `link_tracker` (new) | `tracker_type`, `project`, `tracker_id` | sets `Tracker` when it is nil; otherwise appends the ID to `DuplicateTrackerIDs` |
| `tracker_state` (new) | `value` (`open` / `closed`), `status` | sets `TrackerState` and `TrackerStatus`; any other `value` is ignored |

An older binary skips `link_tracker` and `tracker_state` like any unknown
type, and ignores the new fields of `create`.

### Deterministic import root

Two clones that import tracker issue #42 before seeing each other's refs must
produce the same chain. The root commit of an imported issue is therefore a
pure function of four values: tracker type, near slug, tracker number, and the
tracker's creation date.

- **Payload:** a frozen template, not `json.Marshal(Op)`, so that a later
  change to `Op` cannot change the bytes:

  ```
  {"v":1,"type":"create","at":"<created>","tracker_type":"<type>","project":"<near>","tracker_id":"<id>"}
  ```

  `<created>` is RFC 3339, UTC, to the second. `<near>` is the lowercased
  near slug. `<id>` must match
  `^[0-9A-Za-z_-]+$`; an issue whose ID does not is skipped with a warning.
  With those constraints no value needs JSON escaping.
- **Commit:** author and committer `git-zf <git-zf@localhost>`, both dates
  `<created>`, message `create`, no parent, never signed (whatever
  `commit.gpgsign` says), `i18n.commitEncoding` forced to UTF-8.
- **Plumbing:** `git.WriteFixedChainRoot(ctx, payload, message, when)` in
  `git/chain_ref.go`, next to `WriteChainRoot`.
- **Publishing:** when `refs/zf/issues/<root>` already exists, the issue is
  already imported; this is not an error.

Title and description are mutable in the tracker, so they are not in the
root. They follow as ordinary `set_title` and `set_description` ops, then a
`tracker_state` op. Two clones importing at once write the same root and
duplicate follow-up ops; the chain merge folds them to one record.

`tracker.Issue` gains `CreatedAt time.Time` for this.

### Display ID and lookup

- `Record.DisplayID()` returns the tracker number when `Tracker.Born`, the
  short hash otherwise. An imported issue therefore keeps the slug a
  tracker-born issue has today: existing branch names and branch chains
  (`refs/zf/branches/42`) join to it.
- A repo-born record exported later keeps its short hash: branches may exist
  under it. Its tracker number is shown beside it.
- `issue.Resolve` tries, in order: the full ID, an exact tracker number among
  linked records (only for a query made of digits, which is what the three
  trackers use), an ID prefix.

## Section 3: reconciliation

`issue/mirror.go`:

```go
// Mirror ties the issues of this repository to one tracker project.
type Mirror struct {
	Tracker tracker.Tracker
	Type    string // tracker type
	Project string // near slug
}

// Reconcile imports, exports and syncs open/closed. A nil *Mirror is a no-op.
func (m *Mirror) Reconcile(ctx context.Context, c *git.Client) (MirrorResult, error)

type MirrorResult struct {
	Imported, Exported int
	Pulled, Pushed     int      // state changes: tracker → repo, repo → tracker
	Warnings           []string // one line per issue that could not be handled
}
```

It does not fetch from the git remote: the caller does, just before. Only
records whose link has this `Type` and `Project` take part.

### Steps

1. **List.** `Tracker.ListProjectIssues` returns the project's open issues.
   A failure here ends the run with an error; nothing was written.
2. **Import.** For each listed issue that no record links to (neither as
   `Tracker` nor in `DuplicateTrackerIDs`): write the deterministic root,
   publish it, append `set_title`, `set_description`, `tracker_state open`.
   A listed issue found in some record's `DuplicateTrackerIDs` is not
   imported; it produces a warning asking to close it by hand.
3. **Export.** For each open record without a link: `Tracker.CreateIssue`,
   then append `link_tracker` and `tracker_state open`.
4. **State.** For each linked record, with `L` the recorded `TrackerState`,
   `R` the repo state and `T` the tracker state now:

   | `L` | `R` | `T` | Who moved | Action |
   |---|---|---|---|---|
   | open | open | open | nobody | nothing |
   | open | open | closed | tracker | append `set_state closed`, `tracker_state closed` |
   | open | closed | open | repo | `SetIssueOpen(false)`, append `tracker_state closed` |
   | open | closed | closed | both | append `tracker_state closed` |
   | closed | closed | closed | nobody | nothing |
   | closed | closed | open | tracker | append `set_state open`, `tracker_state open` |
   | closed | open | closed | repo | `SetIssueOpen(true)`, append `tracker_state open` |
   | closed | open | open | both | append `tracker_state open` |

   - The eight rows are every combination. Each side is compared with `L`:
     the side that differs from it moved. When both differ from `L` they are
     equal to each other, so there is no conflict case.
   - An empty `L` (the link exists but no `tracker_state` op followed it)
     counts as `open`: an issue is open when it is imported or exported.
   - On a "nobody" row, a status name that differs from the recorded
     `TrackerStatus` still appends one `tracker_state` op.
   - "tracker" rows count in `MirrorResult.Pulled`, "repo" rows in `Pushed`.
5. **Push** every record that got an op, in one `git push` (`issue.PushAll`:
   push, and on rejection fetch, merge, retry once).

The records are read in three git processes whatever their number
(`issue.List` moves to `ReadAllChains`, like branches and reviews, with the
"the ref names its chain's root" check done on the result). A ref that fails
that check is skipped with a warning.

### Reading the tracker state

Closed issues are not listed, so `T` is derived:

- in the listing: `open`, with the listing's status name;
- not in the listing, and `R` or `L` is `open`: one `IsIssueClosed` call.
  `true` gives `closed`; `false` gives `open`; `ErrIssueNotFound` or any other
  error is a warning and the record is skipped;
- not in the listing, `R` and `L` both `closed`: `closed`, no call.

A run costs one paginated listing, plus one call per issue that closed on the
tracker since the last run.

### Properties

- **Idempotent.** A second run with nothing changed appends no op and makes no
  tracker write.
- **Convergent.** Two clones that see the same tracker change append the same
  ops.
- **Restartable.** Each step can be redone after a failure.

### Known limits

- Two clones exporting the same record at the same moment, or one doing it
  while cut off from the git remote, create two tracker issues. The first
  `link_tracker` in chain order wins; the other number lands in
  `DuplicateTrackerIDs`, is never imported, and is reported on each run until
  someone closes it.
- If an export creates the tracker issue and the local append of
  `link_tracker` then fails, the next run exports again. This needs a local
  git write to fail.
- The listing is fetched in full on every run.
  `ponytail:` add an "updated since" query if a large project makes it slow.
- After the repo reopens an issue and the tracker follows, the status name
  the tracker gave it is only learned from the next listing: that next run
  writes one `tracker_state` op. Every other path settles in one run.
- A tracker number of seven digits could equal a short hash made of digits.
  Not handled.

## Section 4: tracker interface

`tracker.Tracker` gains three methods, implemented by GitHub, Forgejo, Redmine
and the fake:

| Method | GitHub / Forgejo | Redmine |
|---|---|---|
| `ListProjectIssues(ctx) ([]Issue, error)` | the repository's open issues, pull requests dropped, every page | `/projects/<far>/issues.json?status_id=open`, every page |
| `CreateIssue(ctx, title, description string) (Issue, error)` | create in the repository, return the issue with its number and status | `POST /issues.json` in the project |
| `SetIssueOpen(ctx, issueID string, open bool) error` | delegates to `UpdateIssueStatus` with `open` / `closed` | first status with `is_closed` to close, first without to reopen |

- The Redmine `status` struct gains `IsClosed` (`is_closed` of
  `/issue_statuses.json`).
- `ListIssues` ("assigned to me") stays: the `issue start` picker and the
  listing with the mirror off use it.
- The fake records its `CreateIssue` and `SetIssueOpen` calls and hands out
  increasing numbers.

## Section 5: commands

A reconcile lists the whole project, so only the commands that need it run
one: the listing, and the writes that create an issue or change its state.

| Command | Reconciles |
|---|---|
| `issue list` | yes, after fetching the issue refs |
| `issue new` | yes, after the push: the tracker issue is created at once |
| `issue close <id>` | yes, after the push: the tracker issue is closed at once |
| `issue close` (merge) | yes, see "Merge close" |
| `issue sync` | yes, and prints the counts of `MirrorResult` |
| `issue show`, `edit`, `comment`, `label` | no: they fetch the issue refs and read local chains, as today |
| `issue start`, hooks, `commit` | no |

- `fetchIssues` and `pushIssue` (`cmd/issue/record.go`) keep doing git only.
  A new helper next to them, `reconcileIssues(ctx, client, mirror)`, runs
  `Mirror.Reconcile` and prints its warnings; a failure is a warning.
- A command that reconciles builds its `*issuepkg.Mirror` once from the config
  (`nil` when the mirror is off, which makes the helper a no-op).
- A command that does not reconcile still sees what another clone reconciled:
  the ops travel with the issue refs.
- **`issue list`**, mirror on: skips the "assigned to me" tracker listing and
  builds its rows from branches and records, as it does today without a
  tracker. The tracker status column shows `TrackerStatus` when the record has
  one. `Row` gains `TrackerID`; the ID cell of an exported repo-born record
  reads `a1b2c3d (#57)`.
- **`issue show`** prints the link: tracker type, near slug, number.
- **`issue start`** keeps its sources. When it starts from an imported record
  (the tracker did not answer), the branch type is asked with the form a live
  tracker listing uses (the record has none: trackers have no branch type),
  the slug is the tracker number and the branch chain stores the record's
  tracker type and ID, so the branch is the same as one started from the live
  listing. An exported repo-born record starts as a repo-born issue:
  short-hash slug, no tracker type on the branch chain.
- Hooks and `commit` never reconcile.

### Merge close

A branch chain says where its issue was born (`TrackerType`). `issue close`
keeps one rule per origin, so the status picker and the mirror cannot fight
(on Redmine, picking "Resolved" must not be overridden by a forced close):

- **Tracker-born branch** (`TrackerType != ""`): the status picker runs as
  today. The record is not closed directly. The command then reconciles, and
  the record follows the tracker.
- **Repo-born branch:** `closeRepoIssue` closes the record as today, pushes,
  then reconciles, which closes the tracker issue.

With the mirror off this is today's behaviour: `closeRepoIssue` is already a
no-op for a branch without a repo issue ID.

The review transitions (`cmd/review`) keep offering a tracker status; the
record follows at the next reconcile.

## Section 6: error handling

- A tracker failure (down, auth, rate limit) is one warning on stderr. The
  command continues on local data. The next run reconciles from the chains.
- A failure on one issue is a line in `MirrorResult.Warnings`; the run goes on
  with the others.
- Config errors (old `projects` form, `mirror = true` with zero or several
  projects, invalid near slug) stop at load.

## Testing

| Level | File | Covers |
|---|---|---|
| Fold | `issue/record_test.go` | `TestFold` cases: import root, `link_tracker` (first wins, loser recorded), `tracker_state`, invalid value ignored, unknown types skipped |
| Plumbing | `git/chain_ref_test.go` | fixed root: same hash in two repositories with different `user.name`, with `commit.gpgsign` on; a golden hash |
| Reconcile | `issue/mirror_test.go` (new) | real repositories and the fake tracker: import, export, the eight rows of the state table as one table test, a link without `tracker_state`, idempotent second run, two clones importing offline then merging into one record, duplicate link reported and not imported, tracker down |
| Adapters | each adapter's test file | the three calls against `httptest` servers; Redmine's choice of status |
| Config | `config/` tests | near/far parsing, near slug lowercased at load, two slugs differing only by case rejected, old form rejected, `mirror` validation |
| Commands | `cmd/issue/mirror_e2e_test.go` (new) | `new`, `list`, `show`, `close <id>`, `sync` on a `recordRig` with the mirror on |
| Close flow | `cmd/issue/close_mirror_e2e_test.go` (new, on the close rig) | tracker-born branch with a mirrored record: the picker runs, the record follows the tracker, a status that leaves the issue open is kept; repo-born branch: the tracker issue is closed |
| Start flow | `cmd/issue/start_record_e2e_test.go` | an imported record starts as a tracker issue: type asked, tracker number in the branch name, origin on the branch chain |

Every assertion sits in its own `t.Run`.

## Documentation

- `docs/issue-refs.md`: the mirror, the two ops, the import root.
- `README`: the `projects` form and `mirror`.
- `config/default.toml`: `mirror = false`.
- `CLAUDE.md`: a "Testing the tracker mirror" section.

## Out of scope

Each gets a roadmap entry:

- several projects: project-aware tracker calls, project-qualified slugs, a
  project picker in `issue new`;
- sync of title, description, labels and comments;
- the `issue start` picker reading the mirror instead of the live "assigned to
  me" listing;
- author, assignee and `issue list --mine`.

## Revisions

- 2026-10-06, while planning: `CreateIssue` returns the created issue (number
  and status), so an export settles in one run; `projects = []` is tolerated;
  the tracker-number lookup of `Resolve` only runs for a query made of digits;
  `issue start` asks the branch type of an imported record with the tracker
  form; the close tests of the mirror live in their own file.
- 2026-10-06, on review: a reconcile reads every issue in three git processes
  (`List` over `ReadAllChains`) and pushes the records it changed in one
  `git push` (`PushAll`, `git.PushChainRefs`), instead of two or three
  processes per issue and one push per record.
