# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Install

```bash
make                   # build ./bin/git-zf binary
make install           # copy binary to $(git --exec-path)/git-zf
./bin/git-zf install # alternative install without make
```

The Makefile builds for the host platform (`go build` defaults). Version info is injected via ldflags from `git describe` and `git log`.

## Running & Testing

```bash
mise exec -- go test ./...                    # run all tests
mise exec -- go test ./commit/... -run TestX  # run a single test
mise exec -- go build -o ./bin/git-zf .      # manual build
./bin/git-zf -d               # run with debug logging → debug.log
```

#### Testing the close flow

The close flow is end-to-end tested in `cmd/issue/close_e2e_test.go`. Tests
construct a real on-disk repo, a seeded branch chain, and the in-process
tracker fake at `tracker/fake/`, then drive the flow with a `scriptedPrompter`
that returns canned answers instead of opening huh forms.

To exercise just the close-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v

When adding a new merge strategy or changing the merge/record/tracker
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
Tests construct a real on-disk repo + seeded branch chains, then drive the flow
with a `scriptedPrunePrompter` (canned confirm responses) or
`autoConfirmPrunePrompter` (mirrors `--yes`). `newPruneRigWithOrigin` adds a
remote, for the rules that depend on it (a branch still on the remote, an
unreachable remote).

To exercise just the prune-flow tests:

    mise exec -- go test ./cmd/branch/... -run "^TestRunPrune_" -v

For non-interactive use (CI, cron), pass `--yes` to skip the confirmation
prompt:

    git zf branch prune --yes

`branch close <branch-name>` (one branch, no confirmation) is tested in
`cmd/branch/close_e2e_test.go` on the same rig:

    mise exec -- go test ./cmd/branch/... -run "^TestRunCloseBranch|^TestCloseCmd" -v


### Testing the repo issues

Issues stored in the repository (`refs/zf/issues/*`) are tested at three
levels, all on real on-disk repos:

- `issue/record_test.go` — the fold (pure, no git).
- `git/issue_ref_test.go`, `git/issue_ref_sync_test.go` — plumbing: write and
  read a chain, fetch into `refs/remotes/<remote>/zf/issues/*`, reconcile, push.
- `issue/repo_test.go` — the glue (`Create`, `Append`, `Load`, `Resolve`,
  `Sync`), including two clones diverging and merging.
- `cmd/issue/record_e2e_test.go`, `record_ops_e2e_test.go` — the `new`, `show`,
  `edit`, `comment`, `label`, `sync` and `close <id>` commands, driven by a
  `scriptedRecordPrompter` on a `recordRig` (`newRecordRig(t, user, origin)`;
  pass `newBareOrigin(t)` to two rigs to get two clones of one remote).

    mise exec -- go test ./issue/... ./git/... -run "TestFold|TestIssueRef_|TestPushFetchSync" -v
    mise exec -- go test ./cmd/issue/... -run "^TestRun(New|Show|Edit|Comment|Label|Sync|CloseByID)" -v

The start, close and list integrations live next to their flows:
`start_record_e2e_test.go`, `close_record_e2e_test.go`, `list_record_test.go`.

When adding an op type, add its constant and its `Fold` case in
`issue/record.go` with a table case in `TestFold`. Unknown types must keep
being skipped: an older binary reads refs written by a newer one.

### Testing the tracker mirror

With `[issue-tracker] mirror = true`, the repo issues are mirrored with one
tracker project (`issue.Mirror`, `issue/mirror.go`). It is tested at four
levels, on real on-disk repos and the fake tracker:

- `issue/record_test.go` — `TestFold_Tracker`: the link and the tracker state.
- `git/chain_ref_test.go` — `TestChainRef_FixedRoot`: the deterministic root,
  pinned by a golden hash.
- `issue/mirror_test.go` — `Reconcile`: import, export, the eight rows of the
  state table (`TestReconcile_StateTable`), two clones importing offline.
- `cmd/issue/mirror_e2e_test.go`, `close_mirror_e2e_test.go` — the commands.

    mise exec -- go test ./issue/... -run "TestReconcile|TestFold_Tracker|TestResolve_TrackerNumber" -v
    mise exec -- go test ./git/... -run "TestChainRef_FixedRoot" -v
    mise exec -- go test ./cmd/issue/... -run "^TestMirror|^TestClose_Mirror" -v

The fake tracker is stateful for these calls: `CreateIssue` lists the issue,
`SetIssueOpen` / `CloseIssue` / `ReopenIssue` move it in and out of the open
listing. Use `CloseIssue` for "someone closed it in the tracker's UI": it is
not recorded in `RecordedOpens`.

