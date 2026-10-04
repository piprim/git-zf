# `git zf branch prune-tracker` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** [`docs/superpowers/specs/2026-05-27-branch-prune-tracker-design.md`](../specs/2026-05-27-branch-prune-tracker-design.md)

**Goal:** Ship `git zf branch prune-tracker`, a new subcommand that discovers local branches whose tracker issue is closed (regex-extracted ID → tracker lookup) and offers per-branch reaping (safe-delete / force-delete / skip) with a new `closed` store status. Existing `branch prune` is untouched.

**Architecture:** New subcommand under `cmd/branch/` parallel to `prune`. New `tracker.IsIssueClosed` method per adapter (`github`, `redmine`, `fake`). New `TrackerPrunePrompter` interface with three implementations (huh interactive, fixed-action non-interactive, scripted test). New `git.Client` wrappers `SafeDeleteBranch` / `ForceDeleteBranch` shelling out to `git branch -d` / `-D`. New `statuses` row `(3, 'closed')` via migration `0005`.

**Tech Stack:** Go 1.23+, `spf13/cobra`, `charmbracelet/huh`, `go-git/v6`, `modernc.org/sqlite`. Reuses `tracker/fake/` for E2E.

**Toolchain:** Go is managed by `mise`. **Always invoke Go via `mise exec -- go <command>`** (e.g. `mise exec -- go test ./...`). Never call bare `go`.

**Lint conventions to follow throughout the plan:**

- `wrapcheck` — every error from an external package wrapped with `fmt.Errorf("context: %w", err)`. Never bare external errors.
- `nlreturn` — blank line before `return` when it is not the only statement in its block.
- `exec.CommandContext(ctx, ...)` — never `exec.Command(...)`.
- `t.Run` — every distinct assertion or scenario in a test must be wrapped in a named `t.Run("descriptive label", func(t *testing.T) { ... })`. Applies even to single-block tests.
- IO injection — writes go through `client.IO().Out` / `client.IO().Err`, never `fmt.Println` or `cmd.OutOrStderr()` directly inside helpers.

**Git policy:** The user handles all git operations themselves. Each task's commit step shows the exact `git add` + `git commit` invocation — pause and ask the user before running them.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `store/migrations/0005_add_closed_status.sql` | Seeds the new `closed` row into the `statuses` table. |
| `cmd/branch/prune_tracker.go` | The `prune-tracker` cobra command, regex extractor, discovery + execution logic. |
| `cmd/branch/prune_tracker_prompter.go` | `TrackerPrunePrompter` interface + huh / fixed-action implementations. |
| `cmd/branch/prune_tracker_test.go` | Unit tests for regex, discovery, and execution against fakes. |
| `cmd/branch/prune_tracker_prompter_test.go` | `scriptedTrackerPrunePrompter` test helper + interface conformance + `fixedActionPrompter` smoke. |
| `cmd/branch/prune_tracker_e2e_test.go` | `pruneTrackerTestRig` + end-to-end scenarios. |

### Modified files

| Path | Change summary |
|---|---|
| `store/store.go` | Add `StatusIDClosed` constant and `BranchStatusClosed` typed value. |
| `cmd/branch/branch.go` | Extend `toStoreStatus` with `"closed"`; wire `pruneTrackerCmd()` into `GetRootCmd`; add `BranchActionNamePruneTracker` case in the interactive action menu. |
| `tui/branch.go` | Add `BranchActionNamePruneTracker` constant + option in `BranchActionSelect`; add `"closed"` filter option in `BranchStatusFilter`. |
| `tracker/tracker.go` | Add `IsIssueClosed(ctx, id) (bool, error)` to the interface; export `ErrIssueNotFound`. |
| `tracker/github/github.go` | Implement `IsIssueClosed`. |
| `tracker/github/github_test.go` | Test `IsIssueClosed` against httptest fixtures. |
| `tracker/redmine/redmine.go` | Implement `IsIssueClosed`. |
| `tracker/redmine/redmine_test.go` | Test `IsIssueClosed` against httptest fixtures. |
| `tracker/fake/fake.go` | Add `Closed`, `Unknown`, `Errors` maps + `IsIssueClosed` impl. |
| `git/git.go` | Add `SafeDeleteBranch(name)`, `ForceDeleteBranch(name)`, export `ErrBranchNotMerged`. |
| `git/git_test.go` | Test the two new methods against a real on-disk repo. |
| `README.md` | Document `branch prune-tracker` (list, intro, flags, examples) and the new `closed` status in `branch list --status`. |
| `ROADMAP.md` | Strike the `## Related branch to closed issue in the tracker` section (the feature it described is now shipped). |

---

## Task 1: Store schema — `closed` status row + constants

**Files:**
- Create: `store/migrations/0005_add_closed_status.sql`
- Modify: `store/store.go`

- [ ] **Step 1: Write the migration**

`store/migrations/0005_add_closed_status.sql`:

```sql
INSERT INTO statuses (id, name) VALUES (3, 'closed');
```

- [ ] **Step 2: Add the constants**

In `store/store.go`, find the existing block:

```go
const (
    StatusIDInProgress int64 = 1
    StatusIDMerged     int64 = 2
)
```

Replace with:

```go
const (
    StatusIDInProgress int64 = 1
    StatusIDMerged     int64 = 2
    StatusIDClosed     int64 = 3
)
```

In the same file, find:

```go
const (
    BranchStatusInProgress BranchStatus = "in_progress"
    BranchStatusMerged     BranchStatus = "merged"
    BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)
```

Replace with:

```go
const (
    BranchStatusInProgress BranchStatus = "in_progress"
    BranchStatusMerged     BranchStatus = "merged"
    BranchStatusClosed     BranchStatus = "closed"
    BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)
```

- [ ] **Step 3: Verify the migration runs and existing tests pass**

Run: `mise exec -- go test ./store/...`
Expected: PASS (existing tests; the new row is seeded transparently on store open).

- [ ] **Step 4: Commit**

```bash
git add store/migrations/0005_add_closed_status.sql store/store.go
git commit -m "feat(store): add 'closed' branch status row + constants"
```

---

## Task 2: TUI + branch list filter wiring for `closed`

**Files:**
- Modify: `tui/branch.go`
- Modify: `cmd/branch/branch.go`

- [ ] **Step 1: Add the `closed` filter option in `tui/branch.go`**

In `tui/branch.go`, find `BranchStatusFilter` and replace:

```go
return huh.NewGroup(
    huh.NewSelect[string]().
        Title("Filter by status:").
        Options(
            huh.NewOption("In progress", "in_progress"),
            huh.NewOption("Merged", "merged"),
            huh.NewOption("All", "all"),
        ).
        Value(status),
)
```

with:

```go
return huh.NewGroup(
    huh.NewSelect[string]().
        Title("Filter by status:").
        Options(
            huh.NewOption("In progress", "in_progress"),
            huh.NewOption("Merged", "merged"),
            huh.NewOption("Closed", "closed"),
            huh.NewOption("All", "all"),
        ).
        Value(status),
)
```

Also update the godoc above `BranchStatusFilter` to mention `closed`:

```go
// BranchStatusFilter presents a status filter for the branch list.
// selected is the pre-selected value ("in_progress", "merged", "closed", or "all");
// defaults to "in_progress" when empty.
```

- [ ] **Step 2: Extend `toStoreStatus` in `cmd/branch/branch.go`**

Find:

```go
func toStoreStatus(s string) store.BranchStatus {
    switch s {
    case "in_progress":
        return store.BranchStatusInProgress
    case "merged":
        return store.BranchStatusMerged
    default:
        return store.BranchStatusAll
    }
}
```

Replace with:

```go
func toStoreStatus(s string) store.BranchStatus {
    switch s {
    case "in_progress":
        return store.BranchStatusInProgress
    case "merged":
        return store.BranchStatusMerged
    case "closed":
        return store.BranchStatusClosed
    default:
        return store.BranchStatusAll
    }
}
```

- [ ] **Step 3: Extend `branch list` flag help**

In `listCmd()`, update the `--status` flag description from:

```go
f.StringVar(&flags.status, "status", "", "filter by status: in_progress, merged, all")
```

to:

```go
f.StringVar(&flags.status, "status", "", "filter by status: in_progress, merged, closed, all")
```

