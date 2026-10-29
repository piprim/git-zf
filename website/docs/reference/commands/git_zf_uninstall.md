---
title: "git zf uninstall"
sidebar_label: "uninstall"
---

## git zf uninstall

uninstall zf from git-core

### Synopsis

Remove the binary from git's exec path (git --exec-path), the one git zf
install put there. Run it with sudo when that directory is not writable by
you. The hooks installed by git zf init are left in place.

```
git zf uninstall [flags]
```

### Options

```
  -h, --help   help for uninstall
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close

