# Git-ZF - Git Zen workFlow

<p align="center">
  <img src="./assets/git-zf-logo.webp" alt="Logo git-zf" width="272" />
</p>

> A TUI powered CLI that wraps a git workflow (issue → branch → commit → review → close) with optional issue-tracker integration and bi-directional synchronization.

git-zf is a git subcommand. `git zf` opens a menu; `git zf issue start` names
a branch after an issue, `git zf commit` composes a commitizen-style message
pre-filled from it, `git zf review request` locks the branch for a peer
review, and `git zf issue close` merges it into the base with the strategy you
pick. Every command is interactive by default, and every prompt has a flag.

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
`sudo ./git-zf install`. Or with Go:

```bash
go install github.com/piprim/git-zf@latest
sudo git-zf install    # copies the binary to $(git --exec-path)
```

Or from source:

```bash
git clone https://github.com/piprim/git-zf.git
cd git-zf
make
sudo make install      # copies the binary to $(git --exec-path)
```

If `git --exec-path` is user-writable (a Homebrew git on macOS), omit `sudo`.
Check with `git zf version`. Then, once per repository, run `git zf init`
from the main checkout to install the hooks.

## Documentation

The user documentation, from the first commit to the review round, the
tracker mirror and every command, is at **https://piprim.github.io/git-zf/**.

---

## LLM policy

This project is assisted by LLMs.
