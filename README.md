# Git-ZF - Git Zen workFlow

<p align="center">
  <img src="./assets/logo-gitzf-464x481.webp" alt="Logo git-zf" width="464" height="481"/>
</p>

> A TUI powered CLI that wraps a git workflow (issue → branch → commit → review → close) with optional issue-tracker integration and bi-directional synchronization.

git-zf is a git subcommand. `git zf` opens a home screen; `git zf issue start` names
a branch after an issue, `git zf commit` composes a commitizen-style message
pre-filled from it, `git zf review request` locks the branch for a peer
review, and `git zf issue close` merges it into the base with the strategy you
pick. Every command is interactive by default, and every prompt has a flag.

git-zf's screens stay in the top half of the terminal and the bottom half is
an ordinary terminal: the forms open there, and what a commit, a push or a
merge prints stays there, the questions of a git hook included. The `[ui]`
section of the configuration sets the size of the two parts, or turns the
split off.

Everything git-zf records lives in the repository, under `refs/zf/`, as chains
of commits: the issues when there is no tracker, the branches in progress and
the reviews. There is no database and no server. A fresh clone sees the whole
backlog after `git fetch`, and two people acting at once are merged, never
overwritten.

With a tracker configured, git-zf talks to Redmine, GitHub or Forgejo/Gitea:
it lists their issues, moves them through their statuses, and can mirror a
project's issues with the ones in the repository. On GitHub and Forgejo it
also opens and merges pull requests.

## Install

Download a release archive from the
[releases page](https://github.com/piprim/git-zf/releases), unpack it and run
`sudo ./git-zf install`, which copies the binary to `$(git --exec-path)`
(omit `sudo` if that directory is user-writable, as with a Homebrew git on
macOS). Or with Go, no root needed:

```bash
go install github.com/piprim/git-zf@latest
```

Or from source, no root needed either:

```bash
git clone https://github.com/piprim/git-zf.git
cd git-zf
make install           # go install, into Go's bin directory
```

Both put `git-zf` in Go's bin directory (`go env GOBIN`, else `GOPATH/bin`),
which must be on your `PATH`. `make install` warns when an older `git-zf`
would run instead. On Windows, run it from Git Bash.
Check with `git zf version`. Then, once per repository, run `git zf init`
from the main checkout to install the hooks.

## Documentation

The user documentation, from the first commit to the review round, the
tracker mirror and every command, is at **https://piprim.github.io/git-zf/**.

---

## LLM policy

This project is assisted by LLMs.
