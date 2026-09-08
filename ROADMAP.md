# Roadmap

## Enhancement

- `git zf review list` does not show a "review request" (to be confirmed)
- `git zf review reject` should permit to add a md file or a comment for explanation.
- `git zf issue close` could suggest deleting the folder created by the git worktree if it make sens.
- `git zf review status` hangs when there isn't any review.
- Creating an issue from a external issues tracker, add an entry to create a fresh issue to the tracker.
- If the terminal is a little small, during the commit, if a file name is long, we only see part of the input form on the left.
- *Merge-vs-parent preview* on commit — one of:
  - `Fast-forwards into <parent>` — current is strictly ahead of the parent.
  - `Merges into <parent> with a merge commit (no conflicts)` — diverged but
    clean (`MergeDryRun` returns no conflicts).
  - `⚠ Conflicts with <parent>: <files…>` — `MergeDryRun` reports conflicts.
  - `Already merged into <parent>` — current is an ancestor of the parent
    (nothing to merge); shown for information.
- One caveat to flag (downstream of the scope you chose)
  On a fresh clone the parent isn't in Bob's store, so ParentIssueSlug stays empty — the parent relation isn't recorded, and Bob's
  refs/zf/branches/1149831 ref gets ParentSlug="". The branch is now correctly cut from the parent (the reported bug), but at demo
  phase 10 Bob's issue close of X.2 would resolve its merge target to main instead of the parent, because there's no parent link to
  follow.
  Closing that needs one more small change you scoped out: when the picked base parses as a git-zf branch
  (branch.Parse("1149829@feat@big") → 1149829), record it as the parent even when the store misses. It's a few lines in the same
  picker block.
- Delete the remote branches deleting the local branches on `issue close` for example.

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.

## To be discuss

- Add the trackers for “Under development”, “To be reviewed/tested”, “Under review”, “Test/review” to the redimne conf to automate the code review workflow.
