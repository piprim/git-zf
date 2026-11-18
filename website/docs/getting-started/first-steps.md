---
sidebar_position: 2
title: First steps
---

After this page you have made a commit through the form, found your way
around the menu, and know where the configuration lives.

Every command is interactive by default. Passing a flag skips the matching
prompt, and `--help` lists the flags of any command.

## The home screen

```
$ git zf
```

Without a subcommand, `git zf` opens a home screen. Its header says where you
are: the current branch and its issue, the files staged, modified and
untracked, the commits ahead of and behind the upstream, and the state of the
review. Under it are the workflow commands, each with a key and what waits
there:

| Key | Command | Hint |
| --- | --- | --- |
| `c` | **Commit** | the files staged or modified |
| `i` | **Issue** | the open issues |
| `b` | **Branch** | the branches in progress |
| `v` | **Review** | the reviews waiting |
| `l` | **Log** | |

A key runs its command with its interactive defaults; `enter` runs the one
under the cursor. `s` shows the working tree as `git status` does, `r`
fetches the remote and reads everything again, `q` quits. `Esc` does nothing
on the home screen.

Leaving a command brings you back to the home screen, refreshed: `q`, or
`Esc` once there is nothing left to dismiss, closes the issue table, the
branch table and the log. `git zf review` opens a menu of its own that works
the same way; `git zf issue` and `git zf branch` open their tables.

When stdin is not a terminal, in a script or in CI, the usual `--help` text
is printed instead. Outside a git repository it is printed too, under a line
saying so. The setup commands (`init`, `install`, `uninstall`, `config`,
`completion`, `version`) are not on the home screen.

## Your first commit

```
$ git zf commit
```

A commitizen-style form opens: type, scope, subject, body, footer. Then an
options page: stage all, amend, sign-off, hooks, push. Every option has a
flag (`-a`, `--amend`, `-s`, `-n`, `--push`, `--no-push`, `-y`); if any
commit flag is passed the options page is skipped and the flags are used
directly.

The [commit workflow](../workflows/commit.md) page covers what the form
pre-fills on an issue branch and what the review guard does.

## Configuration

```
$ git zf config init   # write the default config file, to edit from there
$ git zf config show   # the active config path and the effective config
```

`config init` writes to `$HOME/.git-zf.toml` when run outside a repository
with no home config, otherwise a picker offers the home file or
`<repo>/.git/.git-zf.toml`. An existing file is overwritten only after
confirmation. Every key is optional: the built-in defaults apply where a
file says nothing. The [configuration](../reference/configuration.md) page
lists every key.

## Where next

- [Issues and branches](../concepts/issues-and-branches.md): the two things
  git-zf works with, and which command to reach for.
- [Where git-zf keeps its state](../concepts/state-in-the-repository.md):
  why there is no database, and what a clone sees.
- [Trackers and the mirror](../concepts/trackers-and-the-mirror.md): Redmine,
  GitHub, Forgejo, and the issues stored in the repository.

## Commands used

- [`git zf`](../reference/commands/git_zf.md)
- [`git zf commit`](../reference/commands/git_zf_commit.md)
- [`git zf config init`](../reference/commands/git_zf_config_init.md)
- [`git zf config show`](../reference/commands/git_zf_config_show.md)
