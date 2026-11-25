---
title: "git zf review request"
sidebar_label: "review request"
---

## git zf review request

Submit an issue branch for code review

### Synopsis

Pick an in-progress branch (the current one is pre-selected) and submit it
for review. Its review ref becomes "in_review" and records the branch and the
commit submitted. The branch is not locked: the reviewer works on it too, and
git zf issue close merges it only when its tip is the commit approved.

On a branch that was already reviewed, approved or not, this starts the next
round: the round counter goes up. It is the way out when commits were made
after an approval.

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