- [ ] **Step 4: Verify the build and existing tests pass**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/branch/... ./tui/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tui/branch.go cmd/branch/branch.go
git commit -m "feat(branch): expose 'closed' status in list filter and TUI"
```

---

## Task 3: Git client — `SafeDeleteBranch` + `ForceDeleteBranch` + `ErrBranchNotMerged`

**Files:**
- Modify: `git/git.go`
- Modify: `git/git_test.go` (or create `git/delete_test.go` if `git_test.go` is large)

- [ ] **Step 1: Write the failing tests**

Append to `git/git_test.go` (adapt the existing test rig — look for an existing test that initializes a temp repo and reuse the same setup helper):

```go
func TestSafeDeleteBranch(t *testing.T) {
    t.Run("deletes a fully-merged branch", func(t *testing.T) {
        c, dir := newTestClient(t) // existing helper that inits a temp repo with one commit on master
        runGitInDir(t, dir, "branch", "feature-merged") // points at HEAD, fully merged into HEAD

        if err := c.SafeDeleteBranch("feature-merged"); err != nil {
            t.Fatalf("SafeDeleteBranch: %v", err)
        }

        names, err := c.LocalBranchNames()
        if err != nil {
            t.Fatalf("LocalBranchNames: %v", err)
        }
        for _, n := range names {
            if n == "feature-merged" {
                t.Fatalf("branch still present: %v", names)
            }
        }
    })

    t.Run("returns ErrBranchNotMerged when branch has unique commits", func(t *testing.T) {
        c, dir := newTestClient(t)
        runGitInDir(t, dir, "checkout", "-b", "feature-divergent")
        writeFile(t, dir, "f.txt", "x")
        runGitInDir(t, dir, "add", "f.txt")
        runGitInDir(t, dir, "commit", "-m", "divergent")
        runGitInDir(t, dir, "checkout", "master")

        err := c.SafeDeleteBranch("feature-divergent")
        if !errors.Is(err, git.ErrBranchNotMerged) {
            t.Fatalf("got %v, want ErrBranchNotMerged", err)
        }
    })
}

func TestForceDeleteBranch(t *testing.T) {
    t.Run("deletes even when not merged", func(t *testing.T) {
        c, dir := newTestClient(t)
        runGitInDir(t, dir, "checkout", "-b", "feature-abandoned")
        writeFile(t, dir, "f.txt", "x")
        runGitInDir(t, dir, "add", "f.txt")
        runGitInDir(t, dir, "commit", "-m", "abandoned")
        runGitInDir(t, dir, "checkout", "master")

        if err := c.ForceDeleteBranch("feature-abandoned"); err != nil {
            t.Fatalf("ForceDeleteBranch: %v", err)
        }

        names, err := c.LocalBranchNames()
        if err != nil {
            t.Fatalf("LocalBranchNames: %v", err)
        }
        for _, n := range names {
            if n == "feature-abandoned" {
                t.Fatalf("branch still present: %v", names)
            }
        }
    })

    t.Run("returns error when branch does not exist", func(t *testing.T) {
        c, _ := newTestClient(t)
        if err := c.ForceDeleteBranch("does-not-exist"); err == nil {
            t.Fatal("want error, got nil")
        }
    })
}
```

If `newTestClient`, `runGitInDir`, or `writeFile` helpers don't exist, add them at the bottom of the test file using the same pattern as `prune_e2e_test.go`'s `newPruneRig`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./git/... -run 'TestSafeDeleteBranch|TestForceDeleteBranch' -v`
Expected: FAIL (`undefined: c.SafeDeleteBranch`, `undefined: git.ErrBranchNotMerged`, etc.).

- [ ] **Step 3: Implement the wrappers + sentinel**

Append to `git/git.go`:

```go
// ErrBranchNotMerged is returned by SafeDeleteBranch when git refuses to delete
// the branch because its tip commit is not fully merged into HEAD or upstream.
var ErrBranchNotMerged = errors.New("git: branch not fully merged")

// SafeDeleteBranch invokes `git branch -d <name>` from the working tree root.
// On git's "not fully merged" refusal, returns ErrBranchNotMerged so callers
// can branch on the sentinel via errors.Is. Other failures are wrapped.
func (c *Client) SafeDeleteBranch(name string) error {
    return c.deleteBranch(context.Background(), "-d", name)
}

// ForceDeleteBranch invokes `git branch -D <name>` from the working tree root.
// Always destructive; safety check skipped. Errors are wrapped.
func (c *Client) ForceDeleteBranch(name string) error {
    return c.deleteBranch(context.Background(), "-D", name)
}

func (c *Client) deleteBranch(ctx context.Context, flag, name string) error {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return fmt.Errorf("working tree root: %w", err)
    }

    cmd := exec.CommandContext(ctx, "git", "branch", flag, name)
    cmd.Dir = root

    out, runErr := cmd.CombinedOutput()
    if runErr == nil {
        return nil
    }

    if flag == "-d" && strings.Contains(string(out), "not fully merged") {
        return ErrBranchNotMerged
    }

    return fmt.Errorf("git branch %s %s: %w (%s)", flag, name, runErr, strings.TrimSpace(string(out)))
}
```

If `os/exec`, `errors`, or `strings` are not already imported at the top of `git/git.go`, add them.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./git/... -run 'TestSafeDeleteBranch|TestForceDeleteBranch' -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Lint check**

Run: `mise exec -- go vet ./git/...` and `mise exec -- golangci-lint run ./git/...` (skip the linter step if golangci-lint isn't installed in your env).
Expected: no findings.

- [ ] **Step 6: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): SafeDeleteBranch + ForceDeleteBranch wrappers with ErrBranchNotMerged sentinel"
```

---

## Task 4: Tracker interface — `IsIssueClosed` + `ErrIssueNotFound`

**Files:**
- Modify: `tracker/tracker.go`

- [ ] **Step 1: Extend the interface and export the sentinel**

Edit `tracker/tracker.go`. Add to the imports if not already present: `"errors"`.

Replace:

```go
// Tracker is the contract every adapter must satisfy.
type Tracker interface {
    // ListIssues retrieves the issues from the tracker
    ListIssues(ctx context.Context) ([]Issue, error)
    // ListStatuses returns the available status names for the tracker.
    ListStatuses(ctx context.Context) ([]string, error)
    // UpdateIssueStatus updates the status from the given issueID
    UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
}
```

With:

```go
// ErrIssueNotFound is returned by IsIssueClosed when the tracker has no record
// of the requested issueID (HTTP 404 or equivalent). Callers can branch on it
// via errors.Is to distinguish "tracker says open" from "tracker doesn't know".
var ErrIssueNotFound = errors.New("tracker: issue not found")

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
    // ListIssues retrieves the issues from the tracker
    ListIssues(ctx context.Context) ([]Issue, error)
    // ListStatuses returns the available status names for the tracker.
    ListStatuses(ctx context.Context) ([]string, error)
    // UpdateIssueStatus updates the status from the given issueID
    UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
    // IsIssueClosed reports whether the tracker considers issueID closed.
    // Returns ErrIssueNotFound for missing-issue cases so callers can format
    // the warning distinctly from transport/auth failures.
    IsIssueClosed(ctx context.Context, issueID string) (bool, error)
}
```

- [ ] **Step 2: Verify build fails on adapters (expected)**

Run: `mise exec -- go build ./...`
Expected: FAIL — github, redmine, fake adapters do not yet implement `IsIssueClosed`. Tasks 5/6/7 fix this. Do NOT commit yet; the build is intentionally broken until those tasks land. Proceed directly to Task 5.

---

## Task 5: Fake tracker — `IsIssueClosed` + scenario maps

**Files:**
- Modify: `tracker/fake/fake.go`

- [ ] **Step 1: Add the maps and implementation**

In `tracker/fake/fake.go`, extend the `Tracker` struct:

```go
type Tracker struct {
    mu              sync.Mutex
    Issues          []tracker.Issue
    Statuses        []string
    RecordedUpdates []Update

    // Closed[id] == true → IsIssueClosed returns (true, nil) for id.
    // Default zero-value (absent or false) → IsIssueClosed returns (false, nil).
    Closed map[string]bool
    // Unknown[id] == true → IsIssueClosed returns (false, tracker.ErrIssueNotFound).
    Unknown map[string]bool
    // Errors[id] != nil → IsIssueClosed returns (false, Errors[id]) — for transport-error scenarios.
    Errors map[string]error
}
```

Append the method:

```go
// IsIssueClosed consults Closed / Unknown / Errors in that priority order.
func (t *Tracker) IsIssueClosed(_ context.Context, issueID string) (bool, error) {
    t.mu.Lock()
    defer t.mu.Unlock()

    if err, ok := t.Errors[issueID]; ok && err != nil {
        return false, err
    }

    if t.Unknown[issueID] {
        return false, tracker.ErrIssueNotFound
    }

    return t.Closed[issueID], nil
}
```

- [ ] **Step 2: Verify fake compiles against the new interface**

Run: `mise exec -- go build ./tracker/fake/...`
Expected: PASS (fake now satisfies `tracker.Tracker` again).

- [ ] **Step 3: Commit (with Task 4 changes, since they were split intentionally)**

```bash
git add tracker/tracker.go tracker/fake/fake.go
git commit -m "feat(tracker): IsIssueClosed interface method + ErrIssueNotFound + fake impl"
```

---

## Task 6: GitHub adapter — `IsIssueClosed`

**Files:**
- Modify: `tracker/github/github.go`
- Modify: `tracker/github/github_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `tracker/github/github_test.go` (reuse whatever httptest+go-github fixture pattern already lives in this file):

