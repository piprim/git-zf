---
title: "git zf branch new"
sidebar_label: "branch new"
---

## git zf branch new

Create a new branch (manual input)

### Synopsis

Create a branch from a form, with no issue behind it: the ID, the type and
the title are typed, then a branch named &lt;id&gt;@&lt;type&gt;@&lt;slug&gt; is created and
checked out, or a worktree is created for it. It never asks the tracker.

A typed ID is used as is: an issue living where git zf does not look. An
empty ID files an issue in the repository and uses its ID. The branch is
recorded in the repository and pushed, as git zf issue start does. If there
is a ticket, use git zf issue start instead. --variant creates a parallel
branch on an issue that already has one.

```
git zf branch new [flags]
```

### Options

```
  -h, --help             help for new
      --variant string   create a parallel branch for the same issue (e.g. --variant=spike)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

