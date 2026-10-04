# Tracker Integration (Step 2b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add pluggable issue-tracker support to `git cz issue start` and `git cz branch new`, with a Redmine adapter as the first implementation.

**Architecture:** Interface + registry pattern: `tracker/tracker.go` defines the `Tracker` interface and a global registry; `tracker/redmine/` self-registers via `init()`. `issueStartRunE` is refactored into a testable `runIssueStart(ctx, flags)` inner function that conditionally shows tracker TUI groups before falling back to manual input. The SQLite store gains a `tracker_type` column to record which adapter sourced each issue.

**Tech Stack:** Go, `github.com/mattn/go-redmine`, `github.com/charmbracelet/huh`, `modernc.org/sqlite`, `net/http/httptest` (tests).

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `tracker/tracker.go` | Create | `Issue`, `TrackerConfig`, `Tracker` interface, `Register`/`New` registry |
| `tracker/tracker_test.go` | Create | Registry unit tests |
| `tracker/redmine/redmine.go` | Create | Redmine adapter: `New`, `ListIssues`, `UpdateIssueStatus` |
| `tracker/redmine/init.go` | Create | `func init() { tracker.Register("redmine", New) }` |
| `tracker/redmine/redmine_test.go` | Create | httptest fake-server adapter tests |
| `store/migrations/0002_add_tracker_type.sql` | Create | `ALTER TABLE issues ADD COLUMN tracker_type TEXT DEFAULT NULL` |
| `store/store.go` | Modify | `Issue` gains `TrackerType *string`; `InsertIssueWithBranch` passes it through |
| `store/store_test.go` | Modify | Migration + `TrackerType` round-trip tests |
| `tui/issue.go` | Modify | 4 new huh groups: Toggle, Picker, Error, StatusConfirm |
| `cmd/issue.go` | Modify | Refactor into `runIssueStart(ctx, flags)`; tracker flow; blank-import redmine adapter |
| `cmd/branch.go` | Modify | `branchNewRunE` calls `runIssueStart` with `trackerFirst=false` |

---

## Task 1: tracker package — interface, models, registry

**Files:**
- Create: `tracker/tracker.go`
- Create: `tracker/tracker_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// tracker/tracker_test.go
package tracker_test

import (
	"context"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

// stubTracker is a no-op used to test the registry.
type stubTracker struct{}

func (s *stubTracker) ListIssues(_ context.Context) ([]tracker.Issue, error) { return nil, nil }
func (s *stubTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }

func TestRegisterAndNew_happy(t *testing.T) {
	t.Parallel()

	const key = "stub-test-register"
	tracker.Register(key, func(cfg tracker.TrackerConfig) (tracker.Tracker, error) {
		return &stubTracker{}, nil
	})

	tr, err := tracker.New(tracker.TrackerConfig{Type: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tr == nil {
		t.Error("New returned nil tracker")
	}
}

func TestNew_unknownType(t *testing.T) {
	t.Parallel()

	_, err := tracker.New(tracker.TrackerConfig{Type: "no-such-adapter-xyz"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !containsSubstring(err.Error(), "no-such-adapter-xyz") {
		t.Errorf("error should mention the unknown type, got: %v", err)
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstringHelper(s, sub))
}

func containsSubstringHelper(s, sub string) bool {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run to verify tests fail**

```bash
cd /workspace && mise exec -- go test ./tracker/... 2>&1
```

Expected: compilation error — `tracker` package does not exist yet.

- [ ] **Step 3: Write tracker/tracker.go**

```go
// tracker/tracker.go
package tracker

import (
	"context"
	"fmt"
)

// Issue is the tracker-agnostic representation of a work item.
type Issue struct {
	TrackerType string // "redmine", "plane", … — set by the adapter
	ID          string // matches store.Issue.IDSlug
	Subject     string
	Description string
	Status      string // human-readable: "New", "In Progress", …
}

