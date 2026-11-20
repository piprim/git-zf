# Roadmap

## Bug

### WARN: sync issue, tracker issue not found

On the repo `/home/pi/code/ovya/cpro`, closing an issue.

```
WARN: sync issue 1173941: read tracker issue 1173941: tracker: issue not found
WARN: sync issue 1173940: read tracker issue 1173940: tracker: issue not found
WARN: sync issue 1173916: read tracker issue 1173916: tracker: issue not found
WARN: sync issue 1171164: read tracker issue 1171164: tracker: issue not found
WARN: sync issue 1171145: read tracker issue 1171145: tracker: issue not found
WARN: sync issue 1171144: read tracker issue 1171144: tracker: issue not found
Dozens of warnings…
```


### Problem deleting branch
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

### Do not use sudo to build

No need to be root to build the project installing from source.
Instead of doing `sudo make install`, `make install` should be sufficient.

### List format in request
Launching `git zf review start`, the list of issue is not well formatted.
Add the Title of the issue.

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
