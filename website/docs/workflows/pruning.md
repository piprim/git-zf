---
sidebar_position: 8
title: Pruning and abandoning
---

After this page you can bring the branch records back in line with what
happened outside git-zf, abandon a branch, and reap the branches of closed
tracker issues.

## What happened outside git-zf

```
$ git zf branch prune
```

Records what happened to in-progress branches outside git-zf:

- a local branch already merged into the base is marked `merged`;
- a branch gone locally and on the remote is marked `closed`, if you started
  it. One that someone else started is listed and left to them, since it
  may be work they have not pushed yet. When you know such branches are
  abandoned (their author left, or you changed your git name or email),
  `--others` closes them too;
- a branch still on the remote is left alone;
- nothing is closed when the remote cannot be reached.

Flags: `--base <branch>`, `--dry-run`, `--others`, `--yes` (skip the
confirmation, for CI). `git zf issue track` on a closed branch reopens it.

## Abandoning one branch

```
$ git zf branch close <branch-name>
```

Records one tracked branch as closed, and pushes that record. **Nothing is
merged and no git branch is deleted**: this is not `issue close`. It is for
a branch that will not be merged. The branch does not have to exist in your
clone, and it does not matter who started it. Only a branch in progress can
be closed. To undo, check the branch out and run `git zf issue track`.

It is not in the `git zf branch` menu, since it needs a name.

## Branches of closed tracker issues

```
$ git zf branch prune-tracker --dry-run
$ git zf branch prune-tracker --safe-delete --base main   # CI
```

Finds local branches whose issue ID, parsed from the branch name, is closed
in the configured tracker, then offers a per-branch action: safe-delete
(the default), force-delete or skip. Successful reaps record the branch as
`closed`. Branches whose name does not parse are skipped silently; tracker
lookup failures print a `WARN:` line and skip that branch.

For non-interactive use pass exactly one of `--safe-delete`,
`--force-delete` or `--skip-delete` to apply it to every candidate;
`--dry-run` previews with no prompts or mutations.

:::warning
`--force-delete` runs `git branch -D` on every match: commits that are not
merged anywhere are lost with the branch.
:::

## Commands used

- [`git zf branch prune`](../reference/commands/git_zf_branch_prune.md)
- [`git zf branch close`](../reference/commands/git_zf_branch_close.md)
- [`git zf branch prune-tracker`](../reference/commands/git_zf_branch_prune-tracker.md)
- [`git zf issue track`](../reference/commands/git_zf_issue_track.md)
