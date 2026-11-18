# Roadmap

## Enhancement

1. I would like to commit referencing a tracker issue without creating a new branch, directly from the master.
3. AI assistant. See https://github.com/rshdhere/vibecheck
12. Starting an issue in a dedicaced branch, prush the branch and create a PR in the tracker. The PR is accepted from the tracker. How close the issue localy ?
13. Architecture cleanup (from the 2026-11-18 review): untangle the `cmd/*` sibling imports (shared actions move to flow packages, handlers take typed arguments instead of a `*cobra.Command`, then upgrade Cobra and drop `cmdutil.StringFlag`); unify the chain glue of `branch`, `issue` and `review`.

## To be discuss
