# Squash Commit via Pre-Filled `tui.commit` Form

**Date:** 2026-05-12
**Status:** Design — pending implementation plan

## Context

When `git zf issue close` performs a squash merge, the final commit is created non-interactively with `git commit -m "squash merge <sha> into <sha>"`. This bypasses the project's commitizen-style message convention enforced by the `tui.commit` form — the same form `git zf commit` uses for every other commit in the repository.

The result is two classes of commits in the history: regular conventional-commit messages everywhere, except squash-close commits, which break the pattern.

This change routes the squash close through the existing `tui.commit` form with pre-filled values so the operator can review and edit before the commit lands, and the resulting commit reads as a normal conventional commit.

## Goals

- Squash-close commits follow the commitizen template just like `git zf commit` does.
- The form opens pre-filled with type, scope, subject, and author so the operator can press Enter for the common case without typing anything.
- `git/` package stays focused on git mechanics; `commit/` keeps the form orchestration.

## Non-goals

- Changing `MergeNoFF` or the no-ff path.
- Changing `git zf commit` behaviour (its call site passes the same data through the refactored APIs).
- End-to-end interactive form tests; the existing huh form chain has no mocking scaffold and inventing one is its own work item.

## Final commit-message shape

With branch `1138611@fix@production-mkdir-file-exists@<id>` squash-merged into base whose tip is `def5678`, the form opens with:

| Field | Pre-filled value |
|---|---|
| `type` | `fix` (from branch `Type`) |
| `scope` | `1138611` (from branch `IssueSlug`, via the issue-hint fallback chain) |
| `subject` | `Squashed merge of abc1234 into def5678.` (where `abc1234` = branch tip) |
| `body` | empty |
| `footer` | empty |
| author | current git-config identity (operator can change in the form) |

Rendered through the configured template:

```
fix(1138611): Squashed merge of abc1234 into def5678.
```

## Architecture

### New call sequence in `cmd/issue/close.go` (squash path)

```
pickedBranch     ← getPickedBranch         (unchanged)
conflicts        ← MergeDryRun              (unchanged, merge-tree based)
squash strategy  ← IssueMergeStrategy form  (unchanged)
                  [REMOVED: pickSquashAuthor]
confirmed        ← IssueMergeConfirm form   (no longer shows author)
branchSHA       ← c.repo.Reference("refs/heads/" + branchName)
baseSHA         ← c.repo.Reference("refs/heads/" + baseBranch)
                  c.MergeSquash(ctx, branchName, baseBranch)   ← staged only
hint             := IssueHint{IssueID: picked.IssueSlug, BranchType: picked.Type}
prefill          := hint.Prefill(cfg.CommitMessage.Items)
prefill["subject"] = "Squashed merge of <bsha[:7]> into <basesha[:7]>."
authors          := client.Authors()
defaults         := tui.CommitOption{Authors: authors, Author: authors[0]}
msg, opts, err   := commitpkg.FillOutForm(ctx, cfg, defaults, store, prefill)
                    client.Commit(ctx, msg, CommitOptions{...opts})
updateStatus, doDeleteBranch (unchanged)
```

`MergeNoFF` path is untouched.

### Layering

- `git/merge.go`'s `MergeSquash` stops after `merge --squash` — leaves staged changes, does not commit. Final commit is the caller's responsibility.
- `commit/form.go`'s `FillOutForm` loses its `hint` parameter and becomes a pure "show a form, return a rendered commit message" function. Issue-aware behaviour is opt-in via a helper.
- `IssueHint` struct stays in `commit/form.go` but its single method becomes `Prefill(items) map[string]any` — callers compose its output into their own prefill before passing to `FillOutForm`.

## File-level changes

### `git/merge.go`

`MergeSquash` signature:

```go
// before
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch, author string) error

// after
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch string) error
```

Body: keeps the `checkout baseBranch` and `merge --squash branchName` steps. Drops the SHA-resolution block (moved to caller), the `commit` step, the `commitArgs`/`author` handling, and the `plumbing` import if no longer used.

### `commit/form.go`

`FillOutForm` signature:

```go
// before
func FillOutForm(ctx, cfg, defaults, hint IssueHint, hs historyStore) ([]byte, tui.CommitOption, error)

// after
func FillOutForm(ctx, cfg, defaults, hs historyStore, initialPrefill map[string]any) ([]byte, tui.CommitOption, error)
```

Internal change: `var prefill map[string]any` → `prefill := initialPrefill`. The `loadForm` invocation loses its `hint` argument.

`loadForm` signature:

```go
// before
func loadForm(cfg, defaults, hint IssueHint, prefill map[string]any) (...)

// after
func loadForm(cfg, defaults, prefill map[string]any) (...)
```

Body: remove the `applyIssueHint(items, hint)` call and the `isValidCommitType(cfg.CommitTypes, hint.BranchType)` block. Items are now seeded only by `applyPayload(items, prefill)`; the `prefill["type"]` validation block immediately below already handles the type pre-selection from the map.

`IssueHint` and its helper:

```go
type IssueHint struct {
    IssueID    string
    BranchType string
}

// Prefill returns the issue-hint contribution to a form prefill map.
// Fallback chain for IssueID: "scope" → "footer" (as "Refs: <id>") → "subject" (as "(<id>)").
// BranchType is emitted as "type" when non-empty; FillOutForm/loadForm validate it
// against cfg.CommitTypes and silently ignore an unconfigured value.
func (h IssueHint) Prefill(items []config.CommitItem) map[string]any
```