// TrackerConfig holds the connection parameters for one tracker instance.
type TrackerConfig struct {
	Type             string // "redmine"
	URL              string // base URL, no trailing slash
	Token            string // API key / personal access token
	InProgressStatus string // status name to set on start
}

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
	ListIssues(ctx context.Context) ([]Issue, error)
	// UpdateIssueStatus resolves statusName to the tracker's internal ID and
	// applies the update. Implementation is adapter-specific.
	UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
}

var registry = map[string]func(TrackerConfig) (Tracker, error){}

// Register adds a factory function for the named tracker type.
func Register(name string, fn func(TrackerConfig) (Tracker, error)) {
	registry[name] = fn
}

// New constructs a Tracker from cfg using the registered factory.
func New(cfg TrackerConfig) (Tracker, error) {
	fn, ok := registry[cfg.Type]
	if !ok {
		return nil, fmt.Errorf("unknown tracker type %q — is the adapter registered?", cfg.Type)
	}

	return fn(cfg)
}
```

- [ ] **Step 4: Simplify the test helper** — replace the manual `containsSubstring` helpers with `strings.Contains`:

```go
// tracker/tracker_test.go
package tracker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

type stubTracker struct{}

func (s *stubTracker) ListIssues(_ context.Context) ([]tracker.Issue, error) { return nil, nil }
func (s *stubTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }

func TestRegisterAndNew_happy(t *testing.T) {
	t.Parallel()

	const key = "stub-test-register"
	tracker.Register(key, func(_ tracker.TrackerConfig) (tracker.Tracker, error) {
		return &stubTracker{}, nil
	})

	tr, err := tracker.New(tracker.TrackerConfig{Type: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tr == nil {
		t.Error("New returned nil tracker")
	}
}

func TestNew_unknownType(t *testing.T) {
	t.Parallel()

	_, err := tracker.New(tracker.TrackerConfig{Type: "no-such-adapter-xyz"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-adapter-xyz") {
		t.Errorf("error should mention the unknown type, got: %v", err)
	}
}
```

- [ ] **Step 5: Run tests and verify they pass**

```bash
cd /workspace && mise exec -- go test ./tracker/... -v 2>&1
```

Expected: `PASS` — `TestRegisterAndNew_happy` and `TestNew_unknownType`.

- [ ] **Step 6: Commit**

```bash
git add tracker/tracker.go tracker/tracker_test.go
git commit -m "feat(tracker): add Tracker interface, Issue model, and registry"
```

---

## Task 2: Redmine adapter

**Files:**
- Modify: `go.mod` (add `github.com/mattn/go-redmine`)
- Create: `tracker/redmine/redmine.go`
- Create: `tracker/redmine/init.go`
- Create: `tracker/redmine/redmine_test.go`

- [ ] **Step 1: Add go-redmine dependency**

```bash
cd /workspace && mise exec -- go get github.com/mattn/go-redmine@latest 2>&1
```

Expected: `go: added github.com/mattn/go-redmine vX.X.X`

- [ ] **Step 2: Write failing tests**

```go
// tracker/redmine/redmine_test.go
package redmine_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/redmine"
)

func TestListIssues_success(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issues":[{"id":1,"subject":"Fix login","description":"details","status":{"id":1,"name":"New"}}],"total_count":1,"offset":0,"limit":100}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, err := redmine.New(tracker.TrackerConfig{URL: srv.URL, Token: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	issues, err := adapter.ListIssues(t.Context())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].ID != "1" {
		t.Errorf("ID = %q, want %q", issues[0].ID, "1")
	}
	if issues[0].Subject != "Fix login" {
		t.Errorf("Subject = %q, want %q", issues[0].Subject, "Fix login")
	}
	if issues[0].TrackerType != "redmine" {
		t.Errorf("TrackerType = %q, want %q", issues[0].TrackerType, "redmine")
	}
}

func TestListIssues_authFailure(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, _ := redmine.New(tracker.TrackerConfig{URL: srv.URL, Token: "bad-key"})
	_, err := adapter.ListIssues(t.Context())
	if err == nil {
		t.Error("expected error on 401, got nil")
	}
}

func TestUpdateIssueStatus_success(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issue_statuses.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issue_statuses":[{"id":1,"name":"New"},{"id":2,"name":"In Progress"}]}`)
	})
	mux.HandleFunc("/issues/42.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, _ := redmine.New(tracker.TrackerConfig{URL: srv.URL, Token: "key"})
	if err := adapter.UpdateIssueStatus(t.Context(), "42", "In Progress"); err != nil {
		t.Fatalf("UpdateIssueStatus: %v", err)
	}
}

