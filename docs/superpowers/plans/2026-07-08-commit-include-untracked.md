# `--include-untracked` Option for `git zf commit` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `--include-untracked` / `-u` option to `git zf commit` that stages untracked files (respecting `.gitignore`) before committing, exposed both as a CLI flag and a form toggle, and reflected live in the "Current Git Status" panel.

**Architecture:** Thread one boolean (`IncludeUntracked`) down the existing option chain (`cmd` → `tui.CommitOption` → `internal/convert` → `git.CommitOptions`), exactly mirroring the `--all` plumbing. Add a self-contained staging step in `git.Client.Commit` that runs `git add` on untracked files before the commit. Extend the panel classifier to fold untracked files into "Changes to be committed" when the toggle is on, driven by a live two-value reader on `FormRunner`.

**Tech Stack:** Go, cobra (CLI), charmbracelet/huh (TUI forms), charmbracelet/lipgloss (panel rendering), system `git` binary (staging + commit), go-git.

## Global Constraints

- **Go via mise:** run every Go command as `mise exec -- go …`. Never call bare `go`.
- **No git commits by the agent:** the user handles all `git add`/`git commit`/`git mv`. Each task ends with a **green-gate checkpoint** (tests + build + vet), NOT a `git commit`. Do not run `git add` or `git commit`.
- **Tests use `t.Run` per scenario:** every distinct assertion/scenario is a named `t.Run("descriptive label", …)` subtest.
- **golangci-lint is CI-only** here (v2 config vs v1 binary). Verify locally with `mise exec -- go vet ./...`, `mise exec -- go build`, and `mise exec -- go test ./...`.
- **Orthogonal semantics:** `--include-untracked` stages *only* untracked files; it does not change what `--all` does. The two compose. `.gitignore`/`.git/info/exclude` are always respected — ignored files are never staged.
- **Module path:** `github.com/piprim/git-zf`.

---

## File Structure

Files created or modified, by responsibility:

- `git/git.go` — `CommitOptions.IncludeUntracked` field; `stageUntracked` helper; staging call in `Commit`. (git wrapper layer)
- `git/git_test.go` — `TestCommitIncludeUntracked` with the four commit scenarios.
- `tui/commit.go` — `CommitOption.IncludeUntracked` field; `--include-untracked` confirm in `CommitOptionsGroup`. (form definition)
- `internal/convert/convert.go` — map `IncludeUntracked`. (option adapter)
- `internal/convert/convert_test.go` — extend `TestCommitOptionsFromTUI`.
- `tui/status_panel.go` — `includeUntracked` param on `groupEntries`/`StatusPanel`; 4-combo `StatusPanelReserveWidth`. (panel classifier/renderer)
- `tui/status_panel_test.go` — update existing calls; new fold/compose/render/reserve tests.
- `tui/runner.go` — `FormRunner` live reader `classifyFn func() (bool, bool)`; `RunForm` signature. (form runner)
- `tui/runner_test.go` — reader rename + live `--include-untracked` view test.
- `commit/form.go` — `runFormFn` type + `FillOutForm` closure carry `func() (bool, bool)`. (form orchestration)
- `commit/form_test.go` — `swapRunFormFn` signature + callers; reader test extended.
- `cmd/commit/commit.go` — `-u` flag, `Skip` predicate, seed the option. (cobra command)

Task order (each leaves the tree compiling and green): **1 → 2 → 3 → 4 → 5**. Task 2 must precede Task 4 (the live reader closure reads `tui.CommitOption.IncludeUntracked`). Task 4 depends on Task 3 (the 3-arg `StatusPanel`).

---

## Task 1: git-layer staging (`CommitOptions.IncludeUntracked` + `stageUntracked`)

**Files:**
- Modify: `git/git.go` (`CommitOptions` struct at `git/git.go:18-26`; `Commit` at `git/git.go:323-369`; add `stageUntracked` method)
- Test: `git/git_test.go` (new `TestCommitIncludeUntracked`)

**Interfaces:**
- Consumes: existing `git/status.go` `exec.CommandContext(ctx, "git", "-C", root, …)` idiom; `newDiskRepo`, `writeFile`, `runGitInDir` test helpers.
- Produces: `git.CommitOptions{… IncludeUntracked bool}`; `Commit` stages untracked files when `IncludeUntracked` is true, before running `git commit`.

- [ ] **Step 1: Write the failing tests**

Append this new test function to `git/git_test.go` (it mirrors the existing `TestCommit` subtests: discard IO, real on-disk repo, inspect the result via `git show --stat HEAD`):

