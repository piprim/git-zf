# `git zf issue`: enter opens a contextual menu

Date: 2026-11-10. Status: draft, awaiting approval.

## Intent

The branch table (`docs/superpowers/specs/2026-11-08-branch-table-design.md`)
made enter the key that asks "what can I do with this row?": a menu whose
entries depend on the row. The issue table predates it and spreads its
actions over single keys (`s` start, `c` close, `a` abandon, `l` label, `e`
edit), with enter showing the issue. The two tables disagree on the one key
every user presses first.

Enter in the issue table opens the same kind of menu: the actions that make
sense for the issue under the cursor, show included. The keys stay as
shortcuts. Nothing else about the table changes.

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

## Non-goals

- Changing the actions themselves (start, close, abandon, label, edit,
  comment, show): same prompts, same effects.
- New keys. Comment gets a menu entry, not a key.
- Changing the branch table, or moving its menu inside its table program.
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

enter actions  s start  e edit  n new  c close  a abandon  l label  r sync  / filter  tab status  p project  m mine  q quit
```

The menu is a huh select drawn under the table, like the `S` and `T`
pickers of the branch table: the table stays visible, the cursor row is the
one the menu is about. Its title is the issue's slug and title. Esc closes
it. The footer gains `enter actions` in front of the keys.

## The contextual menu

Entries, in this order, by row kind. A row is a *repository issue* when
`Row.State` is set (`refs/zf/issues/*`); a tracker-only row or a legacy
branch-only row is not. Open and closed are `Row.MatchesStatus`, as the
status filter reads them.

| Row kind                                        | Entries                                                   |
|-------------------------------------------------|-----------------------------------------------------------|
| repository issue, open, no branch               | show, start, edit, label, comment, abandon                |
| repository issue, open, branch in progress      | show, start, close (merge), edit, label, comment, abandon |
| repository issue, closed                        | show, edit, label, comment                                |
| not a repository issue, open, no branch         | show, start                                               |
| not a repository issue, open, branch in progress| show, start, close (merge)                                |
| not a repository issue, closed (merged branch)  | show                                                      |

"branch in progress" is `Row.Branch != nil && Row.Branch.Status ==
in_progress`; a merged or closed branch on an open issue counts as no
branch (start offers a new one, close has nothing to merge).

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
  - The footer gains `enter actions`.
- `cmd/issue/list.go`: a `tui.IssuePickComment` case in `listAction`; the
  `list` Long.
- `CLAUDE.md`, "Testing the menus": the issue table paragraph says enter
  opens the row's menu (`issueMenuEntries`) and lists comment among the
  picks.
- Nothing in `cmd/branch`, `issueflow`, `issue` or `git` changes.

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
    row and never opens the menu; the footer contains `enter actions`.
  - `TestIssueTable_ActionKeys`: unchanged, proving the shortcuts stay.
  - `TestIssueTable_EnterOpensDialog`: becomes "show from the menu opens
    the dialog", or is folded into `TestIssueTable_Menu`.
- `cmd/issue/list_test.go` (or where `TestListAction` lives):
  `IssuePickComment` runs the comment form on the row's issue and reopens
  the table; esc on the form reopens quietly.
- `cmd/issue/list_test.go`: the `list` Long names the menu
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
