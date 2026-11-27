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
that is neither an issue nor a branch is an error. The remote is fetched
(the close stops when it cannot be reached), and the close checks that the remote
branch holds nothing your local one lacks: see
[when the remote branch moved](#when-the-remote-branch-moved). Then:

1. **The review.** A branch whose review is `in_review` or
   `changes_requested` is refused. An approved one is merged only when its
   tip is the approved commit: see
   [the review at close](#the-review-at-close). A parent issue with open
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

### When the remote branch moved

A close merges your local branch. When `<remote>/<branch>` holds commits it
lacks, merging it would leave them out, or merge an old copy of the work. So
the close compares the two after its fetch:

| Your branch against the remote one | The close |
| --- | --- |
| same commit, or yours is ahead | goes on |
| yours is behind | offers to fast-forward it, then stops |
| each holds commits the other lacks, no conflict | offers to merge the remote branch into yours, then stops |
| each holds commits the other lacks, and they conflict | stops, and names both tips and the conflicting files |

The close stops whatever you answer: what came in has to be looked at, and
reviewed when the issue has a review, before a close merges it.

- **Yes**: the fast-forward or the merge runs in the working tree that holds
  the branch, which must have no uncommitted change. The close then prints
  `git log <old tip>..<branch>` and what to do next: run the close again, or,
  when the branch is now past an approved commit, `git zf review request` for
  a new round.
- **No**, or no terminal to ask on: nothing is changed, and the command that
  takes the commits is printed.

For a diverged branch the default answer is no.

A remote branch that was rewritten (rebased, then pushed with force) is not
merged: a merge would bring back what the rewrite dropped. The close stops
without a question and prints the command that follows the rewrite:

- your branch is an old copy, with nothing the remote branch did not have
  before: `git log <remote>/<branch>..<branch>` to see what the rewrite
  dropped (commits you pushed earlier are among them), then
  `git checkout -B <branch> <remote>/<branch>`, and the commit your branch
  was at, to bring it back;
- your branch holds commits of its own on top of the old history:
  `git rebase --onto <remote>/<branch> <old remote tip> <branch>`.

The rewrite is read from the reflog of the remote-tracking branch, so the
close sees it only when this clone fetched or pushed the branch before the
rewrite, within the reflog's expiry (90 days by default). Otherwise the
branch is a diverged one like any other: answer no, and reset your branch
to the remote one by hand.

A stopped close exits with an error: the issue was not closed.

When the remote cannot be reached the close stops, whatever the strategy: the
remote branch may hold commits this clone never fetched. Run it again once
the remote answers.

### The review at close

The review of an issue covers one branch, the one submitted with
`git zf review request`. Closing another branch of the same issue is not
held by it, and leaves the review open.

For the reviewed branch, nothing is merged while the review is `in_review`
(wait for the decision, `git zf review list` shows it) or
`changes_requested` (fix, push, `git zf review request` for the next round).
Once approved, the close compares the tip of your local branch with the
approved commit and changes nothing when it refuses:

| Your branch | The close | Way out |
| --- | --- | --- |
| is on the approved commit | merges, then marks the review closed | |
| is behind the approved commit | refuses | `git merge --ff-only <commit>`, the message gives the commit |
| holds commits made after the approval | refuses | `git zf review request` for a new round |
| is another line of history (an old copy, a rebase) | refuses and names the approved commit | bring the branch to that commit, or request a new round |
| the approved commit is not in your clone | refuses | `git fetch`, then close again |

When two reviewers approved, the approved commit is the one that contains
the other. Two approvals of commits that diverged refuse the close:
`git zf review request` for a new round.

With `[review] require-signed = true`, an approval of the approved commit
must carry a signature git trusts. Otherwise the close refuses and lists
each approval with its signature state; the reviewer signs a new round.

The picker also lists branches started in another clone, known from the
fetched `refs/zf/branches/*` chains, so a teammate can close an issue they
did not start: the branch is materialized from `origin/<branch>`.

Closing works from inside a linked worktree. Rebase runs its steps in the
worktree holding the branch and fast-forwards the base from the main
checkout; Squash and Classic run in the main checkout. Git refuses the close
when the *base* branch is checked out in another linked worktree.

A branch that has an open pull request on the forge is merged through the
forge instead: see [pull requests](pull-requests.md).

While a step waits on the remote, the forge or the tracker, a spinner names
it (`Fetching the branches and the issues…`, `Recording the merge of
<branch>…`). Warnings and git's output are printed above it; git shows no
progress counters meanwhile. Nothing is drawn when stderr is not a terminal.
`git zf branch merge` and the push proposal do the same.

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
- [`git zf review request`](../reference/commands/git_zf_review_request.md)
- [`git zf branch close`](../reference/commands/git_zf_branch_close.md)
- [`git zf branch merge`](../reference/commands/git_zf_branch_merge.md)
