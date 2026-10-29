---
title: "git zf init"
sidebar_label: "init"
---

## git zf init

Initialize git zf in the current repository (installs the pre-push and pre-commit hooks)

### Synopsis

Install the git zf pre-push and pre-commit hooks into the current repository.

The pre-push hook calls 'git zf review guard' before every push, blocking pushes to
branches that are locked for code review. The pre-commit hook calls 'git zf review
guard-commit', which blocks new commits on a feature branch while reviewer commits
await incorporation.

The hooks are written where git reads them (resolved via 'git rev-parse
--git-path hooks'): the directory named by core.hooksPath when it is set,
otherwise the hooks directory shared by every worktree. In a git submodule that
is the submodule's own git directory, not the parent repo's.

It also adds two fetch refspecs to every remote, so that a plain 'git fetch'
brings the reviews and issues stored under refs/zf/ and 'git fetch --prune'
does not delete their tracking refs.

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

