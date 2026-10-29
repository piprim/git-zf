---
title: "git zf issue comment"
sidebar_label: "issue comment"
---

## git zf issue comment

Comment on an issue stored in the repository

### Synopsis

Add a comment to an issue stored in the repository and push it. &lt;id&gt; is
the full ID or a unique prefix of at least 4 characters. Without &lt;id&gt; a picker
lists the issues. --message skips the comment form.

```
git zf issue comment [<id>] [flags]
```

### Options

```
  -h, --help             help for comment
  -m, --message string   comment text (skips the form)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

