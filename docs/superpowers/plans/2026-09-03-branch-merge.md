# `git zf branch merge` + shared `mergeflow` engine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the merge orchestration out of `issue close` into a reusable `cmd/mergeflow` engine, then build `git zf branch merge` on top of it (pick a non-issue source branch, merge it into the current branch, offer delete/push).

**Architecture:** `cmd/mergeflow.Run` owns the generic middle — dry-run → pick strategy → confirm → execute (Classic/Squash/Rebase) — parameterized by a `Prompter` and a `PrefillFunc` for the commit message. `issue close` keeps its issue-specific outer shell (review, base-resolve, children guard, store, tracker) and calls the engine; `branch merge` is a thin driver that resolves `(source=picked, target=HEAD)`, refuses issue-branch sources, and runs post-merge steps.

**Tech Stack:** Go (managed by mise — run all Go via `mise exec -- go …`), Cobra, `charmbracelet/huh`, go-git, SQLite store.

**Spec:** `docs/superpowers/specs/2026-08-31-branch-merge-design.md` (read it alongside this plan — the plan argues from it).

## Global Constraints

- **Run Go through mise:** `mise exec -- go test ./...`, `mise exec -- go build -o ./bin/git-zf .`. Never bare `go`.
- **Tests use `t.Run` per assertion/scenario** (global rule + repo convention): wrap each independent check in a named `t.Run("label", func(t *testing.T){ … })`. For E2E tests with shared expensive setup, run setup once then wrap each assertion block in `t.Run`.
- **IO injection:** never `fmt.Print*` to `os.Stdout` in flow code — write through `client.IO().Out` / `client.IO().Err` (or an injected `io.Writer`) so tests capture output.
- **Merge strategy values** (from `commit/form.go`): `commit.MergeStrategyRebase = "Rebase"`, `commit.MergeStrategySquash = "Squash"`, `commit.MergeStrategyClassic = "Classic"`.
- **`branch merge` must NEVER merge an issue branch as source** — refuse via `branch.Parse` and redirect to `issue close` (safety: preserves review incorporation, sub-task guard, tracker update).
- **Commit frequently:** each task ends with a commit. Branch off `master` first if still on it (session rule: don't commit to the default branch directly).

---

### Task 1: `cmd/mergeflow` engine (new package)

Create the reusable engine by moving `issue close`'s merge orchestration into a new package, generalized from feature/base to source/target. This task is **purely additive** — `close.go` is untouched here and keeps its own copies until Task 2 deletes them.

**Files:**
- Create: `cmd/mergeflow/mergeflow.go` — `Params`, `Prompter`, `PrefillFunc`, `Result`, `Run`, internal `run` struct, `errFastForwardDeferred`.
- Create: `cmd/mergeflow/strategies.go` — the three executors + `rebasePreflight` + `rebasePlan` + `mergeDryRun` + `composeAndCommit`, as methods on `*run`.
- Create: `cmd/mergeflow/mergeflow_test.go` — engine tests + `scriptedMergePrompter` + test rig.

**Interfaces:**
- Consumes (from existing packages): `git.Client` (`MergeDryRun`, `MergeSquash`, `MergeRebase`, `MergeNoFFNoCommit`, `FastForwardOnly`, `AbortMerge`, `ResetHard`, `ResolveRef`, `ResolveBranchRef`, `LocalOrRemoteRef`, `Remote`, `Fetch`, `Checkout`, `IsDirty`, `IsAncestor`, `Commit`, `IO`); `commit.MergeStrategy*`; `convert.CommitOptionsFromTUI`; `tui.CommitOption`; `plumbing.Hash`.
- Produces (used by Tasks 2 & 4):
  - `mergeflow.Params{Source string; Target string; SourceMaterialized bool}`
  - `mergeflow.Prompter` interface: `PickStrategy(ctx) (commit.MergeStrategy, error)`; `ConfirmMerge(ctx, source, target string, s commit.MergeStrategy) (bool, error)`; `ComposeMessage(ctx, prefill map[string]any) ([]byte, tui.CommitOption, error)`
  - `mergeflow.PrefillFunc func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any`
  - `mergeflow.Result{Strategy commit.MergeStrategy; Aborted bool; FastForwardDeferred bool}`
  - `func mergeflow.Run(ctx, client *git.Client, p Params, prompter Prompter, prefill PrefillFunc) (Result, error)`

- [ ] **Step 1: Write `mergeflow.go` — types + `Run` (no executors yet)**

```go
package mergeflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
)

// Params identify WHAT to merge. The engine advances Target; Source is merged in.
type Params struct {
	Source             string
	Target             string
	SourceMaterialized bool // widens abort-rollback (Source is disposable/reproducible)
}

// Prompter resolves the generic user-facing merge decisions. issue close's
// *huhPrompter already satisfies it; branch merge ships its own.
type Prompter interface {
	PickStrategy(ctx context.Context) (commit.MergeStrategy, error)
	ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (confirmed bool, err error)
	ComposeMessage(ctx context.Context, prefill map[string]any) (msg []byte, opts tui.CommitOption, err error)
}

// PrefillFunc builds the commit-message prefill for the resolved tips. close
// returns an issue-flavored map; branch merge returns a plain merge map.
type PrefillFunc func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any

// Result reports what happened so callers run their own post-steps.
type Result struct {
	Strategy            commit.MergeStrategy
	Aborted             bool // user declined at ConfirmMerge
	FastForwardDeferred bool // rebase committed on Source but post-FF of Target failed
}

// errFastForwardDeferred is internal; Run surfaces it as Result.FastForwardDeferred.
var errFastForwardDeferred = errors.New("commit created, fast-forward deferred")

type run struct {
	client       *git.Client
	source       string
	target       string
	materialized bool
	prompter     Prompter
	prefill      PrefillFunc
}

func Run(ctx context.Context, client *git.Client, p Params, prompter Prompter, prefill PrefillFunc) (Result, error) {
	r := &run{
		client: client, source: p.Source, target: p.Target,
		materialized: p.SourceMaterialized, prompter: prompter, prefill: prefill,
	}

	// The Target may not exist locally; LocalOrRemoteRef falls back to origin/<target>.
	dryRunBase := client.LocalOrRemoteRef(p.Target)
	conflicts, err := client.MergeDryRun(ctx, p.Source, dryRunBase)
	if err != nil {
		return Result{}, fmt.Errorf("merge dry-run: %w", err)
	}
	if len(conflicts) > 0 {
		fmt.Fprintln(client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(client.IO().Out, "  "+f)
		}
		fmt.Fprintln(client.IO().Out, "Aborting.")

		return Result{}, fmt.Errorf("merge conflicts in branch %q", p.Source)
	}

	strategy, err := prompter.PickStrategy(ctx)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // prompter already wraps
	}
	confirmed, err := prompter.ConfirmMerge(ctx, p.Source, p.Target, strategy)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // prompter already wraps
	}
	if !confirmed {
		return Result{Strategy: strategy, Aborted: true}, nil
	}

	switch strategy {
	case commit.MergeStrategyClassic:
		err = r.classic(ctx)
	case commit.MergeStrategySquash:
		err = r.squash(ctx)
	case commit.MergeStrategyRebase:
		err = r.rebase(ctx)
	default:
		return Result{Strategy: strategy}, fmt.Errorf("unknown strategy %q", strategy)
	}

	if errors.Is(err, errFastForwardDeferred) {
		return Result{Strategy: strategy, FastForwardDeferred: true}, nil
	}
	if err != nil {
		return Result{Strategy: strategy}, err
	}

	return Result{Strategy: strategy}, nil
}
```

- [ ] **Step 2: Write `strategies.go` by MOVING the executors from `close.go`, generalized**

Copy these functions from `cmd/issue/close.go` into `cmd/mergeflow/strategies.go` as methods on `*run`, applying the rename table verbatim. **Do not change any git-call sequencing** — only the identifiers below.

Rename table (apply to every moved body):

| In `close.go` (old) | In `strategies.go` (new) |
|---|---|
| `func doSquashCommit(ctx, mc mergeContext, prompter ClosePrompter) (err error)` | `func (r *run) squash(ctx context.Context) (err error)` |
| `func doClassicClose(ctx, mc, prompter) (err error)` | `func (r *run) classic(ctx context.Context) (err error)` |
| `func doRebaseClose(ctx, mc, prompter) (err error)` | `func (r *run) rebase(ctx context.Context) (err error)` |
| `func rebasePreflight(ctx, mc) (rebasePlan, error)` | `func (r *run) rebasePreflight(ctx context.Context) (rebasePlan, error)` |
| `func mergeDryRun(ctx, mc, remoteBase) error` | `func (r *run) mergeDryRun(ctx context.Context, remoteBase string) error` |
| `mc.client` | `r.client` |
| `mc.pickedBranch.BranchName` | `r.source` |
| `mc.baseBranch` | `r.target` |
| `mc.materialized` | `r.materialized` |
| `errFastForwardDeferred` | `errFastForwardDeferred` (now the mergeflow one) |
| `commitpkg.` / `commit.` | `commit.` |

Then replace **every** `composeAndCommit(ctx, mc, prompter, &mergeInfo)` call. Each old call built a `commitpkg.IssueCloseInfo{FromHash: X, ToHash: Y, Strategy: Z}`. In the new code, keep the same `X` (source tip) and `Y` (target tip) resolution, but feed them to the injected `PrefillFunc` and call the generalized `composeAndCommit`:

```go
// OLD (squash example):
//   mergeInfo := commitpkg.IssueCloseInfo{FromHash: branchHash, ToHash: baseHash, Strategy: commitpkg.MergeStrategySquash}
//   return composeAndCommit(ctx, mc, prompter, &mergeInfo)
// NEW:
prefill := r.prefill(commit.MergeStrategySquash, branchHash, baseHash)
return r.composeAndCommit(ctx, prefill, commit.MergeStrategySquash)
```

Apply the same substitution in `classic` (`plan.featureOrigSHA`, `baseSHA`, `MergeStrategyClassic`) and `rebase` (`plan.featureOrigSHA`, `baseOriginSHA`, `MergeStrategyRebase`).

Add the generalized `composeAndCommit` (replaces `close.go`'s, dropping the `IssueCloseInfo` arg — the prefill is now pre-built) and keep `rebasePlan` verbatim:

```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/convert"
)

type rebasePlan struct {
	featureOrigSHA plumbing.Hash
	remoteName     string
	remoteBase     string
}

func (r *run) composeAndCommit(ctx context.Context, prefill map[string]any, strategy commit.MergeStrategy) error {
	msg, opts, err := r.prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter already wraps
	}
	if err := r.client.Commit(ctx, msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("commit %s: %w", strategy, err)
	}

	return nil
}
```

> Reference bodies to move (current `close.go`): `squash`←`doSquashCommit`, `classic`←`doClassicClose`, `rebase`←`doRebaseClose`, `rebasePreflight`, `mergeDryRun`. They use only `r.client`/`r.source`/`r.target`/`r.materialized` after renaming — no store, no cfg, no issue fields.

- [ ] **Step 3: Write the engine test rig + scripted prompter in `mergeflow_test.go`**

```go
package mergeflow

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tui"
)

var _ Prompter = (*scriptedMergePrompter)(nil)

type scriptedMergePrompter struct {
	Strategy commit.MergeStrategy
	Confirm  bool
	Message  []byte
}

func (s *scriptedMergePrompter) PickStrategy(context.Context) (commit.MergeStrategy, error) {
	return s.Strategy, nil
}
func (s *scriptedMergePrompter) ConfirmMerge(context.Context, string, string, commit.MergeStrategy) (bool, error) {
	return s.Confirm, nil
}
func (s *scriptedMergePrompter) ComposeMessage(context.Context, map[string]any) ([]byte, tui.CommitOption, error) {
	return s.Message, tui.CommitOption{}, nil
}

// plainPrefill mirrors branch merge's prefill (subject-only).
func plainPrefill(commit.MergeStrategy, plumbingHashPlaceholder, plumbingHashPlaceholder2) map[string]any {
	return map[string]any{"subject": "chore: merge test"}
}

// engineRig: repo on master, branch "feature" one commit ahead of master.
type engineRig struct {
	dir    string
	client *git.Client
	stdout *bytes.Buffer
}

func newEngineRig(t *testing.T) *engineRig {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q", "-b", "master")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("base.txt", "base\n")
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")
	runGit("switch", "-c", "feature")
	write("feature.txt", "feature\n")
	runGit("add", "feature.txt")
	runGit("commit", "-m", "feat: feature work")
	runGit("switch", "master")

	stdout := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{}}
	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	return &engineRig{dir: dir, client: client, stdout: stdout}
}

// headSubject returns the subject line of HEAD on the given branch.
func (r *engineRig) headSubject(t *testing.T, branch string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", branch)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log %s: %v\n%s", branch, err, out)
	}
	return string(bytes.TrimSpace(out))
}
```

> Note: replace the `plumbingHashPlaceholder` types in `plainPrefill` with `plumbing.Hash` (import `github.com/go-git/go-git/v5/plumbing`); the signature is `func plainPrefill(commit.MergeStrategy, plumbing.Hash, plumbing.Hash) map[string]any`.

- [ ] **Step 4: Write the failing happy-path test (Squash)**

```go
func TestRun_Squash(t *testing.T) {
	rig := newEngineRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: squash feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master"}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("strategy recorded, not aborted", func(t *testing.T) {
		if res.Aborted || res.FastForwardDeferred || res.Strategy != commit.MergeStrategySquash {
			t.Fatalf("unexpected result: %+v", res)
		}
	})
	t.Run("squash commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: squash feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
}
```

- [ ] **Step 5: Run it, verify it fails to compile/pass**

Run: `mise exec -- go test ./cmd/mergeflow/ -run TestRun_Squash -v`
Expected: FAIL (executors not yet wired / build errors) until Steps 1-2 are complete, then PASS.

- [ ] **Step 6: Make Step 4 pass, then add the remaining engine tests**

Add these `Test*` functions in `mergeflow_test.go`, each with `t.Run` sub-assertions:

- `TestRun_Classic` — `Strategy: MergeStrategyClassic`, `Confirm: true`. Assert `master` HEAD is a merge commit whose subject is the scripted message, `res.Strategy == Classic`.
- `TestRun_Rebase` — `Strategy: MergeStrategyRebase`, `Confirm: true`. Assert `master` advanced to include feature's work (`git log master --oneline` contains "feat: feature work" or the rebased commit), `res.FastForwardDeferred == false`.
- `TestRun_UserDeclinesConfirm` — `Confirm: false`. Assert `res.Aborted == true`, `err == nil`, and `master` HEAD subject is still `"chore: init"` (no commit).
- `TestRun_ConflictAborts` — build a second rig where `feature` and `master` edit the same line of `base.txt` divergently (add a `writeConflict` helper: commit a change to `base.txt` on master after branching feature that also changed it). Assert `Run` returns a non-nil error, `res.Aborted == false`, and `master` HEAD unchanged. `PickStrategy` must NOT have been consulted (conflicts short-circuit before it) — assert via a counter field on the prompter if desired.
- `TestRun_MaterializedDiscardsResidueOnAbort` — Squash with `SourceMaterialized: true` and a `ComposeMessage` that returns an error (add a `MessageErr error` field to `scriptedMergePrompter`; return it from `ComposeMessage`). Assert `Run` returns the error and `git status --porcelain` in `rig.dir` is empty (staged squash residue discarded by the executor's `mc.materialized`→`r.materialized` rollback).

Run: `mise exec -- go test ./cmd/mergeflow/ -v` → all PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/mergeflow/
git commit -m "feat(mergeflow): extract reusable merge engine from issue close"
```

---

### Task 2: Rewire `issue close` onto the engine

Delete the now-duplicated orchestration from `close.go` and call `mergeflow.Run`. **Behaviour-preserving** — the existing close E2E suite is the gate and must stay green with only the mechanical edits below.

**Files:**
- Modify: `cmd/issue/close.go` — delete `mergeContext`, `doMerge`, `doSquashCommit`, `doClassicClose`, `doRebaseClose`, `rebasePreflight`, `mergeDryRun`, `composeAndCommit`, `rebasePlan`, `errFastForwardDeferred`; rewire the merge call site in `runClose`.
- Modify: `cmd/issue/close_prompter.go` — add a compile-time assertion that `*huhPrompter` satisfies `mergeflow.Prompter`.

**Interfaces:**
- Consumes: `mergeflow.Run`, `mergeflow.Params`, `mergeflow.Result` (Task 1).
- Produces: no new exported surface; `runClose` behaviour unchanged.

- [ ] **Step 1: Delete the moved symbols from `close.go`**

Remove these definitions (now living in `cmd/mergeflow`): `mergeContext` struct, `doMerge`, `doSquashCommit`, `doClassicClose`, `doRebaseClose`, `rebasePreflight`, `mergeDryRun`, `composeAndCommit`, `rebasePlan`, and the `errFastForwardDeferred` var. Keep everything else (`getPickedBranch`, `resolveDefaultBase`, `chooseMergeTarget`, `updateClosedStatus`, `doDeleteBranch`, `proposeClosePush`, `trackPickedCandidate`, review/children helpers).

- [ ] **Step 2: Rewire the merge call site in `runClose`**

Replace the current block (`mc := mergeContext{…}; strategy, aborted, err := doMerge(…)` … through the `if aborted` handling) with:

```go
prefill := func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any {
	return commit.IssueHint{
		IssueID:      picked.IssueSlug,
		BranchType:   picked.Type,
		IssueSubject: picked.Title,
		Closing:      &commit.IssueCloseInfo{FromHash: sourceTip, ToHash: targetTip, Strategy: s},
	}.Prefill(deps.cfg.CommitMessage)
}

res, err := mergeflow.Run(ctx, deps.client, mergeflow.Params{
	Source:             picked.BranchName,
	Target:             base,
	SourceMaterialized: createdBranch,
}, prompter, prefill)
if err != nil {
	return err
}

if res.FastForwardDeferred {
	// Commit landed on the feature branch; keep it and track the candidate.
	mergeCommitted = true
	picked = trackPickedCandidate(ctx, deps, picked)

	return nil
}

if res.Aborted {
	fmt.Fprintln(deps.client.IO().Out, "Aborted.")

	return nil
}

mergeCommitted = true
picked = trackPickedCandidate(ctx, deps, picked)

updateClosedStatus(ctx, deps, picked, prompter)

if err := doDeleteBranch(ctx, deps.client, picked, res.Strategy, prompter); err != nil {
	return err
}
```

Fix imports: add `"github.com/piprim/git-zf/cmd/mergeflow"` and `"github.com/go-git/go-git/v5/plumbing"`; remove `"github.com/piprim/git-zf/convert"` and the `errors` import **only if** now unused (grep first — `errors` is likely still used elsewhere; `convert` probably becomes unused). Collapse the duplicate `commit`/`commitpkg` imports to a single `commit` alias and update references (`commitpkg.` → `commit.`).

- [ ] **Step 3: Add the Prompter assertion in `close_prompter.go`**

```go
// The generic subset of ClosePrompter also drives the shared merge engine.
var _ mergeflow.Prompter = (*huhPrompter)(nil)
```

Add `"github.com/piprim/git-zf/cmd/mergeflow"` to the imports.

- [ ] **Step 4: Build + run the FULL close suite (the regression gate)**

Run:
```
mise exec -- go build ./...
mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v
```
Expected: PASS — identical behaviour to before the extraction. If any close E2E fails, the move altered sequencing; diff the moved bodies against the rename table and fix.

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_prompter.go
git commit -m "refactor(close): drive merges through the shared mergeflow engine"
```

---

### Task 3: `issue close` empty-state redirect

When `issue close` finds no candidates, point a user on a non-issue branch to `branch merge`.

**Files:**
- Modify: `cmd/issue/close.go` — the `len(branches) == 0` block in `getPickedBranch`.
- Test: `cmd/issue/close_e2e_test.go` — add one case.

**Interfaces:**
- Consumes: `git.Client.CurrentBranch`, `DefaultBaseBranch`, `IsMergedInto` (all existing).
- Produces: no new surface.

- [ ] **Step 1: Write the failing test**

In `cmd/issue/close_e2e_test.go`, using the existing close rig, add:

```go
func TestClose_EmptyState_RedirectsToBranchMerge(t *testing.T) {
	// Rig with an EMPTY store (no in-progress issues) and the repo checked out
	// on a non-issue branch that has a commit not on base. Reuse the close rig's
	// constructor; create branch "hotfix" ahead of master, leave HEAD on it.
	rig := newCloseRig(t) // existing helper
	rig.gitSwitchNewBranchAhead(t, "hotfix") // add helper: switch -c hotfix; commit a file

	err := runClose(t.Context(), rig.deps(), rig.prompter())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("suggests branch merge", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "git zf branch merge") {
			t.Fatalf("stdout missing redirect, got:\n%s", rig.stdout.String())
		}
	})
}
```

> If `newCloseRig`/`deps()`/`prompter()` helper names differ in the existing suite, adapt to the actual rig API — read the top of `close_e2e_test.go` first. Add a small `gitSwitchNewBranchAhead(t, name)` helper mirroring the existing branch-seeding helpers.

- [ ] **Step 2: Run it, verify it fails**

Run: `mise exec -- go test ./cmd/issue/ -run TestClose_EmptyState_RedirectsToBranchMerge -v`
Expected: FAIL — current code prints "No branches available to close." (no "branch merge").

- [ ] **Step 3: Implement the redirect**

In `getPickedBranch`, replace:

```go
if len(branches) == 0 {
	fmt.Fprintln(client.IO().Out, "No branches available to close.")

	return nil, nil
}
```

with:

```go
if len(branches) == 0 {
	if cur, err := client.CurrentBranch(); err == nil && cur != "" {
		if base, bErr := client.DefaultBaseBranch(); bErr == nil {
			if merged, mErr := client.IsMergedInto(cur, base); mErr == nil && !merged {
				fmt.Fprintf(client.IO().Out,
					"You're on %q, which has unmerged commits but isn't an issue branch.\n"+
						"To merge it:  git zf branch merge\n", cur)

				return nil, nil
			}
		}
	}
	fmt.Fprintln(client.IO().Out, "No branches available to close.")

	return nil, nil
}
```

> `IsMergedInto` and `DefaultBaseBranch` are on `git.Client` (used by `branch prune`). Any error path degrades to the plain message — never block.

- [ ] **Step 4: Run tests**

Run:
```
mise exec -- go test ./cmd/issue/ -run "TestClose_EmptyState_RedirectsToBranchMerge" -v
mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v
```
Expected: PASS (new test + all existing close tests).

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(close): point non-issue branches to branch merge on empty state"
```

---

### Task 4: `git zf branch merge` command

Build the command as a thin driver over `mergeflow`: enumerate local ∪ remote-only sources, refuse issue branches, materialize remote-only sources, run the engine, then delete-source / propose-push.

**Files:**
- Create: `cmd/branch/merge.go` — `runMerge`, `SourceBranch`, source enumeration, refusal, materialize, terminal dispatch, post-steps, `plainPrefill`.
- Create: `cmd/branch/merge_prompter.go` — `MergePrompter` interface + `huhMergePrompter`.
- Create: `cmd/branch/merge_prompter_test.go` — `scriptedMergePrompter` (package `branch`).
- Create: `cmd/branch/merge_e2e_test.go` — rig + tests.
- Modify: `cmd/branch/branch.go` — replace the `mergeCmd()` stub to wire `pushflow` flags + `mergeRunE` → `runMerge`; wire `tui.BranchActionNameMerge` in `runE` if the action menu lists merge (check `branch.go:runE`).

**Interfaces:**
- Consumes: `mergeflow.Run/Params/Result/Prompter/PrefillFunc` (Task 1); `git.Client` (`CurrentBranch`, `LocalBranchNames`, `RemoteBranchNames`, `RemoteBranchExists`, `DeleteLocalBranch`, `DeleteRemoteBranch`, `DeleteLocalBranchSafe`); `issueflow.MaterializeBranch`; `pushflow` (`AddFlags`, `ReadFlags`, `ResolveFlags`, `Propose`, `Opts`, `NewHuhConfirm`); `branchpkg.Parse`; `store` (`OpenRepo`); `tui.IssueMergeStrategy/IssueMergeConfirm/IssueDeleteBranch`; `commit.FillOutForm`.
- Produces: the `branch merge` command; no exported surface used by other tasks.

- [ ] **Step 1: Write `merge_prompter.go`**

```go
package branch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// SourceBranch is one pickable merge source. RemoteOnly marks an origin-only
// branch so the picker can label it and runMerge knows to materialize it.
type SourceBranch struct {
	Name       string
	RemoteOnly bool
}

// MergePrompter resolves every branch-merge decision. huhMergePrompter drives
// huh forms; merge_prompter_test.go provides a scripted implementation.
type MergePrompter interface {
	mergeflow.Prompter // PickStrategy, ConfirmMerge, ComposeMessage
	PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error)
	ConfirmDeleteSource(ctx context.Context, source string) (delete bool, err error)
}

var _ MergePrompter = (*huhMergePrompter)(nil)

type huhMergePrompter struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
}

func newHuhMergePrompter(c *git.Client, s *store.Store, cfg *config.AppConfig) *huhMergePrompter {
	return &huhMergePrompter{client: c, store: s, cfg: cfg}
}

func (p *huhMergePrompter) PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error) {
	opts := make([]huh.Option[string], 0, len(sources))
	byName := make(map[string]SourceBranch, len(sources))
	for _, s := range sources {
		label := s.Name
		if s.RemoteOnly {
			label = s.Name + " (origin)"
		}
		opts = append(opts, huh.NewOption(label, s.Name))
		byName[s.Name] = s
	}

	var picked string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Branch to merge into current").
			Options(opts...).
			Value(&picked),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return SourceBranch{}, fmt.Errorf("source picker: %w", err)
	}

	return byName[picked], nil
}

