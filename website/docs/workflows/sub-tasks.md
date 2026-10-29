---
sidebar_position: 5
title: Sub-tasks
---

After this page you can split an issue into sub-tasks that branch from and
close into the issue's own branch.

An issue branch can serve as the integration branch for sub-tasks. Start
one with:

```
$ git zf issue start ABC-43 --parent=ABC-42
```

or simply pick the parent's branch in the base-branch picker that
`issue start` shows when more than one candidate branch exists.

A sub-task then:

- branches from and closes into the parent branch instead of the configured
  base;
- gets parent drift merged in by `git zf review sync`: after the reviewer
  commits, it merges `<remote>/<parent>` into the sub-task, and aborts and
  reports a conflict there;
- must be closed before its parent can be closed: `issue close` refuses a
  parent with open sub-tasks.

The parent relation is recorded on the branch's chain under
`refs/zf/branches/<slug>`, so every clone has it.

## Commands used

- [`git zf issue start`](../reference/commands/git_zf_issue_start.md)
- [`git zf review sync`](../reference/commands/git_zf_review_sync.md)
- [`git zf issue close`](../reference/commands/git_zf_issue_close.md)