`importRootTemplate` (`issue/mirror.go`) is frozen: the ID of every imported
issue is the hash of a commit holding those bytes. Never edit it; if
`TestChainRef_FixedRoot` fails on the golden hash, the code is wrong. Only
`issue list` and the commands that create an issue or change its state
reconcile; a new read-only command must not.

`issue.List` reads every issue with `ReadAllChains` (three git processes) and
`issue.PushAll` pushes many issues in one `git push` (`PushChainRefs`); a loop
of `Load` or `Push` over many issues is the thing to avoid.

    mise exec -- go test ./git/... -run "TestPushChainRefs" -v
    mise exec -- go test ./issue/... -run "TestPushAll|TestList_" -v

### Testing the branch chains

Tracked branches live in the repository (`refs/zf/branches/<slug>`, one chain
per issue): there is no local database. They share the chain plumbing of the
issues and reviews (`git.BranchRefs`) and are tested at the same levels:

- `branch/record_test.go` — the fold (pure, no git): `TestFold`, `TestRows`.
- `git/chain_ref_test.go` — `ReadAllChains` (every chain in three git
  processes), the leased delete of a legacy blob.
- `branch/repo_test.go` — `Start`, `SetStatus`, `Load`, `List`, `Find`, `Sync`,
  two clones starting variants offline, legacy blobs.
- the start, close, prune and review E2E suites — the commands.

    mise exec -- go test ./branch/... -v
    mise exec -- go test ./git/... -run "TestChainRef_ReadAllChains|TestChainRef_DeleteLease" -v

Seed a tracked branch in a test with `branchtest.Seed` (never by writing refs by
hand); `branchtest.Amend` fills the parent, tracker type or issue ID of a branch
a rig already seeded. A git client caches its remote name: add the remote
before the first seed.

When adding a branch op type, add its constant and its case in `apply`
(`branch/record.go`) with a table case in `TestFold`. Unknown types must keep
being skipped. A command that offers a choice of in-progress branches calls
`branch.Fetch` first; a hook or `commit` never does, and finds its branch with
`branch.Find`.

### Testing the review chains

Reviews stored in the repository (`refs/zf/reviews/*`) share the chain plumbing
of the issues (`git/chain_ref.go`, `git/chain_ref_sync.go`, parameterized by
`git.IssueRefs` / `git.ReviewRefs`) and are tested at the same levels:

- `internal/chain/chain_test.go` — the op ordering both folds use.
- `review/record_test.go` — the fold (pure, no git), including the concurrent
  cases.
- `git/chain_ref_test.go` — the review namespace, tracking refs under
  `refs/remotes/<remote>/zf/`, legacy blob refs.
- `review/repo_test.go` — `Append`, `Load`, `List`, `Sync`, two clones
  approving offline.
- `cmd/review/chain_e2e_test.go`, and `^TestClose_SignedGate` /
  `^TestClose_ReviewPreflight` in `cmd/issue/close_e2e_test.go` — the commands
  and the signing gate.

    mise exec -- go test ./internal/chain/... ./review/... -v
    mise exec -- go test ./git/... -run "TestChainRef|TestCommitSignature" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_SignedGate" -v

Seed a review in another package's test with `reviewtest.Seed` (never by
writing refs by hand). Tests that sign use `gittest.SSHSigner`, which skips
when `ssh-keygen` is missing.

When adding a review op type, add its constant and its case in `apply`
(`review/record.go`) with a table case in `TestFold`. Unknown types must keep
being skipped.

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

Main packages under `github.com/piprim/git-zf`:

- **`cmd/`** — Cobra CLI entry point. `root.go` wires config loading (Viper) and the other commands.
- **`tui/`** — TUI form logic using `github.com/charmbracelet/huh`.
- **`git/`** — Thin wrappers around `go-git` and the git CLI, including the commit-chain plumbing (`chain_ref.go`).
- **`branch/`**, **`issue/`**, **`review/`** — what git-zf records in the repository, one chain family each under `refs/zf/`: a pure fold (`record.go`) and the git glue (`repo.go`). There is no database.

## Configuration

Config file: `.git-zf.toml` in the repository's git dir (`<repo>/.git/.git-zf.toml`) or `$HOME`. The repo-level file takes priority. The `commit-message.items` array overrides the default form fields; `commit-message.template` is a Go `text/template` string. If no config is found, the embedded `config/default.toml` is used.

## Go Toolchain

- Go is managed by **mise**. Run `mise install` if the expected Go version is not active.
- To launch Go command the proper way is to use `mise exec -- go…`

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **git-zf** (4696 symbols, 19773 relationships, 300 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

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
