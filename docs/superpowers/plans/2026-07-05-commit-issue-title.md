# Standard Commit Includes Issue Title — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `git zf commit` on an issue branch prefills the commit body with `# <issue title>`, the way `issue close` already carries the title.

**Architecture:** Approach A from the spec (`docs/superpowers/specs/2026-07-05-commit-issue-title-design.md`): `issueHintFromClient` stays a pure branch-name parser; `runE` in `cmd/commit/commit.go` enriches the hint with a store title lookup after the store opens. The rendering side (`prefillNotClosed` in `commit/form.go`) already emits `body = "# <IssueSubject>"` and is fully tested — only the data wiring is missing.

**Tech Stack:** Go (managed by mise — always `mise exec -- go …`), SQLite store (`store` package), existing test patterns from `cmd/commit/commit_merge_parent_test.go`.

## Global Constraints

- Run Go only via `mise exec -- go build|vet|test …` (repo CLAUDE.md).
- Every distinct test assertion/scenario is wrapped in a named `t.Run` subtest (user-level CLAUDE.md rule).
- **Never run `git add` or `git commit`** — at each commit step, stop and hand off to the user with the suggested message (standing user rule; overrides the usual plan workflow).
- Do NOT modify: the `Committer` interface, `issueHintFromClient`, anything in `commit/` (package), or the close flow (`cmd/issue/`). Spec's out-of-scope list applies.
- A missing/failed title lookup must never block a commit: helper returns `""`, logs at `slog.Debug` only.

---

### Task 1: `issueTitleFromStore` helper (TDD)

**Files:**
- Create: `cmd/commit/commit_title_test.go`
- Modify: `cmd/commit/commit.go` (append the helper after `issueHintFromClient`, ~line 210; `context`, `log/slog`, and `store` are already imported)

**Interfaces:**
- Consumes: `store.Open(ctx, dir)`, `(*store.Store).InsertIssueWithBranch`, `(*store.Store).ListBranchesByIssueSlugs(ctx, []string) (map[string]store.BranchRow, error)` — map is keyed by issue slug; `BranchRow.Title` holds the issue title.
- Produces: `func issueTitleFromStore(ctx context.Context, s *store.Store, slug string) string` — Task 2 calls exactly this.

- [ ] **Step 1: Write the failing test**

Create `cmd/commit/commit_title_test.go` with exactly:

```go
package commit

import (
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestIssueTitleFromStore(t *testing.T) {
	// Not parallel: store.Open on disk; each subtest builds its own dir.

	newStore := func(t *testing.T) *store.Store {
		t.Helper()

		s, err := store.Open(t.Context(), t.TempDir())
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })

		return s
	}

	t.Run("returns the stored title for a known slug", func(t *testing.T) {
		s := newStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&store.Issue{IDSlug: "ABC-1", Title: "Add OAuth login", StatusID: store.StatusIDInProgress},
			&store.Branch{Name: "ABC-1@feat@add-oauth-login", Type: "feat", StatusID: store.StatusIDInProgress},
		); err != nil {
			t.Fatalf("seed: %v", err)
		}

		if got := issueTitleFromStore(t.Context(), s, "ABC-1"); got != "Add OAuth login" {
			t.Errorf("issueTitleFromStore(ABC-1) = %q, want %q", got, "Add OAuth login")
		}
	})

	t.Run("returns empty for a slug with no row", func(t *testing.T) {
		s := newStore(t)
		if got := issueTitleFromStore(t.Context(), s, "NOPE-9"); got != "" {
			t.Errorf("issueTitleFromStore(NOPE-9) = %q, want empty", got)
		}
	})

	t.Run("returns empty for an empty slug", func(t *testing.T) {
		s := newStore(t)
		if got := issueTitleFromStore(t.Context(), s, ""); got != "" {
			t.Errorf(`issueTitleFromStore("") = %q, want empty`, got)
		}
	})

	t.Run("returns empty when the lookup fails", func(t *testing.T) {
		s := newStore(t)
		_ = s.Close() // force ListBranchesByIssueSlugs to return an error

		if got := issueTitleFromStore(t.Context(), s, "ABC-1"); got != "" {
			t.Errorf("issueTitleFromStore on closed store = %q, want empty", got)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/commit/ -run "^TestIssueTitleFromStore$" -v`
Expected: FAIL to build with `undefined: issueTitleFromStore`

- [ ] **Step 3: Write minimal implementation**

Append to `cmd/commit/commit.go`, directly after `issueHintFromClient` (after its closing brace, ~line 210):

```go
// issueTitleFromStore returns the stored issue title for slug, or "" when
// slug is empty, no row matches, or the lookup fails — a missing title only
// skips the body prefill and must never block a commit.
func issueTitleFromStore(ctx context.Context, s *store.Store, slug string) string {
	if slug == "" {
		return ""
	}

	rows, err := s.ListBranchesByIssueSlugs(ctx, []string{slug})
	if err != nil {
		slog.Debug("could not look up issue title", "slug", slug, "error", err)

		return ""
	}

	return rows[slug].Title // missing key → zero row → ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/commit/ -run "^TestIssueTitleFromStore$" -v`
Expected: PASS (4 subtests)

- [ ] **Step 5: Hand off for commit (do NOT run git yourself)**

Tell the user the task is done and suggest:
`feat(commit): add store lookup for the current issue's title`
Files: `cmd/commit/commit.go`, `cmd/commit/commit_title_test.go`

---

### Task 2: Wire the title into the commit prefill

**Files:**
- Modify: `cmd/commit/commit.go:111-119` (`runE`)

**Interfaces:**
- Consumes: `issueTitleFromStore(ctx context.Context, s *store.Store, slug string) string` from Task 1; `IssueHint.IssueSubject` field (exists in `commit/form.go`).
- Produces: nothing new — end-user behavior only.

- [ ] **Step 1: Add the enrichment line in `runE`**

In `cmd/commit/commit.go`, `runE` currently reads:

```go
	hint := issueHintFromClient(client)

	s, err := store.OpenRepo(cmd.Context())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	prefill := hint.Prefill(c.appConfig.CommitMessage)
```

Change it to (one new line + blank line before `prefill`):

```go
	hint := issueHintFromClient(client)

	s, err := store.OpenRepo(cmd.Context())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	hint.IssueSubject = issueTitleFromStore(cmd.Context(), s, hint.IssueID)

	prefill := hint.Prefill(c.appConfig.CommitMessage)
```

- [ ] **Step 2: Build and vet**

Run: `mise exec -- go build ./... && mise exec -- go vet ./...`
Expected: both exit 0, no output

- [ ] **Step 3: Run the full test suite**

Run: `mise exec -- go test ./...`
Expected: all packages `ok` (the prefill rendering is already covered by `commit/form_test.go` rows `body_gets_issue_subject_when_present` and `body_gets_subject_then_ref_when_only_body_present`)

- [ ] **Step 4: Hand off for commit (do NOT run git yourself)**

Tell the user the task is done and suggest:
`feat(commit): prefill the commit body with the issue title`
Files: `cmd/commit/commit.go`

Optional manual check for the user (needs a TTY): on a git-zf issue branch with a seeded store, run `./bin/git-zf commit` and confirm the body field shows `# <issue title>`.