```go
func TestCommitIncludeUntracked(t *testing.T) {
	t.Parallel()

	// showStat returns `git show --stat HEAD` output for dir.
	showStat := func(t *testing.T, dir string) string {
		t.Helper()

		var buf bytes.Buffer
		cmd := exec.Command("git", "show", "--stat", "HEAD")
		cmd.Dir = dir
		cmd.Stdout = &buf
		if err := cmd.Run(); err != nil {
			t.Fatalf("git show: %v", err)
		}

		return buf.String()
	}

	t.Run("stages and commits an untracked file", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		writeFile(t, dir, "fresh.go", "package main\n")

		if err := client.Commit(t.Context(), []byte("feat: add fresh"), CommitOptions{IncludeUntracked: true}); err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		if out := showStat(t, dir); !strings.Contains(out, "fresh.go") {
			t.Errorf("fresh.go must be in the commit; got:\n%s", out)
		}
	})

	t.Run("no untracked files is a no-op and the commit still succeeds", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		// Stage a tracked change so the commit has content; no untracked files exist.
		writeFile(t, dir, "base.go", "package changed\n")
		runGitInDir(t, dir, "add", "base.go")

		if err := client.Commit(t.Context(), []byte("chore: no untracked"), CommitOptions{IncludeUntracked: true}); err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		if out := showStat(t, dir); !strings.Contains(out, "base.go") {
			t.Errorf("base.go must be in the commit; got:\n%s", out)
		}
	})

	t.Run("composes with All: tracked edit and untracked file both land in the commit", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		writeFile(t, dir, "base.go", "package modified\n") // tracked, worktree-modified (unstaged)
		writeFile(t, dir, "fresh.go", "package main\n")    // untracked

		err := client.Commit(t.Context(), []byte("feat: all + untracked"), CommitOptions{All: true, IncludeUntracked: true})
		if err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		out := showStat(t, dir)
		if !strings.Contains(out, "base.go") {
			t.Errorf("base.go (tracked edit via --all) must be in the commit; got:\n%s", out)
		}
		if !strings.Contains(out, "fresh.go") {
			t.Errorf("fresh.go (untracked) must be in the commit; got:\n%s", out)
		}
	})

	t.Run("respects .gitignore: an ignored untracked file is not staged", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		writeFile(t, dir, ".gitignore", "ignored.txt\n")
		writeFile(t, dir, "ignored.txt", "secret\n")
		writeFile(t, dir, "fresh.go", "package main\n")

		if err := client.Commit(t.Context(), []byte("feat: respect gitignore"), CommitOptions{IncludeUntracked: true}); err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		out := showStat(t, dir)
		if !strings.Contains(out, "fresh.go") {
			t.Errorf("fresh.go must be in the commit; got:\n%s", out)
		}
		if strings.Contains(out, "ignored.txt") {
			t.Errorf("ignored.txt must NOT be in the commit; got:\n%s", out)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./git/... -run TestCommitIncludeUntracked -v`
Expected: FAIL — `CommitOptions` has no field `IncludeUntracked` (compile error), so the whole `git` test package fails to build.

- [ ] **Step 3: Add the `IncludeUntracked` field**

In `git/git.go`, extend the struct (currently `git/git.go:18-26`):

```go
// CommitOptions configures Client.Commit.
type CommitOptions struct {
	All              bool
	Amend            bool
	NoVerify         bool
	Signoff          bool
	AllowEmpty       bool
	IncludeUntracked bool   // stage untracked (non-ignored) files before committing
	Author           string // "Name <email>"; empty = git config identity
}
```

- [ ] **Step 4: Add the `stageUntracked` helper**

In `git/git.go`, add this method (place it immediately before `Commit`, i.e. before `git/git.go:321`). `strings` and `os/exec` are already imported.

```go
// stageUntracked runs `git add` on every untracked, non-ignored file so an
// --include-untracked commit picks them up. It lists paths with
// `ls-files --others --exclude-standard -z` (NUL-separated, .gitignore and
// .git/info/exclude respected) and passes each as its own argv element to
// `git add --`, so paths containing spaces are safe. An empty list is a no-op.
func (c *Client) stageUntracked(ctx context.Context, root string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", root,
		"ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return fmt.Errorf("list untracked files: %w", err)
	}

	var untracked []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" { // trailing element after the final NUL is empty
			untracked = append(untracked, p)
		}
	}
	if len(untracked) == 0 {
		return nil
	}

	args := append([]string{"-C", root, "add", "--"}, untracked...)
	if err := exec.CommandContext(ctx, "git", args...).Run(); err != nil {
		return fmt.Errorf("stage untracked files: %w", err)
	}

	return nil
}
```

- [ ] **Step 5: Call `stageUntracked` from `Commit` before committing**

In `git/git.go`, inside `Commit`, right after the working-tree-root block (currently `git/git.go:324-327`) and before the temp-file creation, insert the staging step. The result:

```go
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if opts.IncludeUntracked {
		if err := c.stageUntracked(ctx, root); err != nil {
			return err // wrapped by stageUntracked; commit does not proceed
		}
	}

	f, err := os.CreateTemp("", "git-zf-msg-*")
```

Leave the rest of `Commit` unchanged (the untracked add happens first, so `--all` + `--include-untracked` together include everything).

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `mise exec -- go test ./git/... -run TestCommitIncludeUntracked -v`
Expected: PASS — all four subtests green.

