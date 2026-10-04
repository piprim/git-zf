# `git zf review track` / `git zf issue track` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `track` subcommand to both `git zf review` and `git zf issue` that registers
the current git branch — created via plain `git checkout` — into the git-zf store so the
developer or reviewer can participate in the review workflow without re-creating the branch.

**Architecture:** A single exported `TrackCmd(appConfig)` constructor in `cmd/review/track.go`
builds the cobra command and calls `runTrack` (unexported). Auto-detection via the current
branch name dispatches to `runTrackDeveloper` (branch.Parse succeeds) or `runTrackReviewer`
(HasSuffix `@review`). The same `TrackCmd` is added to both `cmd/review/review.go` and
`cmd/issue/issue.go` — no circular dependency since `cmd/issue` may import `cmd/review`
but not vice versa.

**Tech Stack:** Go, Cobra, modernc SQLite (`database/sql`), system `git` binary.

## Global Constraints

- Run all tests with: `mise exec -- go test ./...`
- Build binary with: `mise exec -- go build -o ./bin/git-zf .`
- Every `Test*` function wraps each assertion in `t.Run("descriptive name", func(t *testing.T){...})`.
- No new external dependencies.
- Module path: `github.com/piprim/git-zf`
- Branch naming convention: `<issueID>@<type>@<slug>[@<variant>]` — parsed via `branch.Parse()` in `branch/branch.go`.
- Review branch naming: `<issueID>@review` — detected via `strings.HasSuffix`.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `cmd/review/track.go` | **Create** | `TrackCmd`, `runTrack`, `runTrackDeveloper`, `runTrackReviewer` |
| `cmd/review/review.go` | **Modify** | Add `TrackCmd(r.appConfig)` to `GetRootCmd()` |
| `cmd/issue/issue.go` | **Modify** | Import `cmd/review`, add `review.TrackCmd(i.appConfig)` to `GetRootCmd()` |
| `cmd/review/review_e2e_test.go` | **Modify** | Add `TestTrack_Developer*` and `TestTrack_Reviewer*` tests |

---

## Task 1: Implement `track.go` with developer and reviewer paths

**Files:**
- Create: `cmd/review/track.go`
- Test: `cmd/review/review_e2e_test.go`

**Interfaces produced:**
```go
// TrackCmd returns the cobra command for both `review track` and `issue track`.
// Both command groups call this constructor — one implementation, two aliases.
func TrackCmd(appConfig *config.AppConfig) *cobra.Command
```

- [ ] **Step 1: Write failing tests for the developer path**

Add to `cmd/review/review_e2e_test.go`:

```go
func TestTrack_Developer_RegistersUnknownBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Create a feature branch with plain git (not via git zf issue start).
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "X.2@feat@part-two"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	deps := rig.deps()
	err := runTrack(ctx, deps)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack: %v", err)
		}
	})

	t.Run("branch appears in store as in_progress", func(t *testing.T) {
		rows, listErr := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		if listErr != nil {
			t.Fatalf("ListBranches: %v", listErr)
		}
		var found bool
		for _, r := range rows {
			if r.BranchName == "X.2@feat@part-two" {
				found = true
				break
			}
		}
		if !found {
			t.Error("branch not found in store as in_progress")
		}
	})

	t.Run("output mentions branch name", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "X.2@feat@part-two") {
			t.Errorf("stdout = %q, want it to mention branch name", out)
		}
	})
}

func TestTrack_Developer_IdempotentWhenAlreadyTracked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Seed the store first (as if git zf issue start had been run).
	if err := rig.store.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: "77", Title: "my feature", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "77@feat@my-feature", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Already on 77@feat@my-feature from newReviewE2ERig setup.

	deps := rig.deps()
	err := runTrack(ctx, deps)

	t.Run("no error on duplicate", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack on already-tracked branch: %v", err)
		}
	})

	t.Run("output says already tracked", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "already tracked") {
			t.Errorf("stdout = %q, want 'already tracked'", out)
		}
	})
}

func TestTrack_Developer_ErrorOnUnrecognizedBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Create a branch with no git-zf convention.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "my-random-branch"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	deps := rig.deps()
	err := runTrack(ctx, deps)

	t.Run("returns error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error for unrecognized branch name, got nil")
		}
	})
	t.Run("error mentions naming convention", func(t *testing.T) {
		if !strings.Contains(err.Error(), "naming convention") {
			t.Errorf("error = %v, want mention of naming convention", err)
		}
	})
}

func TestTrack_Reviewer_RegistersReviewBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Simulate: developer submitted for review (ref exists in_review, store has review row).
	featureSHA, _ := rig.client.ResolveRef("refs/heads/77@feat@my-feature")
	ref := git.ReviewRef{
		Status:     "in_review",
		Round:      1,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  "2026-06-20T10:00:00Z",
	}
	if _, err := rig.client.WriteReviewRef(ctx, "77", ref, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	// Reviewer creates branch manually (no git zf review start).
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "77@review"); err != nil {
		t.Fatalf("checkout review branch: %v", err)
	}

	deps := rig.deps()
	err := runTrack(ctx, deps)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack reviewer path: %v", err)
		}
	})

	t.Run("review record exists in store", func(t *testing.T) {
		latest, getErr := rig.store.GetLatestReview(ctx, "77")
		if getErr != nil {
			t.Fatalf("GetLatestReview: %v", getErr)
		}
		if latest == nil {
			t.Fatal("expected review row, got nil")
		}
	})

	t.Run("reviewer identity recorded", func(t *testing.T) {
		latest, _ := rig.store.GetLatestReview(ctx, "77")
		if latest != nil && latest.Reviewer == "" {
			t.Error("reviewer identity not recorded")
		}
	})

	t.Run("output mentions registered", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "registered") {
			t.Errorf("stdout = %q, want 'registered'", out)
		}
	})
}

func TestTrack_Reviewer_ErrorWhenNoRefExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Checkout a review branch manually — but no review ref exists.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "77@review"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	deps := rig.deps()
	err := runTrack(ctx, deps)

	t.Run("returns error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error when no review ref exists, got nil")
		}
	})
}
```

