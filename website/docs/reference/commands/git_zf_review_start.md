---
title: "git zf review start"
sidebar_label: "review start"
---

## git zf review start

Begin reviewing an issue (creates &lt;IssueID&gt;@review branch from the locked snapshot)

### Synopsis

Run git fetch first, then pick an issue awaiting review. A branch named
&lt;IssueID&gt;@review is created and checked out at the commit captured when the
developer requested the review, and your identity is recorded on the review
ref as the reviewer.

Review the code there. You may commit fixes on the @review branch and push
them: the developer incorporates them with git zf review sync, or git zf
issue close does it at the end. When you are done, run git zf review approve
or git zf review reject.

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

