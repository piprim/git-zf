---
title: "git zf issue close"
sidebar_label: "issue close"
---

## git zf issue close

Close an issue by merging its branch

### Synopsis

Pick an in-progress branch (the current one is pre-selected), merge it into
the base branch (rebase, squash, or classic), record the branch as merged,
update the remote tracker, then optionally delete the local branch.

With &lt;id&gt;, the branch of that issue is the one merged. &lt;id&gt; is resolved as
git zf issue start &lt;id&gt; resolves it: the full ID of a repository issue, a
unique prefix of at least 4 characters, or the number of a tracker issue. One
branch in progress is picked without the picker; several (variants) are the
only ones the picker offers. An issue with no branch in progress is told so:

    Issue &lt;id&gt; has no branch in progress. To close it without a merge: git zf issue abandon &lt;id&gt;

An ID that is neither an issue nor a branch is an error.

Closing always merges. For a duplicate or an issue that will not be worked
on, git zf issue abandon &lt;id&gt; closes it without a merge. --base, --push and
--no-push apply with or without &lt;id&gt;.

```
git zf issue close [<id>] [flags]
```

### Options

```
      --base string   merge target branch (default: parent integration branch or base, with an interactive picker)
  -h, --help          help for close
      --no-push       skip the post-action push proposal
      --push          push the resulting branch to the remote after the action (skips the prompt)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

