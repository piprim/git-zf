---
title: "git zf branch merge"
sidebar_label: "branch merge"
---

## git zf branch merge

Merge a branch into the current branch

### Synopsis

Pick a local or remote branch and merge it into the current branch
(rebase, squash, or classic), then optionally delete it and push. Any branch
can be the source: one git zf created, or a plain git branch that follows no
naming convention. Issue branches are refused — use `git zf issue close` for those.

```
git zf branch merge [flags]
```

### Options

```
  -h, --help      help for merge
      --no-push   skip the post-action push proposal
      --push      push the resulting branch to the remote after the action (skips the prompt)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

