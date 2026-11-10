# `git zf branch`: a branch table with a contextual menu

Date: 2026-11-08. Status: draft, awaiting approval.

## Intent

`git zf issue` drops the user into the issue table, where keys act on the row
under the cursor. `git zf branch` today opens a huh menu of subcommand names
(`list`, `new`, `track`, `prune`, ...) and `branch list` asks a status form
before showing a read-only table of the tracked branches.

`git zf branch` becomes a table of every local branch, like `git branch`
shows, where the tracked ones carry their git-zf record (issue, type, status,
title, creation date) and the plain ones do not. Filters narrow the rows
without leaving the table. Enter on a row opens a menu of the actions that
make sense for that branch; the action runs with the terminal free and the
table reopens, as the issue table's keys do.

## Goals

- `git zf branch` shows every local branch at once: no form before the table.
- A plain branch (no git-zf record) is a first-class row; "track" is one of
  its actions.
- Filters by status and by type are multi-selects opened from the table
  (`S`, `T`); remote-only branches join the table on a toggle (`R`); `/`
  filters lines by text like the issue table.
- Enter opens a contextual menu whose entries depend on the row.
- Scripts keep working: `branch list --json` and `--stdout` are unchanged,
  and `git zf branch` with no TTY on stdin prints the table to stdout like
  `branch list --stdout`.

## Non-goals

- A redesign of the actions themselves: checkout, merge, pr, close, delete,
  track, prune, new keep their current behaviour and prompts. The table only
  dispatches to them.
- A merge of issue branches from the table: `branch merge` refuses issue
  branches and the table respects the refusal (the entry is not offered).
- Changing the issue table or the log view.
- Configurable keys or columns.

## Anti-goals

- A table that fetches or reconciles on open. Like `issue list`, the table
  is local work: it shows what the last fetch brought. The actions that need
  freshness (`track`, `close`, `merge`) fetch themselves, as today.
- Losing the subcommands. Every action stays reachable by name
  (`git zf branch prune`), for scripts and for the docs.

## The screen

```
  Branch                       Issue  Type  Status       Title                     Created     Last commit
* master                                                                                        2h ago
  42@feat@assign-creator       42     feat  in_progress  Assign issues to creator  2026-11-01  1d ago
  51@fix@prune-remote          51     fix   merged       Prune remote branches     2026-10-30  3d ago
  spike-log-colors                                                                  2026-10-28  6d ago

S status  T type  R remotes  / filter  enter actions  n new  p prune  P prune-tracker  q quit
```

Rows and columns:

- One row per local branch, in the order `git branch` lists them (name
  order). The current branch carries `*`.
- Issue, Type, Status, Title come from the branch record (`branch.Row`).
  A plain branch leaves them empty. A branch whose name parses as
  `<id>@<type>@<slug>` but that has no record is still a plain branch: the
  record is the source of truth, the name is not.
- Created: the record's `CreatedAt` for a tracked branch. For a plain
  branch, the author date of the branch's first commit not on the base
  branch, empty when the branch is at the base (builder's call: one
  `git log --reverse --format=%ad -1 base..branch` per plain branch; if
  that is too slow on big repos, drop the date for plain branches rather
  than batch it).
- Last commit: the tip's committer date shown relative ("2h ago"), read
  for every row in one `git for-each-ref --format=%(refname) %(committerdate:unix)`
  call over `refs/heads` (and `refs/remotes/<remote>` when `R` is on), and
  formatted in Go.
