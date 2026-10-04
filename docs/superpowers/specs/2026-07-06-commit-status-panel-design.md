# Design: Git status panel in `git zf commit`

**Date:** 2026-07-06
**Status:** Approved (design), pending spec review

## Summary

When the user runs `git zf commit`, show the current working-tree status
(a `git status`-like list of files) inside the commit TUI, rendered as a
bordered panel titled **"Current Git Status"** to the **right of the entire
form**. The list is a static snapshot taken when the form opens.

## Goals

- Give the user visibility into what is staged / modified / untracked while
  they compose the commit message, without leaving the TUI.
- Include staged, unstaged, and untracked entries (full `git status` picture).
- Never let a status read failure block a commit.

## Non-goals

- No live refresh of the status while the form is open (static snapshot).
- No interaction with the panel (read-only; not focusable, not a form field).
- No change to what `git zf commit` actually commits. The panel is
  informational only; staging is still governed by the existing `--all`
  flag / form toggle and the user's own `git add`.
- The issue-close commit flow and the ctrl+r history picker do **not** show
  the panel in this iteration.

## User-facing behaviour

The commit message form and options form render on the left; a status panel
renders to their right:

```
┌ Type: ────────────────┐   ┌ Current Git Status ─────────────┐
│ ● feat                │   │ Changes to be committed:  (yellow) │
│ Subject: ...          │   │   modified: cmd/commit/...          │
│ Body: ...             │   │   new file: git/status.go           │
│ ...                   │   │                                     │
│ Author: ...           │   │ Changes not staged for    (green)  │
│ [--all] [--amend] ... │   │ commit:                             │
│                       │   │   modified: commit/form.go          │
│                       │   │   deleted:  old/removed.go          │
│                       │   │                                     │
│                       │   │ Untracked files:       (turquoise) │
│                       │   │   docs/                             │
└───────────────────────┘   └─────────────────────────────────────┘
```

The panel groups entries into labelled, colored sections that mirror
`git status` long format, using these header labels and colors:

- **Changes to be committed:** — **yellow** — files with an index-side change
  (`X` ≠ unmodified), each shown as `<word>: <path>` (word = `modified` /
  `new file` / `deleted` / `renamed` / `copied` / `typechange`).
- **Changes not staged for commit:** — **green** — files with a worktree-side
  change (`Y` ≠ unmodified).
- **Untracked files:** — **turquoise** — `??` entries, shown as bare paths.
- **Unmerged paths:** — **red** — conflict (`u`) entries, shown as
  `<word>: <path>`. (Colored red, matching git's default for conflicts; the
  user-specified colors above cover the three common sections.)

A file with both index and worktree changes (e.g. porcelain `MM`) appears in
**both** the Staged and Unstaged sections, exactly as `git status` reports it.
Renamed entries render as `renamed: origPath -> path`. Empty sections are
omitted. The full list is shown with no truncation.

When the working tree is clean (no entries), no panel is rendered — the form
appears alone, with no empty box.

## Architecture

Three components with clean boundaries, following the existing package split
(`git` = git semantics, `tui` = rendering, `cmd/commit` = orchestration).

### 1. `git` package — status data

New exported method and type on `*git.Client`:

```go
// StatusEntry is one line of `git status` output.
type StatusEntry struct {
    // XY is the two-character porcelain status code (e.g. " M", "M ", "A ",
    // "??"). For untracked entries this is "??".
    XY   string
    // Path is the current path of the entry.
    Path string
    // OrigPath is the pre-rename path for renamed/copied entries, else "".
    OrigPath string
}

// StatusEntries returns the working-tree status as parsed entries by running
// `git -C <root> status --porcelain=v2`. Mirrors IsDirty: it shells out to the
// system git binary so semantics match git exactly.
func (c *Client) StatusEntries(ctx context.Context) ([]StatusEntry, error)
```

Command: `git -C <root> status --porcelain=v2`

- Format `porcelain=v2` is a stable, config-independent, machine-parseable
  interface (chosen over `--short` so the parse is robust across git versions
  and user config).