func TestUpdateIssueStatus_statusNotFound(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issue_statuses.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issue_statuses":[{"id":1,"name":"New"}]}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, _ := redmine.New(tracker.TrackerConfig{URL: srv.URL, Token: "key"})
	err := adapter.UpdateIssueStatus(t.Context(), "42", "In Progress")
	if err == nil {
		t.Error("expected error for missing status, got nil")
	}
	if !strings.Contains(err.Error(), "In Progress") {
		t.Errorf("error should mention the status name, got: %v", err)
	}
}
```

- [ ] **Step 3: Run to verify tests fail**

```bash
cd /workspace && mise exec -- go test ./tracker/redmine/... 2>&1
```

Expected: compilation error — `tracker/redmine` package does not exist yet.

- [ ] **Step 4: Write tracker/redmine/redmine.go**

```go
// tracker/redmine/redmine.go
package redmine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	redminelib "github.com/mattn/go-redmine"

	"github.com/piprim/git-zf/tracker"
)

type redmineAdapter struct {
	client *redminelib.Client
	cfg    tracker.TrackerConfig
}

// New creates a Redmine adapter from cfg.
func New(cfg tracker.TrackerConfig) (tracker.Tracker, error) {
	c := redminelib.NewClient(cfg.URL, cfg.Token)

	return &redmineAdapter{client: c, cfg: cfg}, nil
}

// ListIssues fetches open issues assigned to the authenticated user.
// Equivalent to GET /issues.json?assigned_to_id=me&status_id=open&limit=100
func (a *redmineAdapter) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	filter := redminelib.IssueFilter{
		AssignedToId: "me",
		StatusId:     "open",
		Limit:        100,
	}

	issues, err := a.client.IssuesByFilter(filter)
	if err != nil {
		return nil, fmt.Errorf("fetch redmine issues: %w", err)
	}

	result := make([]tracker.Issue, len(issues))
	for i, iss := range issues {
		result[i] = tracker.Issue{
			TrackerType: "redmine",
			ID:          strconv.Itoa(iss.Id),
			Subject:     iss.Subject,
			Description: iss.Description,
			Status:      iss.Status.Name,
		}
	}

	return result, nil
}

// UpdateIssueStatus resolves statusName via GET /issue_statuses.json,
// then PUTs the matching status_id onto the issue. Case-insensitive match.
func (a *redmineAdapter) UpdateIssueStatus(_ context.Context, issueID, statusName string) error {
	id, err := strconv.Atoi(issueID)
	if err != nil {
		return fmt.Errorf("invalid issue id %q: %w", issueID, err)
	}

	statuses, err := a.client.IssueStatuses()
	if err != nil {
		return fmt.Errorf("fetch issue statuses: %w", err)
	}

	var statusID int
	found := false

	for _, s := range statuses {
		if strings.EqualFold(s.Name, statusName) {
			statusID = s.Id
			found = true

			break
		}
	}

	if !found {
		return fmt.Errorf("status %q not found in Redmine; check in-progress-status in .git-zf.json", statusName)
	}

	if err := a.client.UpdateIssue(redminelib.Issue{Id: id, StatusId: statusID}); err != nil {
		return fmt.Errorf("update issue %s status: %w", issueID, err)
	}

	return nil
}
```

- [ ] **Step 5: Write tracker/redmine/init.go**

```go
// tracker/redmine/init.go
package redmine

import "github.com/piprim/git-zf/tracker"

