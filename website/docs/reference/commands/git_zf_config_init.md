---
title: "git zf config init"
sidebar_label: "config init"
---

## git zf config init

Write a default config file to home or repo directory

### Synopsis

Write the built-in default configuration to a file, to edit from there.

Outside a repository, or when no home file exists yet, it goes to
$HOME/.git-zf.toml. Otherwise a picker offers the home file or
&lt;repo&gt;/.git/.git-zf.toml. An existing file is overwritten only after
confirmation.

```
git zf config init [flags]
```

### Options

```
  -h, --help   help for init
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf config](git_zf_config.md)	 - Manage git zf configuration

