---
title: "git zf issue close"
sidebar_label: "issue close"
---

## git zf issue close

Close an issue (merge branch, record it, update the tracker)

### Synopsis

Pick an in-progress branch, merge it into the base branch (rebase, squash, or classic),
record the branch as merged, update the remote tracker, then optionally delete the local branch.

With &lt;id&gt;, close an issue stored in the repository without merging anything: for
a duplicate or an issue that will not be worked on. &lt;id&gt; is the full ID or a
unique prefix of at least 4 characters. It is refused while a branch of the
issue is in progress: merge it with git zf issue close, or abandon it with
git zf branch close &lt;branch-name&gt;, then close the issue. With &lt;id&gt; none of
the flags below apply.

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

