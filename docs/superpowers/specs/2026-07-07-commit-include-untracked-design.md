# Design: `--include-untracked` option for `git zf commit`

**Date:** 2026-07-07
**Status:** Approved (design), pending spec review

## Summary

Add an option to `git zf commit` that stages untracked files before
committing. It is exposed as a CLI flag `--include-untracked` (short `-u`) and
as a toggle in the commit options form, mirroring the existing `--all`
mechanism. It is orthogonal to `--all` (stages *only* untracked files) and
composes with it. The "Current Git Status" panel reflects it live: when on,
untracked files fold into "Changes to be committed".

## Goals

- Let the user include untracked files in a `git zf commit` without dropping
  to a shell for `git add`.
- Stage only untracked files (orthogonal to `--all`), so the two options
  compose: staged-only / +untracked / +all / +all+untracked.
- Respect `.gitignore` (never stage ignored files).
- Reflect the option in the status panel, live, the same way `--all` is.

## Non-goals

- No selective staging of individual untracked files (all-or-nothing).
- No change to what `--all` does.
- The issue-close commit flow and the ctrl+r history picker are unaffected
  (they already render no panel and take no commit options here).

## Semantics

`git commit` itself cannot include untracked files (even `git commit --all`
stages only tracked modifications/deletions). So the option performs a
`git add` of untracked files as part of the commit step.

Composition (independent toggles):

| `--all` | `--include-untracked` | What is committed                                   |
|---------|-----------------------|-----------------------------------------------------|
| off     | off                   | already-staged only                                 |
| off     | on                    | already-staged + untracked                          |
| on      | off                   | already-staged + tracked modified/deleted           |
| on      | on                    | everything (staged + tracked edits + untracked)     |

## Architecture

The change threads one boolean down the existing option chain and adds a
staging step in the git layer. It follows the exact pattern already used by
`--all`.

### 1. Option plumbing

- `git.CommitOptions` (git/git.go) gains `IncludeUntracked bool`.
- `tui.CommitOption` (tui/commit.go) gains `IncludeUntracked bool`.
- `internal/convert.CommitOptionsFromTUI` maps
  `opts.IncludeUntracked → git.CommitOptions.IncludeUntracked`. The existing
  `TestCommitOptionsFromTUI` asserts every field is mapped, so a missed wire
  is caught.

### 2. CLI flag

In `cmd/commit/commit.go` `GetRootCmd`:

- Add `f.BoolVarP(&includeUntracked, "include-untracked", "u", false, "stage untracked files before committing")`.
  (`-u` is currently unused by this command; taken short flags: `-y`, `-a`,
  `-n`, `-s`.)
- Add `cmd.Flags().Changed("include-untracked")` to the `Skip` predicate, so
  passing `-u` on the CLI skips the interactive options form (consistent with
  `--all` and the other option flags).
- Seed `tui.CommitOption{... IncludeUntracked: includeUntracked}` in the
  `RunE` closure.

### 3. Staging in `git.Client.Commit`

When `opts.IncludeUntracked` is set, before building/running the `git commit`
command, `Commit` stages untracked files:

```
out       ← git -C <root> ls-files --others --exclude-standard -z
untracked ← split out on NUL, dropping the trailing empty element
if len(untracked) > 0:
    git -C <root> add -- <untracked...>   # each path a separate argv element
```

- The list comes from `ls-files -z` (NUL-separated) and each path is passed as
  its own argv element to `git add` (via `exec`, no shell), so paths with
  spaces are safe. (ARG_MAX is not a practical concern for interactive commit
  sizes; a pathspec-from-file variant can be revisited only if it ever is.)
- `--exclude-standard` respects `.gitignore`/`.git/info/exclude`, so ignored
  files are never staged.
- Empty list → skip the `add` entirely (no-op), still commit.
- A `ls-files` or `add` failure returns a wrapped error **before** the commit
  runs (no partial commit is attempted).
- The subsequent `git commit` is unchanged (it still applies `--all` etc.);
  the untracked add happens first so `--all` + `--include-untracked` together
  include everything.

This keeps staging self-contained in the git layer, runs against a fresh
untracked list at commit time (not a snapshot taken before the form), and lets
pre-commit hooks see the staged files.

### 4. Form toggle

In `tui.CommitOptionsGroup` (tui/commit.go), add a confirm after the `--all`
one:

