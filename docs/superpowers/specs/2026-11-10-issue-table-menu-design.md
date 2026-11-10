# Issue and branch tables: one design (enter menu, shortcut keys, readable key hints)

Date: 2026-11-10. Status: approved keys, awaiting approval of the document.

## Intent

The branch table (`docs/superpowers/specs/2026-11-08-branch-table-design.md`)
made enter the key that asks "what can I do with this row?": a menu whose
entries depend on the row. The issue table predates it and spreads its
actions over single keys (`s` start, `c` close, `a` abandon, `l` label, `e`
edit), with enter showing the issue. The two tables disagree on the one key
every user presses first.

Enter in the issue table opens the same kind of menu: the actions that make
sense for the issue under the cursor, show included. The keys stay as
shortcuts.

Second problem, same screens: the key line of the branch table reads
`S status  T type  R remotes  / filter  enter actions  n new  p prune  P
prune-tracker  q quit`. Nothing tells a key from its label; it reads as a
row of words. The issue table writes `s: start · n: new`, another notation
for the same thing. Both tables take one notation where the key is visibly
a key: `[S]tatus [T]ype [n]ew`.

Third: the issue table has a shortcut key per row action, the branch table
has none (its row actions live only behind enter). The branch table gets
the issue table's design: enter menu plus a key per action, the hints on
two lines (filters, then actions). Same design, same behavior.

## Goals

- Enter on a row opens a contextual menu; esc closes it and leaves the table
  as it was.
- The entries are the row's actions, by its state: a closed issue is not
  offered start or abandon, a row that is not a repository issue is not
  offered the record edits.
- Every entry runs the code the matching key or subcommand runs today.
- Comment, which has a subcommand but no key, becomes reachable from the
  table through the menu.
- The shortcut keys keep working unchanged, `a` pressed twice included.
- The key lines of the branch table and the issue table use one bracket
  notation (see "Key hints").
- The issue table reopens as it was left after an action: cursor, status
  tab, project, mine and `/` text, as the branch table does
  (`BranchTableState`). With a menu on every row, every action goes
  through a reopen. (My call, from "same design, same behavior".)
