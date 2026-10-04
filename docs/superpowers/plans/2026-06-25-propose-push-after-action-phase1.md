# Propose-to-push (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After `issue close`, `commit`, and `review request/approve/reject` succeed, offer to push the relevant branch to the remote — show a porcelain dry-run preview, then push on confirm (default Yes).

**Architecture:** Two thin `*git.Client` helpers (`PushDryRun`, `PushBranch`) plus a shared `cmd/pushflow` package (`Propose` + `ConfirmFunc` + flag helpers) called from the five command sites. A config master switch (`push.propose`, default true) and `--push`/`--no-push` flags gate it. Phase 1 is purely additive: the merge-vs-parent preview and the `resolveDefaultBase` refactor are deferred to Phase 2.

**Tech Stack:** Go (managed by mise), cobra, viper, `charmbracelet/huh`, go-git v6. Spec: `docs/superpowers/specs/2026-06-25-propose-push-after-action-design.md`.

## Global Constraints

- Run Go via mise: `mise exec -- go test ./...`, `mise exec -- go build ./...`, `mise exec -- go vet ./...`.
- **Tests use `t.Run` for every distinct assertion or scenario** (project + user convention).
- golangci-lint cannot run locally here (v2 config vs v1 binary) — verify with `go vet` / `go build` / `go test`; treat lint as CI-only.
- **The user performs all git commits.** Do NOT run `git add`/`git commit`. Each task ends with a verification step and a "ready for the user to commit" hand-off listing the files + a suggested message.
- Config is **TOML** (`config/default.toml`, embedded), not JSON.
- Never `--force` push. No-remote, nothing-to-push, and unreachable-remote all resolve to "skip silently" (never fail the already-completed action).
- Phase 1 must not change existing close/commit/review logic — only append a guarded final step and additive fields/flags.

---

### Task 1: `git` push helpers (`PushDryRun`, `PushBranch`, `PushOutcome`)

**Files:**
- Create: `git/push.go`
- Test: `git/push_test.go`

**Interfaces:**
- Consumes: existing `*git.Client` helpers `Remote()`, `WorkingTreeRoot()`, `runInteractive(ctx, dir, args...)`.
- Produces:
  - `type PushKind int` with `PushUpToDate, PushNewBranch, PushFastForward, PushRejected`
  - `type PushOutcome struct { Kind PushKind; Summary string }`
  - `func (c *Client) PushDryRun(ctx context.Context, branch string) (PushOutcome, bool, error)` — second return is `ok`: false ⇒ caller skips (no remote / up to date / unreachable).
  - `func (c *Client) PushBranch(ctx context.Context, branch string) error`

- [ ] **Step 1: Write the failing tests** in `git/push_test.go`

```go
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPushDryRun(t *testing.T) {
	t.Parallel()

	t.Run("no remote → ok=false, no error", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t) // no remote configured
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if ok {
			t.Fatalf("ok = true, want false (no remote)")
		}
		_ = out
	})

	t.Run("up-to-date main → Kind UpToDate, ok=false", func(t *testing.T) {
		t.Parallel()
		c, _, _ := newDiskRepoWithOrigin(t)
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if ok || out.Kind != PushUpToDate {
			t.Fatalf("got kind=%v ok=%v, want UpToDate/false", out.Kind, ok)
		}
	})

	t.Run("new local branch → Kind NewBranch, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, _ := newDiskRepoWithOrigin(t)
		mustGit(t, cloneDir, "checkout", "-b", "feature-x")
		out, ok, err := c.PushDryRun(t.Context(), "feature-x")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushNewBranch {
			t.Fatalf("got kind=%v ok=%v, want NewBranch/true", out.Kind, ok)
		}
	})

	t.Run("local ahead of origin → Kind FastForward, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, _ := newDiskRepoWithOrigin(t)
		if err := os.WriteFile(filepath.Join(cloneDir, "ahead.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "ahead.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: ahead")
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushFastForward {
			t.Fatalf("got kind=%v ok=%v, want FastForward/true", out.Kind, ok)
		}
	})

	t.Run("diverged from origin → Kind Rejected, ok=true", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, originDir := newDiskRepoWithOrigin(t)
		// Advance origin/main from a second clone so our clone diverges.
		other := t.TempDir()
		mustGit(t, filepath.Dir(other), "clone", originDir, filepath.Base(other))
		mustGit(t, other, "config", "user.email", "o@o.test")
		mustGit(t, other, "config", "user.name", "Other")
		mustGit(t, other, "config", "commit.gpgsign", "false")
		if err := os.WriteFile(filepath.Join(other, "remote.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, other, "add", "remote.go")
		mustGit(t, other, "commit", "-m", "feat: remote-side")
		mustGit(t, other, "push", "origin", "main")
		// Our clone makes a different commit without fetching → diverged.
		if err := os.WriteFile(filepath.Join(cloneDir, "local.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "local.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: local-side")
		out, ok, err := c.PushDryRun(t.Context(), "main")
		if err != nil {
			t.Fatalf("PushDryRun: %v", err)
		}
		if !ok || out.Kind != PushRejected {
			t.Fatalf("got kind=%v ok=%v, want Rejected/true", out.Kind, ok)
		}
	})
}

func TestPushBranch(t *testing.T) {
	t.Parallel()

	t.Run("advances the branch on origin", func(t *testing.T) {
		t.Parallel()
		c, cloneDir, originDir := newDiskRepoWithOrigin(t)
		mustGit(t, cloneDir, "checkout", "-b", "feature-y")
		if err := os.WriteFile(filepath.Join(cloneDir, "y.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		mustGit(t, cloneDir, "add", "y.go")
		mustGit(t, cloneDir, "commit", "-m", "feat: y")
		if err := c.PushBranch(t.Context(), "feature-y"); err != nil {
			t.Fatalf("PushBranch: %v", err)
		}
		// origin now has refs/heads/feature-y.
		got := exec.Command("git", "-C", originDir, "rev-parse", "refs/heads/feature-y")
		if out, err := got.CombinedOutput(); err != nil {
			t.Fatalf("origin missing feature-y: %v\n%s", err, out)
		}
	})

	t.Run("no remote → no-op, nil error", func(t *testing.T) {
		t.Parallel()
		c, _ := newDiskRepo(t)
		if err := c.PushBranch(t.Context(), "main"); err != nil {
			t.Fatalf("PushBranch with no remote: %v", err)
		}
	})
}

// mustGit runs a git command in dir and fails the test on error.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(t.Context(), "git", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./git/ -run 'TestPushDryRun|TestPushBranch' -v`
Expected: FAIL — `c.PushDryRun`/`c.PushBranch` undefined.

