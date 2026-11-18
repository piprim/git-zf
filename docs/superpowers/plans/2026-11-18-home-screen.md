# Home Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bare `git zf` opens a home screen (status header, actions with a letter key and a live hint, `s` status view, `r` refresh) that every command returns to.

**Architecture:** A pure bubbletea model in `tui/` renders data it is given and ends with a pick. `cmd/home.go` gathers the data from one `git status` and the three chain listings, and loops: show, run the pick, show again. The tables and the log learn that Esc at their top level closes them; `cmdutil.RunMenu` (now only `git zf review`) gets the same loop.

**Tech Stack:** Go (run through `mise exec -- go …`), cobra v1.1.3, bubbletea, bubbles/viewport, lipgloss, huh.

**Spec:** `docs/superpowers/specs/2026-11-18-home-screen-design.md` — read it first; this plan does not repeat its reasons.

## Global Constraints

- Do not commit: the user commits themself. Each task ends with its tests green and the tree left modified.
- Before editing any existing function, run `node .gitnexus/run.cjs impact "<symbol>" --direction upstream --repo .` and report callers and risk; warn on HIGH/CRITICAL. Before handing back, run `node .gitnexus/run.cjs detect-changes --scope all --repo .`.
- Every test scenario is a named `t.Run`, single-assertion tests included.
- Each display of the home screen is local git work only. Only `r` touches the network. The tracker is never called.
- Keys: `c` commit, `i` issue, `b` branch, `v` review, `l` log, `s` status, `r` refresh, `q` and ctrl+c quit. Esc on the home menu does nothing.
- Without a terminal on stdin: help only, nothing on stderr, checked before the repository.
- With a terminal, outside a repository: `not a git repository` on stderr, then the help, exit 0.
- Forms are built with `tui.Form`, never `huh.NewForm`. Stop a spinner before any bubbletea program; never keep a writer read from `IO()` across a `Spin` or its `stop`.
- No new dependency.

## Review Focus

1. **A repository with no commit yet.** `git status --branch` reports `branch.oid (initial)`; the home screen opens and shows the branch name. Test in Task 1.
2. **An upstream that is gone** (remote branch deleted). git prints `branch.upstream` without `branch.ab`; the upstream part is shown without counts, nothing crashes. Test in Task 1 and Task 2.
3. **A terminal narrower than the header.** Each header line is cut to the width with `…`, never wrapped. Test in Task 2.
4. **A command that changes the branch** (`issue start`, a checkout from the branch table). The next display shows the new branch: the data is gathered again on every display, with a fresh client. Test in Task 7.
5. **`git zf` typed in a linked worktree.** The header describes that worktree (its branch, its files), not the main tree. Test in Task 4.

---

### Task 1: Branch facts from one `git status`

**Files:**
- Modify: `git/status.go`
- Test: `git/status_test.go`

**Interfaces:**
- Produces:

```go
// BranchStatus is what the `#` header lines of `git status --porcelain=v2 --branch` say.
type BranchStatus struct {
	Head     string // branch name; "" when detached
	OID      string // full hash of HEAD; "" before the first commit
	Upstream string // "origin/main"; "" without an upstream
	HasAB    bool   // false when the upstream is gone: Ahead and Behind mean nothing
	Ahead    int
	Behind   int
}

func (c *Client) Status(ctx context.Context) (BranchStatus, []StatusEntry, error)
func parseBranchHeaders(raw string) BranchStatus
```

`StatusEntries` and `parsePorcelainV2` keep their behaviour.

- [ ] **Step 1: Write the failing tests** in `git/status_test.go`

`TestParseBranchHeaders`, table-driven, one `t.Run` per row:

| name | input header lines | want |
|---|---|---|
| branch with upstream | `# branch.oid abc…`, `# branch.head main`, `# branch.upstream origin/main`, `# branch.ab +2 -1` | `Head:"main", OID:"abc…", Upstream:"origin/main", HasAB:true, Ahead:2, Behind:1` |
| no upstream | oid + `# branch.head wip` | `Head:"wip"`, `Upstream:""`, `HasAB:false` |
| upstream gone | oid, head, upstream, no `branch.ab` | `Upstream:"origin/x"`, `HasAB:false` |
| detached | `# branch.head (detached)` | `Head:""`, `OID` set |
| no commit yet | `# branch.oid (initial)`, `# branch.head main` | `Head:"main"`, `OID:""` |
| entries are ignored | headers followed by `1 .M …` and `? new.txt` | same as row 1 |

