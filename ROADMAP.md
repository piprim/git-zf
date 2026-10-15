# Roadmap

## Enhancement

1. Issue mirror, repo <-> tracker (first version shipped). Spec: docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md. Left out of its first version:
  c. `issue start` should assign to me if it's not already assigned to me.
  d. A warning from the reconcile when two records link the same tracker number (one clone exported while another imported the new issue before fetching).
2. In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine'. Comes after the issue mirror.
3. AI assistant. See https://github.com/rshdhere/vibecheck
4. git zf branch close <branch-name>
   No menu entry or picker. The command takes exactly one name and is not in the git zf branch menu. A picker over in-progress branches is a small addition
5. `git zf issue new` should propose to start the issue now (yes/no).
6. `git zf issue list` show the issues. Allow to pick one and open a view the same `git zf shwo <id>`
7. In a submodule:
```txt
>  git branch
  master
* scaffold

pi@pi-machine mmw cpro/development  scaffold ending at 09:41:09
>  git zf branch merge
┃ Branch to merge into current:
┃ > master
┃   14-middleware-authorization (origin)
```

The branch `14-middleware-authorization` is in the root git directory not in the submodule.

And
```txt
>  git zf branch list
No branches found.
```
8. Stupid functionality:
Launching `git zf issue` and pressing two time on `c` to close the issue 22 output an error :
`error: issue 22 has a branch in progress (22@feat@mmw-module-scaffolding): merge it with `git zf issue close`, or abandon it with `git zf branch close <branch-name>`, then close the issue`
`git zf issue close` is what I want to do from the issue list.
9. `git zf issue close` leave submodule uncomitted:
```
>  git zf issue close
Fetching submodule libs/boa
Fetching submodule libs/ogl
Fetching submodule libs/presence
Fetching submodule mmw
Fetching submodule scripts/libs/bash-stepper
Fetching submodule tools/bungen
Merge made by the 'ort' strategy.
 .gitignore  |  1 +
 go.work.sum |  2 ++
 todo.md     | 10 ++++++----
 3 files changed, 9 insertions(+), 4 deletions(-)
[buf:lint] Linting proto file
[buf:lint] mise lint
[arch:check] 
[arch:check] ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
[arch:check] 
[arch:check] Architecture Validation
…
[pre-commit] [lint] skipped (no Go files in selection)
[pre-commit] pre-commit: all checks passed
[22@feat@mmw-module-scaffolding 6c251ea] feat(22): [close] mmw module scaffolding
 6 files changed, 7369 insertions(+), 8 deletions(-)
 create mode 100644 docs/superpowers/plans/2026-10-13-mmw-new-module.md
 create mode 100644 docs/superpowers/plans/2026-10-14-mmw-module-shapes.md
 create mode 100644 docs/superpowers/specs/2026-10-14-mmw-module-shapes-design.md
Updating 8484f32..6c251ea
Fast-forward
 docs/superpowers/plans/2026-10-13-mmw-new-module.md              | 4728 +++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++
 docs/superpowers/plans/2026-10-14-mmw-module-shapes.md           | 2272 +++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++
 docs/superpowers/specs/2026-10-14-mmw-module-shapes-design.md    |  244 ++++++++
 mmw                                                              |    2 +-
 stow/common/.claude/skills/hexago-ddd/SKILL.md                   |   50 +-
 stow/common/.claude/skills/hexago-ddd/references/slice-recipe.md |   81 +++
 6 files changed, 7369 insertions(+), 8 deletions(-)
 create mode 100644 docs/superpowers/plans/2026-10-13-mmw-new-module.md
 create mode 100644 docs/superpowers/plans/2026-10-14-mmw-module-shapes.md
 create mode 100644 docs/superpowers/specs/2026-10-14-mmw-module-shapes-design.md
warning: worktree remove /home/pi/code/ovya/cpro2/.worktrees/22@feat@mmw-module-scaffolding: exit status 128: fatal: working trees containing submodules cannot be moved or removed
Remove it manually with: git worktree remove --force "/home/pi/code/ovya/cpro2/.worktrees/22@feat@mmw-module-scaffolding"
warning: branch "22@feat@mmw-module-scaffolding" is still checked out in its worktree; delete it after `git worktree remove`
To ssh://gitovya/rcv/cpro.git
 - [deleted]         22@feat@mmw-module-scaffolding
Branch "22@feat@mmw-module-scaffolding" merged into "master" and closed.
Push "master" to origin — 8484f32..6c251ea
Enumerating objects: 30, done.
Counting objects: 100% (30/30), done.
Delta compression using up to 22 threads
Compressing objects: 100% (15/15), done.
Writing objects: 100% (17/17), 73.72 KiB | 12.29 MiB/s, done.
Total 17 (delta 4), reused 0 (delta 0), pack-reused 0 (from 0)
To ssh://gitovya/rcv/cpro.git
   8484f32..6c251ea  master -> master
Pushed "master" to origin.

pi@pi-machine cpro2 cpro/development  master [!] with  v1.64.0ending at 10:07:39 took 2m 
>  git s
On branch master
Your branch is up to date with 'origin/master'.

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   mmw (new commits)

Submodules changed but not updated:

* mmw 7ad47d3...4e4e5da (1):
  < feat(scaffold): database and grpc flags, gateway slice, in-proc modules

no changes added to commit (use "git add" and/or "git commit -a")
```
10. Creating a git worktree does not update submodules
11. Closing an issue developped in a worktree with submodule, removing the worktree failed with:
    `fatal: working trees containing submodules cannot be moved or removed`
12. Starting an issue in a dedicaced branch, prush the branch and create a PR in the tracker. The PR is accepted from the tracker. How close the issue localy ?

## To be discuss

