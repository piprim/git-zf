# Choosable merge target for `git issue close`

**Date:** 2026-06-24
**Status:** Approved (design)

## Goal

Let the operator choose which branch the closing branch merges/rebases into,
instead of the target being fully automatic. The default is a *smart* one: it is
exactly today's auto-computed target — the parent integration branch when the
issue has a parent, otherwise `branch.base` (config) or `DefaultBaseBranch()`.

This turns an implicit, invisible decision into an explicit, overridable one
without changing the common-case outcome.

## Background (current behaviour)

`runClose` (`cmd/issue/close.go:142-185`) resolves the merge target with no user
input:

1. `base = cfg.Branch.Base`, else `client.DefaultBaseBranch()`
   (`refs/remotes/<remote>/HEAD` → `main` → `master`).
2. **Parent redirect:** if the picked issue has a parent (via
   `store.GetParentIssue`, falling back to the `refs/zf/branches/<slug>` ref's
   `ParentSlug`), `base` is overridden to the parent's branch name. The parent
   branch may exist only on the remote.

The merge machinery (`doMerge` / `rebasePreflight` / squash / classic) already
handles a base that is local *or* remote-only (`origin/<base>` fallback).

There is an established precedent for an interactive base picker: the **start
flow** (`cmd/issueflow/start.go:91-103`) uses `tui.BaseBranchPicker` +
`prompter.PickBaseBranch`. `--base` is also already an established flag name in
this project (`branch prune`, `cmd/branch/branch.go:245`).

## Behaviour

- **Smart default (unchanged logic):** the existing base resolution +
  parent-redirect block produces the default target. It becomes the pre-selected
  value rather than the final, unquestionable answer.
- **Picker:** the candidate set is all local branch names (mirrors `issue
  start`), with the smart default *force-included even if it is remote-only* and
  pre-selected. The picker is shown only when **more than one candidate** exists;
  with one or zero candidates it is skipped silently.
- **`--base <branch>` flag:** when non-empty, the value is validated (must resolve
  as `refs/heads/<name>` or `<remote>/<name>`) and used directly, **skipping the
  picker** — for CI/scripts and power users. Empty/unset preserves the default +
  picker behaviour.
- **Confirmation:** the existing `ConfirmMerge` form already prints
  `branch → base`, so the chosen target — including a sub-task deliberately merged
  somewhere other than its parent branch — is surfaced before any mutation. No
  separate warning is added.

## Architecture / changes

### `cmd/issue/close.go`

- Extract the base + parent-redirect block (lines 142-185) into a helper that
  returns the *smart-default* base (e.g. `resolveDefaultBase(ctx, deps, picked)`).
  The parent guard (`ChildrenAllMerged`) and `reconcileChildrenFromRefs` stay
  where they are.
- After computing the default base:
  - If `deps.baseOverride != ""`: validate it resolves (local `refs/heads/<name>`
    or `<remote>/<name>`); on success use it, on failure return a clear error.
    Skip the picker.
  - Else: build the candidate list = local branch names ∪ {smart default}. If the
    list has more than one entry, call
    `prompter.PickBaseBranch(ctx, defaultBase, candidates)` and use the result.
- Add a per-invocation `baseOverride string` field to `closeDeps`. Existing tests
  construct `closeDeps` / `rig.deps()` and leave it at its `""` zero value, so no
  test-call churn beyond the new cases.

### `getCloseCmd()` / `closeRunE`

- Register `cmd.Flags().String("base", "", "merge target branch (default: smart
  default + interactive picker)")`.
- `closeRunE` reads the flag and sets `deps.baseOverride` before calling
  `runClose`.

### `ClosePrompter` (`cmd/issue/close_prompter.go`)

- Add `PickBaseBranch(ctx context.Context, defaultBase string, branches []string)
  (string, error)`.
- `huhPrompter.PickBaseBranch` reuses `tui.BaseBranchPicker` (already present in
  `tui/branch.go:182`).

### `scriptedPrompter` (`cmd/issue/close_prompter_test.go`)

- Add `Base string` and `BaseErr error` fields, plus the `PickBaseBranch` method
  returning them.

## Edge cases

- **Remote-only smart default** (parent branch never checked out locally): it is
  appended to the candidate list so it is selectable and pre-selected; the merge
  machinery already resolves it via `origin/<base>`.
- **`--base` naming a nonexistent branch:** early, clear error before any merge
  step runs.
- **Single candidate:** no picker — zero friction for top-level issues whose only
  target is the base branch.
- **Parent/child semantics unchanged:** the `ChildrenAllMerged` guard and the
  merged-bookkeeping (branch ref `Merged=true`, store status updates) key off the
  picked *issue*, not the target, so overriding the target does not affect them.

## Testing

End-to-end in `cmd/issue/close_e2e_test.go`, following the existing scripted-
prompter pattern. Per the repo convention, every distinct assertion is wrapped in
its own `t.Run`:

- Picker is offered and the smart default is pre-selected when more than one
  branch exists.
- Overriding to a non-default branch merges into the chosen branch.
- `--base` (i.e. `deps.baseOverride`) set: picker is skipped and the merge lands
  on the flag's value.
- `--base` naming a branch that does not resolve: returns an error, no merge.
- Single-branch repo: picker is skipped.

## Out of scope (YAGNI)

- Listing remote `origin/*` entries in the picker (local + force-included default
  only).
- Recording the chosen target as a parent relation (close is terminal; only the
  start flow records parent relations).
- Per-strategy target rules — the target is independent of squash/rebase/classic.
