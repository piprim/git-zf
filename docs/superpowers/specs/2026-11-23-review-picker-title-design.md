# Review pickers show the issue title

## Intent

`git zf review start` lists the issues awaiting review as
`[42] 42 (42@review)`: the slug three times, no title. A reviewer cannot tell
the issues apart. Each line must show the issue's title.

## Cause

The review chain stores no title. `reviewBranches` (`cmd/review/deps.go`)
builds synthetic `branch.Row`s with `Title: st.Slug`, and `tui.BranchPicker`
prints `[slug] title (branch)`.

## Goals

- `review start` shows `[42] Add login form (42@review)`.
- `review approve` and `review reject` use the same rows and get the same fix.
- `review status` uses the same rows, then replaces the row of a tracked issue
  by its feature branch row (`status_cmd.go`): it gains the title for an issue
  whose chain was not fetched yet, and is otherwise unchanged.

## Non-goals

- Changing the `[slug] title (branch)` layout of `tui.BranchPicker`.
- `review request` and `review sync`: they use real branch rows, already fine.
- No new op field, no tracker call.

## Approaches

1. **Read the title from the branch chain (picked).** The title already lives
   in `refs/zf/branches/<slug>` (`branch.State.Title`), which the developer
   pushes. No format change, no network call beyond the fetch of the chains.
2. Store the title in the review chain. A new op field, and the reviews that
   exist today would still have none.
3. Ask the tracker. A network call for data the repository already holds.

## Design

In `reviewBranches`:

1. Only when at least one review is kept: `fetchBranchRefs(ctx, deps)`
   (best-effort, as `request` and `sync` do), then one `branch.List`. A `List`
   error is a warning on Err; the rows are still built, with the slug as the
   title. `List`'s own warnings go to Err.
2. For each review kept, `Title` is the first non-empty of: the chain's
   `Title`, `branch.TitleFromName` of the chain's first entry, the slug.

## Tests

One E2E test in `cmd/review/review_e2e_test.go`, with `t.Run` per case:

- a branch chain seeded with `branchtest.Seed` gives the row its title;
- a review with no branch chain keeps the slug as the title.

## Left to the builder

The helper's shape (inline map of slug to title, or a small function).