```go
func TestIsIssueClosed(t *testing.T) {
    t.Run("returns true for closed issue", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
            if !strings.HasSuffix(r.URL.Path, "/issues/42") {
                t.Fatalf("unexpected path: %s", r.URL.Path)
            }
            _, _ = io.WriteString(w, `{"number":42,"state":"closed"}`)
        })

        closed, err := a.IsIssueClosed(context.Background(), "42")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !closed {
            t.Fatal("want closed=true")
        }
    })

    t.Run("returns false for open issue", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            _, _ = io.WriteString(w, `{"number":42,"state":"open"}`)
        })

        closed, err := a.IsIssueClosed(context.Background(), "42")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if closed {
            t.Fatal("want closed=false")
        }
    })

    t.Run("returns ErrIssueNotFound on 404", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
        })

        _, err := a.IsIssueClosed(context.Background(), "42")
        if !errors.Is(err, tracker.ErrIssueNotFound) {
            t.Fatalf("got %v, want ErrIssueNotFound", err)
        }
    })

    t.Run("wraps other transport errors", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            http.Error(w, `boom`, http.StatusInternalServerError)
        })

        _, err := a.IsIssueClosed(context.Background(), "42")
        if err == nil || errors.Is(err, tracker.ErrIssueNotFound) {
            t.Fatalf("want wrapped non-404 error, got %v", err)
        }
    })
}
```

If `newTestAdapterWithHandler` doesn't exist, model it on the existing test adapter constructor in this file (an httptest server + go-github client pointed at it).

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/... -run TestIsIssueClosed -v`
Expected: FAIL — method not defined.

- [ ] **Step 3: Implement**

Append to `tracker/github/github.go` (verify the receiver name `a` matches existing methods):

```go
// IsIssueClosed asks GitHub for the issue's state. The id is the issue number
// as a decimal string. HTTP 404 → tracker.ErrIssueNotFound; other failures
// are wrapped.
func (a *githubAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
    owner, repo, err := a.ownerRepo()
    if err != nil {
        return false, fmt.Errorf("github: resolve owner/repo: %w", err)
    }

    n, err := strconv.Atoi(issueID)
    if err != nil {
        return false, fmt.Errorf("github: issue id %q not an integer: %w", issueID, err)
    }

    iss, resp, err := a.client.Issues.Get(ctx, owner, repo, n)
    if err != nil {
        if resp != nil && resp.StatusCode == http.StatusNotFound {
            return false, tracker.ErrIssueNotFound
        }

        return false, fmt.Errorf("github: get issue %s: %w", issueID, err)
    }

    return iss.GetState() == statusClosed, nil
}
```

If `strconv` or `net/http` are not imported, add them. The helper `a.ownerRepo()` may already exist (used by `UpdateIssueStatus`); reuse it. If not, factor out the owner/repo resolution that `UpdateIssueStatus` does inline and call it from both places — DRY.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/... -run TestIsIssueClosed -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Commit**

```bash
git add tracker/github/github.go tracker/github/github_test.go
git commit -m "feat(tracker/github): IsIssueClosed via GET /issues/{n}"
```

---

## Task 7: Redmine adapter — `IsIssueClosed`

**Files:**
- Modify: `tracker/redmine/redmine.go`
- Modify: `tracker/redmine/redmine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `tracker/redmine/redmine_test.go` (model on the existing httptest pattern in this file):

```go
func TestIsIssueClosed(t *testing.T) {
    t.Run("returns true when status.is_closed", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
            if !strings.HasSuffix(r.URL.Path, "/issues/42.json") {
                t.Fatalf("unexpected path: %s", r.URL.Path)
            }
            _, _ = io.WriteString(w, `{"issue":{"id":42,"status":{"id":5,"name":"Closed","is_closed":true}}}`)
        })

        closed, err := a.IsIssueClosed(context.Background(), "42")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !closed {
            t.Fatal("want closed=true")
        }
    })

    t.Run("returns false when status.is_closed is false", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            _, _ = io.WriteString(w, `{"issue":{"id":42,"status":{"id":1,"name":"New","is_closed":false}}}`)
        })

        closed, err := a.IsIssueClosed(context.Background(), "42")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if closed {
            t.Fatal("want closed=false")
        }
    })

    t.Run("returns ErrIssueNotFound on 404", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            http.Error(w, ``, http.StatusNotFound)
        })

        _, err := a.IsIssueClosed(context.Background(), "42")
        if !errors.Is(err, tracker.ErrIssueNotFound) {
            t.Fatalf("got %v, want ErrIssueNotFound", err)
        }
    })

    t.Run("wraps other transport errors", func(t *testing.T) {
        a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
            http.Error(w, `boom`, http.StatusInternalServerError)
        })

        _, err := a.IsIssueClosed(context.Background(), "42")
        if err == nil || errors.Is(err, tracker.ErrIssueNotFound) {
            t.Fatalf("want wrapped non-404 error, got %v", err)
        }
    })
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./tracker/redmine/... -run TestIsIssueClosed -v`
Expected: FAIL — method not defined.

- [ ] **Step 3: Implement**

Append to `tracker/redmine/redmine.go`:

```go
// IsIssueClosed asks Redmine for the issue and reads status.is_closed.
// HTTP 404 → tracker.ErrIssueNotFound; other failures are wrapped.
func (a *redmineAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
    base := strings.TrimRight(a.cfg.URL, "/")
    url := fmt.Sprintf("%s/issues/%s.json", base, issueID)

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
    if err != nil {
        return false, fmt.Errorf("redmine: build request: %w", err)
    }

    req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

    resp, err := a.http.Do(req)
    if err != nil {
        return false, fmt.Errorf("redmine: get issue %s: %w", issueID, err)
    }
    defer func() { _ = resp.Body.Close() }()

    if resp.StatusCode == http.StatusNotFound {
        return false, tracker.ErrIssueNotFound
    }

    if resp.StatusCode != http.StatusOK {
        return false, fmt.Errorf("redmine: get issue %s: unexpected status %d", issueID, resp.StatusCode)
    }

    var payload struct {
        Issue struct {
            Status struct {
                IsClosed bool `json:"is_closed"`
            } `json:"status"`
        } `json:"issue"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
        return false, fmt.Errorf("redmine: decode issue %s: %w", issueID, err)
    }

    return payload.Issue.Status.IsClosed, nil
}
```

The existing imports (`encoding/json`, `net/http`, `strings`, `fmt`, `context`) are already present in `redmine.go` — no import block changes needed. Field names match the existing adapter exactly: `a.http` (not `httpClient`), `a.cfg.Token` (not `APIKey`), header `X-Redmine-API-Key` (matches `ListIssues`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./tracker/redmine/... -run TestIsIssueClosed -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Verify the whole build is green again**

Run: `mise exec -- go build ./... && mise exec -- go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tracker/redmine/redmine.go tracker/redmine/redmine_test.go
git commit -m "feat(tracker/redmine): IsIssueClosed via GET /issues/{id}.json"
```

---

## Task 8: Regex extractor

**Files:**
- Create: `cmd/branch/prune_tracker.go` (initial skeleton — extractor only for this task)
- Create: `cmd/branch/prune_tracker_test.go` (table-driven test)

- [ ] **Step 1: Write the failing test**

Create `cmd/branch/prune_tracker_test.go`:

```go
package branch

import "testing"

func TestExtractIssueID(t *testing.T) {
    cases := []struct {
        name      string
        branch    string
        wantID    string
        wantFound bool
    }{
        {"github-style prefixed", "ABC-42@feat@add-oauth-login@550e8400", "ABC-42", true},
        {"redmine-style numeric @", "42@feat@something", "42", true},
        {"redmine-style numeric -", "42-feat-something", "42", true},
        {"github-style with pipe", "ABC-7|spike|try", "ABC-7", true},
        {"unparseable: no delimiter", "main", "", false},
        {"unparseable: empty", "", "", false},
        {"unparseable: just delimiters", "@@@", "", false},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            id, ok := extractIssueID(tc.branch)
            if ok != tc.wantFound {
                t.Fatalf("ok = %v, want %v (id=%q)", ok, tc.wantFound, id)
            }
            if id != tc.wantID {
                t.Fatalf("id = %q, want %q", id, tc.wantID)
            }
        })
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/branch/... -run TestExtractIssueID -v`
Expected: FAIL — `undefined: extractIssueID`.

- [ ] **Step 3: Implement the extractor**

Create `cmd/branch/prune_tracker.go`:

```go
package branch

import "regexp"

// defaultIssueIDPattern matches a leading issue ID at the start of a branch name.
// First non-empty capture wins.
//   Pattern A: leading numeric ID before a delimiter      ([0-9]+)[-@_|+=.]
//   Pattern B: alphanumeric ID before @ or |              ([^@|]+)[@|]
//
// Intentionally narrow for v1. Lift to AppConfig.Branch.IssueIDPattern in a
// follow-up if user-extensibility is needed.
var defaultIssueIDPattern = regexp.MustCompile(
    `^(?:([0-9]+)[-@_|+=.]|([^@|]+)[@|]).+`,
)

