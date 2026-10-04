# Review Sync Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enforce that reviewer commits on `<slug>@review` are incorporated into the feature branch before new work lands on it, and give every dead-end (blocked close, silent branch deletion) a guided way out.

**Architecture:** A shared no-network detection helper in `cmd/issueflow` feeds four enforcement points: a guard in the `git zf commit` flow, a `pre-commit` hook installed by `git zf init` (backed by a hidden `review guard-commit` subcommand), an extended `review sync` that merges the review branch, and a close-time recovery path in `reviewPreflight`. Two new merge primitives in `git/` support conflict-preserving merges without parsing git's message text.

**Tech Stack:** Go (via `mise exec -- go`), cobra, huh (through prompter interfaces), exec-based git wrappers in `git/`, SQLite store in `store/`.

**Spec:** `docs/superpowers/specs/2026-07-08-review-sync-guard-design.md`

## Global Constraints

- Run all Go commands through mise: `mise exec -- go test ./...`, `mise exec -- go vet ./...`, `mise exec -- go build -o ./bin/git-zf .`
- **Agents never run `git add` / `git commit` / `git push` — the user handles all git operations.** At each task's end, stop and report; the user commits. (This overrides the usual per-task commit step.)
- Every distinct assertion or scenario in tests is wrapped in a named `t.Run` — even single-assertion tests.
- golangci-lint is CI-only here (v2 config vs v1 local binary); verify with `go vet` + build + tests instead.
- Guards fail open: any detection error (store, refs, HEAD) means the guard passes silently.
- Guard detection makes **no network calls** — it reads only refs already fetched.
- Never classify git failures by parsing message text — use exit codes, `MERGE_HEAD` presence, and pre-flight checks (`IsDirty`).
- User-facing hint strings (used verbatim in several places):
  - sync hint: `Run 'git zf review sync' to incorporate them first.`
  - stash hint: `Run 'git stash', then 'git zf review sync', then 'git stash pop'`
  - resolve hint: `Resolve the conflict markers, then run 'git zf commit' to conclude the merge.`
- GitNexus blast radius (measured 2026-07-08): `reviewPreflight` HIGH, `runReviewRequest` HIGH, `FastForwardOnly` CRITICAL (4 direct callers, 8 flows — **do not modify `FastForwardOnly` itself; only its call site in `reviewPreflight` changes**), `runReviewSync*` LOW, `ReviewPrompter` LOW (16 impacted symbols — every implementor must gain the new method in the same task). Re-run `node .gitnexus/run.cjs detect-changes` before handing each task back to the user.

---

### Task 1: Merge primitives — `ErrMergeConflicts`, `MergeInProgress`, `MergeLeaveConflicts`

**Files:**
- Modify: `git/merge.go` (add after `MergeForward`, line ~300)
- Test: `git/merge_test.go` (append)

**Interfaces:**
- Consumes: existing `Client.Checkout`, `Client.WorkingTreeRoot`, `Client.runInteractive`, `Client.GitDir`.
- Produces:
  - `var ErrMergeConflicts = errors.New("merge conflicts")` (package `git`)
  - `func (c *Client) MergeInProgress() (bool, error)` — true when `MERGE_HEAD` exists in the git dir.
  - `func (c *Client) MergeLeaveConflicts(ctx context.Context, sourceBranch, targetBranch string) error` — like `MergeForward` but on conflict returns an error wrapping `ErrMergeConflicts` and leaves the merge in progress (no `merge --abort`).

- [ ] **Step 1: Write the failing tests**

