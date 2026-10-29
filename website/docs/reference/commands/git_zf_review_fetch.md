---
title: "git zf review fetch"
sidebar_label: "review fetch"
---

## git zf review fetch

Sync review refs with the remote: fetch, merge, and push the ones it lacks

### Synopsis

Fetch the review chains from the remote, merge them with the local ones,
and push the ones the remote lacks. Nothing is pruned locally.

The interactive review commands sync on their own; run this before scripting
around review state.

```
git zf review fetch [flags]
```

### Options

```
  -h, --help   help for fetch
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf review](git_zf_review.md)	 - Manage the code review lifecycle for an issue branch

