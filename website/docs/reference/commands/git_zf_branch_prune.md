---
title: "git zf branch prune"
sidebar_label: "branch prune"
---

## git zf branch prune

Record branches merged or deleted outside git zf

### Synopsis

Compare each in-progress branch against the local and remote branches:
a local branch already merged into the base is marked merged; a branch gone
locally and on the remote is closed, when you started it. A branch that still
exists on the remote, or that someone else started, is left alone.

A branch someone else started and that exists nowhere may be work they have
not pushed yet. When you know it is abandoned (its author left, or you changed
your git name or email and it is yours), --others closes those too.
'git zf branch track' on a closed branch reopens it.

Use --dry-run to preview, --yes to skip the confirm prompt.

```
git zf branch prune [flags]
```

### Options

```
      --base string   base branch for merge detection (default: auto-detected)
      --dry-run       show what would be pruned without executing
  -h, --help          help for prune
      --others        also close branches gone everywhere that someone else started
  -y, --yes           skip the confirmation prompt (CI-friendly)
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf branch](git_zf_branch.md)	 - Manage local branches

