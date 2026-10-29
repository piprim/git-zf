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

Without a subcommand, git zf opens a menu of the workflow commands (commit,
issue, branch, review, log) and runs the one you pick with its interactive
defaults. git zf branch and git zf review do the same one level down; git zf
issue opens the issue table. Esc or ctrl+c leaves the menu quietly. When stdin
is not a terminal (a script, CI) the menu is skipped and this help is printed.
The setup commands (init, install, uninstall, config, completion, version)
are not in the menu.

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

