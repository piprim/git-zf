---
title: "git zf review request"
sidebar_label: "review request"
---

## git zf review request

Submit an issue branch for code review (locks the branch)

### Synopsis

Pick an in-progress branch (the current one is pre-selected) and submit it
for review. Its review ref becomes "in_review" and the branch is locked: the
pre-push hook installed by git zf init rejects pushes to it until the
reviewer decides (bypass: git push --no-verify).

On a branch that was already reviewed, this starts the next round: the round
counter goes up and the stale &lt;IssueID&gt;@review branch is removed. If that
branch still carries reviewer commits not yet in yours, the request refuses to
delete them and offers to merge them in first.

The review record is pushed (--push / --no-push skip the proposal), and when
the issue came from a tracker a status update is proposed.

```
git zf review request [flags]
```

### Options

```
  -h, --help      help for request
      --no-push   skip the post-action push proposal
      --push      push the resulting branch to the remote after the action (skips the prompt)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

