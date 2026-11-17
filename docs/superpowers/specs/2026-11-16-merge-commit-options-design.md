# Merge commit options — design

## Intent

`issue close` and `branch merge` commit a merge that is already staged. The
commit form's second page then asks the questions of `git zf commit`, most of
which are useless or harmful there:

- `--amend` folds the merge into the base's previous commit (usually pushed).
- `--include-untracked` sweeps stray files into the merge commit.
- `--all`, `--allow-empty` do nothing (the tree is clean, the merge not empty).

Keep a short page with the three options that mean something for a merge.

## Goals

- On `issue close` and `branch merge`, the options page shows only:
  Author, `--no-verify`, `--signoff`, in the order `git zf commit` shows them.
- `All`, `IncludeUntracked`, `Amend`, `AllowEmpty` are always false in the
  options a merge commit is made with.
- `git zf commit` is unchanged.

## Non-goals

- No flags on `issue close` / `branch merge` for these options.
- No change to the forge-merge path (it never opens the form).
- No config knob for which options show.

## Approaches

1. **A flag on `tui.CommitOption` (picked).** `MergeOnly bool`; `loadForm`
   (`commit/form.go`) appends `tui.MergeOptionsGroup(&opts)` instead of
   `tui.CommitOptionsGroup(&opts)` when it is set;
   `mergeflow.HuhPrompter.ComposeMessage` sets it. One form, back-navigation
   between message and options kept, ~20 lines.
2. A generic "hidden fields" list on `CommitOption`. More flexible, nobody
   needs the flexibility.
3. A second form run by `ComposeMessage` after the message form. No change to
   `commit/`, but shift+tab back to the message is lost and esc semantics
   split across two forms.

## Design (my calls)

- `tui/commit.go`: `CommitOption.MergeOnly`; `MergeOptionsGroup(opt)` = the
  Author select, then the no-verify and signoff confirms: the same relative
  order and the same titles as `CommitOptionsGroup`, so tabbing through
  feels the same in both flows.
- Extract `authorSelect(opt)` from `CommitOptionsGroup`; both groups use it.
  It carries the "no authors found" option and the fallback
  `opt.Author = opt.Authors[0]` when `Author` is empty.
- `commit/form.go` `loadForm`: a switch, `Skip` first (no page), then
  `MergeOnly` (merge group), else the full group. `Skip` wins, so a future
  `--yes` on the merge commands needs no change here.
- `cmd/mergeflow/huh_prompter.go`:
  `defaults := tui.CommitOption{Authors: authors, MergeOnly: true}`; its own
  `Author = authors[0]` lines go, `authorSelect` does it.
- Author default stays the git identity (first of `Client.Authors`).
- `MergeOnly` is set on `defaults` before `FillOutForm`, so it survives the
  ctrl+r history loop. That loop already resets page-2 answers to the
  defaults on a reload; unchanged here.

## Tests

- `tui/commit_test.go` (new file): render `huh.NewForm(MergeOptionsGroup(&opt))`
  and assert on the view: Author, `--no-verify`, `--signoff` present, in that
  order; `--all`, `--include-untracked`, `--amend`, `--allow-empty` absent.
  `authorSelect` fallback with `Authors` empty and populated.
- `commit/form_test.go`: `loadForm` with `MergeOnly` yields a second page
  that is the merge one (advance with `form.NextGroup()` after `Init()` and
  check the view; `huh.Form` does not export its groups; in huh
  v1.0.0 `NextGroup()` runs the update itself), and with
  `Skip` + `MergeOnly` yields no second page.
- Close and merge E2E suites stay green (their scripted prompters bypass the
  form; no new E2E needed).

## Left to the builder

How the form tests read the rendered page (view strings, or the options the
form writes back).
