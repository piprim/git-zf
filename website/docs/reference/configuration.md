---
sidebar_position: 2
title: Configuration
---

The config file is TOML. Two locations are supported; the repo-level file
takes precedence:

| Location | Path | Notes |
|---|---|---|
| Home | `$HOME/.git-zf.toml` | Applied everywhere |
| Repo | `<repo>/.git/.git-zf.toml` | Inside `.git/`, never committed, can hold a token |

`git zf config init` creates it, `git zf config show` prints the effective
result. Every key is optional; the defaults are in the binary's
`config/default.toml`, reproduced section by section below.

## `[[commit-types]]`

```toml
[[commit-types]]
name = "feat"
desc = "A new feature"

[[commit-types]]
name = "fix"
desc = "A bug fix"
```

The types offered by the commit form and used in branch names. A
`[[commit-types]]` array in a config file replaces the whole built-in list.
The built-in list is `feat`, `fix`, `docs`, `style`, `refactor`, `perf`,
`test`, `chore`, `revert`, `WIP`.

## `[commit-message]`

```toml
[commit-message]
template     = "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
ref-format   = "Refs #%s"    # footer on regular commits on an issue branch
close-format = "Closes #%s"  # footer on the commit produced by `issue close`

[[commit-message.items]]
name     = "scope"
desc     = "Scope (users, db, poll…):"
form     = "input"

[[commit-message.items]]
name     = "subject"
desc     = "Concise description. Imperative, lower case, no final dot:"
form     = "input"
required = true

[[commit-message.items]]
name = "body"
desc = "Motivation for the change:"
form = "multiline"

[[commit-message.items]]
name = "footer"
desc = "Breaking changes and referenced issues:"
form = "multiline"
```

| Key | Description |
|---|---|
| `template` | A Go `text/template` over the item names, plus `type`. |
| `ref-format` | A `fmt.Sprintf` pattern where `%s` is the issue ID, put in the footer of a regular commit on an issue branch. |
| `close-format` | The same for the commit produced by `issue close`. |
| `items` | The fields of the form after `type`, in order. `form` is `input` or `multiline`; `required` refuses an empty value. An `items` array in a config file replaces the whole built-in list. |

The defaults suit Redmine, GitHub, Forgejo, Gitea and GitLab; for Jira use
`"Refs: %s"` and `"Fixes: %s"`. The issue ID is `ABC-42` for Redmine or Jira,
`123` for GitHub or Forgejo.

## `[branch]`

```toml
[branch]
base         = "develop"     # default: auto-detected from <remote>/HEAD, then "main", then "master"
remote       = "upstream"    # default: auto-detected (see below)
use-worktree = true          # omit = ask at runtime; true = always worktree; false = always branch
worktree-dir = "~/worktrees" # omit = <repo>/.worktrees/<branch>; relative = under the repo root; outside the repo, named <repo>--<branch>
```

| Key | Description |
|---|---|
| `base` | The branch issues are merged into. Auto-detected from `<remote>/HEAD`, then `main`, then `master`. |
| `remote` | The remote git-zf fetches from and pushes to. Auto-detected as below. |
| `use-worktree` | Omitted: `issue start` asks. `true`: always a worktree. `false`: always a branch. |
| `worktree-dir` | Where worktrees go. See [worktrees](../workflows/worktrees.md). |

Remote auto-detection, when `remote` is not set:

| Repo state | Remote used |
|---|---|
| No remotes | Local-only mode: fetch is skipped, merges target the local base |
| Exactly one remote | That remote, whatever its name |
| Several remotes, one named `origin` | `origin` |
| Several remotes, none named `origin` | **Error**: set `branch.remote` |

Branches are named `{id}@{type}@{slugified-title}`, with an optional
`@{variant}`; see [branch names](../concepts/issues-and-branches.md#branch-names).

## `[issue-tracker]`

```toml
[issue-tracker]
type     = "forgejo"                # "redmine", "github", "forgejo" or "gitea"
url      = "https://codeberg.org"
token    = "your_access_token"
mirror   = false                    # true: mirror this project's issues with the repository

[[issue-tracker.projects]]          # optional; one table per project
name = "myproject"                  # stable local name (lowercased), stored in the repository
id   = "owner/repo"                 # what the tracker calls it
```

| Key | Description |
|---|---|
| `type` | `"redmine"`, `"github"`, `"forgejo"` or `"gitea"` (the last two share one adapter). Empty: no tracker. |
| `url` | Redmine: instance URL. GitHub: `https://api.github.com`, or `https://github.example.com/api/v3/` for Enterprise. Forgejo/Gitea: instance root, `/api/v1` is appended. Gate credentials of a Basic-auth proxy go in the URL: see [trackers and the mirror](../concepts/trackers-and-the-mirror.md#an-instance-behind-http-basic-auth). |
| `token` | Redmine API key; GitHub personal access token with the `repo` scope (fine-grained: Issues, Contents and Pull requests, read and write); Forgejo/Gitea access token with the `issue` scope, plus `repository` for pull requests. |
| `projects` | Optional list of projects, one `[[issue-tracker.projects]]` table each. `id` is the tracker's name for the project (Redmine: a slug or numeric ID; GitHub/Forgejo/Gitea: `"owner/repo"`); `name` is a stable local name, so renaming the project in the tracker only means editing `id`. Every open issue of those projects is listed, whoever it is assigned to. Omitted: every open issue you can see across the tracker (GitHub: the issues involving you, assigned, created, mentioned or subscribed). **Exactly one entry is required** to update issue status on GitHub/Forgejo/Gitea, whose issue endpoints are scoped to one repository. |
| `mirror` | `true` mirrors the project's issues with the issues stored in the repository. Needs a tracker type and exactly one project. See [trackers and the mirror](../concepts/trackers-and-the-mirror.md#the-mirror). |

## `[push]`

```toml
[push]
propose = true   # false disables the post-action "push now?" proposal everywhere
```

`--push` / `--no-push` on a command override the proposal for that run.

## `[review]`

```toml
[review]
require-signed = false   # true: approvals are signed, and a close needs a verified one
```

With `require-signed = true`, `review approve` signs its commit and
`issue close` refuses to merge without an approval that `git verify-commit`
accepts and that covers the commit being merged. See
[review a branch](../workflows/review.md#signed-approvals).

## `[log]`

```toml
[log]
default-cmd = "graph"            # the command `git zf log` opens with

[[log.cmds]]
name = "mine"                    # shown in the view and offered by `c`
description = "my commits"       # optional, shown after the name
key = 7                          # 1-9 switches to it; 0 or omitted = no shortcut
cmd = "git log --exclude='*zf/*' --all --author='Jane' --oneline --decorate --pretty=tformat:'%Cred%h%Creset %s %Cgreen(%cr)%Creset'"
```

`cmd` is a shell command line starting with `git log` (the `git` may be left
out); it is split like a shell would, so a quoted `--pretty` format works.
Every line it prints must start with the commit hash, after the graph
characters, for `enter` to find the commit: put `%h` or `%H` first in a
custom format. The view forces colors (`--color=always` is appended);
without a terminal git's own color rules apply, so pass `-- --color=always`
to pipe colors into `less -R`. The arguments given after `--` on the command
line are appended too.

A `[[log.cmds]]` array in a config file replaces the whole built-in list,
like `commit-message.items`, so copy the entries you keep from the defaults.
Names and keys must be unique, keys are 0 to 9, and `default-cmd` must name
one of the entries (it falls back to the first when empty). The six built-in
commands are listed on the [log](../workflows/log.md) page.