- [ ] **Step 3: Implement `git/push.go`**

```go
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PushKind classifies what a dry-run push to the remote would do for a branch.
type PushKind int

const (
	// PushUpToDate means the remote already has the branch tip (nothing to push).
	PushUpToDate PushKind = iota
	// PushNewBranch means the branch does not yet exist on the remote.
	PushNewBranch
	// PushFastForward means the remote branch would advance by fast-forward.
	PushFastForward
	// PushRejected means the push would be rejected (non-fast-forward divergence).
	PushRejected
)

// PushOutcome is the parsed result of a dry-run push for a single branch.
type PushOutcome struct {
	Kind    PushKind
	Summary string // friendly line for the preview, e.g. "abc1234..def5678" or "[new branch]"
}

// PushDryRun runs `git push --porcelain --dry-run <remote> <branch>:<branch>`
// (under LC_ALL=C) and parses the per-ref porcelain flag char into a PushOutcome.
// The remote is contacted for ref negotiation but no objects transfer, so the
// result reflects the true remote state. ok is false (caller skips the proposal)
// when no remote is configured, when the branch is up to date, or when the
// dry-run output cannot be parsed. A real dry-run command failure (e.g. remote
// unreachable) returns a wrapped error with ok=false.
func (c *Client) PushDryRun(ctx context.Context, branch string) (PushOutcome, bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return PushOutcome{}, false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return PushOutcome{}, false, nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return PushOutcome{}, false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"push", "--porcelain", "--dry-run", remote, branch+":"+branch)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, runErr := cmd.CombinedOutput()

	// Parse regardless of exit code: a rejected (non-fast-forward) push exits 1
	// but still emits a porcelain status line we want to surface.
	outcome, parsed := parsePushPorcelain(string(out))
	if !parsed {
		if runErr != nil {
			return PushOutcome{}, false, fmt.Errorf("push --dry-run %s: %w: %s", branch, runErr, out)
		}
		return PushOutcome{}, false, nil
	}
	if outcome.Kind == PushUpToDate {
		return outcome, false, nil
	}
	return outcome, true, nil
}

// parsePushPorcelain reads the first ref status line of `git push --porcelain`
// output. The leading flag char is the contract: '=' up to date, ' ' (or empty)
// fast-forward, '*' new ref, '!' rejected, '+' forced. Header ("To ...") and
// trailer ("Done") lines are ignored. Returns parsed=false when no ref line is
// found.
func parsePushPorcelain(out string) (PushOutcome, bool) {
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, "To ") || strings.HasPrefix(line, "Done") {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		flag := fields[0]
		summary := ""
		if len(fields) == 3 {
			summary = fields[2]
		}
		switch flag {
		case "=":
			return PushOutcome{Kind: PushUpToDate, Summary: "up to date"}, true
		case "*":
			return PushOutcome{Kind: PushNewBranch, Summary: "[new branch]"}, true
		case "!":
			return PushOutcome{Kind: PushRejected, Summary: "rejected (non-fast-forward)"}, true
		case "", " ", "+":
			if summary == "" {
				summary = "fast-forward"
			}
			return PushOutcome{Kind: PushFastForward, Summary: summary}, true
		}
	}
	return PushOutcome{}, false
}

// PushBranch runs `git push <remote> <branch>:<branch>` through the interactive
// IO streams (live progress). Never uses --force. No-op when no remote.
func (c *Client) PushBranch(ctx context.Context, branch string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "push", remote, branch+":"+branch); err != nil {
		return fmt.Errorf("push %s: %w", branch, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./git/ -run 'TestPushDryRun|TestPushBranch' -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Verify build + vet, then hand off for commit**

Run: `mise exec -- go build ./... && mise exec -- go vet ./git/`
Expected: no output (success).
Ready for the user to commit — files: `git/push.go`, `git/push_test.go`. Suggested message: `feat(git): add PushDryRun/PushBranch helpers`.

---

### Task 2: config master switch `push.propose`

**Files:**
- Modify: `config/config.go` (add `PushConfig`, `AppConfig.Push`, Load overlay)
- Modify: `config/default.toml` (add `[push] propose = true`)
- Test: `config/config_test.go` (add subtests)

**Interfaces:**
- Produces: `config.PushConfig{ Propose bool }`, field `AppConfig.Push PushConfig`. Default `Propose == true`.

- [ ] **Step 1: Write the failing tests** — append to `config/config_test.go`

```go
func TestLoadPushPropose(t *testing.T) {
	t.Parallel()

	t.Run("defaults to true", func(t *testing.T) {
		t.Parallel()
		cfg, err := Load(viper.New())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Push.Propose {
			t.Fatalf("Push.Propose = false, want true (default)")
		}
	})

	t.Run("can be overridden to false", func(t *testing.T) {
		t.Parallel()
		v := viper.New()
		v.Set("push.propose", false)
		cfg, err := Load(v)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Push.Propose {
			t.Fatalf("Push.Propose = true, want false (override)")
		}
	})
}
```

If `config/config_test.go` does not already import `github.com/spf13/viper`, add it to the import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./config/ -run TestLoadPushPropose -v`
Expected: FAIL — `cfg.Push` undefined.

