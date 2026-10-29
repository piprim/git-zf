---
title: "git zf issue label"
sidebar_label: "issue label"
---

## git zf issue label

Add or remove labels on an issue stored in the repository

### Synopsis

Add (+name) or remove (-name) labels on an issue stored in the repository
and push it:

    git zf issue label 1a2b3c4 +bug -wontfix

&lt;id&gt; is the full ID or a unique prefix of at least 4 characters. Without
arguments a picker lists the issues, then a form lists the issue's labels to
untick, plus a line for new ones. With the issue mirror on, the list holds the
tracker's labels too; on Redmine the one label is the status, picked among the
tracker's statuses. "issue new" ends with the same form.

```
git zf issue label [<id> [+add|-remove]...] [flags]
```

### Options

```
  -h, --help   help for label
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

