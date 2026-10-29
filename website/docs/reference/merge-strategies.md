---
sidebar_position: 3
title: Merge strategies
---

`git zf issue close` and `git zf branch merge` offer three ways to land a
branch. All three compose the final commit through the commitizen form.

| Strategy | Mechanism | History on base | Submodule-safe |
|---|---|---|---|
| **Rebase** *(default)* | Real `git merge <remote>/<base>` + `git reset --soft`, one commit | one clean commit | ✅ yes |
| **Squash** | `git merge --squash` | one commit, no merge parent | ⚠️ no: `--squash` mishandles submodule gitlinks |
| **Classic** | `git merge --no-ff --no-commit`, local base FF-synced against `<remote>/<base>` first | merge commit + full feature history | ✅ yes |

- **Rebase**: your repo has submodules, or you want a clean linear history
  with one commit per issue. The recommended default. Any failure before the
  commit lands (form abort, hook rejection, signing failure) rolls the
  feature branch back to its original tip.
- **Squash**: no submodules involved and you want plain `git merge --squash`
  semantics, with fewer git operations.
- **Classic**: you want the feature's full commit history on the base branch
  behind a merge commit, for large features, a bisect surface or an audit
  trail. It refuses to merge into a local base that has diverged from the
  remote: run `git pull --ff-only` and retry. A failure before the commit
  runs `git merge --abort` automatically.

After Squash and Rebase the feature branch is deleted with `git branch -D`,
since neither preserves ancestry; Classic uses `git branch -d`.

The mechanics, the rollback rules and the reason Rebase is not `git rebase`
are in [merge engine internals](../under-the-hood/merge-engine.md).