- [ ] **Step 3: Add the config type + field + Load overlay** in `config/config.go`

After the `BranchConfig` type block, add:

```go
// PushConfig holds settings for the post-action push proposal.
// Propose is the master switch (default true); when false the propose-to-push
// step is skipped everywhere.
type PushConfig struct {
	Propose bool `json:"propose" toml:"propose" mapstructure:"propose"`
}
```

In `AppConfig`, add the field after `Branch`:

```go
	Branch        BranchConfig        `json:"branch"         toml:"branch"         mapstructure:"branch"`
	Push          PushConfig          `json:"push"           toml:"push"           mapstructure:"push"`
```

In `Load`, after the `branch.worktree-dir` block and before the `issue-tracker` block, add:

```go
	if v.IsSet("push.propose") {
		cfg.Push.Propose = v.GetBool("push.propose")
	}
```

- [ ] **Step 4: Set the default** — append to `config/default.toml`

```toml

[push]
propose = true
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./config/ -run TestLoadPushPropose -v`
Expected: PASS.

- [ ] **Step 6: Full config suite + build, then hand off**

Run: `mise exec -- go test ./config/ && mise exec -- go build ./...`
Expected: PASS, no build errors.
Ready for the user to commit — files: `config/config.go`, `config/default.toml`, `config/config_test.go`. Suggested message: `feat(config): add push.propose master switch (default true)`.

---

### Task 3: `cmd/pushflow` core (Propose + flag helpers)

**Files:**
- Create: `cmd/pushflow/pushflow.go`
- Test: `cmd/pushflow/pushflow_test.go`

**Interfaces:**
- Consumes: `git.PushOutcome` + `*git.Client` (Task 1), `internal/pkg.IO`.
- Produces:
  - `type Pusher interface { Remote() (string, error); PushDryRun(context.Context, string) (git.PushOutcome, bool, error); PushBranch(context.Context, string) error; IO() *pkg.IO }`
  - `type ConfirmFunc func(ctx context.Context, summary string) (bool, error)`
  - `type Opts struct { Branch string; Skip bool; AutoConfirm bool; NonInteractive bool }`
  - `func Propose(ctx context.Context, c Pusher, opts Opts, confirm ConfirmFunc) error`
  - `func ResolveFlags(push, noPush, propose bool) (skip, autoConfirm bool, err error)`
  - `func AddFlags(cmd *cobra.Command)`
  - `func ReadFlags(cmd *cobra.Command) (push, noPush bool)`

- [ ] **Step 1: Write the failing tests** in `cmd/pushflow/pushflow_test.go`