// extractIssueID returns the first non-empty regex capture from name.
// Returns ("", false) if the name does not match the pattern.
func extractIssueID(name string) (string, bool) {
    m := defaultIssueIDPattern.FindStringSubmatch(name)
    if m == nil {
        return "", false
    }

    for _, g := range m[1:] {
        if g != "" {
            return g, true
        }
    }

    return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/branch/... -run TestExtractIssueID -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/prune_tracker.go cmd/branch/prune_tracker_test.go
git commit -m "feat(branch): regex-based issue ID extractor for prune-tracker"
```

---

## Task 9: Prompter interface + three implementations

**Files:**
- Create: `cmd/branch/prune_tracker_prompter.go`
- Create: `cmd/branch/prune_tracker_prompter_test.go`

- [ ] **Step 1: Define types + sketch the test helper**

Append to `cmd/branch/prune_tracker.go` (the file from Task 8) — this defines the candidate type the prompter consumes:

```go
// trackerCandidate is one branch-and-issue pair flagged by discovery for reaping.
type trackerCandidate struct {
    BranchName string
    IssueID    string
    StoreRow   *store.BranchRow // nil → branch unknown to git-zf; no store flip after delete.
}
```

Add to the imports at the top of `prune_tracker.go`: `"github.com/piprim/git-zf/store"`.

- [ ] **Step 2: Write the prompter interface + the test helper file**

Create `cmd/branch/prune_tracker_prompter_test.go`:

```go
package branch

import (
    "context"
    "fmt"
    "testing"
)

// scriptedTrackerPrunePrompter returns a fixed map of decisions for tests.
type scriptedTrackerPrunePrompter struct {
    decisions map[string]string // branchName → "safe"|"force"|"skip"
    err       error             // set to simulate prompter failure
    calls     int               // observable for test assertions
}

func (p *scriptedTrackerPrunePrompter) DecideReap(_ context.Context, cands []trackerCandidate) (map[string]string, error) {
    p.calls++

    if p.err != nil {
        return nil, p.err
    }

    out := make(map[string]string, len(cands))
    for _, c := range cands {
        action, ok := p.decisions[c.BranchName]
        if !ok {
            action = "skip"
        }

        out[c.BranchName] = action
    }

    return out, nil
}

// Compile-time interface conformance.
var _ TrackerPrunePrompter = (*scriptedTrackerPrunePrompter)(nil)

func TestFixedActionPrompter(t *testing.T) {
    cases := []struct {
        name     string
        action   string
        cands    []trackerCandidate
        wantSize int
    }{
        {"safe-delete two", "safe", []trackerCandidate{{BranchName: "a"}, {BranchName: "b"}}, 2},
        {"force-delete one", "force", []trackerCandidate{{BranchName: "x"}}, 1},
        {"skip empty list returns empty map", "skip", nil, 0},
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            p := newFixedActionPrompter(tc.action)
            got, err := p.DecideReap(context.Background(), tc.cands)
            if err != nil {
                t.Fatalf("err: %v", err)
            }
            if len(got) != tc.wantSize {
                t.Fatalf("len = %d, want %d", len(got), tc.wantSize)
            }
            for _, c := range tc.cands {
                if got[c.BranchName] != tc.action {
                    t.Fatalf("%s → %q, want %q", c.BranchName, got[c.BranchName], tc.action)
                }
            }
        })
    }
}

func TestScriptedPrompter_PropagatesError(t *testing.T) {
    t.Run("returns the configured error", func(t *testing.T) {
        wantErr := fmt.Errorf("simulated")
        p := &scriptedTrackerPrunePrompter{err: wantErr}
        if _, err := p.DecideReap(context.Background(), nil); err != wantErr {
            t.Fatalf("got %v, want %v", err, wantErr)
        }
    })
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/branch/... -run 'TestFixedActionPrompter|TestScriptedPrompter' -v`
Expected: FAIL — `TrackerPrunePrompter` undefined, `newFixedActionPrompter` undefined.

- [ ] **Step 4: Implement the prompter file**

Create `cmd/branch/prune_tracker_prompter.go`:

```go
package branch

import (
    "context"
    "fmt"

    "github.com/charmbracelet/huh"
)

// TrackerPrunePrompter resolves the per-branch reap action for the whole
// candidate batch in a single call. Mirrors PrunePrompter's role in the
// existing prune flow.
type TrackerPrunePrompter interface {
    // DecideReap returns a map from candidate BranchName to action, where
    // action is one of: "safe", "force", "skip". Callers must not invoke
    // DecideReap with an empty candidates slice.
    DecideReap(ctx context.Context, candidates []trackerCandidate) (map[string]string, error)
}

// fixedActionPrompter is wired by --safe-delete / --force-delete / --skip-delete.
// It returns the same action for every candidate without prompting the user.
type fixedActionPrompter struct {
    action string // "safe" | "force" | "skip"
}

func newFixedActionPrompter(action string) *fixedActionPrompter {
    return &fixedActionPrompter{action: action}
}

func (p *fixedActionPrompter) DecideReap(_ context.Context, candidates []trackerCandidate) (map[string]string, error) {
    out := make(map[string]string, len(candidates))
    for _, c := range candidates {
        out[c.BranchName] = p.action
    }

    return out, nil
}

var _ TrackerPrunePrompter = (*fixedActionPrompter)(nil)

// huhTrackerPrunePrompter is the interactive default. It builds one huh.Form
// containing a single huh.Group with one stacked Select per candidate.
// "safe" is pre-selected.
type huhTrackerPrunePrompter struct{}

func newHuhTrackerPrunePrompter() *huhTrackerPrunePrompter {
    return &huhTrackerPrunePrompter{}
}

func (p *huhTrackerPrunePrompter) DecideReap(ctx context.Context, candidates []trackerCandidate) (map[string]string, error) {
    if len(candidates) == 0 {
        return map[string]string{}, nil
    }

    // Per-candidate value cells; pointers handed to huh so Submit writes back.
    values := make([]string, len(candidates))
    fields := make([]huh.Field, 0, len(candidates))
    for i := range candidates {
        values[i] = "safe" // pre-selected default
        v := &values[i]
        c := candidates[i]
        fields = append(fields,
            huh.NewSelect[string]().
                Title(fmt.Sprintf("%s  (issue %s closed in tracker)", c.BranchName, c.IssueID)).
                Options(
                    huh.NewOption("safe-delete (git branch -d)", "safe"),
                    huh.NewOption("force-delete (git branch -D)", "force"),
                    huh.NewOption("skip ref delete; only mark closed", "skip"),
                ).
                Value(v),
        )
    }

    if err := huh.NewForm(huh.NewGroup(fields...)).RunWithContext(ctx); err != nil {
        return nil, fmt.Errorf("tracker-prune decide form: %w", err)
    }

    out := make(map[string]string, len(candidates))
    for i := range candidates {
        out[candidates[i].BranchName] = values[i]
    }

    return out, nil
}

var _ TrackerPrunePrompter = (*huhTrackerPrunePrompter)(nil)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `mise exec -- go test ./cmd/branch/... -run 'TestFixedActionPrompter|TestScriptedPrompter' -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 6: Commit**

```bash
git add cmd/branch/prune_tracker.go cmd/branch/prune_tracker_prompter.go cmd/branch/prune_tracker_prompter_test.go
git commit -m "feat(branch): TrackerPrunePrompter interface + huh/fixed-action/scripted impls"
```

---

## Task 10: Discovery — `runDiscoverTracker`

**Files:**
- Modify: `cmd/branch/prune_tracker.go`
- Modify: `cmd/branch/prune_tracker_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/branch/prune_tracker_test.go`:

```go
import (
    "context"
    "errors"
    "io"
    "testing"

    "github.com/piprim/git-zf/store"
    "github.com/piprim/git-zf/tracker"
)

// fakeTrackerPruner satisfies trackerPruner without a real git repo.
type fakeTrackerPruner struct {
    base    string
    locals  []string
    baseErr error
}

func (f *fakeTrackerPruner) DefaultBaseBranch() (string, error)   { return f.base, f.baseErr }
func (f *fakeTrackerPruner) LocalBranchNames() ([]string, error)  { return f.locals, nil }
func (f *fakeTrackerPruner) SafeDeleteBranch(string) error        { return nil }
func (f *fakeTrackerPruner) ForceDeleteBranch(string) error       { return nil }

// fakeIssueResolver lets discovery tests stub IsIssueClosed without spinning
// up tracker/fake (which is wired here for E2E later).
type fakeIssueResolver struct {
    closed  map[string]bool
    errs    map[string]error
    unknown map[string]bool
}

func (f *fakeIssueResolver) IsIssueClosed(_ context.Context, id string) (bool, error) {
    if err, ok := f.errs[id]; ok && err != nil {
        return false, err
    }
    if f.unknown[id] {
        return false, tracker.ErrIssueNotFound
    }
    return f.closed[id], nil
}

func TestRunDiscoverTracker(t *testing.T) {
    storeByName := map[string]*store.BranchRow{
        "ABC-42@feat@x": {IssueSlug: "ABC-42", BranchName: "ABC-42@feat@x"},
        "ABC-51@feat@y": {IssueSlug: "ABC-51", BranchName: "ABC-51@feat@y"},
    }

    t.Run("returns only branches whose extracted ID is closed", func(t *testing.T) {
        pr := &fakeTrackerPruner{base: "master", locals: []string{
            "master",
            "ABC-42@feat@x",      // closed
            "ABC-51@feat@y",      // open
            "ABC-77@spike@z",     // closed, no store row
            "no-issue-id-here",   // regex miss
        }}
        tr := &fakeIssueResolver{closed: map[string]bool{"ABC-42": true, "ABC-77": true}}

        result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, storeByName, "master")
        if err != nil {
            t.Fatalf("err: %v", err)
        }

        if len(result.Candidates) != 2 {
            t.Fatalf("got %d candidates, want 2: %#v", len(result.Candidates), result.Candidates)
        }
        // Sorted by branch name → ABC-42 first, ABC-77 second.
        if result.Candidates[0].BranchName != "ABC-42@feat@x" || result.Candidates[0].StoreRow == nil {
            t.Fatalf("c[0] = %+v", result.Candidates[0])
        }
        if result.Candidates[1].BranchName != "ABC-77@spike@z" || result.Candidates[1].StoreRow != nil {
            t.Fatalf("c[1] = %+v", result.Candidates[1])
        }
        if len(result.Warnings) != 0 {
            t.Fatalf("warnings = %v, want none", result.Warnings)
        }
    })

    t.Run("skip + warn on ErrIssueNotFound", func(t *testing.T) {
        pr := &fakeTrackerPruner{base: "master", locals: []string{"ABC-99@feat@x"}}
        tr := &fakeIssueResolver{unknown: map[string]bool{"ABC-99": true}}

        result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "master")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(result.Candidates) != 0 {
            t.Fatalf("candidates = %v, want none", result.Candidates)
        }
        if len(result.Warnings) != 1 {
            t.Fatalf("warnings = %v, want 1", result.Warnings)
        }
    })

    t.Run("skip + warn on transport error", func(t *testing.T) {
        pr := &fakeTrackerPruner{base: "master", locals: []string{"ABC-1@feat@x"}}
        tr := &fakeIssueResolver{errs: map[string]error{"ABC-1": errors.New("boom")}}

        result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "master")
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(result.Candidates) != 0 || len(result.Warnings) != 1 {
            t.Fatalf("candidates=%d warnings=%d", len(result.Candidates), len(result.Warnings))
        }
    })

    t.Run("skip base branch name even if regex extracts an ID", func(t *testing.T) {
        // Edge case: a base branch literally named e.g. "release-1.2.3" could match the regex.
        pr := &fakeTrackerPruner{base: "release-1.2.3", locals: []string{"release-1.2.3"}}
        tr := &fakeIssueResolver{closed: map[string]bool{"release": true}}

        result, _ := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "release-1.2.3")
        if len(result.Candidates) != 0 {
            t.Fatalf("should skip base branch, got %v", result.Candidates)
        }
    })
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/branch/... -run TestRunDiscoverTracker -v`
Expected: FAIL — types and function undefined.

- [ ] **Step 3: Implement discovery in `prune_tracker.go`**

Append to `cmd/branch/prune_tracker.go`:

```go
import (
    "context"
    "errors"
    "fmt"
    "io"
    "sort"

    "github.com/piprim/git-zf/tracker"
)