`TestStatus` on a real repo (same rig as `TestStatusEntries`): "reports the branch and the entries" (one modified file → `Head` is the branch, one entry `" M"`); "reports ahead and behind of the upstream" (clone of a bare origin, one local commit, one commit pushed from a second clone then fetched → `Ahead:1, Behind:1`).

- [ ] **Step 2: Run** `mise exec -- go test ./git/... -run "TestParseBranchHeaders|TestStatus$" -v` — expect a compile failure.
- [ ] **Step 3: Implement** `parseBranchHeaders` (lines starting with `# branch.`) and `Status` (one `git status --porcelain=v2 --branch`, entries through `parsePorcelainV2`).
- [ ] **Step 4: Run** the same command plus `-run "TestParsePorcelainV2|TestStatusEntries"` — expect PASS.

---

### Task 2: The home model

**Files:**
- Create: `tui/home.go`, `tui/home_test.go`
- Modify: `tui/status_panel.go` (add `CountStatus`)
- Test: `tui/status_panel_test.go` (or the file that already tests `groupEntries`)

**Interfaces:**
- Consumes: `git.StatusEntry`, `groupEntries(entries, false, false)`, `StatusPanel`, `keyHints`.
- Produces:

```go
// StatusCounts are the sizes of the groups the status panel shows.
type StatusCounts struct{ Staged, Modified, Untracked, Unmerged int }

func CountStatus(entries []git.StatusEntry) StatusCounts // groupEntries(entries, false, false)

type HomeData struct {
	Prog     string            // "git-zf"
	Branch   string            // "42@feat@login", "detached at abc1234", "" when unknown
	Issue    string            // "42 Login form rejects unicode", "" when none
	Upstream string            // "origin/main", "" when none
	HasAB    bool
	Ahead    int
	Behind   int
	Review   string            // "in review", "approved", "changes requested", ""
	Entries  []git.StatusEntry // the working tree, for the counts and the status view
}

type HomeEntry struct{ Key, Name, Label, Hint string } // "c", "commit", "Commit", "3 staged files"

type HomePickKind int

const (
	HomeQuit HomePickKind = iota
	HomeRun
	HomeRefresh
)

type HomePick struct {
	Kind HomePickKind
	Name string // the entry's Name for HomeRun
}

// cursor is the Name of the entry to start on; "" applies the first-display rule.
func NewHomeModel(data HomeData, entries []HomeEntry, cursor string) tea.Model
func PickedHome(m tea.Model) HomePick // HomeQuit for a model that is not a home model
```

Rendering rules the tests pin:

- Line 1: `Prog`, two spaces, `Branch`, then ` · ` + `Issue` when set.
- Line 2, parts joined by ` · `, a part left out when empty:
  - tree: `3 staged, 2 modified, 1 untracked, 1 unmerged`, zero counts left out; `clean` when all four are zero;
  - upstream: `↑2 ↓0 origin` (the remote is `Upstream` up to its first `/`) when `HasAB`; `origin (gone)` when `Upstream != ""` and `!HasAB`; nothing when `Upstream == ""`;
  - review: `review: ` + `Review`.
- One line per entry: `Key`, `Label`, `Hint`, the cursor's line marked with `>`.
- Footer through `keyHints`: `↑/↓ move`, `enter run`, `s status`, `r refresh`, `q quit`.
- After a `tea.WindowSizeMsg`, no rendered line is wider than the width: a longer one is cut with `ansi.Truncate(line, width, "…")` (`github.com/charmbracelet/x/ansi`, already imported in `tui`: it counts display cells and skips colour codes).
- Status mode: a `viewport` holding the line-2 text, a blank line, then `StatusPanel(Entries, false, false)`; when that is `""`, the line `Nothing to commit, working tree clean`. Footer `↑/↓ scroll`, `esc back`. The viewport is created with `logDefaultWidth` and `logDefaultHeight` (`tui/log.go`), not zero, so it renders before the first `tea.WindowSizeMsg`: the tests press `s` without sending one.
- The model runs inline (no alt screen).

