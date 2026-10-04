# Propose-to-push after close / commit / review

**Date:** 2026-06-25
**Status:** Approved (design)

## Goal

After a state-changing command succeeds, **offer to push the relevant branch to
the remote** — showing a dry-run preview first and pushing only on confirm. This
removes a recurring manual `git push` step and closes two concrete gaps where the
tool already does most of the work but leaves the remote stale:

- **`issue close`** merges the feature branch into the integration/parent branch
  locally and pushes the *branch ref* (`refs/zf/branches/<slug>`), but never
  pushes the **integration branch itself**. `origin/<parent>` stays behind until
  the operator remembers to push.
- **`review request`** locks the branch and pushes the *review ref*, then tells
  the reviewer `git fetch && git zf review start` — but it never pushes the
  **feature branch**, so there is nothing for the reviewer to fetch unless the
  developer pushed separately.

The same end-of-command step also applies to `commit` (publish the branch you
just committed on, with a heads-up about how it will merge into its parent) and
to `review approve`/`review reject` (publish reviewer commits on `<slug>@review`
so the developer can incorporate or inspect them).

## Scope

The propose-to-push step is added to all five commands:

| Command          | Branch pushed              | Preview contents                          |
|------------------|----------------------------|-------------------------------------------|
| `issue close`    | merge target (parent/base) | push dry-run                              |
| `commit`         | current branch             | push dry-run *(+ merge-vs-parent — Phase 2)* |
| `review request` | feature branch             | push dry-run                              |
| `review approve` | `<slug>@review`            | push dry-run (only if reviewer commits)   |
| `review reject`  | `<slug>@review`            | push dry-run (only if reviewer commits)   |

Out of scope: force-pushing (never), pushing custom refs (already handled by
`PushBranchRef`/`PushReviewRef`), pushing more than the one relevant branch, and
the still-placeholder `branch merge` command.

## Phasing

The work splits into two independently shippable phases. The split is along the
one seam that carries risk: the merge-vs-parent preview is the *only* part that
needs a parent branch, and resolving the parent is the *only* edit that touches
existing close logic (the HIGH-risk `resolveDefaultBase` refactor).

- **Phase 1 — uniform push proposal (all five commands).** `git.PushDryRun` /
  `PushBranch`, the `cmd/pushflow` package, config + `--push`/`--no-push`, and the
  five call sites. Every command shows the *push dry-run* preview only. This phase
  is **purely additive** — it changes no existing close/commit/review logic, so it
  carries no HIGH-risk edit. For `commit`, the Phase-1 gate is simply "a remote is
  configured AND the dry-run shows something to push" (no parent resolution
  needed).
- **Phase 2 — merge-vs-parent enrichment (`commit` only).** Add the
  `issueflow.ResolveParentBranch` extraction (the behaviour-preserving
  `resolveDefaultBase` refactor) and the FF / merge-commit / conflict preview to
  `commit`. Additive on top of the proven Phase-1 plumbing; the merge preview is
  the only new surface.

Sections below are tagged **(Phase 2)** where they belong to the second phase;
everything else is Phase 1.

## Background (current behaviour)

- The merge/FF/ancestry plumbing already exists on `*git.Client`:
  `MergeDryRun` (in-memory `merge-tree` conflict preview), `FastForwardOnly`,
  `IsAncestor`, `Fetch`, `CommitsAhead`, `Remote`.
- There is **no generic branch-push helper**. Only `PushBranchRef`
  (`git/branch_ref.go:123`) and `PushReviewRef` (`git/review_ref.go:147`) exist,
  and they push custom `refs/zf/*` refs, not `refs/heads/*`.
- `close` resolves its merge target via `resolveDefaultBase`
  (`cmd/issue/close.go:230`): `cfg.Branch.Base` → `DefaultBaseBranch()`, then a
  parent redirect via `store.GetParentIssue` with a `refs/zf/branches/<slug>`
  ref fallback. `commit` needs the same parent resolution but has no access to it
  today.
