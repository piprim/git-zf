# Roadmap

## Question

- Are there cases during a review where using git-zf on one side and not the other can cause problems?

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

