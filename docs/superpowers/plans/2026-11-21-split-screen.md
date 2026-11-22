# Split Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** git-zf's screens stay in a band at the top of the terminal while commands, hooks and forms run on the real terminal in a scroll region below.

**Architecture:** One session in `tui` owns the terminal layout. Every screen starts through `tui.RunScreen`, which either wraps the model in a `band` (inline, fixed line count, DECSTBM region below) or runs it on the alternate screen. The screens learn to fit the height they are given; forms started as programs get their window height clamped to the output band.

**Tech Stack:** Go, bubbletea v1.3.6, huh v1.0.0, lipgloss, `golang.org/x/term`. No new dependency.

**Spec:** `docs/superpowers/specs/2026-11-21-split-screen-design.md`

**Status:** executed 2026-11-21, amended 2026-11-22 after the reviews and the operator's checklist run. The rules below hold what the code does; "Amendments after execution" at the end lists what moved.

## Global Constraints

- Go through mise: `mise exec -- go test ./...`, `mise exec -- golangci-lint run --new-from-rev=HEAD ./...` (0 issues on new lines).
- Every assertion in a `t.Run("label", ...)` (global CLAUDE.md).
- Before editing a function: `node .gitnexus/run.cjs impact "<symbol>" --direction upstream --repo .`; report HIGH/CRITICAL to the operator. Index is stale: run `node .gitnexus/run.cjs analyze --index-only` first.
- **No commits.** The operator commits. Each task ends with a checkpoint (tests + lint green), the tree left uncommitted, on `wip`.
- The working tree already holds the off mode (alternate screen at five sites, `tui/pause.go`). Keep it; this plan builds on it.
- Split only when stdin, stdout and stderr are all terminals, and each band gets at least 10 rows. `size` is 20–80, default 50; `split` defaults to true.
- The E2E rigs have no terminal: nothing in this plan may change what they see.
- No change to keys or to what a screen does, except the height it takes.

## Review Focus

1. **Resize while a screen is open** → the region follows the new row count, the screen is not garbled (Task 2, `band` resize test).
2. **Terminal shrunk below the floor between two screens** → the split closes cleanly and the next screen runs on the alternate screen (Task 3, session fallback test).
3. **One stream redirected** (`git zf 2>err.log`) → no split, no escape sequence written (Task 3, tty test).
4. **Error after an action while fallen back** → `[enter] to continue` still shows (Task 3, `Pause` test).
5. **Exit without a split ever opened, or closed twice** → `CloseScreen` writes nothing (Task 3, idempotence test).

---

### Task 1: `[ui]` config

**Files:**
- Create: `config/ui.go`, `config/ui_test.go`
- Modify: `config/config.go` (the `AppConfig` struct near line 186, `Load` near line 223), `config/default.toml`

**Interfaces:**
- Produces: `type UIConfig struct { Split bool; Size int }` (toml/json keys `split`, `size`), field `AppConfig.UI UIConfig` (key `ui`), `func (u *UIConfig) normalize() error`.

- [ ] **Step 1: Write the failing test** `TestLoad_UI` in `config/ui_test.go`, following `TestLoad_Log`'s way of loading a temp file:

```go
t.Run("defaults", ...)            // cfg.UI == UIConfig{Split: true, Size: 50}
t.Run("split = false", ...)       // user file `[ui]\nsplit = false` → Split false, Size 50
t.Run("size = 70", ...)           // → Size 70, Split true
t.Run("size below 20", ...)       // size = 19 → Load error containing "ui.size"
t.Run("size above 80", ...)       // size = 81 → Load error containing "ui.size"
```

- [ ] **Step 2: Run** `mise exec -- go test ./config/... -run TestLoad_UI -v` — FAIL (no `UI` field).
- [ ] **Step 3: Implement.** `normalize` returns `fmt.Errorf("ui.size: %d is out of 20–80", u.Size)` outside the range. Call it in `Load` after `cfg.Log.normalize()`. Append to `config/default.toml`:

```toml
# git-zf keeps its screens in the top of the terminal and leaves the rest to
# the output of the commands it runs. split = false: every screen full screen.
[ui]
split = true
size = 50   # percent of the rows for git-zf (20-80)
```

