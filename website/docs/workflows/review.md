---
sidebar_position: 3
title: Review a branch
---

After this page you can run a review round from either side, and know what
`git zf issue close` accepts once a review is approved.

Reviews are peer-to-peer, with no server-side component. The state of a
review lives in the repository under `refs/zf/reviews/<ID>`, one
commit per action, pushed to and fetched from the remote. The refs are the
only record, and actions taken on two machines at once are merged, never
overwritten.

There is one branch: the reviewer works on the feature branch itself. Nothing
is locked. What protects the base branch is the rule at close: a reviewed
branch is merged only when its tip is exactly the commit that was approved.

```
Developer                              Reviewer
---------                              --------
git zf review request
                                       git zf review start
                                       (review; commit fixes on the
                                        feature branch; push them)
                                       git zf review approve
                                         or git zf review reject
git zf issue close        (approved)
  or fix, push, git zf review request   (next round)
```

## A round

1. **Developer: `git zf review request`.** Pick an in-progress branch (the
   current one is pre-selected). The review ref becomes `in_review` and
   records the branch and the commit submitted. Push the branch: the reviewer
   needs that commit.
2. **Reviewer: `git zf review start`.** Pick an issue awaiting review. The
   command fetches, checks out the feature branch and always brings your
   local copy to the remote one:
   - no local copy: it is created from the remote branch;
   - a copy that is behind: it is fast-forwarded;
   - an old copy that does not hold the commit submitted (the developer
     rebased since): it is reset to the remote branch. The message gives the
     commit it was on, which `git branch <name> <sha>` brings back;
   - a copy holding your own commits of this round, not pushed yet: it is
     left alone.

   Uncommitted changes stop a fast-forward or a reset before anything is
   changed. Your identity is recorded on the ref. Review the code on the
   branch; you may commit fixes there and push them.
3. **Reviewer decides.**
   - `git zf review approve`: approves the commit your local branch is on,
     and the status becomes `approved`. It is refused when the remote branch
     holds commits you have not taken: pull them, look at them, approve.
   - `git zf review reject`: the status becomes `changes_requested`. The
     reason is asked (`-m`, or `-F` for a file). When you committed on the
     branch, the command proposes to push it and prints the `git log` range
     that shows your commits.
4. **Next round.** The developer takes the reviewer's commits
   (`git pull --ff-only`), pushes fixes and runs `review request` again: the
   round counter goes up. A new round can also be requested on an approved
   review; it is the way out when commits were made after the approval.

`request`, `approve` and `reject` accept `--push` / `--no-push`, and propose
a tracker status update when the issue came from a tracker.

## At close

`git zf issue close` first fetches and compares your branch with the remote
one. When the remote holds commits yours lacks, it offers to take them
(a fast-forward, or a merge when both sides moved and nothing conflicts) and
then stops, whatever you answer: what came in has to be looked at before it
is merged. Run the close again afterwards.

For an approved review the tip of the branch must then be exactly the commit
approved:

| The branch | The close |
| --- | --- |
| is on the approved commit | merges |
| is behind the approved commit | refuses: `git merge --ff-only <commit>` |
| holds commits made after the approval | refuses: `git zf review request` for a new round |
| is another line of history (an old copy, a rebase) | refuses and names the approved commit |
| the approved commit is not in your clone | refuses: `git fetch` first |

A refused close changes nothing. A review that is `in_review` or
`changes_requested` refuses the close. Another branch of the same issue is
not held by the review.
[Close an issue](close-an-issue.md) has the whole sequence.

## Sub-tasks

```
$ git zf review sync
```

Merges the parent branch (`<remote>/<parent>`) into a
[sub-task](sub-tasks.md) branch that fell behind it. A conflict is aborted
and reported.

## Seeing the reviews

- `git zf review list` reads `refs/zf/reviews/*` (it works on a fresh clone)
  and prints every open review, `in_review` or `approved`, with its round
  number. A closed review stays in the repository but is not listed.
- `git zf review status` shows the round-by-round history of an issue:
  status, reviewer, timestamps, whether the reviewer pushed commits. It is
  the same on every clone.
- `git zf review fetch` fetches the review chains, merges them with the
  local ones, and pushes the ones the remote lacks. Nothing is pruned
  locally. The interactive commands sync on their own; use this before
  scripting around review state.
- `git zf review track` registers the current branch without creating
  anything, for a branch made with plain `git checkout`: it is recorded as
  in progress on its branch chain.

## Signed approvals

To require signed approvals, set `require-signed = true` under `[review]` in
`.git-zf.toml`. `review approve` then signs, and `issue close` refuses to
merge without an approval that `git verify-commit` accepts and that covers
the commit being merged. When the close is refused, `git zf review request`
starts a new round on the approved branch. The
[review refs](../under-the-hood/review-refs.md) page says what git-zf checks
and what it does not.

## Upgrading from the `@review` branch

Earlier versions gave the reviewer a second branch, `<ID>@review`, and locked
the feature branch with git hooks. Every clone must upgrade: an older git-zf
closing a review made with this one merges its local branch as it is.

- Run `git zf init` again in every clone: it deletes the three hooks the
  earlier versions installed. Left in place they do no harm, they call
  commands that do nothing now.

- A review that is open and approved on a `<ID>@review` branch: take its
  commits with `git merge --ff-only <remote>/<ID>@review` on the feature
  branch, then close. When that is not a fast-forward, merge by hand and
  request a new round.
- A leftover `<ID>@review` branch is an ordinary branch: delete it when its
  commits are merged.

## Commands used

- [`git zf review request`](../reference/commands/git_zf_review_request.md)
- [`git zf review start`](../reference/commands/git_zf_review_start.md)
- [`git zf review approve`](../reference/commands/git_zf_review_approve.md)
- [`git zf review reject`](../reference/commands/git_zf_review_reject.md)
- [`git zf review sync`](../reference/commands/git_zf_review_sync.md)
- [`git zf review list`](../reference/commands/git_zf_review_list.md)
- [`git zf review status`](../reference/commands/git_zf_review_status.md)
- [`git zf review fetch`](../reference/commands/git_zf_review_fetch.md)
- [`git zf review track`](../reference/commands/git_zf_review_track.md)
