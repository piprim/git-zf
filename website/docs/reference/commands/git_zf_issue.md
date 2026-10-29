---
title: "git zf issue"
sidebar_label: "issue"
---

## git zf issue

Manage issues

### Synopsis

An issue is a piece of work with an identity: a ticket in the configured
tracker (Redmine, GitHub, Forgejo/Gitea), or an issue stored in the
repository itself under refs/zf/issues/ when there is no tracker. Repository
issues travel with git zf issue sync, so a fresh clone sees the same backlog,
descriptions and comments, with no account anywhere.

From the moment an issue is started it has a branch, named after it:
git zf issue start creates the branch, git zf commit pre-fills the commit
form from it, and git zf issue close merges it into the base and offers to
update the tracker. new, show, edit, comment and label work on repository
issues.

Without a subcommand the issue table opens, as git zf issue list does.

```
git zf issue [flags]
```

### Options

```
  -h, --help   help for issue
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close
* [git zf issue close](git_zf_issue_close.md)	 - Close an issue (merge branch, record it, update the tracker)
* [git zf issue comment](git_zf_issue_comment.md)	 - Comment on an issue stored in the repository
* [git zf issue edit](git_zf_issue_edit.md)	 - Edit the title and description of an issue stored in the repository
* [git zf issue label](git_zf_issue_label.md)	 - Add or remove labels on an issue stored in the repository
* [git zf issue list](git_zf_issue_list.md)	 - List issues
* [git zf issue new](git_zf_issue_new.md)	 - Create an issue in the repository (no branch)
* [git zf issue show](git_zf_issue_show.md)	 - Show an issue stored in the repository and its comments
* [git zf issue start](git_zf_issue_start.md)	 - Start work on an issue (create branch)
* [git zf issue sync](git_zf_issue_sync.md)	 - Fetch, merge and push the issues stored in the repository
* [git zf issue track](git_zf_issue_track.md)	 - Track the current branch with git zf (for branches created with plain git checkout)

