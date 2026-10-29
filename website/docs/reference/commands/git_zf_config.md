---
title: "git zf config"
sidebar_label: "config"
---

## git zf config

Manage git zf configuration

### Synopsis

The configuration is a TOML file read from two places, the repository one
taking precedence over the home one:

  $HOME/.git-zf.toml            applied everywhere
  &lt;repo&gt;/.git/.git-zf.toml      inside .git/, never committed, may hold a
                                token

Every key is optional: the built-in defaults apply where a file says
nothing. init writes a default file, show prints the effective result.

### Options

```
  -h, --help   help for config
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close
* [git zf config init](git_zf_config_init.md)	 - Write a default config file to home or repo directory
* [git zf config show](git_zf_config_show.md)	 - Show active config file path and effective configuration

