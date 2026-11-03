---
sidebar_position: 1
title: Issues and branches
---

After this page you know which of `git zf issue` and `git zf branch` to reach
for, and what a branch name tells you.

## Two things

An **issue** is a piece of work with an identity: a ticket in the tracker
(Redmine, GitHub, Forgejo/Gitea), or an issue stored in the repository
itself when there is no tracker. A **branch** is where the work happens in
git.

From the moment an issue is started it has a branch, so the issue drives the
flow: `issue start` names the branch after the issue, `commit` pre-fills the
commit form from it, and `issue close` merges the branch into the base and
offers to update the tracker.

A branch does not need an issue behind it. `branch new` creates one with an
ID of its own and no tracker behind it, to be merged later with
`branch merge`, with no tracker step. `branch merge` takes any branch: one
git-zf created, or a plain git branch that follows no naming convention at
all. Only an issue branch is redirected to `issue close`.

The rule of thumb: **if there is a ticket, go through `issue`; if there is
not, go through `branch`.**

`branch close` does **not** merge: it records a branch as abandoned, without
touching any git branch. The action that merges a branch and updates the
tracker is `issue close`.

## Branch names

A branch is named `{id}@{type}@{slugified-title}`, as in
`ABC-42@feat@add-oauth-login`. The first segment is the **branch's ID**: the
key of its record in the repository and what `commit` puts in the scope. It
is not necessarily the ID of an issue, but it can represent one: a branch
started from a ticket takes the tracker's ID (`ABC-42` on Redmine or Jira,
`123` on GitHub or Forgejo), one started from an issue stored in the
repository takes that issue's 7-character ID (`1a2b3c4`), and `branch new`
takes the ID you type, or mints one when you leave it empty. Where the ID is
an issue's, the commands that look up issues use it: `branch prune` reaps a
branch whose issue is closed, for instance. The type is one of the
configured commit types.

The slug is capped at 50 characters so the ref stays under 100. It keeps
ASCII letters and digits only; a title that leaves nothing, a title in a
non-Latin script for instance, gets the slug `issue`, as in
`1a2b3c4@feat@issue`. The full title stays on the issue and in `issue list`.

A second branch on the same issue, a spike or a parallel approach, carries a
variant label: `ABC-42@feat@add-oauth-login@spike`. See
[start an issue](../workflows/start-an-issue.md).

A reviewer's branch is `<ID>@review`: see
[review a branch](../workflows/review.md).

## Commands used

- [`git zf issue start`](../reference/commands/git_zf_issue_start.md)
- [`git zf issue close`](../reference/commands/git_zf_issue_close.md)
- [`git zf branch new`](../reference/commands/git_zf_branch_new.md)
- [`git zf branch merge`](../reference/commands/git_zf_branch_merge.md)
- [`git zf branch close`](../reference/commands/git_zf_branch_close.md)
