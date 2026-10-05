# Review Chains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store each review as a chain of commits under `refs/zf/reviews/<slug>` (one commit per action, state = fold), sign approvals, and let `issue close` refuse an approval that does not verify.

**Architecture:** The commit-chain plumbing that issues already use (`git/issue_ref.go`, `git/issue_ref_sync.go`) is generalised to take a `ChainRefs` namespace, and its tracking refs move to `refs/remotes/<remote>/zf/<name>/`. A new `review/` package mirrors `issue/`: a pure fold plus thin glue. The review commands, the guards and `issue close` switch from the JSON blob to that package. A config flag adds the signing gate.

**Tech Stack:** Go 1.25 (run through `mise exec -- go …`), git plumbing via `os/exec` (`commit-tree`, `update-ref`, `for-each-ref`, `rev-list`, `cat-file --batch`, `verify-commit`), cobra, SQLite store (unchanged), `ssh-keygen` in tests only.

**Spec:** `docs/superpowers/specs/2026-10-04-review-chains-design.md`

## Global Constraints

- Run Go only through mise: `mise exec -- go test ./...`, `mise exec -- go build -o ./bin/git-zf .`.
- Every test assertion or scenario sits in a named `t.Run` (user rule). Shared setup runs once above the subtests.
- Project rule (CLAUDE.md, GitNexus): before editing any existing function, method or type, run `impact({target: "<symbol>", direction: "upstream"})` and report the blast radius; warn the user before proceeding on HIGH or CRITICAL. Rename symbols with the GitNexus `rename` tool, never with find-and-replace. Run `detect_changes()` before every commit.
- Commit steps: commit only if the user has allowed commits in this session. Otherwise leave the work uncommitted and say so in the task report.
- Local refs: `refs/zf/reviews/<slug>`, `refs/zf/issues/<id>`. Tracking refs: `refs/remotes/<remote>/zf/reviews/<slug>`, `refs/remotes/<remote>/zf/issues/<id>`. `<remote>` is `Client.Remote()`, never a literal `origin` in production code.
- Op envelope: `v` (1), `type`, `at` (RFC 3339, UTC), `author`. File name in the commit tree: `op.json`.
- Review op types: `request`, `start`, `approve`, `reject`, `close`, `merge`. Unknown types and versions are skipped, never an error.
- Status strings are unchanged: `in_review`, `approved`, `changes_requested`.
- No review ref is ever deleted, with one exception: `review request` replaces a legacy blob ref.
- Outside the `review` package itself, import it as `reviewpkg "github.com/piprim/git-zf/review"` (the same convention as `issuepkg`).
- Config key: `[review] require-signed`, default `false`.
- The SQLite `reviews` table and `git/blob_ref.go` stay. Branch refs are not touched.
- Match the surrounding code: comment density, error wrapping with `%w`, blank line before `return`.

### Deviations from the spec's function table

The spec lists `Fetch(ctx, c) error`. This plan uses `Fetch(ctx, c, silent bool) error`: the pre-push guard needs a fetch that prints nothing, as `FetchReviewRef` does today. The plan also adds helpers the spec implies but does not name: `git.ChainRefKind`, `git.DeleteChainRef`, `git.UnpushedChainIDs`, `git.ConfigureChainFetch`, `review.ReplaceLegacy`, `review.SignatureState`, `issueflow.ReviewBranchAhead`, and the test packages `review/reviewtest` and `internal/gittest`.

## Review Focus

Conditions the spec implies but does not spell out, most likely first. Each has a test in the task that owns the code.

1. A reviewer acts on a clone that never loaded the review (local ref absent, the remote has the chain): the op must join the existing chain, not start a second root. Test in Task 4 (`TestSync_EdgeCases`).
2. The push of an op fails (remote unreachable): the op must stay local and go out with the next `Sync`. Test in Task 4.
3. The user runs a plain `git fetch --prune` on a clone that has not run `git zf init`, which deletes the tracking refs: the next `Sync` must restore them with no merge commit and no lost op. Test in Task 4.
4. A repository with no remote at all: every review call is a no-op on the network side and the commands still work. Tests in Task 2 and Task 4.
5. A commit in the chain whose `op.json` is not valid JSON (written by hand or by a broken tool): it is skipped with a warning; the review still loads. Test in Task 4.

## File Structure

| File | Responsibility |
|---|---|
| `internal/chain/chain.go` (new) | `Order`: the deterministic ordering of ops shared by both folds. `ParseAt`. |
| `git/chain_ref.go` (renamed from `git/issue_ref.go`) | Local chain plumbing for any `ChainRefs` namespace: write commit, root, append, tip, list, read, kind, delete. |
| `git/chain_ref_sync.go` (renamed from `git/issue_ref_sync.go`) | Fetch into the tracking namespace, reconcile, pushed check, push, unpushed list, fetch refspec configuration. |
| `git/commit_sig.go` (new, Task 7) | `VerifyCommit`, `CommitSigned`. |
| `git/review_ref.go` | Deleted in Task 6. |
| `issue/record.go`, `issue/repo.go` | Use `chain.Order` and `git.IssueRefs`. |
| `review/record.go` (new) | `Op`, `State`, `Approval`, `DecodeOp`, `Fold`. Pure. |
| `review/repo.go` (new) | `Load`, `List`, `Append`, `Fetch`, `Push`, `Sync`, `ReplaceLegacy`, `SignatureState`, `ErrLegacyReview`. |
| `review/reviewtest/reviewtest.go` (new) | `Seed`: bring a review to a status in tests of other packages. |
| `internal/gittest/gittest.go` (new, Task 7) | `SSHSigner`: make a test repo sign and verify with a fresh SSH key. |
| `cmd/review/*.go`, `cmd/issueflow/review_guard.go`, `cmd/issue/close.go` | Read and write through `reviewpkg`. |
| `cmd/init/init.go` | Calls `ConfigureChainFetch`. |
| `config/config.go`, `config/default.toml` | `[review] require-signed`. |
| `docs/review-refs.md` (new), `docs/issue-refs.md`, `README.md`, `CLAUDE.md` | Documentation. |

---

### Task 1: `internal/chain` — the shared ordering

**Files:**
- Create: `internal/chain/chain.go`
- Create: `internal/chain/chain_test.go`
- Modify: `issue/record.go` (`Fold`, remove `linearize` and `parseAt`)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type chain.Node struct { ID string; Parents []string; At string }`
  - `func chain.Order[T any](items []T, node func(*T) Node) []T`
  - `func chain.ParseAt(s string) time.Time`

- [ ] **Step 1: Write the failing test**

Create `internal/chain/chain_test.go`:

```go
package chain

import (
	"slices"
	"testing"
)

type item struct {
	id, at  string
	parents []string
}

func node(i *item) Node { return Node{ID: i.id, Parents: i.parents, At: i.at} }

func order(items ...item) []string {
	ids := []string{}
	for _, i := range Order(items, node) {
		ids = append(ids, i.id)
	}

	return ids
}

