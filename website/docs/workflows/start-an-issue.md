---
sidebar_position: 1
title: Start an issue
---

After this page you can start work on an issue, from its ID or from the
issue table, on a branch or in a worktree, and know what gets recorded.

## With an ID

```
$ git zf issue start ABC-42
```

`<id>` is one of:

- the full ID of an issue stored in the repository, or a unique prefix of at
  least 4 characters;
- the number of the tracker issue (Redmine, GitHub, Forgejo/Gitea).

The repository issues are fetched first. A number the repository does not
know is reconciled with the [mirror](../concepts/trackers-and-the-mirror.md)
when it is on, so an issue a teammate just filed in the tracker is found,
and asked to the tracker when it is off; a known ID costs no tracker round
trip. An ID nothing knows, for a tracker git-zf does not talk to, opens the
manual form with it filled in: the title and type are asked and the ID is
used as is, with no issue in the repository.

## From the table

```
$ git zf issue start
$ git zf issue            # the same
```

Without an ID the issue table opens, with no sync. The tracker is the
primary source when configured; otherwise the rows are the issues stored in
the repository plus the tracked branches.

Columns: Issue ID, [Project], Title, Branch, Local Status, Issue Status,
Created. Labels follow the title in brackets. Created is the issue's creation
date, or its branch's for a row with no issue behind it. `∅` means no
branch started yet; `N.A.` means the row has neither a tracker nor a
repository issue.

| Key | Does |
|---|---|
| `enter` | shows the issue and its comments |
| `e` | edits its title and description in place |
| `s` | starts the issue under the cursor |
| `n` | files a new issue |
| `c` | merges the branch of the issue under the cursor, as `issue close <id>` does ([close an issue](close-an-issue.md)) |
| `a` `a` | abandons the issue under the cursor, as `issue abandon <id>` does |
| `l` | edits its labels |
| `r` | syncs the issues |
| `/` | filters the rows, any column, case-insensitive |
| `tab` | cycles the status filter: Open, Closed, All |
| `p` | opens the project picker |
| `m` | keeps the issues assigned to you |
| `q` | quits |

Each action runs once the table has exited and reopens it afterwards; a
start leaves you on the new branch.

:::warning
`a` closes an issue without merging anything. The first press asks for the
second; there is no undo.
:::

Flags of `issue list`: `--status open|closed|all`, `--stdout` (a plain table),
`--json`. `issue start` itself takes none of them.

## What happens

1. A branch named `{issue-id}@{type}@{slug}` is created and checked out
   (see [branch names](../concepts/issues-and-branches.md#branch-names)),
   **or a git worktree is created** so the main working tree stays untouched.
   A prompt asks which; pin the choice with `branch.use-worktree` in the
   config. When a worktree is created the command prints its path and a
   `cd` hint, since the shell cannot change directory for you. See
   [worktrees](worktrees.md).
2. With a tracker configured, you can move the issue to "In Progress" in the
   same step.
3. The branch is recorded in the repository under
   `refs/zf/branches/<issue-id>` and that record is pushed: every clone sees
   that the issue is in progress.

When more than one candidate base branch exists, a picker asks. Picking
another issue's branch makes this one a [sub-task](sub-tasks.md) of it;
`--parent=<parent-issue-slug>` does the same without the picker.

## A second branch on the same issue

The default name is unique per issue. For a spike or a parallel approach,
pass `--variant=<label>`:

```
$ git zf issue start ABC-42 --variant=spike     # → ABC-42@feat@add-oauth-login@spike
```

The label is lowercased and slugged (letters, digits, hyphens) and must not
be empty afterwards. When the default name collides with an existing
branch, a prompt offers to **check it out**, **create a variant**, or
**abort**.

## A branch without a ticket

```
$ git zf branch new
```

The same flow with manual input: it opens the form at once, whatever the
repository holds. A typed ID is used as is, an issue living where git-zf
does not look; an empty one files an issue in the repository. It never
asks the tracker. `--variant` works here too. See
[issues and branches](../concepts/issues-and-branches.md) for when to use
which.

## Commands used

- [`git zf issue start`](../reference/commands/git_zf_issue_start.md)
- [`git zf issue list`](../reference/commands/git_zf_issue_list.md)
- [`git zf issue new`](../reference/commands/git_zf_issue_new.md)
- [`git zf branch new`](../reference/commands/git_zf_branch_new.md)
- [`git zf issue track`](../reference/commands/git_zf_issue_track.md), for a
  branch made with plain `git checkout`
