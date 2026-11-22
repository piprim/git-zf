# Split screen: git-zf in one band, real output in the other

Date: 2026-11-21. Status: implemented (amended 2026-11-22: resize rules,
Esc in the commit form).

## Intent

`git zf` is a series of bubbletea programs (home screen, issue table, branch
table, issue picker, log view). Each one used to leave its last frame on the
terminal, so a session stacked tables on top of one another, and the home
menu jumped to the top of the screen after `s`. The operator called it ugly
and baffling.

A first fix (in the working tree, uncommitted) runs every screen on the
alternate screen. It cleans the display but hides what the commands print.
The operator wants to keep seeing that output, and specifically: the log of
git hooks during a commit and a push, with a hook that reads stdin working
correctly.

So git-zf takes one band of the terminal and the rest stays a real terminal
where commands, hooks and forms run.

## Goals

- The screens of git-zf stay in a fixed band; nothing stacks, nothing jumps.
- Everything a launched command prints appears in the other band, live, on a
  real terminal: git keeps its colours, the spinner works, a hook can read
  stdin.
- Forms (huh) open in the output band.
- On quit, git-zf's band disappears, the output slides to the top of the
  terminal and the shell prompt comes right under it.
- The terminal is always restored: normal quit, Ctrl-C, SIGTERM, panic.
- A config option turns the split off and sets its size. The output is
  always at the bottom.

## Non-goals

- Output at the top (the operator dropped it: bottom only).
- A left/right split. A terminal confines output to a band of rows natively;
  a band of columns would need an embedded terminal emulator (approach B).
- Capturing or replaying output. Nothing is recorded; what scrolled out of
  the output band is gone.
- One bubbletea program hosting every screen. The screens stay separate
  programs, run one after the other as today.

## Anti-goals

- A terminal left with a scroll region set after git-zf exits. This breaks
  the operator's shell until `reset` and is worse than the original bug.
- Output of a hook or a command hidden or swallowed in split mode.
- Any change to what the screens do, their keys, or the non-TTY paths
  (tables printed to stdout, help without a terminal). The one change
  allowed is the height a screen takes (see "The screens fit their height").

## Approaches

**A. Scroll region (picked).** git-zf sets the terminal's scroll region
(DECSTBM) to the output band and draws its screens inline in the remaining
rows. Commands run with the real terminal; the terminal itself keeps their
output inside the band. No new dependency. Top/bottom only. The risk is
bubbletea's renderer, which was not written for this; the spike below shows
it cooperates under two conditions.

**B. Embedded terminal pane.** Each action runs as a child process in a
pseudo-terminal and git-zf renders it in a pane, like a small tmux. Allows
left/right and keeps scrollback, but needs a pty library and a terminal
emulator, keystroke forwarding, and one program hosting every screen: a
rewrite of how screens and actions run. Rejected for its size.

**C. Log panel.** Tee the output into memory and show its last lines in a
panel at the bottom of each screen. Rejected: a hook that reads stdin has no
terminal to ask on, and a tee turns off the spinner and git's colours.

The operator picked A, output at the bottom only.

## What the spike showed

`spike/main.go` (throwaway, deleted since) ran in tmux and in the
operator's terminal:

- A wrapper model confines any screen: it rewrites `tea.WindowSizeMsg.Height`
  to the band's height and renders exactly that many lines. With a constant
  line count bubbletea's inline renderer never emits "erase below", so it
  never touches the output band.
- bubbletea erases the last line of the view when a program stops, and
  repaints it blank while running. The band's last row is therefore the
  separator: part of the wrapped view, redrawn by the runner after exit.
- Output scrolling, a child process reading stdin, git seeing a terminal, a
  huh confirm, and terminal restoration after quit all worked with git-zf on
  top.

Not covered by the spike, to be verified during implementation: resize, a
form taller than the output band, a pager, the quit sequence, and the real
screens inside the wrapper. The spike's model was a toy: the real screens
need the changes of "The screens fit their height" before they hold in a
band.

## Design

### Config

```toml
[ui]
split = true   # false: every screen full screen, on the alternate screen
size  = 50     # percent of the rows for git-zf, the output gets the rest
```

