---
title: "git zf review approve"
sidebar_label: "review approve"
---

## git zf review approve

Approve a review — signals the branch is ready to close

### Synopsis

Pick a review you started and approve it. Its status becomes "approved"
and the developer can run git zf issue close, which incorporates any commit
you left on &lt;ID&gt;@review before merging.

With require-signed = true under [review] in the config, the approval is a
signed commit, and git zf issue close refuses to merge without an approval
that git verify-commit accepts and that covers the commit being merged.

The review record is pushed (--push / --no-push skip the proposal), and when
the issue came from a tracker a status update is proposed.

```
git zf review approve [flags]
```

### Options

```
  -h, --help      help for approve
      --no-push   skip the post-action push proposal
      --push      push the resulting branch to the remote after the action (skips the prompt)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

