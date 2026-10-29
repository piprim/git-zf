---
sidebar_position: 6
title: Worktrees
---

After this page you know how to start an issue in a linked worktree, where
the worktree goes, and what happens to it at close.

A [git worktree](https://git-scm.com/docs/git-worktree) is a second checkout
of the same repository in another directory. Starting an issue in one keeps
the main working tree untouched, so you can switch between issues without
stashing.

## Starting

`git zf issue start` asks whether to create a branch or a worktree. Pin the
choice in the config:

```toml
[branch]
use-worktree = true          # omit = ask at runtime; true = always worktree; false = always branch
worktree-dir = "~/worktrees" # omit = <repo>/.worktrees/<branch>
```

When a worktree is created the command prints its path and a `cd` hint,
since the shell cannot change directory for you.

The default path is `.worktrees/<branch>` under the repository root, for
instance `~/code/myapp/.worktrees/ABC-42@feat@add-oauth-login`. Add
`.worktrees/` to your `.gitignore`. The directory is also added to the
clone's `.git/info/exclude`, so the main tree never sees a worktree as
untracked. A relative `worktree-dir` is under the repository root. Outside
the repository (`worktree-dir = "~/worktrees"`) a worktree is named
`<repo>--<branch>`; the repository name is taken from the remote URL when
possible.

## Working

Everything works from inside the worktree: `git zf commit`, the review
commands, `git zf issue close`. Run `git zf init` from the main checkout
though, never from a worktree: git reads hooks from the common git dir.

## Closing

`issue close` works from inside a linked worktree. Rebase runs its steps in
the worktree holding the branch and fast-forwards the base from the main
checkout; Squash and Classic run in the main checkout. Git refuses the close
when the *base* branch is checked out in another linked worktree.

After the merge the worktree is removed, never forced: a worktree with
modified or untracked files is left in place, and the branch it holds is
not deleted. A `cd` hint back to the main checkout is printed when you ran
the command from inside the removed worktree.

`branch merge` offers the same removal for a source branch checked out in a
linked worktree; the main checkout is never removed. A branch held by a
stale worktree entry is refused with a `git worktree prune` hint.

## Commands used

- [`git zf issue start`](../reference/commands/git_zf_issue_start.md)
- [`git zf issue close`](../reference/commands/git_zf_issue_close.md)
- [`git zf branch merge`](../reference/commands/git_zf_branch_merge.md)
