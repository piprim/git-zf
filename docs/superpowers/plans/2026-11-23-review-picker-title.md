# Review Picker Title Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The pickers of `review start`, `approve`, `reject` and `status` show the issue's title instead of its slug.

**Architecture:** `reviewBranches` (`cmd/review/deps.go`) builds the rows of those four pickers from the review chains, which hold no title. It now fetches and reads the branch chains once and takes the title from there.

**Tech Stack:** Go, existing packages `branch`, `review`, `branch/branchtest`, `review/reviewtest`.

**Spec:** `docs/superpowers/specs/2026-11-23-review-picker-title-design.md`

## Global Constraints

- Run Go through mise: `mise exec -- go ...`.
- Every assertion in a test sits in a named `t.Run`.
- Run the GitNexus impact analysis on a symbol before editing it; `detect-changes` before any commit.
- No commit: the user commits themselves.
- `tui.BranchPicker` and its `[slug] title (branch)` layout are not touched.
- No new op field, no tracker call.

## Review Focus

- A branch chain whose start op has no title: the row shows the title derived from the branch name, not the slug. (Test below.)
- A review whose issue has no branch chain in this clone: the row keeps the slug. (Test below.)
- `branch.List` fails: a warning on Err, the rows are still returned with the slug as title. (No test: no cheap way to make `List` fail; check by reading.)
- Remote unreachable: `fetchBranchRefs` already only warns; the picker must still open.
- An issue with several branches: the title is the chain's, the same for all; no duplicate row appears (rows stay one per review). (Test below.)
- No review passes `keep`: the branch chains are neither fetched nor listed, so "No issues currently awaiting review." costs no extra round-trip. (No test: check by reading.)
- `review status` keeps its own loop (`status_cmd.go:38-47`) and still shows the feature branch name for a tracked issue; this plan does not touch it.

---

### Task 1: Titles in the review rows

**Files:**
- Modify: `cmd/review/deps.go` (`reviewBranches`, around line 121)
- Test: `cmd/review/review_e2e_test.go`

**Interfaces:**
- Consumes: `branch.List(ctx, c) ([]branch.State, []string, error)` (its error is already prefixed `list branch refs:`), `branch.TitleFromName(name string) string`, `fetchBranchRefs(ctx, deps)`.
- Produces: `issueTitles(ctx context.Context, deps reviewDeps) map[string]string` in `cmd/review/deps.go`: issue slug to title.

- [ ] **Step 1: Impact analysis**

Run: `node .gitnexus/run.cjs impact "reviewBranches" --direction upstream --repo .`
Expected: callers `inReviewBranches` (start, approve, reject) and `status_cmd.go`. Report the risk; stop and warn on HIGH or CRITICAL.

- [ ] **Step 2: Write the failing test**

Append to `cmd/review/review_e2e_test.go`. The rig already seeds `77@feat@my-feature` with the title `my feature`.

```go
func TestInReviewBranches_Titles(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	branchtest.Seed(t, rig.client, branch.Op{Branch: "77@fix@second-branch"}, branch.StatusInProgress)
	branchtest.Seed(t, rig.client, branch.Op{Branch: "88@fix@empty-title"}, branch.StatusInProgress)
	for _, slug := range []string{"77", "88", "99"} {
		reviewtest.Seed(t, rig.client, slug, reviewpkg.StatusInReview, 1, "HEAD")
	}

	rows, err := inReviewBranches(t.Context(), rig.deps())
	if err != nil {
		t.Fatalf("inReviewBranches: %v", err)
	}
	titles := map[string]string{}
	for _, r := range rows {
		titles[r.IssueSlug] = r.Title
	}

	for _, tc := range []struct{ name, slug, want string }{
		{"title of the branch chain", "77", "my feature"},
		{"title derived from the branch name", "88", "empty title"},
		{"slug when the clone has no branch chain", "99", "99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := titles[tc.slug]; got != tc.want {
				t.Errorf("title of %s = %q, want %q", tc.slug, got, tc.want)
			}
		})
	}

	t.Run("one row per review", func(t *testing.T) {
		if len(rows) != 3 {
			t.Errorf("got %d rows, want 3", len(rows))
		}
	})
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `mise exec -- go test ./cmd/review/... -run "^TestInReviewBranches_Titles$" -v`
Expected: FAIL on the first two cases (`title of 77 = "77"`, `title of 88 = "88"`); the other two pass.

- [ ] **Step 4: Implement**

In `cmd/review/deps.go`:

- Add `issueTitles(ctx context.Context, deps reviewDeps) map[string]string`: `fetchBranchRefs`, then `branch.List`. On error print `warning: <err>` on `deps.client.IO().Err` and return an empty map. Print each of `List`'s warnings on Err, as `reviewBranches` does for `reviewpkg.List`. For each state the title is `st.Title`, else `branch.TitleFromName(st.Entries[0].Name)` when it has an entry; no `branch.Rows`.
- In `reviewBranches`, build the rows with the `keep` filter first, as today. Only when at least one row was kept, call `issueTitles` once and set each row's `Title` to `cmp.Or(titles[row.IssueSlug], row.IssueSlug)`.

- [ ] **Step 5: Run the tests**

Run: `mise exec -- go test ./cmd/review/... ./cmd/issue/... -run "^TestInReviewBranches_Titles$|^TestReview|^TestFullParallel|^TestTrack|^TestClose_SignedGate|^TestClose_ReviewPreflight" -v`
Expected: PASS.

Then: `mise exec -- go test ./...` and `mise exec -- go vet ./cmd/review/...`
Expected: all pass, no vet finding.

- [ ] **Step 6: Graph check**

Run: `node .gitnexus/run.cjs detect-changes --scope all --repo .`
Expected: only `reviewBranches`, `issueTitles` and the new test changed; not `partial` or `truncated`.

No commit: leave the tree for the user.