```go
package pushflow

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
)

type fakePusher struct {
	remote  string
	dry     git.PushOutcome
	dryOK   bool
	dryErr  error
	pushErr error
	pushed  []string
	out     *bytes.Buffer
}

func (f *fakePusher) Remote() (string, error) { return f.remote, nil }
func (f *fakePusher) PushDryRun(_ context.Context, _ string) (git.PushOutcome, bool, error) {
	return f.dry, f.dryOK, f.dryErr
}
func (f *fakePusher) PushBranch(_ context.Context, b string) error {
	f.pushed = append(f.pushed, b)
	return f.pushErr
}
func (f *fakePusher) IO() *pkg.IO {
	return &pkg.IO{In: strings.NewReader(""), Out: f.out, Err: f.out}
}

func newFake() *fakePusher {
	return &fakePusher{
		remote: "origin",
		dry:    git.PushOutcome{Kind: git.PushFastForward, Summary: "aaa..bbb"},
		dryOK:  true,
		out:    &bytes.Buffer{},
	}
}

func yes(_ context.Context, _ string) (bool, error) { return true, nil }
func no(_ context.Context, _ string) (bool, error)  { return false, nil }

func TestPropose(t *testing.T) {
	t.Parallel()

	t.Run("Skip → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b", Skip: true}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("no remote → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.remote = ""
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("nothing to push (dry ok=false) → never pushes", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.dryOK = false
		f.dry = git.PushOutcome{Kind: git.PushUpToDate}
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("dry-run error → skip, nil error", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.dryErr = errors.New("unreachable")
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("non-interactive without auto-confirm → skip", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) {
			t.Fatal("confirm must not be called")
			return false, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b", NonInteractive: true}, boom); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("auto-confirm pushes without calling confirm", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) {
			t.Fatal("confirm must not be called under AutoConfirm")
			return false, nil
		}
		if err := Propose(t.Context(), f, Opts{Branch: "b", AutoConfirm: true, NonInteractive: true}, boom); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || f.pushed[0] != "b" {
			t.Fatalf("pushed %v, want [b]", f.pushed)
		}
	})

	t.Run("confirm Yes → pushes the branch", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || f.pushed[0] != "b" {
			t.Fatalf("pushed %v, want [b]", f.pushed)
		}
	})

	t.Run("confirm No → does not push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, no); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 0 {
			t.Fatalf("pushed %v, want none", f.pushed)
		}
	})

	t.Run("confirm error propagates", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		boom := func(_ context.Context, _ string) (bool, error) { return false, errors.New("tty fail") }
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, boom); err == nil {
			t.Fatal("want error from confirm, got nil")
		}
	})

	t.Run("push failure is returned", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.pushErr = errors.New("denied")
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err == nil {
			t.Fatal("want push error, got nil")
		}
	})

	t.Run("preview is printed before confirm", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		if err := Propose(t.Context(), f, Opts{Branch: "b"}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "aaa..bbb") {
			t.Fatalf("preview missing summary; got %q", f.out.String())
		}
	})
}

func TestResolveFlags(t *testing.T) {
	t.Parallel()

	t.Run("both flags → error", func(t *testing.T) {
		t.Parallel()
		if _, _, err := ResolveFlags(true, true, true); err == nil {
			t.Fatal("want error for --push + --no-push")
		}
	})

	t.Run("no-push → skip", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(false, true, true)
		if err != nil || !skip || auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=true auto=false", skip, auto, err)
		}
	})

	t.Run("propose=false → skip", func(t *testing.T) {
		t.Parallel()
		skip, _, err := ResolveFlags(false, false, false)
		if err != nil || !skip {
			t.Fatalf("got skip=%v err=%v, want skip=true", skip, err)
		}
	})

	t.Run("push → auto-confirm, no skip", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(true, false, true)
		if err != nil || skip || !auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=false auto=true", skip, auto, err)
		}
	})

	t.Run("neither → no skip, no auto", func(t *testing.T) {
		t.Parallel()
		skip, auto, err := ResolveFlags(false, false, true)
		if err != nil || skip || auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want both false", skip, auto, err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/pushflow/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement `cmd/pushflow/pushflow.go`**

```go
// Package pushflow implements the shared "offer to push a branch after the
// action" step used by issue close, commit, and the review lifecycle commands.
package pushflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// Pusher is the slice of *git.Client the proposal step needs.
type Pusher interface {
	Remote() (string, error)
	PushDryRun(ctx context.Context, branch string) (git.PushOutcome, bool, error)
	PushBranch(ctx context.Context, branch string) error
	IO() *pkg.IO
}

// Compile-time check that the production client satisfies the role.
var _ Pusher = (*git.Client)(nil)

// ConfirmFunc asks the operator to confirm the push. Production uses a huh
// form (NewHuhConfirm); tests inject a stub.
type ConfirmFunc func(ctx context.Context, summary string) (bool, error)

// Opts configures one Propose call.
type Opts struct {
	Branch         string // branch to push
	Skip           bool   // --no-push or config push.propose=false
	AutoConfirm    bool   // --push: push without prompting
	NonInteractive bool   // -y / no TTY: skip unless AutoConfirm
}

// Propose runs the preview → confirm → push step. It returns nil on every skip
// path (gated off, no remote, nothing to push, unreachable remote, declined).
// A confirmed push that fails returns the wrapped git error; the caller's
// already-completed action is not rolled back.
func Propose(ctx context.Context, c Pusher, opts Opts, confirm ConfirmFunc) error {
	if opts.Skip || opts.Branch == "" {
		return nil
	}

	remote, err := c.Remote()
	if err != nil || remote == "" {
		return nil
	}

	outcome, ok, err := c.PushDryRun(ctx, opts.Branch)
	if err != nil || !ok {
		// Unreachable remote or nothing to push → skip silently.
		return nil
	}

	fmt.Fprintf(c.IO().Out, "Push %q to %s — %s\n", opts.Branch, remote, outcome.Summary)

	if opts.NonInteractive && !opts.AutoConfirm {
		return nil
	}

	proceed := opts.AutoConfirm
	if !proceed {
		proceed, err = confirm(ctx, fmt.Sprintf("Push %q to %s?", opts.Branch, remote))
		if err != nil {
			return fmt.Errorf("push confirm: %w", err)
		}
	}
	if !proceed {
		return nil
	}

	if err := c.PushBranch(ctx, opts.Branch); err != nil {
		return fmt.Errorf("push %q: %w", opts.Branch, err)
	}
	fmt.Fprintf(c.IO().Out, "Pushed %q to %s.\n", opts.Branch, remote)
	return nil
}