func (p *huhMergePrompter) PickStrategy(ctx context.Context) (commit.MergeStrategy, error) {
	var picked string
	form := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{Value: string(commit.MergeStrategyRebase), Label: "Rebase", Hint: "Single clean commit on current, submodule-safe (recommended)"},
		{Value: string(commit.MergeStrategySquash), Label: "Squash", Hint: "git merge --squash — fast, but not submodule-safe"},
		{Value: string(commit.MergeStrategyClassic), Label: "Classic", Hint: "git merge --no-ff with commitizen message — preserves full history"},
	})
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("strategy picker: %w", err)
	}

	return commit.MergeStrategy(picked), nil
}

func (p *huhMergePrompter) ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(source, target, string(s), &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return confirmed, nil
}

func (p *huhMergePrompter) ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	authors, err := p.client.Authors(ctx)
	if err != nil {
		slog.Warn("could not load author list", "error", err)
		authors = []string{}
	}
	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}
	msg, opts, err := commit.FillOutForm(ctx, p.cfg, defaults, p.store, prefill, nil)
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("fill commit form: %w", err)
	}

	return msg, opts, nil
}

func (p *huhMergePrompter) ConfirmDeleteSource(ctx context.Context, source string) (bool, error) {
	var del bool
	if err := huh.NewForm(tui.IssueDeleteBranch(source, &del)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("delete branch form: %w", err)
	}

	return del, nil
}
```

- [ ] **Step 2: Write the scripted prompter `merge_prompter_test.go`**

```go
package branch