// trackerPruner is the git surface area prune-tracker depends on.
// Same dependency-inversion shape as the existing `pruner` interface
// (see branch.go) — keeps tests off a real repo.
type trackerPruner interface {
    DefaultBaseBranch() (string, error)
    LocalBranchNames() ([]string, error)
    SafeDeleteBranch(name string) error
    ForceDeleteBranch(name string) error
}

// issueResolver is the tracker subset prune-tracker needs.
// Allows test fakes to stub IsIssueClosed without implementing the full tracker.Tracker.
type issueResolver interface {
    IsIssueClosed(ctx context.Context, issueID string) (bool, error)
}

// trackerPruneResult bundles the discovery output (and the warnings discovery emitted).
type trackerPruneResult struct {
    Candidates []trackerCandidate
    Warnings   []string
}

// runDiscoverTracker enumerates local branches, extracts issue IDs, asks the
// resolver which are closed, and produces a sorted candidate list. Warnings
// (tracker errors) are accumulated, not fatal.
//
// storeByName may be nil; entries are looked up by branch name and copied into
// the candidate's StoreRow field (nil → branch unknown to git-zf).
//
// w is the user-facing writer for inline warnings (matches the rest of cmd/branch).
func runDiscoverTracker(
    ctx context.Context,
    w io.Writer,
    pr trackerPruner,
    tr issueResolver,
    storeByName map[string]*storeBranchRowRef,
    base string,
) (trackerPruneResult, error) {
    locals, err := pr.LocalBranchNames()
    if err != nil {
        return trackerPruneResult{}, fmt.Errorf("list local branches: %w", err)
    }

    var result trackerPruneResult

    for _, name := range locals {
        if name == base {
            continue
        }

        id, ok := extractIssueID(name)
        if !ok {
            continue
        }

        closed, err := tr.IsIssueClosed(ctx, id)
        if err != nil {
            switch {
            case errors.Is(err, tracker.ErrIssueNotFound):
                line := fmt.Sprintf("WARN: %s not found in tracker — skipping", id)
                result.Warnings = append(result.Warnings, line)
                fmt.Fprintln(w, line)
            default:
                line := fmt.Sprintf("WARN: %s lookup failed: %v — skipping", id, err)
                result.Warnings = append(result.Warnings, line)
                fmt.Fprintln(w, line)
            }

            continue
        }

        if !closed {
            continue
        }

        result.Candidates = append(result.Candidates, trackerCandidate{
            BranchName: name,
            IssueID:    id,
            StoreRow:   storeByName[name],
        })
    }

    sort.Slice(result.Candidates, func(i, j int) bool {
        return result.Candidates[i].BranchName < result.Candidates[j].BranchName
    })

    return result, nil
}

// storeBranchRowRef is an alias to keep the function signature clean.
type storeBranchRowRef = store.BranchRow
```

If the test imports `store.BranchRow` directly (without the alias), drop the alias and use `*store.BranchRow` in the signature instead. Pick whichever is cleaner.

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/branch/... -run TestRunDiscoverTracker -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/prune_tracker.go cmd/branch/prune_tracker_test.go
git commit -m "feat(branch): tracker-driven discovery (runDiscoverTracker)"
```

---

## Task 11: Execution — `runExecuteTracker`

**Files:**
- Modify: `cmd/branch/prune_tracker.go`
- Modify: `cmd/branch/prune_tracker_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/branch/prune_tracker_test.go`:

```go
// trackingFakePruner records every delete call. Lets tests pre-program
// SafeDeleteBranch to return git.ErrBranchNotMerged for specific branches.
type trackingFakePruner struct {
    fakeTrackerPruner
    safeCalls       []string
    forceCalls      []string
    safeRefuse      map[string]bool // names that should return git.ErrBranchNotMerged
}

func (t *trackingFakePruner) SafeDeleteBranch(n string) error {
    t.safeCalls = append(t.safeCalls, n)
    if t.safeRefuse[n] {
        return git.ErrBranchNotMerged
    }
    return nil
}
func (t *trackingFakePruner) ForceDeleteBranch(n string) error {
    t.forceCalls = append(t.forceCalls, n)
    return nil
}

// statusFlipRecorder counts UpdateBranchStatus invocations per branch.
type statusFlipRecorder struct {
    flipped map[string]int64 // branchName → statusID
}

func (s *statusFlipRecorder) updateBranchStatus(_ context.Context, name string, statusID int64) error {
    if s.flipped == nil {
        s.flipped = map[string]int64{}
    }
    s.flipped[name] = statusID
    return nil
}

func TestRunExecuteTracker(t *testing.T) {
    t.Run("safe delete + store flip for each candidate", func(t *testing.T) {
        pr := &trackingFakePruner{}
        flip := &statusFlipRecorder{}
        cands := []trackerCandidate{
            {BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
            {BranchName: "ABC-43@feat@y", IssueID: "ABC-43", StoreRow: &store.BranchRow{BranchName: "ABC-43@feat@y"}},
        }
        decisions := map[string]string{"ABC-42@feat@x": "safe", "ABC-43@feat@y": "safe"}

        warnings, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions)
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(pr.safeCalls) != 2 {
            t.Fatalf("safeCalls = %v, want 2", pr.safeCalls)
        }
        if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
            t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
        }
        if len(warnings) != 0 {
            t.Fatalf("warnings = %v, want none", warnings)
        }
    })

    t.Run("safe-delete refusal: warn + skip store flip", func(t *testing.T) {
        pr := &trackingFakePruner{safeRefuse: map[string]bool{"ABC-42@feat@x": true}}
        flip := &statusFlipRecorder{}
        cands := []trackerCandidate{
            {BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
        }
        decisions := map[string]string{"ABC-42@feat@x": "safe"}

        warnings, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions)
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if _, ok := flip.flipped["ABC-42@feat@x"]; ok {
            t.Fatal("should NOT have flipped store row for refused branch")
        }
        if len(warnings) != 1 {
            t.Fatalf("warnings = %v, want 1", warnings)
        }
    })

    t.Run("force-delete executes regardless and flips store", func(t *testing.T) {
        pr := &trackingFakePruner{}
        flip := &statusFlipRecorder{}
        cands := []trackerCandidate{
            {BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
        }
        decisions := map[string]string{"ABC-42@feat@x": "force"}

        if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(pr.forceCalls) != 1 || pr.forceCalls[0] != "ABC-42@feat@x" {
            t.Fatalf("forceCalls = %v", pr.forceCalls)
        }
        if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
            t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
        }
    })

    t.Run("skip: no ref action, store still flipped", func(t *testing.T) {
        pr := &trackingFakePruner{}
        flip := &statusFlipRecorder{}
        cands := []trackerCandidate{
            {BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
        }
        decisions := map[string]string{"ABC-42@feat@x": "skip"}

        if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(pr.safeCalls)+len(pr.forceCalls) != 0 {
            t.Fatalf("expected no delete calls; safe=%v force=%v", pr.safeCalls, pr.forceCalls)
        }
        if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
            t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
        }
    })

    t.Run("candidate with nil StoreRow: ref action only, no store call", func(t *testing.T) {
        pr := &trackingFakePruner{}
        flip := &statusFlipRecorder{}
        cands := []trackerCandidate{
            {BranchName: "rogue@feat@x", IssueID: "rogue", StoreRow: nil},
        }
        decisions := map[string]string{"rogue@feat@x": "force"}

        if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
            t.Fatalf("err: %v", err)
        }
        if len(flip.flipped) != 0 {
            t.Fatalf("flipped = %v, want empty", flip.flipped)
        }
    })
}
```