- **No** `--untracked-files=all` flag: git's default untracked mode
  (`normal`) is used, which lists untracked entries while collapsing untracked
  directories.

Parsing lives in an unexported pure helper, unit-tested independently:

```go
func parsePorcelainV2(raw string) []StatusEntry
```

Porcelain v2 line kinds the parser must handle:

- **Ordinary changed** — lines beginning `1 <XY> ...` — fields are
  space-separated; the path is the final field.
- **Renamed / copied** — lines beginning `2 <XY> ...` — the path field is
  `<newPath>\t<origPath>` (tab-separated); populate `Path` and `OrigPath`.
- **Untracked** — lines beginning `? <path>` — emit `XY = "??"`.
- **Ignored** — lines beginning `! <path>` — excluded (git default status
  does not emit these without `--ignored`; parser skips them defensively).
- **Unmerged** — lines beginning `u <xy> ...` — emit the `XY` code and path.
- **Header lines** — beginning `# ` — skipped.

The two-character `XY` reconstructed for `1`/`2` lines uses the porcelain v2
`<XY>` field directly (a dot `.` in v2 means "unmodified" for that side and is
normalised to a space for display).

### 2. `tui` package — panel rendering

New exported function:

```go
// StatusPanel renders entries as a bordered lipgloss panel titled
// "Current Git Status", grouped into colored sections (Staged / Unstaged /
// Untracked / Unmerged). Returns "" when entries is empty.
func StatusPanel(entries []git.StatusEntry) string
```

Classification is a pure, separately-testable helper (no color, no lipgloss)
so section logic can be unit-tested independent of styling:

```go
type statusKind int // Staged, Unstaged, Untracked, Unmerged

type statusLine struct {
    Word string // "modified", "new file", "deleted", "renamed", ...
    Path string // "commit/form.go" or "origPath -> path" for renames
}

type statusGroup struct {
    Kind  statusKind
    Lines []statusLine
}

// groupEntries classifies entries into ordered, non-empty sections.
func groupEntries(entries []git.StatusEntry) []statusGroup
```

Classification rules (from the parsed `XY` code, `.`/space = unmodified):

- **Staged** — `X` is a change code (`M`/`A`/`D`/`R`/`C`/`T`) → one line with
  the word for `X`. Rename/copy (`2` lines) land here with `Path` set to
  `origPath -> path`.
- **Unstaged** — `Y` is a change code → one line with the word for `Y`.
- **Untracked** — `??` → one line, `Word` empty, bare `Path`.
- **Unmerged** — `u` entries → one line in the Unmerged section.
- A single entry with both sides changed (e.g. `MM`) contributes a line to
  **both** Staged and Unstaged.

`StatusPanel` then maps each `statusKind` to a header label and a lipgloss
color style:

| Kind      | Header label                    | Color     | lipgloss color        |
|-----------|---------------------------------|-----------|-----------------------|
| Staged    | `Changes to be committed:`      | yellow    | `lipgloss.Color("3")` |
| Unstaged  | `Changes not staged for commit:`| green     | `lipgloss.Color("2")` |
| Untracked | `Untracked files:`              | turquoise | `lipgloss.Color("#40E0D0")` |
| Unmerged  | `Unmerged paths:`               | red       | `lipgloss.Color("1")` |

(These color values are the concrete defaults; ANSI-indexed colors keep the
yellow/green/red faithful across terminals, and turquoise uses a hex value
that lipgloss degrades to the nearest supported color.) Each section renders
as a colored header plus its lines (the whole section in the section color),
wrapped in the bordered, titled panel. Empty sections are omitted; when every
section is empty the function returns `""`.

- Uses the existing `lipgloss` dependency already imported in `tui/commit.go`.
- lipgloss degrades color automatically (honors `NO_COLOR` and terminals
  without color support), so no explicit color-capability handling is needed.
- Border + title styling consistent with the surrounding TUI.

### 3. `cmd/commit` + `commit` + `tui/runner` — wiring

`FormRunner` already wraps the whole `huh.Form` as a `tea.Model`
(`tui/runner.go`). Its `View()` is the composition seam:

