---
title: "git zf branch prune-tracker"
sidebar_label: "branch prune-tracker"
---

## git zf branch prune-tracker

Reap branches whose tracker issue is closed

### Synopsis

Discover local branches whose issue ID (regex-extracted from the branch name)
is closed in the configured tracker, and offer per-branch reap actions
(safe-delete / force-delete / skip). Successful reaps record the branch as
closed.

```
git zf branch prune-tracker [flags]
```

### Options

```
      --base string                  base branch name to exclude from candidate discovery (default: auto-detect)
      --dry-run                      show what would be done without prompting or mutating
      --force-delete git branch -D   non-interactive: apply git branch -D to every match
  -h, --help                         help for prune-tracker
      --safe-delete git branch -d    non-interactive: apply git branch -d to every match
      --skip-delete                  non-interactive: never touch refs; only record the status
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

