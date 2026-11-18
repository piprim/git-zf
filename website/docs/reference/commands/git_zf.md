---
title: "git zf"
sidebar_label: "git zf"
---

## git zf

Git Zen workFlow: issue → branch → commit → review → close

### Synopsis

A TUI powered CLI that wraps a git-flow workflow (issue → branch → commit →
review → close) with optional issue-tracker integration.

Every command is interactive by default: it asks what it needs in a form.
Passing a flag skips the matching prompt, and --help lists the flags of any
command.

Without a subcommand, git zf opens a home screen. Its header says where you
are: the current branch and its issue, the files staged, modified and
untracked, the commits ahead of and behind the upstream, the state of the
review. Under it are the workflow commands, each with a key and what waits
there: c commit, i issue, b branch, v review, l log. A key runs its command
with its interactive defaults, enter runs the one under the cursor. s shows
the working tree as git status does, r fetches the remote and reads
everything again, q quits. Esc does nothing on the home screen.

Leaving a command returns to the home screen, refreshed: q, or Esc once
there is nothing left to dismiss, closes the issue table, the branch table
and the log. git zf review opens a menu of its own that works the same way;
git zf issue and git zf branch open their tables.

When stdin is not a terminal (a script, CI) this help is printed instead.
Outside a git repository it is printed too, under a line saying so. The
setup commands (init, install, uninstall, config, completion, version) are
not on the home screen.

```
git zf [flags]
```

### Options

```
  -d, --debug   debug mode, print debug info to stdout
  -h, --help    help for git zf
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches
* [git zf commit](git_zf_commit.md)	 - Record changes to the repository
* [git zf completion](git_zf_completion.md)	 - Generate completion script
* [git zf config](git_zf_config.md)	 - Manage git zf configuration
* [git zf init](git_zf_init.md)	 - Initialize git zf in the current repository (installs the pre-push and pre-commit hooks)
* [git zf install](git_zf_install.md)	 - Install this tool to git-core as zf
* [git zf issue](git_zf_issue.md)	 - Manage issues
* [git zf log](git_zf_log.md)	 - Browse the commit graph; enter shows a commit
* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch
* [git zf uninstall](git_zf_uninstall.md)	 - uninstall zf from git-core
* [git zf version](git_zf_version.md)	 - Print version information and quit

