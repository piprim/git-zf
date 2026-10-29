---
title: "git zf issue start"
sidebar_label: "issue start"
---

## git zf issue start

Start work on an issue (create branch)

### Synopsis

Start the issue &lt;id&gt;: a properly named branch is created and checked
out from the default base branch. &lt;id&gt; is the full ID of an issue stored in
the repository, a unique prefix of at least 4 characters, or the number of
the tracker issue. Without &lt;id&gt; the issue list opens, as `git zf issue`
does: s starts the issue under the cursor, n files a new one. The branch is
recorded in the repository (refs/zf/branches/&lt;issue&gt;) and pushed, so every
clone sees it.

```
git zf issue start [<id>] [flags]
```

### Options

```
  -h, --help             help for start
      --parent string    parent issue slug — creates this as a sub-task branching from the parent integration branch
      --variant string   create a parallel branch for the same issue (e.g. --variant=spike)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

