# Git Status Panel in `git zf commit` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the current working-tree status as a colored, grouped `git status`-style panel to the right of the `git zf commit` TUI form.

**Architecture:** The `git` package runs `git status --porcelain=v2` and parses it into typed `StatusEntry` values. The `tui` package classifies those entries into colored sections (`Changes to be committed:` / `Changes not staged for commit:` / `Untracked files:` / `Unmerged paths:`) and renders a bordered lipgloss panel. `cmd/commit` computes the panel once when the form opens and threads it as an opaque string through `FillOutForm` → `runFormFn` → `tui.RunForm`, where `FormRunner.View()` joins it beside the form.

**Tech Stack:** Go (managed by mise), `go-git` v6, `charmbracelet/huh` v1.0.0, `charmbracelet/lipgloss`, `charmbracelet/bubbletea`.

## Global Constraints

- **Go toolchain runs through mise:** build/test/vet with `mise exec -- go ...`. Never call bare `go`.
- **Never run git yourself.** Per the user's standing workflow, the user performs all `git add` / `git commit` / `git push`. Each task ends by verifying a green build/test state and handing off; a suggested commit message is provided for the user to run. Do **not** execute git commands.
- **Tests use `t.Run` for every distinct assertion or scenario** (subtest isolation, readable output) — matches the repo and the user's global Go rule.
- **golangci-lint is CI-only here** (v2 config vs v1 binary mismatch). Verify locally with `mise exec -- go build ./...`, `mise exec -- go vet ./...`, and `mise exec -- go test ./...`.
- **Status reads must never block a commit.** A failed `git status` degrades to no panel (log a warning, continue).
- Command is exactly `git -C <root> status --porcelain=v2` — **no** `--untracked-files=all` flag (git's default `normal` untracked mode).
- If the GitNexus MCP tools are available, follow the repo's CLAUDE.md workflow (`impact` before editing a symbol, `detect_changes` before handing off). If unavailable, proceed with the go build/vet/test gates above.

---

## File Structure

- **Create** `git/status.go` — `StatusEntry` type, `StatusEntries` method, unexported `parsePorcelainV2` + `normalizeXY`.
- **Create** `git/status_test.go` — parser table tests + `StatusEntries` on-disk integration tests.
- **Create** `tui/status_panel.go` — `statusKind`/`statusLine`/`statusGroup` types, `groupEntries`, `StatusPanel`, and rendering helpers.
- **Create** `tui/status_panel_test.go` — `groupEntries` + `StatusPanel` tests.
- **Modify** `tui/runner.go` — add `panel` field to `FormRunner`, join it in `View()`, add `panel` param to `RunForm`.
- **Modify** `tui/runner_test.go` — `View()` tests.
- **Modify** `commit/form.go` — `runFormFn` and `FillOutForm` gain a `panel string` parameter.
- **Modify** `commit/form_test.go` — update stubs/callers for the new parameter, add a forwarding test.
- **Modify** `cmd/commit/commit.go` — `runE` computes and threads the panel.
- **Modify** `cmd/issue/close_prompter.go` — pass `""` to `FillOutForm`.

Task order: Tasks 1–4 build isolated, independently-testable units. Task 5 plumbs the opaque panel string end-to-end (callers pass `""`, behaviour unchanged). Task 6 populates the panel in `runE`. Splitting the signature cascade (Task 5) from the status computation (Task 6) keeps every commit compilable and independently reviewable.

---

## Task 1: `git` — parse `porcelain=v2` into `StatusEntry`

**Files:**
- Create: `git/status.go`
- Test: `git/status_test.go`

**Interfaces:**
- Consumes: nothing (pure parser + a struct).
- Produces:
  - `type StatusEntry struct { XY string; Path string; OrigPath string }`
  - `func parsePorcelainV2(raw string) []StatusEntry` (unexported)
  - `XY` is the two-character code with porcelain-v2 dots normalised to spaces (`" M"`, `"M "`, `"MM"`, `"A "`); untracked entries use `"??"`; unmerged entries keep their raw two letters (e.g. `"UU"`, `"AA"`).

- [ ] **Step 1: Write the failing test**

Create `git/status_test.go`:

```go
package git

import (
	"testing"
)

func TestParsePorcelainV2(t *testing.T) {
	t.Parallel()

	t.Run("staged modified (ordinary '1' line)", func(t *testing.T) {
		t.Parallel()

		raw := "1 M. N... 100644 100644 100644 aaaa bbbb cmd/commit/commit.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].XY != "M " {
			t.Errorf("XY = %q, want %q", got[0].XY, "M ")
		}
		if got[0].Path != "cmd/commit/commit.go" {
			t.Errorf("Path = %q, want %q", got[0].Path, "cmd/commit/commit.go")
		}
	})

	t.Run("unstaged modified normalises leading dot to space", func(t *testing.T) {
		t.Parallel()

		raw := "1 .M N... 100644 100644 100644 aaaa bbbb commit/form.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != " M" || got[0].Path != "commit/form.go" {
			t.Fatalf("got %+v, want XY=%q Path=%q", got, " M", "commit/form.go")
		}
	})

	t.Run("staged and unstaged (MM)", func(t *testing.T) {
		t.Parallel()

		raw := "1 MM N... 100644 100644 100644 aaaa bbbb commit/form.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "MM" {
			t.Fatalf("got %+v, want XY=%q", got, "MM")
		}
	})

	t.Run("added and deleted words", func(t *testing.T) {
		t.Parallel()

		raw := "1 A. N... 000000 100644 100644 0000 bbbb git/status.go\n" +
			"1 D. N... 100644 000000 000000 aaaa 0000 old/removed.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}
		if got[0].XY != "A " || got[0].Path != "git/status.go" {
			t.Errorf("got[0] = %+v", got[0])
		}
		if got[1].XY != "D " || got[1].Path != "old/removed.go" {
			t.Errorf("got[1] = %+v", got[1])
		}
	})

	t.Run("renamed '2' line populates Path and OrigPath", func(t *testing.T) {
		t.Parallel()

		raw := "2 R. N... 100644 100644 100644 aaaa bbbb R100 new/name.go\told/name.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].XY != "R " {
			t.Errorf("XY = %q, want %q", got[0].XY, "R ")
		}
		if got[0].Path != "new/name.go" || got[0].OrigPath != "old/name.go" {
			t.Errorf("Path=%q OrigPath=%q, want %q / %q", got[0].Path, got[0].OrigPath, "new/name.go", "old/name.go")
		}
	})

	t.Run("untracked '?' line yields ?? and path", func(t *testing.T) {
		t.Parallel()

		raw := "? docs/notes.md\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "??" || got[0].Path != "docs/notes.md" {
			t.Fatalf("got %+v, want XY=?? Path=docs/notes.md", got)
		}
	})

	t.Run("unmerged 'u' line yields XY and path", func(t *testing.T) {
		t.Parallel()

		raw := "u UU N... 100644 100644 100644 100644 aaaa bbbb cccc conflict.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].XY != "UU" || got[0].Path != "conflict.go" {
			t.Fatalf("got %+v, want XY=UU Path=conflict.go", got)
		}
	})

	t.Run("ignored '!', header '#', and blank lines are skipped", func(t *testing.T) {
		t.Parallel()

		raw := "# branch.oid aaaa\n! build/artifact\n\n1 M. N... 100644 100644 100644 aaaa bbbb keep.go\n"
		got := parsePorcelainV2(raw)

		if len(got) != 1 || got[0].Path != "keep.go" {
			t.Fatalf("got %+v, want single entry keep.go", got)
		}
	})

	t.Run("empty input yields no entries", func(t *testing.T) {
		t.Parallel()

		if got := parsePorcelainV2(""); len(got) != 0 {
			t.Errorf("len(got) = %d, want 0", len(got))
		}
	})

	t.Run("malformed short line is skipped", func(t *testing.T) {
		t.Parallel()

		if got := parsePorcelainV2("1 M.\n"); len(got) != 0 {
			t.Errorf("len(got) = %d, want 0 (malformed skipped)", len(got))
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./git/ -run TestParsePorcelainV2 -v`
Expected: FAIL — `undefined: parsePorcelainV2` / `undefined: StatusEntry`.

- [ ] **Step 3: Write minimal implementation**

Create `git/status.go`:

```go
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// StatusEntry is one parsed line of `git status --porcelain=v2`.
type StatusEntry struct {
	// XY is the two-character status code with porcelain-v2 unmodified dots
	// normalised to spaces (e.g. " M", "M ", "MM", "A ", "??"). X is the
	// index/staged side, Y the worktree side. Untracked entries use "??";
	// unmerged entries keep their raw two letters (e.g. "UU").
	XY string
	// Path is the current path of the entry.
	Path string
	// OrigPath is the pre-rename/-copy path for '2' entries, else "".
	OrigPath string
}

// StatusEntries returns the working-tree status parsed from
// `git -C <root> status --porcelain=v2`. It shells out to the system git
// binary (like IsDirty) so semantics match git exactly.
func (c *Client) StatusEntries(ctx context.Context) ([]StatusEntry, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v2")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}

	return parsePorcelainV2(string(out)), nil
}

// parsePorcelainV2 parses porcelain=v2 output into entries. Ignored ('!') and
// header ('#') lines are dropped; unrecognised or malformed lines are skipped
// defensively so a single odd line never fails the whole status.
func parsePorcelainV2(raw string) []StatusEntry {
	var entries []StatusEntry

	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}

		switch line[0] {
		case '1': // ordinary: 1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>
			f := strings.SplitN(line, " ", 9)
			if len(f) < 9 {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: f[8]})
		case '2': // rename/copy: ... <Xscore> <path>\t<origPath>
			f := strings.SplitN(line, " ", 10)
			if len(f) < 10 {
				continue
			}
			path, orig, ok := strings.Cut(f[9], "\t")
			if !ok {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: path, OrigPath: orig})
		case 'u': // unmerged: u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>
			f := strings.SplitN(line, " ", 11)
			if len(f) < 11 {
				continue
			}
			entries = append(entries, StatusEntry{XY: normalizeXY(f[1]), Path: f[10]})
		case '?': // untracked: ? <path>
			entries = append(entries, StatusEntry{XY: "??", Path: strings.TrimPrefix(line, "? ")})
		default:
			// '!' ignored, '#' header, or anything unexpected → skip.
		}
	}

	return entries
}

// normalizeXY converts porcelain-v2 unmodified dots to spaces so the two-char
// code matches short-format conventions (" M" not ".M").
func normalizeXY(xy string) string {
	return strings.ReplaceAll(xy, ".", " ")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./git/ -run TestParsePorcelainV2 -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Verify package builds and hand off**

Run: `mise exec -- go build ./git/ && mise exec -- go vet ./git/`
Expected: no output, exit 0.
Then stop and let the user commit. Suggested message:
`feat(git): parse git status --porcelain=v2 into StatusEntry`

---

## Task 2: `git` — `StatusEntries` integration against a real repo

**Files:**
- Modify: `git/status.go` (already contains `StatusEntries` from Task 1 — no code change; this task adds its integration test)
- Test: `git/status_test.go`

**Interfaces:**
- Consumes: `func (c *Client) StatusEntries(ctx context.Context) ([]StatusEntry, error)` (Task 1), test helpers `newDiskRepo(t) (*Client, string)`, `runGitInDir(t, dir, args...)`, `writeFile(t, dir, name, content)` (existing in `git/merge_test.go` / `git/git_test.go`).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Append to `git/status_test.go` (note the file already declares `package git` — add only this function):

```go
func TestStatusEntries(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	// Staged new file.
	writeFile(t, dir, "added.go", "package main\n")
	runGitInDir(t, dir, "add", "added.go")

	// Tracked file modified in the worktree only (base.go exists from newDiskRepo).
	writeFile(t, dir, "base.go", "package main // changed\n")

	// Untracked file.
	writeFile(t, dir, "untracked.txt", "hello\n")

	entries, err := client.StatusEntries(t.Context())
	if err != nil {
		t.Fatalf("StatusEntries: %v", err)
	}

	byPath := make(map[string]StatusEntry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}

	t.Run("staged new file has index-side code", func(t *testing.T) {
		e, ok := byPath["added.go"]
		if !ok {
			t.Fatalf("added.go missing from %+v", entries)
		}
		if e.XY[0] != 'A' {
			t.Errorf("added.go XY = %q, want index-side 'A'", e.XY)
		}
	})

	t.Run("worktree-modified tracked file has worktree-side code", func(t *testing.T) {
		e, ok := byPath["base.go"]
		if !ok {
			t.Fatalf("base.go missing from %+v", entries)
		}
		if e.XY[1] != 'M' {
			t.Errorf("base.go XY = %q, want worktree-side 'M'", e.XY)
		}
	})

	t.Run("untracked file reported as ??", func(t *testing.T) {
		e, ok := byPath["untracked.txt"]
		if !ok {
			t.Fatalf("untracked.txt missing from %+v", entries)
		}
		if e.XY != "??" {
			t.Errorf("untracked.txt XY = %q, want %q", e.XY, "??")
		}
	})

	t.Run("clean subset: nothing unexpected", func(t *testing.T) {
		if len(entries) < 3 {
			t.Errorf("len(entries) = %d, want >= 3 (added, base, untracked)", len(entries))
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails (or is red for the right reason)**

Run: `mise exec -- go test ./git/ -run TestStatusEntries -v`
Expected: If Task 1 is committed, this compiles and should PASS immediately (the method already exists). That is acceptable — this task's value is the integration coverage. If it FAILS, read the diff between reported and expected `XY` and fix `parsePorcelainV2` field indexing in `git/status.go`.

- [ ] **Step 3: Confirm green**

Run: `mise exec -- go test ./git/ -run "TestStatusEntries|TestParsePorcelainV2" -v`
Expected: PASS.

- [ ] **Step 4: Verify and hand off**

Run: `mise exec -- go vet ./git/`
Expected: no output.
Stop and let the user commit. Suggested message:
`test(git): integration test for StatusEntries on a real repo`

---

## Task 3: `tui` — classify entries into sections (`groupEntries`)

**Files:**
- Create: `tui/status_panel.go`
- Test: `tui/status_panel_test.go`

**Interfaces:**
- Consumes: `git.StatusEntry` (Task 1).
- Produces (all unexported except where noted):
  - `type statusKind int` with `kindStaged, kindUnstaged, kindUntracked, kindUnmerged`
  - `type statusLine struct { Word string; Path string }`
  - `type statusGroup struct { Kind statusKind; Lines []statusLine }`
  - `func groupEntries(entries []git.StatusEntry) []statusGroup` — ordered staged, unstaged, untracked, unmerged; empty sections omitted; an `MM`-style entry yields a line in **both** staged and unstaged.
  - Helpers `wordForCode(c byte) string`, `renamePath(e git.StatusEntry) string`, map `unmergedWords`.

- [ ] **Step 1: Write the failing test**

Create `tui/status_panel_test.go`:

```go
package tui

import (
	"testing"

	"github.com/piprim/git-zf/git"
)

func TestGroupEntries(t *testing.T) {
	t.Parallel()

	t.Run("staged-only entry lands in Changes to be committed", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "M ", Path: "a.go"}})

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "modified" || groups[0].Lines[0].Path != "a.go" {
			t.Errorf("line = %+v, want modified/a.go", groups[0].Lines[0])
		}
	})

	t.Run("unstaged-only entry lands in not-staged section", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: " M", Path: "b.go"}})

		if len(groups) != 1 || groups[0].Kind != kindUnstaged {
			t.Fatalf("groups = %+v, want single unstaged group", groups)
		}
	})

	t.Run("MM entry appears in both staged and unstaged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "MM", Path: "c.go"}})

		if len(groups) != 2 {
			t.Fatalf("len(groups) = %d, want 2", len(groups))
		}
		if groups[0].Kind != kindStaged || groups[1].Kind != kindUnstaged {
			t.Errorf("kinds = %v/%v, want staged/unstaged", groups[0].Kind, groups[1].Kind)
		}
		if groups[0].Lines[0].Path != "c.go" || groups[1].Lines[0].Path != "c.go" {
			t.Errorf("c.go should appear in both sections")
		}
	})

	t.Run("added and deleted words", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "A ", Path: "new.go"},
			{XY: "D ", Path: "gone.go"},
		})

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "new file" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[0].Word, "new file")
		}
		if groups[0].Lines[1].Word != "deleted" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[1].Word, "deleted")
		}
	})

	t.Run("renamed entry shows orig -> path in staged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "R ", Path: "new/name.go", OrigPath: "old/name.go"}})

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		if groups[0].Lines[0].Word != "renamed" || groups[0].Lines[0].Path != "old/name.go -> new/name.go" {
			t.Errorf("line = %+v, want renamed/old->new", groups[0].Lines[0])
		}
	})

	t.Run("untracked entry lands in Untracked with bare path", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "??", Path: "docs/notes.md"}})

		if len(groups) != 1 || groups[0].Kind != kindUntracked {
			t.Fatalf("groups = %+v, want single untracked group", groups)
		}
		if groups[0].Lines[0].Word != "" || groups[0].Lines[0].Path != "docs/notes.md" {
			t.Errorf("line = %+v, want bare path", groups[0].Lines[0])
		}
	})

	t.Run("unmerged entry lands in Unmerged with descriptive word", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "UU", Path: "conflict.go"}})

		if len(groups) != 1 || groups[0].Kind != kindUnmerged {
			t.Fatalf("groups = %+v, want single unmerged group", groups)
		}
		if groups[0].Lines[0].Word != "both modified" {
			t.Errorf("Word = %q, want %q", groups[0].Lines[0].Word, "both modified")
		}
	})

	t.Run("empty input yields no groups", func(t *testing.T) {
		t.Parallel()

		if groups := groupEntries(nil); len(groups) != 0 {
			t.Errorf("len(groups) = %d, want 0", len(groups))
		}
	})

	t.Run("section order is staged, unstaged, untracked, unmerged", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "??", Path: "u.txt"},
			{XY: "UU", Path: "c.go"},
			{XY: " M", Path: "b.go"},
			{XY: "M ", Path: "a.go"},
		})

		want := []statusKind{kindStaged, kindUnstaged, kindUntracked, kindUnmerged}
		if len(groups) != len(want) {
			t.Fatalf("len(groups) = %d, want %d", len(groups), len(want))
		}
		for i := range want {
			if groups[i].Kind != want[i] {
				t.Errorf("groups[%d].Kind = %v, want %v", i, groups[i].Kind, want[i])
			}
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./tui/ -run TestGroupEntries -v`
Expected: FAIL — `undefined: groupEntries` / `undefined: kindStaged` etc.

- [ ] **Step 3: Write minimal implementation**

Create `tui/status_panel.go` (imports only `git`; `strings`/`lipgloss` are added by Task 4 when `StatusPanel` needs them):

```go
package tui

import (
	"github.com/piprim/git-zf/git"
)

type statusKind int

const (
	kindStaged statusKind = iota
	kindUnstaged
	kindUntracked
	kindUnmerged
)

// statusLine is one rendered entry: a git word ("modified", "new file", ...)
// and a path. Word is empty for untracked entries.
type statusLine struct {
	Word string
	Path string
}

// statusGroup is one colored section with its classified lines.
type statusGroup struct {
	Kind  statusKind
	Lines []statusLine
}

// unmergedWords maps porcelain conflict codes to git's long-format phrasing.
var unmergedWords = map[string]string{
	"DD": "both deleted",
	"AU": "added by us",
	"UD": "deleted by them",
	"UA": "added by them",
	"DU": "deleted by us",
	"AA": "both added",
	"UU": "both modified",
}

// wordForCode maps a single porcelain change code to its git long-format word.
func wordForCode(c byte) string {
	switch c {
	case 'A':
		return "new file"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "typechange"
	default: // 'M' and any other single-side change
		return "modified"
	}
}

// renamePath renders "orig -> path" for renames/copies, else the plain path.
func renamePath(e git.StatusEntry) string {
	if e.OrigPath != "" {
		return e.OrigPath + " -> " + e.Path
	}

	return e.Path
}

// groupEntries classifies entries into ordered, non-empty sections. An entry
// with both index and worktree changes (e.g. "MM") contributes to both the
// staged and unstaged sections, matching `git status`.
func groupEntries(entries []git.StatusEntry) []statusGroup {
	var staged, unstaged, untracked, unmerged []statusLine

	for _, e := range entries {
		switch {
		case e.XY == "??":
			untracked = append(untracked, statusLine{Path: e.Path})
		case unmergedWords[e.XY] != "":
			unmerged = append(unmerged, statusLine{Word: unmergedWords[e.XY], Path: e.Path})
		case len(e.XY) == 2:
			if x := e.XY[0]; x != ' ' {
				staged = append(staged, statusLine{Word: wordForCode(x), Path: renamePath(e)})
			}
			if y := e.XY[1]; y != ' ' {
				unstaged = append(unstaged, statusLine{Word: wordForCode(y), Path: e.Path})
			}
		}
	}

	var groups []statusGroup
	if len(staged) > 0 {
		groups = append(groups, statusGroup{Kind: kindStaged, Lines: staged})
	}
	if len(unstaged) > 0 {
		groups = append(groups, statusGroup{Kind: kindUnstaged, Lines: unstaged})
	}
	if len(untracked) > 0 {
		groups = append(groups, statusGroup{Kind: kindUntracked, Lines: untracked})
	}
	if len(unmerged) > 0 {
		groups = append(groups, statusGroup{Kind: kindUnmerged, Lines: unmerged})
	}

	return groups
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./tui/ -run TestGroupEntries -v`
Expected: PASS. The file imports only `git`, so it compiles standalone.

- [ ] **Step 5: Verify and hand off**

Run: `mise exec -- go vet ./tui/`
Stop and let the user commit. Suggested message:
`feat(tui): classify git status entries into colored sections`

---

## Task 4: `tui` — render the colored `StatusPanel`

**Files:**
- Modify: `tui/status_panel.go`
- Test: `tui/status_panel_test.go`

**Interfaces:**
- Consumes: `groupEntries`, `statusKind`, `statusLine`, `statusGroup` (Task 3); `git.StatusEntry` (Task 1); `lipgloss`.
- Produces: `func StatusPanel(entries []git.StatusEntry) string` — bordered panel titled "Current Git Status"; returns `""` for zero entries.

- [ ] **Step 1: Write the failing test**

Append to `tui/status_panel_test.go`:

```go
import "strings" // add to the existing import block if not present

func TestStatusPanel(t *testing.T) {
	t.Parallel()

	t.Run("no entries returns empty string", func(t *testing.T) {
		t.Parallel()

		if got := StatusPanel(nil); got != "" {
			t.Errorf("StatusPanel(nil) = %q, want \"\"", got)
		}
	})

	t.Run("renders section headers and word: path lines", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{
			{XY: "A ", Path: "git/status.go"},
			{XY: " M", Path: "commit/form.go"},
			{XY: "??", Path: "docs/notes.md"},
		})

		for _, want := range []string{
			"Current Git Status",
			"Changes to be committed:",
			"new file: git/status.go",
			"Changes not staged for commit:",
			"modified: commit/form.go",
			"Untracked files:",
			"docs/notes.md",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("panel missing %q\n---\n%s", want, out)
			}
		}
	})

	t.Run("rename entry shows orig -> path", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{{XY: "R ", Path: "new/name.go", OrigPath: "old/name.go"}})

		if !strings.Contains(out, "renamed: old/name.go -> new/name.go") {
			t.Errorf("panel missing rename line\n---\n%s", out)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./tui/ -run TestStatusPanel -v`
Expected: FAIL — `undefined: StatusPanel`.

- [ ] **Step 3: Write minimal implementation**

First, expand the import block of `tui/status_panel.go` to add the two packages `StatusPanel` needs:

```go
import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
)
```

Then append to `tui/status_panel.go`:

```go
// sectionMeta pairs a section's header label with its color.
type sectionMeta struct {
	header string
	color  lipgloss.Color
}

// sectionMetaByKind maps each section to its git long-format header and color:
// staged=yellow, unstaged=green, untracked=turquoise, unmerged=red.
var sectionMetaByKind = map[statusKind]sectionMeta{
	kindStaged:    {"Changes to be committed:", lipgloss.Color("3")},
	kindUnstaged:  {"Changes not staged for commit:", lipgloss.Color("2")},
	kindUntracked: {"Untracked files:", lipgloss.Color("#40E0D0")},
	kindUnmerged:  {"Unmerged paths:", lipgloss.Color("1")},
}

// formatLine renders one entry line: "word: path", or just "path" for untracked.
func formatLine(ln statusLine) string {
	if ln.Word == "" {
		return ln.Path
	}

	return ln.Word + ": " + ln.Path
}

// StatusPanel renders entries as a bordered lipgloss panel titled "Current Git
// Status", grouped into colored sections. Returns "" when there are no entries.
func StatusPanel(entries []git.StatusEntry) string {
	groups := groupEntries(entries)
	if len(groups) == 0 {
		return ""
	}

	var b strings.Builder
	for i, g := range groups {
		meta := sectionMetaByKind[g.Kind]
		style := lipgloss.NewStyle().Foreground(meta.color)

		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(style.Render(meta.header))
		b.WriteString("\n")

		for _, ln := range g.Lines {
			b.WriteString(style.Render("  " + formatLine(ln)))
			b.WriteString("\n")
		}
	}

	title := lipgloss.NewStyle().Bold(true).Render("Current Git Status")
	body := strings.TrimRight(b.String(), "\n")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	return box.Render(title + "\n\n" + body)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./tui/ -run "TestStatusPanel|TestGroupEntries" -v`
Expected: PASS.

- [ ] **Step 5: Verify and hand off**

Run: `mise exec -- go build ./tui/ && mise exec -- go vet ./tui/`
Stop and let the user commit. Suggested message:
`feat(tui): render Current Git Status panel with colored sections`

---

## Task 5: Plumb the panel string through the form (callers pass `""`)

This is the signature cascade. All edits land together so every intermediate state compiles: `tui.RunForm` and `FormRunner` gain the panel; `commit.runFormFn` and `FillOutForm` gain the parameter; both `FillOutForm` callers pass `""` (behaviour unchanged). Task 6 later replaces `runE`'s `""` with the real panel.

**Files:**
- Modify: `tui/runner.go`
- Modify: `tui/runner_test.go`
- Modify: `commit/form.go`
- Modify: `commit/form_test.go`
- Modify: `cmd/commit/commit.go` (only the `FillOutForm` call — pass `""` for now)
- Modify: `cmd/issue/close_prompter.go` (pass `""`)

**Interfaces:**
- Consumes: existing `FormRunner`, `RunForm`, `FillOutForm`, `runFormFn`.
- Produces (new signatures every later task relies on):
  - `func RunForm(form *huh.Form, panel string) (*FormRunner, error)`
  - `FormRunner` gains an unexported `panel string` field; `View()` joins it to the right when non-empty.
  - `runFormFn` var: `func(form *huh.Form, panel string) (formRunner, error)`
  - `func FillOutForm(ctx context.Context, cfg *config.AppConfig, defaults tui.CommitOption, hs historyStore, initialPrefill map[string]any, panel string) ([]byte, tui.CommitOption, error)`

- [ ] **Step 1: Write the failing tests (runner View + panel forwarding)**

In `tui/runner_test.go`, add `"strings"` to the import block and append:

```go
func TestFormRunnerView(t *testing.T) {
	t.Parallel()

	t.Run("View without a panel renders only the form", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner() // panel is the zero value ""
		if r.View() != r.form.View() {
			t.Error("View() should equal form.View() when panel is empty")
		}
	})

	t.Run("View with a panel includes the panel text", func(t *testing.T) {
		t.Parallel()

		r := newTestRunner()
		r.panel = "PANEL-MARKER"
		if !strings.Contains(r.View(), "PANEL-MARKER") {
			t.Errorf("View() should contain the panel text; got:\n%s", r.View())
		}
	})
}
```

In `commit/form_test.go`, inside `TestFillOutForm`, add this subtest (it drives the new `panel` parameter):

```go
	t.Run("forwards the panel string to the form runner", func(t *testing.T) {
		var gotPanel string
		swapRunFormFn(t, func(_ *huh.Form, panel string) (formRunner, error) {
			gotPanel = panel
			return &stubFormRunner{}, nil
		})

		hs := &fakeHistoryStore{}
		_, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "PANEL")
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}
		if gotPanel != "PANEL" {
			t.Errorf("panel forwarded = %q, want %q", gotPanel, "PANEL")
		}
	})
```

- [ ] **Step 2: Run tests to verify they fail (compile errors expected)**

Run: `mise exec -- go test ./tui/ ./commit/ 2>&1 | head -30`
Expected: FAIL — `too many arguments in call to swapRunFormFn`'s closure / `FillOutForm` argument count / `r.panel undefined`. These compile failures confirm the tests target the new API.

- [ ] **Step 3: Update `tui/runner.go`**

Add the `lipgloss` import, the `panel` field, the `View` join, and the `RunForm` parameter:

```go
package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// FormRunner wraps a *huh.Form as a tea.Model to intercept ctrl+r before huh
// sees it, and to render an optional read-only panel to the right of the form.
type FormRunner struct {
	form        *huh.Form
	wantHistory bool
	panel       string
}
```

Replace `View`:

```go
// View implements tea.Model. When a panel is set, it is joined to the right of
// the form; otherwise only the form is rendered.
func (r *FormRunner) View() string {
	if r.panel == "" {
		return r.form.View()
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, r.form.View(), r.panel)
}
```

Replace `RunForm`'s signature and construction:

```go
// RunForm runs form inside a bubbletea program, rendering panel (when non-empty)
// to the right of the form.
// Returns (runner, nil) on normal completion or ctrl+r; call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form, panel string) (*FormRunner, error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	r := &FormRunner{form: form, panel: panel}
	_, err := tea.NewProgram(r, tea.WithOutput(os.Stderr)).Run()

	if errors.Is(err, tea.ErrInterrupted) {
		return r, huh.ErrUserAborted
	}
	if err != nil {
		return r, fmt.Errorf("run form: %w", err)
	}

	if r.form.State == huh.StateAborted {
		return r, huh.ErrUserAborted
	}

	return r, nil
}
```

- [ ] **Step 4: Update `commit/form.go`**

Change the `runFormFn` var (around line 48) to take and forward the panel:

```go
// runFormFn is the form runner used by FillOutForm and runHistoryPicker.
// Tests can replace it with a stub to avoid requiring a real terminal.
var runFormFn = func(form *huh.Form, panel string) (formRunner, error) {
	return tui.RunForm(form, panel)
}
```

Update the two non-main call sites to pass `""` (the picker and the dialog never show a panel):

- In `runHistoryPicker`, change `_, runErr := runFormFn(pickerForm)` to `_, runErr := runFormFn(pickerForm, "")`.
- In `showNoHistoryDialog`, change `if _, err := runFormFn(dialog); ...` to `if _, err := runFormFn(dialog, ""); ...`.

Change `FillOutForm`'s signature to accept the panel and forward it at the main-form call site:

```go
func FillOutForm(
	ctx context.Context,
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hs historyStore,
	initialPrefill map[string]any,
	panel string,
) ([]byte, tui.CommitOption, error) {
	prefill := initialPrefill

	for {
		form, extractMsg, extractOpts := loadForm(cfg, defaults, prefill)

		runner, err := runFormFn(form, panel)
```

(Leave the rest of the loop body unchanged.)

- [ ] **Step 5: Update the existing `commit/form_test.go` stubs and callers**

Every existing `swapRunFormFn(t, func(_ *huh.Form) (formRunner, error) { ... })` closure must take the extra string argument: change each to `func(_ *huh.Form, _ string) (formRunner, error) { ... }`. There are five such closures (the subtests for defaults, abort, no-history re-open, and prefill flows).

Every existing `FillOutForm(...)` call in the test file must pass a trailing panel argument `""` — e.g. `FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, nil, "")`. Update all existing call sites (do not touch the new "forwards the panel" subtest, which passes `"PANEL"`).

Also update the `swapRunFormFn` helper signature itself:

```go
func swapRunFormFn(t *testing.T, fn func(*huh.Form, string) (formRunner, error)) {
	t.Helper()

	orig := runFormFn
	runFormFn = fn
	t.Cleanup(func() { runFormFn = orig })
}
```

- [ ] **Step 6: Update the two `FillOutForm` production callers to pass `""`**

In `cmd/commit/commit.go` (around line 123):

```go
	msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, s, prefill, "")
```

In `cmd/issue/close_prompter.go` (around line 130):

```go
	msg, opts, err := commitpkg.FillOutForm(ctx, p.cfg, defaults, p.store, prefill, "")
```

- [ ] **Step 7: Run the full suite to verify green**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: PASS across `tui`, `commit`, `cmd/commit`, `cmd/issue`, and the rest. In particular `TestFormRunnerView` and the "forwards the panel string" subtest pass.

- [ ] **Step 8: Verify and hand off**

Stop and let the user commit. Suggested message:
`feat(commit): plumb an optional status panel through the commit form`

---

## Task 6: Populate the panel in `runE`

**Files:**
- Modify: `cmd/commit/commit.go`

**Interfaces:**
- Consumes: `client.StatusEntries(ctx)` (Task 2), `tui.StatusPanel(entries)` (Task 4), `FillOutForm(..., panel)` (Task 5).
- Produces: `git zf commit` shows the panel when the working tree has changes.

- [ ] **Step 1: Replace the empty panel in `runE` with the computed one**

In `cmd/commit/commit.go`, inside `runE`, just before the `FillOutForm` call (the block currently reading `prefill := hint.Prefill(...)` then `FillOutForm(..., prefill, "")`), insert the status computation and pass the panel:

```go
	prefill := hint.Prefill(c.appConfig.CommitMessage)

	entries, err := client.StatusEntries(cmd.Context())
	if err != nil {
		slog.Warn("could not load git status", "error", err)

		entries = nil
	}
	panel := tui.StatusPanel(entries)

	msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, s, prefill, panel)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}
```

(`slog` and `tui` are already imported in this file; `client` is the `*git.Client` from `cmdutil.NewClientForCmd`.)

- [ ] **Step 2: Verify the package builds and the suite is green**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: PASS. No test asserts on `runE`'s panel directly (it needs a real terminal); the guarantee is that the wiring compiles and all unit tests for the pieces pass.

- [ ] **Step 3: Manual smoke check (optional but recommended)**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
```
Then, in a repo with staged, modified, and untracked files, run `./bin/git-zf commit` and confirm the "Current Git Status" panel appears to the right of the form with yellow / green / turquoise sections, and that a clean tree shows no panel.

- [ ] **Step 4: Verify and hand off**

Stop and let the user commit. Suggested message:
`feat(commit): show Current Git Status panel beside the commit form`

---

## Self-Review

**Spec coverage:**

- `git status --porcelain=v2`, no `--untracked-files=all` → Task 1 (`StatusEntries` command), Global Constraints.
- `StatusEntry` / `parsePorcelainV2` handling of `1`/`2`/`?`/`u`/`!`/`#` lines → Task 1 tests + impl.
- `StatusEntries` real-repo behaviour → Task 2.
- `groupEntries` classification incl. `MM` → both sections, section order, unmerged/untracked → Task 3.
- Colored, bordered, titled panel with the four headers and yellow/green/turquoise/red colors; `""` on empty → Task 4.
- `FormRunner.panel` + `View` join; `RunForm`/`runFormFn`/`FillOutForm` signatures; picker/dialog/close-flow pass `""` → Task 5.
- `runE` computes panel, degrades to no-panel on error → Task 6 + Global Constraints (never block a commit).
- `t.Run` per scenario → every test in the plan.

**Placeholder scan:** No `TBD`/`TODO`/"add error handling"/"similar to Task N" — every code step contains full code. ✔

**Type consistency:** `StatusEntry{XY, Path, OrigPath}` is produced in Task 1 and consumed unchanged in Tasks 2–4. `statusKind`/`statusLine`/`statusGroup`/`groupEntries` defined in Task 3, consumed in Task 4. `RunForm(form, panel)`, `runFormFn(form, panel)`, `FillOutForm(..., panel)` defined in Task 5 and used by Task 6. Names match across tasks. ✔

**Compilability of each task:** Task 3's `tui/status_panel.go` imports only `git`, so it builds standalone; Task 4 expands the import block to add `strings`/`lipgloss` alongside `StatusPanel`. Every other task also leaves `mise exec -- go build ./...` green — the Task 5 signature cascade lands in a single commit precisely so no intermediate state breaks.