- [ ] **Step 7: Green-gate checkpoint (no commit)**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
mise exec -- go vet ./...
mise exec -- go test ./...
```
Expected: build succeeds, vet clean, all packages `ok`. Do NOT run `git add`/`git commit`.

---

## Task 2: option plumbing (`tui.CommitOption` field + form toggle + convert mapping)

**Files:**
- Modify: `tui/commit.go` (`CommitOption` struct at `tui/commit.go:18-27`; `CommitOptionsGroup` at `tui/commit.go:116-140`)
- Modify: `internal/convert/convert.go` (`CommitOptionsFromTUI`)
- Test: `internal/convert/convert_test.go` (extend `TestCommitOptionsFromTUI`)

**Interfaces:**
- Consumes: `git.CommitOptions.IncludeUntracked` (Task 1).
- Produces: `tui.CommitOption{… IncludeUntracked bool}`; `CommitOptionsFromTUI` maps `IncludeUntracked → git.CommitOptions.IncludeUntracked`; the options form shows a "Stage untracked files? (--include-untracked)" confirm writing into `opt.IncludeUntracked`.

- [ ] **Step 1: Write the failing convert test**

In `internal/convert/convert_test.go`, add `IncludeUntracked: true` to the input struct in `TestCommitOptionsFromTUI` (the mixed-boolean input near the top of the test) and add a new subtest. The input becomes:

```go
	in := tui.CommitOption{
		// TUI-only fields that must not influence the result.
		Skip:    true,
		Authors: []string{"Ada <ada@example.com>"},
		// Mapped fields, mixed so a field swap is observable.
		All:              true,
		Amend:            false,
		NoVerify:         true,
		Signoff:          false,
		AllowEmpty:       true,
		IncludeUntracked: true,
		Author:           "Jane Doe <jane@example.com>",
	}
```

And add this subtest alongside the others:

```go
	t.Run("maps IncludeUntracked", func(t *testing.T) {
		if got.IncludeUntracked != in.IncludeUntracked {
			t.Errorf("IncludeUntracked = %v, want %v", got.IncludeUntracked, in.IncludeUntracked)
		}
	})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/convert/... -run TestCommitOptionsFromTUI -v`
Expected: FAIL — `tui.CommitOption` has no field `IncludeUntracked` (compile error) and `git.CommitOptions.IncludeUntracked` is not yet set by the mapper.

- [ ] **Step 3: Add the `IncludeUntracked` field to `tui.CommitOption`**

In `tui/commit.go`, extend the struct (currently `tui/commit.go:18-27`):

```go
type CommitOption struct {
	Skip             bool
	Authors          []string
	Author           string
	All              bool
	Amend            bool
	NoVerify         bool
	Signoff          bool
	AllowEmpty       bool
	IncludeUntracked bool
}
```

- [ ] **Step 4: Add the form toggle**

In `tui/commit.go`, in `CommitOptionsGroup`, add a confirm immediately after the `--all` one (currently `tui/commit.go:134`). The group becomes:

```go
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Author:").
			Options(authorOpts...).
			Value(&opt.Author),
		huh.NewConfirm().Title("Stage all tracked modified/deleted files? (--all)").Value(&opt.All),
		huh.NewConfirm().Title("Stage untracked files? (--include-untracked)").Value(&opt.IncludeUntracked),
		huh.NewConfirm().Title("Amend last commit? (--amend)").Value(&opt.Amend),
		huh.NewConfirm().Title("Skip hooks? (--no-verify)").Value(&opt.NoVerify),
		huh.NewConfirm().Title("Add Signed-off-by trailer? (--signoff)").Value(&opt.Signoff),
		huh.NewConfirm().Title("Allow empty commit? (--allow-empty)").Value(&opt.AllowEmpty),
	)
```

- [ ] **Step 5: Map the field in `CommitOptionsFromTUI`**

In `internal/convert/convert.go`, add `IncludeUntracked` to the returned struct:

```go
func CommitOptionsFromTUI(opts tui.CommitOption) git.CommitOptions {
	return git.CommitOptions{
		All: opts.All, Amend: opts.Amend, NoVerify: opts.NoVerify,
		Signoff: opts.Signoff, AllowEmpty: opts.AllowEmpty,
		IncludeUntracked: opts.IncludeUntracked,
		Author:           opts.Author,
	}
}
```

- [ ] **Step 6: Run the convert test to verify it passes**

Run: `mise exec -- go test ./internal/convert/... -run TestCommitOptionsFromTUI -v`
Expected: PASS — including the new `maps IncludeUntracked` subtest.

- [ ] **Step 7: Green-gate checkpoint (no commit)**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
mise exec -- go vet ./...
mise exec -- go test ./...
```
Expected: build succeeds, vet clean, all packages `ok`.

---

## Task 3: panel classification (`includeUntracked` param + fold logic)