Note: `newReviewE2ERig` (defined earlier in `review_e2e_test.go`) creates a repo with
branch `77@feat@my-feature` checked out and the issue seeded in the store. The `TestTrack_Developer_RegistersUnknownBranch` test checks out a *different* branch (`X.2@feat@part-two`) that is NOT in the store.

The test for `TestTrack_Reviewer_RegistersReviewBranch` needs `git` and `store` imports
already present in the file. Ensure `"strings"` is also imported (already is).

- [ ] **Step 2: Run tests to verify they fail**

```
mise exec -- go test ./cmd/review/... -run "TestTrack_" -v
```
Expected: compile error — `runTrack` not defined.

- [ ] **Step 3: Implement `cmd/review/track.go`**

```go
package review

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

// TrackCmd returns the cobra command for both `git zf review track` and
// `git zf issue track`. Both command groups call this constructor — one
// implementation, two aliases in different namespaces.
func TrackCmd(appConfig *config.AppConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "track",
		Short: "Register the current branch in the git-zf store (for branches created with plain git checkout)",
		Long: `Register the current branch into the git-zf store without creating a new branch.

Use this when you checked out a branch with plain 'git checkout' instead of
'git zf issue start' or 'git zf review start'.

  Developer (feature branch):  git checkout -b X.2@feat@part-two origin/X.2@feat@part-two
                                git zf review track
                                git zf review request

  Reviewer (review branch):    git checkout -b X.1@review <sha>
                                git zf review track
                                git zf review approve`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			deps, err := buildReviewDeps(ctx, cmd, appConfig)
			if err != nil {
				return err
			}
			defer func() { _ = deps.store.Close() }()

			return runTrack(ctx, deps)
		},
	}
}

func runTrack(ctx context.Context, deps reviewDeps) error {
	currentBranch, err := deps.client.CurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	if strings.HasSuffix(currentBranch, "@review") {
		return runTrackReviewer(ctx, deps, currentBranch)
	}

	if b, parseErr := branch.Parse(currentBranch); parseErr == nil {
		return runTrackDeveloper(ctx, deps, currentBranch, b)
	}

	return fmt.Errorf(
		"current branch %q does not match a git-zf naming convention\n"+
			"(expected <IssueID>@<type>@<slug> or <IssueID>@review)",
		currentBranch)
}

// runTrackDeveloper registers a feature branch that was checked out with plain
// git into the git-zf store as an in_progress issue branch.
func runTrackDeveloper(ctx context.Context, deps reviewDeps, branchName string, b *branch.Branch) error {
	// Idempotency: check if already tracked.
	rows, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	for _, r := range rows {
		if r.BranchName == branchName {
			fmt.Fprintf(deps.client.IO().Out,
				"Branch %q is already tracked (status: %s).\n", branchName, r.Status)
			return nil
		}
	}

	// Derive a human-readable title from the slug (replace hyphens with spaces).
	title := strings.ReplaceAll(b.Title(), "-", " ")

	if err := deps.store.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: title, StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("register branch in store: %w", err)
	}

	// Warn if a review ref already exists for this issue (branch is locked).
	if ref, _, _ := deps.client.ReadReviewRef(ctx, b.IssueID()); ref != nil &&
		ref.Status == string(store.ReviewStatusInReview) {
		fmt.Fprintf(deps.client.IO().Err,
			"Note: branch %q is currently locked for review (round %d).\n"+
				"You cannot submit for review again until the reviewer decides.\n",
			branchName, ref.Round)
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q is now tracked (issue %s, type %s).\n"+
			"Run: git zf review request\n",
		branchName, b.IssueID(), b.Type())

	return nil
}

// runTrackReviewer registers a manually-created review branch in the git-zf
// store so the reviewer can run approve/reject without having used review start.
func runTrackReviewer(ctx context.Context, deps reviewDeps, branchName string) error {
	issueSlug := strings.TrimSuffix(branchName, "@review")

	// Fetch review refs best-effort so we see the developer's lock signal.
	if err := deps.client.FetchReviewRefs(ctx); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: fetch review refs: %v\n", err)
	}

	// Verify the review ref exists and is in_review.
	ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
		return fmt.Errorf(
			"no review found for issue %q — has the developer run `git zf review request`?",
			issueSlug)
	}
	if ref.Status != string(store.ReviewStatusInReview) {
		return fmt.Errorf(
			"issue %q is not awaiting review (current status: %s)", issueSlug, ref.Status)
	}

	// Resolve reviewer identity from git config.
	reviewer, _ := deps.client.ConfigUser(ctx)

	// Check existing store record.
	latest, err := deps.store.GetLatestReview(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("check review record: %w", err)
	}

	switch {
	case latest != nil && latest.Reviewer != "":
		// Already registered.
		fmt.Fprintf(deps.client.IO().Out,
			"Already registered as reviewer for issue %q (round %d).\n",
			issueSlug, latest.Round)
		return nil

	case latest != nil && latest.Reviewer == "":
		// Record exists but reviewer identity not yet captured.
		if reviewer != "" {
			_ = deps.store.UpdateReviewerIdentity(ctx, latest.ID, reviewer)
		}

	default:
		// No record at all — insert one.
		var insertErr error
		latest, insertErr = deps.store.InsertReview(ctx, issueSlug, reviewer)
		if insertErr != nil {
			return fmt.Errorf("register review record: %w", insertErr)
		}
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Branch %q registered as review branch for issue %q (round %d).\n"+
			"Run:\n"+
			"  git zf review approve\n"+
			"  git zf review reject\n",
		branchName, issueSlug, latest.Round)

	return nil
}
```

