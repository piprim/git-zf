# Design: branch refs as commit chains, SQLite removed

**Date:** 2026-10-05
**Status:** implemented

## Overview

Issues and reviews are commit chains under `refs/zf/`. Branch tracking is not.
It is split between two places that drift apart:

- the SQLite store (`.git/git-zf.db`): tables `issues`, `branches`,
  `issue_relations`, per clone, never shared;
- a JSON blob at `refs/zf/branches/<slug>`, force-pushed, holding one branch
  per issue and a `merged` flag.

Every picker reconciles one from the other (`ReconcileMergedFromRefs`,
`CloseCandidates`, `TrackCandidate`, the two-step lookup of
`ResolveParentBranch`). The blob loses data the store has (variant branches,
titles, the `closed` status); the store misses what other clones did.

This spec makes `refs/zf/branches/<slug>` a chain like the other two and
deletes the store. The chain holds what the three tables held. The last table,
`command_history`, becomes a file. `modernc.org/sqlite` leaves `go.mod`.

It is the last of the three steps announced in the review-chains spec, with
steps two and three merged. The `reviews` table is already gone (migration
`0007`).

**Decisions taken during brainstorming:**

- Storage: one chain per issue slug, at the ref name the blobs use today. A
  chain holds every branch of the issue (variants included), the issue's title,
  tracker type, repo issue ID and parent.
- Existing data: nothing is migrated. `git-zf.db` is never read again. A blob
  ref is reported as legacy and replaced by the next `issue start` or
  `issue track` on that issue.
- Prune closes a branch only when it is gone everywhere (see "Prune").
- Pickers that act on a branch offer only branches present in this clone. Lists
  show everything the chains know.
- `command_history` moves to a JSON Lines file in the git common dir.

**Decisions I added while writing; veto any of them:**

- (A) `issue track` and `issue start` read the blob they replace and keep its
  `parent_slug`, `tracker_type` and `issue_id`. Without this, re-tracking a
  sub-task loses its parent and `issue close` would propose the base branch
  instead of the parent's integration branch.
- (B) Prune closes a branch that is gone everywhere only when this clone's git
  identity wrote its `start` op. Two cases look "gone everywhere" from another
  clone and are not this clone's to close:
  - a branch a teammate just started. `issue start` pushes the chain at once
    and never pushes the branch, so until the teammate's first `git push` the
    remote has the `start` op and no branch;
  - a branch a teammate pushed, then deleted without merging. Closing it is
    their call; their own prune does it.
- (C) All chains of a namespace are read in three git processes, whatever
  their number. Nearly every command lists branch chains, and there is one per
  issue ever started; the per-chain cost accepted for `review.List` does not
  hold here.

## Section 1: data model

### Ref layout

| Ref | Content |
|---|---|
| `refs/zf/branches/<slug>` | The local branch chain of issue `<slug>`. |
| `refs/remotes/<remote>/zf/branches/<slug>` | What `<remote>` had at the last fetch or push. |

Same rules as reviews: fetch writes only the tracking namespace; a local chain
is never the destination of a fetch; no chain ref is ever deleted.

`<slug>` is what `branch.Parse(name).IssueID()` returns for any branch of the
issue. The chain of a branch is therefore found from its name alone, with no
index.

### Ops

One commit per action, one `op.json` in its tree, the envelope of the other
chains: `v` (1), `type`, `at` (RFC 3339, UTC), `author`.

| `type` | Fields | Written by |
|---|---|---|
| `start` | `branch`, `branch_type`, `title`, `parent`, `tracker_type`, `issue_id` | `issue start`, `branch new`, `issue track`. Root commit for the issue's first branch. |
| `set_status` | `branch`, `status` (`in_progress`, `merged`, `closed`) | `issue close`, `branch prune`, `branch prune-tracker`; `issue start` on a branch name the chain already knows as merged or closed. |
| `merge` | none | reconcile; two parents |

`parent` is the parent issue's slug. `issue_id` is the full ID of the repo
issue (`refs/zf/issues/<id>`), empty for a tracker-born or untracked issue.
`title` is the issue subject as typed or fetched; `issue track` derives it from
the branch name.

### Fold