- `commit` (`cmd/commit/commit.go`) opens the store and computes an `IssueHint`
  from the current branch but performs no remote operations.
- Cross-command shared helpers already live in `cmd/issueflow` (e.g.
  `ReconcileMergedFromRefs`, `ApplyTrackerStatus`), establishing the pattern this
  design follows for `cmd/pushflow`.

## Behaviour

### The proposal step

When a command reaches its propose-to-push point it:

1. **Resolves the remote.** No remote configured → skip silently (local-only repo).
2. **Runs the push dry-run** for the target branch (`git push --porcelain
   --dry-run`). The remote is contacted for ref negotiation (no object transfer),
   so the result reflects the *true* remote state, not a stale tracking ref. The
   per-ref **porcelain flag character** is the authoritative status (`=` up to
   date, ` ` fast-forward, `*` new ref, `!` rejected); a leading `=` means
   **nothing to push** → skip silently (no proposal shown). Detection is by flag
   char, never by scraping the human-readable "Everything up-to-date" line; the
   command runs under `LC_ALL=C` for stability. If the dry-run errors (remote
   unreachable) → treat as skip (non-fatal; see Error handling).
3. **Builds the preview** and prints it:
   - *Push preview* — rendered from `PushOutcome`: `[new branch]` (NewBranch),
     `<old>..<new>` (FastForward), or a `! rejected (non-fast-forward)` warning
     (Rejected) when the remote has diverged. (UpToDate never reaches here — it
     was skipped at step 2.)
   - *Merge-vs-parent preview* **(Phase 2, commit only)** — one of:
     - `Fast-forwards into <parent>` — current is strictly ahead of the parent.
     - `Merges into <parent> with a merge commit (no conflicts)` — diverged but
       clean (`MergeDryRun` returns no conflicts).
     - `⚠ Conflicts with <parent>: <files…>` — `MergeDryRun` reports conflicts.
     - `Already merged into <parent>` — current is an ancestor of the parent
       (nothing to merge); shown for information.
4. **Confirms.** Interactive prompt, **default Yes**. On Yes → `PushBranch`. On
   No → skip, no error.

A diverged (`non-fast-forward`) push is surfaced in the preview but the push is
**never** forced; if the user confirms, the real `git push` will fail and its
error is reported. The tool does not attempt `--force`.

### Control surface

- **Config master switch** `push.propose` (bool, default `true`). When `false`
  the step is skipped everywhere.
- **`--push`** — auto-confirm the proposal (no prompt) and override the
  `-y`/non-interactive skip. Use in CI/scripts that *do* want the push.
- **`--no-push`** — skip the proposal entirely for this run.
- **`-y` / non-interactive** (no TTY) — skip the push **unless `--push` is given**.
  Never push silently as a side effect of `-y`.

`--push` and `--no-push` are mutually exclusive (error if both set). The flags are
added to all five commands; `commit` already has `-y`.

### Per-command placement

- **`issue close`** — final step of `runClose`, after the branch delete and
  before/with the `Branch %q merged…` message. Target = the resolved merge target
  (`base`). Skipped on the `errFastForwardDeferred` path (local base did not
  advance, so there is nothing new to push).
- **`commit`** — after `client.Commit` succeeds. Target = current branch.
  Phase 1: push dry-run only. Phase 2 adds the merge-vs-parent preview; the parent
  is resolved via `ResolveParentBranch` (see below) and, if none resolves, the
  merge preview is omitted while the push proposal still runs.
- **`review request`** — in `runReviewRequestInteractive`, after `runReviewRequest`
  succeeds. Target = the feature branch.
- **`review approve` / `review reject`** — after the ref write/push + store
  update. Target = `<slug>@review`, **guarded**: skipped unless the review branch
  exists and has reviewer commits ahead of the feature branch (the
  `hasCommits` signal both commands already compute).

## Architecture (Approach A)

### New `git.Client` helpers (`git/push.go`)