The existing `applyIssueHint` function is replaced by this method. `setItemValue` is no longer needed by this code path and is removed unless other callers exist.

### `cmd/commit/commit.go`

```go
hint := issueHintFromClient(client)
prefill := hint.Prefill(c.appConfig.CommitMessage.Items)
msg, opts, err := commitpkg.FillOutForm(ctx, c.appConfig, defaults, s, prefill)
```

No behavioural change: the new `Prefill` returns the same effective values that `applyIssueHint` used to write directly into the items slice.

### `cmd/issue/close.go`

- Delete `pickSquashAuthor` and its caller in `doMerge`.
- After the confirm form, in the `squash` branch of the strategy switch:
  - Resolve `branchSHA` and `baseSHA` via `c.repo.Reference(plumbing.ReferenceName("refs/heads/"+name), true)`.
  - Call `c.MergeSquash(ctx, picked.BranchName, baseBranch)`.
  - Build `IssueHint{IssueID: picked.IssueSlug, BranchType: picked.Type}.Prefill(cfg.CommitMessage.Items)`.
  - Overlay `prefill["subject"] = fmt.Sprintf("Squashed merge of %s into %s.", branchSHA[:7], baseSHA[:7])`.
  - Build `tui.CommitOption{Authors: authors, Author: authors[0]}` (guard empty list → empty string).
  - Call `commitpkg.FillOutForm` then `client.Commit(ctx, msg, git.CommitOptions{All: opts.All, Amend: opts.Amend, NoVerify: opts.NoVerify, Signoff: opts.Signoff, AllowEmpty: opts.AllowEmpty, Author: opts.Author})`.

`closeRunE` already holds a `store.Store` (`s`) and the appConfig via the `Issue` receiver, so no new dependencies are threaded.

### `tui/` — `IssueMergeConfirm`

Drop the `author` parameter from the constructor; drop the corresponding line from the confirm summary. Callers in `cmd/issue/close.go` update accordingly.

### Imports

- `git/merge.go` may drop `plumbing` if no other reference uses it (only `MergeSquash` did). Caller `cmd/issue/close.go` gains `plumbing`.

## Error handling

- `FillOutForm` already returns `huh.ErrUserAborted` on Esc/Ctrl+C. The squash caller treats that as "abort the close" — same semantics as the current confirm-form abort. The staged squash changes are left in place; the operator can `git reset` if desired. Document this in the close.go error path.
- `client.Commit` propagates any pre-commit / commit-msg hook failure. Unchanged behaviour from `git zf commit`.
- `c.repo.Reference` failures (missing branch) bubble up as `fmt.Errorf("resolve branch: %w", err)`. This shouldn't happen in practice because `MergeDryRun` already validated the merge.

## Testing

### Tests to update

- `git/merge_test.go::TestMergeSquash` — drop the `author` arg. Replace the "squash merge" message assertion with: verify `git status --porcelain` shows the staged file count and HEAD is unchanged before a manual follow-up commit. Then commit and assert files exist on the target branch.
- `git/merge_test.go::TestDeleteLocalBranch_forceDelete` — after the new `MergeSquash` call, follow up with `git commit -m "x"` so the squash commit is materialized and the branch becomes deletable with `-D`.
- `commit/form_test.go` — every `FillOutForm` invocation gains `nil` as the new `initialPrefill` arg. Any test of `applyIssueHint` is rewritten against `IssueHint.Prefill`.

### Tests to add

- `commit/form_test.go::TestIssueHint_Prefill` — table-driven:
  - scope item present → `{"scope": id, "type": branchType}`
  - only footer item → `{"footer": "Refs: id", "type": branchType}`
  - only subject item → `{"subject": "(id)", "type": branchType}`
  - empty `IssueID` and empty `BranchType` → empty map
  - empty `IssueID`, non-empty `BranchType` → `{"type": branchType}`
- `commit/form_test.go::TestFillOutForm_initialPrefill` — use the existing injectable `runFormFn` hook. Assert `initialPrefill["subject"]` reaches the rendered message, and `initialPrefill["type"]` is honored only when valid.

### Manual end-to-end

```bash
mise exec -- go test ./...
mise exec -- go build -o ./bin/git-zf .
make install
# In a working issue branch:
git zf issue close --debug
# Expect:
# - Dry-run completes instantly (merge-tree).
# - Strategy form: select squash.
# - Confirm screen: shows branch / base / strategy only (no author line).
# - Commit form opens pre-filled:
#     type=fix (or whichever branch type), scope=<issue-id>,
#     subject="Squashed merge of <bsha> into <basesha>.", body/footer empty,
#     author dropdown defaulted to current git identity.
# - Submit: commit lands with the rendered message.
```

## Out of scope (flagged for follow-up)

- An automated end-to-end test of the close flow (needs a huh form mock harness — separate work).
- A similar pre-fill of the current git identity in the `git zf commit` form. This change only sets the default for the squash-close path; whether `git zf commit` should also pre-select needs its own user decision.
- Symmetric routing of `MergeNoFF` through the form. The no-ff path already produces a `Merge branch '<name>'` commit message via git's own machinery, which is a different convention. Out of scope until requested.