Adapt the test's `flip.updateBranchStatus` to match whatever closure signature `runExecuteTracker` accepts — see Step 3 below.

- [ ] **Step 2: Run test to verify it fails**

Run: `mise exec -- go test ./cmd/branch/... -run TestRunExecuteTracker -v`
Expected: FAIL — `runExecuteTracker` undefined.

- [ ] **Step 3: Implement execution**

Append to `cmd/branch/prune_tracker.go`:

```go
// updateStatusFn is the small subset of store.Store reachable from execution.
// Decoupled to keep tests off SQLite.
type updateStatusFn func(ctx context.Context, name string, statusID int64) error

// runExecuteTracker iterates candidates in input order, performs the per-branch
// ref action requested by decisions[name], and flips the store row to closed
// when a StoreRow is present and the ref action succeeded (or was skipped).
//
// Returns warnings accumulated during execution (safe-delete refusals). A
// returned error means a fatal failure (e.g. force-delete failed against git).
//
// w receives inline warnings + the final summary line.
func runExecuteTracker(
    ctx context.Context,
    w io.Writer,
    pr trackerPruner,
    updateStatus updateStatusFn,
    candidates []trackerCandidate,
    decisions map[string]string,
) ([]string, error) {
    var (
        warnings              []string
        nSafe, nForce, nSkip, nKept int
    )

    for _, c := range candidates {
        action := decisions[c.BranchName]
        flipStore := true

        switch action {
        case "safe":
            if err := pr.SafeDeleteBranch(c.BranchName); err != nil {
                if errors.Is(err, git.ErrBranchNotMerged) {
                    line := fmt.Sprintf("WARN: kept %s — git refused safe-delete (not merged into base)", c.BranchName)
                    warnings = append(warnings, line)
                    fmt.Fprintln(w, line)
                    nKept++
                    flipStore = false
                } else {
                    return warnings, fmt.Errorf("safe-delete %s: %w", c.BranchName, err)
                }
            } else {
                nSafe++
            }
        case "force":
            if err := pr.ForceDeleteBranch(c.BranchName); err != nil {
                return warnings, fmt.Errorf("force-delete %s: %w", c.BranchName, err)
            }
            nForce++
        case "skip":
            nSkip++
        default:
            return warnings, fmt.Errorf("internal: unknown action %q for %s", action, c.BranchName)
        }

        if !flipStore || c.StoreRow == nil {
            continue
        }

        if err := updateStatus(ctx, c.BranchName, store.StatusIDClosed); err != nil {
            return warnings, fmt.Errorf("flip status for %s: %w", c.BranchName, err)
        }
    }

    fmt.Fprintf(w, "Tracker-pruned: %d safe, %d forced, %d skipped, %d kept (refused).\n",
        nSafe, nForce, nSkip, nKept)

    return warnings, nil
}
```

Add the import for `"github.com/piprim/git-zf/git"` if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `mise exec -- go test ./cmd/branch/... -run TestRunExecuteTracker -v`
Expected: PASS, all sub-`t.Run` cases green.

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/prune_tracker.go cmd/branch/prune_tracker_test.go
git commit -m "feat(branch): per-branch reap execution (runExecuteTracker)"
```

---

## Task 12: Cobra command + TUI menu wiring

**Files:**
- Modify: `cmd/branch/prune_tracker.go`
- Modify: `cmd/branch/branch.go`
- Modify: `tui/branch.go`

- [ ] **Step 1: Add the cobra command + RunE in `prune_tracker.go`**

Append to `cmd/branch/prune_tracker.go`:

```go
import (
    "os"
    "time"

    "github.com/piprim/git-zf/internal/pkg"
    "github.com/spf13/cobra"
)

// pruneTrackerFlags mirrors the spec's flag list.
type pruneTrackerFlags struct {
    dryRun      bool
    base        string
    safeDelete  bool
    forceDelete bool
    skipDelete  bool
}

func (b Branch) pruneTrackerCmd() *cobra.Command {
    var flags pruneTrackerFlags

    cmd := &cobra.Command{
        Use:   "prune-tracker",
        Short: "Reap branches whose tracker issue is closed",
        Long: `Discover local branches whose issue ID (regex-extracted from the branch name)
is closed in the configured tracker, and offer per-branch reap actions
(safe-delete / force-delete / skip). Successful reaps flip the corresponding
store row to status='closed'.`,
    }

    f := cmd.Flags()
    f.BoolVar(&flags.dryRun, "dry-run", false, "show what would be done without prompting or mutating")
    f.StringVar(&flags.base, "base", "", "base branch (default: auto-detect)")
    f.BoolVar(&flags.safeDelete, "safe-delete", false, "non-interactive: apply `git branch -d` to every match")
    f.BoolVar(&flags.forceDelete, "force-delete", false, "non-interactive: apply `git branch -D` to every match")
    f.BoolVar(&flags.skipDelete, "skip-delete", false, "non-interactive: never touch refs; only flip store status")

    cmd.MarkFlagsMutuallyExclusive("safe-delete", "force-delete", "skip-delete")

    cmd.RunE = func(cmd *cobra.Command, _ []string) error {
        return b.pruneTrackerRunE(cmd, flags)
    }

    return cmd
}

func (b Branch) pruneTrackerRunE(cmd *cobra.Command, flags pruneTrackerFlags) error {
    ctx := cmd.Context()

    s, err := store.OpenRepo(ctx)
    if err != nil {
        return fmt.Errorf("failed to get store: %w", err)
    }
    defer func() { _ = s.Close() }()

    c, err := git.NewClient(&pkg.IO{
        In:  cmd.InOrStdin(),
        Out: cmd.OutOrStdout(),
        Err: cmd.ErrOrStderr(),
    })
    if err != nil {
        return fmt.Errorf("not a git repository: %w", err)
    }

    if b.appConfig.Branch.Remote != "" {
        c.SetRemote(b.appConfig.Branch.Remote)
    }

    tr, err := tracker.New(b.appConfig.IssueTracker)
    if err != nil {
        return fmt.Errorf("build tracker: %w", err)
    }

    var prompter TrackerPrunePrompter = newHuhTrackerPrunePrompter()
    switch {
    case flags.safeDelete:
        prompter = newFixedActionPrompter("safe")
    case flags.forceDelete:
        prompter = newFixedActionPrompter("force")
    case flags.skipDelete:
        prompter = newFixedActionPrompter("skip")
    }

    return runPruneTracker(ctx, os.Stdout, s, c, tr, prompter, flags)
}

