---
title: "git zf review start"
sidebar_label: "review start"
---

## git zf review start

Begin reviewing an issue (checks out its branch, brought to the remote one)

### Synopsis

Run git fetch first, then pick an issue awaiting review. The branch the
developer submitted is checked out and brought to the remote one: created,
fast-forwarded, or, when your local copy is an old one that does not hold the
submitted commit, reset to it (the commit it was on is printed). Commits of
your own that you have not pushed yet are kept. Your identity is recorded on
the review ref as the reviewer.

Nothing is changed when the command refuses: the developer has not pushed
what they submitted, or the branch must move and the working tree has
uncommitted changes.

Review the code there. You may commit fixes on the branch and push them.
When you are done, run git zf review approve or git zf review reject.

```
git zf review start [flags]
```

### Options

```
  -h, --help   help for start
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

