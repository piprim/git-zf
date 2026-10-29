---
sidebar_position: 2
title: First steps
---

After this page you have made a commit through the form, found your way
around the menu, and know where the configuration lives.

Every command is interactive by default. Passing a flag skips the matching
prompt, and `--help` lists the flags of any command.

## The menu

```
$ git zf
```

Without a subcommand, `git zf` opens a menu of the workflow commands:
**Commit**, **Issue**, **Branch**, **Review**, **Log**. It runs the one you
pick with its interactive defaults. `git zf branch` and `git zf review` do
the same one level down; `git zf issue` opens the issue table. `Esc` or
`ctrl+c` leaves the menu quietly.

When stdin is not a terminal, in a script or in CI, the menu is skipped and
the usual `--help` text is printed. The setup commands (`init`, `install`,
`uninstall`, `config`, `completion`, `version`) are not in the menu.

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