func init() {
	tracker.Register("redmine", New)
}
```

- [ ] **Step 6: Run tests and verify they pass**

```bash
cd /workspace && mise exec -- go test ./tracker/... -v 2>&1
```

Expected: all tests pass including `TestListIssues_success`, `TestListIssues_authFailure`, `TestUpdateIssueStatus_success`, `TestUpdateIssueStatus_statusNotFound`.

- [ ] **Step 7: Commit**

```bash
git add tracker/redmine/ go.mod go.sum
git commit -m "feat(tracker): add Redmine adapter using go-redmine"
```

---

## Task 3: Store migration — tracker_type column

**Files:**
- Create: `store/migrations/0002_add_tracker_type.sql`
- Modify: `store/store.go` (`Issue.TrackerType *string`; `InsertIssueWithBranch` updated)
- Modify: `store/store_test.go` (two new tests)

- [ ] **Step 1: Write failing tests**

Add to `store/store_test.go`:

```go
func TestMigration_trackerType_existingRowsNull(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	// Insert a row without specifying tracker_type (should default to NULL).
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "TRK-1", Title: "Pre-tracker issue", StatusID: 1},
		&Branch{UUID: "trk-uuid-1", Name: "TRK-1@feat@pre-tracker@trk-uuid-1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var trackerType *string
	row := s.db.QueryRow("SELECT tracker_type FROM issues WHERE id_slug = ?", "TRK-1")
	if err := row.Scan(&trackerType); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if trackerType != nil {
		t.Errorf("tracker_type = %v, want nil (NULL)", trackerType)
	}
}

func TestInsertIssueWithBranch_trackerTypeRoundTrip(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	tt := "redmine"
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "TRK-2", Title: "Tracker issue", StatusID: 1, TrackerType: &tt},
		&Branch{UUID: "trk-uuid-2", Name: "TRK-2@feat@tracker-issue@trk-uuid-2", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got *string
	row := s.db.QueryRow("SELECT tracker_type FROM issues WHERE id_slug = ?", "TRK-2")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got == nil {
		t.Fatal("tracker_type is nil, want non-nil")
	}
	if *got != "redmine" {
		t.Errorf("tracker_type = %q, want %q", *got, "redmine")
	}
}
```

- [ ] **Step 2: Run to verify tests fail**

```bash
cd /workspace && mise exec -- go test ./store/... -run 'TestMigration_trackerType|TestInsertIssueWithBranch_trackerTypeRoundTrip' -v 2>&1
```

Expected: compilation error — `Issue` has no `TrackerType` field yet.

- [ ] **Step 3: Create the migration SQL**

```sql
-- store/migrations/0002_add_tracker_type.sql
ALTER TABLE issues ADD COLUMN tracker_type TEXT DEFAULT NULL;
PRAGMA user_version = 2;
```

> **Note:** The `migrate()` function in `store.go` sets `user_version` programmatically after each migration file. Do NOT include `PRAGMA user_version` in the SQL file — the migrate loop handles versioning itself. The file content should only be:

```sql
ALTER TABLE issues ADD COLUMN tracker_type TEXT DEFAULT NULL;
```

- [ ] **Step 4: Update store/store.go**

In `store.go`, update the `Issue` struct:

```go
// Issue represents a tracked issue record.
type Issue struct {
	ID          int64
	IDSlug      string  // tracker string ID: "ABC-42", "42", …
	Title       string
	StatusID    int64
	TrackerType *string // nil = manual entry; non-nil = tracker type (e.g. "redmine")
}
```

Update `InsertIssueWithBranch` — replace the INSERT statement for issues:

```go
res, err := tx.ExecContext(ctx,
    `INSERT INTO issues (id_slug, title, status_id, tracker_type) VALUES (?, ?, ?, ?)`,
    issue.IDSlug, issue.Title, issue.StatusID, issue.TrackerType,
)
```

(The rest of `InsertIssueWithBranch` is unchanged.)

- [ ] **Step 5: Run tests and verify they pass**

```bash
cd /workspace && mise exec -- go test ./store/... -v 2>&1
```

Expected: all existing tests still pass, plus `TestMigration_trackerType_existingRowsNull` and `TestInsertIssueWithBranch_trackerTypeRoundTrip`.

- [ ] **Step 6: Commit**

```bash
git add store/migrations/0002_add_tracker_type.sql store/store.go store/store_test.go
git commit -m "feat(store): add tracker_type column to issues"
```

---

## Task 4: TUI groups for tracker flow

**Files:**
- Modify: `tui/issue.go`

No unit tests for TUI (per spec — covered by manual smoke tests). Verify with `go build`.

- [ ] **Step 1: Add tracker TUI functions to tui/issue.go**

Add to `tui/issue.go`. New imports needed: `"fmt"` and `"github.com/piprim/git-zf/tracker"`. Full updated file:

```go
package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/tracker"
)