// ResolveFlags combines the --push/--no-push flags with the config master
// switch into the (skip, autoConfirm) decision. The two flags are mutually
// exclusive.
func ResolveFlags(push, noPush, propose bool) (skip, autoConfirm bool, err error) {
	if push && noPush {
		return false, false, errors.New("--push and --no-push are mutually exclusive")
	}
	if noPush || !propose {
		return true, false, nil
	}
	return false, push, nil
}

// AddFlags registers --push and --no-push on cmd. Call from each command that
// offers the push proposal.
func AddFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("push", false, "push the resulting branch to the remote after the action (skips the prompt)")
	cmd.Flags().Bool("no-push", false, "skip the post-action push proposal")
}

// ReadFlags reads --push/--no-push from cmd. Missing flags read as false, so it
// is safe to call from a shared deps builder used by commands that did not add
// the flags. Mutual exclusion is enforced later by ResolveFlags.
func ReadFlags(cmd *cobra.Command) (push, noPush bool) {
	if cmd.Flags().Lookup("push") != nil {
		push, _ = cmd.Flags().GetBool("push")
	}
	if cmd.Flags().Lookup("no-push") != nil {
		noPush, _ = cmd.Flags().GetBool("no-push")
	}
	return push, noPush
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/pushflow/ -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/pushflow/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/pushflow/pushflow.go`, `cmd/pushflow/pushflow_test.go`. Suggested message: `feat(pushflow): add shared push-proposal orchestrator`.

---

### Task 4: production confirm (`tui.PushConfirm` + `pushflow.NewHuhConfirm`)

**Files:**
- Create: `tui/push.go`
- Create: `cmd/pushflow/confirm.go`

**Interfaces:**
- Consumes: `pushflow.ConfirmFunc` (Task 3), `charmbracelet/huh`.
- Produces:
  - `func tui.PushConfirm(summary string, confirmed *bool) *huh.Group`
  - `func pushflow.NewHuhConfirm() ConfirmFunc` — default selection is **Yes**.

> Note: the huh form needs a real TTY, so it is not unit-tested directly (mirrors the existing `tui.IssueMergeConfirm`/`IssueDeleteBranch` wrappers, which are exercised only through their injected test doubles). Behaviour is covered by the injected `ConfirmFunc` stubs in Tasks 3, 5, 7.

- [ ] **Step 1: Add the tui form** — create `tui/push.go`

```go
package tui

import "github.com/charmbracelet/huh"

// PushConfirm asks whether to push a branch to the remote. summary is the
// preview line (e.g. `Push "feat-x" to origin?`). The caller seeds *confirmed
// to set the default selection.
func PushConfirm(summary string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(summary).
			Affirmative("Push").
			Negative("Skip").
			Value(confirmed),
	)
}
```

- [ ] **Step 2: Add the production confirm** — create `cmd/pushflow/confirm.go`

```go
package pushflow

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)

// NewHuhConfirm returns the production ConfirmFunc: a huh confirm whose default
// selection is Yes (the push is the expected outcome of an opt-in flow).
func NewHuhConfirm() ConfirmFunc {
	return func(ctx context.Context, summary string) (bool, error) {
		confirmed := true // default Yes
		if err := huh.NewForm(tui.PushConfirm(summary, &confirmed)).RunWithContext(ctx); err != nil {
			return false, fmt.Errorf("push confirm form: %w", err)
		}
		return confirmed, nil
	}
}
```

- [ ] **Step 3: Build + vet**

Run: `mise exec -- go build ./... && mise exec -- go vet ./cmd/pushflow/ ./tui/`
Expected: success.

- [ ] **Step 4: Hand off**

Ready for the user to commit — files: `tui/push.go`, `cmd/pushflow/confirm.go`. Suggested message: `feat(pushflow,tui): add default-Yes huh push confirm`.

---

### Task 5: wire `issue close`

**Files:**
- Modify: `cmd/issue/close.go` (closeDeps fields; AddFlags; closeRunE; runClose final step)
- Test: `cmd/issue/close_e2e_test.go` (one new origin-backed subtest)

**Interfaces:**
- Consumes: `pushflow.{AddFlags,ReadFlags,ResolveFlags,Propose,NewHuhConfirm,ConfirmFunc,Opts}` (Tasks 3–4), `config.AppConfig.Push.Propose` (Task 2).
- The push target is the resolved merge target `base` (already computed in `runClose`).

**Design note (why this is additive):** `closeDeps` gains a `pushConfirm ConfirmFunc` field. Production sets it in `closeRunE`; every existing test builds `closeDeps` *without* it (nil), and `runClose` only proposes when it is non-nil — so all current close tests are unchanged and the push step is inert in them.

- [ ] **Step 1: Write the failing test** — add to `cmd/issue/close_e2e_test.go`

This test drives a close on an origin-backed repo with a Yes push-confirm and asserts `origin/main` advanced to the local `main` tip. Use the existing origin-backed construction pattern (as at `close_e2e_test.go:396`/`:857`) to build `client`, `store`, `cfg`, and an `originDir`; seed an in-progress branch with one commit ahead of `main`; then:

```go
func TestClose_ProposesPushOfMergeTarget(t *testing.T) {
	t.Parallel()

	// Arrange: origin-backed repo, seeded in-progress branch ahead of main,
	// cfg with Branch.Base="main" and Push.Propose=true. Build deps with a
	// Yes push-confirm. (Mirror the origin rig used by the existing
	// TestClose_* origin tests; capture originDir.)
	deps, originDir, base := newCloseOriginRig(t) // helper added alongside this test
	deps.cfg.Push.Propose = true
	deps.pushConfirm = func(_ context.Context, _ string) (bool, error) { return true, nil }

	prompter := /* scriptedPrompter that picks the branch, Rebase, confirm=true,
	   no delete, no tracker */ newScriptedCloseRig(t, deps)

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("origin main advanced to local main", func(t *testing.T) {
		local := revParse(t, deps /* cloneDir */, "refs/heads/"+base)
		remote := revParse(t, originDir, "refs/heads/"+base)
		if local != remote {
			t.Fatalf("origin %s = %s, want local tip %s", base, remote, local)
		}
	})
}
```

Implement `newCloseOriginRig` and `revParse` as small helpers next to the test (reuse the `mustGit`/origin pattern from the existing origin tests). If a scripted-close helper already exists for origin tests, reuse it instead of `newScriptedCloseRig`.

- [ ] **Step 2: Run it to verify it fails**

Run: `mise exec -- go test ./cmd/issue/ -run TestClose_ProposesPushOfMergeTarget -v`
Expected: FAIL — `deps.pushConfirm` undefined.

- [ ] **Step 3: Add the field + flag + wiring** in `cmd/issue/close.go`

Add the import `"github.com/piprim/git-zf/cmd/pushflow"`.

In `closeDeps`, add fields:

```go
	// push proposal wiring (Phase 1). pushConfirm is nil in tests that build
	// closeDeps directly, which disables the push step there.
	push, noPush bool
	pushConfirm  pushflow.ConfirmFunc