Append to `git/merge_test.go` (reuse the file's existing repo-setup helpers if present; otherwise the inline setup below is self-contained):

```go
func setupDivergedRepo(t *testing.T) (dir string, client *Client) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-m", "init")
	// source branch edits f.txt one way…
	run("checkout", "-b", "source")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "source change")
	// …target edits it the other way → guaranteed conflict.
	run("checkout", "main")
	run("checkout", "-b", "target")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-am", "target change")

	client, err := NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: io.Discard, Err: io.Discard}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	return dir, client
}

func TestMergeLeaveConflicts(t *testing.T) {
	t.Run("conflict returns ErrMergeConflicts and leaves MERGE_HEAD", func(t *testing.T) {
		_, client := setupDivergedRepo(t)
		err := client.MergeLeaveConflicts(t.Context(), "source", "target")
		if !errors.Is(err, ErrMergeConflicts) {
			t.Fatalf("want ErrMergeConflicts, got %v", err)
		}
		inProgress, mhErr := client.MergeInProgress()
		if mhErr != nil {
			t.Fatalf("MergeInProgress: %v", mhErr)
		}
		if !inProgress {
			t.Fatal("expected MERGE_HEAD to exist after conflicted merge")
		}
	})

	t.Run("clean merge creates merge commit and returns nil", func(t *testing.T) {
		dir, client := setupDivergedRepo(t)
		// Make source non-conflicting: new branch off main touching another file.
		run := func(args ...string) {
			t.Helper()
			cmd := exec.CommandContext(t.Context(), "git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("checkout", "main")
		run("checkout", "-b", "clean-source")
		if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", "other.txt")
		run("commit", "-m", "clean change")
		if err := client.MergeLeaveConflicts(t.Context(), "clean-source", "target"); err != nil {
			t.Fatalf("clean merge: %v", err)
		}
		inProgress, _ := client.MergeInProgress()
		if inProgress {
			t.Fatal("no MERGE_HEAD expected after clean merge")
		}
	})
}

func TestMergeInProgress(t *testing.T) {
	t.Run("false on a quiet repo", func(t *testing.T) {
		_, client := setupDivergedRepo(t)
		inProgress, err := client.MergeInProgress()
		if err != nil {
			t.Fatalf("MergeInProgress: %v", err)
		}
		if inProgress {
			t.Fatal("expected no merge in progress")
		}
	})
}
```

Add missing imports to the test file as needed (`bytes`, `errors`, `io`, `os`, `os/exec`, `path/filepath`, `github.com/piprim/git-zf/internal/pkg`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./git/... -run "TestMergeLeaveConflicts|TestMergeInProgress" -v`
Expected: FAIL — `undefined: ErrMergeConflicts`, `client.MergeLeaveConflicts undefined`, `client.MergeInProgress undefined`.

- [ ] **Step 3: Implement**

Append to `git/merge.go`:

```go
// ErrMergeConflicts marks a merge stopped by content conflicts. The merge is
// intentionally left in progress (MERGE_HEAD present) so the user can resolve
// the markers and conclude it. Detect with errors.Is.
var ErrMergeConflicts = errors.New("merge conflicts")

// MergeInProgress reports whether a merge is currently in progress, i.e.
// MERGE_HEAD exists in the repository's git directory.
func (c *Client) MergeInProgress() (bool, error) {
	gitDir, err := c.GitDir()
	if err != nil {
		return false, fmt.Errorf("git dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "MERGE_HEAD")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat MERGE_HEAD: %w", err)
	}
	return true, nil
}

// MergeLeaveConflicts checks out targetBranch and merges sourceBranch into it
// with `git merge --no-edit`. Unlike MergeForward, a conflicted merge is left
// in progress (no abort) and the returned error wraps ErrMergeConflicts, so
// callers can tell "resolve and conclude" apart from a hard failure. Failure
// classification never parses git's message text: non-zero exit with
// MERGE_HEAD present is a conflict; without MERGE_HEAD the raw error passes
// through.
func (c *Client) MergeLeaveConflicts(ctx context.Context, sourceBranch, targetBranch string) error {
	if err := c.Checkout(ctx, targetBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", targetBranch, err)
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-edit", sourceBranch); err != nil {
		if inProgress, mhErr := c.MergeInProgress(); mhErr == nil && inProgress {
			return fmt.Errorf("merge %s into %s: %w", sourceBranch, targetBranch, ErrMergeConflicts)
		}
		return fmt.Errorf("merge --no-edit %s: %w", sourceBranch, err)
	}

	return nil
}
```

Add `errors`, `os`, `path/filepath` to `git/merge.go` imports if absent.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./git/... -run "TestMergeLeaveConflicts|TestMergeInProgress" -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./git/...`, then report; user commits.

---

### Task 2: Shared detection helper — `issueflow.PendingReviewCommits` / `PendingReviewForHEAD`

**Files:**
- Create: `cmd/issueflow/review_guard.go`
- Test: `cmd/issueflow/review_guard_test.go`

**Interfaces:**
- Consumes: `client.ReadReviewRef(ctx, slug) (*git.ReviewRef, string, error)` (reads the local `refs/zf/reviews/<slug>` copy), `client.BranchExists(name)`, `client.Remote()`, `client.ResolveRef("refs/remotes/"+…)`, `client.CommitsAhead(ctx, branch, base)`, `client.CurrentBranch()`, `client.MergeInProgress()` (Task 1), `s.ListBranches(ctx, store.BranchStatusAll)`.
- Produces (used by Tasks 3, 6, 7, 9):

```go
type PendingReview struct {
	EffectiveRef string             // "42@review" or "origin/42@review"
	Commits      int                // commits ahead of the feature branch
	Status       store.ReviewStatus // approved | changes_requested
}

func PendingReviewCommits(ctx context.Context, client *git.Client, slug, featureBranch string) (*PendingReview, error)
func IssueSlugForBranch(ctx context.Context, s *store.Store, branchName string) (string, error)
func PendingReviewForHEAD(ctx context.Context, client *git.Client, s *store.Store) (*PendingReview, string, error)
```

`PendingReviewForHEAD` returns `(nil, "", nil)` whenever the guard must not trip; the second return is the current branch name when pending.

- [ ] **Step 1: Write the failing tests**

Create `cmd/issueflow/review_guard_test.go`. Self-contained rig (mirrors `cmd/review`'s):

```go
package issueflow

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

type guardRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	run    func(args ...string)
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("checkout", "-b", "42@feat@title")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: work")

	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: os.Stdout, Err: os.Stderr}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "42", Title: "title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "42@feat@title", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &guardRig{dir: dir, client: client, store: s, run: run}
}

// addReviewBranchWithCommit creates 42@review off the feature branch with one
// extra commit, then returns to the feature branch.
func (r *guardRig) addReviewBranchWithCommit(t *testing.T) {
	t.Helper()
	r.run("checkout", "-b", "42@review")
	if err := os.WriteFile(filepath.Join(r.dir, "review-fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.run("add", "review-fix.txt")
	r.run("commit", "-m", "fix: reviewer nit")
	r.run("checkout", "42@feat@title")
}

func (r *guardRig) writeReviewRef(t *testing.T, status string) {
	t.Helper()
	if _, err := r.client.WriteReviewRef(t.Context(), "42", git.ReviewRef{
		Status: status, Round: 1, FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
}

func TestPendingReviewCommits(t *testing.T) {
	t.Run("trips on changes_requested with unincorporated commits", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.Commits != 1 || p.EffectiveRef != "42@review" {
			t.Fatalf("want pending 1 commit on 42@review, got %+v", p)
		}
	})

	t.Run("trips on approved", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusApproved))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p == nil {
			t.Fatalf("want pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent during in_review", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusInReview))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent without a review ref (stale branch after close)", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent when commits are contained", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		rig.run("merge", "--no-edit", "42@review")
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending after merge, got %+v err %v", p, err)
		}
	})

	t.Run("silent when no review branch exists anywhere", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("prefers remote-tracking ref when local review branch is stale", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Simulate: reviewer pushed a second commit that only origin has.
		// The remote is never contacted — the remote-tracking ref is set by
		// hand with update-ref, exactly the state a past `git fetch` leaves.
		rig.run("remote", "add", "origin", rig.dir)
		rig.run("checkout", "42@review")
		if err := os.WriteFile(filepath.Join(rig.dir, "review-fix-2.txt"), []byte("fix2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rig.run("add", "review-fix-2.txt")
		rig.run("commit", "-m", "fix: second reviewer nit")
		rig.run("update-ref", "refs/remotes/origin/42@review", "HEAD")
		rig.run("reset", "--hard", "HEAD~1") // local 42@review is now stale
		rig.run("checkout", "42@feat@title")

		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.EffectiveRef != "origin/42@review" || p.Commits != 2 {
			t.Fatalf("want 2 commits via origin/42@review, got %+v", p)
		}
	})

	t.Run("local wins when it is ahead of the remote-tracking ref", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Remote-tracking ref exists but points one commit behind the local
		// branch — the reviewer's own machine before pushing.
		rig.run("remote", "add", "origin", rig.dir)
		rig.run("update-ref", "refs/remotes/origin/42@review", "42@review~1")

		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.EffectiveRef != "42@review" || p.Commits != 1 {
			t.Fatalf("want 1 commit via local 42@review, got %+v", p)
		}
	})
}

func TestPendingReviewForHEAD(t *testing.T) {
	t.Run("pending on the checked-out tracked branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, branch, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p == nil || branch != "42@feat@title" {
			t.Fatalf("want pending on 42@feat@title, got %+v %q err %v", p, branch, err)
		}
	})

	t.Run("exempt on @review branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		rig.run("checkout", "42@review")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt on @review branch, got %+v err %v", p, err)
		}
	})

	t.Run("exempt while a merge is in progress", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Force a conflicted merge so MERGE_HEAD exists.
		if err := os.WriteFile(filepath.Join(rig.dir, "review-fix.txt"), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rig.run("add", "review-fix.txt")
		rig.run("commit", "-m", "feat: conflicting")
		_ = rig.client.MergeLeaveConflicts(t.Context(), "42@review", "42@feat@title")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt mid-merge, got %+v err %v", p, err)
		}
	})

	t.Run("exempt on an untracked branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.run("checkout", "-b", "random-branch")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt on untracked branch, got %+v err %v", p, err)
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/issueflow/... -run "TestPendingReview" -v`
Expected: FAIL — `undefined: PendingReviewCommits`, etc.

- [ ] **Step 3: Implement**

Create `cmd/issueflow/review_guard.go`:

```go
package issueflow

import (
	"context"
	"strings"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// PendingReview describes reviewer commits on <slug>@review that a reviewer
// decision (approved / changes_requested) says the developer must incorporate
// into the feature branch.
type PendingReview struct {
	EffectiveRef string             // "42@review" or "origin/42@review"
	Commits      int                // commits ahead of the feature branch
	Status       store.ReviewStatus // approved | changes_requested
}

// PendingReviewCommits reports reviewer commits awaiting incorporation for
// slug's featureBranch, or nil when nothing is pending. It reads only local
// refs — no network — so it is cheap enough for a pre-commit hook and works
// offline. The guard is armed only by a decided review ref: in_review means
// the reviewer hasn't decided, and a stale review branch with no ref (e.g.
// after a close) never trips it.
func PendingReviewCommits(ctx context.Context, client *git.Client, slug, featureBranch string) (*PendingReview, error) {
	ref, _, err := client.ReadReviewRef(ctx, slug)
	if err != nil || ref == nil {
		return nil, err
	}
	status := store.ReviewStatus(ref.Status)
	if status != store.ReviewStatusApproved && status != store.ReviewStatusChangesRequested {
		return nil, nil
	}

	reviewBranch := slug + "@review"
	effective := ""
	localExists, _ := client.BranchExists(reviewBranch)
	if localExists {
		effective = reviewBranch
	}
	if remote, _ := client.Remote(); remote != "" {
		candidate := remote + "/" + reviewBranch
		if _, refErr := client.ResolveRef("refs/remotes/" + candidate); refErr == nil {
			switch {
			case !localExists:
				effective = candidate
			default:
				// Both exist: if the remote-tracking ref carries commits the
				// local branch lacks, the reviewer pushed (or force-pushed)
				// after this checkout — their copy is authoritative. A local
				// branch ahead of the remote (the reviewer's own machine)
				// keeps winning. Offline: compares two already-fetched refs.
				if ahead, aErr := client.CommitsAhead(ctx, candidate, reviewBranch); aErr == nil && ahead > 0 {
					effective = candidate
				}
			}
		}
	}
	if effective == "" {
		return nil, nil
	}

	n, err := client.CommitsAhead(ctx, effective, featureBranch)
	if err != nil || n == 0 {
		return nil, err
	}
	return &PendingReview{EffectiveRef: effective, Commits: n, Status: status}, nil
}

// IssueSlugForBranch returns the issue slug owning branchName in the store,
// or "" when the branch is not tracked.
func IssueSlugForBranch(ctx context.Context, s *store.Store, branchName string) (string, error) {
	rows, err := s.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return "", err
	}
	for _, b := range rows {
		if b.BranchName == branchName {
			return b.IssueSlug, nil
		}
	}
	return "", nil
}

// PendingReviewForHEAD applies the commit-guard exemptions and returns the
// pending review for the currently checked-out branch, plus that branch name.
// It returns (nil, "", nil) whenever the guard must not trip: detached HEAD,
// an @review branch, a merge in progress (concluding a merge is exactly how
// incorporation happens), an untracked branch, or nothing pending.
func PendingReviewForHEAD(ctx context.Context, client *git.Client, s *store.Store) (*PendingReview, string, error) {
	branchName, err := client.CurrentBranch()
	if err != nil || branchName == "" {
		return nil, "", nil
	}
	if strings.HasSuffix(branchName, "@review") {
		return nil, "", nil
	}
	if inProgress, mhErr := client.MergeInProgress(); mhErr == nil && inProgress {
		return nil, "", nil
	}
	slug, err := IssueSlugForBranch(ctx, s, branchName)
	if err != nil || slug == "" {
		return nil, "", nil
	}
	pending, err := PendingReviewCommits(ctx, client, slug, branchName)
	if err != nil || pending == nil {
		return nil, "", nil
	}
	return pending, branchName, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/issueflow/... -run "TestPendingReview" -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/issueflow/...`; report; user commits.

---

### Task 3: Hidden subcommand `review guard-commit`

**Files:**
- Create: `cmd/review/guard_commit.go`
- Modify: `cmd/review/review.go:25-36` (register in `AddCommand`)
- Test: `cmd/review/guard_commit_test.go`

**Interfaces:**
- Consumes: `buildReviewDeps` (fail-open on error), `issueflow.PendingReviewForHEAD` (Task 2).
- Produces: `git zf review guard-commit` — exit 0 silent pass, exit non-zero with actionable message when the guard trips. Called by the pre-commit hook (Task 4).

- [ ] **Step 1: Write the failing test**

Create `cmd/review/guard_commit_test.go` (uses `newReviewE2ERig` from `review_e2e_test.go`; the rig's repo has branch `77@feat@my-feature`, slug `77`, and currently checks out `main` — tests check out the feature branch first):

```go
package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// seedPendingReview creates 77@review with one commit ahead of the feature
// branch and writes a local review ref with the given status.
func seedPendingReview(t *testing.T, rig *reviewE2ERig, status store.ReviewStatus) {
	t.Helper()
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	mustRunGit(t, rig.dir, "checkout", "-b", "77@review")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if _, err := rig.client.WriteReviewRef(t.Context(), "77", git.ReviewRef{
		Status: string(status), Round: 1, FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
}

func TestGuardCommit(t *testing.T) {
	t.Run("blocks with sync hint on changes_requested", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
		err := runReviewGuardCommit(t.Context(), rig.deps())
		if err == nil {
			t.Fatal("want guard error, got nil")
		}
		if !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync hint in %q", err.Error())
		}
		if !strings.Contains(err.Error(), "--no-verify") {
			t.Fatalf("want bypass hint in %q", err.Error())
		}
	})

	t.Run("passes during in_review", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusInReview)
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("passes on the @review branch itself", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
		mustRunGit(t, rig.dir, "checkout", "77@review")
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass on review branch, got %v", err)
		}
	})

	t.Run("passes on an untracked branch", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		mustRunGit(t, rig.dir, "checkout", "-b", "scratch")
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})
}
```

If `review_e2e_test.go` has no `mustRunGit(t, dir, args...)` helper, add one to `guard_commit_test.go` (same body as the rig's local `run`, taking `dir` as a parameter).

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/review/... -run "^TestGuardCommit" -v`
Expected: FAIL — `undefined: runReviewGuardCommit`.

- [ ] **Step 3: Implement**

Create `cmd/review/guard_commit.go`:

```go
package review

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/spf13/cobra"
)

// getGuardCommitCmd returns the internal `review guard-commit` command used by
// the pre-commit hook installed by `git zf init`. It exits non-zero when the
// currently checked-out feature branch has unincorporated reviewer commits.
// Hidden from help output. Fail-open on any setup error: a guard must never
// brick committing.
func (r Review) getGuardCommitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard-commit",
		Short:  "Internal: block commits while reviewer commits await incorporation (used by pre-commit hook)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, r.appConfig)
			if err != nil {
				return nil // fail-open
			}
			defer func() { _ = deps.store.Close() }()

			return runReviewGuardCommit(ctx, deps)
		},
	}
}

func runReviewGuardCommit(ctx context.Context, deps reviewDeps) error {
	pending, branchName, err := issueflow.PendingReviewForHEAD(ctx, deps.client, deps.store)
	if err != nil || pending == nil {
		return nil // fail-open
	}

	return fmt.Errorf(
		"commit blocked: %s has %d reviewer commit(s) not in %q (status: %s).\n"+
			"Run 'git zf review sync' to incorporate them first.\n"+
			"To bypass (not recommended): git commit --no-verify",
		pending.EffectiveRef, pending.Commits, branchName, pending.Status)
}
```

Register it in `cmd/review/review.go` — add `r.getGuardCommitCmd(),` to the `cmd.AddCommand(...)` list (after `r.getGuardCmd(),`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/review/... -run "^TestGuardCommit" -v`
Expected: PASS.

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/review/...`; report; user commits.

---

### Task 4: `git zf init` installs the pre-commit hook

**Files:**
- Modify: `cmd/init/init.go` (whole `runE`, new const, small refactor)
- Test: Create `cmd/init/init_test.go`

**Interfaces:**
- Consumes: existing `git.NewClient` / `client.GitDir()` flow.
- Produces: `.git/hooks/pre-commit` alongside `.git/hooks/pre-push`, same per-hook idempotency and foreign-hook policy. Hook body calls `git zf review guard-commit` (Task 3).

- [ ] **Step 1: Write the failing tests**

Create `cmd/init/init_test.go`. `runE` builds its own client via `git.NewClient` (cwd-based), so tests run the command with the working directory set to a temp repo — use `t.Chdir` (Go 1.24+):

```go
package init_cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newInitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "T"},
		{"config", "user.email", "t@t"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func runInit(t *testing.T, dir string) string {
	t.Helper()
	t.Chdir(dir)
	cmd := New().GetRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(bytes.NewReader(nil))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestInit_InstallsBothHooks(t *testing.T) {
	dir := newInitRepo(t)
	out := runInit(t, dir)

	t.Run("pre-push hook written", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "pre-push")); err != nil {
			t.Fatalf("pre-push missing: %v", err)
		}
	})
	t.Run("pre-commit hook written and calls guard-commit", func(t *testing.T) {
		b, err := os.ReadFile(filepath.Join(dir, ".git", "hooks", "pre-commit"))
		if err != nil {
			t.Fatalf("pre-commit missing: %v", err)
		}
		if !strings.Contains(string(b), "git zf review guard-commit") {
			t.Fatalf("pre-commit does not call guard-commit:\n%s", b)
		}
	})
	t.Run("reports both hooks", func(t *testing.T) {
		if !strings.Contains(out, "pre-push") || !strings.Contains(out, "pre-commit") {
			t.Fatalf("output missing hook names:\n%s", out)
		}
	})
}