```go
huh.NewConfirm().Title("Stage untracked files? (--include-untracked)").Value(&opt.IncludeUntracked),
```

### 5. Panel re-classification (live)

`groupEntries` and `StatusPanel` gain an `includeUntracked bool` parameter
alongside the existing `all bool`:

- `groupEntries(entries, all, includeUntracked bool)`:
  - `all` folds tracked worktree changes into "Changes to be committed"
    (existing behavior).
  - `includeUntracked` folds untracked entries into "Changes to be committed",
    rendered as `new file: <path>` (what `git status` shows once an untracked
    file is staged), and omits the "Untracked files:" section.
  - The two folds are independent and compose.
- `StatusPanel(entries, all, includeUntracked bool)` passes both through.
- `StatusPanelReserveWidth(entries)` returns the max panel width across all
  four `(all, includeUntracked)` combinations, so the form width stays stable
  regardless of which toggles flip.
- `FormRunner`'s live reader changes from `allFn func() bool` to a single
  reader returning both flags — Go type `func() (bool, bool)`, returning
  `(all, includeUntracked)` in that order. `View` computes
  `StatusPanel(entries, all, includeUntracked)` each render, so toggling either
  option re-classifies the panel in real time.
- `FillOutForm` builds `classifyFn` from the form's live options:
  `func() (bool, bool) { o := extractOpts(); return o.All, o.IncludeUntracked }`.
  `RunForm`/`runFormFn` carry `classifyFn` instead of `allFn`; the close flow,
  history picker, and no-history dialog pass `nil` (no panel), unchanged.

## Error handling

- `ls-files`/`add` failure in `Commit`: wrap and return before committing;
  the commit does not proceed. (Same "surface the git error" style as the rest
  of `Commit`.)
- If staging succeeds but the commit later fails (e.g. a rejecting hook), the
  untracked files remain staged — identical to running `git add` then a failed
  `git commit` by hand. No rollback is attempted; the user retries.

## Testing

Per repo convention, each distinct scenario is a named `t.Run`.

- **`git`** (git/git_test.go, real on-disk repo via `newDiskRepo`):
  - `Commit` with `IncludeUntracked`: create an untracked file, commit, assert
    the untracked file is in the resulting commit (e.g. via `git show --stat`
    / `ls-tree`).
  - `IncludeUntracked` with no untracked files: no-op add, commit still
    succeeds.
  - `IncludeUntracked` + `All` together: a tracked modification and an
    untracked file both land in the commit.
  - `.gitignore` respected: an ignored untracked file is NOT staged.
- **`tui`** (tui/status_panel_test.go):
  - `groupEntries` with `includeUntracked=true`: untracked folds into staged
    as `new file: <path>`, no untracked section; composes with `all=true`.
  - `StatusPanel` with `includeUntracked=true`: rendered panel shows the
    untracked file under "Changes to be committed" and no "Untracked files:".
  - `StatusPanelReserveWidth`: covers the four combinations.
- **`tui`** (tui/runner_test.go):
  - `FormRunner.View` reflects a live `includeUntracked` toggle (untracked
    moves into "Changes to be committed").
- **`internal/convert`**: extend `TestCommitOptionsFromTUI` for the new field.
- **`commit`** (commit/form_test.go): `classifyFn` reads the live
  `IncludeUntracked` from the form options; existing `FillOutForm` tests
  updated for the signature change.

## Affected files

- `git/git.go` — `CommitOptions.IncludeUntracked`; staging step in `Commit`.
- `git/git_test.go` — new `Commit` scenarios.
- `tui/commit.go` — `CommitOption.IncludeUntracked`; form toggle.
- `tui/status_panel.go` — `includeUntracked` in `groupEntries`/`StatusPanel`/`StatusPanelReserveWidth`.
- `tui/status_panel_test.go` — new fold/compose/render tests.
- `tui/runner.go` — `FormRunner` `classifyFn func() (bool, bool)`; `RunForm` signature.
- `tui/runner_test.go` — live-toggle test updated/extended.
- `internal/convert/*.go` (+test) — map the field.
- `commit/form.go` — `FillOutForm`/`runFormFn` carry `classifyFn`.
- `commit/form_test.go` — updated stubs/callers.
- `cmd/commit/commit.go` — flag, `Skip` predicate, seed the option.
