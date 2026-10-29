---
title: "git zf issue sync"
sidebar_label: "issue sync"
---

## git zf issue sync

Fetch, merge and push the issues stored in the repository

### Synopsis

Fetch refs/zf/issues/* from the remote, merge issues that were changed on
both sides, push back an issue whose chain the remote replaced by an unrelated
one, and push the issues the remote does not have yet. Without a remote
there is nothing to do. With issue-tracker.mirror on, it then mirrors the
issues with the tracker project: imports, exports, and open/closed both ways.

```
git zf issue sync [flags]
```

### Options

```
  -h, --help   help for sync
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf issue](git_zf_issue.md)	 - Manage issues