// runPruneTracker is the top-level orchestrator. Split out so E2E tests can
// invoke it with a fake pruner + fake tracker + scripted prompter.
func runPruneTracker(
    ctx context.Context,
    w io.Writer,
    s *store.Store,
    pr trackerPruner,
    tr issueResolver,
    prompter TrackerPrunePrompter,
    flags pruneTrackerFlags,
) error {
    base := flags.base
    if base == "" {
        var err error
        base, err = pr.DefaultBaseBranch()
        if err != nil {
            return fmt.Errorf("detect base branch: %w", err)
        }
    }

    allRows, err := s.ListBranches(ctx, store.BranchStatusAll)
    if err != nil {
        return fmt.Errorf("list branches: %w", err)
    }

    storeByName := make(map[string]*store.BranchRow, len(allRows))
    for i := range allRows {
        storeByName[allRows[i].BranchName] = &allRows[i]
    }

    result, err := runDiscoverTracker(ctx, w, pr, tr, storeByName, base)
    if err != nil {
        return err
    }

    if len(result.Candidates) == 0 {
        fmt.Fprintln(w, "Nothing to prune from tracker.")

        return nil
    }

    fmt.Fprintln(w, "Tracker-closed candidates:")
    for _, c := range result.Candidates {
        fmt.Fprintf(w, "  ~ %s (issue %s)\n", c.BranchName, c.IssueID)
    }

    if flags.dryRun {
        return nil
    }

    decisions, err := prompter.DecideReap(ctx, result.Candidates)
    if err != nil {
        return fmt.Errorf("decide reap: %w", err)
    }

    updateStatus := func(ctx context.Context, name string, id int64) error {
        now := time.Now()

        return s.UpdateBranchStatus(ctx, name, id, &now)
    }

    if _, err := runExecuteTracker(ctx, w, pr, updateStatus, result.Candidates, decisions); err != nil {
        return err
    }

    return nil
}
```

Note: `git` is already imported by `prune_tracker.go` from Task 11. `tracker` is also already imported from Task 10. Only the imports listed in the block above need to be added in this task.

- [ ] **Step 2: Wire the command into `cmd/branch/branch.go`**

In `GetRootCmd`, change:

```go
cmd.AddCommand(listCmd(), b.newCmd(), b.pruneCmd(), mergeCmd())
```

to:

```go
cmd.AddCommand(listCmd(), b.newCmd(), b.pruneCmd(), b.pruneTrackerCmd(), mergeCmd())
```

In `Branch.runE`, extend the `switch action` block with a new case before the `default:`:

```go
case tui.BranchActionNamePruneTracker:
    return b.pruneTrackerRunE(cmd, pruneTrackerFlags{})
```

- [ ] **Step 3: Add the TUI constant + action option**

In `tui/branch.go`, add to the constants block:

```go
BranchActionNamePruneTracker = "branchPruneTracker"
```

In `BranchActionSelect`, add a new option alongside `BranchActionNamePrune` (model the description in the same style):

```go
huh.NewOption("Prune (tracker)\n"+
    descStyle.Render("Reap branches whose tracker issue is closed"), BranchActionNamePruneTracker),
```

- [ ] **Step 4: Build + run all existing tests**

Run: `mise exec -- go build ./... && mise exec -- go test ./...`
Expected: PASS — no test regressions; the new `prune-tracker` subcommand compiles and is reachable.

- [ ] **Step 5: Sanity-check the CLI surface**

Run: `mise exec -- go run . branch prune-tracker --help`
Expected: prints the new command's help with all five flags.

- [ ] **Step 6: Commit**

```bash
git add cmd/branch/prune_tracker.go cmd/branch/branch.go tui/branch.go
git commit -m "feat(branch): wire prune-tracker subcommand + TUI action option"
```

---

## Task 13: End-to-end tests

**Files:**
- Create: `cmd/branch/prune_tracker_e2e_test.go`

- [ ] **Step 1: Write the E2E rig + scenarios**

Create `cmd/branch/prune_tracker_e2e_test.go` based on the `prune_e2e_test.go` rig. Reuse the same `runGitInDir` / `writeFile` patterns. A reasonable shape:

```go
package branch

import (
    "bytes"
    "errors"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"

    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/internal/pkg"
    "github.com/piprim/git-zf/store"
    fakeTracker "github.com/piprim/git-zf/tracker/fake"
)

type pruneTrackerTestRig struct {
    dir    string
    client *git.Client
    store  *store.Store
    fake   *fakeTracker.Tracker
    stdout *bytes.Buffer
}

func newPruneTrackerRig(t *testing.T) *pruneTrackerTestRig {
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
    if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
        t.Fatalf("write base.txt: %v", err)
    }
    runGit("add", "base.txt")
    runGit("commit", "-m", "chore: init")

    stdout := &bytes.Buffer{}
    ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stdout}
    client, err := git.NewClientAt(ioStreams, dir)
    if err != nil {
        t.Fatalf("git.NewClientAt: %v", err)
    }
    s, err := store.Open(t.Context(), dir)
    if err != nil {
        t.Fatalf("store.Open: %v", err)
    }
    t.Cleanup(func() { _ = s.Close() })

    fakeT := &fakeTracker.Tracker{
        Closed:  map[string]bool{},
        Unknown: map[string]bool{},
        Errors:  map[string]error{},
    }

    return &pruneTrackerTestRig{
        dir: dir, client: client, store: s, fake: fakeT, stdout: stdout,
    }
}

// seedMergedBranch creates a branch pointing at HEAD (fully merged into master)
// and inserts the corresponding store rows.
func (r *pruneTrackerTestRig) seedMergedBranch(t *testing.T, issueSlug, branchName string) {
    t.Helper()
    runGit := func(args ...string) {
        cmd := exec.CommandContext(t.Context(), "git", args...)
        cmd.Dir = r.dir
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }
    runGit("branch", branchName)
    if err := r.store.InsertIssueWithBranch(t.Context(),
        &store.Issue{IDSlug: issueSlug, Title: issueSlug + " title", StatusID: store.StatusIDInProgress},
        &store.Branch{Name: branchName, Type: "feat", StatusID: store.StatusIDInProgress},
    ); err != nil {
        t.Fatalf("InsertIssueWithBranch: %v", err)
    }
}

// seedDivergentBranch creates a branch with a unique commit (not merged into master).
func (r *pruneTrackerTestRig) seedDivergentBranch(t *testing.T, issueSlug, branchName string) {
    t.Helper()
    runGit := func(args ...string) {
        cmd := exec.CommandContext(t.Context(), "git", args...)
        cmd.Dir = r.dir
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }
    runGit("checkout", "-b", branchName)
    fp := filepath.Join(r.dir, branchName+".txt")
    if err := os.WriteFile(fp, []byte("x"), 0o644); err != nil {
        t.Fatalf("write: %v", err)
    }
    runGit("add", branchName+".txt")
    runGit("commit", "-m", "x")
    runGit("checkout", "master")

    if err := r.store.InsertIssueWithBranch(t.Context(),
        &store.Issue{IDSlug: issueSlug, Title: issueSlug + " title", StatusID: store.StatusIDInProgress},
        &store.Branch{Name: branchName, Type: "feat", StatusID: store.StatusIDInProgress},
    ); err != nil {
        t.Fatalf("InsertIssueWithBranch: %v", err)
    }
}

// statusOf reads the current status of a branch row.
func (r *pruneTrackerTestRig) statusOf(t *testing.T, branchName string) store.BranchStatus {
    t.Helper()
    rows, err := r.store.ListBranches(t.Context(), store.BranchStatusAll)
    if err != nil {
        t.Fatalf("ListBranches: %v", err)
    }
    for _, b := range rows {
        if b.BranchName == branchName {
            return b.Status
        }
    }
    return ""
}

// hasLocalRef reports whether branchName exists as a local git ref.
func (r *pruneTrackerTestRig) hasLocalRef(t *testing.T, branchName string) bool {
    t.Helper()
    names, err := r.client.LocalBranchNames()
    if err != nil {
        t.Fatalf("LocalBranchNames: %v", err)
    }
    for _, n := range names {
        if n == branchName {
            return true
        }
    }
    return false
}