- [ ] **Step 4: Run** the test — PASS; `mise exec -- go test ./config/...` — PASS.
- [ ] **Step 5: Checkpoint** (lint, no commit).

---

### Task 2: Layout, sequences and the band wrapper (pure)

**Files:**
- Create: `tui/split.go`, `tui/split_test.go`

**Interfaces:**
- Produces (all unexported, same package as Task 3):
  - `const minBand = 10`
  - `type layout struct{ rows, cols, top int }` — `top` is the row count of git-zf's band, separator included.
  - `func newLayout(rows, cols, size int) (layout, bool)` — `top = rows*size/100`; false when `top < minBand` or `rows-top < minBand`.
  - `func (l layout) open() string`, `before() string`, `after() string`, `resized(rows int) string`, `closeSeq() string`
  - `type band struct { inner tea.Model; l layout; out io.Writer }`, a `tea.Model`.

Sequences, exact (`E` = `\x1b`):

| method | bytes |
|---|---|
| `open` | `E[<rows>;1H` + `rows` × `\n` (visible content goes to the scrollback) + `E[<top+1>;<rows>r` + `E[<top+1>;1H` |
| `before` | `E7` + `E[<top+1>;<rows>r` + `E[1;1H` |
| `after` | `E[<top>;1H` + `cols` × `─` + `E8` |
| `resized(n)` | `E[<top+1>;<n>r` + `E[<top>;1H` |
| `closeSeq` | `E7` + `E[r` + `E[1;1H` + `E[<top>M` + `E8` + `E[<top>A` |

