# Roadmap

## Enhancement

- Actually before `git zf review start` the reviewer must do a `git fetch` manually. Make the `git zf review start` auto fetch 
- `git zf review reject` should permit to add a md file or a comment for explanation
- **Reviewer-initiated close**: allow a reviewer (or any team member) to close
  an issue they didn't start. Currently `issue close` requires the branch to be
  in the local store; a reviewer's clone has no entry unless they run `git zf
  issue track` first. The fix is to fall back to `refs/zf/branches/*` (already
  fetched by the close flow) when the store has no matching in-progress row the
  same cross-machine fallback already used for parent-slug resolution.
- Closing an issue should propose to push the branch on which the feature branch was merged (parent branch) with a dry-run.
  Same feature for `git zf commit` with the current branch (dry-run should print information about FF-merge and possible conflict with the parent branch).
  Check if this feature make sense for `review request` `review reject` and `review approve`.
- *Merge-vs-parent preview* on commit — one of:
  - `Fast-forwards into <parent>` — current is strictly ahead of the parent.
  - `Merges into <parent> with a merge commit (no conflicts)` — diverged but
    clean (`MergeDryRun` returns no conflicts).
  - `⚠ Conflicts with <parent>: <files…>` — `MergeDryRun` reports conflicts.
  - `Already merged into <parent>` — current is an ancestor of the parent
    (nothing to merge); shown for information.

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.