const (
	IssueActionNameStart = "issueStart"
	IssueActionNameList  = "issueList"
	IssueActionNameClose = "issueClose"
)

// IssueActionSelect presents the list of available issue actions.
func IssueActionSelect(action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Issue action:").
			Options(
				huh.NewOption("Start\n"+descStyle.Render(
					"Start working on an issue (create branch)"), IssueActionNameStart),
				huh.NewOption("List\n"+descStyle.Render("List open issues"), IssueActionNameList),
				huh.NewOption("Close\n"+descStyle.Render("Close an issue"), IssueActionNameClose),
			).
			Value(action),
	)
}

func IssueInput(issueID, title, branchType *string, allowedBranchTypes []string) *huh.Group {
	typeOpts := make([]huh.Option[string], 0, len(allowedBranchTypes))
	for _, allowed := range allowedBranchTypes {
		typeOpts = append(typeOpts, huh.NewOption(allowed, allowed))
	}

	group := huh.NewGroup(
		huh.NewInput().
			Title("Issue ID:").
			Placeholder("ABC-42").
			Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}

				return nil
			}).
			Value(issueID),
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}

				return nil
			}).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(branchType),
	)

	return group
}

func IssueConfirm(confirmTitle string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(confirmTitle).
			Value(confirmed),
	)
}

// IssueTrackerToggle asks whether to fetch issues from the tracker.
// trackerFirst=true pre-selects YES (used for `issue start`);
// trackerFirst=false pre-selects NO (used for `branch new`).
func IssueTrackerToggle(useTracker *bool, trackerFirst bool, trackerType string) *huh.Group {
	*useTracker = trackerFirst

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Fetch issues from %s?", trackerType)).
			Value(useTracker),
	)
}

// IssueTrackerPicker shows the live issue list and branch type selector.
func IssueTrackerPicker(issues []tracker.Issue, selected *tracker.Issue, types []string, branchType *string) *huh.Group {
	opts := make([]huh.Option[tracker.Issue], len(issues))
	for i, iss := range issues {
		label := fmt.Sprintf("[%s] %s", iss.ID, iss.Subject)
		opts[i] = huh.NewOption(label, iss)
	}

	typeOpts := make([]huh.Option[string], len(types))
	for i, tp := range types {
		typeOpts[i] = huh.NewOption(tp, tp)
	}

	return huh.NewGroup(
		huh.NewSelect[tracker.Issue]().
			Title("Pick an issue:").
			Options(opts...).
			Value(selected),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(branchType),
	)
}

// IssueTrackerError shows an error note with a "Continue with manual input" button.
func IssueTrackerError(msg string) *huh.Group {
	return huh.NewGroup(
		huh.NewNote().
			Title("Tracker error").
			Description(msg).
			Next(true).
			NextLabel("Continue with manual input"),
	)
}

// IssueUpdateStatusConfirm asks whether to update the issue status in the tracker.
func IssueUpdateStatusConfirm(issueID, statusName, trackerType string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Update issue %s to %q in %s?", issueID, statusName, trackerType)).
			Value(confirmed),
	)
}
```

- [ ] **Step 2: Verify the build**

```bash
cd /workspace && mise exec -- go build ./... 2>&1
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add tui/issue.go
git commit -m "feat(tui): add tracker toggle, picker, error, and status-confirm groups"
```

---

## Task 5: Command wiring — extend issueStartRunE + branchNewRunE

**Files:**
- Modify: `cmd/issue.go`
- Modify: `cmd/branch.go`

No unit tests for the TUI interaction path (per spec). Verify with `go build` and `go vet`.

- [ ] **Step 1: Rewrite cmd/issue.go**

Full replacement of `cmd/issue.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type issueStartFlags struct {
	trackerFirst bool
}

func getIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE:  issueRunE,
	}
	cmd.AddCommand(getIssueStartCmd())

	return cmd
}

func issueRunE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return issueStartRunE(cmd, args)
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}

func getIssueStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-cz.db.`,
		RunE: issueStartRunE,
	}
}

func issueStartRunE(cmd *cobra.Command, _ []string) error {
	return runIssueStart(cmd.Context(), issueStartFlags{trackerFirst: true})
}

// runIssueStart contains the full issue-start flow. trackerFirst=true for
// `issue start` (tracker pre-selected); false for `branch new` (manual pre-selected).
func runIssueStart(ctx context.Context, flags issueStartFlags) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	msgCfg, err := loadMessageConfig()
	if err != nil {
		return fmt.Errorf("load message config: %w", err)
	}

	base := viper.GetString("branch.base")
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	allowedBranchTypes := getAllowedBranchType(msgCfg.Items)
	if len(allowedBranchTypes) == 0 {
		return errors.New("message config: no type options found in first item")
	}

	trackerCfg := tracker.TrackerConfig{
		Type:             viper.GetString("tracker.type"),
		URL:              viper.GetString("tracker.url"),
		Token:            viper.GetString("tracker.token"),
		InProgressStatus: viper.GetString("tracker.in-progress-status"),
	}
	if trackerCfg.InProgressStatus == "" {
		trackerCfg.InProgressStatus = "In Progress"
	}

	var issueID, title, branchType string
	var fromTracker bool
	var pickedIssue tracker.Issue
	var t tracker.Tracker

	if trackerCfg.Type != "" {
		t, err = tracker.New(trackerCfg)
		if err != nil {
			return fmt.Errorf("create tracker: %w", err)
		}

		var useTracker bool
		if err := huh.NewForm(tui.IssueTrackerToggle(&useTracker, flags.trackerFirst, trackerCfg.Type)).Run(); err != nil {
			return fmt.Errorf("tracker toggle: %w", err)
		}

		if useTracker {
			issues, listErr := t.ListIssues(ctx)
			if listErr != nil {
				if noteErr := huh.NewForm(tui.IssueTrackerError(listErr.Error())).Run(); noteErr != nil {
					return fmt.Errorf("error note: %w", noteErr)
				}
				// fallthrough to manual input
			} else {
				if err := huh.NewForm(tui.IssueTrackerPicker(issues, &pickedIssue, allowedBranchTypes, &branchType)).Run(); err != nil {
					return fmt.Errorf("tracker picker: %w", err)
				}
				issueID = pickedIssue.ID
				title = pickedIssue.Subject
				fromTracker = true
			}
		}
	}

	if !fromTracker {
		if err := huh.NewForm(tui.IssueInput(&issueID, &title, &branchType, allowedBranchTypes)).Run(); err != nil {
			return fmt.Errorf("issue form: %w", err)
		}
	}

	b, err := branch.New(issueID, branchType, title)
	if err != nil {
		return fmt.Errorf("assemble branch name: %w", err)
	}

	branchName := b.Name()

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create branch %q based on %q?", branchName, base), &confirmed,
	)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if err := client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	var tt *string
	if fromTracker {
		s := trackerCfg.Type
		tt = &s
	}

	if err := persist(ctx, client, b, title, tt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if fromTracker && t != nil {
		var updateStatus bool
		if err := huh.NewForm(tui.IssueUpdateStatusConfirm(
			pickedIssue.ID, trackerCfg.InProgressStatus, trackerCfg.Type, &updateStatus,
		)).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: status confirm form: %v\n", err)
		} else if updateStatus {
			if err := t.UpdateIssueStatus(ctx, pickedIssue.ID, trackerCfg.InProgressStatus); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not update tracker status: %v\n", err)
			}
		}
	}

	return nil
}

func getAllowedBranchType(items []tui.CommitItem) []string {
	if len(items) == 0 {
		return nil
	}

	allowedBranchTypes := make([]string, 0, len(items[0].Options))
	for _, opt := range items[0].Options {
		allowedBranchTypes = append(allowedBranchTypes, opt.Name)
	}

	return allowedBranchTypes
}

func persist(ctx context.Context, client *git.Client, b *branch.Branch, rawTitle string, trackerType *string) error {
	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: 1, TrackerType: trackerType},
		&store.Branch{UUID: b.ID(), Name: b.Name(), Type: b.Type(), StatusID: 1},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}

	return nil
}
```