import (
	"context"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/tui"
)

var _ MergePrompter = (*scriptedMergePrompter)(nil)

type scriptedMergePrompter struct {
	Source        SourceBranch
	Strategy      commit.MergeStrategy
	Confirm       bool
	Message       []byte
	DeleteSource  bool

	PickSourceCalls   int
	ConfirmDeleteCalls int
}

func (s *scriptedMergePrompter) PickSource(_ context.Context, _ []SourceBranch) (SourceBranch, error) {
	s.PickSourceCalls++
	return s.Source, nil
}
func (s *scriptedMergePrompter) PickStrategy(context.Context) (commit.MergeStrategy, error) {
	return s.Strategy, nil
}
func (s *scriptedMergePrompter) ConfirmMerge(context.Context, string, string, commit.MergeStrategy) (bool, error) {
	return s.Confirm, nil
}
func (s *scriptedMergePrompter) ComposeMessage(context.Context, map[string]any) ([]byte, tui.CommitOption, error) {
	return s.Message, tui.CommitOption{}, nil
}
func (s *scriptedMergePrompter) ConfirmDeleteSource(context.Context, string) (bool, error) {
	s.ConfirmDeleteCalls++
	return s.DeleteSource, nil
}
```

- [ ] **Step 3: Write `merge.go` — `runMerge` + orchestration**

```go
package branch

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	branchpkg "github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

