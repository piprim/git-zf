---
title: "git zf init"
sidebar_label: "init"
---

## git zf init

Initialize git zf in the current repository (fetch refspecs for refs/zf/)

### Synopsis

Add fetch refspecs to every remote, so that a plain 'git fetch' brings the
reviews, issues and branch records stored under refs/zf/ and 'git fetch --prune'
does not delete their tracking refs.

git zf installs no hook. An older 'git zf init' installed a pre-push, a
pre-commit and a pre-rebase hook: a hook file that still holds one of those
scripts unchanged is deleted, where git reads hooks (the directory named by
core.hooksPath when it is set, otherwise the hooks directory shared by every
worktree). A hook of your own is never touched; when it calls 'git zf review
guard', init says that the call can be removed.

Run 'git zf install' first to make the 'git zf' binary available in the git
exec-path, then run 'git zf init' once per repository (and per submodule).

```
git zf init [flags]
```

### Options

```
  -h, --help   help for init
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close