**Files:**
- Modify: `tui/status_panel.go` (`groupEntries` at `tui/status_panel.go:102`; `StatusPanel` at `tui/status_panel.go:173`; `StatusPanelReserveWidth` at `tui/status_panel.go:210`)
- Modify: `tui/runner.go` (`View` at `tui/runner.go:93-104` — update the `StatusPanel` call only)
- Test: `tui/status_panel_test.go` (update existing calls; new tests)

**Interfaces:**
- Consumes: `git.StatusEntry`; existing `mergeUnstagedIntoStaged`, `wordForCode`, `sectionMetaByKind`.
- Produces:
  - `groupEntries(entries []git.StatusEntry, all, includeUntracked bool) []statusGroup`
  - `StatusPanel(entries []git.StatusEntry, all, includeUntracked bool) string`
  - `StatusPanelReserveWidth(entries []git.StatusEntry) int` (unchanged signature; now maxes over all four `(all, includeUntracked)` combos).

  This task keeps `FormRunner.allFn`/`all()` as-is and passes `false` for `includeUntracked` from `View`; the live reader is wired in Task 4.

- [ ] **Step 1: Write the failing panel tests**

In `tui/status_panel_test.go`, add these subtests inside `TestGroupEntries` (after the existing `all=true …` subtests):

```go
	t.Run("includeUntracked folds untracked into to-be-committed as new file", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: "M ", Path: "a.go"},
			{XY: "??", Path: "notes.md"},
		}, false, true)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group (no untracked section)", groups)
		}
		var found bool
		for _, ln := range groups[0].Lines {
			if ln.Path == "notes.md" {
				found = true
				if ln.Word != "new file" {
					t.Errorf("notes.md Word = %q, want %q", ln.Word, "new file")
				}
			}
		}
		if !found {
			t.Errorf("notes.md not folded into staged section: %+v", groups[0].Lines)
		}
	})

	t.Run("includeUntracked composes with all=true", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{
			{XY: " M", Path: "plop"},     // tracked, worktree-modified only
			{XY: "??", Path: "notes.md"}, // untracked
		}, true, true)

		if len(groups) != 1 || groups[0].Kind != kindStaged {
			t.Fatalf("groups = %+v, want single staged group", groups)
		}
		for _, g := range groups {
			if g.Kind == kindUnstaged || g.Kind == kindUntracked {
				t.Errorf("unexpected %v section under all=true, includeUntracked=true: %+v", g.Kind, groups)
			}
		}
	})

	t.Run("includeUntracked=false leaves untracked in its own section", func(t *testing.T) {
		t.Parallel()

		groups := groupEntries([]git.StatusEntry{{XY: "??", Path: "notes.md"}}, false, false)

		if len(groups) != 1 || groups[0].Kind != kindUntracked {
			t.Fatalf("groups = %+v, want single untracked group (unchanged default behavior)", groups)
		}
	})
```

Add these subtests inside `TestStatusPanel` (after the existing `all=true …` subtest):

```go
	t.Run("includeUntracked shows untracked under to-be-committed and no Untracked section", func(t *testing.T) {
		t.Parallel()

		out := StatusPanel([]git.StatusEntry{
			{XY: "A ", Path: "git/status.go"},
			{XY: "??", Path: "docs/notes.md"},
		}, false, true)

		if !strings.Contains(out, "Changes to be committed:") || !strings.Contains(out, "new file: docs/notes.md") {
			t.Errorf("panel should show 'new file: docs/notes.md' under 'Changes to be committed:'\n---\n%s", out)
		}
		if strings.Contains(out, "Untracked files:") {
			t.Errorf("panel must NOT contain an 'Untracked files:' section under --include-untracked\n---\n%s", out)
		}
	})

	t.Run("StatusPanelReserveWidth covers all four all/includeUntracked combos", func(t *testing.T) {
		t.Parallel()

		entries := []git.StatusEntry{
			{XY: " M", Path: "some/longish/path/to/plop.go"},
			{XY: "??", Path: "another/untracked/file/notes.md"},
		}
		w := StatusPanelReserveWidth(entries)

		for _, all := range []bool{false, true} {
			for _, iu := range []bool{false, true} {
				if pw := lipgloss.Width(StatusPanel(entries, all, iu)); pw > w {
					t.Errorf("ReserveWidth = %d, but StatusPanel(all=%v, iu=%v) width = %d (must be >= all combos)", w, all, iu, pw)
				}
			}
		}
	})
```

Add `lipgloss` to the test imports (currently `strings`, `testing`, `git`):

```go
import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/piprim/git-zf/git"
)
```

- [ ] **Step 2: Update the existing panel test call sites to the new 3-arg signature**

Still in `tui/status_panel_test.go`, add a third `false` argument to every existing `groupEntries(…)` and `StatusPanel(…)` call. The existing calls to update (do not touch the new subtests from Step 1, which already use three args):

