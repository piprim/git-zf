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

- Clossing an issue, the subject should be Closes #XXX : the ticket subject
- Add option in `git zf commit` to add untracked files.
- On `git zf commit` a the list of file to be commited (like `git status`)
- Ajouter dans la conf de redimne les tracker pour "En cours de dev", "À Revue/tester", "En cours de revue", "Tester/revue" pour automatiser le wf de revue de code.