func TestInit_Idempotent(t *testing.T) {
	dir := newInitRepo(t)
	runInit(t, dir)
	out := runInit(t, dir)
	t.Run("second run reports up to date", func(t *testing.T) {
		if !strings.Contains(out, "already up to date") {
			t.Fatalf("want up-to-date message, got:\n%s", out)
		}
	})
}

func TestInit_PreservesForeignPreCommit(t *testing.T) {
	dir := newInitRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "#!/bin/sh\necho custom\n"
	if err := os.WriteFile(hookPath, []byte(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	out := runInit(t, dir)
	t.Run("foreign hook untouched", func(t *testing.T) {
		b, _ := os.ReadFile(hookPath)
		if string(b) != foreign {
			t.Fatalf("foreign hook was overwritten:\n%s", b)
		}
	})
	t.Run("warning with snippet printed", func(t *testing.T) {
		if !strings.Contains(out, "WARNING") || !strings.Contains(out, "guard-commit") {
			t.Fatalf("want warning + snippet, got:\n%s", out)
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/init/... -v`
Expected: `TestInit_InstallsBothHooks/pre-commit…` and `TestInit_PreservesForeignPreCommit` FAIL (no pre-commit hook yet). `TestInit_InstallsBothHooks/pre-push…` may already pass — fine.

- [ ] **Step 3: Implement**

In `cmd/init/init.go`: add the new script constant, a `hookSpec` table, and refactor `runE` to loop. The per-hook logic is today's body with the hook name and foreign-hook snippet parametrized:

```go
// preCommitHookScript is the shell script written to .git/hooks/pre-commit.
// It calls `git zf review guard-commit`, which blocks new commits on a feature
// branch while reviewer commits on <slug>@review await incorporation.
const preCommitHookScript = `#!/bin/sh
# git-zf review commit guard — installed by 'git zf init'
# Requires: git zf install (binary in git exec-path)
if ! git zf review guard-commit; then
    exit 1
fi
exit 0
`

// hookSpec describes one hook managed by `git zf init`.
type hookSpec struct {
	name    string // file name under .git/hooks/
	script  string // full managed script body
	snippet string // line to suggest when a foreign hook already exists
}

var managedHooks = []hookSpec{
	{
		name:    "pre-push",
		script:  prePushHookScript,
		snippet: `  git zf review guard "$(echo "$local_ref" | sed 's|^refs/heads/||')"`,
	},
	{
		name:    "pre-commit",
		script:  preCommitHookScript,
		snippet: `  git zf review guard-commit || exit 1`,
	},
}
```

Rewrite `runE` to resolve `gitDir` once, then `for _, h := range managedHooks { installHook(cmd, gitDir, h) }`, where `installHook` is the current body generalized:

```go
func (i Init) runE(cmd *cobra.Command, _ []string) error {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	gitDir, err := client.GitDir()
	if err != nil {
		return fmt.Errorf("resolve git dir: %w", err)
	}

	for _, h := range managedHooks {
		if err := installHook(cmd, gitDir, h); err != nil {
			return err
		}
	}
	return nil
}

// installHook writes one managed hook, preserving foreign hooks. Mirrors the
// original single-hook logic: byte-identical → up-to-date no-op; foreign hook
// → never overwrite, print the snippet to add manually; missing → write.
func installHook(cmd *cobra.Command, gitDir string, h hookSpec) error {
	hookPath := filepath.Join(gitDir, "hooks", h.name)

	if info, err := os.Stat(hookPath); err == nil {
		existing, readErr := os.ReadFile(hookPath) //nolint:gosec
		if readErr == nil {
			if string(existing) == h.script {
				fmt.Fprintf(cmd.OutOrStdout(), "%s hook already up to date at %s\n", h.name, hookPath)
				return nil
			}
			if info.Mode()&0o111 == 0 {
				_ = os.Chmod(hookPath, info.Mode()|0o755)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"WARNING: a %s hook already exists at %s\n"+
					"The git-zf guard was NOT installed to avoid overwriting your hook.\n"+
					"To enable the guard, add this to your existing hook:\n\n%s\n",
				h.name, hookPath, h.snippet)
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		return fmt.Errorf("create hooks dir: %w", err)
	}

	//nolint:gosec // hook script is a compile-time constant
	if err := os.WriteFile(hookPath, []byte(h.script), 0o755); err != nil {
		return fmt.Errorf("write %s hook: %w", h.name, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s hook installed at %s\n", h.name, hookPath)
	return nil
}
```

Keep `prePushHookScript` unchanged. Delete the now-inlined single-hook code from the old `runE`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/init/... -v`
Expected: PASS (all three test functions).

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/init/...`; report; user commits.

---

### Task 5: `ReviewPrompter.Confirm`

**Files:**
- Modify: `cmd/review/prompter.go` (interface + both implementations)

**Interfaces:**
- Produces (used by Tasks 6 and 9):
  - Interface method: `Confirm(ctx context.Context, title string) (bool, error)`
  - `scriptedReviewPrompter` fields: `ConfirmAnswer bool`, `ConfirmErr error`

- [ ] **Step 1: Add the method (compile-time-checked change, no behavior yet)**

In `cmd/review/prompter.go`:

Add to the `ReviewPrompter` interface:

```go
	// Confirm presents a yes/no confirmation with the given title. Used by the
	// request flow to offer merging pending reviewer commits before re-requesting.
	Confirm(ctx context.Context, title string) (bool, error)
```

Add the huh implementation:

```go
func (p *huhReviewPrompter) Confirm(ctx context.Context, title string) (bool, error) {
	confirmed := true
	form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&confirmed)))
	if err := form.RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}
	return confirmed, nil
}
```

Add to `scriptedReviewPrompter` (fields + method):

```go
	ConfirmAnswer    bool
	ConfirmErr       error
```

```go
func (s *scriptedReviewPrompter) Confirm(_ context.Context, _ string) (bool, error) {
	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}
	return s.ConfirmAnswer, nil
}
```

Other `ReviewPrompter` implementations must gain the method too or the build breaks — `captureReviewPrompter` in `cmd/review/reconcile_e2e_test.go` embeds or implements the interface; add:

```go
func (c *captureReviewPrompter) Confirm(_ context.Context, _ string) (bool, error) { return true, nil }
```

(Check for further implementors with `mise exec -- go build ./...` — the compiler lists them.)

- [ ] **Step 2: Verify the build and existing tests**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/review/...`
Expected: PASS — no behavior change yet.

- [ ] **Step 3: Checkpoint** — report; user commits.

---

### Task 6: Extend `review sync` — review-branch merge + any-branch candidates

**Files:**
- Modify: `cmd/review/sync.go` (`runReviewSyncInteractive`, `runReviewSync`)
- Test: Create `cmd/review/sync_e2e_test.go`

**Interfaces:**
- Consumes: `issueflow.PendingReviewCommits` (Task 2), `client.MergeLeaveConflicts` / `git.ErrMergeConflicts` (Task 1), `client.IsDirty`, existing parent-merge code.
- Produces: `runReviewSync(ctx, deps, issueSlug)` with two-step semantics relied on by Tasks 7–9's hint messages.

- [ ] **Step 1: Write the failing tests**

Create `cmd/review/sync_e2e_test.go` (reuses `newReviewE2ERig` + `seedPendingReview` from Task 3; add `t.Run` per assertion):

```go
package review

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestRunReviewSync_MergesReviewBranch(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commit incorporated", func(t *testing.T) {
		n, cErr := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if cErr != nil {
			t.Fatalf("CommitsAhead: %v", cErr)
		}
		if n != 0 {
			t.Fatalf("want 0 pending commits after sync, got %d", n)
		}
	})
	t.Run("review branch kept (cleanup is close/request's job)", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review should survive sync")
		}
	})
}

func TestRunReviewSync_ConflictLeavesMergeInProgress(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	// Conflicting change on the feature branch (same file as the reviewer's).
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat: conflicting work")

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("returns nil (expected outcome, instructions printed)", func(t *testing.T) {
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("merge left in progress", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if !inProgress {
			t.Fatal("want MERGE_HEAD present")
		}
	})
	t.Run("resolve hint printed", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "git zf commit") {
			t.Fatalf("want resolve hint, got:\n%s", rig.stdout.String())
		}
	})
}

func TestRunReviewSync_DirtyTreeRefusedBeforeMerge(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("refused with stash hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git stash") {
			t.Fatalf("want stash hint error, got %v", err)
		}
	})
	t.Run("no merge attempted", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("no MERGE_HEAD expected")
		}
	})
}

func TestRunReviewSync_InReviewStatusDoesNotMerge(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusInReview)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("nothing to sync", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		if !strings.Contains(rig.stdout.String(), "Nothing to sync") {
			t.Fatalf("want nothing-to-sync, got:\n%s", rig.stdout.String())
		}
	})
	t.Run("review commits untouched", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if n != 1 {
			t.Fatalf("want review commit still pending, got %d", n)
		}
	})
}

// seedParent gives the rig's issue 77 a parent issue 7 whose branch carries
// one commit the sub-task doesn't have. No remote in this rig, so sync's
// parent merge uses the local parent branch.
func seedParent(t *testing.T, rig *reviewE2ERig) {
	t.Helper()
	mustRunGit(t, rig.dir, "checkout", "main")
	mustRunGit(t, rig.dir, "checkout", "-b", "7@feat@parent")
	if err := os.WriteFile(filepath.Join(rig.dir, "parent.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "parent.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat(7): parent commit")
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "7", Title: "parent", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "7@feat@parent", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := rig.store.InsertIssueRelation(t.Context(), "7", "77"); err != nil {
		t.Fatalf("InsertIssueRelation: %v", err)
	}
}

func TestRunReviewSync_SubtaskMergesReviewThenParent(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	seedParent(t, rig)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commits incorporated (step 1)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if n != 0 {
			t.Fatalf("want 0 pending review commits, got %d", n)
		}
	})
	t.Run("parent drift merged (step 2)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", "77@feat@my-feature")
		if n != 0 {
			t.Fatalf("want parent merged, %d commits behind", n)
		}
	})
}

func TestRunReviewSync_ConflictedReviewMergeSkipsParent(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	// Conflicting change on the feature branch (same file as the reviewer's).
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat: conflicting work")
	seedParent(t, rig)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("returns nil with merge left in progress", func(t *testing.T) {
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		inProgress, _ := rig.client.MergeInProgress()
		if !inProgress {
			t.Fatal("want MERGE_HEAD present")
		}
	})
	t.Run("parent merge NOT attempted (step 2 skipped)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", "77@feat@my-feature")
		if n == 0 {
			t.Fatal("parent must not be merged while step 1 conflicts are unresolved")
		}
	})
}

func TestRunReviewSyncInteractive_OffersBranchWithPendingReview(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	capture := &captureReviewPrompter{} // records offered branches, picks none

	err := runReviewSyncInteractive(t.Context(), rig.deps(), capture)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("interactive sync: %v", err)
		}
	})
	t.Run("77 offered despite having no parent issue", func(t *testing.T) {
		if !branchSlugsOffered(capture.seen)["77"] {
			t.Fatalf("want slug 77 offered, got %v", capture.seen)
		}
	})
}
```

Adjust `captureReviewPrompter` usage to its actual field names in `reconcile_e2e_test.go` (it records offered branches in a slice named `seen` there; keep names in sync).

Note: `errors` import is used only if you assert with `errors.Is`; drop it otherwise.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/review/... -run "^TestRunReviewSync" -v`
Expected: FAIL — current sync errors with `has no parent — sync is only for sub-tasks`, and interactive filters 77 out.

- [ ] **Step 3: Implement**

Rewrite in `cmd/review/sync.go`:

`runReviewSyncInteractive` — candidate filter becomes "has a parent OR has pending review commits", and refs are freshened first:

```go
func runReviewSyncInteractive(ctx context.Context, deps reviewDeps, prompter ReviewPrompter) error {
	issueflow.ReconcileMergedFromRefs(ctx, deps.store, deps.client)

	// Freshen refs so pending-review detection and parent drift see the
	// current remote state (best-effort; sync must work offline too).
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.Fetch(ctx)
	}

	all, err := deps.store.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var candidates []store.BranchRow
	for _, b := range all {
		parent, pErr := deps.store.GetParentIssue(ctx, b.IssueSlug)
		hasParent := pErr == nil && parent != ""
		pending, _ := issueflow.PendingReviewCommits(ctx, deps.client, b.IssueSlug, b.BranchName)
		if hasParent || pending != nil {
			candidates = append(candidates, b)
		}
	}

	if len(candidates) == 0 {
		fmt.Fprintln(deps.client.IO().Out, "Nothing to sync.")
		return nil
	}

	picked, err := prompter.PickBranch(ctx, "Select branch to sync:", candidates, currentIssueSlug(deps.client))
	if err != nil {
		return fmt.Errorf("branch picker: %w", err)
	}
	if picked == nil {
		return nil
	}

	return runReviewSync(ctx, deps, picked.IssueSlug)
}
```

`runReviewSync` — step 1 (review-branch merge) before the existing parent merge, which becomes optional:

```go
func runReviewSync(ctx context.Context, deps reviewDeps, issueSlug string) error {
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	var childBranch string
	for _, b := range branches {
		if b.IssueSlug == issueSlug {
			childBranch = b.BranchName
			break
		}
	}
	if childBranch == "" {
		return fmt.Errorf("no branch found for issue %q", issueSlug)
	}

	// Step 1 — incorporate reviewer commits from <slug>@review, if a decided
	// review says they are pending.
	pending, err := issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, childBranch)
	if err != nil {
		return fmt.Errorf("detect pending review commits: %w", err)
	}
	if pending != nil {
		dirty, dErr := deps.client.IsDirty(ctx)
		if dErr != nil {
			return fmt.Errorf("status check: %w", dErr)
		}
		if dirty {
			return fmt.Errorf("working tree has uncommitted changes — cannot merge %s.\n"+
				"Run 'git stash', then 'git zf review sync', then 'git stash pop'", pending.EffectiveRef)
		}

		fmt.Fprintf(deps.client.IO().Out, "Merging %d reviewer commit(s) from %s into %q...\n",
			pending.Commits, pending.EffectiveRef, childBranch)

		if mErr := deps.client.MergeLeaveConflicts(ctx, pending.EffectiveRef, childBranch); mErr != nil {
			if errors.Is(mErr, git.ErrMergeConflicts) {
				fmt.Fprintf(deps.client.IO().Out,
					"Merge left in progress with conflicts.\n"+
						"Resolve the conflict markers, then run 'git zf commit' to conclude the merge.\n")
				return nil
			}
			return mErr
		}
		fmt.Fprintf(deps.client.IO().Out, "Reviewer commits incorporated into %q.\n", childBranch)
	}

	// Step 2 — parent integration drift (sub-tasks only; unchanged semantics).
	parentSlug, err := deps.store.GetParentIssue(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("get parent issue: %w", err)
	}
	if parentSlug == "" {
		if pending == nil {
			fmt.Fprintln(deps.client.IO().Out, "Nothing to sync.")
		}
		return nil
	}

	var parentBranch string
	for _, b := range branches {
		if b.IssueSlug == parentSlug {
			parentBranch = b.BranchName
			break
		}
	}
	if parentBranch == "" {
		return fmt.Errorf("no branch found for parent issue %q", parentSlug)
	}

	effectiveParent := parentBranch
	if remote, _ := deps.client.Remote(); remote != "" {
		effectiveParent = remote + "/" + parentBranch
	}

	behind, err := deps.client.CommitsAhead(ctx, effectiveParent, childBranch)
	if err != nil {
		return fmt.Errorf("check drift: %w", err)
	}
	if behind == 0 {
		fmt.Fprintf(deps.client.IO().Out, "Branch %q is already up to date with %q.\n",
			childBranch, parentBranch)
		return nil
	}

	fmt.Fprintf(deps.client.IO().Out, "Merging %q (%d new commit(s)) into %q...\n",
		parentBranch, behind, childBranch)

	if err := deps.client.MergeForward(ctx, effectiveParent, childBranch); err != nil {
		_ = deps.client.AbortMerge(ctx)
		return fmt.Errorf("merge %s into %s failed (conflicts detected — merge aborted): %w",
			parentBranch, childBranch, err)
	}

	fmt.Fprintf(deps.client.IO().Out, "Branch %q synced with %q.\n", childBranch, parentBranch)
	return nil
}
```

Add imports `errors`, `github.com/piprim/git-zf/git` to `sync.go`.

- [ ] **Step 4: Run tests**

Run: `mise exec -- go test ./cmd/review/... -run "^TestRunReviewSync|^TestReviewSync" -v`
Expected: new tests PASS **and** the pre-existing `TestReviewSync_UsesRemoteParentBase` / `TestReviewSync_ExcludesSubtaskMergedInSiblingClone` still PASS (parent semantics unchanged).

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/review/...`; report; user commits.

---

### Task 7: `git zf commit` guard with inline merge offer

**Files:**
- Create: `cmd/commit/review_guard.go`
- Modify: `cmd/commit/commit.go:117-133` (wire guard into `runE` after the store opens)
- Test: Create: `cmd/commit/review_guard_test.go`

**Interfaces:**
- Consumes: `issueflow.PendingReviewForHEAD` (Task 2), `client.IsDirty`, `client.MergeLeaveConflicts` / `git.ErrMergeConflicts` (Task 1), `flags.NoVerify` (`tui.CommitOption`).
- Produces: `guardPendingReview(ctx, client, s, confirm reviewConfirmFunc) error` and `type reviewConfirmFunc func(ctx context.Context, title string) (bool, error)`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/commit/review_guard_test.go`. The rig mirrors Task 2's (`cmd/commit` cannot import `cmd/review` test helpers). Full setup:

```go
package commit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

type guardRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	stdout *bytes.Buffer
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("checkout", "-b", "42@feat@title")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: work")
	// Reviewer branch with one commit, back to feature branch.
	run("checkout", "-b", "42@review")
	if err := os.WriteFile(filepath.Join(dir, "review-fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "review-fix.txt")
	run("commit", "-m", "fix: reviewer nit")
	run("checkout", "42@feat@title")

	stdout := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: os.Stderr}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "42", Title: "title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "42@feat@title", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := client.WriteReviewRef(t.Context(), "42", git.ReviewRef{
		Status: string(store.ReviewStatusChangesRequested), Round: 1,
		FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
	return &guardRig{dir: dir, client: client, store: s, stdout: stdout}
}

func answer(v bool) reviewConfirmFunc {
	return func(context.Context, string) (bool, error) { return v, nil }
}

func TestGuardPendingReview(t *testing.T) {
	t.Run("accept merges and continues", func(t *testing.T) {
		rig := newGuardRig(t)
		if err := guardPendingReview(t.Context(), rig.client, rig.store, answer(true)); err != nil {
			t.Fatalf("want nil after accepted merge, got %v", err)
		}
		n, _ := rig.client.CommitsAhead(t.Context(), "42@review", "42@feat@title")
		if n != 0 {
			t.Fatalf("want reviewer commit incorporated, %d pending", n)
		}
	})

	t.Run("decline aborts with sync hint", func(t *testing.T) {
		rig := newGuardRig(t)
		err := guardPendingReview(t.Context(), rig.client, rig.store, answer(false))
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync-hint error, got %v", err)
		}
	})

	t.Run("dirty tree refused before merge with stash hint", func(t *testing.T) {
		rig := newGuardRig(t)
		if err := os.WriteFile(filepath.Join(rig.dir, "feat.txt"), []byte("wip\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := guardPendingReview(t.Context(), rig.client, rig.store, answer(true))
		if err == nil || !strings.Contains(err.Error(), "git stash") {
			t.Fatalf("want stash-hint error, got %v", err)
		}
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("no merge must be attempted on a dirty tree")
		}
	})

	t.Run("no pending review passes silently", func(t *testing.T) {
		rig := newGuardRig(t)
		mustRun := func(args ...string) {
			cmd := exec.CommandContext(t.Context(), "git", args...)
			cmd.Dir = rig.dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		mustRun("merge", "--no-edit", "42@review") // incorporate manually
		if err := guardPendingReview(t.Context(), rig.client, rig.store, answer(false)); err != nil {
			t.Fatalf("want silent pass, got %v", err)
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/commit/... -run "^TestGuardPendingReview" -v`
Expected: FAIL — `undefined: guardPendingReview`, `undefined: reviewConfirmFunc`.

- [ ] **Step 3: Implement**

Create `cmd/commit/review_guard.go`:

```go
package commit

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// reviewConfirmFunc resolves the "merge reviewer commits now?" decision.
// Production uses newHuhReviewConfirm; tests inject canned answers.
type reviewConfirmFunc func(ctx context.Context, title string) (bool, error)

func newHuhReviewConfirm() reviewConfirmFunc {
	return func(ctx context.Context, title string) (bool, error) {
		confirmed := true
		form := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(title).Value(&confirmed)))
		if err := form.RunWithContext(ctx); err != nil {
			return false, fmt.Errorf("review merge confirm: %w", err)
		}
		return confirmed, nil
	}
}

// guardPendingReview blocks the commit flow while reviewer commits on the
// issue's @review branch await incorporation (reviewer decision: approved or
// changes_requested). It offers to merge them inline; declining aborts with
// the sync hint. Detection errors fail open — a guard must never brick
// committing. Callers skip it entirely under --no-verify.
func guardPendingReview(ctx context.Context, client *git.Client, s *store.Store, confirm reviewConfirmFunc) error {
	pending, branchName, err := issueflow.PendingReviewForHEAD(ctx, client, s)
	if err != nil || pending == nil {
		return nil // fail-open
	}

	ok, err := confirm(ctx, fmt.Sprintf("%s has %d reviewer commit(s) not in your branch — merge now?",
		pending.EffectiveRef, pending.Commits))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("commit aborted: incorporate reviewer commits first.\n" +
			"Run 'git zf review sync' to incorporate them first.\n" +
			"To bypass (not recommended): git zf commit --no-verify")
	}

	dirty, err := client.IsDirty(ctx)
	if err != nil {
		return fmt.Errorf("status check: %w", err)
	}
	if dirty {
		return fmt.Errorf("cannot merge %s: working tree has uncommitted changes.\n"+
			"Run 'git stash', then 'git zf review sync', then 'git stash pop', then retry the commit",
			pending.EffectiveRef)
	}

	if err := client.MergeLeaveConflicts(ctx, pending.EffectiveRef, branchName); err != nil {
		if errors.Is(err, git.ErrMergeConflicts) {
			return fmt.Errorf("merge of %s left in progress with conflicts.\n"+
				"Resolve the conflict markers, then run 'git zf commit' to conclude the merge",
				pending.EffectiveRef)
		}
		return err
	}

	fmt.Fprintf(client.IO().Out, "Reviewer commits from %s incorporated into %q.\n",
		pending.EffectiveRef, branchName)
	return nil
}
```

Wire into `cmd/commit/commit.go` `runE`, right after the store opens (after the `defer s.Close()` at line ~121) and before the form work:

```go
	if !flags.NoVerify {
		if err := guardPendingReview(cmd.Context(), client, s, newHuhReviewConfirm()); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Run tests**

Run: `mise exec -- go test ./cmd/commit/... -v`
Expected: new tests PASS, existing commit tests PASS.

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/commit/...`; report; user commits.

---

### Task 8: Close-time recovery in `reviewPreflight`

**Files:**
- Modify: `cmd/issue/close.go` (sentinel near line 85; approved-branch block at lines 392-408)
- Test: `cmd/issue/close_e2e_test.go` (append; reuse `newCloseRig`, `seedReviewRef`, `rig.deps()`)

**Interfaces:**
- Consumes: `issueflow.PendingReviewCommits` (Task 2 — replaces close's own local-first effective-ref resolution, so a stale local `@review` never shadows a fresher `origin/…@review`), `client.CommitsAhead`, `client.MergeDryRun(ctx, branchName, baseBranch)` (source, target), `client.MergeForward`, `client.AbortMerge`, `client.IsDirty`, `client.FastForwardOnly` (call site only — **CRITICAL blast radius, do not touch its body**).
- Produces: `var ErrReviewSyncNeeded = errors.New("review sync needed")` (package-level in `cmd/issue`, detectable with `errors.Is`).

- [ ] **Step 1: Write the failing tests**

Append to `cmd/issue/close_e2e_test.go`. Model the setup on the existing `TestClose_ReviewPreflight` cases (which use `seedReviewRef(t, rig, slug, status, round)` and a `42@review`-style branch); the new part is committing on the feature branch *after* the review branch diverges:

```go
func TestClose_ReviewPreflight_DivergedCleanAutoMerges(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug
	feature := rig.pickedBranchRow().BranchName

	// Reviewer branch with a commit touching a reviewer-only file.
	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "reviewer-only.txt", "r\n")
	mustRunGitAt(t, rig.dir, "add", "reviewer-only.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	// Developer continues on the feature branch (non-conflicting file) → diverged.
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "dev-later.txt", "d\n")
	mustRunGitAt(t, rig.dir, "add", "dev-later.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: more work")

	seedReviewRef(t, rig, slug, store.ReviewStatusApproved, 1)

	err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow())

	t.Run("preflight succeeds via real merge", func(t *testing.T) {
		if err != nil {
			t.Fatalf("reviewPreflight: %v", err)
		}
	})
	t.Run("reviewer commit incorporated", func(t *testing.T) {
		n, cErr := rig.client.CommitsAhead(t.Context(), slug+"@review", feature)
		// branch may already be deleted by cleanup; only check when it survives
		if cErr == nil && n != 0 {
			t.Fatalf("want 0 pending, got %d", n)
		}
	})
	t.Run("review ref cleaned up", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), slug)
		if ref != nil {
			t.Fatalf("want review ref deleted, got %+v", ref)
		}
	})
}