- `TestGroupEntries`: `groupEntries(…, false)` → `groupEntries(…, false, false)` and `groupEntries(…, true)` → `groupEntries(…, true, false)` in every subtest (staged-only, unstaged-only, MM, added/deleted, renamed, untracked, unmerged, double-letter conflict, empty `groupEntries(nil, false)` → `groupEntries(nil, false, false)`, section-order, `all=true` folds, `all=true` never emits unstaged, `all=true` dedups MM).
- `TestStatusPanel`: `StatusPanel(nil, false)` → `StatusPanel(nil, false, false)`; the "renders section headers…" `StatusPanel(…, false)` → `StatusPanel(…, false, false)`; the "rename entry…" `StatusPanel(…, false)` → `StatusPanel(…, false, false)`; the "all=true shows tracked changes…" `StatusPanel(…, true)` → `StatusPanel(…, true, false)`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./tui/... -run 'TestGroupEntries|TestStatusPanel' -v`
Expected: FAIL — `groupEntries`/`StatusPanel` still take two args (compile error: too many arguments).

- [ ] **Step 4: Add the `includeUntracked` param + fold to `groupEntries`**

In `tui/status_panel.go`, change the signature and add the untracked fold after the existing `all` fold (currently `tui/status_panel.go:102-124`):

```go
func groupEntries(entries []git.StatusEntry, all, includeUntracked bool) []statusGroup {
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

	if all {
		staged = mergeUnstagedIntoStaged(staged, unstaged)
		unstaged = nil
	}

	// --include-untracked stages untracked files at commit time, so they show up
	// under "Changes to be committed" as `new file: <path>` (what git status
	// prints once an untracked file is staged), and no separate "Untracked
	// files:" section is emitted. Untracked paths never collide with staged
	// paths, so a plain append is correct. Independent of the `all` fold.
	if includeUntracked {
		for _, ln := range untracked {
			staged = append(staged, statusLine{Word: "new file", Path: ln.Path})
		}
		untracked = nil
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

Also update the doc comment above `groupEntries` (currently `tui/status_panel.go:93-101`) to mention the new fold — append this sentence to it:

```go
// When includeUntracked is true (the commit ran with -u/--include-untracked),
// untracked files are folded into "Changes to be committed" as "new file:"
// lines and the untracked section is omitted.
```

- [ ] **Step 5: Add the `includeUntracked` param to `StatusPanel`**

In `tui/status_panel.go`, change `StatusPanel`'s signature and forward the flag (currently `tui/status_panel.go:173-174`):

```go
func StatusPanel(entries []git.StatusEntry, all, includeUntracked bool) string {
	groups := groupEntries(entries, all, includeUntracked)
	if len(groups) == 0 {
		return ""
	}
```

Leave the rest of `StatusPanel`'s body unchanged.

- [ ] **Step 6: Make `StatusPanelReserveWidth` max over all four combos**

In `tui/status_panel.go`, replace the body of `StatusPanelReserveWidth` (currently `tui/status_panel.go:210-217`):

```go
func StatusPanelReserveWidth(entries []git.StatusEntry) int {
	w := 0
	for _, all := range []bool{false, true} {
		for _, includeUntracked := range []bool{false, true} {
			if pw := lipgloss.Width(StatusPanel(entries, all, includeUntracked)); pw > w {
				w = pw
			}
		}
	}

	return w
}
```

Update its doc comment (currently `tui/status_panel.go:206-209`) to say it maxes over both the `--all` and `--include-untracked` layouts:

```go
// StatusPanelReserveWidth returns the widest the panel can render for these
// entries across every combination of the --all and --include-untracked
// re-classifications. The form reserves this so its width stays stable when
// either toggle flips the panel between layouts. Returns 0 when nothing shows.
```

- [ ] **Step 7: Update `FormRunner.View` to pass `includeUntracked` (false for now)**

In `tui/runner.go`, `View` currently calls `StatusPanel(r.entries, r.all())` (`tui/runner.go:99-101`). Add the third argument, hard-coded `false` (the live reader arrives in Task 4):

```go
	panel := lipgloss.NewStyle().
		MarginLeft(panelGap).
		Render(StatusPanel(r.entries, r.all(), false))
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `mise exec -- go test ./tui/... -run 'TestGroupEntries|TestStatusPanel' -v`
Expected: PASS — existing and new subtests green.

- [ ] **Step 9: Green-gate checkpoint (no commit)**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
mise exec -- go vet ./...
mise exec -- go test ./...
```
Expected: build succeeds, vet clean, all packages `ok`.

---

## Task 4: live wiring (`FormRunner` reader `func() (bool, bool)`)

**Files:**
- Modify: `tui/runner.go` (`FormRunner` struct at `tui/runner.go:34-40`; `all()` at `tui/runner.go:48-49`; `View` at `tui/runner.go:93-104`; `RunForm` at `tui/runner.go:111-135`)
- Modify: `tui/runner_test.go` (reader field usages; new live test)
- Modify: `commit/form.go` (`runFormFn` at `commit/form.go:53-55`; `FillOutForm` closure at `commit/form.go:188`)
- Test: `commit/form_test.go` (`swapRunFormFn` at `commit/form_test.go:326`; its callers; the reader test at `commit/form_test.go:592`)

**Interfaces:**
- Consumes: `StatusPanel(entries, all, includeUntracked bool)` (Task 3); `tui.CommitOption.IncludeUntracked` (Task 2).
- Produces:
  - `FormRunner.classifyFn func() (bool, bool)` replacing `allFn func() bool`.
  - `func (r *FormRunner) classify() (all, includeUntracked bool)` replacing `all()`.
  - `RunForm(form *huh.Form, entries []git.StatusEntry, classifyFn func() (bool, bool)) (*FormRunner, error)`.
  - `runFormFn` var of type `func(*huh.Form, []git.StatusEntry, func() (bool, bool)) (formRunner, error)`.
  - `FillOutForm` passes `func() (bool, bool) { o := extractOpts(); return o.All, o.IncludeUntracked }`.

- [ ] **Step 1: Update the runner tests to the new reader (failing)**

In `tui/runner_test.go`:

Replace the three existing `r.allFn = func() bool { … }` assignments:
- In "View with entries includes the panel content" (`tui/runner_test.go:89`): `r.allFn = func() bool { return false }` → `r.classifyFn = func() (bool, bool) { return false, false }`.
- In "View reflects the live --all value" (`tui/runner_test.go:101`): `r.allFn = func() bool { return all }` → `r.classifyFn = func() (bool, bool) { return all, false }`.
- In "no panel once the form is done (quitting/aborted)" (`tui/runner_test.go:121`): `r.allFn = func() bool { return false }` → `r.classifyFn = func() (bool, bool) { return false, false }`.
- In "WindowSizeMsg reserves room…" (`tui/runner_test.go:144`): `r.allFn = func() bool { return false }` → `r.classifyFn = func() (bool, bool) { return false, false }`.

Add a new subtest inside `TestFormRunnerView` (after "View reflects the live --all value"):

```go
	t.Run("View reflects the live --include-untracked value", func(t *testing.T) {
		t.Parallel()

		iu := false
		r := newTestRunner()
		r.entries = []git.StatusEntry{{XY: "??", Path: "notes.md"}}
		r.classifyFn = func() (bool, bool) { return false, iu }

		if !strings.Contains(r.View(), "Untracked files:") {
			t.Errorf("includeUntracked=false: expected an 'Untracked files:' section; got:\n%s", r.View())
		}

		iu = true
		if strings.Contains(r.View(), "Untracked files:") {
			t.Errorf("includeUntracked=true: 'Untracked files:' section should be gone; got:\n%s", r.View())
		}
		if !strings.Contains(r.View(), "new file: notes.md") {
			t.Errorf("includeUntracked=true: untracked file should move under 'to be committed'; got:\n%s", r.View())
		}
	})
```

- [ ] **Step 2: Run the runner tests to verify they fail**

Run: `mise exec -- go test ./tui/... -run TestFormRunner -v`
Expected: FAIL — `FormRunner` has no field `classifyFn` (compile error).

- [ ] **Step 3: Swap the `FormRunner` field and reader**

In `tui/runner.go`:

Change the struct field (currently `tui/runner.go:34-40`):

```go
type FormRunner struct {
	form         *huh.Form
	wantHistory  bool
	entries      []git.StatusEntry
	classifyFn   func() (bool, bool)
	reserveWidth int
}
```

Replace the `all()` method (currently `tui/runner.go:48-49`) with `classify()`:

```go
// classify reads the live (--all, --include-untracked) values, defaulting to
// (false, false) when no reader is set.
func (r *FormRunner) classify() (all, includeUntracked bool) {
	if r.classifyFn == nil {
		return false, false
	}

	return r.classifyFn()
}
```

Update `View` (currently `tui/runner.go:93-104`) to use the reader:

```go
func (r *FormRunner) View() string {
	formView := r.form.View()
	if !r.hasPanel() || formView == "" {
		return formView
	}

	all, includeUntracked := r.classify()
	panel := lipgloss.NewStyle().
		MarginLeft(panelGap).
		Render(StatusPanel(r.entries, all, includeUntracked))

	return lipgloss.JoinHorizontal(lipgloss.Top, formView, panel)
}
```

Update `RunForm` (currently `tui/runner.go:111-120`) — signature and field assignment:

```go
func RunForm(form *huh.Form, entries []git.StatusEntry, classifyFn func() (bool, bool)) (*FormRunner, error) {
	form.SubmitCmd = tea.Quit
	form.CancelCmd = tea.Quit

	r := &FormRunner{
		form:         form,
		entries:      entries,
		classifyFn:   classifyFn,
		reserveWidth: StatusPanelReserveWidth(entries),
	}
```

Also refresh the doc comments on the `FormRunner` type (`tui/runner.go:25-33`) and `RunForm` (`tui/runner.go:106-110`) that mention "live --all value (allFn)": reword to "live (--all, --include-untracked) values (classifyFn)". Example for the type comment:

```go
// The panel is recomputed on every render from a fixed working-tree snapshot
// (entries) and the form's live (--all, --include-untracked) values
// (classifyFn), so toggling either option re-classifies the panel in real
// time. reserveWidth is the widest the panel can be across all layouts, so the
// form width stays stable as the toggles flip. When entries is empty there is
// no panel.
```

- [ ] **Step 4: Update `commit/form.go` (runner var + closure)**

In `commit/form.go`:

Change the `runFormFn` var type and body (currently `commit/form.go:53-55`):

```go
var runFormFn = func(form *huh.Form, entries []git.StatusEntry, classifyFn func() (bool, bool)) (formRunner, error) {
	return tui.RunForm(form, entries, classifyFn)
}
```

Update its doc comment (currently `commit/form.go:47-52`), replacing the `allFn` sentence:

```go
// entries is the working-tree snapshot for the status panel (nil = no panel);
// classifyFn reads the form's live (--all, --include-untracked) values so the
// panel re-classifies as the user toggles either one.
```

Change the `FillOutForm` call (currently `commit/form.go:184-188`) to pass both values:

```go
		// classifyFn reads the form's live --all and --include-untracked values
		// (the CommitOptionsGroup writes into the opts extractOpts returns), so
		// the status panel re-classifies as the user toggles either. When the
		// options form is skipped, these are the fixed launch flags.
		runner, err := runFormFn(form, entries, func() (bool, bool) {
			o := extractOpts()

			return o.All, o.IncludeUntracked
		})
```

- [ ] **Step 5: Update `commit/form_test.go` (stub signature + callers + reader test)**

In `commit/form_test.go`:

Change `swapRunFormFn`'s parameter type (currently `commit/form_test.go:326`):

```go
func swapRunFormFn(t *testing.T, fn func(*huh.Form, []git.StatusEntry, func() (bool, bool)) (formRunner, error)) {
```

Update every `swapRunFormFn(t, func(…) …)` caller's closure signature. The five callers that ignore the reader (`commit/form_test.go:443, 464, 476, 516, 540`) and the entries-forwarding one (`commit/form_test.go:576`) change `_ func() bool` → `_ func() (bool, bool)`, e.g.:

```go
		swapRunFormFn(t, func(_ *huh.Form, _ []git.StatusEntry, _ func() (bool, bool)) (formRunner, error) {
```

Replace the reader test (currently "allFn reads the live --all value from the form options", `commit/form_test.go:592-610`) with the two-value version:

```go
	t.Run("classifyFn reads the live --all and --include-untracked values", func(t *testing.T) {
		var gotFn func() (bool, bool)
		swapRunFormFn(t, func(_ *huh.Form, _ []git.StatusEntry, classifyFn func() (bool, bool)) (formRunner, error) {
			gotFn = classifyFn
			return &stubFormRunner{}, nil
		})

		hs := &fakeHistoryStore{}
		// Skip=true so no interactive options group is built; opts stays at
		// defaults, so classifyFn reflects the launch flags (both true here).
		_, _, err := FillOutForm(context.Background(), minimalCfg(),
			tui.CommitOption{All: true, IncludeUntracked: true, Skip: true}, hs, nil,
			[]git.StatusEntry{{XY: " M", Path: "x"}})
		if err != nil {
			t.Fatalf("FillOutForm: %v", err)
		}
		if gotFn == nil {
			t.Fatal("classifyFn was not passed to the runner")
		}
		all, iu := gotFn()
		if !all || !iu {
			t.Errorf("classifyFn() = (%v, %v), want (true, true)", all, iu)
		}
	})
```

- [ ] **Step 6: Run the affected tests to verify they pass**

Run:
```bash
mise exec -- go test ./tui/... -run TestFormRunner -v
mise exec -- go test ./commit/... -run TestFillOutForm -v
```
Expected: PASS — runner reader tests (incl. the new `--include-untracked` view test) and the `commit` form tests (incl. `classifyFn reads the live …`) green.

> If `TestFillOutForm` is not the exact test name, run the whole package: `mise exec -- go test ./commit/... -v` and confirm the `classifyFn reads the live …` and `forwards the status entries …` subtests pass.

- [ ] **Step 7: Green-gate checkpoint (no commit)**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
mise exec -- go vet ./...
mise exec -- go test ./...
```
Expected: build succeeds, vet clean, all packages `ok`.

---

## Task 5: CLI flag (`-u` / `--include-untracked`)

**Files:**
- Modify: `cmd/commit/commit.go` (`GetRootCmd` var block at `cmd/commit/commit.go:47-55`; flags at `cmd/commit/commit.go:65-73`; `Skip` predicate + option seed at `cmd/commit/commit.go:75-91`)

**Interfaces:**
- Consumes: `tui.CommitOption.IncludeUntracked` (Task 2); the whole chain wired in Tasks 1–4.
- Produces: `git zf commit -u` / `--include-untracked` sets `tui.CommitOption.IncludeUntracked` and skips the interactive options form (like the other option flags).

This is command wiring; following the repo's existing pattern the other option flags (`-a`, `-n`, `-s`, …) have no unit test at the `cmd` layer, so this task is verified by build/vet + a manual `--help` smoke check.

- [ ] **Step 1: Add the flag variable**

In `cmd/commit/commit.go`, add `includeUntracked` to the `var` block in `GetRootCmd` (currently `cmd/commit/commit.go:47-55`):

```go
	var (
		skip             bool
		all              bool
		amend            bool
		noVerify         bool
		signoff          bool
		allowEmpty       bool
		includeUntracked bool
		author           string
	)
```

- [ ] **Step 2: Register the `-u` flag**

In `cmd/commit/commit.go`, add the flag registration immediately after the `--all` flag (currently `cmd/commit/commit.go:67`). `-u` is currently unused by this command (`-y`, `-a`, `-n`, `-s` are taken):

```go
	f.BoolVarP(&all, "all", "a", false, "stage all tracked modified/deleted files before committing")
	f.BoolVarP(&includeUntracked, "include-untracked", "u", false, "stage untracked files before committing")
```

- [ ] **Step 3: Add the flag to the `Skip` predicate and seed the option**

In `cmd/commit/commit.go`, update the `RunE` closure (currently `cmd/commit/commit.go:75-91`) so passing `-u` skips the options form and seeds the value:

```go
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return c.runE(cmd, tui.CommitOption{
			Skip: skip ||
				cmd.Flags().Changed("all") ||
				cmd.Flags().Changed("include-untracked") ||
				cmd.Flags().Changed("amend") ||
				cmd.Flags().Changed("no-verify") ||
				cmd.Flags().Changed("signoff") ||
				cmd.Flags().Changed("allow-empty") ||
				cmd.Flags().Changed("author"),
			All:              all,
			IncludeUntracked: includeUntracked,
			Amend:            amend,
			NoVerify:         noVerify,
			Signoff:          signoff,
			AllowEmpty:       allowEmpty,
			Author:           author,
		})
	}
```

- [ ] **Step 4: Build and verify the flag is registered**

Run:
```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf commit --help
```
Expected: build succeeds; `--help` lists `-u, --include-untracked   stage untracked files before committing`.

- [ ] **Step 5: Green-gate checkpoint (no commit)**

Run:
```bash
mise exec -- go vet ./...
mise exec -- go test ./...
```
Expected: vet clean, all packages `ok`.

- [ ] **Step 6: Manual smoke (optional, user-run)**

In a scratch repo with an untracked file, `git zf commit -u` should show the untracked file under "Changes to be committed" in the panel and, on submit, include it in the commit. Because this stages files, leave it for the user to run interactively — do not automate a real commit.

---

## Self-Review

**1. Spec coverage** (`docs/superpowers/specs/2026-07-07-commit-include-untracked-design.md`):

- §Architecture 1 (option plumbing: git + tui fields + convert map) → Tasks 1 & 2. ✓
- §Architecture 2 (CLI flag + Skip predicate + seed) → Task 5. ✓
- §Architecture 3 (staging in `git.Client.Commit` via `ls-files --others --exclude-standard -z` + `git add --`) → Task 1 (`stageUntracked`). ✓
- §Architecture 4 (form toggle confirm) → Task 2 Step 4. ✓
- §Architecture 5 (panel re-classification: `groupEntries`/`StatusPanel` `includeUntracked`; `StatusPanelReserveWidth` 4 combos; `FormRunner` `func() (bool, bool)`; `FillOutForm` closure) → Tasks 3 & 4. ✓
- §Error handling (ls-files/add failure wrapped, returned before commit; no rollback) → Task 1 Steps 4–5 (errors returned before temp-file/commit). ✓
- §Testing — git 4 scenarios → Task 1; tui group/panel/reserve → Task 3; runner live toggle → Task 4; convert field → Task 2; commit classifyFn → Task 4. ✓

**2. Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N"/"write tests for the above". Every code step shows full code. ✓

**3. Type consistency:**
- `IncludeUntracked bool` — identical field name in `git.CommitOptions`, `tui.CommitOption`, and the convert mapping. ✓
- Reader type is `func() (bool, bool)` returning `(all, includeUntracked)` everywhere: `FormRunner.classifyFn`, `classify()`, `RunForm`, `runFormFn`, `swapRunFormFn`, `FillOutForm` closure. ✓
- `groupEntries`/`StatusPanel` are 3-arg `(entries, all, includeUntracked)` in impl and all callers/tests after Task 3. ✓
- `StatusPanelReserveWidth(entries)` keeps its 1-arg signature; only its body changes. ✓
- Untracked fold uses `Word: "new file"` matching `git status` and the render assertion `new file: <path>`. ✓
