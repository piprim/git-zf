# Roadmap

## Bug

### Problem deleting branch
In CPro2: /home/pi/code/ovya/cpro2
List of branches :
0. Branch                                  Issue       Type      Status        Title                         Created     Last comm… 
   36@feat@gestion-des-secrets           36          feat      merged        Gestion des secrets           2026-11-10  6d ago     
   f27-sessions-glissantes                                                                                             1w ago     
 * master                                                                                                              4h ago

- Deleting 36@feat@gestion-des-secrets and f27-sessions-glissantes  does not delete the branch
- `git zf` says that 2 branch in progress
```
>  git zf
git-zf  master
        1 untracked · ↑0 ↓0 origin

  c  Commit
> i  Issue      6 open
  b  Branch     2 in progress
  v  Review
  l  Log
```
But issue display no branch/inssue in progress:
```
>  git zf issue
 Issue ID    Title                         Branch                                  Local Status  Issue Status    Assigned  Created    
 41          Ajouter un système d'anti-b…  ∅                                       ∅             open            rva       2026-11-16 
 40          Generate Caddy files [Kind/…  ∅                                       ∅             open            PI        2026-11-16 
 32          Gestion des vendeurs [Kind/…  ∅                                       ∅             open            rva       2026-11-05 
 30          Mettre en place l'authentif…  ∅                                       ∅             open            rva       2026-11-05 
 29          Agenda Google [Kind/Feature…  ∅                                       ∅             open                      2026-11-05 
 21          Better management of intern…  ∅                                       ∅             open                      2026-10-14 
```

### Clean UX full screen

Git-zf is not full screen…
Starting from `git zf` at the bottom of the screen, pressing `i`, `ESC`, `b` and `ESC`, the screen becomes like this :
```
[ Open ]  Closed  All  Mine    [tab]-Status  [m]ine  [/]-Filter
 Issue ID    Title                         Branch                                  Local Status  Issue Status    Assigned  Created    
 41          Ajouter un système d'anti-b…  ∅                                       ∅             open            rva       2026-11-16 
 40          Generate Caddy files [Kind/…  ∅                                       ∅             open            PI        2026-11-16 
 32          Gestion des vendeurs [Kind/…  ∅                                       ∅             open            rva       2026-11-05 
 30          Mettre en place l'authentif…  ∅                                       ∅             open            rva       2026-11-05 
 29          Agenda Google [Kind/Feature…  ∅                                       ∅             open                      2026-11-05 
 21          Better management of intern…  ∅                                       ∅             open                      2026-10-14 
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      
                                                                                                                                      

[ Open ]  Closed  All  Mine    [tab]-Status  [m]ine  [/]-Filter
 Branch                                  Issue       Type      Status        Title                         Created     Last comm… 
   36@feat@gestion-des-secrets           36          feat      merged        Gestion des secrets           2026-11-10  6d ago     
   f27-sessions-glissantes                                                                                             1w ago     
 * master                                                                                                              4h ago     
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  
                                                                                                                                  

[S]tatus  [T]ype  [R]emotes  [/]-Filter
git-zf  master
        1 untracked · ↑0 ↓0 origin

  c  Commit
  i  Issue      6 open
> b  Branch     2 in progress
  v  Review
  l  Log

[↑/↓]-Move  [enter]-Run  [s]tatus  [r]efresh  [q]uit
```

It's ugly !

Pressing `s` and `ESC` the menu is at the top of the screen ; baffling

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