git-zf is on top, the output at the bottom; there is no option for the
side. [My call: a boolean, since "bottom" and "off" are the only values
left.] A size out of 20–80 is a config error, validated like `[log]` is.

### One entry point for every screen

The five places that start a screen today (`cmd/home.go`,
`cmd/issue/list.go`, `cmd/issue/record.go`, `cmd/branch/table.go`,
`cmd/log/log.go`) call one function instead of `tea.NewProgram(...).Run()`.
It runs the model full screen or in the band, and returns the *inner* final
model, so `tui.PickedHome`, `PickedIssue`, `PickedBranch` and the table
state helpers keep working unchanged.

The split applies to every screen, whether opened from the home screen or
directly (`git zf log`, `git zf issue`, `git zf branch`). [My call, for
consistency: the log view also launches actions (`L` pushes).]

### Split mode

- First screen of the process: scroll what is visible into the scrollback
  (newlines, not an erase: the operator's previous commands are not lost),
  set the scroll region to the output band, park the output cursor at the
  band's first row.
- Before a screen: save the output cursor, move to the first row of git-zf's
  band, run the wrapped model inline. After it: redraw the separator,
  restore the output cursor.
- Between screens the previous frame stays painted in its band while the
  action runs underneath. There is no flash. `homeModel.View` returns `""`
  today once an action is picked (`tui/home.go`, written for the inline
  days); in a band that would blank it, so the last frame is kept.
- Quit: reset the scroll region, delete the rows of git-zf's band so the
  output slides to the top, leave the cursor on the line after the output.
  Resetting the region homes the cursor, so the output cursor is saved
  first: `ESC 7`, `ESC [ r`, `ESC [ 1;1 H`, `ESC [ <band> M`, `ESC 8`,
  `ESC [ <band> A`. Confirmed in tmux.
- Resize. The active layout is *kept* while the terminal has the size it
  was built for and both bands have their 10 rows (`session.kept`):
  - while a screen is open, the band keeps its number of rows and the
    output band takes the difference; the region is set again on every
    resize, a change of width included. The band returns to the configured
    percentage at the next layout.
  - a screen that ends on a layout that is no longer kept (the output band
    under 10 rows, the terminal under the floor) closes the split without
    writing a region, which could be invalid (top under bottom): the
    action runs on the whole terminal and the next screen splits afresh.
  - a terminal resized between two screens, during an action, is laid out
    again by the next screen: what it shows is scrolled into the
    scrollback.
- Restoration runs on every exit path: return from the root command, SIGINT
  and SIGTERM during an action, panic.

### Off mode and fallback

`split = false` is what the working tree does now: each screen on the
alternate screen, and `tui.Pause` (`[enter] to continue`) after an error
printed between two screens. Hook output prints live on the normal terminal
during an action and is covered when the screen returns unless the action
failed.

Split mode falls back to off mode when either band would get fewer than 10
rows [my call], and is used only when stdin, stdout and stderr are all
terminals: the screens draw on stdout, the commit form (`tui.RunForm`) and
the spinner on stderr, and a redirected stream would desynchronize them. Above that
floor a form is squeezed into the output band and scrolls there; it never
switches to full screen. `tui.Pause` does nothing
while the layout is kept: the error stays visible in the output band. It
asks the session, not the config: it pauses on a terminal that fell back to
off mode, and on one resized since the last screen, whose next screen would
scroll the error away.

### The screens fit their height

The wrapper cuts what exceeds the band; a screen that ignores its height
loses its bottom lines (hints, filter input, notices, inline menus). Today:

- `issueTableModel` and `branchTableModel` have a fixed table of 20 rows
  (`issueTableHeight`, `branchTableHeight`), about 27 lines with their
  chrome, and do not size from `tea.WindowSizeMsg.Height`. They must: the
  table shrinks to what the height leaves after the chrome, the notice and
  any open inline form (`tui.Form` is also used *inside* the tables, for the
  row menu, the abandon confirm and the S/T pickers, and shares the band's
  height with the table).
- `logModel` already sizes its list and dialogs; only its `c` picker is
  appended under the list and must fit as well.
- `homeModel` already fits (`fit`, `cutLines`).

The same sizing applies full screen: with `split = false` the tables use the
terminal's height instead of 20 rows. [My call: one code path.]

Lines wider than the terminal need nothing: bubbletea's renderer truncates
every line to the terminal's width (`standard_renderer.go`, `flush`).

### Forms and pagers

A bubbletea program taller than the output band garbles it (its lines scroll
the region, then its cursor-up is clamped at the region's top). In split
mode every form must fit the output band: `tui.NewForm` / `tui.Form` (the
only constructors, per CLAUDE.md) and `tui.RunForm` (the commit form with
its status panel) are given the band's height. `FormRunner.Update` takes its
height from `tea.WindowSizeMsg`, which is the whole terminal's: it must be
clamped to the output band's. The height a form is told is never 0 while
the terminal is split, however small the output band got: 0 means "not
split" to the clamp. A child `git` must not start
a full-screen pager inside the band.

Esc leaves every form opened from a screen, the commit form, its history
picker and its "no history" note included (`tui.Form`): from the home
screen `c` must return like the other keys do. [Decided by the operator;
the cost is a typed message lost on a stray Esc.] With stderr redirected
the commit form draws into the file (it has drawn on stderr since the
commit history); that is left as it is.

### Full-screen children and suspension

A full-screen child (huh's text fields can open `$EDITOR`) may reset the
scroll region when it exits; what the action prints afterwards then scrolls
the whole terminal, git-zf's band included. This is accepted [my call]: the
region is set again at every screen start, which repaints the band. The
editor binding stays.

Ctrl-Z during an action must not hand the operator a shell with the region
still set: git-zf resets the region before it stops and sets it again when
it continues [my call: in scope, it is the first anti-goal]. This concerns
the time between two screens: while a bubbletea program runs the terminal
is raw and Ctrl-Z is a key, which no git-zf screen binds to a suspend.

### The log view's `L`

[Decided by the operator.] `L` stays as it is: it pushes from inside the
running log program with captured output (`git.Client.PushLinearized`, `git
push --quiet`), so a pre-push hook's output is not shown and a hook reading
stdin cannot work there. It is the one exception to "no hook output
hidden". Making `L` quit the log view and push with the terminal free is
not part of this work.

### What may be dropped

A screen that cannot be confined to the band runs on the alternate screen instead, and that is reported
to the operator. [My call.]

## Constraints

- bubbletea v1.3.6, huh v1.0.0; no new dependency.
- The spinner rules of CLAUDE.md hold: stop before a form or a program,
  never keep a writer across a `Spin`.
- Tests follow the repo's levels: pure functions and models unit-tested with
  `t.Run` per assertion; the E2E rigs have no terminal and must keep taking
  the non-split path untouched.

## Testing

- The wrapper model: height reported to the inner model, exact line count
  for short and tall views, separator on the last row, inner final model
  returned.
- The screens at a small height (10 rows): the two tables never render more
  lines than given, with a filter, a notice or an inline menu open; the log
  view with its `c` picker open; the home screen's last frame after a pick
  is not empty.
- `FormRunner` with a clamped height.
- The layout as a pure function (rows, size → the two bands, or
  "fall back") and the enter/leave/quit sequences as golden strings.
- Config: defaults, `split = false`, invalid size.
- A tmux script replaying the spike's steps on the real binary, in a seeded
  repo with a pre-commit hook that reads stdin: band stable, output
  below, prompt answered, form, quit display, shell scrolling normally
  afterwards, Ctrl-C and Ctrl-Z during an action. It is a checklist run by
  hand during the work and by the operator in their terminal, not part of
  `go test` [my call: the repo has no terminal harness and building one is a
  project of its own].

## Left to the builder

- Package and names (the wrapper, the entry point, where the session state
  lives).
- How the band height reaches the forms.
- How the pager is kept out (`GIT_PAGER`, `--no-pager`, or showing that no
  launched command pages).
- The resize mechanism, within the requirement above (the wrapper setting
  the region again on `tea.WindowSizeMsg` is the expected shape).
- Docs: `website/docs`, CLAUDE.md's testing section, deleting `spike/`.
