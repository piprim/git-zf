---
title: "git zf issue edit"
sidebar_label: "issue edit"
---

## git zf issue edit

Edit the title and description of an issue stored in the repository

### Synopsis

Change the title and the description of an issue stored in the repository
and push it. &lt;id&gt; is the full ID or a unique prefix of at least 4 characters.
Without &lt;id&gt; a picker lists the issues.

--title and --description skip the form and change only the field passed;
--description "" clears the description. A branch already started for the
issue keeps its name.

```
git zf issue edit [<id>] [flags]
```

### Options

```
      --description string   new description (skips the form)
  -h, --help                 help for edit
      --title string         new title (skips the form)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

