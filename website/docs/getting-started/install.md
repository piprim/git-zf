---
sidebar_position: 1
title: Install
---

After this page, `git zf` runs in your repository, with the hooks that the
review flow relies on and shell completion for its commands.

## Prerequisites

- Git
- [Go 1.25+](https://go.dev/dl/), only to build from source or `go install`

## Install the binary

Three ways. git knows the binary as `git zf` once it is in git's exec path
(the release archive) or anywhere on your `PATH` (the other two).

### A release archive

Download the archive for your platform from the
[releases page](https://github.com/piprim/git-zf/releases). It is named
`git-zf_<version>_<os>_<arch>.tar.gz` and holds one binary. Unpack it, then
let the binary install itself:

```
$ tar xzf git-zf_*_linux_amd64.tar.gz
$ sudo ./git-zf install    # copies the binary to $(git --exec-path)
```

If `git --exec-path` is writable by you, as with a Homebrew git on macOS,
omit `sudo`. Remove the binary with `git zf uninstall`.

### `go install`

```
$ go install github.com/piprim/git-zf@latest
```

The binary lands in Go's bin directory (`go env GOBIN`, else `GOPATH/bin`),
which must be on your `PATH`. No root is needed. To remove it, delete
`git-zf` from that directory.

### From source

```
$ git clone https://github.com/piprim/git-zf.git
$ cd git-zf
$ make install             # go install, into Go's bin directory
```

No root is needed either. `make install` prints where the binary went, and
warns when an older `git-zf` would run instead: one left in git's exec path
by an earlier install, or one earlier on your `PATH`. On Windows, run it
from Git Bash. Remove the binary with `make uninstall`.

Whichever way you chose, check with `git zf version`.

## Initialize a repository

Once per repository, and per submodule, run from the main checkout:

```
$ git zf init
```

It installs two hooks:

- **pre-push** blocks pushes to a branch locked for review. It runs
  `git zf review guard`. Bypass with `git push --no-verify`.
- **pre-commit** blocks commits on a feature branch while reviewer commits
  await incorporation. It runs `git zf review guard-commit`. Bypass with
  `git commit --no-verify`.

It also configures every remote so that a plain `git fetch` brings the
reviews, issues and branch records that git-zf stores under `refs/zf/`, and
so that `git fetch --prune` does not delete their tracking refs.

The hooks go where git reads them: the directory named by `core.hooksPath`
when it is set, otherwise the hooks directory shared by every worktree. In a
submodule that is the submodule's own git directory, not the parent's. Run
it from the main checkout, not from a linked worktree: git reads hooks from
the common git dir, and inside a worktree they would be written where git
never looks.

Re-running `git zf init` is safe. A hook you wrote yourself is never
overwritten: a warning prints the snippet to add to it instead.

## Shell completion

```
$ git zf completion bash > ~/.local/share/bash-completion/completions/git-zf   # user-only
$ # or: sudo sh -c 'git zf completion bash > /etc/bash_completion.d/git-zf'   # system-wide
$ source ~/.bashrc
```

The script completes `git-zf …` and, through git's own completion,
`git zf …`. For the latter the file must be named `git-zf`, as above, so that
git finds it. See the
[Cobra shell-completion guide](https://cobra.dev/docs/how-to-guides/shell-completion/)
for zsh, fish and PowerShell.

## Commands used

- [`git zf install`](../reference/commands/git_zf_install.md)
- [`git zf init`](../reference/commands/git_zf_init.md)
- [`git zf version`](../reference/commands/git_zf_version.md)
- [`git zf completion`](../reference/commands/git_zf_completion.md)
- [`git zf uninstall`](../reference/commands/git_zf_uninstall.md)
