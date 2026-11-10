---
title: "git zf branch list"
sidebar_label: "branch list"
---

## git zf branch list

List branches

### Synopsis

List the tracked branches with their status: yours and the ones other
clones started. It does not contact the remote: it shows what the last git
fetch brought. In a terminal it opens the branch table of git zf branch.

--status preselects the status filter (in_progress, merged, closed or all);
--stdout and --json print the tracked branches only, as a table or as rows.

```
git zf branch list [flags]
```

### Options

```
  -h, --help            help for list
      --json            print JSON array to stdout
      --status string   filter by status: in_progress, merged, closed, all
      --stdout          print table to stdout without TUI
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

