# Review Health Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Register the tracker adapters at the composition root, remove the mutable `commit.runFormFn` global, and record the deferred architecture work in the roadmap.

**Architecture:** Two independent, behaviour-preserving moves. The blank imports go from `cmd/issue/issue.go` to `cmd/root.go`. `commit.FillOutForm` keeps its signature and delegates to an unexported `fillOutFormWith` that receives the form runner as a parameter.

**Tech Stack:** Go 1.25 via mise (`mise exec -- go …`), cobra v1.1.3 (not upgraded), huh.

**Spec:** `docs/superpowers/specs/2026-11-18-review-health-check-design.md`

## Global Constraints

- No user-visible behaviour change.
- `commit.FillOutForm`'s signature and its two production callers (`cmd/commit/commit.go:117`, `cmd/mergeflow/huh_prompter.go:67`) do not change.
- Do not upgrade Cobra; do not touch `isDebug` / `appConfig`, `cmdutil.StringFlag`, the `cmd/*` sibling imports or the chain glue.
- `review.md` stays untracked and untouched.
- Tests: every assertion or scenario in a named `t.Run` (user's global rule).
- Before editing a symbol, run GitNexus impact on it (`FillOutForm`, `runHistoryPicker`, `showNoHistoryDialog`) and report the risk (project rule).
- No commits: the user commits themselves.

## Review Focus

- A test in another `cmd/*` package that got a real adapter only because it imported `cmd/issue`: it must still pass, or import the adapter itself. `cmd/branch/prune_tracker_test.go` and `cmd/review/legacy_branch_e2e_test.go` mention real adapter types — covered by the full `./cmd/...` run in Task 1.
- `gitea` is registered by the `forgejo` package alongside `forgejo`: the registration test covers both names.
- ctrl+r with an empty history must still show the dialog and reopen the form with the current answers — an existing `TestFillOutForm` subtest covers it; it must keep passing with the injected runner.
- A runner error other than `huh.ErrUserAborted` in the no-history dialog stays swallowed — `TestFillOutForm` subtest "a runner error in the no-history dialog does not stop the flow", added after the final review (none existed).
- The real runner path (`FillOutForm` → `tui.RunForm`) has no automated test (it needs a terminal): a one-time TTY smoke of `./bin/git-zf commit` is left to the user.

---

### Task 1: Tracker adapters registered at the root + roadmap entry

**Files:**
- Modify: `cmd/issue/issue.go:6-8` (remove the three blank imports)
- Modify: `cmd/root.go` (add them, comments kept)
- Modify: `cmd/root_test.go` (new test)
- Modify: `ROADMAP.md`

- [ ] **Step 1: Write the test** in `cmd/root_test.go` (package `cmd`)

```go
func TestTrackerAdaptersRegistered(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"forgejo", "gitea", "github", "redmine"} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			// New validates and constructs: no network call.
			tr, err := tracker.New(config.IssueTrackerConfig{Type: typ, URL: "https://example.invalid", Token: "t"})
			if err != nil {
				t.Fatalf("tracker.New(%q): %v", typ, err)
			}
			if tr == nil {
				t.Fatalf("tracker.New(%q) returned a nil tracker", typ)
			}
		})
	}
}
```

Check the four names against the `trackerType` / `giteaType` constants of `tracker/forgejo`, `tracker/github`, `tracker/redmine` and fix the list if one differs.

- [ ] **Step 2: Run it**

Run: `mise exec -- go test ./cmd/ -run TestTrackerAdaptersRegistered -v`
Expected: PASS already (package `cmd` imports `cmd/issue`). The test pins the registration; it cannot fail first. To see it fail, temporarily delete the blank imports from `cmd/issue/issue.go` only: 4 subtests FAIL with "adapter not registered".

- [ ] **Step 3: Move the imports**

Delete the three `_ "github.com/piprim/git-zf/tracker/{forgejo,github,redmine}"` lines from `cmd/issue/issue.go`; add them with their comments to the import block of `cmd/root.go`.

- [ ] **Step 4: Append the roadmap entry** under `## Enhancement` in `ROADMAP.md`

```markdown
13. Architecture cleanup (from the 2026-11-18 review): untangle the `cmd/*` sibling imports (shared actions move to flow packages, handlers take typed arguments instead of a `*cobra.Command`, then upgrade Cobra and drop `cmdutil.StringFlag`); unify the chain glue of `branch`, `issue` and `review`.
```

- [ ] **Step 5: Verify**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/... ./tracker/...`
Expected: all PASS. `cmd/root.go` only covers what loads package `cmd`: a `cmd/<subcommand>` test binary now gets no real adapter (the three `tracker.New` calls in `cmd/issue` and `cmd/review` tests run on the fake, which they import). A failure with "adapter not registered" there means that test relied on the side effect: add the blank import of the adapter it needs to that test file.

---

### Task 2: Inject the form runner in `commit`

**Files:**
- Modify: `commit/form.go:40-225`
- Modify: `commit/form_test.go` (`swapRunFormFn` at 211-218, `TestFillOutForm` at 323, the test at ~838)

**Interfaces (all in package `commit`):**

```go
// runFormFunc runs a form; tui.RunForm in production, a stub in tests.
type runFormFunc func(form *huh.Form, entries []git.StatusEntry, classifyFn func() (bool, bool)) (formRunner, error)

func runTUIForm(form *huh.Form, entries []git.StatusEntry, classifyFn func() (bool, bool)) (formRunner, error) // wraps tui.RunForm

func fillOutForm(ctx context.Context, run runFormFunc, cfg *config.AppConfig, defaults tui.CommitOption,
	hs historyStore, initialPrefill map[string]any, entries []git.StatusEntry) ([]byte, tui.CommitOption, error)

func runHistoryPicker(ctx context.Context, run runFormFunc, tmplText string, hs historyStore) (map[string]any, error)

func showNoHistoryDialog(run runFormFunc)

// unchanged signature; body is: return fillOutForm(ctx, runTUIForm, cfg, defaults, hs, initialPrefill, entries)
func FillOutForm(ctx, cfg, defaults, hs, initialPrefill, entries) ([]byte, tui.CommitOption, error)
```

- [ ] **Step 1: Impact analysis**

Run GitNexus `impact` (upstream) on `FillOutForm`, `runHistoryPicker`, `showNoHistoryDialog`; report callers and risk. Expected callers of `FillOutForm`: the two production sites in Global Constraints, plus tests.

- [ ] **Step 2: Rewrite the tests to the new shape (they fail to compile first)**

In `commit/form_test.go`:
- delete `swapRunFormFn`;
- every `swapRunFormFn(t, fn)` + `FillOutForm(ctx, …)` pair becomes `fillOutForm(ctx, fn, …)` with the same stub `fn` and the same assertions (the stub closures and `stubFormRunner` stay as they are);
- any direct call to `runHistoryPicker` / `showNoHistoryDialog` gets the stub as its runner argument;
- remove the two "Not parallel — … global runFormFn" comments and add `t.Parallel()` to those two test functions and to their subtests, unless a subtest shares mutable state with a sibling (then leave that subtest serial and say why in a comment);
- add `t.Parallel()` to `TestAssembleMessage` (line 20) and inside its `for _, tt := range tests` `t.Run`: it is pure and the last serial test of `commit/`.

Run: `mise exec -- go test ./commit/...`
Expected: FAIL to compile, `undefined: fillOutForm`.

- [ ] **Step 3: Implement** in `commit/form.go`

Delete the `runFormFn` variable; add `runFormFunc` and `runTUIForm`; rename the body of `FillOutForm` to `fillOutForm` with the `run` parameter and thread `run` to `runHistoryPicker` and `showNoHistoryDialog`; `FillOutForm` becomes the one-line wrapper. Keep the existing doc comments on the function that now holds the behaviour; update the `formRunner` comment (no more package-level var).

- [ ] **Step 4: Verify**

Run: `mise exec -- go test -race ./commit/... && mise exec -- go test ./cmd/commit/... ./cmd/mergeflow/... && grep -rn runFormFn --include=*.go . ; mise exec -- go build -o ./bin/git-zf .`
Expected: tests PASS under `-race`, `grep` prints nothing, build succeeds.

---

### Final check

- [ ] `mise exec -- go test ./...` — all PASS.
- [ ] GitNexus `detect_changes({scope: "all"})` — only `cmd/root.go`, `cmd/issue/issue.go`, `cmd/root_test.go`, `commit/form.go`, `commit/form_test.go` (plus `ROADMAP.md` and the two docs) changed; not `partial` / `truncated`.
- [ ] Tell the user: uncommitted on the current branch; TTY smoke of `./bin/git-zf commit` (form, ctrl+r history) is theirs.
