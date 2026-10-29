---
title: "git zf branch pr"
sidebar_label: "branch pr"
---

## git zf branch pr

Open a pull request for a branch on the forge

### Synopsis

Open a pull request for the branch (the current one by default) on the
issue tracker's forge, GitHub or Forgejo. The branch is pushed first when it
is not on the remote. A branch that already has an open pull request gets its
URL printed.

The form is prefilled from the branch's issue: its title, its parent branch as
the base, and "Closes #N" when the issue is mirrored with the tracker.

```
git zf branch pr [branch] [flags]
```

### Options

```
  -h, --help      help for pr
      --no-push   skip the post-action push proposal
      --push      push the resulting branch to the remote after the action (skips the prompt)
  -y, --yes       non-interactive: refused, a pull request needs a title
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