func Test_PruneTracker_E2E(t *testing.T) {
    runRun := func(t *testing.T, rig *pruneTrackerTestRig, prompter TrackerPrunePrompter, flags pruneTrackerFlags) error {
        t.Helper()
        return runPruneTracker(t.Context(), rig.stdout, rig.store, rig.client, rig.fake, prompter, flags)
    }

    t.Run("nothing to prune", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        err := runRun(t, rig, &scriptedTrackerPrunePrompter{}, pruneTrackerFlags{})
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
            t.Fatalf("output: %s", rig.stdout.String())
        }
    })

    t.Run("--dry-run lists candidates and does not mutate", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Closed["ABC-42"] = true

        if err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if !rig.hasLocalRef(t, "ABC-42@feat@x") {
            t.Fatal("local ref should still exist after dry-run")
        }
        if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusInProgress {
            t.Fatal("store row should still be in_progress after dry-run")
        }
    })

    t.Run("--safe-delete happy path: ref gone, store flipped to closed", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Closed["ABC-42"] = true

        if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if rig.hasLocalRef(t, "ABC-42@feat@x") {
            t.Fatal("local ref should be gone")
        }
        if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
            t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
        }
    })

    t.Run("--safe-delete with unmerged branch: kept + warned + store NOT flipped", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Closed["ABC-42"] = true

        if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if !rig.hasLocalRef(t, "ABC-42@feat@x") {
            t.Fatal("local ref should still exist (safe-delete refused)")
        }
        if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusInProgress {
            t.Fatalf("store status should remain in_progress when safe-delete refused, got %q",
                rig.statusOf(t, "ABC-42@feat@x"))
        }
        if !strings.Contains(rig.stdout.String(), "git refused safe-delete") {
            t.Fatalf("expected warning in output:\n%s", rig.stdout.String())
        }
    })

    t.Run("--force-delete: ref gone even when unmerged, store flipped", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Closed["ABC-42"] = true

        if err := runRun(t, rig, newFixedActionPrompter("force"), pruneTrackerFlags{forceDelete: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if rig.hasLocalRef(t, "ABC-42@feat@x") {
            t.Fatal("local ref should be gone (force)")
        }
        if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
            t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
        }
    })

    t.Run("--skip-delete: ref intact, store flipped", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedDivergentBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Closed["ABC-42"] = true

        if err := runRun(t, rig, newFixedActionPrompter("skip"), pruneTrackerFlags{skipDelete: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if !rig.hasLocalRef(t, "ABC-42@feat@x") {
            t.Fatal("local ref should still exist (skip)")
        }
        if rig.statusOf(t, "ABC-42@feat@x") != store.BranchStatusClosed {
            t.Fatalf("store status = %q, want closed", rig.statusOf(t, "ABC-42@feat@x"))
        }
    })

    t.Run("regex no-match: silently skipped", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        // Create a local branch with a name that won't extract any ID.
        exec.CommandContext(t.Context(), "git", "branch", "no-issue-id-here").Run() // intentionally untracked from rig
        // We bypass store seeding entirely; the branch just exists.

        err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
            t.Fatalf("output: %s", rig.stdout.String())
        }
    })

    t.Run("tracker error: warn + skip", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Errors["ABC-42"] = errors.New("boom")

        err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !strings.Contains(rig.stdout.String(), "lookup failed") {
            t.Fatalf("expected lookup-failed warning:\n%s", rig.stdout.String())
        }
        if !strings.Contains(rig.stdout.String(), "Nothing to prune from tracker.") {
            t.Fatalf("expected nothing-to-prune line:\n%s", rig.stdout.String())
        }
    })

    t.Run("tracker 404 (ErrIssueNotFound): warn + skip", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        rig.seedMergedBranch(t, "ABC-42", "ABC-42@feat@x")
        rig.fake.Unknown["ABC-42"] = true

        err := runRun(t, rig, nil, pruneTrackerFlags{dryRun: true})
        if err != nil {
            t.Fatalf("err: %v", err)
        }
        if !strings.Contains(rig.stdout.String(), "not found in tracker") {
            t.Fatalf("expected not-found warning:\n%s", rig.stdout.String())
        }
    })

    t.Run("branch known to git but not in store: ref deleted, no store action", func(t *testing.T) {
        rig := newPruneTrackerRig(t)
        // Create a local branch without seeding the store.
        cmd := exec.CommandContext(t.Context(), "git", "branch", "ABC-99@feat@rogue")
        cmd.Dir = rig.dir
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git branch: %v %s", err, out)
        }
        rig.fake.Closed["ABC-99"] = true

        if err := runRun(t, rig, newFixedActionPrompter("safe"), pruneTrackerFlags{safeDelete: true}); err != nil {
            t.Fatalf("err: %v", err)
        }
        if rig.hasLocalRef(t, "ABC-99@feat@rogue") {
            t.Fatal("local ref should be gone")
        }
        // Confirm no store row was created or flipped (none existed).
        rows, _ := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
        for _, r := range rows {
            if r.BranchName == "ABC-99@feat@rogue" {
                t.Fatalf("unexpected store row for rogue branch: %+v", r)
            }
        }
    })

}
```

The `runPruneTracker` signature in Task 12 takes `(ctx, w, *store.Store, trackerPruner, issueResolver, TrackerPrunePrompter, pruneTrackerFlags)`. The rig passes `rig.client` (which now satisfies `trackerPruner`) and `rig.fake` (which satisfies `issueResolver` via the new `IsIssueClosed` method on `*fake.Tracker`).

If `git.NewClientAt` doesn't exist with that name, search the `git` package for the helper that constructs a Client pointing at a specific directory (e.g. `NewClient` with cwd set, or `Open(dir)`) and adapt.

- [ ] **Step 2: Run the E2E suite**

Run: `mise exec -- go test ./cmd/branch/... -run Test_PruneTracker_E2E -v`
Expected: PASS, every sub-`t.Run` green.

- [ ] **Step 3: Run the whole test suite**

Run: `mise exec -- go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/branch/prune_tracker_e2e_test.go
git commit -m "test(branch): E2E coverage for prune-tracker scenarios"
```

---

## Task 14: README — document `branch prune-tracker`

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add `prune-tracker` to the branch commands list**

In `README.md`, find the block:

```
$ git zf branch new       # create a branch with manual input
$ git zf branch list      # list tracked branches
$ git zf branch merge     # merge a branch via TUI
$ git zf branch prune     # clean up stale DB records
```

Replace with:

```
$ git zf branch new            # create a branch with manual input
$ git zf branch list           # list tracked branches
$ git zf branch merge          # merge a branch via TUI
$ git zf branch prune          # clean up stale DB records (local-only)
$ git zf branch prune-tracker  # reap branches whose tracker issue is closed
```

- [ ] **Step 2: Update `branch list` flag help to mention `closed`**

Find:

```
--status string   filter by status: in_progress, merged, all (default: in_progress)
```

Replace with:

```
--status string   filter by status: in_progress, merged, closed, all (default: in_progress)
```

- [ ] **Step 3: Add the `branch prune-tracker` documentation block**

Insert a new section immediately after the `branch prune` flags block (after the closing triple-backtick of that block, before `### Config`):

````markdown
`branch prune-tracker` discovers local branches whose issue ID (regex-extracted from the branch name) is closed in the configured tracker, then offers per-branch reap actions. Successful reaps flip the corresponding store row to status `closed` (distinct from `merged` — which only the local `branch prune` produces when the tip is reachable from base).

By default, an interactive huh form is presented with one selector per candidate (safe-delete / force-delete / skip), with `safe-delete` pre-selected. Pass `--safe-delete`, `--force-delete`, or `--skip-delete` to apply that action to every candidate non-interactively (CI / scripting). The three action flags are mutually exclusive.

The discovery is per-issue — one tracker lookup per local branch whose name parses as an issue ID. Branches that don't parse are silently skipped; tracker lookup failures (404 / transport / auth) print a `WARN:` line and skip that branch without aborting the run.

`branch prune-tracker` flags:
```
--base string     base branch (default: auto-detect) — used by --safe-delete's ancestry check
--dry-run         show what would be done; no prompts, no mutations
--safe-delete     non-interactive: apply `git branch -d` to every match
--force-delete    non-interactive: apply `git branch -D` to every match
--skip-delete     non-interactive: never touch refs; only flip store status to closed
```

Examples:
```
$ git zf branch prune-tracker --dry-run                # preview only
$ git zf branch prune-tracker --safe-delete            # CI: safe-delete every closed-issue branch
$ git zf branch prune-tracker --force-delete --base main
```
````

- [ ] **Step 4: Verify the README renders sensibly**

Run: `grep -n "prune-tracker" README.md`
Expected: at least three matches (the command-list entry, the section header / intro, and the example block).

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "docs(readme): document branch prune-tracker subcommand + closed status filter"
```

---

## Task 15: Strike the ROADMAP entry

**Files:**
- Modify: `ROADMAP.md`

- [ ] **Step 1: Remove the section**

Delete the entire `## Related branch to closed issue in the tracker` block (currently lines 3–15 — verify before deleting). The implementation now lives in `branch prune-tracker`; the roadmap entry is no longer pending.

- [ ] **Step 2: Verify the file still parses**

Run: `cat ROADMAP.md | head -20` and visually confirm the next section heading is what should follow once the removed block is gone.

- [ ] **Step 3: Commit**

```bash
git add ROADMAP.md
git commit -m "chore(roadmap): strike 'Related branch to closed issue in the tracker' (now shipped)"
```

---

## Self-Review Checklist (run before handoff)

- [ ] Every spec section has a corresponding task.
  - Command surface → Task 12.
  - Tracker interface change → Tasks 4, 5, 6, 7.
  - Discovery → Tasks 8, 10.
  - Prompter → Task 9.
  - Execution → Task 11.
  - New git client wrappers → Task 3.
  - Store schema change → Tasks 1, 2.
  - Testing strategy → Tasks 9 (unit prompter), 10 (unit discovery), 11 (unit execution), 13 (E2E), 6+7 (adapter unit tests), 3 (git wrapper unit tests).
  - User-facing documentation → Task 14 (README); ROADMAP cleanup → Task 15.
- [ ] No placeholders (`TBD`, `add appropriate handling`, etc.). Every step has the actual code.
- [ ] Type / signature consistency: `runDiscoverTracker`, `runExecuteTracker`, `runPruneTracker`, `TrackerPrunePrompter.DecideReap`, `trackerPruner`, `issueResolver`, `trackerCandidate`, `trackerPruneResult` — names used consistently across Tasks 9–13.
- [ ] Lint conventions called out at the top apply throughout: `wrapcheck`, `nlreturn`, `exec.CommandContext`, `t.Run`, IO injection.
- [ ] `mise exec -- go ...` used for every Go command in every Task.