```

In `getCloseCmd`, after the `--base` flag:

```go
	pushflow.AddFlags(cmd)
```

In `closeRunE`, after `deps.baseOverride = baseOverride`:

```go
	deps.push, deps.noPush = pushflow.ReadFlags(cmd)
	deps.pushConfirm = pushflow.NewHuhConfirm()
```

In `runClose`, replace the final success block:

```go
	fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
```

with:

```go
	fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return proposeClosePush(ctx, deps, base)
```

Add the helper at the end of `cmd/issue/close.go`:

```go
// proposeClosePush offers to push the merge target (base) after a successful
// close. No-op when no confirm was wired (tests) or when gating/skip applies.
func proposeClosePush(ctx context.Context, deps closeDeps, base string) error {
	if deps.pushConfirm == nil {
		return nil
	}
	skip, auto, err := pushflow.ResolveFlags(deps.push, deps.noPush, deps.cfg.Push.Propose)
	if err != nil {
		return err
	}
	return pushflow.Propose(ctx, deps.client, pushflow.Opts{
		Branch:      base,
		Skip:        skip,
		AutoConfirm: auto,
	}, deps.pushConfirm)
}
```

- [ ] **Step 4: Run the new test + the full close suite**

Run: `mise exec -- go test ./cmd/issue/ -run '^TestClose_' -v`
Expected: PASS — the new test passes and every pre-existing `TestClose_*` still passes (their `closeDeps` has a nil `pushConfirm`, so the push step is skipped).

- [ ] **Step 5: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/issue/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/issue/close.go`, `cmd/issue/close_e2e_test.go`. Suggested message: `feat(issue): offer to push the merge target after close`.

---

### Task 6: wire `commit`

**Files:**
- Modify: `cmd/commit/commit.go` (AddFlags; post-commit push step)
- Test: `cmd/commit/commit_push_test.go` (new) — flag/opts wiring

**Interfaces:**
- Consumes: `pushflow.{AddFlags,ReadFlags,ResolveFlags,Propose,NewHuhConfirm}` (Tasks 3–4), `config.AppConfig.Push.Propose` (Task 2). `*git.Client` satisfies `pushflow.Pusher`.
- Push target = current branch.

- [ ] **Step 1: Write the failing test** — create `cmd/commit/commit_push_test.go`

```go
package commit

import (
	"testing"

	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/spf13/cobra"
)

// commitPushDecision is the pure flag→decision step used by the commit push
// wiring; tested here in isolation from the store/repo.
func TestCommitPushDecision(t *testing.T) {
	t.Parallel()

	newCmd := func(args ...string) *cobra.Command {
		cmd := &cobra.Command{Use: "commit", RunE: func(*cobra.Command, []string) error { return nil }}
		cmd.Flags().BoolP("yes", "y", false, "")
		pushflow.AddFlags(cmd)
		_ = cmd.ParseFlags(args)
		return cmd
	}

	t.Run("--no-push → skip", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("--no-push")
		push, noPush := pushflow.ReadFlags(cmd)
		skip, _, err := pushflow.ResolveFlags(push, noPush, true)
		if err != nil || !skip {
			t.Fatalf("got skip=%v err=%v, want skip=true", skip, err)
		}
	})

	t.Run("--push → auto-confirm", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("--push")
		push, noPush := pushflow.ReadFlags(cmd)
		skip, auto, err := pushflow.ResolveFlags(push, noPush, true)
		if err != nil || skip || !auto {
			t.Fatalf("got skip=%v auto=%v err=%v, want skip=false auto=true", skip, auto, err)
		}
	})

	t.Run("-y maps to NonInteractive", func(t *testing.T) {
		t.Parallel()
		cmd := newCmd("-y")
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			t.Fatal("yes flag not parsed")
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `mise exec -- go test ./cmd/commit/ -run TestCommitPushDecision -v`
Expected: FAIL — `pushflow` not yet imported by the package / flags not added (compile error until Step 3 adds the import via the source change). If it compiles and passes already, proceed; the wiring in Step 3 is still required.

- [ ] **Step 3: Add the flag + post-commit push** in `cmd/commit/commit.go`

Add the import `"github.com/piprim/git-zf/cmd/pushflow"`.

In `GetRootCmd`, after the existing flag registrations (`f.StringVar(&author, ...)`):

```go
	pushflow.AddFlags(cmd)
