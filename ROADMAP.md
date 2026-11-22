# Roadmap

## Bug

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
