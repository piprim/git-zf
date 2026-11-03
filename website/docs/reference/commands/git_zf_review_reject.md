---
title: "git zf review reject"
sidebar_label: "review reject"
---

## git zf review reject

Request changes on a review — unlocks the branch for the next iteration

### Synopsis

Pick a review you started and request changes, with a reason (-m, or -F
for a file). Its status becomes "changes_requested" and the branch is
unlocked for the developer.

An empty &lt;ID&gt;@review branch is deleted, locally and on the remote. One
carrying your commits is kept for the developer to inspect (git log
&lt;feature&gt;..&lt;ID&gt;@review); until git zf review sync merges them in, new
commits on the feature branch are blocked by the pre-commit hook (bypass:
--no-verify).

The review record is pushed (--push / --no-push skip the proposal), and when
the issue came from a tracker a status update is proposed.

```
git zf review reject [flags]
```

### Options

```
  -F, --file string      read the reason from a file, e.g. a Markdown note (skips the prompt)
  -h, --help             help for reject
  -m, --message string   reason for requesting changes (skips the prompt)
      --no-push          skip the post-action push proposal
      --push             push the resulting branch to the remote after the action (skips the prompt)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