// mergeDeps bundles what runMerge needs; the E2E rig builds it directly.
type mergeDeps struct {
	client      *git.Client
	store       *store.Store
	cfg         *appConfigT // = *config.AppConfig; use the real type in code
	push, noPush bool
	pushConfirm pushflow.ConfirmFunc
}

func runMerge(ctx context.Context, d mergeDeps, prompter MergePrompter) (err error) {
	target, err := d.client.CurrentBranch()
	if err != nil || target == "" {
		return fmt.Errorf("checkout a branch before merging into it: %w", err)
	}

	sources, err := collectSources(ctx, d.client, target)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Fprintln(d.client.IO().Out, "No other branches to merge.")

		return nil
	}

	source, err := prompter.PickSource(ctx, sources)
	if err != nil {
		return err //nolint:wrapcheck // prompter already wraps
	}

	// SAFETY: refuse issue-branch sources — merging one here would bypass review
	// incorporation, the sub-task guard, and the tracker update that issue close runs.
	if parsed, perr := branchpkg.Parse(source.Name); perr == nil {
		fmt.Fprintf(d.client.IO().Out,
			"%q is an issue branch (%s). Use \"git zf issue close\" to merge it safely —\n"+
				"that runs review incorporation, the sub-task guard, and the tracker update.\n",
			source.Name, parsed.IssueID())

		return nil
	}

	mergeCommitted := false
	created := false
	if source.RemoteOnly {
		created, err = issueflow.MaterializeBranch(ctx, d.client, store.BranchRow{BranchName: source.Name})
		if err != nil {
			return err
		}
	}
	defer func() {
		if !created || mergeCommitted {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		if delErr := d.client.DeleteLocalBranchSafe(cleanupCtx, source.Name, true, d.cfg.Branch.Base); delErr != nil {
			fmt.Fprintf(d.client.IO().Err, "warning: rollback materialized branch %q: %v\n", source.Name, delErr)
		}
	}()

	prefill := func(_ commit.MergeStrategy, _, _ plumbing.Hash) map[string]any {
		return map[string]any{"subject": fmt.Sprintf("Merge %q into %q", source.Name, target)}
	}

	res, err := mergeflow.Run(ctx, d.client, mergeflow.Params{
		Source: source.Name, Target: target, SourceMaterialized: created,
	}, prompter, prefill)

	if err != nil {
		return err
	}
	if res.FastForwardDeferred {
		mergeCommitted = true // keep the materialized branch: rebased commits live only on it
		fmt.Fprintf(d.client.IO().Out,
			"Commit landed on %q; fast-forward %q into it manually.\n", source.Name, target)

		return nil
	}
	if res.Aborted {
		fmt.Fprintln(d.client.IO().Out, "Aborted.")

		return nil
	}
	mergeCommitted = true

	// Post-merge: delete source (local + remote), then propose push of target.
	if del, derr := prompter.ConfirmDeleteSource(ctx, source.Name); derr != nil {
		return derr
	} else if del {
		force := res.Strategy == commit.MergeStrategySquash || res.Strategy == commit.MergeStrategyRebase
		if delErr := d.client.DeleteLocalBranch(ctx, source.Name, force); delErr != nil {
			fmt.Fprintf(d.client.IO().Err, "warning: delete branch: %v\n", delErr)
		}
		if d.client.RemoteBranchExists(ctx, source.Name) {
			if rErr := d.client.DeleteRemoteBranch(ctx, source.Name); rErr != nil {
				fmt.Fprintf(d.client.IO().Err, "warning: delete remote branch: %v\n", rErr)
			}
		}
	}

	if d.pushConfirm != nil {
		skip, auto, rerr := pushflow.ResolveFlags(d.push, d.noPush, d.cfg.Push.Propose)
		if rerr != nil {
			return rerr
		}
		if perr := pushflow.Propose(ctx, d.client, pushflow.Opts{Branch: target, Skip: skip, AutoConfirm: auto}, d.pushConfirm); perr != nil {
			return perr
		}
	}

	fmt.Fprintf(d.client.IO().Out, "Branch %q merged into %q.\n", source.Name, target)

	return nil
}

