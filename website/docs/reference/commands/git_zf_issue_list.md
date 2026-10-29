---
title: "git zf issue list"
sidebar_label: "issue list"
---

## git zf issue list

List issues

### Synopsis

List the issues with their branch data in a table. With a tracker
configured the tracker is the primary source; otherwise the rows are the
issues stored in the repository plus the tracked branches. No sync: r syncs.

Columns: Issue ID, [Project], Title, Branch, Local Status, Issue Status,
Created. Labels follow the title in brackets. "∅" means no branch started
yet; "N.A." means the row has neither a tracker nor a repository issue.

Keys: enter shows the issue, e edits it, s starts it, n files a new one,
c twice closes the one under the cursor (as git zf issue close &lt;id&gt;: the
first press asks for the second, there is no undo), l edits its labels,
r syncs, / filters the rows, tab cycles open/closed/all, p picks the
project, m keeps the issues assigned to you, q quits.

--stdout prints the table without the TUI, --json prints the rows.

```
git zf issue list [flags]
```

### Options

```
  -h, --help            help for list
      --json            print JSON array to stdout
      --status string   filter by status: open, closed, all
      --stdout          print table to stdout without TUI
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