- [ ] **Step 2: Update branchNewRunE in cmd/branch.go**

Replace `branchNewRunE` (currently a one-liner that delegates to `issueStartRunE`):

```go
// branchNewRunE delegates to runIssueStart with manual-first (tracker toggle defaults to NO).
func branchNewRunE(cmd *cobra.Command, _ []string) error {
	return runIssueStart(cmd.Context(), issueStartFlags{trackerFirst: false})
}
```

- [ ] **Step 3: Verify build and vet**

```bash
cd /workspace && mise exec -- go build ./... 2>&1 && mise exec -- go vet ./... 2>&1
```

Expected: no errors.

- [ ] **Step 4: Run all tests**

```bash
cd /workspace && mise exec -- go test ./... 2>&1
```

Expected: all tests pass. The `cmd` package tests (`TestBranchList_*`, `TestRunBranchPrune_*`) should continue to pass since `persist` is now called with an extra `trackerType *string` argument — verify that `cmd/branch_test.go` doesn't call `persist` directly (it doesn't — it calls `s.InsertIssueWithBranch` directly via `insertTestBranch`).

- [ ] **Step 5: Commit**

```bash
git add cmd/issue.go cmd/branch.go
git commit -m "feat(cmd): wire tracker flow into issue start and branch new"
```

---

## Self-Review

### Spec coverage

| Spec section | Covered by |
|---|---|
| tracker/ interface + registry | Task 1 |
| Redmine adapter using go-redmine | Task 2 |
| store/migrations/0002_add_tracker_type.sql | Task 3 |
| store.Issue.TrackerType *string | Task 3 |
| InsertIssueWithBranch passes TrackerType | Task 3 |
| .git-zf.json tracker block (Viper) | Task 5 (viper.GetString calls) |
| IssueTrackerToggle | Task 4 |
| IssueTrackerPicker | Task 4 |
| IssueTrackerError | Task 4 |
| IssueUpdateStatusConfirm | Task 4 |
| issueStartRunE extended flow | Task 5 |
| branchNewRunE uses trackerFirst=false | Task 5 |
| tracker_test.go registry tests | Task 1 |
| redmine_test.go httptest tests | Task 2 |
| store_test.go migration + TrackerType | Task 3 |
| Error handling: ListIssues failure → IssueTrackerError | Task 5 |
| Error handling: UpdateIssueStatus failure → non-fatal stderr | Task 5 |
| InProgressStatus defaults to "In Progress" | Task 5 |
| tracker.Register triggered via blank import | Task 5 |

All sections covered. ✅

### Placeholder scan

No TBD, TODO, or vague requirements. Every code step has complete code. ✅

### Type consistency

- `tracker.Issue.ID string` — used as `issueID = pickedIssue.ID` ✅
- `tracker.Issue.Subject string` — used as `title = pickedIssue.Subject` ✅
- `IssueTrackerPicker(issues []tracker.Issue, selected *tracker.Issue, ...)` — called as `IssueTrackerPicker(issues, &pickedIssue, ...)` ✅
- `persist(ctx, client, b, title, tt)` — 5-arg signature consistent in definition and call ✅
- `store.Issue.TrackerType *string` — passed as `nil` (manual) or `&trackerCfg.Type` (tracker) ✅
- `tracker.New(trackerCfg)` — returns `(Tracker, error)`, stored as `t tracker.Tracker` ✅
