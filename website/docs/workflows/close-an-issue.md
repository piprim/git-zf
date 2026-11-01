---
sidebar_position: 4
title: Close an issue
---

After this page you can merge an issue branch into its base, pick a merge
strategy, and abandon an issue that will not be worked on.

## Merge and close

```
$ git zf issue close
$ git zf issue close ABC-42
```

Pick a branch (the current one is pre-selected). With an ID, the branch of
that issue is picked for you; when the issue has several branches in
progress, the picker offers those alone. An issue with no branch in progress
is told so, with a hint to `issue abandon`: closing always merges. An ID
that is neither an issue nor a branch is an error. Then:

1. **Reviewer commits** left on `<IssueID>@review` by an approved or rejected
   review are incorporated, fast-forward or merge. A conflicting merge
   refuses with a hint to run `git zf review sync`. A parent issue with open
   [sub-tasks](sub-tasks.md) is refused.
2. **Conflict dry-run** with `git merge-tree` against the target. The target
   is the parent branch for a sub-task, otherwise the configured base;
   `--base <branch>` overrides it. Conflicts abort the command before
   anything is touched.
3. **Pick a merge strategy**, Rebase (the default), Squash or Classic, and
   compose the final commit in the commitizen form, pre-filled from the
   issue. See [merge strategies](../reference/merge-strategies.md) for
   which to pick.
4. **Confirm.** The branch is recorded as `merged` on its chain, which is
   pushed. An issue stored in the repository is closed there too, and
   pushed.
5. **Tracker status** picker, if a tracker is configured, or skip.
6. **Worktree removal**, if the branch was started in one. Never forced: a
   worktree with modified or untracked files is left in place. A `cd` hint
   back to the main checkout is printed when you ran the command from
   inside the removed worktree.
7. **Branch deletion**, locally and on the remote, then a push proposal. A
   branch still held by a kept worktree is not deleted.

:::warning
Classic deletes the branch with `git branch -d`. Squash and Rebase need
`-D`, since neither preserves ancestry: once confirmed, the feature branch
is gone, and only the merged commit remains.
:::

The picker also lists branches started in another clone, known from the
fetched `refs/zf/branches/*` chains, so a teammate can close an issue they
did not start: the branch is materialized from `origin/<branch>`.

Closing works from inside a linked worktree. Rebase runs its steps in the
worktree holding the branch and fast-forwards the base from the main
checkout; Squash and Classic run in the main checkout. Git refuses the close
when the *base* branch is checked out in another linked worktree.

A branch that has an open pull request on the forge is merged through the
forge instead: see [pull requests](pull-requests.md).

## Abandon an issue

```
$ git zf issue abandon 1a2b3c4
```

Close an issue stored in the repository without merging anything, for a
duplicate or an issue that will not be worked on. Without an ID a picker
lists the issues. The close is pushed and, with the issue mirror on, the
tracker issue is closed too. It is refused while a branch of the issue is in
progress: merge it with `git zf issue close <id>`, or abandon the branch with
`git zf branch close <branch-name>` (which records the branch as abandoned
and merges nothing, see [pruning and abandoning](pruning.md)), then abandon
the issue.

The `a` key of the issue table, pressed twice, does the same for the row
under the cursor; `c` merges.

## A branch without an issue

```
$ git zf branch merge
```

Pick a local or remote-only branch and merge it into the current branch
with one of the same strategies, then offer to delete the source, locally
and on the remote, and propose a push. Issue branches are refused: use
`git zf issue close` for those, so the review, tracker and record steps
still run. A source branch checked out in another working tree is merged in
place; when that tree is a linked worktree you are offered to remove it
after the commit lands (the main checkout is never removed). A branch held
by a stale worktree entry is refused with a `git worktree prune` hint.

## Commands used

- [`git zf issue close`](../reference/commands/git_zf_issue_close.md)
- [`git zf issue abandon`](../reference/commands/git_zf_issue_abandon.md)
- [`git zf review sync`](../reference/commands/git_zf_review_sync.md)
- [`git zf branch close`](../reference/commands/git_zf_branch_close.md)
- [`git zf branch merge`](../reference/commands/git_zf_branch_merge.md)
