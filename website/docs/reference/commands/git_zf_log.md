---
title: "git zf log"
sidebar_label: "log"
---

## git zf log

Browse the commit graph; enter shows a commit

### Synopsis

Browse the commit graph of every branch and tag, without git-zf's own refs.
Enter opens the commit under the cursor with its stat and patch; esc returns.
L on a merge at the tip of a local branch replaces it by one commit holding its
tree (the branch stays bit-identical but linear), after fetching: it is refused
when the upstream moved, and the push that follows is offered with a lease on
what was fetched. The old tip stays in the reflog.
1-9 switch to the command with that key, c picks one by name; the commands and
the default are configured under [log] (see git zf config show).
Arguments after -- go to git log, e.g. a revision range or -n 100.
Without a terminal the default command's output is printed like git log.

```
git zf log [-- <git log arguments>] [flags]
```

### Options

```
  -h, --help   help for log
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close

