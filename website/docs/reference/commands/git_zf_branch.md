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

Without a subcommand, a table shows every local branch: a tracked one with
its issue, type, status, title and creation date, a plain one with those
columns empty; the current branch is starred. S checks the statuses shown
(plain, in_progress, merged, closed), T the types, R adds the branches that
exist only on the remote, / filters by text. Enter opens the actions of the
branch under the cursor: checkout; issue close (merge) for an in-progress
branch, the flow of git zf issue close; merge into current for a plain
branch; pull request; abandon (record closed, no merge); track, which records
a plain branch after the branch new form and renames it &lt;id&gt;@&lt;type&gt;@&lt;slug&gt;;
log, the commits of that branch alone; delete. Each action also has a key on
the row: C checks out, c closes the issue by merging, m merges into the
current branch, u opens a pull request, a twice abandons, t tracks, l shows
the log, D twice deletes; a key that does not apply to the row says so. n
creates a branch, p prunes, P prunes with the tracker, q quits. Without a
terminal the rows are printed.

list shows the tracked branches, yours and the ones other clones started.
prune records what happened to in-progress branches outside git-zf, and
prune-tracker reaps local branches whose tracker issue is closed. pr opens
a pull request on the forge.

branch close &lt;name&gt; does not merge: it records a branch as abandoned. The
action that merges an issue branch and updates the tracker is git zf issue
close.

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

