# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Install

```bash
make                   # build ./bin/git-zf binary
make install           # copy binary to $(git --exec-path)/git-zf
./bin/git-zf install # alternative install without make
```

The Makefile auto-detects OS (Linux/Darwin/Windows) and sets `GOOS`/`GOARCH`. Version info is injected via ldflags from `git describe` and `git log`.

## Running & Testing

```bash
mise exec -- go test ./...                    # run all tests
mise exec -- go test ./commit/... -run TestX  # run a single test
mise exec -- go build -o ./bin/git-zf .      # manual build
./bin/git-zf -d               # run with debug logging → debug.log
```

#### Testing the close flow

The close flow is end-to-end tested in `cmd/issue/close_e2e_test.go`. Tests
construct a real on-disk repo, a seeded SQLite store, and the in-process
tracker fake at `tracker/fake/`, then drive the flow with a `scriptedPrompter`
that returns canned answers instead of opening huh forms.

To exercise just the close-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v

When adding a new merge strategy or changing the merge/store/tracker
sequencing, add a corresponding E2E test alongside the existing happy-path
and failure-mode tests.

Worktree-held branches are covered by `newWorktreeCloseRig` in the same file
(feature branch checked out in a linked worktree). The engine-level worktree
cases live in `cmd/mergeflow/mergeflow_worktree_test.go` (`newWorktreeRig`).

    mise exec -- go test ./cmd/issue/... -run "^TestClose_Worktree" -v
    mise exec -- go test ./cmd/mergeflow/... -run "Worktree" -v

### Testing the start flow

The issue-start flow (used by both `issue start` and `branch new`) is
end-to-end tested in `cmd/issue/start_e2e_test.go`. The pattern mirrors the
close-flow tests: a real on-disk repo, the fake tracker at `tracker/fake/`,
and a `scriptedStartPrompter` that returns canned answers for every huh form.

To exercise just the start-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_" -v

When adding a new toggle, confirm, or picker form, extend `StartPrompter` and
add a matching E2E test alongside the existing happy-path and failure-mode
tests.

### Testing the prune flow

The branch-prune flow is end-to-end tested in `cmd/branch/prune_e2e_test.go`.
Tests construct a real on-disk repo + seeded store, then drive the flow with
a `scriptedPrunePrompter` (canned confirm responses) or
`autoConfirmPrunePrompter` (mirrors `--yes`).

To exercise just the prune-flow tests:

    mise exec -- go test ./cmd/branch/... -run "^TestRunPrune_" -v

For non-interactive use (CI, cron), pass `--yes` to skip the confirmation
prompt:

    git zf branch prune --yes


### Testing the repo issues

Issues stored in the repository (`refs/zf/issues/*`) are tested at three
levels, all on real on-disk repos:

- `issue/record_test.go` — the fold (pure, no git).
- `git/issue_ref_test.go`, `git/issue_ref_sync_test.go` — plumbing: write and
  read a chain, fetch into `refs/zf/remote/issues/*`, reconcile, push.
- `issue/repo_test.go` — the glue (`Create`, `Append`, `Load`, `Resolve`,
  `Sync`), including two clones diverging and merging.
- `cmd/issue/record_e2e_test.go`, `record_ops_e2e_test.go` — the `new`, `show`,
  `comment`, `label` and `sync` commands, driven by a `scriptedRecordPrompter`
  on a `recordRig` (`newRecordRig(t, user, origin)`; pass `newBareOrigin(t)` to
  two rigs to get two clones of one remote).

    mise exec -- go test ./issue/... ./git/... -run "TestFold|TestIssueRef_|TestPushFetchSync" -v
    mise exec -- go test ./cmd/issue/... -run "^TestRun(New|Show|Comment|Label|Sync)" -v

The start, close and list integrations live next to their flows:
`start_record_e2e_test.go`, `close_record_e2e_test.go`, `list_record_test.go`.

When adding an op type, add its constant and its `Fold` case in
`issue/record.go` with a table case in `TestFold`. Unknown types must keep
being skipped: an older binary reads refs written by a newer one.

### Testing the menus

`git zf` (no subcommand), `git zf review`, `git zf branch` and `git zf issue`
open an action menu built on
`cmdutil.RunMenu` (`cmd/cmdutil/menu.go`): it takes the parent command, a
title, the ordered subcommands to offer, and a `MenuPrompter`. The picked
subcommand's `RunE` runs with the *parent* command (cobra v1.1.3 has no
`SetContext`), so it sees its flags as unset and runs interactively. No TTY on
stdin → the group's help is printed; Esc → quiet exit.

    mise exec -- go test ./cmd/cmdutil/... -run "^TestRunMenu|^TestMenuOptions" -v
    mise exec -- go test ./cmd/ -run "^TestGetRootCmd_menu" -v
    mise exec -- go test ./cmd/review/... -run "^TestReviewRootCmd" -v

To add an entry, append the subcommand to `rootMenu` (`cmd/root.go`),
`Review.menuSubs` (`cmd/review/review.go`) or the slice in `GetRootCmd` of
`cmd/branch/branch.go` / `cmd/issue/issue.go`, and extend the matching test. A
subcommand offered by a menu reads its string flags with `cmdutil.StringFlag`,
never `cmd.Flags().GetString`: the parent command does not define them.

### Testing the merge flow

The shared merge engine (`cmd/mergeflow`) is unit-tested in
`cmd/mergeflow/mergeflow_test.go` (real on-disk repo, scripted
`mergeflow.Prompter`, one path per strategy plus conflict / abort / materialized
rollback). The `branch merge` command is end-to-end tested in
`cmd/branch/merge_e2e_test.go` with a `scriptedMergePrompter`.

    mise exec -- go test ./cmd/mergeflow/... -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunMerge_" -v

`issue close` and `branch merge` share `cmd/mergeflow` as their merge engine.
`branch merge` REFUSES issue branches as the source (it redirects to `issue
close`). When changing the refusal or the shared engine, keep the close E2E
suite green — it is the regression net for the extraction.


## Architecture

Three packages under `github.com/piprim/git-zf`:

- **`cmd/`** — Cobra CLI entry point. `root.go` wires config loading (Viper) and the other commands.
- **`tui/`** — TUI form logic using `github.com/charmbracelet/huh`.
- **`git/`** — Thin wrappers around `go-git`.

## Configuration

Config file: `.git-zf.toml` in the repository's git dir (`<repo>/.git/.git-zf.toml`) or `$HOME`. The repo-level file takes priority. The `commit-message.items` array overrides the default form fields; `commit-message.template` is a Go `text/template` string. If no config is found, the embedded `config/default.toml` is used.

## Go Toolchain

- Go is managed by **mise**. Run `mise install` if the expected Go version is not active.
- To launch Go command the proper way is to use `mise exec -- go…`

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **git-zf** (4051 symbols, 15823 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/git-zf/context` | Codebase overview, check index freshness |
| `gitnexus://repo/git-zf/clusters` | All functional areas |
| `gitnexus://repo/git-zf/processes` | All execution flows |
| `gitnexus://repo/git-zf/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
