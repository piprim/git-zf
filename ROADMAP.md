# Roadmap

## Bug

### Copy/Past of log

Copy/past a log entry detail give this ugly format (from the gt-zf repo ) :
```
 * commit 19b45553cb462549ddd0ed8e357452b6810b7e3b (HEAD -> master, tag: v0.19.1, pi/master)                                                                                                                       │
│ | Author: pivaldi <git@ivaldi.me>                                                                                                                                                                                 │
│ | Date:   Mon Nov 23 15:18:26 2026 +0100                                                                                                                                                                          │
│ |                                                                                                                                                                                                                 │
│ |     build: install to Go bin dir without root                                                                                                                                                                   │
│ |                                                                                                                                                                                                                 │
│ |     Copying to $(git --exec-path) required sudo, running the Go build                                                                                                                                           │
│ |     itself as root. Target GOBIN/GOPATH/bin via go install instead and                                                                                                                                          │
│ |     warn when an older binary in git's exec path or PATH shadows it.                                                                                                                                            │
│ | ---                                                                                                                                                                                                             │
│ | :100644 100644 ea43b50 1249112 M    CLAUDE.md                                                                                                                                                                   │
│ | :100644 100644 0991d81 a52fe39 M    Makefile                                                                                                                                                                    │
│ | :100644 100644 fd07468 6fb8123 M    README.md                                                                                                                                                                   │
│ | :100644 100644 d9b278e 15d1733 M    ROADMAP.md                                                                                                                                                                  │
│ | :000000 100644 0000000 fec42e8 A    docs/superpowers/plans/2026-11-23-rootless-make-install.md                                                                                                                  │
│ | :000000 100644 0000000 f6a890c A    docs/superpowers/specs/2026-11-23-rootless-make-install-design.md                                                                                                           │
│ | :100644 100644 d231076 5ad925f M    website/docs/getting-started/install.md                                                                                                                                     │
│ |  CLAUDE.md                                    |  4 +-                                                                                                                                                           │
│ |  Makefile                                     | 36 ++++---                                                                                                                                                      │
│ |  README.md                                    | 14 +--                                                                                                                                                          │
│ |  ROADMAP.md                                   |  5 -                                                                                                                                                            │
│ |  .../2026-11-23-rootless-make-install.md      | 97 ++++++++++++++++++                                                                                                                                           │
│ |  ...026-11-23-rootless-make-install-design.md | 97 ++++++++++++++++++                                                                                                                                           │
│ |  website/docs/getting-started/install.md      | 23 +++--                                                                                                                                                        │
│ |  7 files changed, 240 insertions(+), 36 deletions(-)     
```


### Issue close workflow
Un problème étrange est survenu dans `/home/pi/code/ovya/cpro`.
Deux paires de migrations similaires se sont retrouvées dans la master :
```txt
Version20260630152122.php
Version20260630154448.php
Version20261103173000.php
Version20261103173100.php
```

Le correctif est dans le commit `f21bb52a7c9c59bd0350c84f1fe8cf753e64548d`.

Voici un échange qui met en lumière une explication possible :
```txt
Ce que fait le close (cmd/issue/close.go:628-664) :
1. Il prend la branche feature locale de la personne qui ferme, 1102229@feat@revoir-les-changelogs chez pi.
2. Chez pi, cette branche est sa version d'origine de juin, 172314517, jamais rebasée. Elle contient les migrations 20260630*.
3. CommitsAhead(feature, @review) > 0, puisque 172314517 n'est pas dans @review. Le close passe donc dans la branche « Diverged ».
4. MergeDryRun ne trouve aucun conflit : les noms de fichiers sont différents.
5. MergeForward(@review → feature locale de pi) fusionne le WIP de juin avec ta branche rebasée, qui contient le renommage. On obtient les 4 migrations, c'est exactement ma simulation merge-tree de tout à l'heure.
6. La stratégie Rebase squashe le tout sur origin/master, ce qui donne 6362977d0.

Le close ne vérifie jamais que la branche locale correspond au feature_sha que tu as soumis à la review. Une branche locale périmée chez le relecteur se fait donc fusionner sans rien dire.

Je n'ai pas accès au clone de pi, donc le point 2 reste à confirmer. Il peut le vérifier avec git log --oneline -3 1102229@feat@revoir-les-changelogs, ou dans son reflog s'il a supprimé la branche.

Correction proposée dans git-zf, juste avant le switch de close.go:628 : si ref.FeatureSHA n'est pas un ancêtre de la branche feature locale, refuser le close. Par exemple : « local branch diverged from the reviewed commit be8297e — reset it to the submitted SHA ». Il faudrait aussi un test e2e : branche locale périmée chez le relecteur, plus un rebase avec renommage côté développeur.

Il fallait que la branche feature locale de pi soit au même commit que ce que tu as soumis (be8297e05) au moment du close. Le close fait alors un simple fast-forward vers @review (le cas m == 0), et seules les deux migrations renommées partent sur master.

Côté pi, avant le git zf issue close :
git switch 1102229@feat@revoir-les-changelogs
git reset --hard be8297e05     # ou origin/1102229@… si ta version rebasée y était poussée
git zf issue close
Il pouvait aussi supprimer sa branche locale et la recréer depuis la tienne :
git branch -D 1102229@feat@revoir-les-changelogs
git switch -c 1102229@feat@revoir-les-changelogs be8297e05

Ou le close fait par toi : ta branche locale était déjà à be8297e05. Une fois les commits de review de pi poussés sur origin/1102229@review, ton close aurait fait un fast-forward propre.

À l'origine du problème : tu as rebasé le 03/11 une branche que pi avait créée et dont il gardait une copie locale. Après un rebase de branche partagée, il faut :
1. pousser avec git push --force-with-lease ;
2. prévenir les autres, qui doivent faire git fetch && git reset --hard origin/<branche>, et surtout pas un git pull, qui refusionnerait l'ancienne version.

8843  git push origin 1102229@feat@revoir-les-changelogs
8844  git push --force-with-lease origin 1102229@feat@revoir-les-changelogs

C'est la séquence classique après un rebase : le push normal est refusé, puis le --force-with-lease passe. ~/.bash_history n'a pas d'horodatage, je ne peux donc pas dater ces lignes exactement. Mais le seul rebase de cette branche dans ton reflog est celui du 03/11 à 17:32, donc elles viennent très probablement de là.

Je corrige ce que j'ai dit : tu as fait ce qu'il fallait. Après ton push, origin avait bien la version rebasée. Le problème vient uniquement de la copie locale de pi, restée à 172314517. Le close de git-zf l'a fusionnée sans vérifier qu'elle correspondait au feature_sha relu (be8297e05).
```


## Enhancement

### TAB in the lists
In all list [TAB] go to next circularly. [Shift Tab] should go to previous.

### New Feature
I would like to commit referencing a tracker issue without creating a new branch, directly from the master.

### AI assistance
AI assistant. See https://github.com/rshdhere/vibecheck

### How close the issue localy ?
Starting an issue in a dedicaced branch, prush the branch and create a PR in the tracker. The PR is accepted from the tracker. How close the issue localy ?

### Architecture cleanup

Architecture cleanup (from the 2026-11-18 review): untangle the `cmd/*` sibling imports (shared actions move to flow packages, handlers take typed arguments instead of a `*cobra.Command`, then upgrade Cobra and drop `cmdutil.StringFlag`); unify the chain glue of `branch`, `issue` and `review`.

## To be discuss