Ops are linearized by `chain.Order`, as for issues and reviews.

```go
type Entry struct {
    Name      string    // full branch name
    Type      string
    Status    string    // in_progress, merged, closed
    Author    string    // author of the start op
    CreatedAt time.Time // at of the start op
    UpdatedAt time.Time // at of the last applied op naming this branch
}

type State struct {
    Slug        string
    Title       string
    TrackerType string
    IssueID     string
    Parent      string
    Entries     []Entry  // in start order
    Warnings    []string // commits skipped as malformed
}
```

| Op | Applies when | Effect |
|---|---|---|
| `start` | `branch` is not in `Entries` | Appends an entry, status `in_progress`. |
| `start` | always | Sets each of `Title`, `TrackerType`, `IssueID`, `Parent` that is still empty and that the op carries. |
| `set_status` | `branch` is in `Entries` and `status` is one of the three | Sets the entry's status and `UpdatedAt`. |
| `merge`, unknown type, unknown version | never | Skipped. |

Outcomes for concurrent actions:

- Two `start` ops for the same branch (two clones tracked it): one entry. The
  first in the linear order is kept.
- Two `start` ops for different branches of one issue: two entries.
- Issue-level fields are first-writer-wins, so they do not flip after a sync.
- Two `set_status` ops: the later in the linear order wins. A close on one
  clone and a prune on another both say `merged` or the close sorts last.
- Two clones creating the root of the same slug: unrelated histories, joined by
  a `merge` commit at reconcile, as for reviews.

A `start` never reopens a branch. `issue start` on a name the chain knows as
merged or closed writes `set_status in_progress` instead.

### The flat view

Commands and the TUI work on rows, one per branch:

```go
type Row struct {
    IssueSlug  string    `json:"issue_slug"`
    Title      string    `json:"title"`
    BranchName string    `json:"branch_name"`
    Type       string    `json:"type"`
    Status     string    `json:"status"`
    CreatedAt  time.Time `json:"created_at"`
}
```

This is `store.BranchRow` without `IssueID`, the SQLite primary key. `branch
list --json` loses its `issue_id` field.

### Old blob refs

A `refs/zf/branches/<slug>` that points at a blob was written by an older
git-zf.

- `branch.Load` returns `ErrLegacyBranch` for it. `branch.List` skips it and
  returns one warning naming `git zf issue track`.
- `issue start` and `issue track` replace it, in this order:
  1. `branch.Fetch`, as before any write. Reconcile replaces a local blob by
     the remote chain when the remote has one (`reconcileChainRef` does this
     already). So when another clone converted the issue first, there is no
     blob left to replace: the op is appended to that chain and the steps below
     are skipped.
  2. Read `parent`, `tracker_type` and `issue_id` from the blob. The root's
     `start` op takes them when the command has no value of its own
     (decision A). The blob's `merged` flag and `created_at` are dropped.
  3. Write the chain root. A signing failure leaves the blob in place.
  4. Delete the blob ref locally. Delete it on the remote only if the tracking
     ref showed a blob at step 1, and only if the remote still holds that blob:
     `git push --force-with-lease=<ref>:<blob sha> <remote> :<ref>`. A chain
     pushed by another clone in the meantime makes the lease fail and is left
     alone.
  5. Publish the root and push. A push rejected because the remote now has a
     chain goes through the usual fetch, merge and retry.
- Two clones that convert the same issue while both are offline each create a
  root. This is the "two roots" case of the fold: reconcile joins them with one
  two-parent commit (written with `commit-tree`, no content merge), and the
  second `start` for the same branch is a duplicate.
- A legacy ref is treated as "not tracked", except where that would change a
  merge target: when an issue's `Parent` names a slug whose ref is a blob,
  `issue close` and `review sync` refuse and name `git zf issue track` for the
  parent's branch. An absent parent chain keeps today's fallback to the base
  branch.
- Reconcile already skips tracking refs that are not commits.

## Section 2: code and commands

### `git/`

- `BranchRefs = ChainRefs{name: "branches"}`. `ConfigureChainFetch` adds its
  refspec with the other two.
- `git/branch_ref.go` and `git/blob_ref.go` are deleted. `LegacyBlob` is what
  the legacy replacement needs: it reads the JSON blob a chain ref points at.
