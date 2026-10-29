---
title: "git zf commit"
sidebar_label: "commit"
---

## git zf commit

Record changes to the repository

### Synopsis

Compose a commit message in a form (type, scope, subject, body, footer)
followed by an options page (stage all, amend, sign-off, hooks, push), then
commit. Every option has a flag; if any commit flag is passed the options
page is skipped and the flags are used directly.

On an issue branch the form is pre-filled from the branch name: the type,
the issue ID as the scope, and the footer from ref-format. Every field stays
editable.

On a feature branch whose review left reviewer commits on &lt;IssueID&gt;@review
that are not yet in the branch, the command offers to merge them in before
opening the form; declining aborts with a hint to run git zf review sync.
--no-verify skips the guard, as it does the pre-commit hook.

A push is proposed afterwards (--push / --no-push answer it), and after a
push of a feature branch the forge may be offered a pull request.

```
git zf commit [flags]
```

### Options

```
  -a, --all                 stage all tracked modified/deleted files before committing
      --allow-empty         allow a commit with no changes
      --amend               replace the tip of the current branch
      --author string       override commit author as "Name <email>"
  -h, --help                help for commit
  -u, --include-untracked   stage untracked files before committing
      --no-push             skip the post-action push proposal
  -n, --no-verify           bypass pre-commit and commit-msg hooks
      --push                push the resulting branch to the remote after the action (skips the prompt)
  -s, --signoff             add Signed-off-by trailer to the commit message
  -y, --yes                 skip the commit options form and assume defaults
```

### Options inherited from parent commands

```
  -d, --debug   debug mode, print debug info to stdout
```

### SEE ALSO

* [git zf](git_zf.md)	 - Git Zen workFlow: issue → branch → commit → review → close

