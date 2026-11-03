---
title: "git zf branch"
sidebar_label: "branch"
---

## git zf branch

Manage local branches

### Synopsis

A branch is where the work happens in git. It does not need an issue
behind it: git zf branch new creates one with a typed ID that is looked up
nowhere, git zf branch track records one checked out with plain git, and git
zf branch merge merges a branch with no tracker step. If there is a ticket,
go through git zf issue instead.

list shows the tracked branches, yours and the ones other clones started.
prune records what happened to in-progress branches outside git-zf, and
prune-tracker reaps local branches whose tracker issue is closed. pr opens
a pull request on the forge.

branch close &lt;name&gt; does not merge: it records a branch as abandoned. The
action that merges an issue branch and updates the tracker is git zf issue
close.

Without a subcommand a menu offers these actions.

```
git zf branch [flags]
```

### Options

```
  -h, --help   help for branch
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close
* [git zf branch close](git_zf_branch_close.md)	 - Record one tracked branch as closed, without merging or deleting it
* [git zf branch list](git_zf_branch_list.md)	 - List branches
* [git zf branch merge](git_zf_branch_merge.md)	 - Merge a branch into the current branch
* [git zf branch new](git_zf_branch_new.md)	 - Create a new branch (manual input)
* [git zf branch pr](git_zf_branch_pr.md)	 - Open a pull request for a branch on the forge
* [git zf branch prune](git_zf_branch_prune.md)	 - Record branches merged or deleted outside git zf
* [git zf branch prune-tracker](git_zf_branch_prune-tracker.md)	 - Reap branches whose tracker issue is closed
* [git zf branch track](git_zf_branch_track.md)	 - Track the current branch with git zf (for branches created with plain git checkout)