- [ ] **Step 1: Write the failing tests**

`TestCountStatus` (table, `t.Run` per row): `"M "`→Staged 1; `" M"`→Modified 1; `"MM"`→Staged 1 and Modified 1; `" D"`→Modified 1; `"??"`→Untracked 1; `"UU"`→Unmerged 1; no entry→all zero.

`TestHomeModel_Keys` — helper `home(entries…)` builds a model with the five entries (`c commit`, `i issue`, `b branch`, `v review`, `l log`); `runeKey` already exists in `tui/branch_test.go`:

- "a letter runs its command": `v` → `PickedHome == {HomeRun, "review"}` and the returned `tea.Cmd` is `tea.Quit` (compare with `cmd() == tea.Quit()`).
- "enter runs the entry under the cursor": cursor `"log"`, enter → `{HomeRun, "log"}`.
- "down then enter runs the next entry": cursor `"commit"`, down, enter → `{HomeRun, "issue"}`.
- "r asks for a refresh": `{HomeRefresh, ""}`.
- "q quits" and "ctrl+c quits": `{HomeQuit, ""}` with a quit command.
- "esc on the menu does nothing": the returned command is nil and a following `c` still gives `{HomeRun, "commit"}`.
- "s opens the status view and esc returns": after `s` the view contains `Nothing to commit, working tree clean` (no entries); `c` in that mode picks nothing; after esc, `c` gives `{HomeRun, "commit"}`.
- "q in the status view returns to the menu": after `s`, `q` returns a nil command and the view shows the entries again.

`TestHomeModel_Cursor`: "starts on commit with staged files" (`"M "`), "starts on commit with modified files", "starts on commit with unmerged files", "starts on issue on a clean tree", "starts on issue with untracked files only", "starts on the given entry" (cursor `"branch"`) — each presses enter and checks the pick's Name.

`TestHomeModel_View` (strip ANSI before comparing; `tui/log.go` has the stripper `commitHash` uses):

- "full header": data of the spec's sketch → line 1 contains `git-zf  42@feat@login-form · 42 Login form rejects unicode`, line 2 contains `3 staged, 2 modified, 1 untracked · ↑2 ↓0 origin · review: changes requested`.
- "clean tree without upstream or review": line 2 is `clean` alone.
- "upstream gone": line 2 contains `origin (gone)` and no `↑`.
- "no issue": line 1 has no ` · `.
- "hint left out": an entry with `Hint: ""` renders its label with no trailing text.
- "narrow terminal cuts the header": `tea.WindowSizeMsg{Width: 30}` → every line of `View()` is at most 30 cells (`lipgloss.Width`) and line 1 ends with `…`.
- "status view lists the files": entries `{XY: "M ", Path: "a.go"}`, `{XY: "??", Path: "b.go"}` (two characters, no trailing space), after `s` the view contains `a.go`, `b.go` and `Untracked`.

- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run "TestCountStatus|TestHomeModel" -v` — expect a compile failure.
- [ ] **Step 3: Implement** `CountStatus`, then `tui/home.go`: a model with `menu` and `status` modes; the reserved keys are handled before the entry keys; `PickedHome` reads the pick off the final model.
- [ ] **Step 4: Run** the same command — expect PASS. Then `mise exec -- go test ./tui/...` — expect PASS.

---

### Task 3: Quiet fetches

**Files:**
- Modify: `git/merge.go` (`Fetch`), `issue/repo.go` (`Fetch`), `branch/repo.go` (`Fetch`)
- Test: `git/merge_test.go` (or where `Fetch` is tested), `issue/repo_test.go`, `branch/repo_test.go`

**Interfaces:**
- Produces, each sharing a private body with its existing sibling so no caller changes:

```go
func (c *Client) FetchQuiet(ctx context.Context) error        // git: `git fetch <remote>` through remoteCmd(ctx, true, …)
func FetchQuiet(ctx context.Context, c *git.Client) (merged int, err error) // package issue
func FetchQuiet(ctx context.Context, c *git.Client) error      // package branch
```

`review.Fetch(ctx, c, true)` already exists.

- [ ] **Step 1: Write the failing tests**, one per package, each a `Test…FetchQuiet` with two `t.Run`:
  - "fetches like Fetch": two clones of one bare origin; a chain (or a commit, for `git`) pushed from the first is visible in the second after `FetchQuiet`.
  - "prints nothing when the remote is down": the client's `IO` has buffers for `Out` and `Err`; the remote URL is set to a path that does not exist; `FetchQuiet` returns an error and both buffers are empty.
- [ ] **Step 2: Run** `mise exec -- go test ./git/... ./issue/... ./branch/... -run "FetchQuiet" -v` — expect a compile failure.
- [ ] **Step 3: Implement** by extracting the body of each `Fetch` into a private function taking `silent bool`.
- [ ] **Step 4: Run** the same command, then `mise exec -- go test ./git/... ./issue/... ./branch/...` — expect PASS.

---

### Task 4: Gathering the home data, and the refresh

**Files:**
- Create: `cmd/home_data.go`, `cmd/home_data_test.go`

**Interfaces:**
- Consumes: `git.Client.Status` (Task 1); `tui.HomeData`, `tui.HomeEntry`, `tui.CountStatus` (Task 2); the `FetchQuiet` functions (Task 3); `issue.List`, `branch.List`, `branch.Rows`, `review.List`, `branch.CutReviewSuffix`, `issueflow.PrintWarnings`.
- Produces:

```go
// homeKeys maps a rootMenu command name to its key.
var homeKeys = map[string]string{"commit": "c", "issue": "i", "branch": "b", "review": "v", "log": "l"}

// gatherHome reads what the home screen shows. It never fails: a read that
// fails is a warning on c.IO().Err and what depends on it is left out.
func gatherHome(ctx context.Context, c *git.Client, prog string) (tui.HomeData, map[string]string) // hints by command name

// homeEntries builds the entries from the menu's subcommands: Label is
// tui.TitleCase(Name), Key from homeKeys, Hint from hints.
func homeEntries(subs []*cobra.Command, hints map[string]string) []tui.HomeEntry

