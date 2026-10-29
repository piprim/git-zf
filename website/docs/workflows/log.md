---
sidebar_position: 9
title: Browse the log
---

After this page you can browse the commit graph, open a commit, switch
between log commands, and linearize a merge at the tip of a branch.

```
$ git zf log                    # browse the commit graph of every branch and tag
$ git zf log -- -n 100          # arguments after -- go to git log
$ git zf log -- main..feature   # a revision range
$ git zf log -- --color=always | less -R   # without a terminal: the plain git log output
```

An interactive view of `git log`, without git-zf's own refs (`refs/zf/*`).

| Key | Does |
|---|---|
| `enter` | opens the commit under the cursor in a dialog with its stat, raw file list and patch; `esc` returns |
| `/` | filters the lines as you type; `enter` keeps the filter, `esc` clears it |
| `←` `→` | scroll long lines |
| `1` to `9` | switch to the command with that key |
| `c` | pick a command by name |
| `L` | linearize the merge under the cursor (below) |
| `q` | quits |

## The commands

The view runs one of several log commands and shows the name of the
current one. Six are built in:

| Key | Name | Shows |
|---|---|---|
| 1 | `graph` | every branch and tag as a graph, with both dates and the committer (the default) |
| 2 | `branch` | the current branch and what was merged into it |
| 3 | `linear` | a flat list of every commit but the merges, across branches |
| 4 | `first-parent` | the current branch, one line per merged feature |
| 5 | `recent` | what moved in the last two weeks, across branches |
| 6 | `tags` | tagged commits only |

Your own commands go under `[log]` in the config: see
[configuration](../reference/configuration.md#log).

## Linearizing a merge

`L` on a merge at the tip of a local branch replaces it by one commit
holding its tree: the branch stays bit-identical but linear. It fetches the
branch's remote first and is refused when the merge is not at the tip or
when the upstream moved. The push that follows is offered with a lease on
what was fetched. The old tip stays in the reflog.

## Commands used

- [`git zf log`](../reference/commands/git_zf_log.md)
