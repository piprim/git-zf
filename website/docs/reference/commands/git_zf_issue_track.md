---
title: "git zf issue track"
sidebar_label: "issue track"
---

## git zf issue track

Track the current branch with git zf (for branches created with plain git checkout)

### Synopsis

Register the current branch without creating a new branch: a feature branch
is recorded as in progress, a review branch records you as the reviewer.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start' or 'git zf review start'.

  Developer (feature branch):  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
                                git zf review track
                                git zf review request

  Reviewer (review branch):    git checkout -b X.1@review &lt;sha&gt;
                                git zf review track
                                git zf review approve

```
git zf issue track [flags]
```

### Options

```
  -h, --help   help for track
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

