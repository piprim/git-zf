---
sidebar_position: 7
title: Pull requests
---

After this page you can open a pull request from git-zf on GitHub or
Forgejo, including from a fork, and know how a close goes through the forge.

On GitHub and Forgejo the tracker is also the forge. git-zf finds, opens and
merges pull requests there.

## Opening one

```
$ git zf branch pr [branch]
```

Opens a pull request for the branch (the current one by default) on the
tracker's forge. The form is prefilled with the issue's title, the parent
branch as the base, and `Closes #N` for a mirrored issue. The branch is
pushed first when it is not on the remote. A branch that already has an
open pull request gets its URL printed.

`git zf commit` offers the same after pushing a feature branch, as one
yes/no question.

## Closing through the forge

`git zf issue close` and `git zf branch merge` merge a branch that has an
open pull request on the forge through the forge: the strategy picker and
the confirmation are the same as a local close, then the forge merges and
the local base is fast-forwarded. The branch must be at the remote's tip.

A local base that holds commits the remote lacks is left as it is: the merge
is done on the forge, a warning says the base was not fast-forwarded and a
hint names the commands that list those commits and rebase them.

## From a fork

Configure the upstream repository as the tracker's project and push to your
fork as usual. The pull request is opened on the upstream with `you:branch`
as its head, the owner being read from the remote's URL. Closing it through
the forge fetches the base from the upstream, fast-forwards it locally and
pushes it to your fork.

## Commands used

- [`git zf branch pr`](../reference/commands/git_zf_branch_pr.md)
- [`git zf commit`](../reference/commands/git_zf_commit.md)
- [`git zf issue close`](../reference/commands/git_zf_issue_close.md)
- [`git zf branch merge`](../reference/commands/git_zf_branch_merge.md)