func TestClose_ReviewPreflight_DivergedConflictRefusesWithSyncHint(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug
	feature := rig.pickedBranchRow().BranchName

	// Reviewer and developer edit the same file differently → conflict.
	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "clash.txt", "reviewer\n")
	mustRunGitAt(t, rig.dir, "add", "clash.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer version")
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "clash.txt", "developer\n")
	mustRunGitAt(t, rig.dir, "add", "clash.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: developer version")

	seedReviewRef(t, rig, slug, store.ReviewStatusApproved, 1)

	err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow())

	t.Run("refused with ErrReviewSyncNeeded", func(t *testing.T) {
		if !errors.Is(err, ErrReviewSyncNeeded) {
			t.Fatalf("want ErrReviewSyncNeeded, got %v", err)
		}
	})
	t.Run("sync hint present", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync hint, got %v", err)
		}
	})
	t.Run("repo left clean (no MERGE_HEAD)", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("close must never leave a merge in progress")
		}
	})
	t.Run("review ref NOT cleaned up", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), slug)
		if ref == nil {
			t.Fatal("review ref must survive a refused close")
		}
	})
}
```

Add tiny local helpers if the file lacks them (`mustRunGitAt(t, dir, args...)` and `writeFileAt(t, dir, name, content)` — same bodies as the rigs above). Check the existing rig first; reuse its helpers when present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_ReviewPreflight_Diverged" -v`
Expected: FAIL — `undefined: ErrReviewSyncNeeded`; the clean-diverged case fails on `fast-forward … : exit status 128`.

