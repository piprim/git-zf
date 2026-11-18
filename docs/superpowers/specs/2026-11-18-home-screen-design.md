# Home screen for bare `git zf` — design

Date: 2026-11-18.

## Intent

Bare `git zf` shows a flat list of five commands. It says nothing about the
repository, and once a command is picked there is no way back: leaving the
issue table, the log or a form ends the program. The first screen should
answer "where am I and what is next?" and be a place one returns to.

## Goals

1. `git zf` opens a home screen: a status header read from the repository,
   then the workflow commands with a letter key and a live hint each.
2. Leaving a command started from the home screen returns to it, refreshed.
3. `s` shows the working tree's status in a readable, scrollable view.
4. `r` refreshes the home screen from the remote.

## Non-goals

- Panes, a dashboard, anything that duplicates the issue or branch table.
- Asking the tracker. The home screen never calls it, mirror or not.
- Staging, unstaging or any action from the status view: it is read-only.
- A configurable set of entries or keys.

## Anti-goals

- A home screen that is slow to appear. Each display is local git work only;
  the network is touched by `r` alone.
- Changing what `git zf <command>` does when called directly, beyond the Esc
  rule below.
- Changing what a script sees: without a terminal on stdin the help is printed
  as today.

## Approaches

- *A. Keep the list, loop back to it.* Fixes the way back, nothing else.
- *B. Status header above the actions, letter keys, loop* — picked by the
  user: the screen becomes useful before any key is pressed.
- *C. Full home screen with panes.* Dropped: it rebuilds the three tables that
  already exist.

## What the user decided

- Approach B.
- The header is itself an action: `s` opens a "fancy git status".
- `r` refreshes.

Everything below is the designer's call unless marked otherwise.

## The screen

```
git-zf  42@feat@login-form  ·  #42 Login form rejects unicode
        3 staged, 2 modified, 1 untracked  ·  ↑2 ↓0 origin  ·  review: changes requested

> c  Commit    3 staged files
  i  Issue     12 open
  b  Branch    4 in progress
  v  Review    1 in review
  l  Log

↑/↓ move · enter run · s status · r refresh · q quit
```

### Header

Line 1: the program name, the current branch (`detached at <short hash>` on a
detached HEAD), and, when the branch is tracked, its issue: the display ID
and title of the issue's record when the repository holds one (it follows
`issue edit`), else the slug and title the branch chain froze at start (a
tracker issue without the mirror, an ID typed in `branch new`).

A reviewer's branch (`<slug>@review`, made by `review start`) is not a
tracked branch and its name does not parse as one. `branch.CutReviewSuffix`
gives its slug, as the commit command does, and the header then shows the
issue and the review of that slug exactly as it does on the feature branch:
this is the screen a reviewer sees most.

Line 2, each part left out when it has nothing to say:

- the working tree: counts of staged, modified, untracked and unmerged files,
  each left out at zero; `clean` when all are. The counts are the sizes of the
  groups the status view shows (the grouping behind `tui.StatusPanel` with
  both flags false), so the header and `s` never disagree: a file staged and
  modified again counts in both, a tree holding only conflicts is not clean;
- the upstream: commits ahead and behind, and the remote's name; left out
  when the branch has no upstream;
- the review of the current branch's issue, when its review chain has a
  status and is not closed: `in review`, `approved`, `changes requested`.

Outside a git repository there is no home screen: every command it offers
fails there, and with the way back each failure would only return to it.
`git zf` prints one line on stderr, `not a git repository`, then the help (the
commands that work anywhere, such as `version` and `install`, are listed
there), and exits 0 as it does without a terminal. The terminal is checked
first: without one the help alone is printed, in a repository or not, and
nothing goes to stderr, so a script and the root command's tests that run
outside a repository see what they see today. Today the menu opens and
whatever is picked ends on git's raw error.

### Actions and hints

The entries are those of `rootMenu`, in its order. Keys: `c` commit, `i`
issue, `b` branch, `v` review, `l` log. Review takes `v` because the user gave
`r` to refresh, which also matches `r` in the issue table. A letter runs its
command at once; enter runs the one under the cursor.

Hints, each left out at zero:

- Commit: `N staged files`, else `N modified files`.
- Issue: `N open`, from the issues stored in the repository (`issue.List`).
- Branch: `N in progress` (`branch.Rows` over `branch.List`).
- Review: `N in review`: the reviews of `review.List` whose status is in
  review and that are not closed (a close keeps the last status).
- Log: none.

The filter (`/`) of today's menu goes away: five entries with a key each.

### Cursor

On first display: Commit when the tree has staged, modified or unmerged
files, Issue otherwise. After a command returns, the cursor stays on that command.

### Status view (`s`)

A mode of the home screen, not a separate command. It shows the branch and
upstream line, then the files grouped and coloured as the commit form's panel
does, in a view that scrolls when the list is taller than the terminal. It is
`git status`, not a commit preview: staged, unstaged and untracked files each
in their own section (`tui.StatusPanel(entries, false, false)`; `true` for
the last argument would fold the untracked files into the staged ones). A
clean tree says so: the panel renders nothing for it. Esc or `q`
returns to the menu. It shows what was read for the current display: it does
not read the tree again on its own.

