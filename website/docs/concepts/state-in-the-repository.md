---
sidebar_position: 2
title: Where git-zf keeps its state
---

After this page you know that everything git-zf records lives in the
repository, what a fresh clone sees, and why two people acting at once never
get a rejected push.

## No database

git-zf keeps no database of its own. Issues, branches and reviews are
recorded in the repository, under `refs/zf/`, as chains of commits: one
commit per action, each pointing at the previous one. They are pushed to
and fetched from the remote like any ref.

| Ref | Holds |
|---|---|
| `refs/zf/issues/<id>` | an issue stored in the repository: title, description, labels, comments, open or closed |
| `refs/zf/branches/<issue>` | the branches of an issue: one commit when a branch is started, one each time its status changes (in progress, merged, closed), plus the issue's title, parent and tracker |
| `refs/zf/reviews/<issue>` | the review of an issue: request, start, approve, reject, close |

What follows from it:

- **A fresh clone sees everything.** After `git zf init` a plain `git fetch`
  brings the chains, so a teammate sees the backlog, which branches are in
  progress, and which reviews are open, with no account anywhere.
- **Nothing is force-pushed.** Two people commenting on the same issue
  offline, or two reviewers acting on the same review at once, both land:
  their chains are merged when they sync, never overwritten.
- **Records outlive the work.** A review stays in the repository after the
  issue is closed. A branch's history of statuses stays on its chain.

## Issues in the repository

Without a tracker, issues live under `refs/zf/issues/` and travel with
`git zf issue sync`. A teammate with a fresh clone sees the same backlog,
descriptions and comments.

```
$ git zf issue new --title "Login fails on Safari" --type fix --label bug
Created issue 1a2b3c4: Login fails on Safari
$ git zf issue comment 1a2b3c4 -m "Reproduced on 17.4"
$ git zf issue label 1a2b3c4 +ui -bug
$ git zf issue edit 1a2b3c4 --title "Login fails on Safari 17"
$ git zf issue show 1a2b3c4
$ git zf issue start 1a2b3c4    # the branch is 1a2b3c4@fix@login-fails-on-safari
```

Each of these commands pushes its own change; `issue sync` is mostly for
bringing in other people's. Two people can comment on or relabel the same
issue offline: both changes are kept when they sync.

## Branches in the repository

The branches of an issue are recorded under `refs/zf/branches/<issue>`: one
commit when a branch is started, one each time its status changes. The chain
also holds the issue's title, its parent issue and the tracker it came from.
Every clone knows which branches are in progress, including the ones a
teammate started and has not merged yet.

```
$ git zf branch list
```

lists them with their status, yours and the ones other clones started. It
does not contact the remote: it shows what the last `git fetch` brought.
Flags: `--status in_progress|merged|closed|all`, `--stdout`, `--json`.

## Reviews in the repository

A review is stored under `refs/zf/reviews/<issue>` as a chain of commits, one
per action. Two people acting on the same review at once never get a
rejected push: their actions are merged. The review stays in the repository
after the issue is closed.

## Reading the refs yourself

The formats are documented for anyone reading them with plain git:
[issue refs](../under-the-hood/issue-refs.md),
[branch refs](../under-the-hood/branch-refs.md),
[review refs](../under-the-hood/review-refs.md).

```
$ git log refs/zf/issues/<id>
$ git show <commit>:op.json
```

## Commands used

- [`git zf init`](../reference/commands/git_zf_init.md)
- [`git zf issue sync`](../reference/commands/git_zf_issue_sync.md)
- [`git zf branch list`](../reference/commands/git_zf_branch_list.md)
- [`git zf review fetch`](../reference/commands/git_zf_review_fetch.md)
