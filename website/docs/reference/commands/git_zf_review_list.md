---
title: "git zf review list"
sidebar_label: "review list"
---

## git zf review list

List all issues currently in review or approved

### Synopsis

Print every open review, "in_review" or "approved", with its round number.
It reads refs/zf/reviews/* and so works on a fresh clone after a git fetch.
A closed review stays in the repository but is not listed.

```
git zf review list [flags]
```

### Options

```
  -h, --help   help for list
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