```go
// View joins the form with the status panel to its right. When panel is "",
// only the form is rendered.
func (r *FormRunner) View() string {
    if r.panel == "" {
        return r.form.View()
    }

    return lipgloss.JoinHorizontal(lipgloss.Top, r.form.View(), r.panel)
}
```

`RunForm` gains a `panel string` parameter, stored on the `FormRunner`.

Data flow (all downstream code carries an opaque string; only `runE` knows how
the panel is built):

```
cmd/commit/commit.go runE():
  entries, err := client.StatusEntries(ctx)   // git package; on err → log + entries=nil
  panel := tui.StatusPanel(entries)            // tui package; "" when no entries
  msg, opts, err := commitpkg.FillOutForm(ctx, cfg, defaults, store, prefill, panel)
        └─ runFormFn(form, panel)
              └─ tui.RunForm(form, panel)
                    └─ FormRunner{form, panel}
```

Signature changes:

- `tui.RunForm(form *huh.Form, panel string) (*FormRunner, error)`
- `commit.runFormFn` var: `func(form *huh.Form, panel string) (formRunner, error)`
- `commit.FillOutForm(ctx, cfg, defaults, hs, initialPrefill, panel string)`

Call sites that pass `""` (no panel), keeping current behaviour:

- `cmd/issue/close_prompter.go:130` — issue-close commit form.
- `commit/form.go` `runHistoryPicker` — the ctrl+r history select form.

## Error handling

- `StatusEntries` failure in `runE`: log a warning (`slog.Warn`) and continue
  with `entries = nil` → empty panel → form renders alone. A status read must
  never block a commit — same philosophy as the author-list fallback at
  `cmd/commit/commit.go:104`.
- Parser encountering an unrecognised line: skip it (defensive), do not error
  the whole status.

## Testing

Per the repo convention, every distinct assertion/scenario is wrapped in a
named `t.Run`.

- **`git` package**
  - `parsePorcelainV2` — table test, one `t.Run` per line kind: ordinary
    modified (staged / unstaged / both), added, deleted, renamed (with
    `OrigPath`), untracked `?`, unmerged `u`, ignored `!` (skipped), header
    `#` (skipped), empty input.
  - `StatusEntries` — integration test against a real on-disk temp repo
    (mirrors existing `git` tests): stage/modify/create files, assert the
    returned entries. One `t.Run` per repo scenario.
- **`tui` package**
  - `groupEntries` — table test, one `t.Run` per scenario: staged-only,
    unstaged-only, `MM` → line in **both** Staged and Unstaged, added
    (`new file`), deleted, renamed (`renamed: orig -> path` in Staged),
    untracked (`??` in Untracked), unmerged (`u` in Unmerged), empty input →
    no groups. Asserts section order, kind, word, and path — no color.
  - `StatusPanel` — `t.Run` for: empty entries → `""`; a set of entries →
    output contains the expected section headers and `<word>: <path>` lines;
    a rename entry → shows `origPath -> path`. (Assert on text content, not
    ANSI codes.)
- **`commit` package**
  - Update existing `FillOutForm` tests for the new `panel` parameter.
  - Add a `t.Run` asserting the panel string is forwarded to `runFormFn`
    (stub captures the argument).
- **`tui/runner`**
  - `FormRunner.View` — `t.Run` for empty panel (form only) vs non-empty panel
    (joined output contains the panel text).

## Affected files

- `git/status.go` (new) — `StatusEntry`, `StatusEntries`, `parsePorcelainV2`.
- `git/status_test.go` (new).
- `tui/status_panel.go` (new) — `StatusPanel`.
- `tui/status_panel_test.go` (new).
- `tui/runner.go` — `RunForm` + `FormRunner` panel field and `View`.
- `commit/form.go` — `FillOutForm` + `runFormFn` signature.
- `commit/form_test.go` — updated.
- `cmd/commit/commit.go` — `runE` computes and threads the panel.
- `cmd/issue/close_prompter.go` — pass `""` to `FillOutForm`.