// collectSources returns local ∪ remote-only branches minus target, remote-only flagged.
func collectSources(_ context.Context, c *git.Client, target string) ([]SourceBranch, error) {
	locals, err := c.LocalBranchNames()
	if err != nil {
		return nil, fmt.Errorf("list local branches: %w", err)
	}
	localSet := make(map[string]bool, len(locals))
	var out []SourceBranch
	for _, n := range locals {
		if n == target {
			continue
		}
		localSet[n] = true
		out = append(out, SourceBranch{Name: n})
	}

	remotes, err := c.RemoteBranchNames()
	if err != nil {
		return out, nil //nolint:nilerr // best-effort: degrade to local-only
	}
	for _, n := range remotes {
		if n == target || localSet[n] {
			continue
		}
		out = append(out, SourceBranch{Name: n, RemoteOnly: true})
	}

	return out, nil
}
```

> Replace the `appConfigT` placeholder with the real type: `mergeDeps.cfg` is `*config.AppConfig` (import `github.com/piprim/git-zf/config`); confirmed fields `cfg.Branch.Base` (`config/config.go:60`) and `cfg.Push.Propose` (`config/config.go:70`). `branchpkg.Parse` returns `*branch.Branch`; its fields are unexported, so read the slug via the accessor `parsed.IssueID()` (`branch/branch.go:75`) — do NOT reach for a struct field (`cmd/branch` is a separate package from the domain `branch` package).

- [ ] **Step 4: Wire the command in `branch.go`**

Replace the stub `mergeCmd()` / `mergeRunE`:

```go
func (b Branch) mergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge a branch into the current branch",
		Long:  "Pick a local or remote branch and merge it into the current branch (rebase, squash, or classic), then optionally delete it and push.",
		RunE:  b.mergeRunE,
	}
	pushflow.AddFlags(cmd)

	return cmd
}