- [ ] **Step 4: Add missing imports to test file**

The test uses `git.ReviewRef`. Verify `"github.com/piprim/git-zf/git"` is imported in
`cmd/review/review_e2e_test.go` (it already is from the lease-correctness test). If not,
add it.

Also verify `"strings"` is in the import block — add it if missing.

- [ ] **Step 5: Run tests**

```
mise exec -- go test ./cmd/review/... -run "TestTrack_" -v
```
Expected: all subtests PASS.

- [ ] **Step 6: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

---

## Task 2: Wire `track` into both command groups

**Files:**
- Modify: `cmd/review/review.go` (add `TrackCmd` to `GetRootCmd`)
- Modify: `cmd/issue/issue.go` (import `cmd/review`, add `TrackCmd` to `GetRootCmd`)

**Interfaces consumed:** `TrackCmd(appConfig *config.AppConfig) *cobra.Command` from Task 1.

- [ ] **Step 1: Add to `cmd/review/review.go`**

In `GetRootCmd()`, add `TrackCmd(r.appConfig)` to the `cmd.AddCommand(...)` call:

```go
func (r Review) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage the code review lifecycle for an issue branch",
	}

	cmd.AddCommand(
		r.getRequestCmd(),
		r.getStartCmd(),
		r.getApproveCmd(),
		r.getRejectCmd(),
		r.getListCmd(),
		r.getStatusCmd(),
		r.getFetchCmd(),
		r.getSyncCmd(),
		r.getGuardCmd(),
		TrackCmd(r.appConfig),
	)

	return cmd
}
```

- [ ] **Step 2: Add to `cmd/issue/issue.go`**

Add the import and the command:

```go
import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/cmd/review"   // ← add this
	_ "github.com/piprim/git-zf/tracker/github"
	_ "github.com/piprim/git-zf/tracker/redmine"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)
```

In `GetRootCmd()`:

```go
cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd(), review.TrackCmd(i.appConfig))
```

- [ ] **Step 3: Build**

```
mise exec -- go build ./... 2>&1
```
Expected: no output (clean build).

- [ ] **Step 4: Verify CLI surface**

```
./bin/git-zf review --help | grep track
./bin/git-zf issue  --help | grep track
```
Expected:
```
  track       Register the current branch in the git-zf store ...
  track       Register the current branch in the git-zf store ...
```

- [ ] **Step 5: Run full suite**

```
mise exec -- go test ./...
```
Expected: PASS.

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|-----------------|------|
| `git zf review track` alias | Task 2 |
| `git zf issue track` alias | Task 2 |
| Auto-detect developer vs reviewer via branch name | Task 1 `runTrack` |
| Developer path: register as in_progress | Task 1 `runTrackDeveloper` |
| Developer path: idempotent when already tracked | Task 1 test |
| Developer path: title from slug | Task 1 `runTrackDeveloper` |
| Developer path: warn if branch locked for review | Task 1 `runTrackDeveloper` |
| Reviewer path: verify ref exists and is in_review | Task 1 `runTrackReviewer` |
| Reviewer path: record reviewer identity | Task 1 `runTrackReviewer` |
| Reviewer path: idempotent when already registered | Task 1 `runTrackReviewer` |
| Error on unrecognized branch name | Task 1 `runTrack` |

**No gaps found.**