- `DeleteChainRef` takes the blob SHA it expects on the remote and deletes
  there with a lease, instead of an unconditional `push --delete`. Today a
  `review request` replacing a legacy blob can delete a chain another clone
  pushed a moment before; the same fix covers it.
- `ReadAllChains(ctx, ns) (map[string][]ChainCommit, error)` (decision C): one
  `for-each-ref` for the tips, one `git log --stdin` for every commit reachable
  from them, one `cat-file --batch` for the payloads; commits are assigned to
  chains by walking parents in memory. `branch.List` and `review.List` both use
  it, which removes the `ponytail:` note in `review/repo.go`.
- The bodies of `review.Fetch`, `review.Push` and `review.Sync` differ from
  what branches need only by namespace and merge payload. They move to
  `git.Client` methods taking both; `review` and `branch` call them.

### `branch/`

The package keeps its naming functions. Two files are added, mirroring
`review/`: `record.go` (`Op`, `Entry`, `State`, `Row`, `DecodeOp`, `Fold`; pure)
and `repo.go` (the glue).

| Function | Does |
|---|---|
| `Load(ctx, c, slug) (*State, error)` | Reads and folds one chain. `nil` when absent, `ErrLegacyBranch` for a blob. |
| `List(ctx, c) ([]State, []string, error)` | Every local chain, through `ReadAllChains`. |
| `Rows(states, status) []Row` | Flattens, newest `CreatedAt` first. `status` `""` keeps all. |
| `Find(ctx, c, branchName) (*State, *Entry, error)` | Parses the name, loads the slug's chain, returns the entry. `nil, nil` when the name does not parse or is not tracked. |
| `Start(ctx, c, slug, op) error` | Fetches first. Appends `start`, or `set_status in_progress` when the branch is known and not in progress; creates the root when the chain is absent, replacing a legacy blob that is still one after the fetch. |
| `SetStatus(ctx, c, slug, branchName, status) error` | Appends `set_status`. |
| `Fetch`, `Push`, `Sync` | As in `review/`. |

Every write is pushed right away. A failed push is a warning; the op stays
local and the next `Sync` pushes it.

### Which branches a command sees

| Command | Reads | Fetches first |
|---|---|---|
| `branch list`, `issue list` | Every row of every local chain, after a local reconcile with the tracking refs. | No. A plain `git fetch` brings the chains once `git zf init` has run. |
| `review request`, `review sync` pickers | In-progress rows whose branch exists locally. | Yes, best-effort. |
| `issue close` picker | In-progress rows whose branch resolves locally or on the remote (`ResolveBranchRef`). | Yes, best-effort. |
| `branch prune`, `branch prune-tracker` | See "Prune". | Yes. |
| pre-commit and pre-push guards, `commit` | One chain, found from the branch name. | Never. |

`branch list` now shows the team's branches, not only this clone's.

### Call sites

| File | Change |
|---|---|
| `cmd/issueflow/start.go` | `persist`, `InsertIssueRelation` and `writePushBranchRef` become one `branch.Start` and `Push`. `--parent` resolves the parent's branch from its chain. `resolveParentSlug` only parses the base branch name. |
| `cmd/issueflow/parent.go` | `ResolveParentBranch` reads `State.Parent`, then the parent's chain: its newest entry's name. The `ParentStore` interface goes away. |
| `cmd/issueflow/closecandidates.go` | `CloseCandidates` filters rows. `MaterializeBranch` stays. `TrackCandidate` and `synthRow` are deleted: a candidate is already tracked. |
| `cmd/issueflow/reconcile.go` | Deleted. A `branch.Fetch` replaces every call. |
| `cmd/issueflow/review_guard.go` | `IssueSlugForBranch` becomes `branch.Find`. |
| `cmd/issue/close.go` | `updateClosedStatus` calls `SetStatus(merged)` and `Push`; the repo issue and the tracker gate read `State.IssueID` and `State.TrackerType`. The children check reads the chains (below). |
| `cmd/issue/list.go` | Rows from `branch.List` instead of the store. |
| `cmd/branch/branch.go`, `prune_tracker.go`, `merge.go` | Rows from `branch.List`; writes through `SetStatus`. |
| `cmd/review/*` | `request`, `sync`, `status`, `guard` read rows; `track` on a feature branch calls `branch.Start`; `tracker.go` reads `State.TrackerType`. `reviewDeps.store` is removed. |
| `cmd/commit/commit.go`, `commit/form.go` | Branch lookup through `branch.Find`; history through the file (below). |
| `tui/`, `tty/` | `store.BranchRow` becomes `branch.Row`. `store.IssueRow` and its helpers (`TitleWithLabels`, `IssueRowCells`, `UniqueProjects`, ...) move to `issue/row.go`. |
| `cmd/init/init.go` | Third fetch refspec. One line when `.git/git-zf.db` exists: the file is no longer used and can be deleted. |
| `store/` | Deleted, with its migrations. |