- Every row action of the branch table has a shortcut key (see "Branch
  table keys"); its hints are on two lines like the issue table's.

## Non-goals

- Changing the actions themselves (start, close, abandon, label, edit,
  comment, show): same prompts, same effects.
- New keys. Comment gets a menu entry, not a key.
- Changing what a key does or which case it takes: `p` and `P` are two
  actions in the branch table, so the hints show the exact key.
- The hint of `git zf log` and the `(esc: clear  enter: confirm)` line of
  the filter input.
- Moving the branch table's menu inside its table program, or changing
  its entries (`menuEntriesFor`).
- The pick mode of the table (the record picker of `issue abandon`, `label`,
  `edit`... without an ID): enter keeps picking the row.
- Configurable entries.

## Anti-goals

- A menu that sometimes opens and sometimes acts. Enter always opens the
  menu in list mode, even when the row has one sensible entry: a key whose
  effect depends on the row is worse than one extra keystroke.
- Fetching or reconciling on enter. The menu reads the row; the actions that
  need freshness fetch themselves, as today.

## The screen

```
  Issue ID  Title                      Branch                  Local Status  Issue Status  Created
  42        Assign issues to creator   42@feat@assign-creator  in_progress   open          2026-11-01
> 51        Prune remote branches      ∅                       ∅             open          2026-10-30
  7         Fix the logo               7@fix@logo              merged        closed        2026-10-20

  Issue 51: Prune remote branches
  > show
    start
    edit
    label
    comment
    abandon (no undo)

[tab]-Status  [p]roject  [m]ine  [/]-Filter
[enter]-Actions  [e]dit  [s]tart  [n]ew  [c]lose  [a]bandon  [l]abel  [r]-Sync
```

The menu is a huh select drawn under the table, like the `S` and `T`
pickers of the branch table: the table stays visible, the cursor row is the
one the menu is about. Its title is `"Issue " + Row.IssueSlug + ": " + Row.Title`, the tracker number left out. Esc closes
it. The footer gains `[enter]-Actions` in front of the keys.

## Key hints

One notation for both tables, written by one helper:

- the key is in brackets, exactly as typed: `[S]`, `[p]`, `[P]`, `[/]`,
  `[enter]`, `[tab]`;
- when the key is one letter and the label starts with it, whatever the
  case (`strings.EqualFold(key, label[:1])`), the hint is
  `"[" + key + "]" + label[1:]`: the key keeps its case, the rest of the
  label is as given. `[S]tatus`, `[n]ew`, `[C]heckout`, `[c]lose`,
  `[P]rune-tracker`;
- otherwise the key is joined to the capitalized label by a dash:
  `[r]-Sync`, `[/]-Filter`, `[enter]-Actions`, `[tab]-Status`;
- hints are separated by two spaces; the issue table drops its `·`.

The branch table, on two lines like the issue table (filters, then
actions), without `q`:

```
[S]tatus  [T]ype  [R]emotes  [/]-Filter
[enter]-Actions  [C]heckout  [c]lose  [m]erge  [u]-Pull request  [a]bandon  [t]rack  [l]og  [D]elete  [n]ew  [p]rune  [P]rune-tracker
```

`[R]emotes` shows only with a remote, as today. The issue table keeps its
two lines and still fits 90 columns without `q`; pick mode reads
`[enter]-Pick  [q]uit`. The branch action line is longer than 90 columns
and is left to wrap. (My call.)

The bracket is plain text, no color: it must read in a terminal without
styles and in the test's view string. (My call.)

## The contextual menu

Entries, in this order, by row kind. A row is a *repository issue* when
`Row.State` is set (`refs/zf/issues/*`); a tracker-only row or a legacy
branch-only row is not. Open and closed are `Row.MatchesStatus`, as the
status filter reads them.

| Row kind                                        | Entries                                                   |
|-------------------------------------------------|-----------------------------------------------------------|
| repository issue, open, no branch               | show, start, edit, label, comment, abandon (no undo)      |
| repository issue, open, branch in progress      | show, start, close (merge), edit, label, comment          |
| repository issue, closed                        | show, edit, label, comment                                |
| not a repository issue, open, no branch         | show, start                                               |
| not a repository issue, open, branch in progress| show, start, close (merge)                                |
| not a repository issue, closed (merged branch)  | show                                                      |
| not a repository issue, branch closed (abandoned)| show                                                     |

"branch in progress" is `Row.Branch != nil && Row.Branch.Status ==
in_progress`; a merged or closed branch on an open issue counts as no
branch (start offers a new one, close has nothing to merge).

Abandon is not offered while a branch is in progress: `runAbandon` refuses
that case (merge or abandon the branch first), and the menu offers what
will work. The `a` key is unchanged and still prints that refusal. (My
call.) A row that is not a repository issue and whose branch is closed is
neither open nor closed for `Row.MatchesStatus`; `s` refuses it today, so
its menu is show alone. (My call.)

`issueMenuEntries` reads the row alone, `Row.Branch` included. An issue
with several branches has one row per branch: on the row of its merged
attempt abandon is offered although a sibling branch is in progress.
`runAbandon` stays the guard: it refuses, `listAction` prints the error
and the table reopens.

The menu drops the entries the table cannot run: show without
`IssueActions.Show`, edit without `Load` and `Edit`. `issueMenuEntries`
stays pure; the model filters its result, as `offeredEntries` does in
`cmd/branch`.

What each entry runs:

- show: the dialog enter opens today (`IssueActions.Show`), in the table.
- edit: the edit dialog `e` opens today (`IssueActions.Load`/`Edit`), in
  the table.
- start: quits with `IssuePickRow`, as `s` does; `listAction` runs
  `issueflow.StartRow`. The closed-row refusal of `s` is moot: the entry is
  not offered on a closed row.
- close (merge): quits with `IssuePickClose`, as `c` does.
- label: quits with `IssuePickLabel`, as `l` does.
- comment: quits with a new `IssuePickComment`; `listAction` runs
  `runComment(ctx, client, p, []string{slug}, "", mirror)`, the form of
  `git zf issue comment <id>`.
- abandon (no undo): a huh confirm under the table ("Abandon 51? There is
  no undo."), then quits with `IssuePickAbandon`, as the second `a` does.
  No answers close the confirm and leave the table as it was. The `a` key
  keeps its press-twice arming: two idioms, one action, both deliberate.

The quit-and-reopen contract is unchanged: `listAction` runs the action
with the terminal free, prints its error, reopens the table on
`huh.ErrUserAborted` and after every action but a start (the operator is
left on the new branch).

Pick mode (`IssueActions.Pick`): enter picks the row and quits, no menu, as
today. The start flow's own picker is this mode too.

## Branch table keys

Each row action of the menu (`menuEntriesFor`) gets a key. `c` and `a`
mean what they mean in the issue table.

| Key | Action                                | Menu entry         |
|-----|---------------------------------------|--------------------|
| `C` | checkout                              | `actionCheckout`   |
| `c` | issue close (merge)                   | `actionIssueClose` |
| `m` | merge into current                    | `actionMerge`      |
| `u` | pull request                          | `actionPR`         |
| `a` | abandon, pressed twice                | `actionAbandon`    |
| `t` | track                                 | `actionTrack`      |
| `l` | log                                   | `actionLog`        |
| `D` | delete, pressed twice                 | `actionDelete`     |

- A key runs exactly what picking its entry in the menu runs
  (`runBranchAction`), prompts included, and the table reopens as after a
  menu pick.
- A key whose entry is not offered for the row (`offeredEntries`: the
  row's state, a nil func) does nothing and shows a notice under the
  table, as `s` does on a closed issue ("no merge for this branch"). The
  table does not quit for it. `a` and `D` are checked on the first press:
  a row that offers no abandon or delete gets the notice at once and is
  never armed.
- `a` and `D` arm on the first press (a notice: "press a again to
  abandon"), act on the second, and any other key disarms: the issue
  table's `a`. Delete's own force-delete confirm still runs after.
- `u` and `d` scroll half a page in the bubbles table today. `u` becomes
  pull request and stops scrolling; delete is `D` so that `d` keeps
  scrolling and `d d` never deletes. `ctrl+u`, `ctrl+d`, page up and page
  down scroll as before.
- The existing keys keep their meaning: `S`, `T`, `R`, `/`, `n`, `p`, `P`,
  `q`, enter.

## Command surface

- `git zf issue` / `git zf issue list`: enter opens the menu. The Long of
  `list` describes the menu first, then the keys as shortcuts; `make docs`
  regenerates `website/docs/reference/commands/git_zf_issue_list.md`.
- `--stdout`, `--json`, no TTY: unchanged.
- No new subcommand or flag.

## Architecture

- `tui/issue.go`:
  - `issueMenuEntries(row *issue.Row) []issueMenuEntry` is a pure function
    of the row, table-tested, like `menuEntriesFor` in `cmd/branch`. An
    `issueMenuEntry` is a label and what it does: open the show dialog, open
    the edit dialog, quit with an `IssuePick`, or confirm then quit.
  - Enter in list mode opens `tui.Form(tui.MenuSelect(title, opts, &picked))`
    as a nested form under the table, the way the branch table runs `S` and
    `T` (`form`/`formKind` fields; the edit dialog already runs a nested
    `*huh.Form`, the builder may share or add a field). Esc closes it.
  - `IssuePickComment` joins the `IssuePick` constants.
  - The footer gains `[enter]-Actions`.
- `tui`: `keyHint(key, label string) string` renders one hint by the rule
  of "Key hints"; `branch.go`'s key line and `issue.go`'s `filterHint` and
  `hint` are built with it.
- `cmd/issue/list.go`: a `tui.IssuePickComment` case in `listAction`; the
  `list` Long.
- `CLAUDE.md`, "Testing the menus": the issue table paragraph says enter
  opens the row's menu (`issueMenuEntries`) and lists comment among the
  picks.
- `tui/issue.go`, `cmd/issue/list.go`: an `IssueTableState(m)` returning
  what rebuilds the table as it was left (cursor, status tab, project,
  mine, `/` text), passed to the next `IssueTableModel` by the list loop,
  on the model of `BranchTableState` / `BranchTableOptions`. A cursor past
  the end of the new rows is clamped.
- `tui/branch.go`: `BranchTableOptions` gains a func telling whether a row
  offers an action (the model knows keys and action names, not
  `cmd/branch`'s entries). An offered key quits the table with the row and
  the action asked for (a new `BranchPick`, or a field next to
  `BranchPickRow`); a key not offered sets the notice and stays. The
  arming of `a` and `D` and the notice line live in the model, as in
  `tui/issue.go`. The two hint lines.
- `cmd/branch/table.go`: passes that func, built on `offeredEntries(d,
  row)`; the table loop maps the action asked for to a `branchAction` and
  runs `runBranchAction`.
- `cmd/branch/branch.go`: the Long lists the keys; `make docs`.
- `CLAUDE.md`, "Testing the branch table": the keys.
- Nothing in `issueflow`, `issue` or `git` changes.

## Tests

Following the repository's pattern: `t.Run` per assertion, real rows,
bubbletea messages for the table.

- `tui/issue_test.go`:
  - `TestIssueMenuEntries`: one case per row kind of the table above,
    plus "a merged branch on an open repository issue is no branch".
  - `TestIssueTable_Menu`: enter in list mode opens the menu (the view
    shows the first entry); esc closes it and the table is unchanged;
    picking start, close, label, comment quits with that pick and the
    row's slug; picking show opens the show dialog; picking edit opens the
    edit dialog; picking abandon opens the confirm, no leaves the table,
    yes quits with `IssuePickAbandon`; enter in pick mode still picks the
    row and never opens the menu; the footer contains `[enter]-Actions`.
  - `TestIssueTable_ActionKeys`: unchanged, proving the shortcuts stay.
  - `TestIssueTable_EnterOpensDialog`: becomes "show from the menu opens
    the dialog", or is folded into `TestIssueTable_Menu`.
- `tui`: `TestKeyHint`: `S`/`status` gives `[S]tatus`, `P`/`prune-tracker`
  gives `[P]rune-tracker`, `r`/`sync` gives `[r]-Sync`, `enter`/`actions`
  gives `[enter]-Actions`. The existing view assertions of both tables
  that match the old key lines are updated.
- `tui/branch_test.go`, `TestBranchTable_ActionKeys`: each key quits with
  its action and the row; `a` and `D` once arm and show the notice, twice
  act, another key disarms; the view has the two hint lines.
- `cmd/branch/table_test.go`: a key on a row that offers its entry runs
  what the menu entry runs (one case per key, on the existing rig); the
  func handed to the table answers as `offeredEntries` does.
- `tui/branch_test.go`: a key the row does not offer shows the notice and
  the table does not quit; `a` and `D` on such a row show it on the first
  press and do not arm.
- `tui/issue_test.go`: the menu has no show entry without
  `IssueActions.Show`, no edit entry without `Load` and `Edit`;
  `TestIssueTableState`: a table rebuilt from its state has the same
  cursor, tab, project, mine and filter text; a cursor past the rows is
  clamped.
- `cmd/issue`: after an action the list loop reopens the table with the
  state it was left in.
- `tui`: `TestKeyHint` also covers `C`/`checkout` and `c`/`close`.
- `cmd/issue/record_ops_e2e_test.go`, `TestListAction`:
  `IssuePickComment` runs the comment form on the row's issue and reopens
  the table; esc on the form reopens quietly.
- `cmd/issue/issue_test.go`: the `list` Long names the menu
  (`TestIssueListCmd_Long`, like `TestBranchRootCmd_Long`).

## Left to the builder

- Whether the menu reuses the edit dialog's `form` field with a kind, or
  gets its own. Either is fine; the branch table's `form`/`formKind` is the
  precedent.
- The exact confirm wording for abandon, as long as it says there is no
  undo.
- Whether `TestIssueTable_EnterOpensDialog` is rewritten or folded.
- Making the branch table draw its menu under the table too, for the same
  look, is a follow-up, not part of this change.