- [ ] **Step 3: Implement**

In `cmd/issue/close.go`, add the sentinel next to the existing ones (line ~85):

```go
// ErrReviewSyncNeeded is returned by reviewPreflight when reviewer commits on
// the @review branch conflict with the feature branch (or the tree is dirty)
// and the developer must run `git zf review sync` before closing.
var ErrReviewSyncNeeded = errors.New("review sync needed")
```

Replace the whole approved-case resolution + incorporation block (the current lines 376-408: `localExists` / `effectiveReview` resolution through the cleanup) with the following. The shared helper now owns effective-ref resolution — including the stale-local rule — while branch existence is still resolved locally for the cleanup decisions:

```go
	// Resolve pending reviewer commits through the shared helper so a stale
	// local <slug>@review never shadows a fresher origin/<slug>@review
	// (reviewer pushed or force-pushed after the developer's checkout).
	pending, pendErr := issueflow.PendingReviewCommits(ctx, deps.client, picked.IssueSlug, picked.BranchName)
	if pendErr != nil {
		return fmt.Errorf("detect pending review commits: %w", pendErr)
	}

	localExists, _ := deps.client.BranchExists(reviewBranch)
	remoteTrackingExists := false
	if remote, _ := deps.client.Remote(); remote != "" {
		if _, err := deps.client.ResolveRef("refs/remotes/" + remote + "/" + reviewBranch); err == nil {
			remoteTrackingExists = true
		}
	}

	if pending != nil {
		m, mErr := deps.client.CommitsAhead(ctx, picked.BranchName, pending.EffectiveRef)
		if mErr != nil {
			return fmt.Errorf("check review divergence: %w", mErr)
		}
		switch {
		case m == 0:
			// Feature branch has not moved — plain fast-forward as before.
			fmt.Fprintf(deps.client.IO().Out,
				"Incorporating %d reviewer commit(s) from %s into %s...\n",
				pending.Commits, pending.EffectiveRef, picked.BranchName)
			if err := deps.client.FastForwardOnly(ctx, pending.EffectiveRef, picked.BranchName); err != nil {
				return fmt.Errorf("fast-forward %s to %s: %w", picked.BranchName, pending.EffectiveRef, err)
			}
		default:
			// Diverged: dry-run first; close never leaves MERGE_HEAD behind.
			conflicts, dryErr := deps.client.MergeDryRun(ctx, pending.EffectiveRef, picked.BranchName)
			if dryErr != nil {
				return fmt.Errorf("review merge dry-run: %w", dryErr)
			}
			if len(conflicts) > 0 {
				return fmt.Errorf(
					"reviewer commits on %s conflict with %q (%s).\n"+
						"Run 'git zf review sync', resolve the conflicts, then close: %w",
					pending.EffectiveRef, picked.BranchName, strings.Join(conflicts, ", "), ErrReviewSyncNeeded)
			}
			if dirty, dErr := deps.client.IsDirty(ctx); dErr == nil && dirty {
				return fmt.Errorf(
					"working tree has uncommitted changes — cannot incorporate %s.\n"+
						"Run 'git stash', then retry the close: %w", pending.EffectiveRef, ErrReviewSyncNeeded)
			}
			fmt.Fprintf(deps.client.IO().Out,
				"Merging %d reviewer commit(s) from %s into %s...\n",
				pending.Commits, pending.EffectiveRef, picked.BranchName)
			if err := deps.client.MergeForward(ctx, pending.EffectiveRef, picked.BranchName); err != nil {
				_ = deps.client.AbortMerge(ctx)
				return fmt.Errorf("merge %s into %s: %w", pending.EffectiveRef, picked.BranchName, err)
			}
		}
	}

	// Cleanup mirrors today's behavior: delete the local review branch when it
	// exists, and push a remote delete when any review branch was known.
	if localExists {
		if err := deps.client.DeleteLocalBranchSafe(ctx, reviewBranch, true, deps.cfg.Branch.Base); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete %s: %v\n", reviewBranch, err)
		}
	}
	if localExists || remoteTrackingExists {
		_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
	}
```