### Parent and children

- Parent of an issue: `State.Parent` of its chain.
- Children of an issue: the states whose `Parent` is its slug.
- `issue close` on a parent is refused until every entry of every child is
  `merged`. A `closed` child entry blocks, as a `closed` store row does today.

### Prune

`branch prune` asks the remote for its branches (`git ls-remote --heads`), then
looks at every in-progress row:

| Branch is | Action |
|---|---|
| present locally and merged into the base | `set_status merged` |
| present locally and not merged, or present only on the remote | none |
| gone locally and on the remote, `start` written by this clone's identity | `set_status closed` |
| gone locally and on the remote, `start` written by someone else | none; listed as skipped (decision B). `set_status closed` with `--others` |

When the remote cannot be reached, nothing is closed: "gone on the remote"
cannot be established. Merges are still recorded. Without a remote, "gone locally" is
"gone everywhere".

The summary wording changes from "Will delete" to "Will close". No chain is
deleted.

`branch prune-tracker` keeps its rules and writes `set_status` where it updates
the store today.

### Commit history file

`commit/history.go` implements the `historyStore` interface of `commit/form.go`
on `<git common dir>/git-zf-history.jsonl`, shared by all worktrees as the
database was.

```json
{"command":"commit","at":"2026-10-05T09:12:44Z","payload":{"type":"feat","subject":"..."}}
```

A save reads the file, appends, keeps the newest 100 entries and writes the
result with a rename. A line that does not parse is skipped. `ponytail:` two
commits finishing at the same instant in two worktrees may lose one entry; add
a lock file if that is ever seen.

## Section 3: error handling

| Situation | Behavior |
|---|---|
| Push of an op fails | Warning; the op stays local; the next `branch.Sync` pushes it. |
| Push rejected (someone pushed first) | One fetch, merge and retry. |
| Push rejected because the remote ref is a blob | Warning naming `git push <remote> --delete refs/zf/branches/<slug>`. Happens when an older git-zf force-pushed a blob after the upgrade. |
| Fetch fails | Warning; the command continues on local chains. Prune closes nothing. |
| Legacy blob ref | See "Old blob refs". |
| Fetch fails while replacing a legacy blob | The root is created locally; the remote blob is not deleted (nothing was seen on the remote). The next `Sync` fetches, then merges with the remote chain or reports the remote blob as above. |
| Malformed op commit | Skipped by the fold, reported in `State.Warnings`. |
| Local branch with no chain | Not offered by any picker. `review request` and `issue close` say to run `git zf issue track`, as `review request` does today. |
| `set_status` for a branch the chain does not know | Ignored by the fold. Commands only write it for an entry they just read. |

## Upgrade

- Every clone of a repository upgrades together. An older binary reads a chain
  ref as "no ref" and its `issue start` force-pushes a blob over the remote
  chain.
- After the upgrade no branch is tracked. For each branch still in progress:
  check it out and run `git zf issue track`. Merged and closed history is not
  carried over.
- `git zf init` adds the new fetch refspec.
- `.git/git-zf.db` is left in place and never read. The commit form's history
  starts empty.

## Testing

All on real on-disk repos; every assertion in a `t.Run`.