```go
// PushOutcome is the parsed result of a dry-run push for one branch.
type PushOutcome struct {
    Kind    PushKind // UpToDate | NewBranch | FastForward | Rejected
    Summary string   // friendly line for the preview, e.g. "abc1234..def5678" or "[new branch]"
}

// PushDryRun runs `git push --porcelain --dry-run <remote> <branch>:<branch>`
// (under LC_ALL=C) and parses the per-ref porcelain flag char into a PushOutcome.
// Kind == UpToDate (flag '=') tells the caller to skip the proposal. Returns a
// zero PushOutcome with ok=false when no remote is configured. A dry-run error
// (remote unreachable) is returned to the caller, which treats it as skip.
func (c *Client) PushDryRun(ctx context.Context, branch string) (out PushOutcome, ok bool, err error)

// PushBranch runs `git push <remote> <branch>:<branch>` (never --force).
// No-op when no remote is configured.
func (c *Client) PushBranch(ctx context.Context, branch string) error
```

Both shell out through `exec` like the other helpers. `--porcelain` sends a
tab-separated status line per ref to **stdout** whose leading flag char is the
stable, locale-independent status (`=`/` `/`*`/`!`); `PushDryRun` reads that flag
rather than scraping the stderr summary.

### New `cmd/pushflow` package

```go
type ConfirmFunc func(ctx context.Context, summary string) (bool, error)

type Opts struct {
    Branch              string // branch to push
    AutoConfirm         bool   // --push: skip the prompt, treat as Yes
    Skip                bool   // --no-push or config push.propose=false
    NonInteractive      bool   // -y / no TTY (skip unless AutoConfirm)

    // Phase 2 (commit only); zero values in Phase 1 mean "no merge preview".
    IncludeMergePreview bool   // render the merge-vs-parent line
    Parent              string // parent branch for the merge preview (origin/<parent>)
}

// Propose runs the full preview → confirm → push step. Returns nil on every
// skip path (no remote, nothing to push, declined, gated off).
func Propose(ctx context.Context, c Pusher, opts Opts, confirm ConfirmFunc) error
```

- `Pusher` is a narrow role interface satisfied by `*git.Client`, mirroring the
  `Committer`/`BranchClient` role-interface pattern already used in `cmd/`.
  Phase 1 needs only `PushDryRun`, `PushBranch`, `Remote`, `IO`; Phase 2 adds
  `IsAncestor` + `MergeDryRun` for the merge preview.
- `NewHuhConfirm(io)` returns a production `ConfirmFunc` (default-Yes huh
  confirm). Tests inject a stub. This keeps the three existing prompter
  interfaces (`ClosePrompter`, `ReviewPrompter`, the commit form) untouched.
- The merge-preview string (Phase 2) is built from `IsAncestor` + `MergeDryRun`.

### Shared parent resolution (refactor) — Phase 2

Extract the full merge-target resolution of `resolveDefaultBase`
(`cmd/issue/close.go:229-270`) — base seed (`cfg.Branch.Base` →
`DefaultBaseBranch`) **and** the parent redirect (`GetParentIssue` →
`refs/zf/branches/<slug>` fallback → `ListBranches` slug→name map) — into:

```go
// cmd/issueflow
func ResolveParentBranch(ctx context.Context, s ParentStore, c ParentClient,
    issueSlug, cfgBase string) (string, error)
```

`ParentStore` (`GetParentIssue`, `ListBranches`) and `ParentClient`
(`ReadBranchRef`, `FetchBranchRefs`, `DefaultBaseBranch`) are the narrow role
interfaces the GitNexus `context` trace shows `resolveDefaultBase` actually
touches. `commit` calls `ResolveParentBranch` directly to find the parent for its
merge preview.

**Behaviour-preserving delegation.** `resolveDefaultBase(ctx, deps, picked)` keeps
its exact signature and becomes a one-line wrapper around `ResolveParentBranch`,
so `runClose`'s call site is byte-for-byte unchanged. The refactor moves logic
without altering the close path's behaviour.