```

In `runE`, replace the final commit block:

```go
	if err := client.Commit(cmd.Context(), msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}
```

with:

```go
	if err := client.Commit(cmd.Context(), msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	return proposeCommitPush(cmd, client, c.appConfig.Push.Propose)
}

// proposeCommitPush offers to push the current branch after a successful commit.
func proposeCommitPush(cmd *cobra.Command, client *git.Client, propose bool) error {
	push, noPush := pushflow.ReadFlags(cmd)
	skip, auto, err := pushflow.ResolveFlags(push, noPush, propose)
	if err != nil {
		return err
	}

	branch, err := client.CurrentBranch()
	if err != nil {
		return nil // detached/unknown HEAD → nothing to offer
	}

	yes, _ := cmd.Flags().GetBool("yes")

	return pushflow.Propose(cmd.Context(), client, pushflow.Opts{
		Branch:         branch,
		Skip:           skip,
		AutoConfirm:    auto,
		NonInteractive: yes,
	}, pushflow.NewHuhConfirm())
}
```

(`git` is already imported in `commit.go`.)

- [ ] **Step 4: Run the test + full commit suite**

Run: `mise exec -- go test ./cmd/commit/ -v`
Expected: PASS.

- [ ] **Step 5: Vet + build, then hand off**

Run: `mise exec -- go vet ./cmd/commit/ && mise exec -- go build ./...`
Expected: success.
Ready for the user to commit — files: `cmd/commit/commit.go`, `cmd/commit/commit_push_test.go`. Suggested message: `feat(commit): offer to push the current branch after commit`.

---

### Task 7: wire `review request` / `approve` / `reject`

**Files:**
- Modify: `cmd/review/deps.go` (reviewDeps fields; buildReviewDeps wiring; shared helper)
- Modify: `cmd/review/request.go` (AddFlags; push feature branch)
- Modify: `cmd/review/approve.go` (AddFlags; push @review when it exists)
- Modify: `cmd/review/reject.go` (AddFlags; push @review when reviewer commits exist)
- Test: `cmd/review/review_e2e_test.go` (one new origin-backed subtest for request)

**Interfaces:**
- Consumes: `pushflow.{AddFlags,ReadFlags,ResolveFlags,Propose,NewHuhConfirm,ConfirmFunc}` (Tasks 3–4), `config.AppConfig.Push.Propose` (Task 2).
- Targets: request → feature branch; approve/reject → `<slug>@review` (guarded).

**Design note:** like close, `reviewDeps` gains a `pushConfirm ConfirmFunc` set in `buildReviewDeps`. Existing tests build `reviewDeps` literals without it (nil) → push step inert.

- [ ] **Step 1: Write the failing test** — add to `cmd/review/review_e2e_test.go`

Using the existing origin-backed rig (`newReviewE2ERigWithOrigin`), submit a feature branch for review with a Yes push-confirm and assert the feature branch now exists on origin:

```go
func TestReviewRequest_ProposesFeatureBranchPush(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERigWithOrigin(t) // existing helper; exposes originDir
	// ... seed an in-progress feature branch with a commit (as the existing
	// request tests do) ...

	deps := rig.deps()
	deps.cfg.Push.Propose = true
	deps.pushConfirm = func(_ context.Context, _ string) (bool, error) { return true, nil }

	prompter := /* scripted ReviewPrompter that picks the seeded branch */ nil

	if err := runReviewRequestInteractive(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	t.Run("feature branch present on origin", func(t *testing.T) {
		featureBranch := /* the seeded feature branch name */ ""
		cmd := exec.CommandContext(t.Context(), "git", "-C", rig.originDir, "rev-parse", "refs/heads/"+featureBranch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("origin missing %s: %v\n%s", featureBranch, err, out)
		}
	})
}
```

Fill the elided parts from the nearest existing request test in this file (branch seeding + scripted prompter). If `newReviewE2ERigWithOrigin` does not expose `originDir`, add the field to the rig struct.

- [ ] **Step 2: Run it to verify it fails**

Run: `mise exec -- go test ./cmd/review/ -run TestReviewRequest_ProposesFeatureBranchPush -v`
Expected: FAIL — `deps.pushConfirm` undefined.

- [ ] **Step 3: Add deps fields + shared helper** in `cmd/review/deps.go`

Add the import `"github.com/piprim/git-zf/cmd/pushflow"`.

In `reviewDeps`, add:

```go
	// push proposal wiring (Phase 1). pushConfirm is nil in tests that build
	// reviewDeps literals, disabling the push step there.
	push, noPush bool
	pushConfirm  pushflow.ConfirmFunc
```

In `buildReviewDeps`, after `deps := reviewDeps{client: client, store: s, cfg: cfg}`:

```go
	deps.push, deps.noPush = pushflow.ReadFlags(cmd)
	deps.pushConfirm = pushflow.NewHuhConfirm()
```

Add a shared helper at the end of `deps.go`:

```go
// proposeReviewPush offers to push branch after a review transition. No-op when
// no confirm was wired (tests) or when gating/skip applies.
func proposeReviewPush(ctx context.Context, deps reviewDeps, branch string) error {
	if deps.pushConfirm == nil {
		return nil
	}
	skip, auto, err := pushflow.ResolveFlags(deps.push, deps.noPush, deps.cfg.Push.Propose)
	if err != nil {
		return err
	}
	return pushflow.Propose(ctx, deps.client, pushflow.Opts{
		Branch:      branch,
		Skip:        skip,
		AutoConfirm: auto,
	}, deps.pushConfirm)
}
```

- [ ] **Step 4: Register flags + call the helper in each subcommand**

In `cmd/review/request.go` `getRequestCmd`, add `pushflow.AddFlags(cmd)` to the returned command (assign the command to a variable first, add flags, then return it). At the end of `runReviewRequest`, before the final `return nil`, push the feature branch:

```go
	if err := proposeReviewPush(ctx, deps, featureBranch); err != nil {
		return err
	}

	return nil
```

In `cmd/review/approve.go` `getApproveCmd`, add `pushflow.AddFlags(cmd)`. At the end of `runReviewApprove`, before `return nil`, push `<slug>@review` when it exists:

```go
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		if err := proposeReviewPush(ctx, deps, reviewBranch); err != nil {
			return err
		}
	}

	return nil