`cmd/issue/close.go` already imports `issueflow` (used by `getPickedBranch`), so only `strings` may need adding.

Add `strings` to `close.go` imports if absent.

- [ ] **Step 4: Run tests**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v`
Expected: new tests PASS; **every pre-existing close test still PASSES** (ff path and non-review paths untouched).

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/issue/...`; report; user commits.

---

### Task 9: `review request` round-N+1 safety

**Files:**
- Modify: `cmd/review/request.go` (`runReviewRequestInteractive` line ~78, `runReviewRequest` stale-delete block lines ~127-134)
- Test: `cmd/review/sync_e2e_test.go` or new `cmd/review/request_guard_e2e_test.go` (append)

**Interfaces:**
- Consumes: `issueflow.PendingReviewCommits` (Task 2), `prompter.Confirm` (Task 5), `client.IsDirty`, `client.MergeLeaveConflicts` / `git.ErrMergeConflicts` (Task 1).
- Produces: `runReviewRequest` refuses (safety net) when pending; interactive path offers the merge first.

- [ ] **Step 1: Write the failing tests**

Append (file: `cmd/review/request_guard_e2e_test.go`):

```go
package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestReviewRequest_RefusesWithUnincorporatedReviewerCommits(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)

	err := runReviewRequest(t.Context(), rig.deps(), "77")

	t.Run("refused with sync hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync-hint refusal, got %v", err)
		}
	})
	t.Run("review branch NOT deleted", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review must survive a refused request")
		}
	})
}

func TestReviewRequest_InteractiveOfferMergesThenProceeds(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	prompter := &scriptedReviewPrompter{
		Branch:        rig.pickedBranch("77"),
		ConfirmAnswer: true,
	}

	err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter)

	t.Run("request succeeds after inline merge", func(t *testing.T) {
		if err != nil {
			t.Fatalf("interactive request: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commits incorporated", func(t *testing.T) {
		// The stale 42@review was deleted by the round-2 request, so check the
		// reviewer file landed on the feature branch instead.
		mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
		if _, statErr := os.Stat(filepath.Join(rig.dir, "reviewer.txt")); statErr != nil {
			t.Fatalf("reviewer.txt not on feature branch: %v", statErr)
		}
	})
	t.Run("round 2 ref written", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), "77")
		if ref == nil || ref.Round != 2 || ref.Status != string(store.ReviewStatusInReview) {
			t.Fatalf("want round-2 in_review ref, got %+v", ref)
		}
	})
}

func TestReviewRequest_InteractiveDeclineAborts(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	prompter := &scriptedReviewPrompter{
		Branch:        rig.pickedBranch("77"),
		ConfirmAnswer: false,
	}

	err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter)

	t.Run("aborted with sync hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want abort with sync hint, got %v", err)
		}
	})
	t.Run("review branch untouched", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review must survive a declined offer")
		}
	})
}
```

