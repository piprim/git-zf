---
sidebar_position: 2
title: Commit
---

After this page you know what the commit form pre-fills on an issue branch,
what the review guard does, and what the push proposal offers.

```
$ git zf commit
```

Opens a commitizen-style form: type, scope, subject, body, footer. Then an
options page: stage all, amend, sign-off, hooks, push. Every option has a
flag; if any commit flag is passed the options page is skipped and the flags
are used directly.

| Flag | Does |
|---|---|
| `-a`, `--all` | stage all tracked modified and deleted files first |
| `-u`, `--include-untracked` | stage untracked files too |
| `--amend` | replace the tip of the current branch |
| `-s`, `--signoff` | add a `Signed-off-by` trailer |
| `-n`, `--no-verify` | bypass the pre-commit and commit-msg hooks, and the review guard |
| `--push` / `--no-push` | push afterwards, or not, without asking |
| `-y`, `--yes` | skip the options page and assume defaults |

The fields of the form and the message template are configured under
`[commit-message]`; the types under `[[commit-types]]`. See
[configuration](../reference/configuration.md#commit-message).

## Pre-fill on an issue branch

On an issue branch such as `ABC-42@feat@add-oauth`, the form is pre-filled
from the branch name. The pre-fill is a hint; every field stays editable.

| Field | Filled with |
|---|---|
| `type` | the branch type |
| `scope` | the issue ID, when the field exists |
| `footer` | `ref-format` applied to the issue ID (`Refs #ABC-42`), when the field exists; `issue close` uses `close-format` instead |
| `subject` | `(ABC-42)`, only when neither `scope` nor `footer` exist |

## The review guard

On a feature branch whose review decision left reviewer commits on
`<ID>@review` that are not yet in your branch, `git zf commit` offers
to merge them in before opening the form. Declining aborts with a hint to
run `git zf review sync`. `--no-verify` skips the guard, as it does the
pre-commit hook. See [review a branch](review.md).

## After the commit

A push is proposed. `--push` and `--no-push` answer it from the command
line, and `propose = false` under `[push]` turns the proposal off
everywhere.

After a push of a feature branch, when the tracker is GitHub or Forgejo,
one more question offers to open a pull request. See
[pull requests](pull-requests.md).

## Commands used

- [`git zf commit`](../reference/commands/git_zf_commit.md)
- [`git zf review sync`](../reference/commands/git_zf_review_sync.md)