```

In `cmd/review/reject.go` `getRejectCmd`, add `pushflow.AddFlags(cmd)`. In `runReviewReject`, push `<slug>@review` only when reviewer commits exist (`reviewBranchExists && hasCommits`). Add, before the `if hasCommits { ... }` print block returns:

```go
	if reviewBranchExists && hasCommits {
		if err := proposeReviewPush(ctx, deps, reviewBranch); err != nil {
			return err
		}
	}
```

Add it so it runs after the store update and before the function's existing return paths print/return. (The reject flow has multiple returns; place the push immediately before the `if hasCommits {` block so it runs on the reviewer-commits path.)

Add the import `"github.com/piprim/git-zf/cmd/pushflow"` to each of `request.go`, `approve.go`, `reject.go` (the `getXxxCmd` functions reference `pushflow.AddFlags`).

- [ ] **Step 5: Run the new test + full review suite**

Run: `mise exec -- go test ./cmd/review/ -v`
Expected: PASS — the new test passes; all existing review tests still pass (their `reviewDeps` literals have nil `pushConfirm`).

- [ ] **Step 6: Vet + build + full test suite, then hand off**

Run: `mise exec -- go vet ./... && mise exec -- go build ./... && mise exec -- go test ./...`
Expected: success across the repo.
Ready for the user to commit — files: `cmd/review/deps.go`, `cmd/review/request.go`, `cmd/review/approve.go`, `cmd/review/reject.go`, `cmd/review/review_e2e_test.go`. Suggested message: `feat(review): offer to push feature/@review branch after request/approve/reject`.

---

## Final verification (all tasks)

- [ ] `mise exec -- go build ./...` — clean build.
- [ ] `mise exec -- go vet ./...` — no findings.
- [ ] `mise exec -- go test ./...` — full suite green.
- [ ] Run GitNexus `detect_changes({scope: "compare", base_ref: "main"})` and confirm only the expected symbols/flows changed (new `git/push.go`, `cmd/pushflow`, additive fields/flags on the five commands) — no unexpected impact on existing close/review execution flows.
- [ ] Hand off to the user for commit; do not commit automatically.

## Self-Review

- **Spec coverage (Phase 1):** `PushDryRun`/`PushBranch` (Task 1); `push.propose` config (Task 2); `cmd/pushflow` `Propose`/`ConfirmFunc`/`Pusher`/flags (Tasks 3–4); `--push`/`--no-push` + default-Yes confirm + `-y`-skips-unless-`--push` (Tasks 3–6); five call sites (Tasks 5–7); skip rules — no remote / nothing to push / unreachable / gated off (Task 3, with `PushDryRun` semantics from Task 1). Phase 2 (merge-vs-parent preview, `ResolveParentBranch`) is intentionally excluded.
- **Placeholders:** the only elided code is inside the two command-level E2E tests (Tasks 5 & 7), which reuse existing per-file rig/seed/prompter helpers the implementer copies from the nearest existing test in the same file; assertions and wiring are concrete. All library/source code is complete.
- **Type consistency:** `PushOutcome`/`PushKind` (Task 1) are consumed unchanged by `Pusher`/`Propose` (Task 3); `ConfirmFunc`, `Opts{Branch,Skip,AutoConfirm,NonInteractive}`, `ResolveFlags`, `ReadFlags`, `AddFlags`, `NewHuhConfirm` names match across Tasks 3–7; `proposeClosePush`/`proposeCommitPush`/`proposeReviewPush` are each defined in their own task.
