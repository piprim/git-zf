---
title: "git zf issue new"
sidebar_label: "issue new"
---

## git zf issue new

Create an issue in the repository (no branch)

### Synopsis

Create an issue stored in the repository under refs/zf/issues/ and push it.
No branch is created: run "git zf issue start" to begin work on it.
Passing any flag skips the form.

```
git zf issue new [flags]
```

### Options

```
      --assign               assign the issue to you
      --description string   issue description
  -h, --help                 help for new
      --label stringArray    label to add (repeatable)
      --title string         issue title
      --type string          branch type, one of the configured commit types (default: the first)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

