---
title: "git zf install"
sidebar_label: "install"
---

## git zf install

Install this tool to git-core as zf

### Synopsis

Copy this binary into git's exec path (git --exec-path), so that git
knows it as "git zf". Run it with sudo when that directory is not writable
by you; a Homebrew git on macOS does not need it.

Check with git zf version. Then run git zf init once per repository to
install the hooks. git zf uninstall removes the binary.

```
git zf install [flags]
```

### Options

```
  -h, --help   help for install
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close

