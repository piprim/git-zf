---
title: "git zf issue abandon"
sidebar_label: "issue abandon"
---

## git zf issue abandon

Close an issue without merging anything

### Synopsis

Close an issue stored in the repository without merging anything: for a
duplicate or an issue that will not be worked on. The close is pushed and,
with the issue mirror on, the tracker issue is closed too.

&lt;id&gt; is the full ID of a repository issue or a unique prefix of at least 4
characters; without one a picker lists the repository issues. A tracker
issue that is not in the repository cannot be abandoned.

It is refused while a branch of the issue is in progress: merge it with
git zf issue close &lt;id&gt;, or abandon the branch with
git zf branch close &lt;branch-name&gt;, then abandon the issue. To close an issue
by merging its branch, use git zf issue close.

```
git zf issue abandon [<id>] [flags]
```

### Options

```
  -h, --help   help for abandon
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

