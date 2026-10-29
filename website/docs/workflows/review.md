---
sidebar_position: 3
title: Review a branch
---

After this page you can run a review round from either side, know what a
lock does, and know how reviewer commits get back into the branch.

Reviews are peer-to-peer, with no server-side component. The state of a
review lives in the repository under `refs/zf/reviews/<IssueID>`, one
commit per action, pushed to and fetched from the remote. The refs are the
only record, and actions taken on two machines at once are merged, never
overwritten.

```
Developer                              Reviewer
---------                              --------
git zf review request
                                       git fetch
                                       git zf review start
                                       (review; commit fixes on
                                        <IssueID>@review; push them)
                                       git zf review approve
                                         or git zf review reject
git zf review sync        (if the reviewer left commits)
git zf issue close        (approved)
  or fix, push, git zf review request   (next round)
```

## A round

1. **Developer: `git zf review request`.** Pick an in-progress branch (the
   current one is pre-selected). Its review ref becomes `in_review` and the
   branch is **locked**: the pre-push hook installed by `git zf init`
   rejects pushes to it until the reviewer decides. Bypass with
   `git push --no-verify`.
2. **Reviewer: `git fetch && git zf review start`.** Pick an issue awaiting
   review. A branch `<IssueID>@review` is created at the commit captured at
   lock time, and your identity is recorded on the ref. Review the code
   there. You may commit fixes on the `@review` branch and push them.
3. **Reviewer decides.**
   - `git zf review approve`: the status becomes `approved`. The developer
     can run `git zf issue close`, which incorporates any reviewer commits
     before merging.
   - `git zf review reject`: the status becomes `changes_requested` and the
     branch is unlocked. The reason is asked (`-m`, or `-F` for a file).
     An empty `@review` branch is deleted, locally and on the remote; one
     carrying reviewer commits is kept for the developer to inspect
     (`git log <feature>..<IssueID>@review`). While such commits await
     incorporation, **new commits on the feature branch are blocked** by
     the commit guard and the pre-commit hook until `git zf review sync`
     merges them in. Bypass with `--no-verify`.
4. **Next round.** The developer pushes fixes and runs `review request`
   again: the round counter increments and the stale `@review` branch is
   removed. If it still carries unincorporated reviewer commits the request
   refuses to delete them and offers to merge them first.

`request`, `approve` and `reject` accept `--push` / `--no-push`, and propose
a tracker status update when the issue came from a tracker.

## Bringing reviewer commits in

```
$ git zf review sync
```

Brings an in-progress branch up to date (the current one is pre-selected).
First it merges pending reviewer commits from `<IssueID>@review`; on
conflict the merge is left in progress for you to resolve, then
`git zf commit` concludes it. Then, for [sub-task](sub-tasks.md) branches
only, it merges the parent branch (`<remote>/<parent>`) into the sub-task; a
conflict there is aborted and reported. A dirty working tree is refused for
the first step: `git stash` first.

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
  anything, for branches made with plain `git checkout`: a feature branch
  is recorded as in progress on its branch chain, a review branch records
  you as the reviewer on the review chain.

## Signed approvals

To require signed approvals, set `require-signed = true` under `[review]` in
`.git-zf.toml`. `review approve` then signs, and `issue close` refuses to
merge without an approval that `git verify-commit` accepts and that covers
the commit being merged. When the close is refused, `git zf review request`
starts a new round on the approved branch. The
[review refs](../under-the-hood/review-refs.md) page says what git-zf checks
and what it does not.

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