func (b Branch) mergeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
	if err != nil {
		return err
	}

	push, noPush := pushflow.ReadFlags(cmd)
	d := mergeDeps{
		client: c, store: s, cfg: b.appConfig,
		push: push, noPush: noPush, pushConfirm: pushflow.NewHuhConfirm(),
	}

	return runMerge(ctx, d, newHuhMergePrompter(c, s, b.appConfig))
}
```

Update `GetRootCmd`'s `AddCommand(… mergeCmd())` → `b.mergeCmd()` (now a method). If `runE`'s action switch has a `tui.BranchActionNameMerge` case printing "Not yet implemented.", change it to call `b.mergeRunE(cmd, nil)` (check whether such an action constant exists; if not, leave `runE` as-is).

- [ ] **Step 5: Write the E2E rig + tests `merge_e2e_test.go`**

Mirror `prune_e2e_test.go`'s rig (real repo on `master`, injected IO buffers, `store.Open`). Add helpers: `switchNewBranchAhead(name)` (switch -c name; commit a unique file; switch back to master), and a bare-remote helper `addOriginWithBranch(name)` for the remote-only cases (`git init --bare` in a temp dir, `git remote add origin`, push a branch, then delete it locally so it's origin-only). Build `mergeDeps` directly with `pushConfirm` a stub that records/answers.

Tests (each with `t.Run` sub-assertions):

```go
func TestRunMerge_HappyPath_Squash(t *testing.T) {
	rig := newMergeRig(t)
	rig.switchNewBranchAhead(t, "feature") // leaves HEAD back on master
	p := &scriptedMergePrompter{
		Source:   SourceBranch{Name: "feature"},
		Strategy: commit.MergeStrategySquash,
		Confirm:  true,
		Message:  []byte("chore: merge feature\n"),
		DeleteSource: false,
	}

	err := runMerge(t.Context(), rig.deps(), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil { t.Fatalf("runMerge: %v", err) }
	})
	t.Run("commit landed on master", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge feature" {
			t.Fatalf("master HEAD = %q", got)
		}
	})
	t.Run("source kept (delete declined)", func(t *testing.T) {
		if !rig.branchExists(t, "feature") {
			t.Fatal("feature branch was deleted despite decline")
		}
	})
}
```

Add the remaining functions with concrete inputs/asserts:

- `TestRunMerge_HappyPath_Classic` / `_Rebase` — same shape, other strategies; assert master advanced.
- `TestRunMerge_DeletesSource` — `DeleteSource: true`; assert `feature` local branch gone; `ConfirmDeleteCalls == 1`.
- `TestRunMerge_KeepsSourceWhenDeclined` — covered above (fold or keep separate).
- `TestRunMerge_DeletesRemoteSourceBranch` — origin-only-and-local source with an origin ref; `DeleteSource: true`; assert both local gone and `RemoteBranchExists("feature") == false` after.
- `TestRunMerge_RefusesIssueBranchSource` — `Source: SourceBranch{Name: "1149829@feat@big"}` (a `branch.Parse`-able name; create it locally ahead of master). Assert: stdout contains `git zf issue close`; `master` HEAD unchanged (no merge); `PickStrategy`/materialize never reached. Add a second sub-test with the same name as **remote-only** (`RemoteOnly: true`) and assert no local branch was materialized (`branchExists("1149829@feat@big") == false`).
- `TestRunMerge_RemoteOnlySource_Materializes` — non-issue name (`spike`) present only on origin; `Confirm: true`. Assert it merges into master and a local `spike` now exists (materialized).
- `TestRunMerge_RemoteOnlySource_AbortRollsBackMaterialized` — remote-only `spike`, `Confirm: false`. Assert `res` aborted path printed "Aborted." and NO local `spike` remains (rollback fired).
- `TestRunMerge_RemoteOnlyRebase_FFDeferred_KeepsMaterialized` — construct a state where the post-rebase FF of `master` fails (e.g. `master` has an extra commit not on origin so the executor's final `FastForwardOnly` can't fast-forward). `Strategy: Rebase`, remote-only `spike`, `Confirm: true`. Assert: stdout contains "fast-forward"; local `spike` STILL exists (materialized branch survived); `master` HEAD is the pre-merge commit. *(If reliably provoking FF-deferred at the command layer proves brittle, assert this at the engine layer via `TestRun_FastForwardDeferred` in Task 1 and keep a lighter command-layer check that a materialized non-FF-deferred success deletes nothing unexpectedly.)*
- `TestRunMerge_DetachedHead_Errors` — detach HEAD (`git checkout --detach`); assert `runMerge` returns an error mentioning "checkout a branch".
- `TestRunMerge_NoOtherBranches` — only `master`; assert stdout "No other branches to merge." and no error.
- `TestRunMerge_AbortAtConfirm` — `Confirm: false` on a normal local source; assert "Aborted.", master unchanged.

- [ ] **Step 6: Run the branch-merge suite + full build**

Run:
```
mise exec -- go build ./...
mise exec -- go test ./cmd/branch/... -run "^TestRunMerge_" -v
mise exec -- go test ./...
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/branch/merge.go cmd/branch/merge_prompter.go cmd/branch/merge_prompter_test.go cmd/branch/merge_e2e_test.go cmd/branch/branch.go
git commit -m "feat(branch): implement branch merge over the shared mergeflow engine"
```

---

### Task 5: Documentation

**Files:**
- Modify: `CLAUDE.md` — add a "Testing the merge flow" subsection.
- Modify: `README.md` — describe the `branch merge` behaviour.
- Modify: `ROADMAP.md` — strike the "Open: `git zf branch merge`" section.

- [ ] **Step 1: `CLAUDE.md` — add after the prune-flow testing section**

```markdown
### Testing the merge flow