func TestOrder(t *testing.T) {
	t.Parallel()

	root := item{id: "a", at: "2026-10-01T10:00:00Z"}
	x := item{id: "x", at: "2026-10-01T12:00:00Z", parents: []string{"a"}}
	y := item{id: "y", at: "2026-10-01T11:00:00Z", parents: []string{"a"}}

	t.Run("a child comes after its parent whatever the input order", func(t *testing.T) {
		t.Parallel()

		child := item{id: "b", at: "2026-10-01T09:00:00Z", parents: []string{"a"}}
		if got := order(child, root); !slices.Equal(got, []string{"a", "b"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("concurrent items are ordered by At then ID", func(t *testing.T) {
		t.Parallel()

		c := item{id: "c", at: "2026-10-01T12:00:00Z", parents: []string{"a"}}
		if got := order(x, y, c, root); !slices.Equal(got, []string{"a", "y", "c", "x"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a merge comes after both of its parents even when dated earlier", func(t *testing.T) {
		t.Parallel()

		m := item{id: "0", at: "2026-10-01T08:00:00Z", parents: []string{"x", "y"}}
		if got := order(m, x, y, root); !slices.Equal(got, []string{"a", "y", "x", "0"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a parent outside the slice is ignored", func(t *testing.T) {
		t.Parallel()

		orphan := item{id: "o", at: "2026-10-01T10:00:00Z", parents: []string{"missing"}}
		if got := order(orphan); !slices.Equal(got, []string{"o"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("a malformed At sorts before a valid one", func(t *testing.T) {
		t.Parallel()

		bad := item{id: "z", at: "yesterday", parents: []string{"a"}}
		if got := order(y, bad, root); !slices.Equal(got, []string{"a", "z", "y"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("an empty slice orders to an empty slice", func(t *testing.T) {
		t.Parallel()

		if got := order(); len(got) != 0 {
			t.Errorf("order = %v", got)
		}
	})
}

func TestParseAt(t *testing.T) {
	t.Parallel()

	t.Run("a valid RFC 3339 time is returned in UTC", func(t *testing.T) {
		t.Parallel()

		got := ParseAt("2026-10-01T12:00:00+02:00")
		if got.IsZero() || got.Hour() != 10 {
			t.Errorf("ParseAt = %v", got)
		}
	})

	t.Run("garbage is the zero time", func(t *testing.T) {
		t.Parallel()

		if got := ParseAt("yesterday"); !got.IsZero() {
			t.Errorf("ParseAt = %v", got)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/chain/... -v`
Expected: FAIL, build error `undefined: Order`, `undefined: Node`, `undefined: ParseAt`.

- [ ] **Step 3: Write the implementation**

Create `internal/chain/chain.go`:

```go
// Package chain orders the ops of a commit chain (refs/zf/issues/*,
// refs/zf/reviews/*) the same way on every clone.
package chain

import (
	"cmp"
	"slices"
	"time"
)

// Node is what Order needs to know about one op: its commit, the commits it
// was written on top of, and when it was written (RFC 3339).
type Node struct {
	ID      string
	Parents []string
	At      string
}

// ParseAt parses an op timestamp. A malformed one is the zero time.
func ParseAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// Order returns items so that every item comes after all of its parents. Items
// with no ordering between them (written concurrently on two clones) are
// ordered by At, then by ID, so every clone computes the same sequence. The
// order of items in the input does not matter. A parent that is not in items is
// ignored.
func Order[T any](items []T, node func(*T) Node) []T {
	nodes := make([]Node, len(items))
	index := make(map[string]int, len(items))
	for i := range items {
		nodes[i] = node(&items[i])
		index[nodes[i].ID] = i
	}

	pending := make([]int, len(items))
	children := make([][]int, len(items))
	for i, n := range nodes {
		for _, p := range n.Parents {
			pi, known := index[p]
			if !known {
				continue
			}
			pending[i]++
			children[pi] = append(children[pi], i)
		}
	}

	var ready []int
	for i := range nodes {
		if pending[i] == 0 {
			ready = append(ready, i)
		}
	}

	out := make([]T, 0, len(items))
	for len(ready) > 0 {
		// ponytail: re-sorts the ready set at every step, O(n² log n) worst
		// case; switch to container/heap if a chain reaches thousands of ops.
		slices.SortFunc(ready, func(a, b int) int {
			if c := ParseAt(nodes[a].At).Compare(ParseAt(nodes[b].At)); c != 0 {
				return c
			}

			return cmp.Compare(nodes[a].ID, nodes[b].ID)
		})

		next := ready[0]
		ready = ready[1:]
		out = append(out, items[next])

		for _, child := range children[next] {
			pending[child]--
			if pending[child] == 0 {
				ready = append(ready, child)
			}
		}
	}

	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./internal/chain/... -v`
Expected: PASS, 8 subtests.

- [ ] **Step 5: Make `issue.Fold` use it**

Run `impact({target: "linearize", direction: "upstream"})` and `impact({target: "parseAt", direction: "upstream"})` first and report the result.

In `issue/record.go`:

1. Add the import `"github.com/piprim/git-zf/internal/chain"`; remove `"cmp"` (it is only used by `linearize`).
2. In `Fold`, replace `ordered := linearize(ops)` with:

```go
	ordered := chain.Order(ops, func(op *Op) chain.Node {
		return chain.Node{ID: op.ID, Parents: op.Parents, At: op.At}
	})
```

3. Delete the functions `parseAt` and `linearize` entirely.
4. Replace every remaining `parseAt(` in package `issue` with `chain.ParseAt(`. Find them with `grep -rn 'parseAt(' issue/`; add the `chain` import to any other file of the package that used it.

- [ ] **Step 6: Run the issue tests**

Run: `mise exec -- go test ./issue/... ./internal/chain/... -v -run "TestFold|TestOrder|TestParseAt|TestDecodeOp"`
Expected: PASS. The `TestFold` subtests "slice order does not matter", "concurrent ops are ordered by At then ID" and "a causally later op beats a future-dated one" still pass: they now exercise `chain.Order` through `Fold`.

Run: `mise exec -- go build ./... && mise exec -- go vet ./issue/... ./internal/chain/...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/chain issue/record.go
git commit -m "refactor: share the op ordering between chain folds"
```

---

### Task 2: chain plumbing with namespaces and git-native tracking refs

**Files:**
- Rename: `git/issue_ref.go` → `git/chain_ref.go`
- Rename: `git/issue_ref_sync.go` → `git/chain_ref_sync.go`
- Modify: `git/issue_ref_test.go`, `git/issue_ref_sync_test.go` (call sites)
- Create: `git/chain_ref_test.go`
- Modify: `issue/repo.go`, `issue/repo_test.go` (call sites)

**Interfaces:**
- Consumes: nothing from Task 1.
- Produces (all in package `git`):
  - `type ChainRefs struct{ … }`, `var IssueRefs`, `var ReviewRefs`, `func (n ChainRefs) FetchRefspec(remote string) string`
  - `const ChainAbsent = ""`, `ChainCommits = "chain"`, `ChainLegacy = "legacy"`
  - `type ChainCommit struct { ID string; Parents []string; Payload []byte }` (was `IssueCommit`)
  - `func (c *Client) WriteChainRoot(ctx context.Context, payload []byte, message string, sign bool) (string, error)`
  - `func (c *Client) PublishChainRoot(ctx context.Context, ns ChainRefs, id, commit string) error`
  - `func (c *Client) AppendChainCommit(ctx context.Context, ns ChainRefs, id string, payload []byte, message string, sign bool) (string, error)`
  - `func (c *Client) ChainTip(ctx context.Context, ns ChainRefs, id string) (string, error)`
  - `func (c *Client) ListChainIDs(ctx context.Context, ns ChainRefs) ([]string, error)`
  - `func (c *Client) ReadChainCommits(ctx context.Context, ns ChainRefs, id string) ([]ChainCommit, error)`
  - `func (c *Client) ChainRefKind(ctx context.Context, ns ChainRefs, id string) (string, error)`
  - `func (c *Client) DeleteChainRef(ctx context.Context, ns ChainRefs, id string) error`
  - `func (c *Client) FetchChainRefs(ctx context.Context, ns ChainRefs, silent bool) error`
  - `func (c *Client) ReconcileChainRefs(ctx context.Context, ns ChainRefs, mergePayload []byte) (int, error)`
  - `func (c *Client) ChainRefPushed(ctx context.Context, ns ChainRefs, id string) (bool, error)`
  - `func (c *Client) PushChainRef(ctx context.Context, ns ChainRefs, id string) error`
  - `func (c *Client) UnpushedChainIDs(ctx context.Context, ns ChainRefs) ([]string, error)`
  - `ErrIssueNotFound` and `ErrIssueRefCorrupt` keep their names and messages.

- [ ] **Step 1: Impact analysis**

Run `impact({target: "<name>", direction: "upstream"})` for each of: `WriteIssueRoot`, `PublishIssueRoot`, `AppendIssueCommit`, `IssueTip`, `ListIssueIDs`, `ReadIssueCommits`, `FetchIssueRefs`, `ReconcileIssueRefs`, `IssueRefPushed`, `PushIssueRef`, `IssueCommit`. Report the blast radius. Expected callers: `issue/repo.go` and the tests of `git/` and `issue/` only.

- [ ] **Step 2: Write the failing tests**

Create `git/chain_ref_test.go`:

```go
package git

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// originRefs lists the ref names the bare origin has under prefix.
func originRefs(t *testing.T, originDir, prefix string) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"for-each-ref", "--format=%(refname)", prefix).Output()
	if err != nil {
		t.Fatalf("for-each-ref on origin: %v", err)
	}

	return strings.Fields(string(out))
}

func TestChainRef_ReviewNamespace(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	root, err := client.WriteChainRoot(ctx, []byte(`{"type":"request"}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := client.PublishChainRoot(ctx, ReviewRefs, "ABC-1", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}
	tip, err := client.AppendChainCommit(ctx, ReviewRefs, "ABC-1", []byte(`{"type":"approve"}`), "approve", false)
	if err != nil {
		t.Fatalf("AppendChainCommit: %v", err)
	}

	t.Run("the ref is named after the slug and points at the tip", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "refs/zf/reviews/ABC-1").Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != tip {
			t.Errorf("ref points at %q, want %q", got, tip)
		}
	})

	t.Run("ReadChainCommits does not require the name to be the root commit", func(t *testing.T) {
		commits, err := client.ReadChainCommits(ctx, ReviewRefs, "ABC-1")
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		if len(commits) != 2 || commits[0].ID != root || commits[1].ID != tip {
			t.Errorf("commits = %+v", commits)
		}
	})

	t.Run("ListChainIDs lists only its own namespace", func(t *testing.T) {
		reviews, err := client.ListChainIDs(ctx, ReviewRefs)
		if err != nil || !slices.Equal(reviews, []string{"ABC-1"}) {
			t.Errorf("reviews = %v, %v", reviews, err)
		}
		issues, err := client.ListChainIDs(ctx, IssueRefs)
		if err != nil || len(issues) != 0 {
			t.Errorf("issues = %v, %v", issues, err)
		}
	})

	t.Run("publishing a root over an existing chain fails", func(t *testing.T) {
		if err := client.PublishChainRoot(ctx, ReviewRefs, "ABC-1", root); err == nil {
			t.Error("PublishChainRoot succeeded on an existing ref")
		}
	})
}

func TestChainRef_TrackingNamespace(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, bobDir := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}

	t.Run("push records the tip under refs/remotes/origin/zf/issues/", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", aliceDir, "rev-parse", "refs/remotes/origin/zf/issues/"+id).Output()
		if err != nil || strings.TrimSpace(string(out)) != id {
			t.Errorf("tracking ref = %q, %v; want %q", out, err, id)
		}
	})

	t.Run("fetch writes the tracking ref and no local ref", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", bobDir, "rev-parse", "refs/remotes/origin/zf/issues/"+id).Output()
		if err != nil || strings.TrimSpace(string(out)) != id {
			t.Errorf("tracking ref = %q, %v; want %q", out, err, id)
		}
		if tip, _ := bob.ChainTip(ctx, IssueRefs, id); tip != "" {
			t.Errorf("fetch created the local ref: %q", tip)
		}
	})

	t.Run("nothing is written under the old refs/zf/remote/ prefix", func(t *testing.T) {
		for _, c := range []*Client{alice, bob} {
			out, err := c.output(ctx, "for-each-ref", "refs/zf/remote/")
			if err != nil || out != "" {
				t.Errorf("refs/zf/remote/ = %q, %v", out, err)
			}
		}
	})

	t.Run("FetchRefspec maps the local namespace to the tracking one", func(t *testing.T) {
		want := "+refs/zf/reviews/*:refs/remotes/up/zf/reviews/*"
		if got := ReviewRefs.FetchRefspec("up"); got != want {
			t.Errorf("FetchRefspec = %q, want %q", got, want)
		}
	})
}

func TestChainRefKind(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	kind := func(c *Client, id string) string {
		t.Helper()
		k, err := c.ChainRefKind(ctx, ReviewRefs, id)
		if err != nil {
			t.Fatalf("ChainRefKind(%s): %v", id, err)
		}

		return k
	}

	t.Run("no ref is ChainAbsent", func(t *testing.T) {
		if got := kind(alice, "none"); got != ChainAbsent {
			t.Errorf("kind = %q", got)
		}
	})

	root, err := alice.WriteChainRoot(ctx, []byte(`{}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := alice.PublishChainRoot(ctx, ReviewRefs, "42", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	t.Run("a commit ref is ChainCommits", func(t *testing.T) {
		if got := kind(alice, "42"); got != ChainCommits {
			t.Errorf("kind = %q", got)
		}
	})

	blob, err := alice.outputStdin(ctx, []byte(`{"status":"in_review"}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/reviews/old", blob)

	t.Run("a local blob ref is ChainLegacy", func(t *testing.T) {
		if got := kind(alice, "old"); got != ChainLegacy {
			t.Errorf("kind = %q", got)
		}
	})

	mustGit(t, aliceDir, "push", "-q", "origin", "refs/zf/reviews/old")
	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}

	t.Run("a blob known only through the tracking ref is ChainLegacy", func(t *testing.T) {
		if got := kind(bob, "old"); got != ChainLegacy {
			t.Errorf("kind = %q", got)
		}
	})
}

func TestChainRef_LegacyBlobs(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, bobDir := cloneOf(t, originDir, "bob")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	blob, err := alice.outputStdin(ctx, []byte(`{"status":"in_review"}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/reviews/old", blob)
	mustGit(t, aliceDir, "push", "-q", "origin", "refs/zf/reviews/old")

	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	merged, err := bob.ReconcileChainRefs(ctx, ReviewRefs, merge)

	t.Run("reconcile skips a blob tracking ref", func(t *testing.T) {
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileChainRefs = %d, %v", merged, err)
		}
		if tip, _ := bob.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("reconcile imported the blob: %q", tip)
		}
	})

	// bob still holds the blob locally, as a clone upgraded in place does.
	mustGit(t, bobDir, "update-ref", "refs/zf/reviews/old", blob)

	if err := alice.DeleteChainRef(ctx, ReviewRefs, "old"); err != nil {
		t.Fatalf("DeleteChainRef: %v", err)
	}

	t.Run("DeleteChainRef removes the ref locally and on the remote", func(t *testing.T) {
		if tip, _ := alice.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("local ref survives: %q", tip)
		}
		if refs := originRefs(t, originDir, "refs/zf/reviews/"); len(refs) != 0 {
			t.Errorf("origin still has %v", refs)
		}
	})

	t.Run("DeleteChainRef of a missing ref is not an error", func(t *testing.T) {
		if err := alice.DeleteChainRef(ctx, ReviewRefs, "never-existed"); err != nil {
			t.Errorf("DeleteChainRef: %v", err)
		}
	})

	root, err := alice.WriteChainRoot(ctx, []byte(`{"type":"request"}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := alice.PublishChainRoot(ctx, ReviewRefs, "old", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}
	if err := alice.PushChainRef(ctx, ReviewRefs, "old"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	_, err = bob.ReconcileChainRefs(ctx, ReviewRefs, merge)

	t.Run("reconcile replaces a local blob with the remote chain", func(t *testing.T) {
		if err != nil {
			t.Fatalf("ReconcileChainRefs: %v", err)
		}
		if tip, _ := bob.ChainTip(ctx, ReviewRefs, "old"); tip != root {
			t.Errorf("bob tip = %q, want %q", tip, root)
		}
	})
}

func TestChainRef_UnpushedAndNoRemote(t *testing.T) {
	t.Parallel()

	alice, _, _ := newDiskRepoWithOrigin(t)
	solo, _ := newDiskRepo(t)
	ctx := t.Context()

	for _, id := range []string{"10", "42"} {
		root, err := alice.WriteChainRoot(ctx, []byte(`{}`), "request", false)
		if err != nil {
			t.Fatalf("WriteChainRoot: %v", err)
		}
		if err := alice.PublishChainRoot(ctx, ReviewRefs, id, root); err != nil {
			t.Fatalf("PublishChainRoot: %v", err)
		}
	}
	if err := alice.PushChainRef(ctx, ReviewRefs, "10"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	t.Run("UnpushedChainIDs lists the chains the remote lacks", func(t *testing.T) {
		ids, err := alice.UnpushedChainIDs(ctx, ReviewRefs)
		if err != nil || !slices.Equal(ids, []string{"42"}) {
			t.Errorf("UnpushedChainIDs = %v, %v", ids, err)
		}
	})

	root, err := solo.WriteChainRoot(ctx, []byte(`{}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := solo.PublishChainRoot(ctx, ReviewRefs, "42", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	t.Run("without a remote, fetch, reconcile and push are no-ops", func(t *testing.T) {
		if err := solo.FetchChainRefs(ctx, ReviewRefs, true); err != nil {
			t.Errorf("FetchChainRefs: %v", err)
		}
		if n, err := solo.ReconcileChainRefs(ctx, ReviewRefs, []byte(`{}`)); err != nil || n != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v", n, err)
		}
		if err := solo.PushChainRef(ctx, ReviewRefs, "42"); err != nil {
			t.Errorf("PushChainRef: %v", err)
		}
	})

	t.Run("without a remote, nothing is unpushed and the kind is still read", func(t *testing.T) {
		if ids, err := solo.UnpushedChainIDs(ctx, ReviewRefs); err != nil || len(ids) != 0 {
			t.Errorf("UnpushedChainIDs = %v, %v", ids, err)
		}
		if k, err := solo.ChainRefKind(ctx, ReviewRefs, "42"); err != nil || k != ChainCommits {
			t.Errorf("ChainRefKind = %q, %v", k, err)
		}
	})
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./git/... -run "TestChainRef" -v`
Expected: FAIL, build errors such as `undefined: ReviewRefs`, `client.WriteChainRoot undefined`.

- [ ] **Step 4: Rename the files and write `git/chain_ref.go`**

```bash
git mv git/issue_ref.go git/chain_ref.go
git mv git/issue_ref_sync.go git/chain_ref_sync.go
```

Use the GitNexus `rename` tool for each symbol in this table (it updates every caller), then change the signatures and bodies as shown below.

| Old | New |
|---|---|
| `IssueCommit` | `ChainCommit` |
| `writeIssueCommit` | `writeChainCommit` |
| `WriteIssueRoot` | `WriteChainRoot` |
| `PublishIssueRoot` | `PublishChainRoot` |
| `AppendIssueCommit` | `AppendChainCommit` |
| `IssueTip` | `ChainTip` |
| `ListIssueIDs` | `ListChainIDs` |
| `ReadIssueCommits` | `ReadChainCommits` |
| `FetchIssueRefs` | `FetchChainRefs` |
| `ReconcileIssueRefs` | `ReconcileChainRefs` |
| `reconcileIssueRef` | `reconcileChainRef` |
| `IssueRefPushed` | `ChainRefPushed` |
| `PushIssueRef` | `PushChainRef` |

Replace the content of `git/chain_ref.go` from the top of the file down to (not including) `func readBatchPayloads` with the following. Keep `readBatchPayloads` as it is; the rename already changed its parameter type to `[]ChainCommit`.

```go
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const chainOpFile = "op.json"

// ChainRefs names one family of commit chains: the local refs
// refs/zf/<name>/<id> and, for each remote, the tracking refs
// refs/remotes/<remote>/zf/<name>/<id>.
type ChainRefs struct {
	name string
	// idIsRoot is true when a chain's ID is the object ID of its root commit.
	idIsRoot bool
}

var (
	// IssueRefs are the issue chains. An issue's ID is its root commit.
	IssueRefs = ChainRefs{name: "issues", idIsRoot: true}
	// ReviewRefs are the review chains. A review's ID is its issue slug.
	ReviewRefs = ChainRefs{name: "reviews"}
)

func (n ChainRefs) prefix() string { return "refs/zf/" + n.name + "/" }

func (n ChainRefs) trackingPrefix(remote string) string {
	return "refs/remotes/" + remote + "/zf/" + n.name + "/"
}

// FetchRefspec is the refspec that fetches the chains of remote into their
// tracking namespace. Local chain refs are never the destination of a fetch.
func (n ChainRefs) FetchRefspec(remote string) string {
	return "+" + n.prefix() + "*:" + n.trackingPrefix(remote) + "*"
}

// What ChainRefKind finds under a chain's name.
const (
	ChainAbsent  = ""       // no ref
	ChainCommits = "chain"  // a commit chain
	ChainLegacy  = "legacy" // a non-commit object, written by an older git-zf
)

// ErrIssueRefCorrupt is returned by ReadChainCommits when a ref of a family
// whose IDs are root commits does not name a root of its chain.
var ErrIssueRefCorrupt = errors.New("issue ref does not name a root of its chain")

// ErrIssueNotFound is returned when the chain ref does not exist.
var ErrIssueNotFound = errors.New("issue not found")

// ChainCommit is one commit of a chain: its ID, its parents and the raw
// content of its op.json (nil when the commit has no such file).
type ChainCommit struct {
	ID      string
	Parents []string
	Payload []byte
}

// outputStdin is output with stdin fed to the command.
func (c *Client) outputStdin(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := c.gitCmd(ctx, args...)
	cmd.Stdin = bytes.NewReader(stdin)

	out, err := cmd.Output()
	if err != nil {
		return "", gitStderr(err)
	}

	return strings.TrimSpace(string(out)), nil
}

// writeChainCommit stores payload as op.json in a new commit with the given
// parents and returns the commit ID. It does not move any ref. The commit is
// signed when sign is true or when commit.gpgsign is true: `git commit-tree`
// ignores that setting on its own.
func (c *Client) writeChainCommit(
	ctx context.Context, payload []byte, message string, sign bool, parents ...string,
) (string, error) {
	blob, err := c.outputStdin(ctx, payload, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}

	tree, err := c.outputStdin(ctx, []byte("100644 blob "+blob+"\t"+chainOpFile+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	if !sign {
		configured, _ := c.output(ctx, "config", "--bool", "commit.gpgsign")
		sign = configured == "true"
	}
	if sign {
		args = append(args, "-S")
	}

	commit, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}

	return commit, nil
}

// WriteChainRoot writes payload as the root commit of a new chain and returns
// its ID. No ref is created: the chain does not exist for any command until
// PublishChainRoot is called, and an unpublished root is ordinary garbage for
// git.
func (c *Client) WriteChainRoot(ctx context.Context, payload []byte, message string, sign bool) (string, error) {
	return c.writeChainCommit(ctx, payload, message, sign)
}

// PublishChainRoot creates the ref of chain id pointing at commit, a root
// written by WriteChainRoot. It fails when the ref already exists.
func (c *Client) PublishChainRoot(ctx context.Context, ns ChainRefs, id, commit string) error {
	// The zero old-value makes update-ref fail if the ref already exists.
	if _, err := c.output(ctx, "update-ref", ns.prefix()+id, commit, ZeroHash.String()); err != nil {
		return fmt.Errorf("create %s ref: %w", ns.name, err)
	}

	return nil
}

// AppendChainCommit writes payload as a new commit on top of chain id and
// moves the ref to it with compare-and-swap. Returns the new commit ID.
func (c *Client) AppendChainCommit(
	ctx context.Context, ns ChainRefs, id string, payload []byte, message string, sign bool,
) (string, error) {
	tip, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return "", err
	}
	if tip == "" {
		return "", fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	commit, err := c.writeChainCommit(ctx, payload, message, sign, tip)
	if err != nil {
		return "", err
	}

	if _, err := c.output(ctx, "update-ref", ns.prefix()+id, commit, tip); err != nil {
		return "", fmt.Errorf("update %s ref: %w", ns.name, err)
	}

	return commit, nil
}

// ChainTip returns the object the ref of chain id points at, or "" when the
// ref does not exist.
func (c *Client) ChainTip(ctx context.Context, ns ChainRefs, id string) (string, error) {
	return c.refTip(ctx, ns.prefix()+id)
}

func (c *Client) refTip(ctx context.Context, ref string) (string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", ref)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", ref, err)
	}

	// for-each-ref also matches refs *under* ref; keep the exact name only.
	for _, line := range strings.Split(out, "\n") {
		if tip, name, ok := strings.Cut(line, " "); ok && name == ref {
			return tip, nil
		}
	}

	return "", nil
}

// ListChainIDs returns the IDs of all local refs of the family.
func (c *Client) ListChainIDs(ctx context.Context, ns ChainRefs) ([]string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(refname)", ns.prefix())
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", ns.prefix(), err)
	}

	ids := []string{}
	for _, name := range strings.Fields(out) {
		ids = append(ids, strings.TrimPrefix(name, ns.prefix()))
	}

	return ids, nil
}

// ChainRefKind tells what exists under the name of chain id: nothing
// (ChainAbsent), a commit chain (ChainCommits), or a non-commit object left by
// an older git-zf (ChainLegacy). A local chain wins over the tracking ref; a
// non-commit known only through the tracking ref is still ChainLegacy, so a
// fresh clone does not mistake it for "nothing".
func (c *Client) ChainRefKind(ctx context.Context, ns ChainRefs, id string) (string, error) {
	local := ns.prefix() + id
	args := []string{"for-each-ref", "--format=%(objecttype) %(refname)", local}

	tracking := ""
	if remote, _ := c.Remote(); remote != "" {
		tracking = ns.trackingPrefix(remote) + id
		args = append(args, tracking)
	}

	out, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", local, err)
	}

	types := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		if typ, name, ok := strings.Cut(line, " "); ok {
			types[name] = typ
		}
	}

	switch {
	case types[local] == "commit":
		return ChainCommits, nil
	case types[local] != "", tracking != "" && types[tracking] != "" && types[tracking] != "commit":
		return ChainLegacy, nil
	default:
		return ChainAbsent, nil
	}
}

// DeleteChainRef removes the ref of chain id locally, its tracking ref, and
// the ref on the remote. The remote deletion is best-effort: the ref may not
// exist there. Deleting a ref that does not exist is not an error.
func (c *Client) DeleteChainRef(ctx context.Context, ns ChainRefs, id string) error {
	ref := ns.prefix() + id
	if _, err := c.output(ctx, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("delete %s: %w", ref, err)
	}

	remote, _ := c.Remote()
	if remote == "" {
		return nil
	}

	_, _ = c.output(ctx, "update-ref", "-d", ns.trackingPrefix(remote)+id)
	_ = c.gitCmd(ctx, "push", "--quiet", remote, "--delete", ref).Run()

	return nil
}

// ReadChainCommits returns every commit of chain id, parents before children,
// each with the content of its op.json.
func (c *Client) ReadChainCommits(ctx context.Context, ns ChainRefs, id string) ([]ChainCommit, error) {
	ref := ns.prefix() + id

	out, err := c.output(ctx, "rev-list", "--topo-order", "--reverse", "--parents", ref)
	if err != nil {
		// Only on failure: tell "no such chain" from a chain that cannot be
		// read. Checking the ref first would cost one more git process per
		// chain on every listing.
		if tip, tipErr := c.ChainTip(ctx, ns, id); tipErr == nil && tip == "" {
			return nil, fmt.Errorf("%s: %w", id, ErrIssueNotFound)
		}

		return nil, fmt.Errorf("rev-list %s: %w", ref, err)
	}

	var (
		commits []ChainCommit
		specs   []string
		rooted  = !ns.idIsRoot
	)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		commits = append(commits, ChainCommit{ID: fields[0], Parents: fields[1:]})
		specs = append(specs, fields[0]+":"+chainOpFile)
		if len(fields) == 1 && fields[0] == id {
			rooted = true
		}
	}

	if !rooted {
		return nil, fmt.Errorf("%s: %w", ref, ErrIssueRefCorrupt)
	}

	cmd := c.gitCmd(ctx, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")

	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cat-file --batch: %w", gitStderr(err))
	}

	if err := readBatchPayloads(bufio.NewReader(bytes.NewReader(raw)), commits); err != nil {
		return nil, fmt.Errorf("parse cat-file output for %s: %w", ref, err)
	}

	return commits, nil
}
```

Leave the imports `io` and `strconv` in place: `readBatchPayloads` uses them.

- [ ] **Step 5: Write `git/chain_ref_sync.go`**

Replace the whole file with:

```go
package git

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// chainTip is what one ref under a chain prefix points at.
type chainTip struct {
	sha    string
	commit bool
}

// chainTips lists the refs under prefix, keyed by the name after prefix.
func (c *Client) chainTips(ctx context.Context, prefix string) (map[string]chainTip, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(objecttype) %(refname)", prefix)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", prefix, err)
	}

	const fieldCount = 3 // "<oid> <type> <refname>"

	tips := make(map[string]chainTip)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, " ", fieldCount)
		if len(fields) != fieldCount {
			continue
		}

		tips[strings.TrimPrefix(fields[2], prefix)] = chainTip{sha: fields[0], commit: fields[1] == "commit"}
	}

	return tips, nil
}

// FetchChainRefs fetches the remote's chains into the tracking namespace
// refs/remotes/<remote>/zf/<name>/*. Local chain refs are never touched, so a
// chain written offline cannot be lost. The tracking namespace is pruned: a
// tracking ref the remote no longer has is removed, so ChainRefPushed stops
// reporting that chain as pushed and the next sync pushes it again. silent
// runs the fetch without printing anything, for use in hooks. No-op when no
// remote is configured.
func (c *Client) FetchChainRefs(ctx context.Context, ns ChainRefs, silent bool) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	args := []string{"fetch", "--quiet", "--prune", remote, ns.FetchRefspec(remote)}

	if silent {
		if err := c.gitCmd(ctx, args...).Run(); err != nil {
			return fmt.Errorf("fetch %s refs: %w", ns.name, err)
		}

		return nil
	}

	if err := c.runInteractive(ctx, c.root, args...); err != nil {
		return fmt.Errorf("fetch %s refs: %w", ns.name, err)
	}

	return nil
}

// ReconcileChainRefs brings every local chain ref up to date with its tracking
// ref: a missing local ref is created, a local ref that is behind is
// fast-forwarded, and a diverged one gets a two-parent commit carrying
// mergePayload as its op.json. A local ref that is ahead is left alone. A
// tracking ref that is not a commit (written by an older git-zf) is skipped; a
// local ref that is not a commit is replaced by the remote chain. Returns the
// number of merge commits written. No-op without a remote.
func (c *Client) ReconcileChainRefs(ctx context.Context, ns ChainRefs, mergePayload []byte) (int, error) {
	remote, err := c.Remote()
	if err != nil {
		return 0, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return 0, nil
	}

	tracked, err := c.chainTips(ctx, ns.trackingPrefix(remote))
	if err != nil {
		return 0, err
	}

	// One listing of the local tips, instead of one git process per chain.
	local, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return 0, err
	}

	merged := 0
	for _, id := range slices.Sorted(maps.Keys(tracked)) {
		if !tracked[id].commit {
			continue
		}

		didMerge, err := c.reconcileChainRef(ctx, ns, id, local[id], tracked[id].sha, mergePayload)
		if err != nil {
			return merged, fmt.Errorf("reconcile %s %s: %w", ns.name, id, err)
		}
		if didMerge {
			merged++
		}
	}

	return merged, nil
}

// reconcileChainRef reconciles one chain. local is the current tip of its
// local ref; its sha is "" when the ref does not exist.
func (c *Client) reconcileChainRef(
	ctx context.Context, ns ChainRefs, id string, local chainTip, remoteTip string, mergePayload []byte,
) (bool, error) {
	ref := ns.prefix() + id

	switch {
	case local.sha == "":
		_, err := c.output(ctx, "update-ref", ref, remoteTip, ZeroHash.String())

		return false, err
	case !local.commit:
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local.sha)

		return false, err
	case local.sha == remoteTip:
		return false, nil
	}

	// IsAncestor(a, b) reports whether a is an ancestor of b.
	if ahead, err := c.IsAncestor(ctx, remoteTip, local.sha); err != nil || ahead {
		return false, err
	}

	behind, err := c.IsAncestor(ctx, local.sha, remoteTip)
	if err != nil {
		return false, err
	}
	if behind {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local.sha)

		return false, err
	}

	commit, err := c.writeChainCommit(ctx, mergePayload, "merge", false, local.sha, remoteTip)
	if err != nil {
		return false, err
	}

	_, err = c.output(ctx, "update-ref", ref, commit, local.sha)

	return err == nil, err
}

// ChainRefPushed reports whether the remote already has the local tip of
// chain id, according to the tracking ref. Always true without a remote.
func (c *Client) ChainRefPushed(ctx context.Context, ns ChainRefs, id string) (bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return true, nil
	}

	local, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return false, err
	}

	tracked, err := c.refTip(ctx, ns.trackingPrefix(remote)+id)
	if err != nil {
		return false, err
	}

	return local == tracked, nil
}

// UnpushedChainIDs returns, sorted, the IDs of the local chains whose tip the
// remote does not have according to the tracking refs. Empty without a
// remote. Two git processes whatever the number of chains.
func (c *Client) UnpushedChainIDs(ctx context.Context, ns ChainRefs) ([]string, error) {
	remote, err := c.Remote()
	if err != nil {
		return nil, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil, nil
	}

	local, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return nil, err
	}

	tracked, err := c.chainTips(ctx, ns.trackingPrefix(remote))
	if err != nil {
		return nil, err
	}

	var ids []string
	for id, tip := range local {
		if tip.commit && tracked[id].sha != tip.sha {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	return ids, nil
}

// PushChainRef pushes the ref of chain id to the remote with a plain,
// non-forced push: it succeeds only as a fast-forward, so it can never
// overwrite ops pushed by someone else. On success the tracking ref is moved
// to the pushed tip. No-op when no remote is configured.
func (c *Client) PushChainRef(ctx context.Context, ns ChainRefs, id string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	ref := ns.prefix() + id

	tip, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return err
	}
	if tip == "" {
		return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	if err := c.runInteractive(ctx, c.root, "push", "--quiet", remote, ref+":"+ref); err != nil {
		return fmt.Errorf("push %s ref %s: %w", ns.name, id, err)
	}

	if _, err := c.output(ctx, "update-ref", ns.trackingPrefix(remote)+id, tip); err != nil {
		return fmt.Errorf("update tracking ref: %w", err)
	}

	return nil
}
```

- [ ] **Step 6: Fix the call sites**

After the GitNexus renames, the compiler reports every call that lacks the new arguments. Fix each one by this table. Run `mise exec -- go build ./... && mise exec -- go vet ./git/... ./issue/...` until clean.

| Call after the rename | Becomes |
|---|---|
| `c.WriteChainRoot(ctx, p, m)` | `c.WriteChainRoot(ctx, p, m, false)` |
| `c.PublishChainRoot(ctx, id)` | `c.PublishChainRoot(ctx, git.IssueRefs, id, id)` |
| `c.AppendChainCommit(ctx, id, p, m)` | `c.AppendChainCommit(ctx, git.IssueRefs, id, p, m, false)` |
| `c.ChainTip(ctx, id)` | `c.ChainTip(ctx, git.IssueRefs, id)` |
| `c.ListChainIDs(ctx)` | `c.ListChainIDs(ctx, git.IssueRefs)` |
| `c.ReadChainCommits(ctx, id)` | `c.ReadChainCommits(ctx, git.IssueRefs, id)` |
| `c.FetchChainRefs(ctx)` | `c.FetchChainRefs(ctx, git.IssueRefs, false)` |
| `c.ReconcileChainRefs(ctx, payload)` | `c.ReconcileChainRefs(ctx, git.IssueRefs, payload)` |
| `c.ChainRefPushed(ctx, id)` | `c.ChainRefPushed(ctx, git.IssueRefs, id)` |
| `c.PushChainRef(ctx, id)` | `c.PushChainRef(ctx, git.IssueRefs, id)` |

Inside package `git` (the two test files) write `IssueRefs` without the `git.` qualifier. Files to fix: `issue/repo.go` (12 sites), `issue/repo_test.go` (2), `git/issue_ref_test.go` (23), `git/issue_ref_sync_test.go` (37).

The test-only helper at the bottom of `git/issue_ref_test.go` becomes:

```go
// CreateIssueRef writes payload as the root commit of a new issue chain and
// creates refs/zf/issues/<id>, where id is that commit's ID. Returns id.
// Test-only: production code calls WriteChainRoot and PublishChainRoot
// separately, so nothing is published before the branch exists.
func (c *Client) CreateIssueRef(ctx context.Context, payload []byte, message string) (string, error) {
	id, err := c.WriteChainRoot(ctx, payload, message, false)
	if err != nil {
		return "", err
	}

	if err := c.PublishChainRoot(ctx, IssueRefs, id, id); err != nil {
		return "", err
	}

	return id, nil
}
```

In `git/issue_ref_test.go` and `git/issue_ref_sync_test.go`, any literal `refs/zf/remote/issues/` becomes `refs/remotes/origin/zf/issues/` (check with `grep -n 'refs/zf/remote' git/*_test.go issue/*_test.go`; none is expected).

- [ ] **Step 7: Run the tests**

Run: `mise exec -- go test ./git/... -run "TestChainRef|TestIssueRef_" -v`
Expected: PASS, including the five new `TestChainRef*` functions.

Run: `mise exec -- go test ./git/... ./issue/... ./cmd/issue/...`
Expected: PASS. The issue suites are the regression net for the tracking-namespace move.

- [ ] **Step 8: Commit**

```bash
git add git issue
git commit -m "refactor: chain plumbing takes a namespace, tracking refs under refs/remotes"
```

---

### Task 3: `review/record.go` — ops and the fold

**Files:**
- Create: `review/record.go`
- Create: `review/record_test.go`

**Interfaces:**
- Consumes: `chain.Order`, `chain.Node`, `chain.ParseAt` (Task 1).
- Produces (package `review`):
  - `const OpVersion = 1`
  - `const OpRequest = "request"`, `OpStart = "start"`, `OpApprove = "approve"`, `OpReject = "reject"`, `OpClose = "close"`, `OpMerge = "merge"`
  - `const StatusInReview = "in_review"`, `StatusApproved = "approved"`, `StatusChangesRequested = "changes_requested"`
  - `type Op struct { V int; Type, At, Author string; FeatureSHA, ApprovedSHA string; HasCommits bool; Comment string; ID string; Parents []string }`
  - `func DecodeOp(id string, parents []string, payload []byte) (Op, bool)`
  - `type Approval struct { Commit, Author, ApprovedSHA string; HasCommits bool }`
  - `type State struct { Slug, Status string; Round int; FeatureSHA, Reviewer, Comment string; HasCommits bool; Approvals []Approval; Closed bool; UpdatedAt time.Time; Warnings []string }`
  - `func Fold(slug string, ops []Op) State`

- [ ] **Step 1: Write the failing test**

Create `review/record_test.go`:

```go
package review

import (
	"slices"
	"testing"
)

const (
	t0 = "2026-10-01T10:00:00Z"
	t1 = "2026-10-01T11:00:00Z"
	t2 = "2026-10-01T12:00:00Z"
	t3 = "2026-10-01T13:00:00Z"
)

func op(id, typ, at string, parents ...string) Op {
	return Op{V: OpVersion, ID: id, Type: typ, At: at, Author: "dev <dev@test.com>", Parents: parents}
}

func request(id, at, sha string, parents ...string) Op {
	o := op(id, OpRequest, at, parents...)
	o.FeatureSHA = sha

	return o
}

func approve(id, at, sha, author string, hasCommits bool, parents ...string) Op {
	o := op(id, OpApprove, at, parents...)
	o.ApprovedSHA, o.Author, o.HasCommits = sha, author, hasCommits

	return o
}

func reject(id, at, comment string, hasCommits bool, parents ...string) Op {
	o := op(id, OpReject, at, parents...)
	o.Comment, o.HasCommits = comment, hasCommits

	return o
}

func approvedSHAs(st State) []string {
	shas := []string{}
	for _, a := range st.Approvals {
		shas = append(shas, a.ApprovedSHA)
	}
	slices.Sort(shas)

	return shas
}

func TestFold(t *testing.T) {
	t.Parallel()

	req := request("r1", t0, "f1")

	t.Run("an empty chain has no status and round 0", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", nil)
		if st.Slug != "42" || st.Status != "" || st.Round != 0 || st.Closed {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("request opens round 1 in review", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req})
		if st.Status != StatusInReview || st.Round != 1 || st.FeatureSHA != "f1" {
			t.Errorf("state = %+v", st)
		}
		if st.UpdatedAt.IsZero() {
			t.Error("UpdatedAt is zero")
		}
	})

	t.Run("start records the first op author as reviewer", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t1, "r1")
		s1.Author = "alice"
		s2 := op("s2", OpStart, t2, "s1")
		s2.Author = "bob"

		if got := Fold("42", []Op{req, s1, s2}).Reviewer; got != "alice" {
			t.Errorf("Reviewer = %q, want alice", got)
		}
	})

	t.Run("start, approve and reject before any request are ignored", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			op("s0", OpStart, t0),
			approve("a0", t1, "f1", "alice", true, "s0"),
			reject("j0", t2, "no", true, "a0"),
		})
		if st.Status != "" || st.Reviewer != "" || len(st.Approvals) != 0 || st.Comment != "" || st.HasCommits {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("approve moves to approved and records the approval", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req, approve("a1", t1, "f1", "alice", false, "r1")})
		if st.Status != StatusApproved || len(st.Approvals) != 1 {
			t.Fatalf("state = %+v", st)
		}
		want := Approval{Commit: "a1", Author: "alice", ApprovedSHA: "f1"}
		if st.Approvals[0] != want {
			t.Errorf("approval = %+v, want %+v", st.Approvals[0], want)
		}
	})

	t.Run("reject moves to changes_requested, keeps the comment and voids approvals", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			reject("j1", t2, "fix the tests", true, "a1"),
		})
		if st.Status != StatusChangesRequested || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
		if st.Comment != "fix the tests" || !st.HasCommits {
			t.Errorf("Comment = %q, HasCommits = %v", st.Comment, st.HasCommits)
		}
	})

	t.Run("a new request starts the next round and clears the previous one", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t1, "r1")
		s1.Author = "alice"
		st := Fold("42", []Op{
			req, s1,
			reject("j1", t2, "fix the tests", true, "s1"),
			request("r2", t3, "f2", "j1"),
		})
		if st.Status != StatusInReview || st.Round != 2 || st.FeatureSHA != "f2" {
			t.Errorf("state = %+v", st)
		}
		if st.Reviewer != "" || st.Comment != "" || st.HasCommits || len(st.Approvals) != 0 {
			t.Errorf("round 1 leaked into round 2: %+v", st)
		}
	})

	t.Run("two concurrent requests count as one round", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			request("ra", t0, "f1"),
			request("rb", t0, "f1"),
			op("m", OpMerge, t1, "ra", "rb"),
		})
		if st.Round != 1 || st.Status != StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("two concurrent approvals are both kept and has_commits is ORed", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			approve("b1", t1, "f9", "bob", true, "r1"),
			op("m", OpMerge, t2, "a1", "b1"),
		})
		if st.Status != StatusApproved || !st.HasCommits {
			t.Errorf("state = %+v", st)
		}
		if got := approvedSHAs(st); !slices.Equal(got, []string{"f1", "f9"}) {
			t.Errorf("approved SHAs = %v", got)
		}
	})

	t.Run("a reject wins over a concurrent approve in both sort orders", func(t *testing.T) {
		t.Parallel()

		approveFirst := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			reject("j1", t2, "no", false, "r1"),
		})
		rejectFirst := Fold("42", []Op{
			req,
			reject("j1", t1, "no", false, "r1"),
			approve("a1", t2, "f1", "alice", false, "r1"),
		})
		for name, st := range map[string]State{"approve first": approveFirst, "reject first": rejectFirst} {
			if st.Status != StatusChangesRequested || len(st.Approvals) != 0 {
				t.Errorf("%s: state = %+v", name, st)
			}
		}
	})

	t.Run("two concurrent rejects keep both comments", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			reject("j1", t1, "first", false, "r1"),
			reject("j2", t2, "second", true, "r1"),
		})
		if st.Comment != "first\n\nsecond" || !st.HasCommits {
			t.Errorf("Comment = %q, HasCommits = %v", st.Comment, st.HasCommits)
		}
	})

	t.Run("close marks the review closed and a later request reopens it", func(t *testing.T) {
		t.Parallel()

		closed := []Op{req, approve("a1", t1, "f1", "alice", false, "r1"), op("c1", OpClose, t2, "a1")}
		st := Fold("42", closed)
		if !st.Closed || st.Status != StatusApproved {
			t.Errorf("after close: %+v", st)
		}

		st = Fold("42", append(closed, request("r2", t3, "f2", "c1")))
		if st.Closed || st.Round != 2 || st.Status != StatusInReview {
			t.Errorf("after reopen: %+v", st)
		}
	})

	t.Run("unknown types and merge ops change nothing", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req, op("u1", "assign", t1, "r1"), op("m1", OpMerge, t2, "u1")})
		base := Fold("42", []Op{req})
		if st.Status != base.Status || st.Round != base.Round || !st.UpdatedAt.Equal(base.UpdatedAt) {
			t.Errorf("state = %+v, want %+v", st, base)
		}
	})

	t.Run("slice order does not matter", func(t *testing.T) {
		t.Parallel()

		a := approve("a1", t1, "f1", "alice", false, "r1")
		if got := Fold("42", []Op{a, req}).Status; got != StatusApproved {
			t.Errorf("Status = %q", got)
		}
	})
}

func TestDecodeOp(t *testing.T) {
	t.Parallel()

	t.Run("a valid payload decodes and carries the commit identity", func(t *testing.T) {
		t.Parallel()

		payload := []byte(`{"v":1,"type":"approve","at":"2026-10-01T10:00:00Z","author":"a","approved_sha":"f1","has_commits":true}`)
		got, ok := DecodeOp("c1", []string{"p1"}, payload)
		if !ok || got.Type != OpApprove || got.ApprovedSHA != "f1" || !got.HasCommits {
			t.Errorf("op = %+v, ok = %v", got, ok)
		}
		if got.ID != "c1" || !slices.Equal(got.Parents, []string{"p1"}) {
			t.Errorf("identity = %q %v", got.ID, got.Parents)
		}
	})

	for name, payload := range map[string][]byte{
		"invalid JSON":       []byte("not json"),
		"an unknown version": []byte(`{"v":99,"type":"approve"}`),
		"a missing payload":  nil,
	} {
		t.Run(name+" is rejected but keeps ID and parents", func(t *testing.T) {
			t.Parallel()

			got, ok := DecodeOp("c1", []string{"p1"}, payload)
			if ok || got.Type != "" || got.ID != "c1" || !slices.Equal(got.Parents, []string{"p1"}) {
				t.Errorf("op = %+v, ok = %v", got, ok)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./review/... -v`
Expected: FAIL, build errors `undefined: Op`, `undefined: Fold`.

- [ ] **Step 3: Write the implementation**

Create `review/record.go`:

```go
// Package review holds the review of an issue as stored in the repository: a
// chain of op commits under refs/zf/reviews/<slug>, folded into a State.
package review

import (
	"encoding/json"
	"time"

	"github.com/piprim/git-zf/internal/chain"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpRequest = "request"
	OpStart   = "start"
	OpApprove = "approve"
	OpReject  = "reject"
	OpClose   = "close"
	OpMerge   = "merge"
)

// Review statuses. The values are the ones stored in the reviews table.
const (
	StatusInReview         = "in_review"
	StatusApproved         = "approved"
	StatusChangesRequested = "changes_requested"
)

// Op is one action on a review: the content of op.json in one commit of the
// chain at refs/zf/reviews/<slug>. ID and Parents come from the commit itself
// and are not part of the JSON.
type Op struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	At     string `json:"at"` // RFC 3339, UTC
	Author string `json:"author,omitempty"`

	FeatureSHA  string `json:"feature_sha,omitempty"`  // request
	ApprovedSHA string `json:"approved_sha,omitempty"` // approve
	HasCommits  bool   `json:"has_commits,omitempty"`  // approve, reject
	Comment     string `json:"comment,omitempty"`      // reject

	ID      string   `json:"-"`
	Parents []string `json:"-"`
}

// DecodeOp builds the Op of commit id from its op.json payload. ok is false
// when the payload is missing, is not valid JSON or has an unknown version; the
// returned Op then has an empty Type, so Fold ignores it while its ID and
// Parents still keep the chain connected.
func DecodeOp(id string, parents []string, payload []byte) (op Op, ok bool) {
	if err := json.Unmarshal(payload, &op); err != nil || op.V != OpVersion {
		return Op{ID: id, Parents: parents}, false
	}

	op.ID, op.Parents = id, parents

	return op, true
}

// Approval is one approve op of the current round.
type Approval struct {
	Commit      string // the approve op's commit ID: what a signature covers
	Author      string
	ApprovedSHA string // the commit the reviewer approved
	HasCommits  bool
}

// State is the current state of a review: the fold of its op chain.
type State struct {
	Slug       string
	Status     string // "", in_review, approved, changes_requested
	Round      int
	FeatureSHA string
	Reviewer   string
	Comment    string     // reject comments of the round, joined by a blank line
	HasCommits bool       // OR over the round's approve and reject ops
	Approvals  []Approval // approvals of the current round
	Closed     bool
	UpdatedAt  time.Time // at of the last applied op

	// Warnings lists the commits Load skipped as malformed.
	Warnings []string
}

// Fold computes the State of slug's review from its ops. The order of ops in
// the slice does not matter: they are linearized from their Parents.
func Fold(slug string, ops []Op) State {
	st := State{Slug: slug}

	ordered := chain.Order(ops, func(op *Op) chain.Node {
		return chain.Node{ID: op.ID, Parents: op.Parents, At: op.At}
	})
	for i := range ordered {
		if apply(&st, &ordered[i]) {
			st.UpdatedAt = chain.ParseAt(ordered[i].At)
		}
	}

	return st
}

// apply applies op to st and reports whether it changed anything. An op that
// does not fit the current status is ignored: that is how concurrent actions
// resolve the same way on every clone.
func apply(st *State, op *Op) bool {
	switch op.Type {
	case OpRequest:
		// A second request while in review is a duplicate (two clones
		// requested concurrently), not a new round.
		if st.Status == StatusInReview {
			return false
		}
		st.Round++
		st.Status, st.FeatureSHA = StatusInReview, op.FeatureSHA
		st.Reviewer, st.Comment, st.HasCommits, st.Approvals, st.Closed = "", "", false, nil, false
	case OpStart:
		if st.Status != StatusInReview || st.Reviewer != "" {
			return false
		}
		st.Reviewer = op.Author
	case OpApprove:
		// Every approval of the round is kept: the one that covers the final
		// tip may be the second one.
		if st.Status != StatusInReview && st.Status != StatusApproved {
			return false
		}
		st.Status = StatusApproved
		st.Approvals = append(st.Approvals, Approval{
			Commit: op.ID, Author: op.Author, ApprovedSHA: op.ApprovedSHA, HasCommits: op.HasCommits,
		})
		st.HasCommits = st.HasCommits || op.HasCommits
	case OpReject:
		// A reject wins over a concurrent approve whichever sorts first: it
		// applies on top of approved, and an approve does not apply on top of
		// changes_requested.
		if st.Status == "" {
			return false
		}
		st.Status, st.Approvals = StatusChangesRequested, nil
		if op.Comment != "" {
			if st.Comment != "" {
				st.Comment += "\n\n"
			}
			st.Comment += op.Comment
		}
		st.HasCommits = st.HasCommits || op.HasCommits
	case OpClose:
		st.Closed = true
	default:
		return false
	}

	return true
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./review/... -v`
Expected: PASS, all `TestFold` and `TestDecodeOp` subtests.

- [ ] **Step 5: Commit**

```bash
git add review/record.go review/record_test.go
git commit -m "feat: review ops and their fold"
```

---

### Task 4: `review/repo.go` — the glue, and the test seed helper

**Files:**
- Create: `review/repo.go`
- Create: `review/repo_test.go`
- Create: `review/reviewtest/reviewtest.go`

**Interfaces:**
- Consumes: Task 2 (`git.ReviewRefs`, `WriteChainRoot`, `PublishChainRoot`, `AppendChainCommit`, `ListChainIDs`, `ReadChainCommits`, `ChainRefKind`, `DeleteChainRef`, `FetchChainRefs`, `ReconcileChainRefs`, `PushChainRef`, `UnpushedChainIDs`, `git.ChainAbsent`, `git.ChainLegacy`), Task 3 (`Op`, `State`, `Fold`, `DecodeOp`).
- Produces (package `review`):
  - `var ErrLegacyReview error`
  - `func Load(ctx context.Context, c *git.Client, slug string) (*State, error)` — `(nil, nil)` when there is no review
  - `func List(ctx context.Context, c *git.Client) (states []State, warnings []string, err error)` — in slug order
  - `func Append(ctx context.Context, c *git.Client, slug string, op *Op, sign bool) error`
  - `func ReplaceLegacy(ctx context.Context, c *git.Client, slug string) error`
  - `func Fetch(ctx context.Context, c *git.Client, silent bool) error`
  - `func Push(ctx context.Context, c *git.Client, slug string) error`
  - `func Sync(ctx context.Context, c *git.Client) error`
- Produces (package `reviewtest`):
  - `func Seed(t testing.TB, c *git.Client, slug, status string, round int, featureSHA string)`

- [ ] **Step 1: Write the failing test**

Create `review/repo_test.go`:

```go
package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// newRepo creates a repository with one commit and the given user identity.
// origin, when non-empty, is added as the "origin" remote.
func newRepo(t *testing.T, user, origin string) *git.Client {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", user)
	runGit(t, dir, "config", "user.email", user+"@test.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit(t, dir, "add", "base.txt")
	runGit(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		runGit(t, dir, "remote", "add", "origin", origin)
	}

	c, err := git.NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func newOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

func mustAppend(t *testing.T, c *git.Client, slug string, op Op) {
	t.Helper()

	if err := Append(t.Context(), c, slug, &op, false); err != nil {
		t.Fatalf("Append %s: %v", op.Type, err)
	}
}

func mustLoad(t *testing.T, c *git.Client, slug string) *State {
	t.Helper()

	st, err := Load(t.Context(), c, slug)
	if err != nil || st == nil {
		t.Fatalf("Load(%s) = %v, %v", slug, st, err)
	}

	return st
}

func writeBlobRef(t *testing.T, c *git.Client, slug string) {
	t.Helper()

	dir := c.WorkingTreeRoot()
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(`{"status":"in_review","round":1}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	runGit(t, dir, "update-ref", "refs/zf/reviews/"+slug, strings.TrimSpace(string(out)))
}

func TestAppendLoad(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	dir := c.WorkingTreeRoot()
	ctx := t.Context()

	t.Run("Load of an unknown review is nil without error", func(t *testing.T) {
		st, err := Load(ctx, c, "42")
		if st != nil || err != nil {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})

	t.Run("the first op creates the chain", func(t *testing.T) {
		st := mustLoad(t, c, "42")
		if st.Slug != "42" || st.Status != StatusInReview || st.Round != 1 || st.FeatureSHA != "f1" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("Append fills v, at and author", func(t *testing.T) {
		raw := runGit(t, dir, "cat-file", "blob", "refs/zf/reviews/42:op.json")
		for _, want := range []string{`"v":1`, `"at":"`, `"author":"alice <alice@test.com>"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("op.json = %s, want it to contain %s", raw, want)
			}
		}
	})

	mustAppend(t, c, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})

	t.Run("a later op lands on the same chain", func(t *testing.T) {
		if n := runGit(t, dir, "rev-list", "--count", "refs/zf/reviews/42"); n != "2" {
			t.Errorf("chain length = %s, want 2", n)
		}
		if st := mustLoad(t, c, "42"); st.Status != StatusApproved || len(st.Approvals) != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestListAndLegacy(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	mustAppend(t, c, "10", Op{Type: OpRequest, FeatureSHA: "f2"})
	writeBlobRef(t, c, "old")

	t.Run("List returns the chains in slug order and warns about the blob", func(t *testing.T) {
		states, warnings, err := List(ctx, c)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		slugs := []string{}
		for _, st := range states {
			slugs = append(slugs, st.Slug)
		}
		if !slices.Equal(slugs, []string{"10", "42"}) {
			t.Errorf("slugs = %v", slugs)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "old") {
			t.Errorf("warnings = %v", warnings)
		}
	})

	t.Run("Load of a blob ref reports ErrLegacyReview", func(t *testing.T) {
		st, err := Load(ctx, c, "old")
		if st != nil || !errors.Is(err, ErrLegacyReview) {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	t.Run("Append refuses to write on a blob ref", func(t *testing.T) {
		err := Append(ctx, c, "old", &Op{Type: OpRequest}, false)
		if !errors.Is(err, ErrLegacyReview) {
			t.Errorf("Append = %v", err)
		}
	})

	t.Run("ReplaceLegacy makes room for a chain", func(t *testing.T) {
		if err := ReplaceLegacy(ctx, c, "old"); err != nil {
			t.Fatalf("ReplaceLegacy: %v", err)
		}
		if st, err := Load(ctx, c, "old"); st != nil || err != nil {
			t.Fatalf("Load after replace = %v, %v", st, err)
		}
		mustAppend(t, c, "old", Op{Type: OpRequest, FeatureSHA: "f3"})
		if st := mustLoad(t, c, "old"); st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestTwoClonesApproveOffline(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	alice := newRepo(t, "alice", origin)
	bob := newRepo(t, "bob", origin)
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push: %v", err)
	}
	for _, c := range []*git.Client{alice, bob} {
		if err := Sync(ctx, c); err != nil {
			t.Fatalf("reviewer sync: %v", err)
		}
	}

	// Both reviewers approve without seeing each other's op.
	mustAppend(t, alice, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})
	mustAppend(t, bob, "42", Op{Type: OpApprove, ApprovedSHA: "f2", HasCommits: true})

	t.Run("the first reviewer's push succeeds", func(t *testing.T) {
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("alice push: %v", err)
		}
	})

	t.Run("the second reviewer's push succeeds after an automatic merge", func(t *testing.T) {
		if err := Push(ctx, bob, "42"); err != nil {
			t.Fatalf("bob push: %v", err)
		}
	})

	for _, c := range []*git.Client{dev, alice} {
		if err := Sync(ctx, c); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	t.Run("the developer sees both approvals", func(t *testing.T) {
		st := mustLoad(t, dev, "42")
		if st.Status != StatusApproved || len(st.Approvals) != 2 || !st.HasCommits {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("the chain holds exactly one merge commit", func(t *testing.T) {
		n := runGit(t, dev.WorkingTreeRoot(), "rev-list", "--count", "--min-parents=2", "refs/zf/reviews/42")
		if n != "1" {
			t.Errorf("merge commits = %s, want 1", n)
		}
	})

	t.Run("every clone converges on the same tip", func(t *testing.T) {
		tips := []string{}
		for _, c := range []*git.Client{dev, alice, bob} {
			tips = append(tips, runGit(t, c.WorkingTreeRoot(), "rev-parse", "refs/zf/reviews/42"))
		}
		if tips[0] != tips[1] || tips[1] != tips[2] {
			t.Errorf("tips = %v", tips)
		}
	})
}

func TestSync_EdgeCases(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	devDir := dev.WorkingTreeRoot()
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push: %v", err)
	}

	t.Run("an approval on a clone that never loaded the review joins the existing chain", func(t *testing.T) {
		carol := newRepo(t, "carol", origin)
		if err := Sync(ctx, carol); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		mustAppend(t, carol, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})

		roots := runGit(t, carol.WorkingTreeRoot(), "rev-list", "--count", "--max-parents=0", "refs/zf/reviews/42")
		if roots != "1" {
			t.Errorf("root commits = %s, want 1", roots)
		}
	})

	t.Run("an op whose push failed stays local and goes out with the next Sync", func(t *testing.T) {
		mustAppend(t, dev, "42", Op{Type: OpStart})

		if err := os.Rename(origin, origin+".off"); err != nil {
			t.Fatalf("rename origin: %v", err)
		}
		pushErr := Push(ctx, dev, "42")
		if err := os.Rename(origin+".off", origin); err != nil {
			t.Fatalf("restore origin: %v", err)
		}
		if pushErr == nil {
			t.Fatal("Push succeeded with the remote gone")
		}

		local := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42")
		if err := Sync(ctx, dev); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if remote := runGit(t, origin, "rev-parse", "refs/zf/reviews/42"); remote != local {
			t.Errorf("origin tip = %s, want %s", remote, local)
		}
	})

	t.Run("tracking refs removed by a plain git fetch --prune are restored without a merge", func(t *testing.T) {
		before := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42")
		runGit(t, devDir, "update-ref", "-d", "refs/remotes/origin/zf/reviews/42")

		if err := Sync(ctx, dev); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if after := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42"); after != before {
			t.Errorf("tip moved from %s to %s", before, after)
		}
		if n := runGit(t, devDir, "rev-list", "--count", "--min-parents=2", "refs/zf/reviews/42"); n != "0" {
			t.Errorf("merge commits = %s, want 0", n)
		}
		if tracked := runGit(t, devDir, "rev-parse", "refs/remotes/origin/zf/reviews/42"); tracked != before {
			t.Errorf("tracking ref = %s, want %s", tracked, before)
		}
	})

	t.Run("without a remote every network call is a no-op", func(t *testing.T) {
		solo := newRepo(t, "solo", "")
		mustAppend(t, solo, "7", Op{Type: OpRequest, FeatureSHA: "f1"})
		if err := Sync(ctx, solo); err != nil {
			t.Errorf("Sync: %v", err)
		}
		if err := Push(ctx, solo, "7"); err != nil {
			t.Errorf("Push: %v", err)
		}
		if st := mustLoad(t, solo, "7"); st.Status != StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a commit whose op.json is not JSON is skipped with a warning", func(t *testing.T) {
		solo := newRepo(t, "solo", "")
		mustAppend(t, solo, "7", Op{Type: OpRequest, FeatureSHA: "f1"})
		if _, err := solo.AppendChainCommit(ctx, git.ReviewRefs, "7", []byte("not json"), "junk", false); err != nil {
			t.Fatalf("AppendChainCommit: %v", err)
		}

		st := mustLoad(t, solo, "7")
		if st.Status != StatusInReview || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
		if len(st.Warnings) != 1 {
			t.Errorf("warnings = %v", st.Warnings)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./review/... -v`
Expected: FAIL, build errors `undefined: Append`, `undefined: Load`, `undefined: ErrLegacyReview`.

- [ ] **Step 3: Write the implementation**

Create `review/repo.go`:

```go
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/piprim/git-zf/git"
)

// ErrLegacyReview is returned for a review ref that points at a JSON blob, the
// format of git-zf before reviews were commit chains. Such a review is not
// migrated: `git zf review request` replaces it.
var ErrLegacyReview = errors.New("review ref uses the old blob format")

// marshalOp fills the fields every op written by this clone shares (V, At,
// Author) and returns the op.json bytes.
func marshalOp(ctx context.Context, c *git.Client, op *Op) ([]byte, error) {
	author, _ := c.ConfigUser(ctx)
	op.V, op.At, op.Author = OpVersion, time.Now().UTC().Format(time.RFC3339), author

	payload, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("marshal %s op: %w", op.Type, err)
	}

	return payload, nil
}

// Load reads the review chain of slug and folds it. It returns (nil, nil) when
// the issue has no review, and ErrLegacyReview when the ref is a blob.
// Malformed commits are skipped and named in State.Warnings.
func Load(ctx context.Context, c *git.Client, slug string) (*State, error) {
	kind, err := c.ChainRefKind(ctx, git.ReviewRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read review %s: %w", slug, err)
	}

	switch kind {
	case git.ChainAbsent:
		return nil, nil
	case git.ChainLegacy:
		return nil, fmt.Errorf("review %s: %w", slug, ErrLegacyReview)
	}

	commits, err := c.ReadChainCommits(ctx, git.ReviewRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read review %s: %w", slug, err)
	}

	ops := make([]Op, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		op, ok := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: review %s: skipping malformed op %s", slug, commit.ID))
		}
		ops = append(ops, op)
	}

	st := Fold(slug, ops)
	st.Warnings = warnings

	return &st, nil
}

// List loads every local review, in slug order. An unreadable ref is skipped;
// warnings names it, along with every malformed op met on the way.
//
// ponytail: three git processes per review, closed ones included. Fine to a
// few hundred reviews; batch all chains through one `git log --stdin` if
// listing gets slow.
func List(ctx context.Context, c *git.Client) (states []State, warnings []string, err error) {
	slugs, err := c.ListChainIDs(ctx, git.ReviewRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list reviews: %w", err)
	}

	states = make([]State, 0, len(slugs))
	for _, slug := range slugs {
		st, err := Load(ctx, c, slug)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping review ref %s: %v", slug, err))

			continue
		}
		if st == nil {
			continue
		}

		warnings = append(warnings, st.Warnings...)
		states = append(states, *st)
	}

	return states, warnings, nil
}

// Append writes op on the review chain of slug, creating the chain when the
// issue has no review yet. V, At and Author are filled in here. sign forces a
// signature even when commit.gpgsign is off. Nothing is pushed. Callers Sync
// first, so that an op is not written beside a chain the remote already has.
func Append(ctx context.Context, c *git.Client, slug string, op *Op, sign bool) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	kind, err := c.ChainRefKind(ctx, git.ReviewRefs, slug)
	if err != nil {
		return fmt.Errorf("read review %s: %w", slug, err)
	}

	switch kind {
	case git.ChainLegacy:
		return fmt.Errorf("review %s: %w", slug, ErrLegacyReview)
	case git.ChainAbsent:
		root, err := c.WriteChainRoot(ctx, payload, op.Type, sign)
		if err != nil {
			return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
		}
		if err := c.PublishChainRoot(ctx, git.ReviewRefs, slug, root); err != nil {
			return fmt.Errorf("create review %s: %w", slug, err)
		}

		return nil
	}

	if _, err := c.AppendChainCommit(ctx, git.ReviewRefs, slug, payload, op.Type, sign); err != nil {
		return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
	}

	return nil
}

// ReplaceLegacy deletes the blob review ref of slug, locally and on the
// remote, so that a chain can take its name. Until the remote blob is gone a
// push of the chain is rejected.
func ReplaceLegacy(ctx context.Context, c *git.Client, slug string) error {
	if err := c.DeleteChainRef(ctx, git.ReviewRefs, slug); err != nil {
		return fmt.Errorf("replace legacy review %s: %w", slug, err)
	}

	return nil
}

// Fetch fetches the remote's review chains and reconciles the local ones with
// them, merging diverged chains. silent prints nothing, for use in hooks.
// No-op without a remote.
func Fetch(ctx context.Context, c *git.Client, silent bool) error {
	if err := c.FetchChainRefs(ctx, git.ReviewRefs, silent); err != nil {
		return fmt.Errorf("fetch reviews: %w", err)
	}

	payload, err := marshalOp(ctx, c, &Op{Type: OpMerge})
	if err != nil {
		return err
	}

	if _, err := c.ReconcileChainRefs(ctx, git.ReviewRefs, payload); err != nil {
		return fmt.Errorf("reconcile reviews: %w", err)
	}

	return nil
}

// Push pushes the review chain of slug. A rejected push (someone pushed
// first) triggers one fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, slug string) error {
	firstErr := c.PushChainRef(ctx, git.ReviewRefs, slug)
	if firstErr == nil {
		return nil
	}

	if err := Fetch(ctx, c, false); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushChainRef(ctx, git.ReviewRefs, slug); err != nil {
		return fmt.Errorf("push review %s after merge: %w", slug, err)
	}

	return nil
}

// Sync fetches and reconciles every review, then pushes the chains the remote
// does not have yet: an op whose push failed earlier goes out here. No-op
// without a remote.
func Sync(ctx context.Context, c *git.Client) error {
	if err := Fetch(ctx, c, false); err != nil {
		return err
	}

	slugs, err := c.UnpushedChainIDs(ctx, git.ReviewRefs)
	if err != nil {
		return fmt.Errorf("list unpushed reviews: %w", err)
	}

	var failed []error
	for _, slug := range slugs {
		if err := Push(ctx, c, slug); err != nil {
			failed = append(failed, err)
		}
	}

	return errors.Join(failed...)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./review/... -v`
Expected: PASS: `TestFold`, `TestDecodeOp`, `TestAppendLoad`, `TestListAndLegacy`, `TestTwoClonesApproveOffline`, `TestSync_EdgeCases`.

- [ ] **Step 5: Write the seed helper**

Create `review/reviewtest/reviewtest.go`:

```go
// Package reviewtest seeds review chains in the tests of other packages.
package reviewtest

import (
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
)

// Seed brings the review of slug to status at the given round, as the review
// commands would: every earlier round is a request followed by a reject.
// status is review.StatusInReview, StatusApproved or StatusChangesRequested.
// The approval, if any, covers featureSHA. Nothing is pushed.
func Seed(t testing.TB, c *git.Client, slug, status string, round int, featureSHA string) {
	t.Helper()

	add := func(op review.Op) {
		t.Helper()

		if err := review.Append(t.Context(), c, slug, &op, false); err != nil {
			t.Fatalf("seed review %s: %s op: %v", slug, op.Type, err)
		}
	}

	for range max(round, 1) - 1 {
		add(review.Op{Type: review.OpRequest, FeatureSHA: featureSHA})
		add(review.Op{Type: review.OpReject})
	}

	add(review.Op{Type: review.OpRequest, FeatureSHA: featureSHA})

	switch status {
	case review.StatusApproved:
		add(review.Op{Type: review.OpApprove, ApprovedSHA: featureSHA})
	case review.StatusChangesRequested:
		add(review.Op{Type: review.OpReject})
	}
}
```

Add to `review/repo_test.go` nothing for it: a helper test would import `reviewtest`, which imports `review`, a cycle for an internal test file. Check it instead with a throwaway external test, created and kept as `review/reviewtest/reviewtest_test.go`:

```go
package reviewtest_test

import (
	"os/exec"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
)

func TestSeed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@test.com"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	c, err := git.NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	for name, tc := range map[string]struct {
		slug, status string
		round        int
	}{
		"in review, round 1":         {"a", review.StatusInReview, 1},
		"approved, round 1":          {"b", review.StatusApproved, 1},
		"changes requested, round 3": {"c", review.StatusChangesRequested, 3},
	} {
		t.Run(name, func(t *testing.T) {
			reviewtest.Seed(t, c, tc.slug, tc.status, tc.round, "f1")

			st, err := review.Load(t.Context(), c, tc.slug)
			if err != nil || st == nil {
				t.Fatalf("Load = %v, %v", st, err)
			}
			if st.Status != tc.status || st.Round != tc.round || st.FeatureSHA != "f1" {
				t.Errorf("state = %+v", st)
			}
		})
	}
}
```

`git.NewClientAt` needs a repository with a working tree; `git init` alone gives one.

- [ ] **Step 6: Run everything in the package**

Run: `mise exec -- go test ./review/... -v`
Expected: PASS, including `TestSeed`.

- [ ] **Step 7: Commit**

```bash
git add review
git commit -m "feat: review chains: load, append, sync"
```

---

### Task 5: `git zf init` configures the fetch refspecs

**Files:**
- Modify: `git/chain_ref_sync.go` (add `ConfigureChainFetch`)
- Modify: `cmd/init/init.go` (`runE`, the `Long` help text)
- Modify: `cmd/init/init_test.go`

**Interfaces:**
- Consumes: `ChainRefs.FetchRefspec`, `IssueRefs`, `ReviewRefs` (Task 2).
- Produces: `func (c *Client) ConfigureChainFetch(ctx context.Context) (remote string, err error)` — `remote` is `""` when the repository has none.

- [ ] **Step 1: Write the failing test**

Append to `cmd/init/init_test.go`:

```go
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(string(out))
}

func TestInit_ConfiguresChainFetch(t *testing.T) {
	dir := newInitRepo(t)
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitOut(t, dir, "init", "-q", "--bare", origin)
	gitOut(t, dir, "remote", "add", "origin", origin)
	// A tracking ref of the layout used before refs/remotes/<remote>/zf/.
	gitOut(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	gitOut(t, dir, "update-ref", "refs/zf/remote/issues/abc", "HEAD")

	out := runInit(t, dir)
	runInit(t, dir)

	specs := gitOut(t, dir, "config", "--get-all", "remote.origin.fetch")

	for _, want := range []string{
		"+refs/zf/reviews/*:refs/remotes/origin/zf/reviews/*",
		"+refs/zf/issues/*:refs/remotes/origin/zf/issues/*",
	} {
		t.Run("refspec added exactly once: "+want, func(t *testing.T) {
			if n := strings.Count(specs, want); n != 1 {
				t.Fatalf("refspec appears %d times in:\n%s", n, specs)
			}
		})
	}

	t.Run("the default branch refspec is kept", func(t *testing.T) {
		if !strings.Contains(specs, "+refs/heads/*:refs/remotes/origin/*") {
			t.Fatalf("default refspec lost:\n%s", specs)
		}
	})

	t.Run("stale refs/zf/remote/ tracking refs are deleted", func(t *testing.T) {
		if refs := gitOut(t, dir, "for-each-ref", "refs/zf/remote/"); refs != "" {
			t.Fatalf("stale refs survive:\n%s", refs)
		}
	})

	t.Run("init reports the remote it configured", func(t *testing.T) {
		if !strings.Contains(out, `"origin"`) {
			t.Fatalf("output does not name the remote:\n%s", out)
		}
	})
}

func TestInit_NoRemote_SkipsChainFetch(t *testing.T) {
	dir := newInitRepo(t)
	out := runInit(t, dir)

	t.Run("init succeeds and configures no refspec", func(t *testing.T) {
		if strings.Contains(out, "refs/zf/") {
			t.Fatalf("unexpected refspec message:\n%s", out)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/init/... -run "TestInit_ConfiguresChainFetch|TestInit_NoRemote" -v`
Expected: FAIL: `refspec appears 0 times`, `stale refs survive`.

- [ ] **Step 3: Write `ConfigureChainFetch`**

Append to `git/chain_ref_sync.go`:

```go
// ConfigureChainFetch adds the fetch refspecs of the chain families to the
// remote's configuration, unless already present, and returns the remote's
// name ("" when there is none). Git prunes by refspec: without these lines a
// plain `git fetch --prune` deletes the tracking refs under
// refs/remotes/<remote>/zf/, since the default refspec owns everything under
// refs/remotes/<remote>/. With them, a plain `git fetch` also brings the
// chains. It also deletes the tracking refs of the layout used before
// (refs/zf/remote/*), which nothing reads any more.
func (c *Client) ConfigureChainFetch(ctx context.Context) (string, error) {
	stale, _ := c.output(ctx, "for-each-ref", "--format=%(refname)", "refs/zf/remote/")
	for _, ref := range strings.Fields(stale) {
		_, _ = c.output(ctx, "update-ref", "-d", ref)
	}

	remote, err := c.Remote()
	if err != nil {
		return "", fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return "", nil
	}

	key := "remote." + remote + ".fetch"
	// --get-all exits 1 when the key is unset: an empty list, not an error.
	existing, _ := c.output(ctx, "config", "--get-all", key)
	configured := strings.Split(existing, "\n")

	for _, ns := range []ChainRefs{ReviewRefs, IssueRefs} {
		spec := ns.FetchRefspec(remote)
		if slices.Contains(configured, spec) {
			continue
		}
		if _, err := c.output(ctx, "config", "--add", key, spec); err != nil {
			return "", fmt.Errorf("configure %s: %w", key, err)
		}
	}

	return remote, nil
}
```

- [ ] **Step 4: Call it from `init`**

Run `impact({target: "runE", direction: "upstream", file_path: "cmd/init/init.go"})` first.

In `cmd/init/init.go`, in `runE`, replace the final `return nil` (after the `for _, h := range managedHooks` loop) with:

```go
	remote, err := client.ConfigureChainFetch(cmd.Context())
	if err != nil {
		return fmt.Errorf("configure fetch refspecs: %w", err)
	}
	if remote != "" {
		fmt.Fprintf(cmd.OutOrStdout(),
			"remote %q: `git fetch` now brings reviews and issues (refs/zf/reviews/*, refs/zf/issues/*)\n", remote)
	}

	return nil
```

In the command's `Long` text, add this paragraph before the line that starts with `Run 'git zf install' first`:

```
It also adds two fetch refspecs to the remote, so that a plain 'git fetch'
brings the reviews and issues stored under refs/zf/ and 'git fetch --prune'
does not delete their tracking refs.

```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/init/... ./git/... -v -run "TestInit|TestChainRef"`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add git/chain_ref_sync.go cmd/init
git commit -m "feat: git zf init configures the fetch refspecs of refs/zf chains"
```

---

### Task 6: the review commands, the guards and `issue close` use chains

This task replaces every reader and writer of the blob in one go: a reader on the blob and a writer on the chain cannot coexist.

**Files:**
- Modify: `cmd/issueflow/review_guard.go`
- Modify: `cmd/review/request.go`, `start.go`, `deps.go`, `list.go`, `status_cmd.go`, `track.go`, `fetch.go`, `sync.go`, `guard.go`
- Modify: `cmd/issue/close.go` (`reviewPreflight`)
- Delete: `git/review_ref.go`, `git/review_ref_test.go`
- Create: `cmd/review/chain_e2e_test.go`
- Modify (port): `cmd/review/review_e2e_test.go`, `reject_reason_e2e_test.go`, `request_guard_e2e_test.go`, `guard_commit_test.go`, `cmd/issueflow/review_guard_test.go`, `cmd/commit/review_guard_test.go`, `cmd/issue/close_e2e_test.go`

**Interfaces:**
- Consumes: Task 4 (`reviewpkg.Load`, `List`, `Append`, `ReplaceLegacy`, `Fetch`, `Push`, `Sync`, `ErrLegacyReview`, `Op`, `State`, the `Op*` and `Status*` constants), `reviewtest.Seed`.
- Produces:
  - `func issueflow.ReviewBranchAhead(ctx context.Context, client *git.Client, slug, featureBranch string) (effective string, n int, err error)`
  - `issueflow.PendingReviewCommits` keeps its signature; it now returns `nil` for a closed review.
  - `recordReviewDecision`, `runReviewRequest`, `runReviewStart`, `reviewPreflight` keep their signatures.

- [ ] **Step 1: Impact analysis**

Run `impact({target: "<name>", direction: "upstream"})` for: `PendingReviewCommits`, `runReviewRequest`, `runReviewRequestInteractive`, `runReviewStart`, `inReviewBranches`, `ensureReviewRecord`, `recordReviewDecision`, `runReviewList`, `runReviewStatus`, `runTrackReviewer`, `runReviewGuard`, `reviewPreflight`, `ReadReviewRef`, `WriteReviewRef`, `PushReviewRef`, `FetchReviewRefs`, `FetchReviewRef`, `ListReviewRefs`, `DeleteReviewRef`. Report the blast radius. `reviewPreflight` and `PendingReviewCommits` sit on the close path: expect HIGH and tell the user before editing.

- [ ] **Step 2: Write the new failing tests**

Create `cmd/review/chain_e2e_test.go`:

```go
package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
)

// writeLegacyBlob points refs/zf/reviews/<slug> at a JSON blob, as git-zf did
// before reviews were commit chains.
func writeLegacyBlob(t *testing.T, dir, slug string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(`{"status":"in_review","round":1,"feature_sha":"x"}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	upd := exec.CommandContext(t.Context(), "git", "-C", dir,
		"update-ref", "refs/zf/reviews/"+slug, strings.TrimSpace(string(out)))
	if b, err := upd.CombinedOutput(); err != nil {
		t.Fatalf("update-ref: %v\n%s", err, b)
	}
}

func TestReviewRequest_ReplacesLegacyBlob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	writeLegacyBlob(t, rig.dir, "77")

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request succeeds over a legacy blob", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewRequest: %v", err)
		}
	})

	t.Run("the ref is now a chain in review at round 1", func(t *testing.T) {
		st, lErr := reviewpkg.Load(ctx, rig.client, "77")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, lErr)
		}
		if st.Status != reviewpkg.StatusInReview || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestReviewRequest_LegacyBlobKeepsReviewerCommits(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	writeLegacyBlob(t, rig.dir, "77")

	// The reviewer left a commit on 77@review that the feature branch lacks.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-q", "-b", "77@review", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout 77@review: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rig.dir, "nit.txt"), []byte("nit\n"), 0o644); err != nil {
		t.Fatalf("write nit.txt: %v", err)
	}
	for _, args := range [][]string{
		{"add", "nit.txt"},
		{"commit", "-q", "-m", "fix: reviewer nit"},
		{"checkout", "-q", "main"},
	} {
		if err := rig.client.RunGitAt(ctx, rig.dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request is refused while reviewer commits are unincorporated", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "unincorporated") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("the legacy blob and the review branch are left alone", func(t *testing.T) {
		if _, lErr := reviewpkg.Load(ctx, rig.client, "77"); !errors.Is(lErr, reviewpkg.ErrLegacyReview) {
			t.Errorf("Load err = %v, want ErrLegacyReview", lErr)
		}
		if exists, _ := rig.client.BranchExists("77@review"); !exists {
			t.Error("77@review was deleted")
		}
	})
}

func TestReviewList_HidesClosedReviews(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)

	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, "f1")
	reviewtest.Seed(t, rig.client, "78", reviewpkg.StatusInReview, 1, "f2")
	if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}

	rig.stdout.Reset()
	err := runReviewList(ctx, rig.deps())
	out := rig.stdout.String()

	t.Run("list succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewList: %v", err)
		}
	})
	t.Run("the open review is listed", func(t *testing.T) {
		if !strings.Contains(out, "78") {
			t.Errorf("output:\n%s", out)
		}
	})
	t.Run("the closed review is hidden", func(t *testing.T) {
		if strings.Contains(out, "77") {
			t.Errorf("output:\n%s", out)
		}
	})
}

func TestReviewRequest_AfterClose_StartsNextRound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)

	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, "f1")
	if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}

	err := runReviewRequest(ctx, rig.deps(), "77")

	t.Run("request on a closed review succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewRequest: %v", err)
		}
	})
	t.Run("the review reopens at round 2", func(t *testing.T) {
		st, lErr := reviewpkg.Load(ctx, rig.client, "77")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, lErr)
		}
		if st.Closed || st.Round != 2 || st.Status != reviewpkg.StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})
}
```

Add to `cmd/issue/close_e2e_test.go` (it needs the imports `reviewpkg "github.com/piprim/git-zf/review"` and `"github.com/piprim/git-zf/review/reviewtest"`):

```go
func TestClose_ReviewPreflight_LegacyBlobRefuses(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug

	cmd := exec.CommandContext(t.Context(), "git", "-C", rig.dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(`{"status":"in_review","round":1}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustRunGitAt(t, rig.dir, "update-ref", "refs/zf/reviews/"+slug, strings.TrimSpace(string(out)))

	cleanup, pErr := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close is refused with ErrLegacyReview", func(t *testing.T) {
		if !errors.Is(pErr, reviewpkg.ErrLegacyReview) || cleanup != nil {
			t.Fatalf("reviewPreflight = %v, cleanup nil: %v", pErr, cleanup == nil)
		}
	})
	t.Run("the message tells how to restart or drop the review", func(t *testing.T) {
		for _, want := range []string{"git zf review request", "git update-ref -d refs/zf/reviews/" + slug} {
			if !strings.Contains(pErr.Error(), want) {
				t.Errorf("message lacks %q:\n%v", want, pErr)
			}
		}
	})
}

func TestClose_ReviewPreflight_ClosedReviewIsIgnored(t *testing.T) {
	rig := newCloseRig(t)
	slug := rig.pickedBranchRow().IssueSlug

	reviewtest.Seed(t, rig.client, slug, reviewpkg.StatusInReview, 1, "f1")
	if err := reviewpkg.Append(t.Context(), rig.client, slug, &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
		t.Fatalf("append close: %v", err)
	}

	cleanup, pErr := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("a closed review does not lock the branch", func(t *testing.T) {
		if pErr != nil || cleanup != nil {
			t.Fatalf("reviewPreflight = %v, cleanup nil: %v", pErr, cleanup == nil)
		}
	})
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run: `mise exec -- go test ./cmd/review/... -run "TestReviewRequest_ReplacesLegacyBlob|TestReviewRequest_AfterClose" -v`
Expected: FAIL. `TestReviewRequest_ReplacesLegacyBlob` fails with "already in review": the current code decodes the blob. `TestReviewRequest_AfterClose_StartsNextRound` fails on the round.

Run: `mise exec -- go test ./cmd/issue/... -run "TestClose_ReviewPreflight_LegacyBlobRefuses|TestClose_ReviewPreflight_ClosedReviewIsIgnored" -v`
Expected: FAIL: the current preflight reads the blob as a live review and does not know `ErrLegacyReview`.

- [ ] **Step 4: Port `cmd/issueflow/review_guard.go`**

Add the import `reviewpkg "github.com/piprim/git-zf/review"`. Replace `PendingReviewCommits` with these two functions:

```go
// ReviewBranchAhead finds the review branch that counts for slug and how many
// commits it has that featureBranch lacks. effective is "42@review" or
// "origin/42@review", "" when there is no review branch. It reads only local
// refs, whatever the state of the review.
func ReviewBranchAhead(
	ctx context.Context, client *git.Client, slug, featureBranch string,
) (effective string, n int, err error) {
	reviewBranch := branch.ReviewBranchName(slug)
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
		return "", 0, nil
	}

	n, err = client.CommitsAhead(ctx, effective, featureBranch)

	return effective, n, err
}

// PendingReviewCommits reports reviewer commits awaiting incorporation for
// slug's featureBranch, or nil when nothing is pending. It reads only local
// refs — no network — so it is cheap enough for a pre-commit hook and works
// offline. The guard is armed only by a decided, open review: in_review means
// the reviewer hasn't decided, and a closed review or a stale review branch
// with no review never trips it.
func PendingReviewCommits(ctx context.Context, client *git.Client, slug, featureBranch string) (*PendingReview, error) {
	st, err := reviewpkg.Load(ctx, client, slug)
	if err != nil || st == nil || st.Closed {
		return nil, err
	}
	status := store.ReviewStatus(st.Status)
	if status != store.ReviewStatusApproved && status != store.ReviewStatusChangesRequested {
		return nil, nil
	}

	effective, n, err := ReviewBranchAhead(ctx, client, slug, featureBranch)
	if err != nil || n == 0 {
		return nil, err
	}

	return &PendingReview{EffectiveRef: effective, Commits: n, Status: status}, nil
}
```

- [ ] **Step 5: Port `cmd/review/deps.go`**

Add the import `reviewpkg "github.com/piprim/git-zf/review"`; remove `"time"`.

`inReviewBranches`: replace the fetch, the listing and the loop with:

```go
	// Fetch latest state and push anything still local (best-effort).
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	states, warnings, err := reviewpkg.List(ctx, deps.client)
	if err != nil {
		return nil, fmt.Errorf("list review refs: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	var result []store.BranchRow
	for _, st := range states {
		if st.Closed || st.Status != reviewpkg.StatusInReview {
			continue
		}
		// Build a synthetic BranchRow from the review. The reviewer's branch
		// follows the <IssueID>@review convention.
		result = append(result, store.BranchRow{
			IssueSlug:  st.Slug,
			BranchName: branch.ReviewBranchName(st.Slug),
			Title:      st.Slug,
		})
	}
	return result, nil
```

`ensureReviewRecord`: replace its first statement

```go
	ref, _, refErr := deps.client.ReadReviewRef(ctx, issueSlug)
```

with

```go
	ref, refErr := reviewpkg.Load(ctx, deps.client, issueSlug)
```

The rest of the function is unchanged: `ref.Status`, `ref.Round` and `ref.Reviewer` have the same names on `*reviewpkg.State`.

`recordReviewDecision`: replace everything from `currentRef, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)` down to and including the `PushReviewRef` block with:

```go
	st, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return reviewDecision{}, fmt.Errorf("read review ref: %w", err)
	}

	op := &reviewpkg.Op{Type: reviewpkg.OpReject, Comment: comment, HasCommits: d.hasCommits}
	if status == store.ReviewStatusApproved {
		// What the reviewer approved: their review branch when they have one,
		// else the commit the developer submitted.
		approved := st.FeatureSHA
		if d.branchExists {
			tip, tipErr := deps.client.ResolveRef("refs/heads/" + d.reviewBranch)
			if tipErr != nil {
				return reviewDecision{}, fmt.Errorf("resolve %s: %w", d.reviewBranch, tipErr)
			}
			approved = tip.String()
		}
		op = &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: approved, HasCommits: d.hasCommits}
	}

	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, false); err != nil {
		return reviewDecision{}, fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, deps.client, issueSlug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}
```

`ensureReviewRecord` ran just before and returned an error for a missing review, so `st` is not nil here. In the function's doc comment, "writes and pushes the review ref FIRST" stays true.

- [ ] **Step 6: Port `cmd/review/request.go`**

Add the imports `"errors"` and `reviewpkg "github.com/piprim/git-zf/review"`; remove `"time"` and `"github.com/piprim/git-zf/git"` if nothing else in the file uses them.

In `runReviewRequestInteractive`, replace from `_ = deps.client.FetchReviewRefs(ctx)` to the end of the `for _, b := range branches` loop with:

```go
	_ = reviewpkg.Sync(ctx, deps.client)
	states, _, _ := reviewpkg.List(ctx, deps.client)
	locked := make(map[string]bool, len(states))
	for _, st := range states {
		if !st.Closed && (st.Status == reviewpkg.StatusInReview || st.Status == reviewpkg.StatusApproved) {
			locked[st.Slug] = true
		}
	}

	var submittable []store.BranchRow
	for _, b := range branches {
		if !locked[b.IssueSlug] {
			submittable = append(submittable, b)
		}
	}
```

Replace the whole of `runReviewRequest` with:

```go
func runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug string) error {
	// Sync first so Load reflects what the remote has, and so the request op
	// lands on the existing chain rather than beside it.
	_ = reviewpkg.Sync(ctx, deps.client)

	// A blob ref written by an older git-zf is not migrated: this request
	// replaces it, once every check below has passed.
	existing, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	legacy := errors.Is(err, reviewpkg.ErrLegacyReview)
	if err != nil && !legacy {
		return fmt.Errorf("read review ref: %w", err)
	}
	if existing != nil && existing.Status == reviewpkg.StatusInReview {
		return fmt.Errorf("issue %q is already in review (round %d) — awaiting reviewer decision",
			issueSlug, existing.Round)
	}

	// Find the feature branch for this issue.
	branches, err := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	var featureBranch string
	for _, b := range branches {
		if b.IssueSlug == issueSlug && b.Status == store.BranchStatusInProgress {
			featureBranch = b.BranchName
			break
		}
	}
	if featureBranch == "" {
		return fmt.Errorf("no in-progress branch found for issue %q", issueSlug)
	}

	featureSHA, err := deps.client.ResolveRef("refs/heads/" + featureBranch)
	if err != nil {
		return fmt.Errorf("resolve feature branch HEAD: %w", err)
	}

	// Refuse to delete reviewer work that was never incorporated. This is the
	// safety net; the interactive wrapper offers an inline merge first.
	// A detection error must also refuse (fail closed) since we're about to
	// irreversibly delete the local and remote review branch below. The state
	// of a legacy review is unknown, so any reviewer commit counts.
	var pending *issueflow.PendingReview
	if legacy {
		effective, n, aErr := issueflow.ReviewBranchAhead(ctx, deps.client, issueSlug, featureBranch)
		if aErr != nil {
			return fmt.Errorf("detect pending review commits: %w", aErr)
		}
		if n > 0 {
			pending = &issueflow.PendingReview{EffectiveRef: effective, Commits: n}
		}
	} else {
		var pErr error
		pending, pErr = issueflow.PendingReviewCommits(ctx, deps.client, issueSlug, featureBranch)
		if pErr != nil {
			return fmt.Errorf("detect pending review commits: %w", pErr)
		}
	}
	if pending != nil {
		return fmt.Errorf(
			"%s has %d unincorporated reviewer commit(s) from the previous round.\n"+
				"Run 'git zf review sync' to incorporate them first "+
				"(or delete the branch to discard them), then re-request",
			pending.EffectiveRef, pending.Commits)
	}

	// Delete any stale review branch from a previous rejected round.
	reviewBranch := branch.ReviewBranchName(issueSlug)
	if exists, _ := deps.client.BranchExists(reviewBranch); exists {
		if err := deps.client.DeleteLocalBranch(ctx, reviewBranch, true); err != nil {
			fmt.Fprintf(deps.client.IO().Err, "warning: delete stale %s: %v\n", reviewBranch, err)
		}
		_ = deps.client.DeleteRemoteBranch(ctx, reviewBranch)
	}

	if legacy {
		if err := reviewpkg.ReplaceLegacy(ctx, deps.client, issueSlug); err != nil {
			return err
		}
	}

	// Write and push the request op (the chain is the source of truth), then
	// mirror the round in the store.
	op := &reviewpkg.Op{Type: reviewpkg.OpRequest, FeatureSHA: featureSHA.String()}
	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, false); err != nil {
		return fmt.Errorf("write review ref: %w", err)
	}

	if err := reviewpkg.Push(ctx, deps.client, issueSlug); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
	}

	st, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil || st == nil {
		return fmt.Errorf("read review ref after request: %w", err)
	}

	reviewRow, err := deps.store.InsertReview(ctx, issueSlug, "")
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}
	// InsertReview counts the rows of this clone; the chain knows the round.
	if reviewRow.Round != st.Round {
		if err := deps.store.SetReviewRound(ctx, reviewRow.ID, st.Round); err == nil {
			reviewRow.Round = st.Round
		}
	}

	fmt.Fprintf(deps.client.IO().Out,
		"Issue %q is now in review (round %d). Branch %q is locked.\n"+
			"Share with your reviewer: git fetch && git zf review start\n",
		issueSlug, st.Round, featureBranch)

	if err := proposeReviewPush(ctx, deps, featureBranch); err != nil {
		return err
	}

	return nil
}
```

- [ ] **Step 7: Port `cmd/review/start.go`**

Add the import `reviewpkg "github.com/piprim/git-zf/review"`.

In `runReviewStart`, replace the first block

```go
	ref, currentSHA, err := deps.client.ReadReviewRef(ctx, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil {
```

with

```go
	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil {
		return fmt.Errorf("read review ref: %w", err)
	}
	if ref == nil || ref.Closed {
```

and replace the inner block that rewrites the blob

```go
		if ref.Reviewer == "" {
			updatedRef := *ref
			updatedRef.Reviewer = reviewer
			if _, writeErr := deps.client.WriteReviewRef(ctx, issueSlug, updatedRef, currentSHA); writeErr == nil {
				// Push so the developer can see who started the review.
				if pushErr := deps.client.PushReviewRef(ctx, issueSlug, currentSHA); pushErr != nil {
					fmt.Fprintf(deps.client.IO().Err, "warning: push reviewer identity: %v\n", pushErr)
				}
			}
		}
```

with

```go
		if ref.Reviewer == "" {
			startOp := &reviewpkg.Op{Type: reviewpkg.OpStart}
			if writeErr := reviewpkg.Append(ctx, deps.client, issueSlug, startOp, false); writeErr == nil {
				// Push so the developer can see who started the review.
				if pushErr := reviewpkg.Push(ctx, deps.client, issueSlug); pushErr != nil {
					fmt.Fprintf(deps.client.IO().Err, "warning: push reviewer identity: %v\n", pushErr)
				}
			}
		}
```

`ref.Status`, `ref.FeatureSHA` and `ref.Round` are unchanged. `ref.Status != string(store.ReviewStatusInReview)` keeps compiling: `Status` is a `string`.

- [ ] **Step 8: Port the read-only commands**

Each file gets the import `reviewpkg "github.com/piprim/git-zf/review"`.

`cmd/review/list.go`: replace `runReviewList` and drop the `store` import and the comment line `// store is used indirectly …`:

```go
func runReviewList(ctx context.Context, deps reviewDeps) error {
	if err := reviewpkg.Sync(ctx, deps.client); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: sync review refs: %v\n", err)
	}

	// Read directly from git refs — works even when the reviewer's store is
	// empty (fresh clone that never ran git zf issue start).
	states, warnings, err := reviewpkg.List(ctx, deps.client)
	if err != nil {
		return fmt.Errorf("list review refs: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintln(deps.client.IO().Err, w)
	}

	printed := 0
	for _, st := range states {
		if st.Closed || (st.Status != reviewpkg.StatusInReview && st.Status != reviewpkg.StatusApproved) {
			continue
		}
		fmt.Fprintf(deps.client.IO().Out, "%-12s  round %-2d  %s\n", st.Slug, st.Round, st.Status)
		printed++
	}

	if printed == 0 {
		fmt.Fprintln(deps.client.IO().Out, "No issues currently in review.")
	}

	return nil
}
```

`cmd/review/fetch.go`: in `runReviewFetch`, replace `deps.client.FetchReviewRefs(ctx)` with `reviewpkg.Sync(ctx, deps.client)`.

`cmd/review/sync.go` line 33: replace `deps.client.FetchReviewRefs(ctx)` with `reviewpkg.Sync(ctx, deps.client)`.

`cmd/review/status_cmd.go`: replace both `deps.client.FetchReviewRefs(ctx)` with `reviewpkg.Sync(ctx, deps.client)`, and `ref, _, _ := deps.client.ReadReviewRef(ctx, issueSlug)` with `ref, _ := reviewpkg.Load(ctx, deps.client, issueSlug)`.

`cmd/review/track.go`:
- line 85: `if ref, _, _ := deps.client.ReadReviewRef(ctx, b.IssueID()); ref != nil &&` becomes `if ref, _ := reviewpkg.Load(ctx, deps.client, b.IssueID()); ref != nil && !ref.Closed &&`
- line 107: `deps.client.FetchReviewRefs(ctx)` becomes `reviewpkg.Sync(ctx, deps.client)`
- line 112: `ref, _, err := deps.client.ReadReviewRef(ctx, issueSlug)` becomes `ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)`

`cmd/review/guard.go`: in `runReviewGuard`, replace from `deps.client.FetchReviewRef(ctx, issueSlug)` through the `if err != nil || ref == nil` block with:

```go
	// Fetch the latest decision for this issue before checking — the reviewer
	// may have approved or rejected after the developer last fetched. Silent
	// and best-effort: if the fetch fails we fall back to the local chain.
	_ = reviewpkg.Fetch(ctx, deps.client, true)

	ref, err := reviewpkg.Load(ctx, deps.client, issueSlug)
	if err != nil || ref == nil || ref.Closed {
		return nil // fail-open, legacy blob included
	}
```

- [ ] **Step 9: Port `cmd/issue/close.go`**

Add the import `reviewpkg "github.com/piprim/git-zf/review"`.

In `reviewPreflight`, replace from `_ = deps.client.FetchReviewRefs(ctx)` through the `if ref == nil { … return nil, nil }` block with:

```go
	// Sync reviews (best-effort) so we see the reviewer's latest decision even
	// if the developer has not fetched since submitting for review.
	_ = reviewpkg.Sync(ctx, deps.client)

	// Read the chain — authoritative source of truth.
	ref, refErr := reviewpkg.Load(ctx, deps.client, picked.IssueSlug)
	if errors.Is(refErr, reviewpkg.ErrLegacyReview) {
		// Not treated as "no review": the blob may be a lock.
		drop := "git update-ref -d refs/zf/reviews/" + picked.IssueSlug
		if remote, _ := deps.client.Remote(); remote != "" {
			drop += " && git push " + remote + " --delete refs/zf/reviews/" + picked.IssueSlug
		}

		return nil, fmt.Errorf(
			"issue %q has a review written by an older git-zf.\n"+
				"Run `git zf review request` to restart it, or drop it with:\n  %s\n%w",
			picked.IssueSlug, drop, refErr)
	}
	if refErr != nil {
		return nil, fmt.Errorf("read review ref: %w", refErr)
	}

	if ref == nil || ref.Closed {
		// No open review — either none was submitted, or the issue was
		// already closed once. Proceed.
		return nil, nil
	}
```

Update the function's doc comment: "The git ref (refs/zf/reviews/<IssueID>) is the source of truth" stays; change "always fetches and reads the ref first" to "always syncs and reads the chain first".

In the cleanup closure of the approved case, replace

```go
			// Always clean up the review ref (local + remote) on close, regardless
			// of whether a review branch existed.
			_ = deps.client.DeleteReviewRef(ctx, picked.IssueSlug)
```

with

```go
			// The review is kept as an audit trail and marked closed, whether
			// or not a review branch existed.
			closeOp := &reviewpkg.Op{Type: reviewpkg.OpClose}
			if err := reviewpkg.Append(ctx, deps.client, picked.IssueSlug, closeOp, false); err != nil {
				fmt.Fprintf(deps.client.IO().Err, "warning: mark review closed: %v\n", err)
			} else if err := reviewpkg.Push(ctx, deps.client, picked.IssueSlug); err != nil {
				fmt.Fprintf(deps.client.IO().Err, "warning: push review ref: %v\n", err)
			}
```

Update the comment above the closure: "and always drop the review ref" becomes "and always mark the review closed".

`"errors"` is already imported in `close.go` (it declares `ErrBranchLockedForReview`).

- [ ] **Step 10: Delete the blob API**

```bash
git rm git/review_ref.go git/review_ref_test.go
mise exec -- go build ./...
```

Expected: the build of non-test code passes. If it reports a remaining use of `ReadReviewRef`, `WriteReviewRef`, `PushReviewRef`, `FetchReviewRefs`, `FetchReviewRef`, `ListReviewRefs`, `DeleteReviewRef` or `git.ReviewRef`, port it with the rules of Step 11. `git/blob_ref.go` stays: `git/branch_ref.go` uses it.

- [ ] **Step 11: Port the existing tests**

Run `mise exec -- go vet ./...` to list every test that no longer compiles. Port each site by these rules. Test files in package `review` (`cmd/review`) import `reviewpkg "github.com/piprim/git-zf/review"`; add `"github.com/piprim/git-zf/review/reviewtest"` where `Seed` is used.

| Before | After |
|---|---|
| `ref, _, err := c.ReadReviewRef(ctx, slug)` | `ref, err := reviewpkg.Load(ctx, c, slug)` |
| `ref, _, _ := c.ReadReviewRef(ctx, slug)` | `ref, _ := reviewpkg.Load(ctx, c, slug)` |
| Seeding a state: `c.WriteReviewRef(ctx, slug, git.ReviewRef{Status: S, Round: N, FeatureSHA: F, …}, "")` | `reviewtest.Seed(t, c, slug, S, N, F)` |
| Simulating a reviewer decision: read the ref, set `Status` to approved or changes_requested, `WriteReviewRef(ctx, slug, x, sha)` | `reviewpkg.Append(ctx, c, slug, &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: ref.FeatureSHA}, false)` or `&reviewpkg.Op{Type: reviewpkg.OpReject}` |
| `c.PushReviewRef(ctx, slug, sha)` | `reviewpkg.Push(ctx, c, slug)` |
| `git push --force origin refs/zf/reviews/<slug>` run by a test | `reviewpkg.Push(ctx, c, slug)` |
| `c.FetchReviewRefs(ctx)` | `reviewpkg.Fetch(ctx, c, false)` |
| An assertion that the review ref is gone after a close (`ref != nil` is fatal) | Assert `ref != nil && ref.Closed`; rename the subtest from "review ref cleaned up" to "review kept and marked closed" |
| An assertion that the ref survives an aborted close (`ref == nil` is fatal) | Keep it, and add `ref.Closed` must be false |
| A test of the CAS or `--force-with-lease` rejection itself | Delete it: the case cannot occur. `TestTwoClonesApproveOffline` (Task 4) covers concurrent writers |
| A test that a ref deleted on the remote is pruned locally | Delete it: review refs are never deleted |
| `store.ReviewStatus…` passed as a status to a seed helper | `string(status)` at the `reviewtest.Seed` call |

Known sites:

- `cmd/issue/close_e2e_test.go`: the helper `seedReviewRef` (line 835) becomes:

```go
// seedReviewRef writes a review chain so reviewPreflight (which reads the
// chain as authoritative) can see the correct status in tests.
func seedReviewRef(t *testing.T, rig *closeTestRig, issueSlug string, status store.ReviewStatus, round int) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", rig.dir, "rev-parse", "ABC-1@feat@add-thing").Output()
	if err != nil {
		t.Fatalf("rev-parse feature branch: %v", err)
	}

	reviewtest.Seed(t, rig.client, issueSlug, string(status), round, strings.TrimSpace(string(out)))
}
```

  The seeds at lines 1001, 1286 and 2380 and the reads at 1911, 1923, 1971, 2427, 2468 and 2872 follow the table.

- `cmd/review/review_e2e_test.go`: `readRemoteReviewRef` (line 382) becomes the function below; its callers keep reading `ref.Status` and `ref.Round`. Remove the `encoding/json` import if nothing else uses it.

```go
// readRemoteReviewRef folds refs/zf/reviews/<issueID> as the bare origin has
// it, bypassing the local refs. nil when the origin has no such ref.
func readRemoteReviewRef(t *testing.T, originDir, issueID string) *reviewpkg.State {
	t.Helper()

	ref := "refs/zf/reviews/" + issueID
	out, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"rev-list", "--topo-order", "--reverse", "--parents", ref).Output()
	if err != nil {
		return nil // ref does not exist
	}

	var ops []reviewpkg.Op
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		payload, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
			"cat-file", "blob", fields[0]+":op.json").Output()
		if err != nil {
			t.Fatalf("cat-file %s:op.json: %v", fields[0], err)
		}
		op, ok := reviewpkg.DecodeOp(fields[0], fields[1:], payload)
		if !ok {
			t.Fatalf("malformed op %s on origin", fields[0])
		}
		ops = append(ops, op)
	}

	st := reviewpkg.Fold(issueID, ops)

	return &st
}
```

- `cmd/review/reject_reason_e2e_test.go` line 193: the "not in review any more" setup becomes one `reviewpkg.Append` of an `approve` op.
- `cmd/review/guard_commit_test.go:36`, `cmd/issueflow/review_guard_test.go:82`, `cmd/commit/review_guard_test.go:75`, `cmd/review/request_guard_e2e_test.go:61`: seeds and reads by the table.

- [ ] **Step 12: Run the suites**

Run: `mise exec -- go test ./cmd/review/... ./cmd/issueflow/... ./cmd/commit/... ./review/... ./git/... -v 2>&1 | tail -40`
Expected: PASS, including the four tests of `chain_e2e_test.go`.

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v 2>&1 | tail -40`
Expected: PASS, including `TestClose_ReviewPreflight_LegacyBlobRefuses` and `TestClose_ReviewPreflight_ClosedReviewIsIgnored`.

Run: `mise exec -- go test ./...`
Expected: PASS.

- [ ] **Step 13: Commit**

```bash
git add -A cmd git review
git commit -m "feat: reviews are commit chains, kept and marked closed after close"
```

---

### Task 7: the signing gate

**Files:**
- Create: `internal/gittest/gittest.go`
- Create: `git/commit_sig.go`, `git/commit_sig_test.go`
- Modify: `config/config.go`, `config/default.toml`, `config/config_test.go`
- Modify: `review/repo.go` (`SignatureState`), `review/repo_test.go`
- Modify: `cmd/review/deps.go` (`recordReviewDecision`), `cmd/review/status_cmd.go`
- Modify: `cmd/issue/close.go` (`reviewPreflight`, new `checkSignedApproval`)
- Modify: `cmd/review/chain_e2e_test.go`, `cmd/issue/close_e2e_test.go`

**Interfaces:**
- Consumes: Task 2 (`WriteChainRoot` with `sign`), Task 4 (`Append` with `sign`, `State.Approvals`), Task 6 (ported `recordReviewDecision`, `reviewPreflight`).
- Produces:
  - `func gittest.SSHSigner(t testing.TB, dir string)`
  - `func (c *Client) VerifyCommit(ctx context.Context, sha string) error`
  - `func (c *Client) CommitSigned(ctx context.Context, sha string) (bool, error)`
  - `type config.ReviewConfig struct { RequireSigned bool }`, field `AppConfig.Review`
  - `const reviewpkg.SigVerified = "verified"`, `SigUnverified = "signed, not verified"`, `SigNone = "unsigned"`
  - `func reviewpkg.SignatureState(ctx context.Context, c *git.Client, commit string) string`
  - `var ErrApprovalNotSigned error` in `cmd/issue`

- [ ] **Step 1: Write the signing test helper**

Create `internal/gittest/gittest.go`:

```go
// Package gittest holds test helpers for packages that drive real
// repositories. It imports nothing from this module, so any package's tests
// can use it.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// SSHSigner makes the repository at dir able to sign commits with a fresh SSH
// key and to verify them: gpg.format, user.signingkey and
// gpg.ssh.allowedSignersFile are set in its local configuration.
// commit.gpgsign is left as it is. The test is skipped when ssh-keygen is not
// installed.
func SSHSigner(t testing.TB, dir string) {
	t.Helper()

	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not installed")
	}

	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "key")
	run(t, keyDir, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "signer@test.com", "-f", key)

	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}

	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(allowed, append([]byte("signer@test.com "), pub...), 0o600); err != nil {
		t.Fatalf("write allowed signers: %v", err)
	}

	for _, kv := range [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingkey", key + ".pub"},
		{"gpg.ssh.allowedSignersFile", allowed},
	} {
		run(t, dir, "git", "config", kv[0], kv[1])
	}
}

func run(t testing.TB, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}
```

- [ ] **Step 2: Write the failing git-level test**

Create `git/commit_sig_test.go`:

```go
package git

import (
	"testing"

	"github.com/piprim/git-zf/internal/gittest"
)

func TestCommitSignature(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()
	mustGit(t, dir, "config", "commit.gpgsign", "false")
	gittest.SSHSigner(t, dir)

	unsigned, err := client.WriteChainRoot(ctx, []byte(`{}`), "unsigned", false)
	if err != nil {
		t.Fatalf("WriteChainRoot unsigned: %v", err)
	}
	signed, err := client.WriteChainRoot(ctx, []byte(`{}`), "signed", true)
	if err != nil {
		t.Fatalf("WriteChainRoot signed: %v", err)
	}

	t.Run("sign=true signs although commit.gpgsign is false", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, signed); err != nil || !ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
	})

	t.Run("sign=false with commit.gpgsign false leaves the commit unsigned", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, unsigned); err != nil || ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
	})

	t.Run("VerifyCommit accepts a signature from an allowed signer", func(t *testing.T) {
		if err := client.VerifyCommit(ctx, signed); err != nil {
			t.Errorf("VerifyCommit: %v", err)
		}
	})

	t.Run("VerifyCommit rejects an unsigned commit", func(t *testing.T) {
		if err := client.VerifyCommit(ctx, unsigned); err == nil {
			t.Error("VerifyCommit accepted an unsigned commit")
		}
	})

	mustGit(t, dir, "config", "--unset", "gpg.ssh.allowedSignersFile")

	t.Run("a signature git cannot check is signed but not verified", func(t *testing.T) {
		if ok, err := client.CommitSigned(ctx, signed); err != nil || !ok {
			t.Errorf("CommitSigned = %v, %v", ok, err)
		}
		if err := client.VerifyCommit(ctx, signed); err == nil {
			t.Error("VerifyCommit accepted a signature with no allowed signers")
		}
	})

	mustGit(t, dir, "config", "user.signingkey", "/nonexistent/key.pub")

	t.Run("sign=true fails when the key cannot be loaded", func(t *testing.T) {
		if _, err := client.WriteChainRoot(ctx, []byte(`{}`), "x", true); err == nil {
			t.Error("WriteChainRoot signed without a usable key")
		}
	})
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `mise exec -- go test ./git/... -run TestCommitSignature -v`
Expected: FAIL, build errors `client.CommitSigned undefined`, `client.VerifyCommit undefined`.

- [ ] **Step 4: Write `git/commit_sig.go`**

```go
package git

import (
	"context"
	"fmt"
	"strings"
)

// VerifyCommit reports whether sha carries a signature git trusts: nil when
// `git verify-commit` succeeds. Trust is git's own, the GPG keyring or
// gpg.ssh.allowedSignersFile.
func (c *Client) VerifyCommit(ctx context.Context, sha string) error {
	if _, err := c.output(ctx, "verify-commit", sha); err != nil {
		return fmt.Errorf("verify-commit %s: %w", sha, err)
	}

	return nil
}

// CommitSigned reports whether sha carries a signature at all, valid or not.
// It reads the commit header rather than `%G?`, which prints N for an SSH
// signature when gpg.ssh.allowedSignersFile is not configured.
func (c *Client) CommitSigned(ctx context.Context, sha string) (bool, error) {
	raw, err := c.output(ctx, "cat-file", "commit", sha)
	if err != nil {
		return false, fmt.Errorf("cat-file commit %s: %w", sha, err)
	}

	header, _, _ := strings.Cut(raw, "\n\n")
	for line := range strings.SplitSeq(header, "\n") {
		// "gpgsig" in a SHA-1 repository, "gpgsig-sha256" in a SHA-256 one.
		if strings.HasPrefix(line, "gpgsig") {
			return true, nil
		}
	}

	return false, nil
}
```

Run: `mise exec -- go test ./git/... -run TestCommitSignature -v`
Expected: PASS (or SKIP with "ssh-keygen not installed").

- [ ] **Step 5: Add the configuration (test first)**

Append to `config/config_test.go`:

```go
func TestLoadReviewRequireSigned(t *testing.T) {
	t.Parallel()

	t.Run("defaults to false", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Review.RequireSigned {
			t.Fatalf("Review.RequireSigned = true, want false (default)")
		}
	})

	t.Run("can be set to true", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(writeTOML(t, "[review]\nrequire-signed = true\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.Review.RequireSigned {
			t.Fatalf("Review.RequireSigned = false, want true (override)")
		}
	})
}
```

Run: `mise exec -- go test ./config/... -run TestLoadReviewRequireSigned -v`
Expected: FAIL, build error `cfg.Review undefined`.

In `config/config.go`, add after `PushConfig`:

```go
// ReviewConfig holds review settings. RequireSigned makes `review approve`
// sign its op, and `issue close` refuse an approval whose signature git does
// not trust or that does not cover the branch tip.
type ReviewConfig struct {
	RequireSigned bool `json:"require-signed" toml:"require-signed"`
}
```

and in `AppConfig`, after the `Push` field:

```go
	Review        ReviewConfig        `json:"review"         toml:"review"`
```

Append to `config/default.toml`:

```toml

[review]
require-signed = false
```

Run: `mise exec -- go test ./config/... -v -run "TestLoadReviewRequireSigned|TestDefaultTOML"`
Expected: PASS.

- [ ] **Step 6: `SignatureState` (test first)**

Append to `review/repo_test.go` (add the import `"github.com/piprim/git-zf/internal/gittest"`):

```go
func TestSignatureState(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	dir := c.WorkingTreeRoot()
	ctx := t.Context()
	gittest.SSHSigner(t, dir)

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	mustAppend(t, c, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})
	if err := Append(ctx, c, "42", &Op{Type: OpApprove, ApprovedSHA: "f1"}, true); err != nil {
		t.Fatalf("signed Append: %v", err)
	}
	st := mustLoad(t, c, "42")
	if len(st.Approvals) != 2 {
		t.Fatalf("approvals = %+v", st.Approvals)
	}

	t.Run("an unsigned approval is unsigned", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[0].Commit); got != SigNone {
			t.Errorf("SignatureState = %q", got)
		}
	})

	t.Run("an approval signed by an allowed signer is verified", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[1].Commit); got != SigVerified {
			t.Errorf("SignatureState = %q", got)
		}
	})

	runGit(t, dir, "config", "--unset", "gpg.ssh.allowedSignersFile")

	t.Run("a signature git cannot check is signed, not verified", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[1].Commit); got != SigUnverified {
			t.Errorf("SignatureState = %q", got)
		}
	})
}
```

Run: `mise exec -- go test ./review/... -run TestSignatureState -v`
Expected: FAIL, build error `undefined: SignatureState`.

Append to `review/repo.go`:

```go
// What SignatureState reports for an op commit.
const (
	SigVerified   = "verified"
	SigUnverified = "signed, not verified"
	SigNone       = "unsigned"
)

// SignatureState tells whether the op commit carries a signature git trusts
// (SigVerified), a signature git cannot vouch for (SigUnverified), or none
// (SigNone).
func SignatureState(ctx context.Context, c *git.Client, commit string) string {
	if c.VerifyCommit(ctx, commit) == nil {
		return SigVerified
	}
	if signed, _ := c.CommitSigned(ctx, commit); signed {
		return SigUnverified
	}

	return SigNone
}
```

Run: `mise exec -- go test ./review/... -run TestSignatureState -v`
Expected: PASS.

- [ ] **Step 7: `review approve` signs under the flag (test first)**

Append to `cmd/review/chain_e2e_test.go` (add the import `"github.com/piprim/git-zf/internal/gittest"`):

```go
func TestReviewApprove_RequireSigned(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	rig.cfg.Review.RequireSigned = true

	// No usable key yet: signing must fail.
	for _, kv := range [][2]string{{"gpg.format", "ssh"}, {"user.signingkey", "/nonexistent/key.pub"}} {
		if err := rig.client.RunGitAt(ctx, rig.dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("git config %s: %v", kv[0], err)
		}
	}
	failErr := runReviewApprove(ctx, rig.deps(), "77")

	t.Run("approve fails when the op cannot be signed", func(t *testing.T) {
		if failErr == nil {
			t.Fatal("approve succeeded without a usable signing key")
		}
	})

	t.Run("no approve op is written on failure", func(t *testing.T) {
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil {
			t.Fatalf("Load = %v, %v", st, err)
		}
		if st.Status != reviewpkg.StatusInReview || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	gittest.SSHSigner(t, rig.dir)
	okErr := runReviewApprove(ctx, rig.deps(), "77")

	t.Run("approve succeeds with a signing key", func(t *testing.T) {
		if okErr != nil {
			t.Fatalf("runReviewApprove: %v", okErr)
		}
	})

	t.Run("the approve op is signed although commit.gpgsign is false", func(t *testing.T) {
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil || len(st.Approvals) != 1 {
			t.Fatalf("Load = %+v, %v", st, err)
		}
		if got := reviewpkg.SignatureState(ctx, rig.client, st.Approvals[0].Commit); got != reviewpkg.SigVerified {
			t.Errorf("SignatureState = %q", got)
		}
	})
}
```

Run: `mise exec -- go test ./cmd/review/... -run TestReviewApprove_RequireSigned -v`
Expected: FAIL: "approve succeeded without a usable signing key".

In `cmd/review/deps.go`, in `recordReviewDecision`, replace

```go
	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, false); err != nil {
```

with

```go
	// review.require-signed: the approval is signed whatever commit.gpgsign
	// says, and the command fails when it cannot be.
	sign := status == store.ReviewStatusApproved && deps.cfg.Review.RequireSigned
	if err := reviewpkg.Append(ctx, deps.client, issueSlug, op, sign); err != nil {
```

Run: `mise exec -- go test ./cmd/review/... -run TestReviewApprove_RequireSigned -v`
Expected: PASS.

- [ ] **Step 8: `review status` shows the signature state**

In `cmd/review/status_cmd.go`, in `runReviewStatus`, add before the final `return nil`:

```go
	if ref != nil && len(ref.Approvals) > 0 {
		fmt.Fprintf(deps.client.IO().Out, "\nRound %d approvals:\n", ref.Round)
		for _, a := range ref.Approvals {
			fmt.Fprintf(deps.client.IO().Out, "  %s  %s\n",
				a.Author, reviewpkg.SignatureState(ctx, deps.client, a.Commit))
		}
	}
```

Append to `cmd/review/chain_e2e_test.go`:

```go
func TestReviewStatus_ShowsSignatureState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
		t.Fatalf("runReviewApprove: %v", err)
	}

	rig.stdout.Reset()
	err := runReviewStatus(ctx, rig.deps(), "77")
	out := rig.stdout.String()

	t.Run("status succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewStatus: %v", err)
		}
	})
	t.Run("the approval is listed as unsigned", func(t *testing.T) {
		if !strings.Contains(out, "Round 1 approvals:") || !strings.Contains(out, reviewpkg.SigNone) {
			t.Errorf("output:\n%s", out)
		}
	})
}
```

Run: `mise exec -- go test ./cmd/review/... -run TestReviewStatus_ShowsSignatureState -v`
Expected: PASS.

- [ ] **Step 9: Write the failing close-gate tests**

Append to `cmd/issue/close_e2e_test.go` (add the import `"github.com/piprim/git-zf/internal/gittest"`):

```go
// seedApproval puts slug in review at round 1, then appends one approve op
// covering approvedSHA, signed when sign is true.
func seedApproval(t *testing.T, rig *closeTestRig, slug, approvedSHA string, sign bool) {
	t.Helper()

	feature := revParseInDir(t, rig.dir, "refs/heads/"+rig.pickedBranchRow().BranchName)
	if st, _ := reviewpkg.Load(t.Context(), rig.client, slug); st == nil {
		reviewtest.Seed(t, rig.client, slug, reviewpkg.StatusInReview, 1, feature)
	}

	op := &reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: approvedSHA}
	if err := reviewpkg.Append(t.Context(), rig.client, slug, op, sign); err != nil {
		t.Fatalf("append approve: %v", err)
	}
}

func newSignedCloseRig(t *testing.T) (*closeTestRig, string, string) {
	t.Helper()

	rig := newCloseRig(t)
	rig.cfg.Review.RequireSigned = true
	mustRunGitAt(t, rig.dir, "config", "commit.gpgsign", "false")
	gittest.SSHSigner(t, rig.dir)

	return rig, rig.pickedBranchRow().IssueSlug, rig.pickedBranchRow().BranchName
}

func TestClose_SignedGate_UnsignedApprovalRefused(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	seedApproval(t, rig, slug, revParseInDir(t, rig.dir, "refs/heads/"+feature), false)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close is refused with ErrApprovalNotSigned", func(t *testing.T) {
		if !errors.Is(err, ErrApprovalNotSigned) || cleanup != nil {
			t.Fatalf("reviewPreflight = %v", err)
		}
	})
	t.Run("the message lists the approver as unsigned and names the way out", func(t *testing.T) {
		for _, want := range []string{reviewpkg.SigNone, "git zf review request"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message lacks %q:\n%v", want, err)
			}
		}
	})
}

func TestClose_SignedGate_SignedApprovalPasses(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	seedApproval(t, rig, slug, revParseInDir(t, rig.dir, "refs/heads/"+feature), true)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close proceeds", func(t *testing.T) {
		if err != nil || cleanup == nil {
			t.Fatalf("reviewPreflight = %v, cleanup nil: %v", err, cleanup == nil)
		}
	})
}

func TestClose_SignedGate_FlagOffIgnoresSignatures(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	rig.cfg.Review.RequireSigned = false
	seedApproval(t, rig, slug, revParseInDir(t, rig.dir, "refs/heads/"+feature), false)

	_, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("an unsigned approval closes when the flag is off", func(t *testing.T) {
		if err != nil {
			t.Fatalf("reviewPreflight: %v", err)
		}
	})
}

func TestClose_SignedGate_CommitAfterApprovalRefused(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	seedApproval(t, rig, slug, revParseInDir(t, rig.dir, "refs/heads/"+feature), true)

	// The developer adds a commit after the approval.
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "late.txt", "late\n")
	mustRunGitAt(t, rig.dir, "add", "late.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: slipped in after approval")
	mustRunGitAt(t, rig.dir, "checkout", "main")

	_, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close is refused: the approval does not cover the tip", func(t *testing.T) {
		if !errors.Is(err, ErrApprovalNotSigned) {
			t.Fatalf("reviewPreflight = %v", err)
		}
		if !strings.Contains(err.Error(), "does not cover") {
			t.Errorf("message:\n%v", err)
		}
	})
}

func TestClose_SignedGate_SecondApprovalCoversReviewerCommits(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	featureTip := revParseInDir(t, rig.dir, "refs/heads/"+feature)

	// Reviewer B commits on the review branch; reviewer A approved the
	// feature tip as submitted.
	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "reviewer-only.txt", "r\n")
	mustRunGitAt(t, rig.dir, "add", "reviewer-only.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	mustRunGitAt(t, rig.dir, "checkout", "main")
	reviewTip := revParseInDir(t, rig.dir, "refs/heads/"+slug+"@review")

	seedApproval(t, rig, slug, featureTip, true)
	seedApproval(t, rig, slug, reviewTip, true)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close proceeds on the approval that covers the review branch", func(t *testing.T) {
		if err != nil || cleanup == nil {
			t.Fatalf("reviewPreflight = %v, cleanup nil: %v", err, cleanup == nil)
		}
	})
	t.Run("the feature branch is fast-forwarded to the approved commit", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/"+feature); got != reviewTip {
			t.Errorf("feature tip = %s, want %s", got, reviewTip)
		}
	})
}

func TestClose_SignedGate_DivergedBranchRefusedUntouched(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)

	mustRunGitAt(t, rig.dir, "checkout", "-b", slug+"@review", feature)
	writeFileAt(t, rig.dir, "reviewer-only.txt", "r\n")
	mustRunGitAt(t, rig.dir, "add", "reviewer-only.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	reviewTip := revParseInDir(t, rig.dir, "refs/heads/"+slug+"@review")
	seedApproval(t, rig, slug, reviewTip, true)

	// The developer moves the feature branch after the approval: diverged.
	mustRunGitAt(t, rig.dir, "checkout", feature)
	writeFileAt(t, rig.dir, "dev-later.txt", "d\n")
	mustRunGitAt(t, rig.dir, "add", "dev-later.txt")
	mustRunGitAt(t, rig.dir, "commit", "-m", "feat: more work")
	mustRunGitAt(t, rig.dir, "checkout", "main")
	before := revParseInDir(t, rig.dir, "refs/heads/"+feature)

	_, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("close is refused", func(t *testing.T) {
		if !errors.Is(err, ErrApprovalNotSigned) {
			t.Fatalf("reviewPreflight = %v", err)
		}
	})
	t.Run("the feature branch is not touched", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/"+feature); got != before {
			t.Errorf("feature tip moved from %s to %s", before, got)
		}
	})
}
```

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_SignedGate" -v`
Expected: FAIL, build error `undefined: ErrApprovalNotSigned`.

- [ ] **Step 10: Implement the gate**

Run `impact({target: "reviewPreflight", direction: "upstream"})` and report it.

In `cmd/issue/close.go`, next to `ErrReviewSyncNeeded`, add:

```go
// ErrApprovalNotSigned is returned by reviewPreflight when review.require-signed
// is set and no approval of the current round both carries a signature git
// trusts and covers the commit about to be merged.
var ErrApprovalNotSigned = errors.New("no verified approval covers the branch")
```

In `reviewPreflight`, in the `case store.ReviewStatusApproved:` block, insert between the computation of `remoteTrackingExists` and `if pending != nil {`:

```go
		if deps.cfg.Review.RequireSigned {
			// Before the branch is touched: a refused close changes nothing.
			if err := checkSignedApproval(ctx, deps.client, ref, picked.BranchName, pending); err != nil {
				return nil, err
			}
		}
```

Add the function below `reviewPreflight`:

```go
// checkSignedApproval enforces review.require-signed. Some approval of the
// current round must carry a signature git trusts and name, as approved_sha,
// exactly the commit the feature branch will point at once reviewer commits
// are incorporated. A branch that moved after the approval has no such commit.
func checkSignedApproval(
	ctx context.Context, client *git.Client, st *reviewpkg.State, featureBranch string, pending *issueflow.PendingReview,
) error {
	target := featureBranch
	if pending != nil {
		ahead, err := client.CommitsAhead(ctx, featureBranch, pending.EffectiveRef)
		if err != nil {
			return fmt.Errorf("check review divergence: %w", err)
		}
		if ahead > 0 {
			return fmt.Errorf(
				"branch %q has %d commit(s) made after the approval; the approval does not cover them "+
					"(review.require-signed is set).\nRun `git zf review request` for a new round: %w",
				featureBranch, ahead, ErrApprovalNotSigned)
		}
		target = pending.EffectiveRef
	}

	// target is a local branch, or "<remote>/<branch>" for a review branch
	// known only through its remote-tracking ref.
	tip, err := client.ResolveRef("refs/heads/" + target)
	if err != nil {
		if tip, err = client.ResolveRef("refs/remotes/" + target); err != nil {
			return fmt.Errorf("resolve %s: %w", target, err)
		}
	}

	verified := false
	lines := make([]string, 0, len(st.Approvals))
	for _, a := range st.Approvals {
		state := reviewpkg.SignatureState(ctx, client, a.Commit)
		if state == reviewpkg.SigVerified {
			if a.ApprovedSHA == tip.String() {
				return nil
			}
			verified = true
		}
		lines = append(lines, fmt.Sprintf("  %s: %s, approved %.7s", a.Author, state, a.ApprovedSHA))
	}

	reason := "no approval carries a signature git trusts"
	if verified {
		reason = fmt.Sprintf("the verified approval does not cover %.7s, the commit to merge", tip.String())
	}

	return fmt.Errorf(
		"issue %q, round %d: %s (review.require-signed is set).\n%s\n"+
			"Run `git zf review request` for a signed round: %w",
		st.Slug, st.Round, reason, strings.Join(lines, "\n"), ErrApprovalNotSigned)
}
```

`close.go` already imports `strings`, `git`, `issueflow` and `fmt`.

- [ ] **Step 11: Run the tests**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_SignedGate" -v`
Expected: PASS, six functions (or SKIP without `ssh-keygen`).

Run: `mise exec -- go test ./...`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/gittest git/commit_sig.go git/commit_sig_test.go config review cmd
git commit -m "feat: review.require-signed gates issue close on a verified approval"
```

---

### Task 8: documentation

**Files:**
- Create: `docs/review-refs.md`
- Modify: `docs/issue-refs.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: the behavior of Tasks 2 to 7.
- Produces: nothing code depends on.

- [ ] **Step 1: Write `docs/review-refs.md`**

````markdown
# Review refs

git-zf stores the review of an issue in the repository as a chain of commits
under `refs/zf/reviews/<slug>`, where `<slug>` is the issue's ID as it appears
in the branch name. This page describes the format, for people reading it with
plain git:

```
git log refs/zf/reviews/<slug>
git show <commit>:op.json
```

## Layout

| Ref | Content |
|---|---|
| `refs/zf/reviews/<slug>` | The local review. |
| `refs/remotes/<remote>/zf/reviews/<slug>` | What the remote had at the last fetch or push. Never edited by hand. |

A review ref is never deleted. When the issue is closed, the chain gets a
`close` op and stays as a record of who approved what.

## Ops

Every action is one commit. Its tree holds one file, `op.json`; its parent is
the previous action. Author and committer are the user's git identity.

```json
{"v":1,"type":"approve","at":"2026-10-04T10:00:00Z","author":"Pi <pi@example.org>","approved_sha":"3fad482…"}
```

| `type` | Fields | Written by | Effect |
|---|---|---|---|
| `request` | `feature_sha` | `git zf review request` | Starts a round: the branch is locked. First commit of the chain on round 1. |
| `start` | none | `git zf review start` | Records the reviewer. |
| `approve` | `approved_sha`, `has_commits` | `git zf review approve` | Approves the commit `approved_sha`. |
| `reject` | `comment`, `has_commits` | `git zf review reject` | Requests changes: the branch is unlocked. |
| `close` | none | `git zf issue close` | The issue was merged. |
| `merge` | none | sync | Two-parent commit joining actions made on two clones. |

`v` is the format version, currently 1. `at` is RFC 3339, UTC.

## Reading a review

The current state is the fold of all ops: a commit is applied after its
parents; commits with no order between them (made on two clones before either
synced) are applied by `at`, then by commit ID. An op that does not fit the
current status is ignored, which settles concurrent actions the same way on
every clone:

- two requests made at once count as one round;
- two approvals are both kept;
- an approval and a rejection made at once end as changes requested;
- two rejections keep both comments.

An op of an unknown type or version is skipped, so an older git-zf reads refs
written by a newer one.

## Sharing

Every review command fetches `refs/zf/reviews/*` into the tracking namespace,
then for each review: creates the local ref if it is missing, fast-forwards it
if it is behind, leaves it if it is ahead, and writes a `merge` commit if both
sides changed. Pushes are plain fast-forward pushes, never forced, so a push
cannot discard someone else's action. A command that writes an op pushes it
right away; when the push fails the op stays local and goes out with the next
review command.

Run `git zf init` once per clone. It adds the fetch refspec to the remote, so a
plain `git fetch` brings the reviews, and `git fetch --prune` does not delete
their tracking refs. The tracking refs appear in `git branch -r` as
`<remote>/zf/reviews/<slug>`.

## Signed approvals

An op is signed when `commit.gpgsign` is true. To require it, set in
`.git-zf.toml`:

```toml
[review]
require-signed = true
```

The file is per clone, so each side sets it:

- on the reviewer's clone, `git zf review approve` signs the approval and
  fails if it cannot;
- on the developer's clone, `git zf issue close` refuses to merge unless an
  approval of the current round verifies with `git verify-commit` and its
  `approved_sha` is exactly the commit to be merged. A commit added after the
  approval is not covered: request a new round.

`git zf review status` shows each approval as `verified`, `signed, not
verified` or `unsigned`. Trust is git's own: the GPG keyring, or
`gpg.ssh.allowedSignersFile` for SSH signatures. git-zf does not check who the
signer is.

## Reviews written by an older git-zf

Before this format a review was a JSON blob at the same ref name. Such a ref is
not migrated. `git zf review request` replaces it; `git zf issue close` refuses
to merge while it exists and prints the command to delete it.
````

- [ ] **Step 2: Update `docs/issue-refs.md`**

In the Layout table, replace the row

```
| `refs/zf/remote/issues/<id>` | What the remote had at the last fetch or push. Never edited by hand. |
```

with

```
| `refs/remotes/<remote>/zf/issues/<id>` | What the remote had at the last fetch or push. Never edited by hand. |
```

In the Sharing section, replace `fetches \`refs/zf/issues/*\` into \`refs/zf/remote/issues/*\`` with `fetches \`refs/zf/issues/*\` into \`refs/remotes/<remote>/zf/issues/*\``, and replace the last paragraph (from "A plain `git clone`…" to the end) with:

```markdown
A plain `git clone` does not bring these refs. Run `git zf init` once per
clone: it adds the fetch refspec to the remote, so that a plain `git fetch`
brings the issues and `git fetch --prune` does not delete their tracking refs.
Without it, run `git zf issue sync`. The tracking refs appear in
`git branch -r` as `<remote>/zf/issues/<id>`.
```

- [ ] **Step 3: Update `README.md`**

Find the review section with `grep -n -i 'review' README.md`. At the end of that section add:

```markdown
#### Reviews in the repository

A review is stored under `refs/zf/reviews/<issue>` as a chain of commits, one
per action (request, start, approve, reject, close). Two people acting on the
same review at once never get a rejected push: their actions are merged. The
review stays in the repository after the issue is closed. See
[docs/review-refs.md](docs/review-refs.md).

To require signed approvals, set `require-signed = true` under `[review]` in
`.git-zf.toml`: `review approve` then signs, and `issue close` refuses to merge
without an approval that `git verify-commit` accepts and that covers the commit
being merged.
```

Where the README documents `git zf init`, add one sentence: "It also configures the remote so that `git fetch` brings the reviews and issues stored under `refs/zf/`."

- [ ] **Step 4: Update `CLAUDE.md`**

After the "Testing the repo issues" section, add:

````markdown
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
````

In the "Testing the repo issues" section, replace `fetch into \`refs/zf/remote/issues/*\`` with `fetch into \`refs/remotes/<remote>/zf/issues/*\``.

- [ ] **Step 5: Check and commit**

Run: `grep -rn 'refs/zf/remote' --include=*.md --include=*.go . | grep -v docs/superpowers`
Expected: only the two mentions of the old layout as something `init` deletes (`git/chain_ref_sync.go`, `cmd/init/init_test.go`).

Run: `mise exec -- go test ./... && make`
Expected: PASS, and `./bin/git-zf` builds.

```bash
git add docs/review-refs.md docs/issue-refs.md README.md CLAUDE.md
git commit -m "docs: review chains, signed approvals, tracking refs"
```

---

## Self-Review Notes

- **Spec coverage:** ref layout and tracking namespace → Task 2; refspecs and stale-ref cleanup → Task 5; ops and fold rules → Task 3; old blob refs → Task 2 (`ChainRefKind`, reconcile, `DeleteChainRef`), Task 4 (`ErrLegacyReview`, `ReplaceLegacy`), Task 6 (request, close, guard, list); `internal/chain` → Task 1; `review/` package → Tasks 3 and 4; call sites → Task 6; signing gate, config, display → Task 7; error-handling table → Tasks 4, 6, 7; documentation → Task 8. The SQLite `reviews` table is untouched, as the spec requires.
- **Not covered, by decision of the spec:** branch refs as chains, removing SQLite, a server hook, a reviewer allow-list, a remote helper, migration of blob reviews.
- **Review Focus:** items 1, 2, 3 and 5 are subtests of `TestSync_EdgeCases` (Task 4); item 4 is `TestChainRef_UnpushedAndNoRemote` (Task 2) and a subtest of `TestSync_EdgeCases`.