### Refresh (`r`)

Fetches the remote (the branches, and the issue, branch and review chains)
under the spinner, then reads everything again. No tracker reconcile, no push.
A fetch that fails (offline) is one warning line and the local data is shown:
the fetches run quietly, git's own error block is not printed. `issue.Fetch`
and `branch.Fetch` are not quiet today (`review.Fetch` takes a flag), so they
need a quiet path. Each refresh uses a git client of its own: a client that
saw the remote down fails every later remote call at once
(`git.Client.remoteDown`), which is wanted within one refresh and must not
outlive it, or `r` would never retry once the network is back.
Returning from a command already re-reads the local data, so a local-only `r`
would have little use: that is why it fetches.

## The way back

After the picked command returns, the home screen is shown again with fresh
data, whatever the command did: finished, failed or left with Esc. An error is
printed as `error: …` above the redrawn screen, the way the issue list does
after an action. `huh.ErrUserAborted` is not an error.

So that one key means "leave this view" everywhere:

- In the issue table, the branch table and the log, Esc at the top level
  (no dialog, menu, filter or pending question to dismiss) closes the view,
  as `q` does. This also applies when the command is called directly.
  A text filter (`/`) that is applied counts as something to dismiss: the
  first Esc clears it, the next one closes the view. The toggles (status tab,
  project, `m`, the branch table's S/T/R) do not absorb Esc. An armed key
  (`a` in the issue table, `a` or `D` in the branch table, waiting for its
  second press) counts too: Esc disarms it and the view stays. Both tables
  clear the armed key before they look at the key pressed, so the Esc rule
  must be decided before that.
- On the home screen's menu, Esc does nothing (decided by the user): pressing
  it once too often on the way back must not quit. `q` quits, and ctrl+c as
  everywhere. In the status view Esc still returns to the menu.

There is no "quit everything" key from inside a view: `q` then `q`. The views
do not have to report which key closed them, which keeps them unchanged apart
from the Esc rule.

`git zf review` keeps its list menu (`cmdutil.RunMenu`) and gets the same
loop: after an action the review menu is shown again, Esc leaves it. Reached
from the home screen, leaving it returns to the home screen. `RunMenu`'s loop
treats what an action returns as the home screen's does: an abort is ignored,
any other error is printed as `error: …` on the command's stderr and the menu
is shown again. It ends when the menu itself is left (nil) or its prompter
fails (the error is returned).

## Reading the data

One read per display, under `IO().Spin`, stopped before the screen is drawn.
It is local and costs as few git processes as it can:

- one `git status --porcelain=v2 --branch` gives the files, the current
  branch (or the detached commit), the upstream and the ahead and behind
  counts; today's parser drops those `#` lines;
- one listing each of the issue, branch and review chains: `issue.List`,
  `branch.List`, `review.List`, which all return items, warnings and an
  error. Not `branch.ListRows`: it reconciles and writes its own warnings.

The current branch's chain, issue record and review are looked up in those
listings, never loaded one by one (no `branch.Find`, no `Load`). A listing
that fails is a warning and what depends on it is left out; the screen still
opens.

## Shape of the code

- A pure bubbletea model in `tui/` that takes the data to show and the
  entries, and ends with a pick: a command name, refresh, or quit. The status
  view is a mode of that model.
- The gathering of the data and the loop live with the root command. The loop
  talks to the model through a small prompter interface so tests script it,
  as `MenuPrompter` does today.
- The picked command runs as `RunMenu` runs it today: its `RunE` with the
  parent command.

## Tests

Every scenario in a named `t.Run`.

- The model: letter keys, enter, the first cursor, `s` and back, `r`, `q`
  and ctrl+c quit, Esc on the menu does nothing, the rendering of the header parts that are left out.
- The data: on a real on-disk repo — clean and dirty tree, a file staged and
  modified again, an unmerged file, tracked branch with an issue record and
  without one, a `<slug>@review` branch, upstream ahead and behind, detached HEAD, a review status, a
  closed review.
- The refresh: a remote that is down gives one warning, and the next refresh
  reaches a remote that is back.
  Seed with `branchtest.Seed` and `reviewtest.Seed`.
- The loop: a scripted prompter picks a command, the command returns (nil, an
  error, an abort) and the screen is asked again; quit ends it; no terminal
  prints the help; outside a repository the line and the help are printed and
  the prompter is never asked.
- Esc at the top level of the issue table, the branch table and the log; Esc
  on an applied filter and on an armed `a` / `D`.
- `RunMenu`'s loop for `git zf review`.
- Update `TestGetRootCmd_menu`, the `RunMenu` tests and the CLAUDE.md section
  "Testing the menus".

## Left to the builder

- Names of the types and files, and how `issue.Fetch` and `branch.Fetch` get
  their quiet path.
- The exact wording, colours and spacing of the header and hints.
- Whether the status view reuses the log's dialog component or a viewport of
  its own.
- How the tables detect "nothing to dismiss" for the Esc rule.