The shared merge engine is unit-tested in `cmd/mergeflow/mergeflow_test.go`
(real on-disk repo, scripted `mergeflow.Prompter`, one path per strategy plus
conflict/abort/FF-deferred). The `branch merge` command is end-to-end tested in
`cmd/branch/merge_e2e_test.go` with a `scriptedMergePrompter`.

    mise exec -- go test ./cmd/mergeflow/... -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunMerge_" -v

`branch merge` REFUSES issue branches as the source (redirects to `issue close`);
when changing the refusal or the shared engine, keep the close E2E suite green —
it is the regression net for the extraction.
```

- [ ] **Step 2: `README.md` — expand the `branch merge` line**

Near the existing `git zf branch merge` usage line, add: "Pick a local or remote-only branch and merge it into the current branch (rebase/squash/classic), then optionally delete the source (local + remote) and push. Issue branches are refused — use `git zf issue close` for those."

- [ ] **Step 3: `ROADMAP.md` — strike the Open section**

Remove the `### Open: git zf branch merge` section (lines ~28-30), or convert it to a struck/shipped note consistent with how other shipped items are marked in this file.

- [ ] **Step 4: Verify build + full suite once more, then commit**

Run: `mise exec -- go build ./... && mise exec -- go test ./...`

```bash
git add CLAUDE.md README.md ROADMAP.md
git commit -m "docs: document branch merge and the mergeflow engine"
```