- `branch/record_test.go`: a `TestFold` table. One row per rule, plus: a second
  `start` for a known branch; two concurrent starts of two variants;
  first-writer-wins for each issue-level field; concurrent `set_status` in both
  sort orders; `set_status` for an unknown branch; unknown type and version
  skipped.
- `git/`: `ReadAllChains` against `ReadChainCommits` on the same repository,
  including a merge commit and two chains sharing no history; the branches
  refspec in `ConfigureChainFetch`.
- `branch/repo_test.go`: `Start`, `SetStatus`, `Load`, `List`, `Find`, `Sync`;
  two clones starting variants offline then syncing; a legacy blob replaced,
  with its parent carried over. Two clones holding the same legacy blob: the
  second to run `Start` appends to the first one's chain and creates no root;
  both converting offline end with one merge commit and one entry; a chain
  pushed between the fetch and the delete survives the lease.
- `branch/branchtest`: `Seed(t, c, slug, branchName, status)`, used by every rig
  that calls `store.InsertIssueWithBranch` today (55 call sites).
- The start, close, merge, prune, review and repo-issue E2E suites are ported
  and stay green. New cases: close of a sub-task whose parent ref is a blob is
  refused; prune leaves alone a teammate's branch that has a chain and no
  remote branch (just started, or deleted by its author); prune closes nothing
  when the fetch fails; `branch list` shows a branch started in another
  clone.
- `commit/history_test.go`: append and read back, the cap, a corrupt line.
- `store/` tests are deleted with the package.

## Documentation

- `docs/branch-refs.md`: the format, as `docs/review-refs.md` does for reviews.
- README: the "local SQLite store" wording in `issue start`, `issue list`,
  `issue close`, `branch prune` and `prune-tracker`; an upgrade note.
- `CLAUDE.md`: the testing sections that mention a seeded store; the
  architecture list.

## Out of scope

- Reading or importing `git-zf.db`.
- Importing blob refs in bulk. Each is replaced when its issue is next started
  or tracked.
- A command to edit the title, parent or tracker type recorded by `start`.
- Deleting or archiving old chains.

## Revisions

- (a) 2026-10-05, external review: replacing a legacy blob fetches first, so a
  clone that converts second appends to the existing chain; the remote blob is
  deleted with a lease on its SHA. The review's stated failure (two roots break
  the merge) does not occur, since reconcile writes the join with `commit-tree`
  and the fold already handles two roots. What it pointed at was real: `Start`
  did not say it fetches, and the remote delete was unconditional.
- (b) Same review: the justification of decision B now names both cases. The
  review's premise that an unpushed branch has an unpushed chain is wrong for
  git-zf: `issue start` pushes the chain immediately and never the branch.
- (c) Implementation: prune records a merge only for a branch that exists
  locally. A branch that exists only on the remote and has no commit of its own
  yet has the base's tip, so it would read as merged; today's rule (local
  branches only) is kept.
- (d) Implementation: prune lists the remote's branches with `git ls-remote`
  instead of `git fetch --prune`. It learns the same thing and does not touch
  the user's remote-tracking refs.
- (e) Implementation: `branch list` and `issue list` reconcile the local chains
  with the tracking refs before reading (no network). Without it a chain
  brought by a plain `git fetch` stayed invisible until a command that fetches
  ran. Found by running the binary on two clones.
- (f) Implementation: `branch merge` is not in the visibility table. Its picker
  lists git branches and refuses issue branches; it never read the store.
- (g) Implementation: `git zf commit` no longer fetches when it resolves the
  parent branch for the merge preview. `ResolveParentBranch` reads local chains
  only; `issue close` fetches before calling it.
- (h) Side review: decision B alone left some branches in progress for ever,
  with no command to close them: those of someone who left or never prunes,
  and one's own after a change of git name or email (the identity is the exact
  `Name <email>` string). `branch prune --others` closes vanished branches
  whatever their author; the summary names each author before the
  confirmation. So that such a close can be undone, `issue track` on a branch
  recorded as closed reopens it (it still leaves a merged branch alone).
- (i) `branch close <branch-name>` records one named branch as closed and
  pushes it: no merge, no git branch deleted, no author guard, since naming the
  branch is the intent. A branch already merged or closed is left as it is. It
  takes exactly one argument and is not in the `git zf branch` menu.
