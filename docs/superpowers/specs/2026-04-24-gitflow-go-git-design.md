# Design: CLI Restructure & go-git Migration (Step 1)

**Date:** 2026-04-24  
**Status:** Approved  
**Scope:** Step 1 of 2 — CLI restructure + go-git migration. Step 2 (tracker integration) is a separate spec.

---

## Overview

`commitizen-go` is evolving into an interactive git-flow CLI. This spec covers the first deliverable:

- Restructure the CLI: `git cz` becomes a dispatcher; `git cz commit` replaces the current default behaviour; `git cz issue` is introduced as a stub.
- Migrate the `git/` package from subprocess calls to `github.com/go-git/go-git/v6`.
- Expose all standard `git commit` flags through `git cz commit`, with Cobra-generated shell completions.
- Extend the TUI commit form with a second group for commit options (author, all, amend, no-verify, signoff, allow-empty).

---

## Section 1: CLI Structure

### Commands

| Command | Behaviour |
|---|---|
| `git cz` | Prints help, no action |
| `git cz commit` | TUI commit form — current default behaviour, now backed by go-git |
| `git cz issue` | Stub: prints `"not yet implemented"` and exits cleanly |
| `git cz install` | Unchanged |
| `git cz version` | Unchanged |

### `git cz commit` flags

All flags map 1:1 to `git.CommitOptions` fields and pre-fill the TUI form:

```
-a, --all           Stage all tracked modified/deleted files before committing
    --amend         Replace the tip of the current branch
-n, --no-verify     Bypass pre-commit and commit-msg hooks
-s, --signoff       Add Signed-off-by trailer to the commit message
    --allow-empty   Allow a commit with no changes
    --author        Override commit author as "Name <email>"
```

Cobra generates shell completions for all flags automatically.

### `git cz issue` stub

The `issue` subcommand is defined in full in `cmd/issue.go` (flags, usage, short/long descriptions) so Step 2 can fill in the body without modifying the CLI layer.

---

## Section 2: go-git Migration (`git/` package)

### Dependency

```
github.com/go-git/go-git/v6
```

### Public API

```go
// CommitOptions maps CLI flags to go-git commit behaviour.
type CommitOptions struct {
    All        bool
    Amend      bool
    NoVerify   bool
    Signoff    bool
    AllowEmpty bool
    Author     string // "Name <email>" format; empty = git config identity
}

// Commit assembles and records the commit using go-git.
func Commit(msg []byte, opts *CommitOptions) error
```

### Internal operations

| Operation | go-git v6 call |
|---|---|
| Open repo + detect `.git` | `gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})` |
| Stage tracked modified/deleted (`--all`) | `worktree.AddWithOptions(&gogit.AddOptions{All: true})` |
| Commit | `worktree.Commit(msg, &gogit.CommitOptions{...})` |
| Skip hooks (`--no-verify`) | `CommitOptions.NoVerifyHooks = true` *(verify exact field name against go-git v6 API during implementation)* |
| Amend | `CommitOptions.Amend = true` |
| Signoff | Append `Signed-off-by: Name <email>` trailer to `msg` before committing |
| Author override | Parse `"Name <email>"` → `object.Signature`; set on `CommitOptions.Author` |

**`--all` semantics:** `worktree.AddWithOptions(&gogit.AddOptions{All: true})` stages modifications and deletions of tracked files only — untracked files are not touched. This matches `git commit --all` behaviour exactly (not `git add .`).

**Author fallback:** if `--author` is not provided, go-git uses `user.name`/`user.email` from git config.

---

## Section 3: TUI Commit Form Extension

The commit form gains a **second huh group** (rendered as a second page):

### Group 1 — Commit message (unchanged)
Type, scope, subject, body, footer — existing fields.

### Group 2 — Commit options

| Field | Widget | Behaviour |
|---|---|---|
| Author | `huh.NewSelect[string]` | Deduplicated `"Name <email>"` from repo log; current user default |
| Stage all tracked | `huh.NewConfirm` | Maps to `--all` |
| Amend | `huh.NewConfirm` | Maps to `--amend` |
| Skip hooks | `huh.NewConfirm` | Maps to `--no-verify` |
| Signoff | `huh.NewConfirm` | Maps to `--signoff` |
| Allow empty | `huh.NewConfirm` | Maps to `--allow-empty` |

**Flag pre-filling / skip rule:** If any commit-option flag (`--all`, `--amend`, `--no-verify`, `--signoff`, `--allow-empty`, `--author`) is passed, Group 2 is skipped entirely — the flags are taken as-is. Group 2 only renders when no commit-option flag is provided. Global flags (`-d`, `-h`) do not affect this rule.

**Author list construction (go-git):** iterate `repo.Log(&gogit.LogOptions{})`, collect unique `"Name <email>"` strings, sort alphabetically, prepend the current git config identity as the default selection.

---

## Section 4: Package & File Structure

```
commitizen-go/
├── cmd/
│   ├── root.go       # stripped to pure dispatcher (no commit logic)
│   ├── commit.go     # NEW — git cz commit subcommand + flag definitions
│   ├── issue.go      # NEW — git cz issue stub (full Cobra definition)
│   ├── install.go    # unchanged
│   └── version.go    # unchanged
├── commit/
│   ├── form.go       # extended: second huh group for commit options + author list
│   ├── form_test.go  # extended
│   └── defaultConfig.go  # unchanged
├── git/
│   ├── git.go        # rewritten: go-git v6, Commit(msg []byte, opts *CommitOptions)
│   ├── git_test.go   # NEW — in-memory repo tests
│   └── (subprocess helpers removed)
├── go.mod            # add go-git/v6
└── main.go           # unchanged
```

**Migration path:** `cmd/root.go` currently wires `commit.FillOutForm()` → `git.CommitMessage()`. After this change, that logic moves to `cmd/commit.go`. `root.go` becomes a thin `rootCmd` with `AddCommand(commitCmd, issueCmd)`. Existing behaviour is fully preserved under `git cz commit`.

---

## Section 5: Testing

### `git/` package
Tests use an in-memory go-git repository (exact init syntax — e.g. `gogit.Init(memory.NewStorage(), memfs.New())` — to be confirmed against go-git v6 API) — no temp directories, no subprocess calls:

| Test | Verifies |
|---|---|
| `TestCommit_basic` | Commit message stored correctly |
| `TestCommit_all` | Only tracked modified/deleted files staged; untracked files ignored |
| `TestCommit_signoff` | `Signed-off-by:` trailer appended to message |
| `TestCommit_author` | Custom author signature applied |
| `TestCommit_amend` | Tip commit replaced; history length unchanged |

### `commit/` package
Extends existing `form_test.go`:

| Test | Verifies |
|---|---|
| `TestAuthorList_dedup` | Duplicate authors collapsed to one entry |
| `TestAuthorList_sort` | Authors sorted alphabetically |
| `TestAuthorList_currentUserFirst` | Current git config identity is the default selection |
| `TestFlagPreFill_author` | `--author` flag sets correct default in form |
| `TestFlagPreFill_all` | `-a` flag sets "Stage all tracked" confirm to yes |

---

## Out of Scope (Step 2)

- `tracker/` package (Plane, Redmine, pluggable registry)
- `branch/` package (branch naming template, slug, UUID)
- `store/` package (SQLite in `.git/git-cz.db`)
- Full `git cz issue` TUI flow
