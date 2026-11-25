---
title: "git zf branch track"
sidebar_label: "branch track"
---

## git zf branch track

Track the current branch with git zf (for branches created with plain git checkout)

### Synopsis

Register the current branch without creating a new branch: it is recorded
as in progress for its issue.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start'.

  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
  git zf review track
  git zf review request

```
git zf branch track [flags]
```

### Options

```
  -h, --help   help for track
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

