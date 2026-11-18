# Review health check — design

Date: 2026-11-18. Source: `review.md` (untracked, repo root), an architecture
review with six concerns and five recommendations.

## Intent

A health check: fix what is worth fixing now, write down the rest. Nothing in
the current structure is blocking work.

## Goals

1. Tracker adapters are registered at the composition root, not as a side
   effect of importing `cmd/issue`.
2. `commit` has no package-level mutable form runner; its form tests can run
   in parallel.
3. The deferred work is recorded in `ROADMAP.md`.

## Non-goals

- Untangling the `cmd/*` sibling imports.
- Unifying the chain glue of `branch`, `issue` and `review`.
- Upgrading Cobra.
- Touching `isDebug` / `appConfig` in `cmd/root.go` (a flag variable and a
  value set once at startup).
- `website/build/`: the review's claim is false, the directory is ignored and
  nothing under it is tracked.
- `review.md` itself stays untracked and untouched.

## Anti-goals

- Changing the signature of `commit.FillOutForm` or its two production callers
  (`cmd/commit/commit.go`, `cmd/mergeflow/huh_prompter.go`).
- Any behaviour change visible to a user.

## Approaches

**Tracker imports**

- *Move the three blank imports to `cmd/root.go`* — picked. Three lines, the
  root already imports every command package.
- A `tracker/all` package imported by the root — a package for three lines,
  with one importer. Rejected.
- `main.go` — tests that build the root command through `cmd.GetRootCmd` would
  not get the adapters. Rejected.

**`commit.runFormFn`**

- *Unexported `fillOutForm` taking the runner; exported `FillOutForm` wraps it
  with the real one* — picked. Production callers unchanged, global gone,
  tests inject per call.
- A `FormRunner` parameter on the exported `FillOutForm` (the review's
  proposal) — a seventh parameter that both production callers fill with the
  same value. Rejected.
- Leave it — the cost is two non-parallel test functions; cheap enough to fix.

**Cobra upgrade** — deferred. `cmdutil.StringFlag` and `pushflow.ReadFlags`
are also needed by the issue and branch tables, which run handlers with the
table's command, so upgrading fixes only the `RunMenu` path while
v1.1.3 → v1.10.2 can move help output, completions and the generated website
docs. The real fix is handlers that take typed arguments, which belongs to the
`cmd/*` untangling.

## Design

### 1. Tracker imports

- Remove the `forgejo`, `github`, `redmine` blank imports from
  `cmd/issue/issue.go`; add them, with their comments, to `cmd/root.go`.
- One test in `cmd/` that the adapters are registered once the package is
  loaded, one `t.Run` per adapter type.
- `cmd/root.go` covers whatever loads package `cmd`: the binary and the tests
  of `cmd`. A `cmd/<subcommand>` test binary does not import `cmd`, so it gets
  no real adapter at all any more (`cmd/branch` used to get them through
  `cmd/issue`). Today those tests use the fake tracker, which they import
  themselves; a test that needs a real adapter blank-imports it itself.

### 2. `commit` form runner

- Delete the package-level `runFormFn` variable in `commit/form.go`.
- Introduce a runner func type with the variable's current signature.
- `fillOutForm`, `runHistoryPicker` and `showNoHistoryDialog` take the runner
  as a parameter.
- `FillOutForm` keeps its signature and calls `fillOutForm` with the runner
  that wraps `tui.RunForm`.
- `commit/form_test.go`: drop `swapRunFormFn`, call `fillOutForm` with the
  stub, remove the "Not parallel" comments and add `t.Parallel()` where the
  global was the only reason against it.

### 3. `ROADMAP.md`

Append under `## Enhancement`:

> 13. Architecture cleanup (from the 2026-11-18 review): untangle the `cmd/*`
> sibling imports (shared actions move to flow packages, handlers take typed
> arguments instead of a `*cobra.Command`, then upgrade Cobra and drop
> `cmdutil.StringFlag`); unify the chain glue of `branch`, `issue` and
> `review`.

## Left to the builder

- The name of the runner func type.

The registration test builds each adapter with `tracker.New` on a dummy URL
and token and expects no error: the adapters' `New` validate and construct,
without a network call. `TestAssembleMessage`, the last serial test of
`commit/`, becomes parallel too.

## Verification

    mise exec -- go test ./commit/... ./cmd/... ./tracker/...
    mise exec -- go test -race ./commit/...
    mise exec -- go build -o ./bin/git-zf .

The design doc is not committed: the user commits themselves.
