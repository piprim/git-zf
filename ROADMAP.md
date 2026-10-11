# Roadmap

## Bug

### Create issue

Create an issue "Test" in the repo:
```txt
>  git zf issue new
Created issue 1d05aac: Test
```
next, the issues listing with `git zf issue list` does not show the issue "Test":
```txt
>  git zf issue list
 Issue ID    Project             Title                         Branch                                  Local Status  Issue Status    Created
 1168983     CPRO                [formation] SQLSTATE[22001]…  ∅                                       ∅             À mettre en p…  2026-10-05
 1149831     OVYA                two                           ∅                                       ∅             Revu et Testé   2026-06-24
 1149830     OVYA                one                           ∅                                       ∅             Revu et Testé   2026-06-24
 1149829     OVYA                big                           ∅                                       ∅             À mettre en p…  2026-06-24
 1102229     CPRO                Revoir les changelogs         ∅                                       ∅             En Cours de t…  2025-11-06
```

The project has a tracker but mirror is off.

### Listing issues are not consistent
Listing issues whit tracker in a non mirrored gives only issues affected to me:
```txt
>  git zf issue list
 Issue ID    Project             Title                         Branch                                  Local Status  Issue Status    Created    
 1168983     CPRO                [formation] SQLSTATE[22001]…  ∅                                       ∅             À mettre en p…  2026-10-05 
 1149831     OVYA                two                           ∅                                       ∅             Revu et Testé   2026-06-24 
 1149830     OVYA                one                           ∅                                       ∅             Revu et Testé   2026-06-24 
 1149829     OVYA                big                           ∅                                       ∅             À mettre en p…  2026-06-24 
 1102229     CPRO                Revoir les changelogs         ∅                                       ∅             En Cours de t…  2025-11-06 
```

The same project mirrored gives:
```txt
 Issue ID    Title                         Branch                                  Local Status  Issue Status    Created    
 1149835     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-06-24 
 1143012     Utilisation de personne_vie…  ∅                                       ∅             En Cours de t…  2026-06-03 
 1142281     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-06-02 
 1139632     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-05-05 
 1139571     Loup - Développement nouvel…  ∅                                       ∅             Attend Réponse  2026-04-30 
 1139370     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-04-23 
 1139219     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-04-15 
 1139128     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-04-13 
 1138929     Optimisation recherche ACQ …  ∅                                       ∅             En Cours de t…  2026-04-02 
 1138728     [UBIFLOW] Gestion de la dif…  ∅                                       ∅             Revu et Testé   2026-03-27 
 1138665     Intégration VVL à toutes le…  ∅                                       ∅             En Cours de t…  2026-03-24 
 1138368     Gestion de la rente  [En co…  ∅                                       ∅             En conception   2026-03-12 
 1138367     Gestion de la vente details…  ∅                                       ∅             Suspendu        2026-03-12 
 1138366     Gestion de la vente  [En Co…  ∅                                       ∅             En Cours de t…  2026-03-12 
 1137564     [production] [Questions ou …  ∅                                       ∅             Nouveau         2026-03-03 
 1137265     Implémentation api ubiflow …  ∅                                       ∅             À Tester / Re…  2026-02-16 
 1102229     Revoir les changelogs [En C…  ∅                                       ∅             En Cours de t…  2025-11-06 
 1087408     Repasse du front pour evite…  ∅                                       ∅             Nouveau         2025-09-04 
 1078966     Traitement des bounces cour…  ∅                                       ∅             À mettre en p…  2025-03-31 
 …
 …
```


## Enhancement

1. Issue mirror, repo <-> tracker (first version shipped). Spec: docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md. Left out of its first version:
  a. [abandoned] ~Several projects: project-aware tracker calls, project-qualified slugs, a project picker in `issue new`.~
  b. [done] Sync of title, description, labels and comments. Spec: docs/superpowers/specs/2026-10-10-issue-mirror-content-sync-design.md
  c. `issue start` picker reading the mirror instead of the live "assigned to me" listing.
  d. A warning from the reconcile when two records link the same tracker number (one clone exported while another imported the new issue before fetching).
2. In-repo issue must carry on author and assignee: Who opened it and who works on it, from git identity. Lets `issue list` filter 'mine'. Comes after the issue mirror.
3. AI assistant. See https://github.com/rshdhere/vibecheck
4. git zf branch close <branch-name>
   No menu entry or picker. The command takes exactly one name and is not in the git zf branch menu. A picker over in-progress branches is a small addition
5. `git zf issue new` should propose to start the issue now (yes/no).
6. `git zf issue list` show the issues. Allow to pick one and open a view the same `git zf shwo <id>`

## To be discuss

Add the trackers for “Under development”, “To be reviewed/tested”, “Under review”, “Test/review” to the redimne conf to automate the code review workflow.
Is it possible to do the same with Forgejo with labels or projects ?