Add a tiny rig helper if missing (`rig.pickedBranch(slug)` returning the seeded `*store.BranchRow`; build it from `rig.store.ListBranches` or construct the literal `&store.BranchRow{IssueSlug: "77", BranchName: "77@feat@my-feature"}`). Add `os` / `path/filepath` imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/review/... -run "^TestReviewRequest_" -v`
Expected: the three new tests FAIL — request currently deletes `77@review` silently and never refuses. (Pre-existing `TestReviewRequest_*` tests must keep passing.)

- [ ] **Step 3: Implement**

In `runReviewRequest` (`cmd/review/request.go`), insert the safety net *before* the stale-delete block (line ~127) — the delete then only ever runs on contained commits:

```go
	// Refuse to delete reviewer work that was never incorporated. This is the
	// safety net; the interactive wrapper offers an inline merge first.
	if pending, pErr := issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, featureBranch); pErr == nil && pending != nil {
		return fmt.Errorf(
			"%s has %d unincorporated reviewer commit(s) from the previous round.\n"+
				"Run 'git zf review sync' to incorporate them first "+
				"(or delete the branch to discard them), then re-request",
			pending.EffectiveRef, pending.Commits)
	}
```

In `runReviewRequestInteractive`, after the picker returns `picked` and before `runReviewRequest` is called (line ~78):

```go
	// Best-effort branch fetch so origin/<slug>@review is visible for the
	// pending-review offer (review refs were already fetched above).
	if remote, _ := deps.client.Remote(); remote != "" {
		_ = deps.client.Fetch(ctx)
	}

	if pending, pErr := issueflow.PendingReviewCommits(ctx, deps.client, picked.IssueSlug, picked.BranchName); pErr == nil && pending != nil {
		ok, cErr := prompter.Confirm(ctx, fmt.Sprintf(
			"%s has %d reviewer commit(s) not in %q — merge now?",
			pending.EffectiveRef, pending.Commits, picked.BranchName))
		if cErr != nil {
			return fmt.Errorf("merge confirm: %w", cErr)
		}
		if !ok {
			return fmt.Errorf("request aborted: run 'git zf review sync' to incorporate reviewer commits first")
		}
		if dirty, dErr := deps.client.IsDirty(ctx); dErr == nil && dirty {
			return fmt.Errorf("working tree has uncommitted changes — cannot merge %s.\n"+
				"Run 'git stash', then 'git zf review sync', then 'git stash pop'", pending.EffectiveRef)
		}
		if mErr := deps.client.MergeLeaveConflicts(ctx, pending.EffectiveRef, picked.BranchName); mErr != nil {
			if errors.Is(mErr, git.ErrMergeConflicts) {
				fmt.Fprintf(deps.client.IO().Out,
					"Merge left in progress with conflicts.\n"+
						"Resolve the conflict markers, then run 'git zf commit' to conclude the merge.\n")
				return nil
			}
			return mErr
		}
		fmt.Fprintf(deps.client.IO().Out, "Reviewer commits incorporated into %q.\n", picked.BranchName)
	}
