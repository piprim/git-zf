---
title: "git zf review sync"
sidebar_label: "review sync"
---

## git zf review sync

Bring a branch up to date: merge pending reviewer commits, then parent drift

### Synopsis

Pick an in-progress branch (the current one is pre-selected) and bring it up
to date in two steps.

First, reviewer commits pending on &lt;ID&gt;@review are merged in. On a
conflict the merge is left in progress for you to resolve, then git zf
commit concludes it. A dirty working tree is refused for this step: git
stash first.

Then, for a sub-task branch only, the parent branch (&lt;remote&gt;/&lt;parent&gt;) is
merged into it. A conflict there is aborted and reported.

```
git zf review sync [flags]
```

### Options

```
  -h, --help   help for sync
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