// refreshHome fetches the remote quietly: the branches, then the issue,
// branch and review chains. The first failure is one warning line on
// c.IO().Err: "warning: could not fetch, showing local data: <err>".
// git.ErrForeignChain is not a failure. No-op without a remote.
func refreshHome(ctx context.Context, c *git.Client)
```

Rules the tests pin:

- `Branch`: `BranchStatus.Head`; `detached at <first 7 of OID>` when `Head == ""`.
- The slug of the current branch: `branch.CutReviewSuffix(head)` when it is a review branch; else the `Slug` of the state in `branch.List` whose `Entry(head)` is not nil; else none.
- `Issue`: the record of `issue.List` whose `ID` is the state's `IssueID`, else the one whose `DisplayID()` is the slug → `DisplayID() + " " + Title`. Without a record: the state's `Slug + " " + Title` (the state of that slug, found by `Slug` on a review branch). Else `""`.
- `Review`: the state of `review.List` with that slug, when `!Closed && Status != ""` → `Status` with `_` replaced by a space.
- Hints: `commit` → `N staged files`, else `N modified files` (`file` at 1); `issue` → `N open` (`State == issue.StateOpen`); `branch` → `N in progress` (`len(branch.Rows(states, branch.StatusInProgress))`); `review` → `N in review` (`!Closed && Status == review.StatusInReview`). A zero count has no key in the map.
- `gatherHome` wraps its reads in `c.IO().Spin("Reading the repository")`. It collects the warnings while it spins and writes them after `stop()`, reading `c.IO().Err` at that moment (`issueflow.PrintWarnings(c.IO().Err, warnings)`): a writer read before a `Spin` or a `stop` is stale. `refreshHome` spins on `"Fetching " + remote` and writes its warning line the same way, after `stop()`.
- No `branch.Find`, `branch.ListRows`, or any `Load`.

- [ ] **Step 1: Write the failing tests** in `cmd/home_data_test.go`. A helper `homeRig(t)` makes a repo in `t.TempDir()` with one commit and a `git.NewClientAt` whose `IO.Err` is a buffer. `TestGatherHome`:

  - "clean tree on a plain branch": `Branch` is the branch, `Issue == ""`, `len(Entries) == 0`, no `commit` hint.
  - "staged and modified again": a file staged then edited → `tui.CountStatus(d.Entries)` is `{Staged:1, Modified:1}`, hint `commit` is `1 staged file`.
  - "an unmerged file": a conflicting merge left in progress → `Unmerged == 1`.
  - "tracked branch with an issue record": `issue.Create` a record titled `Login form`, `branchtest.Seed` an in-progress branch for it with `IssueID` set, check it out → `Issue == rec.DisplayID()+" Login form"`, hints `issue: "1 open"`, `branch: "1 in progress"`.
  - "the record's title follows an edit": append a title edit to that record → `Issue` ends with the new title.
  - "tracked branch without a record": `branchtest.Seed` with slug `77`, title `From the tracker`, no issue → `Issue == "77 From the tracker"`.
  - "a review branch": on `<slug>@review` with `reviewtest.Seed(t, c, slug, review.StatusInReview, 1, sha)` → `Issue` as on the feature branch, `Review == "in review"`, hint `review: "1 in review"`.
  - "a closed review is ignored": append a close op to that review → `Review == ""`, no `review` hint.
  - "changes requested": status `changes_requested` → `Review == "changes requested"`.
  - "upstream ahead and behind": as in Task 1 → `Upstream == "origin/<branch>"`, `HasAB`, `Ahead == 1`, `Behind == 1`.
  - "detached HEAD": `git checkout --detach` → `Branch` starts with `detached at ` and is 19 characters long.
  - "no commit yet": a fresh `git init` → `Branch` is the initial branch's name, no warning in the `Err` buffer.
  - "a linked worktree": `git worktree add` on another branch, a client opened there with an untracked file → `Branch` is the worktree's branch and `Untracked == 1`, while the main tree's client reports a clean tree.

  `TestHomeEntries`: "keys, labels and hints" — subs `commit`, `log`, hints `{"commit": "2 staged files"}` → `[{c commit Commit 2 staged files} {l log Log }]`.

  `TestRefreshHome`: "fetches what the remote has" (two clones: an issue created and pushed in the first shows up in `issue.List` of the second after `refreshHome`); "a remote that is down gives one warning" (remote URL set to a missing path → the `Err` buffer holds exactly one line, starting with `warning: could not fetch, showing local data:`); "the next refresh reaches a remote that is back" (same repo, URL restored, a **new** client → no warning, the issue shows up); "no remote is a no-op" (empty `Err` buffer).

- [ ] **Step 2: Run** `mise exec -- go test ./cmd/ -run "TestGatherHome|TestHomeEntries|TestRefreshHome" -v` — expect a compile failure.
- [ ] **Step 3: Implement** `cmd/home_data.go`.
- [ ] **Step 4: Run** the same command — expect PASS.

---

### Task 5: Esc closes a table or the log at its top level

**Files:**
- Modify: `tui/issue.go` (`Update`, around line 472), `tui/branch.go` (`Update`, around line 285), `tui/log.go` (`updateKey`)
- Test: `tui/issue_test.go`, `tui/branch_test.go`, `tui/log_test.go`

**Interfaces:**
- Produces: no new symbol. Esc in normal mode (no dialog, form, menu, picker, filter input, pending question), in this order: an armed key (`armAbandon`, `armKey`) is disarmed and its notice cleared, the view stays; else an applied text filter (`filter.Value() != ""`) is reset and the rows refreshed, the view stays; else the view closes as `q` does (`IssuePickNone`, `BranchPickNone`, `tea.Quit`). The check runs before the tables clear the armed key.

- [ ] **Step 1: Write the failing tests** — `TestIssueTable_Esc`, `TestBranchTable_Esc`, `TestLogModel_Esc`, each `t.Run`:
  - "esc at the top level closes the view": the command is a quit and the pick is the `None` one.
  - "esc clears an applied filter first": `/`, type a text matching one row, enter, esc → no quit command and every row is visible again; a second esc quits.
  - "esc disarms a pending abandon" (issue: `a`; branch: `a` and `D` as two subtests): after `a`, esc → no quit command, the notice is empty, and a following `a` arms again instead of acting (the pick stays `None`).
  - "esc in the filter input still only leaves the input": `/`, type, esc → no quit.
  - log only: "esc on a pending question still answers no" (the existing linearize case stays green).
- [ ] **Step 2: Run** `mise exec -- go test ./tui/... -run "_Esc" -v` — expect FAIL on the first three cases.
- [ ] **Step 3: Implement** the rule in the three models. Add `esc` to nothing in the hints: `q quit` stays the documented key.
- [ ] **Step 4: Run** `mise exec -- go test ./tui/... ./cmd/issue/... ./cmd/branch/... ./cmd/log/...` — expect PASS.

---

### Task 6: `RunMenu` loops

**Files:**
- Modify: `cmd/cmdutil/menu.go` (`RunMenu`)
- Test: `cmd/cmdutil/menu_test.go`

**Interfaces:**
- Produces: same signature. After the picked `RunE` returns, the menu is shown again: `huh.ErrUserAborted` from the action is ignored; any other error is printed as `error: <picked>: <err>` on `cmd.ErrOrStderr()`. The loop ends when `Select` reports `huh.ErrUserAborted` (nil), `ErrMenuUnavailable` (help, nil) or another error (returned wrapped, as today). A pick that matches no subcommand, or one without `RunE`, is still returned as an error.

- [ ] **Step 1: Update the tests.** `scriptedMenuPrompter` takes a list of answers (`picks []string`, then `huh.ErrUserAborted` once exhausted, unless `err` is set) and counts its calls. Change:
  - "dispatches to the picked subcommand with the parent command": picks `["commit"]` → the sub ran once with the parent, `Select` was called twice, `RunMenu` returns nil.
  - "propagates the subcommand error…" becomes "prints the action's error and shows the menu again": the sub returns `errors.New("boom")` → nil returned, the parent's stderr holds `error: commit: boom`, `Select` called twice.
  - New "an aborted action is not an error": the sub returns `huh.ErrUserAborted` → nothing printed, `Select` called twice.
  - New "runs several actions in a row": picks `["a", "b"]` → both ran, in order.
  - The other cases keep their assertions.
- [ ] **Step 2: Run** `mise exec -- go test ./cmd/cmdutil/... -run "^TestRunMenu" -v` — expect FAIL.
- [ ] **Step 3: Implement** the loop in `RunMenu`; update its doc comment.
- [ ] **Step 4: Run** `mise exec -- go test ./cmd/cmdutil/... ./cmd/review/...` — expect PASS.

---

### Task 7: The loop and the root command

**Files:**
- Create: `cmd/home.go`, `cmd/home_test.go`
- Modify: `cmd/root.go` (root `RunE`, `Long`), `cmd/root_test.go`, `CLAUDE.md` ("Testing the menus"), and any page under `website/docs` that describes the root menu (`grep -rn "opens a menu" website/docs`)

**Interfaces:**
- Consumes: `gatherHome`, `homeEntries`, `refreshHome` (Task 4); `tui.NewHomeModel`, `tui.PickedHome`, `tui.HomePick` (Task 2); `menuSubs(root)`.
- Produces:

```go
type homePrompter interface {
	// Pick shows the home screen and returns what the user chose.
	Pick(ctx context.Context, data tui.HomeData, entries []tui.HomeEntry, cursor string) (tui.HomePick, error)
}

