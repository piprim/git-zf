---
title: "git zf branch close"
sidebar_label: "branch close"
---

## git zf branch close

Record one tracked branch as closed, without merging or deleting it

### Synopsis

Record the named branch as closed (abandoned) on its chain and push that
record. Nothing is merged and no git branch is deleted, here or on the remote:
to merge an issue branch, use 'git zf issue close'.

The branch does not have to exist in this clone, and it does not matter who
started it. Only a branch in progress can be closed.

To undo, check the branch out and run 'git zf branch track'.

```
git zf branch close <branch-name> [flags]
```

### Options

```
  -h, --help   help for close
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