- A remote-only branch (visible with `R`) shows `<remote>/` in front of its
  name (the clone's remote, `client.Remote()`, not a hardcoded `origin`),
  its record columns if the branch is tracked, and its tip's date.
  `Row.BranchName` always holds the short name; the prefix is display only.

Filters, all combinable, shown in the footer when active
(`status: in_progress, plain · type: feat`):

- `S` opens a huh multi-select of statuses: plain, in_progress, merged,
  closed. All checked by default.
- `T` opens a huh multi-select of the types: the `commit-types` of the
  config plus "none" for plain branches. All checked by default.
- `R` toggles remote-only branches in and out. Off by default.
- `/` filters lines by text, like the issue table and the log view.

Both multi-selects are `tui.Form` forms (esc aborts and leaves the filter
as it was). They run inside the bubbletea program the way the issue table
nests its edit dialog (`m.form`), rendered under the table
(`table.View() + form.View()`) so the rows stay visible; the issue table's
`p` picker is a hand-rolled list and is not the model here. An empty table after a
filter shows "No branch matches." in place of the rows.

## The contextual menu

Enter on a row quits the table program with a pick, like the issue table's
keys; `runBranchTable` then opens a huh select (the `MenuPrompter` pattern of
`cmdutil.RunMenu`, with action labels instead of subcommand names) titled
with the branch name. Esc on the menu reopens the table.

Entries, in this order, by row kind:

| Row kind                         | Entries                                                        |
|----------------------------------|----------------------------------------------------------------|
| tracked, in progress             | checkout, issue close (merge), pull request, abandon, log, delete |
| tracked, merged                  | checkout, log, delete                                          |
| tracked, closed                  | checkout, track (reopens it), log, delete                      |
| plain                            | checkout, track, merge into current, pull request, log, delete |
| remote-only (R on)               | checkout (creates the local branch), log                       |
| the current branch, any kind     | the kind's entries minus checkout, merge into current, delete  |

"track" on a plain row is always offered: a name that does not parse opens
a form first (below). Every tracked branch has an issue slug (`Row.IssueSlug`,
the chain's slug: a tracker ID, a typed ID or the short hash of a repo
issue), so there is no "tracked without an issue" kind. `menuEntriesFor`
tests `RemoteOnly` first, then `Current`, then the record.

What each entry runs:

- checkout: `git.Client.Checkout`. A branch held by a linked worktree is
  refused with the worktree path in the message (`git.Worktrees`). For a
  remote-only branch the same `git checkout <name>`: git creates the local
  branch from `<remote>/<name>` with its upstream set, which
  `MaterializeBranch` (meant for a temporary merge source) does not.
- merge into current: `runMerge` with the row as the source, skipping
  `PickSource`. Offered only when `branch.Parse` fails on the name, since
  `runMerge` refuses issue branches. Every tracked branch has such a name
  (`branch new` and `track` both produce or require it), so the entry is
  offered on plain rows only; a `<slug>@review` branch is a plain row and
  keeps the entry, as `branch merge` accepts it today. The builder exposes a
  `runMergeSource(ctx, deps, prompter, source)` that `runMerge` also calls
  after its pick.
- pull request: `OpenPullRequest` of `cmd/branch/pr.go` with the row's
  branch, as `branch pr <branch>` does.
- issue close (merge): the merge flow of `git zf issue close <id>`, on the
  row's branch. `cmd/issue` exports `CloseIssue(ctx, cmd, appConfig,
  issueSlug, branchName)`, which builds the production `closeDeps` from the
  command (`buildCloseDeps`, the main-tree client, the tracker, the push
  confirm), sets `issueID` to the slug and a new `branchName` field that
  narrows the candidates to that one branch (an issue with two variant
  branches must not open the branch picker a second time), and runs
  `runClose` with the huh `ClosePrompter`. `cmd/branch` imports
  `cmd/issue`; `cmd/issue` does not import `cmd/branch` (only `cmd/root.go`
  does), so there is no cycle. Offered on every tracked in-progress row. It
  does not reopen the table: the close flow prints its summary and its `cd`
  hint.
- abandon: `runCloseBranch`, unchanged (records as closed, no merge, no
  delete). Labelled "abandon (record closed, no merge)" so it is not taken
  for the merge.
- log: `git zf log` restricted to the branch: the log view runs with the
  branch name appended to the command's arguments (`cmd/log.runLog` with
  `extra = []string{branchName}`), the way arguments after `--` reach
  `git log` today. Because the default commands carry `--all`, the view
  drops `--all`, `--branches`, `--remotes` and `--tags` from any command it
  runs while a branch is given (1-9 and `c` included); the `--exclude`
  options stay. Design call: strip rather than pick a specific command,
  since a user `[[log.cmds]]` array replaces the built-ins. The log view
  returns to the branch table on quit.
- delete: `git.Client.DeleteLocalBranch(force=false)`, which refuses an
  unmerged branch (`ErrBranchNotMerged`). On that error alone, one yes/no
  confirm offers the force delete (`DeleteLocalBranch(force=true)`); any
  other error (a branch held by a worktree) is printed as is. Deleting a
  tracked in-progress branch does not change its record: a local delete
  does not say whether a teammate still works on the branch, and `prune`
  checks the remote before recording it closed or merged. The row vanishes
  from the table, so the delete prints a reminder: "Branch X deleted. Its
  record is still in progress: p (prune) records what happened to it.".
- track: `review.TrackBranch(ctx, appConfig, client, name)`, the body of
  `review track` with the name as a parameter (`review track` keeps reading
  the current branch). It keeps its order: a `<slug>@review` name goes to
  `runTrackReviewer`, a name `branch.Parse` accepts to `runTrackDeveloper`
  (on a closed tracked row that is the documented way to reopen the
  branch), and only a name that is neither reaches `trackPlain`.

  `trackPlain` (`spike-log-colors`) opens the manual form of `branch new`
  (`StartPrompter.PickIssueFromUser`, ID, subject and type) and then
  renames and records the branch:
  1. The branch must not exist on the remote (`RemoteBranchExists`): a
     pushed branch is refused with "delete <remote>/<name> first or keep the
     branch plain". A branch held by a linked worktree is renamed in place
     (`git branch -m` updates the worktree's HEAD).
  2. The form's subject is prefilled from the branch name: its dashes and
     underscores replaced by spaces. `branch.TitleFromName` is not usable
     here, it returns "" for a name `branch.Parse` rejects; the builder
     adds a small helper or extends it with a plain-name fallback. `PickIssueFromUser` gains a `subject string`
     prefill parameter; its other callers pass "".
  3. An empty ID mints a repo issue the way `branch new` does, in the same
     two phases: `issue.Prepare` before the rename, `issue.Publish` and
     `issue.Push` after it, and the branch op carries `IssueID` = the
     record's full ID with the record's `DisplayID` as the slug. A typed ID
     is used as is, with no record.
  4. The new name is `<id>@<type>@<slug>` with the slug built from the
     subject by the function `branch new` uses. A name clash with an
     existing branch aborts before anything changes.
  5. `git branch -m <old> <new>` (new `git.Client.RenameBranch`), then
     `branch.Start` with the subject as the title and `branch.Push`, as
     `runTrackDeveloper` does.
  Esc on the form leaves the branch as it is.

  Remote-only rows offer checkout and log only, on purpose: the close,
  abandon and merge flows accept a remote-only branch, but the table asks
  the user to materialize it first so every record-changing action runs on
  a branch they hold. A later version can lift this.

After an action the table reopens, with two exceptions, as the issue
table's `s` does not reopen after a start: checkout (local or remote-only)
and `n` leave the operator on the branch they moved to, with the command's
output (the worktree `cd` hint included) as the last thing on screen.
`branchAction` returns a per-action `reopen` bool like `listAction`.

Global keys, each quits the table, runs, and reopens it (`n` excepted):

- `n`: `branch new` (`RunIssueStart` with the manual form), as today.
- `p`: `branch prune`, with its confirm.
- `P`: `branch prune-tracker`, with its prompts.
- `q`, ctrl+c: quit.

## Command surface

- `git zf branch` (no subcommand): the table. No TTY on stdin: the rows
  are printed to stdout by a new `tty.RenderBranchRows` with the table's
  seven columns (`*` marker, blank Created for a zero date), every local
  branch, no filter. This replaces `cmdutil.RunMenu` for the branch group;
  the root menu entry "branch" now opens the table.
- `git zf branch list`: unchanged flags (`--status`, `--stdout`, `--json`).
  Its interactive path becomes the same table, with `--status` preselecting
  the status filter and the status form removed. `--json`, `--stdout` and
  the no-TTY fallback keep their rows, renderer and shape: tracked branches
  only, `tty.RenderBranchTable` untouched. (Builder's call: the `--json`
  consumers are scripts; plain branches are not added to them.)
- `branch close <name>`, `merge`, `pr`, `prune`, `prune-tracker`, `new`,
  `track`: unchanged.
- The `Long` of the branch command and the docs page of `git zf branch`
  describe the table and its keys instead of the menu.

## Architecture

- `tui/branch.go`: `BranchTableModel` grows into the model of this design,
  structured like `tui/issue.go`: all rows, visible rows, the filters, a
  `BranchPick` (`BranchPickNone`, `BranchPickRow`, `BranchPickNew`,
  `BranchPickPrune`, `BranchPickPruneTracker`), `PickedBranch(m)` returning
  the pick and the row. The S/T pickers are `tui.Form` multi-selects run
  as nested forms, the way the issue table runs its pickers.
- `branch/record.go`: `Row` gains `Plain bool`, `Current bool`, `RemoteOnly
  bool`, `LastCommit time.Time` (zero when unknown; `BranchName` stays the
  short name). A new `branch.TableRows(ctx, c, includeRemote)`
  in `branch/repo.go` merges `LocalBranchNames` (+ `RemoteBranchNames` when
  asked) with `List` into one row per branch. It does not reconcile (the
  table is local work); `ListRows` keeps reconciling for `--json`/`--stdout`
  compatibility.
- `cmd/branch/table.go` (new): the loop of `git zf branch`, mirroring
  `cmd/issue/list.go`: `runBranchTable` builds the rows, runs the program,
  reads the pick, opens the contextual menu through a `BranchMenuPrompter`
  interface (production: huh; tests: scripted), runs the action with
  `branchAction`, reopens. Errors of an action are printed to stderr and the
  table reopens, as `listAction` does.
- `cmd/branch/branch.go`: the group's `RunE` calls `runBranchTable`;
  `listRunE` calls it too when interactive. The subcommands registration is
  unchanged.
- `cmd/review/track.go`: `runTrack` splits into the current-branch lookup
  and `TrackBranch(ctx, appConfig, client, name)`, exported for the table
  (`cmd/branch` already imports `cmd/review` for `TrackCmd`); the
  unparseable case returns a sentinel `ErrNotTrackable` the table turns into
  `trackPlain`.
- `cmd/issueflow/start_prompter.go`: `PickIssueFromUser(ctx, id, subject,
  allowedTypes)`; `tui.IssueInput` already takes the subject pointer.
- `cmd/branch/merge.go`: `runMerge` splits into the pick and
  `runMergeSource`.
- `cmd/branch/track.go` (new): `trackPlain(ctx, deps, prompter, name)`,
  the form, rename and record of a free-named branch; the parseable case
  delegates to `review.TrackBranch`.
- `cmd/issue/close.go`: `closeDeps.branchName` narrows the candidates;
  `CloseIssue(ctx, cmd, appConfig, issueSlug, branchName)` exported.
- `git/git.go`: `RenameBranch(ctx, old, new)`.
- `cmd/log`: export `RunLogFor(ctx, client, cfg, extra, isTTY)` or make the
  table call `runLog` through a small exported wrapper; builder's call.

## Tests

Following the repository's pattern: real on-disk repos, scripted prompters,
`t.Run` per assertion.

- `branch/repo_test.go`: `TestTableRows_` cases: plain and tracked rows
  merged, current flag, remote-only inclusion, a tracked record whose branch
  no longer exists locally is not a row (unless R and it is on the remote).
- `tui/branch_test.go`: `TestBranchTable_` cases: the status and type
  filters applied, the R toggle, `/` text filter, enter sets
  `BranchPickRow`, `n`/`p`/`P` set their picks, the footer shows active
  filters, the menu entries for each row kind (`menuEntriesFor(row,
  current)` is a pure function, table-tested).
- `cmd/branch/table_e2e_test.go` (new): a rig with a base, one tracked
  in-progress branch, one plain branch and one remote-only branch, driven
  by a `scriptedBranchMenuPrompter`: checkout a plain branch, checkout a
  remote-only branch (local branch created with its upstream), track a
  plain branch with a parseable name, delete a merged branch, force-delete
  after refusal, no force offer for a worktree-held branch, abandon an
  in-progress branch, merge a plain branch into the current, the current
  branch's menu has no checkout, no TTY prints the seven columns; `TestBranchTable_IssueClose` drives the
  issue close entry on a tracked row with the fake tracker and the close
  rig's scripted prompter (the row's branch is merged, the record is
  `merged`, and with two variant branches of one issue only the row's
  branch is a candidate); `TestBranchTable_TrackPlain` renames `spike-log-colors` to
  `<id>@feat@<slug>` with a typed ID, mints an ID from the form when it is
  empty, refuses a branch that exists on the remote, aborts on a name
  clash, and renames a branch held by a linked worktree.
- `cmd/branch/branch_test.go`: `TestBranchRootCmd` no longer expects the
  menu; `TestGetRootCmd_menu` in `cmd/` still lists "branch".
- `cmd/branch/merge_e2e_test.go`: `runMergeSource` covered by the existing
  `TestRunMerge_` suite through `runMerge`.

## Left to the builder

- Column widths and the handling of a narrow terminal (the issue table's
  approach is the reference).
- Whether the Created date of a plain branch is computed or dropped (see
  above).
- The exact wording of the footer, the menu title and the refusal messages.
- Whether `BranchMenuPrompter` reuses `cmdutil.MenuPrompter` with synthetic
  cobra commands or takes a `[]MenuEntry`; a plain entry type is expected
  to be simpler.
