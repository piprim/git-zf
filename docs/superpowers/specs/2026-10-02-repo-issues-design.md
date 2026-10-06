# Design: issues stored in the repository, synced with the tracker

**Date:** 2026-10-02 (revised 2026-10-03 after an external review, see
[Revisions](#revisions))
**Status:** draft

## Overview

Today an issue exists in git-zf only as a branch name and a SQLite row. The
title, description and status live in the tracker; a repo without a tracker has
nothing but the ID the user typed into a form. Teammates with a fresh clone see
no backlog, and a reviewer without a tracker account cannot read what the issue
is about.

This spec moves the issue record into the repository, under
`refs/zf/issues/<id>`, so it travels with clone, fetch and push like the review
and branch refs do. The tracker becomes an optional mirror: `git zf issue sync`
exchanges changes in both directions, and the existing commands keep writing
their one status change straight to the tracker as they do now.

The storage model borrows git-bug's event-sourced design (one commit per
change, deterministic fold) and nothing else: no web UI, no remote helpers, no
feature parity. The git-zf workflow (issue → branch → commit → review → close)
stays the spine; the repo issue is what the workflow operates on.

**Decisions taken during brainstorming:**

- Purpose: shared issue data across clones, mirrored to the tracker when one is
  configured. The repo is the home of the record; the tracker is a mirror.
- Fields in v1: ID, title, description, branch type, state, tracker status,
  labels, comments. No author/assignee, no attachments, no milestones.
- IDs: the object ID of the `create` commit, stable for the issue's life. Its
  first 7 characters are the *short ID*. When the issue is linked to a tracker
  the tracker ID becomes the *display* ID (branch names, `Refs #` footers,
  listings); the short ID stays as alias.
- Storage: event-sourced commit chain per issue (approach B), not a JSON blob
  with CAS (A) nor one branch with a file per issue (C). B removes the
  retry-on-collision logic every writer would otherwise need and makes offline
  comments a set union.
- Sync: explicit `issue sync` plus opportunistic writes from the existing
  commands. No background work.
- Import scope: open issues in the configured projects, plus closed ones the
  repo already knows, so closes propagate.
- Conflict policy: three-way against the last synced values. A field changed
  on one side only propagates to the other. A field changed on both sides is a
  conflict: the tracker wins and the summary reports it. Comments and labels
  never conflict. (Revised: the first draft compared wall-clock timestamps.)
- Delivery: one spec, two implementation plans. Plan 1 is the in-repo store
  and the command integration, usable with no tracker at all. Plan 2 is the
  bridge.

## Section 1: data model and git storage

### Ref layout

`refs/zf/issues/<id>` points at a commit. `<id>` is the **full object ID** of
the `create` commit (40 hex characters in a SHA-1 repository). The ref
namespace can hold only one chain per name, so a truncated ID in the ref name
would make two issues with the same prefix overwrite each other locally and
break `git fetch` for everyone (verified: the fetch is rejected as a
non-fast-forward and exits 1). Truncation is presentation only.

The remote's issue refs are fetched into a tracking namespace, with the refspec
`+refs/zf/issues/*:refs/zf/remote/issues/*`, pruning that tracking namespace
so it always mirrors the remote. **A fetch never writes or prunes
`refs/zf/issues/*` itself.** A same-name refspec does not work here: it is
rejected as soon as a local ref is ahead of or diverged from the remote, and
with `--prune` it deletes issues created offline and not pushed yet (both
verified). The local refs are then brought up to date from the tracking refs
(see Divergence). Closed issues keep their ref.

Integrity check on read: the ref name must be the ID of one of the chain's root
commits. A ref that fails the check is skipped with a `WARN:` line.

### IDs

| Name | Value | Used for |
|---|---|---|
| ID | full object ID of the `create` commit | ref name, `BranchRef.issue_id`, op cross-references |
| short ID | first 7 characters of the ID | display and input when the issue is not linked |
| display ID | tracker ID when linked, else short ID | branch names, `Refs #` footers, listings |

Lookup (`issue show`, `issue comment`, `issue label`, `issue link`) accepts a
tracker ID, a full ID or any unique ID prefix of 4 characters or more. A prefix
matching several issues is an ambiguity error listing the candidates with
their titles, as git does.

A branch name carries only the display ID, so `BranchRef` (the blob at
`refs/zf/branches/<slug>`) gains `issue_id` (full ID, `omitempty`). Resolving a
branch to its record reads that field first and falls back to prefix lookup
for refs written before this change.

### Op commit

Each change is one commit whose tree holds a single blob `op.json`. Plumbing
used: `hash-object -w`, `mktree`, `commit-tree`, `update-ref`, `rev-list`,
`cat-file --batch`, `merge-base`. The commit message is the op type on one line
(cosmetic).

Two kinds of op commit:

- **User ops**: author and committer are the user's git identity.
  `git commit-tree` ignores `commit.gpgsign` (verified on git 2.47), so the
  writer reads `git config --bool commit.gpgsign` and passes `-S` itself when
  it is true.
- **Bridge ops** (written on behalf of the tracker during sync): a
  *deterministic* commit, so two clones importing the same tracker data
  produce the same object IDs, hence the same ref and the same chain. This is
  what removes the duplicate-import problem. Every input of the commit ID is
  pinned (each one verified to change the ID when left free):
  - **tree**: `op.json` is marshalled from a struct with `encoding/json`,
    mode `100644`, no other entry;
  - **identity**: `GIT_AUTHOR_*` and `GIT_COMMITTER_*` name and email are both
    `tracker:<type> <tracker@git-zf>`, never the local user;
  - **dates**: `GIT_AUTHOR_DATE` and `GIT_COMMITTER_DATE` are both set, to the
    op's `at` truncated to the second and written as `@<unix seconds> +0000`.
    An omitted committer date falls back to the local clock, and the same
    instant written with another UTC offset is a different commit;
  - **encoding**: the command runs with `-c i18n.commitEncoding=UTF-8`, since
    a user-level setting adds an `encoding` header to the commit;
  - **signature**: never signed;
  - **message**: the op type, nothing else;
  - **parent**: none for `create`; the previous bridge op for the rest of an
    import, written in the fixed order of section 3.

  Only a first import is guaranteed identical across clones. Bridge ops
  appended later sit on whatever the local tip is, so they may differ; the
  chains still share their root and merge like any divergence.

```json
{
  "v": 1,
  "type": "add_comment",
  "at": "2026-10-02T10:00:00Z",
  "author": "Pi <piprim@pm.me>",
  "body": "…",
  "tracker_comment_id": "18234"
}
```

`at` is RFC 3339 UTC. For bridge ops `at` is the tracker's timestamp for that
datum and `author` is `tracker:<login>` (comments) or `tracker:<type>`.

| type | payload | notes |
|---|---|---|
| `create` | `title`, `description`, `branch_type`; on import `tracker_type`, `tracker_id` instead | root of the chain. An imported `create` carries only the tracker identity and the tracker's creation date so it is deterministic; title and the rest follow as `set_*` ops |
| `set_title` | `value` | |
| `set_description` | `value` | |
| `set_branch_type` | `value` | a `commit-types` name; written by `issue start` on a record that has none (imported issues) |
| `set_state` | `value`: `open` \| `closed` | |
| `set_tracker_status` | `value` | tracker status name; Redmine only in practice |
| `add_label` / `remove_label` | `value` | set semantics; removing an absent label is a no-op |
| `add_comment` | `body`, optional `tracker_comment_id` | imported comments carry the tracker ID |
| `link_comment` | `comment_id` (op commit ID), `tracker_comment_id` | written after a repo comment is exported |
| `export_intent` | none | written and pushed before the tracker issue is created (section 3) |
| `link_tracker` | `tracker_type`, `tracker_id` | written after export, or by `issue link` |
| `synced` | `tracker_updated_at`, `base`: `title`, `description`, `state`, `tracker_status`, `labels` | the values both sides agreed on at the end of a sync; the three-way base |
| `superseded_by` | `id` | tombstone on a record merged into another (section 3) |
| `merge` | none | two-parent commit written on divergence or record merge |

### Fold

`issue.Fold(ops) Record` applies ops in a linearization of the commit DAG:
parents always before children; concurrent ops (neither an ancestor of the
other) ordered by `at`, then commit ID. Scalar fields are last-writer-wins;
labels are a sorted set; comments accumulate, keyed by op commit ID and
de-duplicated on `tracker_comment_id`. A `create` assigns its fields like the
`set_*` ops do, so a chain holding several `create` ops after a record merge
folds without special cases; the record's ID always comes from the ref name.
Unknown op types and unknown `v` values are skipped (with a warning at the CLI
layer) so an older binary tolerates newer ops.

Clock skew: an op that causally follows another always wins regardless of
`at`. A wrong clock can only win against *concurrent* ops, once, and never
locks out later edits. The bridge does not compare `at` with tracker
timestamps at all (section 3).

```go
type Record struct {
    ID            string     // full object ID of the create commit
    Title         string
    Description   string
    BranchType    string
    State         string     // "open" | "closed"
    TrackerStatus string
    Labels        []string
    TrackerType   string     // "" = not linked
    TrackerID     string
    Comments      []Comment  // {ID, Author, At, Body, TrackerCommentID}
    CreatedAt     time.Time
    ExportPending bool       // export_intent seen, no link_tracker yet
    SupersededBy  string     // "" unless tombstoned
    Synced        *SyncBase  // latest synced op, nil before the first sync
}

func (r Record) ShortID() string   // ID[:7]
func (r Record) DisplayID() string // TrackerID when linked, else ShortID()
```

"Latest `synced`" is the one with the greatest `tracker_updated_at` (the
tracker's own clock compared with itself), ties broken by commit ID.

### Divergence

After a fetch, each local ref is reconciled with its tracking ref: a missing
local ref is created, one that is behind is fast-forwarded, one that is ahead
is left alone. When the two tips are distinct and neither is the other's
ancestor (`merge-base --is-ancestor` both ways), the client writes a `merge` op
commit with both as parents and moves the local ref to it. Both tips share the
root named by the ref, so unrelated histories cannot be spliced. Pushes are
plain, non-forced pushes: after a reconcile the local tip descends from the
remote's, so the push is a fast-forward, and it can never overwrite ops pushed
by someone else. A rejected push (the remote moved meanwhile) triggers one
fetch + merge + retry; a second rejection is returned to the caller. The local op is already committed either way, so nothing is lost and
the next command or `issue sync` pushes it.

### Where it lives

- `git/issue_ref.go`: byte-level plumbing for one chain: `CreateIssueRef`,
  `AppendIssueCommit`, `ReadIssueCommits`, `ListIssueIDs`, `IssueTip`.
- `git/issue_ref_sync.go`: `FetchIssueRefs`, `ReconcileIssueRefs`,
  `PushIssueRef`, `IssueRefPushed`.
- `issue/record.go`: the `Op` type, the op-type constants, `Fold`, `Record`.
  Pure, no git.
- `issue/repo.go`: the glue over `*git.Client`: `Create`, `Append`, `Load`,
  `List`, `Resolve`, `Fetch`, `Push` (with the merge-and-retry), `Sync`.
- `git/branch_ref.go`: `BranchRef.IssueID`.
- The SQLite store is untouched. `issues.id_slug` keeps holding the display ID
  so branch rows resolve as today.

## Section 2: commands and workflow integration

### New subcommands under `git zf issue`

| Command | Behavior |
|---|---|
| `new` | Form: title, type (from `commit-types`), description, labels. Writes `create` and one `add_label` per label, pushes the ref, prints the display ID. Creates no branch. Flags: `--title`, `--type`, `--description`, `--label` (repeatable); any flag skips the form. |
| `show <id>` | Prints the record, labels and comments. `--json` for scripts. |
| `comment <id>` | Multiline form, writes `add_comment`, pushes. `--message` skips the form. |
| `label <id> +foo -bar` | Writes `add_label` / `remove_label` per argument, pushes. |
| `sync` | Section 3. |
| `link <id> [<tracker-id>]` | Section 3 (Plan 2). Links a record to a tracker issue by hand, or forces its export. |

`new`, `show`, `comment` and `label` go into the `issue` menu after `start`,
`list`, `close`. `sync` is appended last. `link` is a repair command and stays
out of the menu.

### Existing flows

- **`issue start` / `branch new`.** Fetches `refs/zf/issues/*`. When a tracker
  is configured, runs sync steps 1 and 2 first so the backlog is fresh; a
  tracker failure prints a warning and the picker shows what the repo has. The
  picker lists repo records in state `open` and no longer calls `ListIssues`
  directly. The manual path becomes "create a new issue": the existing title
  and type form writes `create`, so a hand-typed issue is a real record. For a
  record without a branch type (imported), the type form's answer is written
  as `set_branch_type`. The `--parent` relation stays in the store and the
  branch ref, unchanged. The tracker "move to In Progress" step stays and
  additionally records the choice through `RecordTrackerStatus` (below). The
  branch name uses `Record.DisplayID()` and the branch ref records
  `issue_id`.
- **`issue list`.** Rows come from the repo records (superseded ones hidden)
  joined with store branch rows. Columns: ID · Title · Branch · Local Status ·
  Issue Status · Created. Labels follow the title in the same cell
  (`Login fails [bug, ui]`): a separate column overflows a 120-column
  terminal. The `N.A.` tracker case disappears. The `tab` status filter cycles on `state`. `--json` emits the
  record plus the branch fields.
- **`issue close`.** After the merge lands: `set_state closed`, then the
  existing tracker status picker whose choice is recorded through
  `RecordTrackerStatus`. The issue ref is pushed with the branch ref.
- **Review commands** (`request`, `approve`, `reject`). The tracker status
  proposal stays; its choice is recorded through `RecordTrackerStatus`. No new
  prompt.
- **`branch prune-tracker`.** Unchanged: it still asks the tracker.

### Shared helper

`cmd/issueflow.RecordTrackerStatus(ctx, g, rec, status)` maps a status picked
in a tracker status picker to the right op: `set_tracker_status` when the
tracker has a workflow (Redmine), `add_label` with the configured in-progress
label on GitHub/Forgejo when the picked status is the in-progress one, and
`set_state` when the tracker reports the status as closing. It pushes the ref.
Used by `issue start`, `issue close` and the three review decisions.

New config key:

```toml
[issue-tracker]
in-progress-label = "in progress"   # GitHub/Forgejo only; label that mirrors "In Progress"
```

### Domain types

`issue.Record` replaces `tracker.Issue` as the value `issue start` carries
through its forms; `issue.Issue` (the `tracker.Issue` + `Type` wrapper) is
removed in favor of `Record`, whose `BranchType` field plays that role.
`tracker.Issue` stays the wire shape for adapters. This replacement happens in
Plan 2, with the tracker path; Plan 1 keeps `issue.Issue` and adds a `RecordID`
field to it.

### Push behavior

Issue-ref pushes are immediate, fast-forward only (never forced), and a no-op
without a remote. A failed push is a warning: the op stays committed locally. They are not part of the "push now?" proposal, which
stays about branches.

## Section 3: the sync bridge

### Tracker interface additions

Implemented by Redmine, GitHub, Forgejo/Gitea and the fake:

```go
CreateIssue(ctx context.Context, title, description string, labels []string) (id string, err error)
UpdateIssue(ctx context.Context, id string, patch IssuePatch) error
ListComments(ctx context.Context, id string) ([]Comment, error)
GetIssue(ctx context.Context, id string) (Issue, error)   // closed issues included

type IssuePatch struct {
    Title       *string
    Description *string
    Labels      *[]string   // nil = untouched
}

type Comment struct {
    ID        string
    Author    string
    Body      string
    CreatedAt time.Time
}
```

`tracker.Issue` gains `Labels []string`, `Closed bool`, `CreatedAt time.Time`
and `UpdatedAt time.Time`. `AddComment` changes to return the created
comment's ID. Redmine has no labels: `CreateIssue` and `UpdateIssue` ignore
them and `ListIssues` returns none.

### Three-way comparison

The bridge never compares a repo timestamp with a tracker timestamp. For each
linked record it has three values per field: `t` (tracker now), `r` (repo
now), `b` (the `base` of the latest `synced` op).

| Situation | Action |
|---|---|
| `t == r` | nothing |
| `t != b`, `r == b` | tracker changed: write the repo op (`set_*`) |
| `r != b`, `t == b` | repo changed: update the tracker |
| both differ from `b` and from each other | conflict: the tracker wins, the repo gets the `set_*` op, the summary lists issue and field. The losing value stays in the op history |

Labels are merged as sets, never conflicting: additions and removals of each
side relative to `b` are both applied, and each side receives what it lacks.
Comments are a set union: tracker comments whose ID is absent from the record
become `add_comment`; record comments without a tracker ID are posted with
`AddComment` and then get a `link_comment`.

A field edited on the tracker therefore cannot overwrite a different field
edited in the repo, and a machine with a wrong clock cannot lock anything out.

When the tracker's `UpdatedAt` equals the base's `tracker_updated_at`, the
tracker side is unchanged: `ListComments` is skipped and only repo-side changes
are pushed.

### `issue sync` algorithm

One pass, idempotent.

1. **Fetch and reconcile.** `FetchIssueRefs`, merge every diverged
   chain (section 1), then re-merge any superseded record that gained ops (see
   "Record merge").
2. **Linked issues, both directions.** Tracker set = `ListIssues` (open issues
   in the configured projects) plus `GetIssue` for every linked record whose
   tracker ID is not in that list, so closes propagate. For each tracker issue:
   - no record linked to it: **import** as bridge ops, in a fixed order:
     `create`, `set_title`, `set_description`, `set_state`,
     `set_tracker_status` (Redmine), `add_label` sorted, `add_comment` by
     creation date then ID, `synced`. Being deterministic, a concurrent import
     on another clone yields the same chain;
   - a linked record: apply the three-way comparison, then write `synced`
     with the agreed values and the tracker's new `UpdatedAt` when anything
     changed on either side.
3. **Export unlinked records.** For each open record with no tracker link:
   - no `export_intent`: write `export_intent` and push the ref. The push is
     the lock: if it is still rejected after the merge-and-retry, or the merged
     chain now holds someone else's intent or link, skip the record. Otherwise
     `CreateIssue`, `link_tracker`, export the comments, `synced`;
   - `export_intent` without `link_tracker` (`ExportPending`): an earlier
     export was interrupted or is running elsewhere. Skip it and list it in the
     summary with the repair hint below. Never create a second tracker issue
     automatically.
   Closed unlinked records are left alone.
4. **Push refs** and print a summary: created / updated / commented in each
   direction, conflicts, pending exports, failures.

### `issue link <id> [<tracker-id>]`

The repair command for an interrupted export, also usable to attach a repo
issue to a tracker issue that already exists.

- With a tracker ID: verifies it with `GetIssue`, writes `link_tracker`, pushes.
  If another record is already linked to that tracker ID, performs a record
  merge instead.
- Without a tracker ID: creates the tracker issue now, whatever the
  `export_intent` state, then writes `link_tracker`.

Nothing is written into the tracker issue's body to support recovery, so
there is no marker for users to see or delete, and the description compares
byte for byte in the three-way check.

### Record merge

Two records linked to the same tracker ID are merged, never discarded. The
survivor is the one with the smallest ID (deterministic on every clone, no
clock involved).

1. On the survivor's ref, write a `merge` commit whose parents are both tips.
   Every op of the other record, including local comments, labels and edits,
   is now part of the survivor and folds normally; imported comments
   de-duplicate on `tracker_comment_id`.
2. On the other record, write `superseded_by` with the survivor's ID.

A superseded record is hidden from listings and lookups by its ID redirect to
the survivor, so a branch named after its short ID still resolves. A clone that
had not seen the tombstone may still append ops to it; sync step 1 detects a
superseded tip that is not an ancestor of the survivor's tip and repeats
step 1 of the merge.

With deterministic imports, a record merge only arises from `issue link`
pointing at an already-imported tracker issue, or from two clones linking
different records to the same tracker issue. Sync step 2 runs the merge when
it finds two live records with one tracker ID.

### State mapping

- Redmine: `state` derives from the status's `is_closed` flag (what
  `IsIssueClosed` already reads). `tracker_status` is the status name.
- GitHub / Forgejo: `state` is the issue state. `tracker_status` is never
  written. The in-progress label is an ordinary label.

### No tracker configured

`issue sync` runs steps 1 and 4 only, which doubles as "fetch and reconcile
the issue refs".

## Section 4: error handling

- **Push rejected.** Fetch, merge, retry once (section 1). A second rejection
  is reported; the op is committed locally and goes out with the next push.
- **Malformed op or ref** (bad JSON, unknown `v`, ref name not a root of its
  chain). Skipped with a `WARN:` line naming the commit or ref; the rest
  continues. Same tolerance as the review and branch refs.
- **Tracker unreachable.** `issue start`, `list` and `close` warn and continue
  on repo data. `issue sync` records the failure for that issue in the summary
  and moves on; the exit status is non-zero when anything failed. A repo op is
  never written for a tracker change that did not succeed.
- **Interrupted export.** The process dies between `CreateIssue` and
  `link_tracker`. The record stays `ExportPending`, sync refuses to export it
  again and prints: check the tracker, then run
  `git zf issue link <id> <tracker-id>` if the issue exists there, or
  `git zf issue link <id>` to create it.
- **Concurrent export.** The pushed `export_intent` makes the second clone
  skip the record.
- **Concurrent import.** Deterministic bridge ops give both clones the same
  chain; if the tracker changed between the two imports the chains share their
  root and are merged like any divergence.
- **Prefix ambiguity.** A short ID or prefix matching several records is an
  ambiguity error listing them; storage is unaffected because refs use the full
  ID.
- **Working tree.** Issue ops never touch the working tree or the index, so no
  stash or rollback logic exists.

## Testing

House pattern: real on-disk repo, fake tracker, scripted prompters, every
assertion in its own `t.Run`.

- `git/issue_ref_test.go`: write and read a chain; ref name equals the root
  ID; two clones diverge and merge; a non-fast-forward push is rejected, then
  merged and retried;
  malformed op skipped; ref whose name is not a root skipped; a local-only ref
  survives a fetch; a user op is signed only when `commit.gpgsign` is true;
  an import built in two repos with different user identity, clock,
  `i18n.commitEncoding` and `commit.gpgsign` yields the same IDs.
- `issue/record_test.go`: fold table tests, including label set semantics,
  concurrent ops ordered by `at` then ID, a causally later op beating a
  future-dated one, several `create` ops, comment de-duplication, latest
  `synced` selection, unknown op types.
- `cmd/issue/new_e2e_test.go`, `comment_e2e_test.go`, `label_e2e_test.go`,
  `show_test.go`: one happy path each plus the no-remote case; lookup by
  tracker ID, full ID, prefix, ambiguous prefix.
- `cmd/issue/start_e2e_test.go`, `close_e2e_test.go`: extended for the op
  writes, `set_branch_type` on an imported record, `BranchRef.issue_id` and
  the picker source change; the existing cases stay green.
- `cmd/review/*_test.go`: the status proposal now also writes an op.
- `cmd/issue/sync_e2e_test.go`: import new; two clones import the same issue
  and end with one ref; export new; each field changed on the tracker only, in
  the repo only, and on both (tracker wins, reported); a future-dated repo op
  does not block a later tracker edit; labels merged both ways; comments both
  ways; close propagation; unchanged `UpdatedAt` skips `ListComments`;
  interrupted export is skipped and reported; concurrent export is skipped;
  tracker failure in the summary. The fake gains the four new methods and
  records calls as it does for updates.
- `cmd/issue/link_e2e_test.go`: link by hand; forced export; link onto an
  already-imported issue merges the records and keeps the local comments;
  late ops on a superseded record are re-merged.
- Adapters: new methods tested the way the existing ones are, with `httptest`
  servers where the package already uses them.

## Documentation

- README: the new subcommands, the `issue-tracker.in-progress-label` key, and
  an "Issues in the repo" section explaining the ref, the three IDs and sync.
- `docs/issue-refs.md`: the op format, for people reading
  `git log refs/zf/issues/<id>`.
- CLAUDE.md: how to run the new E2E suites.

## Delivery

- **Plan 1, repo issues:** sections 1 and 2 for the non-tracker path; `issue
  sync` limited to fetch, reconcile and push. Fully usable with no tracker
  configured. Precisely:
  - ops written and folded: `create`, `set_state`, `add_label`,
    `remove_label`, `add_comment`, `merge`;
  - commands: `issue new`, `show`, `comment`, `label`, `sync`;
  - `issue start` / `branch new`: the *manual* path offers the open repo
    issues, and its form's Issue ID becomes optional. Empty creates a repo
    issue; a typed ID is kept as is with no record, for trackers git-zf does
    not talk to. The tracker path is unchanged;
  - `issue close` writes `set_state closed` on the branch's repo issue;
  - `issue list`: unchanged when a tracker is configured and reachable;
    otherwise repo issues joined with the store rows.
- **Plan 2, the bridge:** section 3 and what depends on linked records: the
  four `Tracker` methods in every adapter and the fake, bridge ops and their
  fold support (`set_title`, `set_description`, `set_branch_type`,
  `set_tracker_status`, `synced`, `export_intent`, `link_*`, `superseded_by`),
  `issue link`, record merge, `RecordTrackerStatus` and the
  `in-progress-label` key, the review commands' op writes, `issue start` and
  `issue list` reading records on the tracker path, the replacement of
  `issue.Issue` by `Record`, the sync and link E2E suites.

Open point, in neither plan: no command edits the title or description of a
repo-only issue. `set_title` / `set_description` exist for the bridge; an
`issue edit` form writing them would be a small addition.

## Revisions

2026-10-03, after an external review of the first draft. Each point was
checked against git 2.47.3 before changing anything.

| Finding | Verdict | Change |
|---|---|---|
| A 7-character ref name cannot hold two issues with the same prefix | Confirmed: fetch is rejected, local create overwrites | Ref name is the full ID; 7 characters is display and input only; `BranchRef.issue_id` added |
| Duplicate import closes the newer record and strands its local ops | Confirmed | Imports are deterministic, so the duplicate no longer forms; the residual case is a non-destructive record merge |
| Newest-timestamp-wins lets a skewed clock lock out the tracker | Confirmed | Three-way comparison against a `synced` base; no cross-system clock comparison |
| The `git-zf:` body trailer is visible and deletable | Partly: it was only read in the crash window, but it also broke description comparison | Trailer removed; `export_intent` op plus `issue link` |

2026-10-03, while writing the plan for Plan 1 (each point tested in a scratch
copy of the repository):

- The fetch goes to a tracking namespace, `refs/zf/remote/issues/*`, instead
  of a same-name refspec, which git rejects whenever a local ref is ahead.
- Pushes are plain fast-forward pushes instead of `--force-with-lease`.
- Labels are shown in the Title cell of `issue list`, not in their own column.
- The Delivery section spells out what Plan 1 changes on the non-tracker path
  and what waits for Plan 2.

2026-10-03, from the whole-branch review of the Plan 1 implementation:

- The tracking fetch prunes `refs/zf/remote/issues/*`, so `issue sync` pushes
  again an issue the remote no longer has.
- `issue start` only *prepares* a new issue (its create commit, hence its ID)
  and publishes and pushes it once the branch exists: an aborted start leaves
  no issue behind.
- `issue close` fetches the issue refs before writing `set_state closed`, so
  it works on a clone that knows the issue only through the branch ref.
- Open point raised by the review, not decided: an issue can only be closed by
  closing a branch for it. Duplicates and wontfix issues need a way to be
  closed by ID, and titles that produce no branch slug (non-Latin scripts)
  need a naming fallback.

Found while verifying, not raised by the review:

- `git commit-tree` ignores `commit.gpgsign`; the first draft claimed signing
  applied untouched. The writer now passes `-S` itself.
- `git fetch --prune` on the issue refspec deletes unpushed local issues. The
  spec now forbids it.
- Imported issues had no way to get a branch type. `set_branch_type` added.
- A deterministic commit also depends on the date's UTC offset and on
  `i18n.commitEncoding`; both are now pinned, with the committer date.


2026-10-06, the open points of this spec, closed outside both plans:

- `issue edit [<id>]` writes `set_title` and `set_description`, from a form or
  from `--title` / `--description`. Their fold cases ship with it instead of
  waiting for the bridge.
- `issue close <id>` writes `set_state closed` without a merge. It refuses
  while a branch of the issue is in progress.
- A title that slugs to nothing names its branch with the constant slug
  `issue` (`branch.FallbackSlug`); the start flow no longer refuses it.