## Impact analysis (GitNexus)

Run before finalising this design; folded in here so the plan reflects the real
blast radius.

- **`resolveDefaultBase` — risk HIGH, but contained and deferred to Phase 2.**
  It has exactly **one** direct caller (`runClose`); the HIGH rating reflects its
  position on the critical close path (`runClose → closeRunE → Issue.runE`), not a
  wide fan-out. Mitigation: the behaviour-preserving delegation above keeps the
  call site unchanged, the existing `close_e2e_test.go` suite guards the close
  path, and `detect_changes` is run pre-commit to confirm only the expected
  symbols/flows moved. **This is the only HIGH-risk edit in the plan, and the
  phasing isolates it to Phase 2 — Phase 1 is entirely additive (no existing
  logic touched).**
- **`runClose` — risk LOW (upstream).** Only `closeRunE` calls it; appending the
  push step is additive.
- **`Commit.runE` — risk LOW.** Only `GetRootCmd` wires it; additive.
- **No existing `refs/heads/*` push flow.** The graph shows only `PushBranchRef`
  and `PushReviewRef` (custom `refs/zf/*` refs). `PushBranch`/`PushDryRun` are
  genuinely new — no duplication — and the `Remote()`-gating pattern they follow
  is the one already used by `doSquashCommit`, `reviewPreflight`, and
  `runReviewSyncInteractive`.

### Config

Add a `Push` section to `AppConfig` with `Propose bool` (default `true`), wired
through the existing Viper load in `config`. Default JSON/TOML gains
`push.propose = true`.

## Error handling

- Every skip path returns `nil` — a declined or impossible push is not a failure.
- A confirmed push that fails (diverged remote, auth, network) returns the
  wrapped `git push` error from the command; the preceding action (merge, commit,
  review transition) has already completed and is **not** rolled back. The push is
  an additive final step.
- `PushDryRun` failures are non-fatal: log/print a warning and fall through to
  *not* offering the push (consistent with the "remote unreachable → skip"
  behaviour), so a flaky remote never blocks a completed close/commit.

## Testing

Per the project convention, every distinct scenario is a `t.Run` subtest.

- **`cmd/pushflow` unit tests** with a fake `Pusher` + scripted `ConfirmFunc`:
  - skip when no remote / nothing to push / `Skip` / non-interactive without
    `AutoConfirm`;
  - `AutoConfirm` pushes without calling confirm;
  - confirm Yes → `PushBranch` called once with the right branch; No → not called;
  - a failing `PushBranch` surfaces its error;
  - **(Phase 2)** merge-preview composition for each case (FF / merge-commit /
    conflicts / already-merged).
- **`git` helper tests** (`git/push_test.go`) against an on-disk repo with a
  local bare "remote": `PushDryRun` maps each porcelain flag to the right
  `PushOutcome.Kind` — new-branch (`*`), up-to-date (`=`), fast-forward (` `),
  and rejected (`!`, set up by advancing the remote past local); `PushBranch`
  actually advances the remote ref; both no-op without a remote.
- **E2E**: extend `close_e2e_test.go` and the review suites with a scripted
  push-confirm to assert the proposal fires (and is correctly skipped) at the
  right point; add commit coverage for the push proposal. **(Phase 2)** add
  commit coverage for the merge-vs-parent preview and the `ResolveParentBranch`
  delegation (assert close behaviour is unchanged).

## Open decisions (resolved)

- Push model: **preview, then push on confirm** (not preview-only, not silent push).
- Commit scope: proposal appears for **any branch with a resolvable upstream/parent**.
  Phase 1 gate is "remote configured AND dry-run shows something to push"; the
  parent-aware merge preview lands in Phase 2.
- Phasing: **merge-vs-parent preview deferred to Phase 2** to keep Phase 1 purely
  additive and isolate the one HIGH-risk refactor.
- Review subcommands: **all three** (request, approve, reject).
- Confirm default: **Yes**.
- `-y`/non-interactive: **skip unless `--push`**.