type homeDeps struct {
	prompter homePrompter
	isTTY    func() bool                                  // production: term.IsTerminal(int(os.Stdin.Fd()))
	open     func(cmd *cobra.Command) (*git.Client, error) // production: cmdutil.NewClientForCmd(cmd, appConfig)
}

func runHome(cmd *cobra.Command, prog string, subs []*cobra.Command, d homeDeps) error
```

`runHome`, in this order:

1. `!d.isTTY()` → `cmd.Help()`, return nil. `open` is not called.
2. `d.open(cmd)` fails → `not a git repository` on `cmd.ErrOrStderr()`, `cmd.Help()`, return nil. The prompter is not asked.
3. Loop. Each turn opens a **fresh** client with `d.open`, calls `gatherHome`, then `d.prompter.Pick` with the cursor (`""` on the first turn):
   - `HomeQuit` → return nil.
   - `HomeRefresh` → `refreshHome` with this turn's client; next turn.
   - `HomeRun` → the sub's `RunE(cmd, nil)`, as `RunMenu` does; `huh.ErrUserAborted` ignored, another error printed as `error: <name>: <err>` on `cmd.ErrOrStderr()`; the cursor becomes that name; next turn.
   - A prompter error → returned wrapped.

The production prompter runs `tea.NewProgram(tui.NewHomeModel(…), tea.WithContext(ctx))` inline and returns `tui.PickedHome(final)`.

- [ ] **Step 1: Write the failing tests** in `cmd/home_test.go`. `scriptedHomePrompter` returns a list of picks in order (then `HomeQuit`) and records the `data` and `cursor` of each call. `homeCmd(t, subs…)` returns a parent command primed with a context, as `newParent` in `cmd/cmdutil/menu_test.go` does, and its two buffers: unlike `newParent`, `SetOut` and `SetErr` get a buffer each, since the cases below assert on stdout and stderr separately. `TestRunHome`:
  - "without a terminal the help alone is printed": `isTTY` false, `open` fails the test if called → stdout holds the help, stderr is empty, the prompter was not asked.
  - "outside a repository the line and the help are printed": `isTTY` true, `open` returns an error → stderr is `not a git repository\n`, stdout holds the help, nil returned, the prompter was not asked.
  - "runs the picked command with the parent and asks again": picks `[{HomeRun, "commit"}]` → the sub ran once with the parent command, the prompter was asked twice, the second cursor is `"commit"`.
  - "the first cursor is empty": the first recorded cursor is `""`.
  - "an error is printed and the screen comes back": the sub returns `errors.New("boom")` → stderr holds `error: commit: boom`, prompter asked twice, nil returned.
  - "an aborted command is not an error": the sub returns `huh.ErrUserAborted` → stderr empty.
  - "quit ends the loop": picks `[]` → prompter asked once, no sub ran.
  - "refresh gathers again": picks `[{HomeRefresh, ""}]` on a repo without a remote → prompter asked twice, no sub ran, stderr empty.
  - "a command that changes the branch shows on the next display": the sub checks out a new branch in the rig's repo → the second recorded `data.Branch` is the new branch.
  - "each turn opens a fresh client": `open` counts its calls → two for one pick then quit.
- [ ] **Step 2: Run** `mise exec -- go test ./cmd/ -run "^TestRunHome" -v` — expect a compile failure.
- [ ] **Step 3: Implement** `cmd/home.go`, then point the root `RunE` at `runHome(cmd, appConfig.ProgName, menuSubs(cmd), …)`. Rewrite the last paragraph of the root `Long`: the home screen, its keys, Esc doing nothing there, `q` quitting, every command returning to it, the help without a terminal or outside a repository. Keep "git zf", never "git-zf", in the prose (`TestGetRootCmd_help` checks it).
- [ ] **Step 4: Update `cmd/root_test.go`**: `TestGetRootCmd_menu` keeps its four cases (the entries are still `rootMenu`); add "every menu entry has a key" (each name of `rootMenu` is in `homeKeys`, and no key is `s`, `r` or `q`) and "keys are unique".
- [ ] **Step 5: Run** `mise exec -- go test ./cmd/...` — expect PASS. Then `make` and, outside a repository with stdin redirected, `./bin/git-zf </dev/null` — expect the help on stdout and nothing on stderr.
- [ ] **Step 6: Update the docs.** In `CLAUDE.md`, rewrite the opening of "Testing the menus": `git zf` runs `runHome` (`cmd/home.go`, data in `cmd/home_data.go`, model `tui.HomeModel`), `git zf review` keeps `RunMenu`, which now loops; list the test commands of Tasks 2, 4, 6 and 7; "to add an entry" now also needs a key in `homeKeys`. Update the website page found by the grep the same way.
- [ ] **Step 7: Whole-tree check.** `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...` — expect PASS. `node .gitnexus/run.cjs detect-changes --scope all --repo .` — report the result; `partial` or `truncated` is not a clean check.

Not covered by tests and left to the user: the look of the screen in a real terminal (colours, spacing, the status view scrolling, the spinner during `r`).