---

## Self-Review

**1. Spec coverage:**
- `mergeflow` engine (surface + moved executors + subtleties) → Task 1. ✓
- `runClose` rewiring (prefill closure, FF-deferred→flag, materialized) → Task 2. ✓
- Empty-state redirect → Task 3. ✓
- `branch merge` command, source enumeration (local ∪ remote-only), issue-branch refusal, materialize + rollback, terminal dispatch (FF-deferred keeps materialized), delete-source (local+remote), propose-push → Task 4. ✓
- `MergePrompter` / `SourceBranch` / huh + scripted prompters → Task 4. ✓
- Testing (engine + command + close-suite-as-net + redirect) → Tasks 1,3,4. ✓
- Docs (CLAUDE.md/README/ROADMAP) → Task 5. ✓
- Spec "resolved: update-store-if-tracked dropped" → honoured (no store-update step in Task 4). ✓

**2. Placeholder scan:** One intentional call-out remains with its resolution inlined (not a TBD): `plainPrefill`'s `plumbing.Hash` param types (Task 1 Step 3 note). `mergeDeps.cfg` is resolved to `*config.AppConfig` with verified field paths; the parsed-slug accessor is resolved to `parsed.IssueID()` (`branch/branch.go:75`). No "TODO/implement later/add error handling" placeholders.

**3. Type consistency:** `mergeflow.Prompter` signatures are identical across Task 1 (definition), Task 2 (`var _ mergeflow.Prompter = (*huhPrompter)(nil)`), and Task 4 (`MergePrompter` embeds it; `huhMergePrompter`/`scriptedMergePrompter` implement it). `Result{Strategy, Aborted, FastForwardDeferred}` fields are read consistently in Tasks 2 and 4. `SourceBranch{Name, RemoteOnly}` defined and used consistently in Task 4. `PrefillFunc` signature `(commit.MergeStrategy, plumbing.Hash, plumbing.Hash) map[string]any` matches close's closure (Task 2) and `plainPrefill` (Tasks 1 & 4).
