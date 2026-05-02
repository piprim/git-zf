# Git-ZF

> Command line utility to manage git workflow, connect to issue trackers, and standardize commit messages through a TUI.

## Getting Started

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- Git

### Install from source

```bash
git clone https://github.com/piprim/git-zf.git
cd git-zf
make
sudo make install      # copies binary to $(git --exec-path)
```

> On macOS with Homebrew Git the exec-path is user-writable; omit `sudo`.

### Install via `go install`

```bash
go install github.com/piprim/git-zf@latest
sudo git-zf install    # copies binary to $(git --exec-path)
```

> If `git --exec-path` is user-writable, omit `sudo`.

### Verify

```bash
git zf version
```

## Usage

### Commit
```
$ git zf commit
```

```
Usage:
  git-zf commit [flags]

Flags:
  -a, --all             stage all tracked modified/deleted files before committing
      --allow-empty     allow a commit with no changes
      --amend           replace the tip of the current branch
      --author string   override commit author as "Name <email>"
  -h, --help            help for commit
  -n, --no-verify       bypass pre-commit and commit-msg hooks
  -s, --signoff         add Signed-off-by trailer to the commit message
```

If any commit flag is passed, the options page of the TUI form is skipped and the flags are used directly.

### Issue
```
$ git zf issue
$ git zf issue start
```

Start work on an issue: optionally fetch open issues from a tracker (Redmine), or enter an issue ID, title, and type manually. A properly named branch is created and checked out automatically. Branch state is tracked in `.git/git-zf.db`.

If a tracker is configured, `issue start` pre-selects fetching from the tracker; after picking an issue you can update its status to "In Progress" in one step.

### Branch
```
$ git zf branch new       # create a branch with manual input
$ git zf branch list      # list tracked branches
$ git zf branch merge     # merge a branch via TUI
$ git zf branch prune     # clean up stale DB records
```

`branch new` is the same flow as `issue start` but pre-selects manual input.

`branch list` flags:
```
--status string   filter by status: in_progress, merged, all (default: in_progress)
--stdout          print table to stdout without TUI
--json            print JSON array to stdout
```

`branch prune` flags:
```
--base string   base branch for merge detection (default: auto-detected)
--dry-run       show what would be pruned without executing
```

### All commands
```txt
Usage:
  git-zf [command]

Available Commands:
  branch      Manage local branches
  commit      Record changes to the repository
  completion  Generate completion script
  help        Help about any command
  install     Install this tool to git-core as git-zf
  issue       Manage issues
  version     Print version information and quit

Flags:
  -d, --debug   debug mode, output debug info to debug.log
  -h, --help    help for git-zf
```

## Configure

Config file: `.git-zf.json` (JSON) at repository root or `$HOME`. Repository root takes priority over home directory.

The default configuration is embedded in [`config/default.json`](https://github.com/piprim/git-zf/blob/master/config/default.json).

### Commit types

Override the list of commit types shown in the type selector:

```json
{
  "commit-types": [
    { "name": "feat",  "desc": "A new feature" },
    { "name": "fix",   "desc": "A bug fix" },
    { "name": "chore", "desc": "Build process or tooling changes" }
  ]
}
```

### Commit message

Override the form fields and/or the message template:

```json
{
  "commit-message": {
    "items": [
      { "name": "scope",   "desc": "Scope (users, db, poll…):", "form": "input" },
      { "name": "subject", "desc": "Concise description. Imperative, lower case, no final dot:", "form": "input", "required": true },
      { "name": "body",    "desc": "Motivation for the change:", "form": "multiline" },
      { "name": "footer",  "desc": "Breaking changes and referenced issues:", "form": "multiline" }
    ],
    "template": "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
  }
}
```

### Branch naming

Branch names follow the format `{issue-id}@{type}@{slugified-title}@{short-uuid}`, e.g.:

```
ABC-42@feat@add-oauth-login@550e8400
```

To override the base branch (default: auto-detected from `origin/HEAD`, then `main`, then `master`):

```json
{
  "branch": {
    "base": "develop"
  }
}
```

### Tracker integration

`git zf issue start` can fetch open issues assigned to you from a project tracker. Currently supported: **Redmine**.

Add an `issue-tracker` section to `.git-zf.json`:

```json
{
  "issue-tracker": {
    "type": "redmine",
    "url": "https://redmine.example.com",
    "token": "YOUR_API_KEY",
    "in-progress-status": "In Progress"
  }
}
```

| Key | Description |
|-----|-------------|
| `type` | Tracker type. Currently only `"redmine"` is supported. |
| `url` | Base URL of your tracker instance. |
| `token` | API key / personal access token. |
| `in-progress-status` | Status name to set when work starts (default: `"In Progress"`). |

When a tracker is configured:
1. `issue start` asks whether to fetch issues from the tracker.
2. If yes, open issues assigned to you are listed; type any key to filter the list, pick one and select a branch type.
3. After the branch is created, you can update the issue status in the tracker in one step.
4. If the tracker is unavailable or returns no issues, the flow falls back to manual input.