```

Add `errors` and `github.com/piprim/git-zf/cmd/issueflow` to `request.go` imports (`git` is already imported).

- [ ] **Step 4: Run tests**

Run: `mise exec -- go test ./cmd/review/... -v`
Expected: all review tests PASS, including the pre-existing lifecycle tests (`TestReviewLifecycle_RequestReject` ends in changes_requested with reviewer commits — verify it still passes; its round-2 request, if any, now goes through the incorporation path).

- [ ] **Step 5: Checkpoint** — `mise exec -- go vet ./cmd/review/...`; report; user commits.

---

### Task 10: README documentation + full verification

**Files:**
- Modify: `README.md` — Review section, Init section, Commit section.

**Interfaces:** none (docs). Per the user's standing rule, CLI behavior changes are not done until the README reflects them; per-command Flags blocks mirror `--help` verbatim (no new flags in this feature, so prose only).

- [ ] **Step 1: Update the Review section**

- `review sync` bullet in the command list: change to `# bring a branch up to date: reviewer commits + parent drift`.
- Replace the `**review sync**` paragraph with: works on any in-progress branch (picker pre-selects the current one). Step 1 merges pending reviewer commits from `<IssueID>@review` (only after an `approved`/`changes_requested` decision); on conflicts the merge is left in progress with conflict markers — resolve, then `git zf commit` concludes it. Step 2 merges the parent integration branch for sub-tasks (unchanged, aborts on conflict). Dirty tree → refused with the stash → sync → pop hint.
- In the round-lifecycle list, extend the reject bullet: after a reject with reviewer commits, **new commits on the feature branch are blocked** (commit guard + pre-commit hook) until `git zf review sync` incorporates them; `--no-verify` bypasses.
- Extend the `review request` paragraph: round N+1 refuses to delete a `@review` branch carrying unincorporated commits; interactive runs offer to merge them on the spot.
- Extend the approve/close prose: close auto-merges a diverged review branch when clean; on conflicts it refuses with the sync hint (never leaves a merge in progress).
- Mention the second hidden subcommand alongside `review guard`: `review guard-commit`, called by the pre-commit hook, exempting `@review` branches and in-progress merges, failing open.

- [ ] **Step 2: Update the Init section**

Init now installs **two** hooks: `pre-push` (`review guard` — blocks pushing locked branches) and `pre-commit` (`review guard-commit` — blocks committing over unincorporated reviewer commits). Same per-hook policy: idempotent, foreign hooks preserved with a printed snippet. Bypass: `git commit --no-verify` / `git push --no-verify`.

- [ ] **Step 3: Update the Commit section**

After the flags block prose, add: on a feature branch with unincorporated reviewer commits (after an `approved`/`changes_requested` decision), `git zf commit` offers to merge `<IssueID>@review` before opening the form; declining aborts with the `git zf review sync` hint; `--no-verify` skips the guard.

- [ ] **Step 4: Full verification**

Run: `mise exec -- go vet ./... && mise exec -- go build -o ./bin/git-zf . && mise exec -- go test ./...`
Expected: all green.

Run: `node .gitnexus/run.cjs detect-changes` — verify the affected symbols/flows match this plan's tasks (sync/request/close/commit/init flows) and nothing unexpected.

- [ ] **Step 5: Checkpoint** — report results; user reviews and commits.