`band` rules: `Update` rewrites a `tea.WindowSizeMsg` to `Height: l.top - 1` before passing it on. It stores `msg.Width` in `l.cols` (the separator follows a horizontal resize). When `msg.Height != l.rows` it stores the new `rows` and, only when `msg.Height > l.top` (a region `top+1..n` with `n <= top` is invalid), writes `l.resized(msg.Height)` to `out`. `band` is used by pointer so the session can read the final layout back. `View` cuts or pads the inner view to `l.top - 1` lines and appends a last line of `cols` × `─`. (bubbletea erases a view's last line on exit and never emits "erase below" while the line count is constant: see the spec.)

- [ ] **Step 1: Write the failing tests** in `tui/split_test.go`:

```go
func TestNewLayout(t *testing.T) {
    // t.Run each:
    // (40, 100, 50) → {40,100,20}, true
    // (24, 80, 50)  → top 12, true
    // (19, 80, 50)  → false   (top 9)
    // (24, 80, 80)  → false   (output 5)
    // (50, 80, 80)  → top 40, true
}

func TestLayout_Sequences(t *testing.T) {
    l := layout{rows: 24, cols: 3, top: 12}
    // open:      "\x1b[24;1H" + strings.Repeat("\n", 24) + "\x1b[13;24r\x1b[13;1H"
    // before:    "\x1b7\x1b[13;24r\x1b[1;1H"
    // after:     "\x1b[12;1H───\x1b8"
    // resized 30:"\x1b[13;30r\x1b[12;1H"
    // closeSeq:  "\x1b7\x1b[r\x1b[1;1H\x1b[12M\x1b8\x1b[12A"
}

func TestBand(t *testing.T) {
    // inner: a stub model recording the last WindowSizeMsg and returning a set view
    // "reports the band's height": send WindowSizeMsg{80,24} → inner saw Height 11
    // "pads a short view": inner view "a\nb" → 12 lines, last is the separator
    // "cuts a tall view": inner view of 40 lines → 12 lines, line 11 is inner's line 11
    // "an empty view is a blank band": "" → 12 lines
    // "same rows writes nothing": WindowSizeMsg{80,24} → out empty
    // "a resize moves the region": WindowSizeMsg{80,30} → out == "\x1b[13;30r\x1b[12;1H"
    // "a horizontal resize widens the separator": WindowSizeMsg{100,24} → last view line is 100 × "─", out empty
    // "shrunk to the band or less writes nothing": WindowSizeMsg{80,12} → out empty, l.rows == 12
    // "passes other messages through": a tea.KeyMsg reaches inner unchanged
}
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run "TestNewLayout|TestLayout_Sequences|TestBand" -v` — FAIL (undefined).
- [ ] **Step 3: Implement** in `tui/split.go`.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 3: The session, the entry point and the wiring

**Files:**
- Modify: `tui/split.go`, `tui/split_test.go`, `tui/pause.go`, `tui/pause_test.go`
- Modify: `cmd/root.go` (after `config.Load`, line ~155), `main.go`
- Modify: `cmd/home.go:40`, `cmd/issue/list.go:249`, `cmd/issue/record.go:74`, `cmd/branch/table.go:175`, `cmd/log/log.go:124`

**Interfaces:**
- Consumes: Task 1 `AppConfig.UI`; Task 2 `layout`, `band`.
- Produces:
  - `func Configure(split bool, size int)` — called once, from `cmd/root.go`: `tui.Configure(appConfig.UI.Split, appConfig.UI.Size)`.
  - `func RunScreen(ctx context.Context, m tea.Model) (tea.Model, error)` — returns the *inner* final model.
  - `func CloseScreen()` — idempotent; restores the terminal.
  - `func SplitActive() bool`
  - `func OutputHeight() int` — rows of the output band while the split is active (never under 1), else 0.

The state is one unexported struct so tests can inject its edges:

```go
type session struct {
    mu       sync.Mutex
    split    bool
    size     int
    out      io.Writer                      // os.Stdout
    isTTY    func() bool                    // stdin, stdout and stderr are terminals
    termSize func() (cols, rows int, err error)
    run      func(m tea.Model, opts ...tea.ProgramOption) (tea.Model, error) // tea.NewProgram(m, opts...).Run()
    active   *layout                        // nil: no region set
    inScreen bool
}
var screen = newSession() // the exported functions delegate to it
```

`runScreen` rules, in order:
1. `!split`, `!isTTY()`, size error, or `newLayout` false → if `active != nil` write `closeSeq` and clear it; run `m` with `tea.WithContext(ctx), tea.WithAltScreen()`; return.
2. `active == nil` → write `open()`, set `active`, install the signal handler, `os.Setenv("GIT_PAGER", "cat")` (no child pages inside the band).
3. `active` set and `kept()` → reuse `*active` as it is: a band resized while its screen was open keeps its top. `kept()`: the terminal has the rows and cols of `*active`, a fresh layout is possible, and `active.fits()` (both bands ≥ `minBand`).
4. `active` set and not kept (the terminal was resized since the last screen) → write `E[r`, then `open()` of the new layout. No `E[2J`: `open()` already scrolls what is visible, the old output included, into the scrollback.
5. Write `before()`, run `&band{inner: m, l: *active, out: out}` with `tea.WithContext(ctx)` (inline). Then copy the band's final `l` back into `*active` (it may have been resized while open). `kept()` → write `after()`; otherwise write `E8` and close the split, with no region (`top+1..rows` could be invalid): the action that follows has the whole terminal. Return the band's `inner`, on a run error too.

Locking: `mu` guards `active` and `inScreen` and is **never held across `run`**. Lock, do rules 1–4 and `before()`, set `inScreen = true`, unlock; `run`; lock, set `inScreen = false`, copy back, `after()` or close, unlock. The signal goroutine takes `mu` for its checks and writes. `OutputHeight` is 0 without a split and `max(1, rows - active.top)` with one, `rows` read from the terminal at the call: 0 must keep meaning "not split" to `clampHeight`.

Signals (installed once, in step 2): SIGINT, SIGTERM → when `!inScreen`, `CloseScreen()`, `signal.Reset`, re-raise with `syscall.Kill(os.Getpid(), sig)`; while `inScreen` do nothing (bubbletea handles it). SIGTSTP → when `!inScreen`, write `E7E[rE8`, `signal.Reset(SIGTSTP)`, re-raise. SIGCONT → re-`Notify` SIGTSTP and, when `active != nil`, write `E7` + `E[<top+1>;<rows>r` + `E8`. Put the signal code in `tui/split_unix.go` (`//go:build unix`) with an empty `tui/split_other.go`.

`Pause(w)` returns at once when `session.quiet()`: the process is dying, or `kept()`. A terminal resized during the action pauses: the next screen would scroll the error away.

`main.go`: `defer tui.CloseScreen()` in `main`, and call `tui.CloseScreen()` at the top of `fatalError` (it exits with `os.Exit`).

- [ ] **Step 1: Write the failing tests** `TestSession` in `tui/split_test.go`. Each subtest builds a `session` with a `bytes.Buffer` as `out`, a fake `termSize` and a `run` that records the model and options and returns the model unchanged:

```go
// "split off runs on the alternate screen": split=false → run got a non-band model, out empty, active nil
// "one stream redirected": isTTY false → same as above
// "terminal too small": rows 18 → same as above
// "first screen opens the split": rows 24, size 50 → out == l.open()+l.before()+l.after(); run got a band
// "returns the inner model": the tea.Model returned is the stub, not a band
// "second screen does not reopen": two calls → second adds only before()+after()
// "shrunk below the floor closes the split": 24 rows then 18 → out ends with closeSeq(); active nil; third arg model not a band
// "resized between screens relays out": 24 rows then 40 → out contains "\x1b[r" + the new layout's open(), and no "\x1b[2J"
// "resized while open is read back": run resizes the band to 30 rows → the after() written uses the new cols, OutputHeight() == 18
// "the lock is free during run": run calls SplitActive() and returns → no deadlock
// "OutputHeight": 24 rows, size 50, after a screen → 12; before any screen → 0
// "close without open writes nothing"
// "close twice writes closeSeq once"
```

and in `tui/pause_test.go` (make `Pause`'s terminal check injectable the same way, or test through `screen.active`):

```go
// "silent while the split is active"
// "still pauses once the split fell back" — after the shrink case, the pause path is taken
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run "TestSession|TestPause" -v` — FAIL.
- [ ] **Step 3: Implement** the session, the exported functions and the signal files.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Wire the five sites.** Each becomes `final, err := tui.RunScreen(ctx, m)` (drop the `tea.WithAltScreen()` added earlier; `cmd/log/log.go` passes its `ctx` too). Add `tui.Configure` in `cmd/root.go` and the two `CloseScreen` calls in `main.go`.
- [ ] **Step 6: Run** `mise exec -- go test ./...` — PASS (the rigs have no TTY, so they take rule 1).
- [ ] **Step 7: Terminal check in tmux** (24 rows then 50 rows): `git zf`, `q` → prompt at the top, shell scrolls normally (`seq 1 60`); `git zf`, `i`, Esc, `b`, Esc, `q` → no stacked frames. The tables will be cut at the bottom until Tasks 5–6: expected.
- [ ] **Step 8: Checkpoint.**

---

### Task 4: The home screen keeps its last frame

**Files:**
- Modify: `tui/home.go:177-182`, `tui/home_test.go` (the assertion near line 186 expects `""`)

- [ ] **Step 1: Change the test**: after a key that picks an action (`c`, `i`, `enter`, `r`), `m.View()` is **not** empty and contains the entry labels; add `t.Run("the last frame fits the height", ...)`: after `WindowSizeMsg{80, 10}` and a pick, `lipgloss.Height(m.View()) <= 10`.
- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run TestHomeModel -v` — FAIL.
- [ ] **Step 3: Remove** the `if m.pick.Kind != HomeQuit { return "" }` block and its comment in `homeModel.View`.
- [ ] **Step 4: Run** — PASS; `mise exec -- go test ./cmd/ -run "^TestRunHome" -v` — PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 5: The issue table fits its height

**Files:**
- Modify: `tui/issue.go` (`issueTableHeight` at :27, constructor :258-291, `Update` :463, `openMenu` :592, abandon confirm :626, `View` :861)
- Test: `tui/issue_test.go`

**Interfaces:**
- Produces: `renderPicker(title string, cursor int, options []string, maxLines int) string` (Task 7 uses it). Internal: `m.height int`, `func (m *issueTableModel) below() string` (everything `View` puts under the table for the current state), `func (m *issueTableModel) resize()`.

Rules:
- `height == 0` (no `WindowSizeMsg` yet): 20 rows, as today. Existing tests must pass unchanged.
- Otherwise the table's height is `max(3, height - 1 - lipgloss.Height(below()))`: `SetHeight(H)` makes `table.View()` exactly `H` lines, and `View` joins the table and `below()` with `"\n\n"`, one blank line that `below()` does not include.
- `resize()` runs after every `Update`, whatever path returned: `Update` has many early returns (form, filter, picker, notice, esc), so it is `defer m.resize()` at the top of `Update`, not a call at the bottom.
- `resize()` also re-applies the inline form's height: `updateForm` forwards a `tea.WindowSizeMsg` to `m.form`, and huh then sets the form to the whole height.
- `renderPicker` (`tui/issue.go:845`, shared with the log view) takes a line budget and windows its options around the cursor: `renderPicker(title string, cursor int, options []string, maxLines int) string`, `maxLines <= 0` meaning no limit. The project picker passes what the height leaves.
- The show dialog: `m.dialog.Height = height - 4` (border, blank line, hint), as `logModel` does.
- An inline form (row menu, abandon confirm) is built `.WithHeight(height - 5)` when it would be taller, so table (3) + blank + form fit.
- The same sizing applies full screen: `split = false` gives the terminal's height.

- [ ] **Step 1: Write the failing tests** `TestIssueTable_FitsHeight`, a model with 30 rows, after `tea.WindowSizeMsg{Width: 140, Height: 10}`; each subtest asserts `lipgloss.Height(m.View()) <= 10` and that the hint or input named is still in the view:

```go
// "plain": view contains "[/]-Filter"
// "filtering": after "/" → contains "(esc: clear  enter: confirm)"
// "with a notice": after "a" (armed abandon) → contains the notice
// "row menu open": after enter → contains the menu's first entry
// "project picker open": a model with two projects, after "p" → contains "Pick project"
// "project picker with eight projects": after "p" and seven "down" → <= 10 and the option under the cursor is in the view
// "resize with the row menu open": enter, then WindowSizeMsg{140, 10} again → <= 10
// "show dialog": after the show key → contains "esc: back"
// "taller terminal uses it": WindowSizeMsg{140, 40} → more than 20 data rows visible
// "no size yet keeps 20 rows": no WindowSizeMsg → lipgloss.Height(m.table.View()) as before
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run TestIssueTable_FitsHeight -v` — FAIL.
- [ ] **Step 3: Implement.** Split `View` into the table part and `below()`; do not change the strings.
- [ ] **Step 4: Run** `mise exec -- go test ./tui/... -run "TestIssueTable" -v` and `mise exec -- go test ./cmd/issue/...` — PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 6: The branch table fits its height

**Files:**
- Modify: `tui/branch.go` (`branchTableHeight` :25, constructor :145, `Update` :285, `openForm` :373, `View` :503)
- Test: `tui/branch_test.go`

Same rules and same internal names as Task 5 (`m.height`, `below()`, `resize()`); `branchTableModel` ignores `WindowSizeMsg` today and must store both width and height.

- [ ] **Step 1: Write the failing tests** `TestBranchTable_FitsHeight`, 30 rows, `WindowSizeMsg{130, 10}`, each `lipgloss.Height(m.View()) <= 10`:

```go
// "plain": contains "[/]-Filter"
// "active filters line": after toggling R → contains "remotes: on"
// "filtering": after "/" → contains "(esc: clear  enter: confirm)"
// "S picker open": after "S" → contains the picker's title
// "resize with the S picker open": then WindowSizeMsg{130, 10} again → <= 10
// "armed D notice": after "D" → contains the notice
// "no branch matches": a filter matching nothing → contains "No branch matches."
// "taller terminal uses it": WindowSizeMsg{130, 40} → more than 20 rows visible
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run TestBranchTable_FitsHeight -v` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `mise exec -- go test ./tui/... -run "TestBranchTable" -v` and `mise exec -- go test ./cmd/branch/...` — PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 7: The log view's command picker fits

**Files:**
- Modify: `tui/log.go` (`Update` :217, `View` :476-488)
- Test: `tui/log_test.go`

**Interfaces:**
- Consumes: `renderPicker(title, cursor, options, maxLines)` (Task 5).

Rule: while `m.picking`, the list shrinks to `max(1, size.Height - logChrome - lipgloss.Height(picker))` and is restored when the picker closes; the picker gets `maxLines = size.Height - logChrome - 1`, so with the six default commands it still fits the floor (a band of 10 rows gives the view 9). Nothing else in the log view changes; `L` is untouched (spec: decided).

- [ ] **Step 1: Write the failing test** `TestLogModel_FitsHeight`, six commands, `WindowSizeMsg{100, 9}` (the smallest height a band gives):

```go
// "list": lipgloss.Height(View()) <= 9
// "picker open": after "c" → <= 9 and contains "Pick command"
// "picker follows the cursor": after "c" and five "down" → <= 9 and contains the sixth command's name
// "picker closed restores the list": after "c", esc → list height back to 9-logChrome
// "commit dialog": after enter on a commit line → <= 9
// "search in the dialog": then "/" → <= 9
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run TestLogModel_FitsHeight -v` — FAIL on "picker open".
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `mise exec -- go test ./tui/... -run TestLogModel -v`, `mise exec -- go test ./cmd/log/...` — PASS.
- [ ] **Step 5: Checkpoint.**

---

### Task 8: Forms started as programs stay in the output band

**Files:**
- Modify: `tui/form.go` (`NewForm` :12), `tui/runner.go` (`RunForm` :132)
- Test: `tui/form_test.go` (create if absent)

**Interfaces:**
- Consumes: `OutputHeight()` (Task 3).
- Produces: `func clampHeight(_ tea.Model, msg tea.Msg) tea.Msg` — a `tea.WithFilter` filter: a `tea.WindowSizeMsg` taller than `OutputHeight()` (when > 0) gets `Height: OutputHeight()`; anything else passes through.

`NewForm` adds `.WithProgramOptions(tea.WithFilter(clampHeight))` (no call site sets program options today). `RunForm` adds `tea.WithFilter(clampHeight)` next to `tea.WithOutput(os.Stderr)`; `FormRunner.Update` then sees the clamped height with no change of its own. Forms embedded in the tables are not programs and are not affected (Tasks 5–6 size them).

- [ ] **Step 1: Write the failing test** `TestClampHeight`, driving `screen.active` as Task 3's tests do:

```go
// "no split: unchanged": WindowSizeMsg{80, 40} → same
// "split, taller than the band": active top 20 of 40 rows → Height 20, Width 80
// "split, already smaller": WindowSizeMsg{80, 8} → same
// "other messages pass": a tea.KeyMsg → same value
```

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run TestClampHeight -v` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `mise exec -- go test ./tui/... ./cmd/...` — PASS.
- [ ] **Step 5: Terminal check in tmux, 24 rows**: `git zf`, `c` with a staged file → the commit form and its status panel stay under the separator, the top band is intact while typing and after submit.
- [ ] **Step 6: Checkpoint.**

---

### Task 9: Terminal checklist, docs, cleanup

**Files:**
- Create: `scripts/split-check.sh`
- Modify: `CLAUDE.md` (new section "Testing the split screen"), `website/docs` (the configuration reference page that documents `[log]`), `ROADMAP.md` only if the operator asks
- Delete: `spike/`

`scripts/split-check.sh` builds `./bin/git-zf`, seeds a throwaway repo in `mktemp -d` with one staged file and this `pre-commit` hook, then prints the steps for the person to follow inside tmux (it is a checklist, not an automated test):

```sh
#!/bin/sh
exec < /dev/tty
printf 'hook asks, continue? [y/n] '; read a
[ "$a" = y ] || { echo 'hook: refused'; exit 1; }
echo 'hook: ok'
```

Checklist it prints, each with the expected result:
1. `git zf` → home in the top half, separator, empty bottom.
2. `c`, fill the form → form under the separator; the hook's question appears there; `y` → `hook: ok` and git's summary stay visible; home shows the new state.
3. `c` again, answer `n` → `hook: refused` and `error: commit: …` visible, **no** `[enter] to continue`.
4. `i`, `/`, type, Esc, Esc; `b`, enter, Esc, Esc; `l`, enter, `/`, Esc, Esc, then `c` (the log's command picker), Esc, `q` → every hint line visible, nothing under the separator disturbed. (`c` on the home screen is the commit form; Esc leaves it too.)
5. Resize the pane taller and wider, then shorter than 20 rows, press a key → usable, separator as wide as the pane; next screen full screen when too small. Resize with a table's row menu open → nothing spills under the separator.
6. During an action waiting on the hook: Ctrl-Z → shell scrolls the whole window; `fg` → split back. Ctrl-C → the commit fails, git-zf ends, prompt, whole window scrolls.
7. `q` → git-zf's band gone, the output at the top, prompt under it; `seq 1 60` scrolls normally.
8. `[ui] split = false` in `.git/.git-zf.toml` → full screen, error pauses.
9. `git zf 2>/tmp/err.log`, then `b`, Esc, `q` (not `c`: the commit form draws on stderr, into the file) → full screen, no escape bytes in the file.

- [ ] **Step 1:** Write the script; run it in tmux and go through the checklist. Record every failure; fix in the owning task's files with a test where one is possible. Per the spec, a screen that cannot be confined runs on the alternate screen and is reported to the operator.
- [ ] **Step 2:** Write the CLAUDE.md section (what `RunScreen`, `band`, `clampHeight` do, the rule "a new screen starts through `tui.RunScreen` and must render within its `WindowSizeMsg.Height`", the test commands of Tasks 1–8, the script) and the `[ui]` reference on the website.
- [ ] **Step 3:** `rm -r spike`; `mise exec -- go build ./... && mise exec -- go test ./...`; lint; `node .gitnexus/run.cjs detect-changes --scope all --repo .`.
- [ ] **Step 4:** Hand the checklist to the operator for their own terminal, with the list of what was dropped or degraded, if anything.

## Amendments after execution

What the code does that the tasks above did not say, by date. Each has its test unless noted.

**2026-11-21, during execution and the final review**
- Signals live in `tui/session_unix.go` / `session_other.go`, not `split_unix.go`. SIGQUIT and SIGHUP are handled like SIGINT and SIGTERM. Ctrl-Z stops the process with SIGSTOP (Go keeps its SIGTSTP handler, a re-raised SIGTSTP does not stop it). A `dying` flag makes `runScreen` return `errDying` and `Pause` stay silent once a signal is ending the process (`TestSession_Signals`).
- `after()` sets the region again, and `band` writes `resized` on a change of width too: a terminal may drop the region on any resize.
- `clampHeight` takes `formMargin` (2) off the output band: huh draws a blank line and its help on top of the height it is told. Inline forms are sized in two passes (`fitForm`, `tui/fit.go`).

**2026-11-22, reviews after the checklist**
- `runScreen` rules 3–5, `Pause` and `OutputHeight` as written above (`session.kept`, `layout.fits`): no invalid region after a shrink while a screen is open, no output scrolled away after a grow, a pause after a resize during an action. Tests: "shrunk below the band while open sets no invalid region", "a band resized while open keeps its top for the next screen", "shrunk while open until the output band is too small closes the split", "resized during an action is not quiet", "OutputHeight is never 0 while split" (`tui/session_test.go`).
- Task 6: `branchTableModel` stores the width of a `WindowSizeMsg` as well as its height; the S/T pickers take it ("the pickers take the terminal's width").
- Task 7: `fitList` renders again when the list's height changes, so the selected commit stays in view under the command picker ("picker open keeps the selected commit in view").
- Outside the tasks, decided by the operator: the commit form, its history picker and its "no history" note are built with `tui.Form` (`commit/form.go`), so Esc leaves them. No test for the two dialogs.
- Docs beyond Task 9: the split is described in `README.md`, `git zf --help` (`cmd/root.go`, generated page) and `website/docs/getting-started/first-steps.md`.

**Left as it is**
- With stderr redirected the commit form draws into the file (`tui.RunForm` writes to stderr).
- A width-only resize between two screens lays the terminal out again; an external kill while a form runs leaves the tty raw; `GIT_PAGER=cat` stays set for the process; non-unix builds restore nothing on a signal.
