# GitHub Tracker Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a GitHub issue tracker adapter (using `go-github/v85`) to git-zf, extend `IssueTrackerConfig` with an optional `Projects []string` filter, populate the new `tracker.Issue.Project` field from both adapters, and integrate per-project filtering into the existing bubbletea-based issues-list TUI.

**Architecture:** New `tracker/github/{init.go, github.go, github_test.go}` mirrors `tracker/redmine/`. The adapter delegates all HTTP/auth concerns to `go-github`; only mapping `*github.Issue` → `tracker.Issue` and a client-side `Projects` filter live in the adapter. `IssueTrackerConfig.Projects` is honoured uniformly by both adapters via post-fetch filtering. `store.IssueRow` gains a `Project` field that flows through to both renderers (bubbletea TUI and lipgloss `--stdout` table).

**Tech Stack:** Go 1.25, `github.com/google/go-github/v85`, `mattn/go-redmine`, `charmbracelet/{bubbletea,bubbles/table,huh,lipgloss}`, `mitchellh/mapstructure`. Toolchain via mise — always `mise exec -- go ...`.

---

## Spec deviation (please review before execution)

The spec (`docs/superpowers/specs/2026-05-07-github-tracker-design.md`) calls for a new TUI helper `IssueProjectFilter(projects, current) (string, error)` modelled on a pre-existing `IssueStatusFilter` huh form. **That precedent doesn't exist in the codebase**: status filtering happens _inside_ the bubbletea `issueTableModel` via the `tab` key (and text search via `/`).

This plan adapts the spec's intent — "let users filter by project in the issues list" — to the actual TUI pattern: extend `applyFilters`, add a `projectFilter` field to `issueTableModel`, bind a key (`p`) to cycle through projects, and render a project tab strip alongside the existing status tabs. The pre-render huh prompt is skipped. The `--stdout` and `--json` paths remain unfiltered (matching the spec); only the interactive bubbletea view exposes runtime project switching.

If you'd prefer the literal spec implementation (a separate huh form invoked before `tea.NewProgram`), say so and we'll restructure tasks 9-10.

---

## File map

| File | Status | Responsibility |
|------|--------|----------------|
| `go.mod` / `go.sum` | modify | Add `github.com/google/go-github/v85` |
| `config/config.go` | modify | `IssueTrackerConfig.Projects []string` field |
| `config/config_test.go` | modify | round-trip JSON test for `Projects` |
| `tracker/tracker.go` | modify | `Issue.Project string` field |
| `tracker/redmine/redmine.go` | modify | `project` substruct, populate `Issue.Project`, client-side `Projects` filter |
| `tracker/redmine/redmine_test.go` | modify | `TestListIssues_populatesProject`, `TestListIssues_projectsFilter` |
| `tracker/github/init.go` | create | `tracker.Register("github", New)` |
| `tracker/github/github.go` | create | `githubAdapter` + `New` + `ListIssues` + `ListStatuses` + `UpdateIssueStatus` |
| `tracker/github/github_test.go` | create | adapter tests via `httptest` (internal package) |
| `cmd/issue/issue.go` | modify | blank-import `tracker/github` for `init()` registration |
| `store/store.go` | modify | `IssueRow.Project string` field |
| `cmd/issue/list.go` | modify | plumb `tracker.Issue.Project` into `IssueRow.Project` |
| `tui/issue.go` | modify | extend `applyFilters` + `issueTableModel`: project tab, `p` key, conditional `Project` column |
| `tui/issue_test.go` | modify | tests for project filter + project tab + cycle |
| `tty/issue.go` | modify | conditional `Project` column when multi-project |

---

## Task 1: `IssueTrackerConfig.Projects []string`

**Files:**
- Modify: `/workspace/config/config.go`
- Modify: `/workspace/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `/workspace/config/config_test.go`:

```go
func TestLoad_projects(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".git-zf.json")

	const blob = `{
		"issue-tracker": {
			"type": "github",
			"url": "https://api.github.com",
			"token": "x",
			"projects": ["a/b", "c/d"]
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(blob), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	v := viper.New()
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("read cfg: %v", err)
	}

	// Swap the global viper used by config.Load() with our local instance.
	viper.Reset()
	defer viper.Reset()
	for _, k := range v.AllKeys() {
		viper.Set(k, v.Get(k))
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []string{"a/b", "c/d"}
	if !slices.Equal(cfg.IssueTracker.Projects, want) {
		t.Errorf("Projects = %v, want %v", cfg.IssueTracker.Projects, want)
	}
}
```

Add imports `"os"`, `"path/filepath"`, `"slices"`, `"github.com/spf13/viper"` to the test file's import block if missing. The package is `config_test` (external) — verify by reading the existing file header before editing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./config/... -run TestLoad_projects -v`
Expected: build failure — `cfg.IssueTracker.Projects undefined`.

- [ ] **Step 3: Add the field**

In `/workspace/config/config.go`, replace the `IssueTrackerConfig` struct:

```go
// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type     string   `json:"type"     mapstructure:"type"`
	URL      string   `json:"url"      mapstructure:"url"`
	Token    string   `json:"token"    mapstructure:"token"`
	Projects []string `json:"projects" mapstructure:"projects"`
}
```

The `Load()` function already calls `viper.UnmarshalKey("issue-tracker", &cfg.IssueTracker)` which uses mapstructure — slices are decoded fine without `ZeroFields` here because the default is `nil` (no merge ambiguity). No change to `Load()`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./config/... -run TestLoad_projects -v`
Expected: PASS.

- [ ] **Step 5: Run all config tests + full suite**

Run: `mise exec -- go test ./config/... && mise exec -- go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit (SKIP — user handles git)**

Do not run `git add`/`git commit`.

---

## Task 2: `tracker.Issue.Project string`

**Files:**
- Modify: `/workspace/tracker/tracker.go`

This field has no behavioural test of its own (it's a struct field). Tasks 3 and 5 verify it via the redmine and github adapter tests respectively. The full test suite must still build after the change.

- [ ] **Step 1: Add the field**

In `/workspace/tracker/tracker.go`, replace the `Issue` struct:

```go
// Issue is the tracker-agnostic representation of a work item.
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
	Project     string
}
```

- [ ] **Step 2: Verify the build still passes**

Run: `mise exec -- go build ./...`
Expected: clean. Existing zero-value `tracker.Issue{...}` literals at call sites work unchanged because `Project` defaults to `""`.

- [ ] **Step 3: Run all tests to verify no regressions**

Run: `mise exec -- go test ./...`
Expected: all PASS (existing redmine tests don't yet check `Project`, so populating it later adds value without breaking them now).

- [ ] **Step 4: Commit (SKIP — user handles git)**

---

## Task 3: Redmine adapter — populate `Project` and apply `Projects` filter

**Files:**
- Modify: `/workspace/tracker/redmine/redmine.go`
- Modify: `/workspace/tracker/redmine/redmine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `/workspace/tracker/redmine/redmine_test.go`:

```go
func TestListIssues_populatesProject(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issues":[{"id":1,"subject":"x","description":"","status":{"id":1,"name":"New"},"project":{"id":7,"identifier":"myproj"}}],"total_count":1,"offset":0,"limit":100}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "k"})
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
	if issues[0].Project != "myproj" {
		t.Errorf("Project = %q, want %q", issues[0].Project, "myproj")
	}
}

func TestListIssues_projectsFilter(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"issues":[
			{"id":1,"subject":"keep","status":{"id":1,"name":"New"},"project":{"id":1,"identifier":"foo"}},
			{"id":2,"subject":"drop","status":{"id":1,"name":"New"},"project":{"id":2,"identifier":"bar"}}
		],"total_count":2,"offset":0,"limit":100}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, err := redmine.New(config.IssueTrackerConfig{
		URL:      srv.URL,
		Token:    "k",
		Projects: []string{"foo"},
	})
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
	if issues[0].ID != "1" || issues[0].Project != "foo" {
		t.Errorf("got issue %+v, want id=1 project=foo", issues[0])
	}
}

func TestListIssues_projectsFilter_numericFallback(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// project has only a numeric id; identifier is empty
		fmt.Fprint(w, `{"issues":[
			{"id":1,"subject":"x","status":{"id":1,"name":"New"},"project":{"id":42,"identifier":""}}
		],"total_count":1,"offset":0,"limit":100}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	adapter, err := redmine.New(config.IssueTrackerConfig{
		URL:      srv.URL,
		Token:    "k",
		Projects: []string{"42"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	issues, err := adapter.ListIssues(t.Context())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Project != "42" {
		t.Errorf("got %+v, want one issue with Project=42", issues)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/redmine/... -run "TestListIssues_populatesProject|TestListIssues_projectsFilter" -v`
Expected: FAIL — issues come back with empty `Project` (the JSON struct doesn't have a `Project` field yet, and the filter is not applied).

- [ ] **Step 3: Add the `project` JSON struct and update `issue`**

In `/workspace/tracker/redmine/redmine.go`, after the `status` struct (around line 25), add:

```go
type project struct {
	ID         int    `json:"id"`
	Identifier string `json:"identifier"`
}
```

Then extend the `issue` struct to include the optional project field:

```go
type issue struct {
	ID          int      `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Status      *status  `json:"status"`
	Project     *project `json:"project"`
}
```

- [ ] **Step 4: Populate `Issue.Project` and apply the filter in `ListIssues`**

Replace the result-building loop in `ListIssues` (currently at lines ~88-104):

```go
projectsSet := toRedmineProjectsSet(a.cfg.Projects)

result := make([]tracker.Issue, 0, len(payload.Issues))
for _, iss := range payload.Issues {
	statusName := ""
	if iss.Status != nil {
		statusName = iss.Status.Name
	}

	proj := redmineProjectName(iss.Project)
	if projectsSet != nil {
		if _, ok := projectsSet[proj]; !ok {
			continue
		}
	}

	result = append(result, tracker.Issue{
		TrackerType: trackerType,
		ID:          strconv.Itoa(iss.ID),
		Subject:     iss.Subject,
		Description: iss.Description,
		Status:      statusName,
		Project:     proj,
	})
}

return result, nil
```

Add these two unexported helpers at the bottom of the file (after `UpdateIssueStatus`):

```go
// toRedmineProjectsSet builds a lookup set from cfg.Projects. Returns nil when
// the slice is empty so callers can short-circuit the filter.
func toRedmineProjectsSet(list []string) map[string]struct{} {
	if len(list) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(list))
	for _, p := range list {
		out[p] = struct{}{}
	}

	return out
}

// redmineProjectName picks the slug if present, falls back to the numeric ID,
// or returns "" when the project is omitted from the response.
func redmineProjectName(p *project) string {
	if p == nil {
		return ""
	}
	if p.Identifier != "" {
		return p.Identifier
	}

	return strconv.Itoa(p.ID)
}
```

- [ ] **Step 5: Run the new tests to verify they pass**

Run: `mise exec -- go test ./tracker/redmine/... -run "TestListIssues_populatesProject|TestListIssues_projectsFilter" -v`
Expected: all 3 PASS.

- [ ] **Step 6: Run the full redmine + tracker suites for regressions**

Run: `mise exec -- go test ./tracker/...`
Expected: all PASS, including the existing `TestListIssues_success`, `TestListIssues_authFailure`, `TestUpdateIssueStatus_*`.

- [ ] **Step 7: Commit (SKIP — user handles git)**

---

## Task 4: Add `go-github/v85` dependency + GitHub adapter scaffold

**Files:**
- Modify: `/workspace/go.mod`, `/workspace/go.sum`
- Create: `/workspace/tracker/github/init.go`
- Create: `/workspace/tracker/github/github.go`
- Create: `/workspace/tracker/github/github_test.go` (skeleton — full coverage in later tasks)

This task lays the package down with `New` only (no `ListIssues`/`ListStatuses`/`UpdateIssueStatus` yet). Stub methods return `errors.New("not implemented")` so the package satisfies `tracker.Tracker` and the registry compiles.

- [ ] **Step 1: Add the dependency**

Run: `mise exec -- go get github.com/google/go-github/v85@latest`
Expected: `go.mod` updated, `go.sum` populated. If `v85` is not yet published, fall back to the latest available major (`go get github.com/google/go-github/v74@latest` or current head) and **change every `v85` reference in this plan to the version actually fetched** before proceeding.

- [ ] **Step 2: Write the failing test for `New`**

Create `/workspace/tracker/github/github_test.go`:

```go
package github

import (
	"testing"

	"github.com/piprim/git-zf/config"
)

func TestNew_missingToken(t *testing.T) {
	t.Parallel()

	_, err := New(config.IssueTrackerConfig{})
	if err == nil {
		t.Fatal("New with empty Token: expected error, got nil")
	}
}

func TestNew_default(t *testing.T) {
	t.Parallel()

	a, err := New(config.IssueTrackerConfig{Token: "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatal("New returned nil adapter")
	}
}

func TestNew_enterpriseURL(t *testing.T) {
	t.Parallel()

	_, err := New(config.IssueTrackerConfig{
		Token: "x",
		URL:   "https://github.example.com/api/v3/",
	})
	if err != nil {
		t.Fatalf("New with enterprise URL: %v", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `mise exec -- go test ./tracker/github/... -run TestNew -v`
Expected: build failure — package does not exist.

- [ ] **Step 4: Create `init.go`**

Create `/workspace/tracker/github/init.go`:

```go
package github

import "github.com/piprim/git-zf/tracker"

//nolint:gochecknoinits // Register pattern needs it
func init() {
	tracker.Register(trackerType, New)
}
```

- [ ] **Step 5: Create `github.go` with constants + `New` + stubbed methods**

Create `/workspace/tracker/github/github.go`:

```go
package github

import (
	"context"
	"errors"
	"fmt"

	gogithub "github.com/google/go-github/v85/github"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

const (
	trackerType  = "github"
	statusOpen   = "open"
	statusClosed = "closed"
)

type githubAdapter struct {
	client   *gogithub.Client
	cfg      config.IssueTrackerConfig
	projects map[string]struct{}
}

// New creates a GitHub adapter from cfg.
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.Token == "" {
		return nil, errors.New("github: Token is required")
	}

	c := gogithub.NewClient(nil).WithAuthToken(cfg.Token)
	if cfg.URL != "" && cfg.URL != "https://api.github.com" {
		var err error
		c, err = c.WithEnterpriseURLs(cfg.URL, cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("github: enterprise URL %q: %w", cfg.URL, err)
		}
	}

	return &githubAdapter{
		client:   c,
		cfg:      cfg,
		projects: toProjectSet(cfg.Projects),
	}, nil
}

// toProjectSet builds a lookup set from cfg.Projects. Returns nil when the
// slice is empty so callers can short-circuit the filter.
func toProjectSet(list []string) map[string]struct{} {
	if len(list) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(list))
	for _, p := range list {
		out[p] = struct{}{}
	}

	return out
}

// ListIssues — implemented in Task 5.
func (a *githubAdapter) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	return nil, errors.New("github: ListIssues not implemented")
}

// ListStatuses — implemented in Task 6.
func (a *githubAdapter) ListStatuses(_ context.Context) ([]string, error) {
	return nil, errors.New("github: ListStatuses not implemented")
}

// UpdateIssueStatus — implemented in Task 7.
func (a *githubAdapter) UpdateIssueStatus(_ context.Context, _, _ string) error {
	return errors.New("github: UpdateIssueStatus not implemented")
}
```

- [ ] **Step 6: Run the new tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/... -run TestNew -v`
Expected: all 3 PASS.

- [ ] **Step 7: Run the full suite**

Run: `mise exec -- go test ./...`
Expected: all PASS. The github stub methods aren't called yet.

- [ ] **Step 8: Commit (SKIP — user handles git)**

---

## Task 5: GitHub adapter — `ListIssues`

**Files:**
- Modify: `/workspace/tracker/github/github.go`
- Modify: `/workspace/tracker/github/github_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `/workspace/tracker/github/github_test.go`:

```go
import (
	// ...existing imports
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
)

// newTestAdapter returns a *githubAdapter whose BaseURL points at srv. cfg.Token
// is set to "test"; the caller may override other fields via opts (currently a
// single Projects override).
func newTestAdapter(t *testing.T, srv *httptest.Server, projects []string) *githubAdapter {
	t.Helper()

	a, err := New(config.IssueTrackerConfig{Token: "test", Projects: projects})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ga, ok := a.(*githubAdapter)
	if !ok {
		t.Fatalf("New returned %T, want *githubAdapter", a)
	}

	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}

	ga.client.BaseURL = u

	return ga
}

func TestListIssues_success(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"number": 42, "title": "Fix login", "body": "details", "state": "open",
			 "repository": {"full_name": "octocat/hello"}},
			{"number": 99, "title": "PR title", "body": "", "state": "open",
			 "repository": {"full_name": "octocat/hello"},
			 "pull_request": {"url": "https://api.github.com/repos/octocat/hello/pulls/99"}}
		]`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, nil)

	issues, err := a.ListIssues(t.Context())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1 (PR should be filtered)", len(issues))
	}
	got := issues[0]
	if got.ID != "42" || got.Subject != "Fix login" || got.Status != "open" || got.Project != "octocat/hello" || got.TrackerType != "github" {
		t.Errorf("issue mismatch: %+v", got)
	}
}

func TestListIssues_pagination(t *testing.T) {
	t.Parallel()

	var requestedPages []string

	mux := http.NewServeMux()
	mux.HandleFunc("/issues", func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		w.Header().Set("Content-Type", "application/json")

		switch page {
		case "", "1":
			// page 1 announces a next page
			w.Header().Set("Link", `<`+"http://"+r.Host+`/issues?page=2>; rel="next"`)
			fmt.Fprint(w, `[{"number":1,"title":"a","state":"open","repository":{"full_name":"o/r"}}]`)
		case "2":
			fmt.Fprint(w, `[{"number":2,"title":"b","state":"open","repository":{"full_name":"o/r"}}]`)
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, nil)

	issues, err := a.ListIssues(t.Context())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}
	if issues[0].ID != "1" || issues[1].ID != "2" {
		t.Errorf("ids in wrong order: %+v", issues)
	}
}

func TestListIssues_projectFilter(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/issues", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"number": 1, "title": "keep", "state": "open", "repository": {"full_name": "a/b"}},
			{"number": 2, "title": "drop", "state": "open", "repository": {"full_name": "c/d"}}
		]`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, []string{"a/b"})

	issues, err := a.ListIssues(t.Context())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(issues))
	}
	if issues[0].ID != "1" || issues[0].Project != "a/b" {
		t.Errorf("issue = %+v, want id=1 project=a/b", issues[0])
	}
}

// guard against accidentally depending on encoding/json import being unused.
var _ = json.Marshal
```

The `var _ = json.Marshal` line keeps the `encoding/json` import used until later tasks add a real consumer. It can be removed once Task 7 lands. (Alternative: delay adding the `encoding/json` import to Task 7. Either works; the explicit guard is safer if you transcribe imports verbatim.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/... -run TestListIssues -v`
Expected: FAIL with `"github: ListIssues not implemented"`.

- [ ] **Step 3: Implement `ListIssues`**

In `/workspace/tracker/github/github.go`:

1. Add to imports: `"strconv"`.

2. Replace the `ListIssues` stub:

```go
// ListIssues fetches open issues assigned to the authenticated user across all
// accessible repositories, paginates through every page, drops pull requests,
// and applies the optional client-side Projects filter.
func (a *githubAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
	opt := &gogithub.IssueListOptions{
		Filter:      "assigned",
		State:       statusOpen,
		ListOptions: gogithub.ListOptions{PerPage: 100},
	}

	var out []tracker.Issue

	for {
		page, resp, err := a.client.Issues.ListByAuthenticatedUser(ctx, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list issues: %w", err)
		}

		for _, iss := range page {
			if iss.IsPullRequest() {
				continue
			}

			proj := iss.GetRepository().GetFullName()
			if a.projects != nil {
				if _, ok := a.projects[proj]; !ok {
					continue
				}
			}

			out = append(out, tracker.Issue{
				TrackerType: trackerType,
				ID:          strconv.Itoa(iss.GetNumber()),
				Subject:     iss.GetTitle(),
				Description: iss.GetBody(),
				Status:      iss.GetState(),
				Project:     proj,
			})
		}

		if resp.NextPage == 0 {
			break
		}

		opt.Page = resp.NextPage
	}

	return out, nil
}
```

**Note on the go-github API surface:** the spec uses `client.Issues.ListByAuthenticatedUser`. If the version you fetched in Task 4 exposes this method as `client.Issues.List` (older signature) instead, adapt the call accordingly — both the v74+ and v85 lines have the cross-repo listing under one of these names. Verify with `go doc github.com/google/go-github/vNN/github Client.Issues` after the dependency is added.

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/... -run TestListIssues -v`
Expected: all 3 PASS.

- [ ] **Step 5: Run the full github + tracker suites**

Run: `mise exec -- go test ./tracker/...`
Expected: all PASS.

- [ ] **Step 6: Commit (SKIP — user handles git)**

---

## Task 6: GitHub adapter — `ListStatuses`

**Files:**
- Modify: `/workspace/tracker/github/github.go`
- Modify: `/workspace/tracker/github/github_test.go`

- [ ] **Step 1: Write the failing test**

Append to `/workspace/tracker/github/github_test.go`:

```go
func TestListStatuses(t *testing.T) {
	t.Parallel()

	a, err := New(config.IssueTrackerConfig{Token: "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := a.ListStatuses(t.Context())
	if err != nil {
		t.Fatalf("ListStatuses: %v", err)
	}

	want := []string{"open", "closed"}
	if !slices.Equal(got, want) {
		t.Errorf("ListStatuses = %v, want %v", got, want)
	}
}
```

Add `"slices"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./tracker/github/... -run TestListStatuses -v`
Expected: FAIL with `"github: ListStatuses not implemented"`.

- [ ] **Step 3: Implement `ListStatuses`**

In `/workspace/tracker/github/github.go`, replace the stub:

```go
// ListStatuses returns the static set of GitHub issue states (open, closed).
func (a *githubAdapter) ListStatuses(_ context.Context) ([]string, error) {
	return []string{statusOpen, statusClosed}, nil
}
```

- [ ] **Step 4: Run the new test to verify it passes**

Run: `mise exec -- go test ./tracker/github/... -run TestListStatuses -v`
Expected: PASS.

- [ ] **Step 5: Commit (SKIP — user handles git)**

---

## Task 7: GitHub adapter — `UpdateIssueStatus`

**Files:**
- Modify: `/workspace/tracker/github/github.go`
- Modify: `/workspace/tracker/github/github_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `/workspace/tracker/github/github_test.go`:

```go
func TestUpdateIssueStatus_close(t *testing.T) {
	t.Parallel()

	var gotBody struct {
		State string `json:"state"`
	}
	hits := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/a/b/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		hits++
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"number":42,"state":"closed"}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, []string{"a/b"})

	if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err != nil {
		t.Fatalf("UpdateIssueStatus: %v", err)
	}
	if hits != 1 {
		t.Errorf("server hits = %d, want 1", hits)
	}
	if gotBody.State != "closed" {
		t.Errorf(`state = %q, want "closed"`, gotBody.State)
	}
}

func TestUpdateIssueStatus_open(t *testing.T) {
	t.Parallel()

	var gotBody struct {
		State string `json:"state"`
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/a/b/issues/42", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"number":42,"state":"open"}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, []string{"a/b"})

	if err := a.UpdateIssueStatus(t.Context(), "42", "open"); err != nil {
		t.Fatalf("UpdateIssueStatus: %v", err)
	}
	if gotBody.State != "open" {
		t.Errorf(`state = %q, want "open"`, gotBody.State)
	}
}

func TestUpdateIssueStatus_unknownStatus(t *testing.T) {
	t.Parallel()

	hits := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(_ http.ResponseWriter, _ *http.Request) { hits++ })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, []string{"a/b"})

	err := a.UpdateIssueStatus(t.Context(), "42", "WIP")
	if err == nil {
		t.Fatal("expected error for unknown status, got nil")
	}
	if hits != 0 {
		t.Errorf("server hits = %d, want 0 (no HTTP call should be made)", hits)
	}
}

func TestUpdateIssueStatus_zeroProjects(t *testing.T) {
	t.Parallel()

	a, err := New(config.IssueTrackerConfig{Token: "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
		t.Error("expected error when Projects is empty, got nil")
	}
}

func TestUpdateIssueStatus_multipleProjects(t *testing.T) {
	t.Parallel()

	a, err := New(config.IssueTrackerConfig{Token: "x", Projects: []string{"a/b", "c/d"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
		t.Error("expected error when Projects has 2+ entries, got nil")
	}
}

func TestUpdateIssueStatus_invalidProject(t *testing.T) {
	t.Parallel()

	a, err := New(config.IssueTrackerConfig{Token: "x", Projects: []string{"onlyone"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
		t.Error(`expected error when Projects[0] is not "owner/repo", got nil`)
	}
}

func TestUpdateIssueStatus_invalidIssueID(t *testing.T) {
	t.Parallel()

	hits := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(_ http.ResponseWriter, _ *http.Request) { hits++ })

	srv := httptest.NewServer(mux)
	defer srv.Close()

	a := newTestAdapter(t, srv, []string{"a/b"})

	err := a.UpdateIssueStatus(t.Context(), "not-a-number", "closed")
	if err == nil {
		t.Fatal("expected error for non-integer issue id, got nil")
	}
	if hits != 0 {
		t.Errorf("server hits = %d, want 0", hits)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/... -run TestUpdateIssueStatus -v`
Expected: FAIL with `"github: UpdateIssueStatus not implemented"` (or wrong error messages).

- [ ] **Step 3: Implement `UpdateIssueStatus`**

In `/workspace/tracker/github/github.go`:

1. Add to imports: `"strings"`.

2. Replace the `UpdateIssueStatus` stub and add `mapState`:

```go
// UpdateIssueStatus toggles the issue's state to "open" or "closed" via
// PATCH /repos/{owner}/{repo}/issues/{number}. The owner/repo is taken from
// cfg.Projects, which must contain exactly one "owner/repo" entry.
func (a *githubAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusName string) error {
	if len(a.cfg.Projects) != 1 {
		return errors.New("github: UpdateIssueStatus requires exactly one project configured")
	}

	owner, repo, ok := strings.Cut(a.cfg.Projects[0], "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("github: invalid project %q (expected owner/repo)", a.cfg.Projects[0])
	}

	state, err := mapState(statusName)
	if err != nil {
		return err
	}

	n, err := strconv.Atoi(issueID)
	if err != nil {
		return fmt.Errorf("github: invalid issue id %q: %w", issueID, err)
	}

	_, _, err = a.client.Issues.Edit(ctx, owner, repo, n, &gogithub.IssueRequest{State: gogithub.Ptr(state)})
	if err != nil {
		return fmt.Errorf("github: edit issue %d: %w", n, err)
	}

	return nil
}

func mapState(name string) (string, error) {
	switch name {
	case statusOpen, statusClosed:
		return name, nil
	default:
		return "", fmt.Errorf("github: unknown status %q (want %q or %q)", name, statusOpen, statusClosed)
	}
}
```

3. Remove the `var _ = json.Marshal` guard from `github_test.go` if you added it in Task 5 — `encoding/json` is now used by these tests.

**Note on `gogithub.Ptr`:** the helper has been called `github.String(...)` in older versions and `github.Ptr[T any](T) *T` in v74+. If `gogithub.Ptr` is not exported by the version you fetched, substitute `gogithub.String(state)`.

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/... -run TestUpdateIssueStatus -v`
Expected: all 7 PASS.

- [ ] **Step 5: Run the full github + tracker suites**

Run: `mise exec -- go test ./tracker/...`
Expected: all PASS.

- [ ] **Step 6: Commit (SKIP — user handles git)**

---

## Task 8: Register the github adapter in the cmd layer

**Files:**
- Modify: `/workspace/cmd/issue/issue.go`

The github adapter's `init()` only fires when its package is imported. The redmine adapter is wired the same way at `cmd/issue/issue.go:8`.

- [ ] **Step 1: Verify the existing redmine import**

Run: `grep -n 'tracker/redmine\|tracker/github' /workspace/cmd/issue/issue.go`
Expected: one line — the redmine blank import. github is not yet imported.

- [ ] **Step 2: Add the github blank import**

In `/workspace/cmd/issue/issue.go`, find the import block containing the line:

```go
_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
```

Add immediately below it:

```go
_ "github.com/piprim/git-zf/tracker/github"  // registers github adapter
```

Keep the alignment consistent with the rest of the import block. If `goimports`/`gofmt` rewrites the order, accept it.

- [ ] **Step 3: Verify the registration takes effect**

Write a quick sanity test by running:

```bash
mise exec -- go build ./...
mise exec -- go run . issue list --help 2>&1 | head -5  # smoke check
```

Expected: build clean. The `--help` output should still be sensible.

- [ ] **Step 4: Run the full suite**

Run: `mise exec -- go test ./...`
Expected: all PASS. The github init now runs as part of any test that imports `cmd/issue`.

- [ ] **Step 5: Commit (SKIP — user handles git)**

---

## Task 9: `store.IssueRow.Project` + plumb through `buildFromTracker`

**Files:**
- Modify: `/workspace/store/store.go`
- Modify: `/workspace/cmd/issue/list.go`
- Modify: `/workspace/cmd/issue/issue_test.go`

- [ ] **Step 1: Write the failing test**

Append to `/workspace/cmd/issue/issue_test.go`:

```go
func TestBuildFromTracker_populatesProject(t *testing.T) {
	t.Parallel()

	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "1", Subject: "a", Status: "open", Project: "octo/cat"},
		{ID: "2", Subject: "b", Status: "open", Project: "octo/dog"},
	}}

	db := newTestStore(t)
	defer db.Close()

	infra := issueListInfra{tracker: tk, store: db, stderr: io.Discard}

	rows, err := buildFromTracker(t.Context(), infra)
	if err != nil {
		t.Fatalf("buildFromTracker: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Project != "octo/cat" || rows[1].Project != "octo/dog" {
		t.Errorf("projects = %q,%q want octo/cat,octo/dog", rows[0].Project, rows[1].Project)
	}
}
```

The existing test file (`/workspace/cmd/issue/issue_test.go`) already defines `fakeIssueTracker` and a `newTestStore(t)` helper — confirm by reading the file's first 60 lines before writing this test. If `newTestStore` is named differently (e.g. `setupStore`), adjust the call.

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run TestBuildFromTracker_populatesProject -v`
Expected: build failure — `rows[0].Project undefined`.

- [ ] **Step 3: Add the field to `IssueRow`**

In `/workspace/store/store.go`, replace the `IssueRow` struct (currently at lines ~76-81):

```go
// IssueRow is the unified display row for git zf issue list.
// It composes an issue identity with an optional local branch.
type IssueRow struct {
	IssueSlug     string     `json:"issue_slug"`
	Title         string     `json:"title"`
	Project       string     `json:"project"`        // tracker project / repo; empty when unknown
	TrackerStatus *string    `json:"tracker_status"` // nil → display "N.A."
	Branch        *BranchRow `json:"branch"`         // nil → not started locally
}
```

- [ ] **Step 4: Plumb `tracker.Issue.Project` into the row**

In `/workspace/cmd/issue/list.go`, find `buildFromTracker` (around line 142) and update the row construction:

```go
rows := make([]store.IssueRow, len(issues))
for i, iss := range issues {
	status := iss.Status
	row := store.IssueRow{
		IssueSlug:     iss.ID,
		Title:         iss.Subject,
		Project:       iss.Project,
		TrackerStatus: &status,
	}
	if b, ok := branchMap[iss.ID]; ok {
		row.Branch = &b
	}
	rows[i] = row
}
```

- [ ] **Step 5: Run the new test + full suite**

Run: `mise exec -- go test ./cmd/issue/... -run TestBuildFromTracker_populatesProject -v && mise exec -- go test ./...`
Expected: target test PASS; nothing else regresses. The `buildFromStore` path leaves `Project` empty (no project information lives in the local store), which is the documented contract.

- [ ] **Step 6: Commit (SKIP — user handles git)**

---

## Task 10: TUI project filter (bubbletea integration)

**Files:**
- Modify: `/workspace/tui/issue.go`
- Modify: `/workspace/tui/issue_test.go`

This is the largest task. It extends the existing `issueTableModel` with project filtering parallel to the status filter.

- [ ] **Step 1: Write the failing tests**

Append to `/workspace/tui/issue_test.go`:

```go
func TestApplyFilters_ByProject(t *testing.T) {
	t.Parallel()

	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "a", Project: "octo/cat"},
		{IssueSlug: "B", Title: "b", Project: "octo/dog"},
		{IssueSlug: "C", Title: "c", Project: "octo/cat"},
	}

	got := applyFilters(rows, statusOpen, "", "octo/cat")
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0][0] != "A" || got[1][0] != "C" {
		t.Errorf("unexpected rows: %v", got)
	}
}

func TestApplyFilters_AllProjects(t *testing.T) {
	t.Parallel()

	rows := []store.IssueRow{
		{IssueSlug: "A", Project: "octo/cat"},
		{IssueSlug: "B", Project: "octo/dog"},
	}

	got := applyFilters(rows, statusOpen, "", projectAll)
	if len(got) != 2 {
		t.Fatalf(`got %d rows with projectAll, want 2`, len(got))
	}
}

func TestUniqueProjects(t *testing.T) {
	t.Parallel()

	rows := []store.IssueRow{
		{Project: "z/y"},
		{Project: "a/b"},
		{Project: ""},
		{Project: "a/b"},
		{Project: "z/y"},
	}

	got := uniqueProjects(rows)
	want := []string{"a/b", "z/y"} // sorted, no empty, deduplicated
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		current  string
		projects []string
		want     string
	}{
		{projectAll, []string{"a/b", "c/d"}, "a/b"},
		{"a/b", []string{"a/b", "c/d"}, "c/d"},
		{"c/d", []string{"a/b", "c/d"}, projectAll},
		{"unknown", []string{"a/b"}, projectAll},
		{projectAll, []string{}, projectAll}, // no projects → stay on all
	}

	for _, tt := range tests {
		t.Run(tt.current+"_in_"+strings.Join(tt.projects, ","), func(t *testing.T) {
			got := nextProject(tt.current, tt.projects)
			if got != tt.want {
				t.Errorf("nextProject(%q, %v) = %q, want %q", tt.current, tt.projects, got, tt.want)
			}
		})
	}
}
```

Add `"slices"` and `"strings"` to the test file's import block if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tui/... -run "TestApplyFilters_ByProject|TestApplyFilters_AllProjects|TestUniqueProjects|TestNextProject" -v`
Expected: FAIL — `applyFilters` is currently a 3-arg function, and `projectAll`, `uniqueProjects`, `nextProject` don't exist.

- [ ] **Step 3: Add the project constants and helpers**

In `/workspace/tui/issue.go`, add to the `const ( ... )` block at the top of the file:

```go
const (
	// existing constants...
	projectAll = "all"
)
```

After the existing `nextStatus` function (around line 278), add:

```go
// uniqueProjects returns the deduplicated, sorted list of non-empty
// IssueRow.Project values.
func uniqueProjects(rows []store.IssueRow) []string {
	seen := make(map[string]struct{})
	for _, r := range rows {
		if r.Project == "" {
			continue
		}
		seen[r.Project] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

// nextProject cycles through projects in the order:
//   all → projects[0] → projects[1] → ... → all → ...
// If projects is empty, always returns projectAll. If current is not in
// projects (and not projectAll), the cycle restarts at projectAll.
func nextProject(current string, projects []string) string {
	if len(projects) == 0 {
		return projectAll
	}

	if current == projectAll {
		return projects[0]
	}

	for i, p := range projects {
		if p == current {
			if i+1 < len(projects) {
				return projects[i+1]
			}

			return projectAll
		}
	}

	return projectAll
}
```

Add `"slices"` to the imports in `/workspace/tui/issue.go` if not already present.

- [ ] **Step 4: Update `issueRowToTableRow` to take `includeProject`**

Replace the existing `issueRowToTableRow` (currently at lines ~224-233):

```go
func issueRowToTableRow(r store.IssueRow, includeProject bool) btable.Row {
	row := make(btable.Row, 0, 7)
	row = append(row, r.IssueSlug)

	if includeProject {
		row = append(row, r.Project)
	}

	return append(row,
		r.Title,
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
		pkg.TrackerStatusOrNA(r.TrackerStatus),
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
	)
}
```

- [ ] **Step 5: Replace `applyFilters` with the final 5-arg shape**

Replace the existing `applyFilters` function (currently at lines ~246-276):

```go
// applyFilters builds the bubbletea table rows, keeping only those matching
// status, project, and free-text search. includeProject controls whether the
// Project cell is emitted (must match the table's column count).
//
//	status:         one of statusOpen, statusClosed, statusAll
//	text:           free-text query (case-insensitive substring match)
//	project:        a Project value, or projectAll to disable the filter
//	includeProject: whether the table has a Project column visible
func applyFilters(rows []store.IssueRow, status, text, project string, includeProject bool) []btable.Row {
	q := strings.ToLower(text)
	out := make([]btable.Row, 0, len(rows))

	for _, r := range rows {
		if !matchesStatus(r, status) {
			continue
		}
		if project != projectAll && r.Project != project {
			continue
		}

		row := issueRowToTableRow(r, includeProject)

		if q != "" {
			matched := false
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), q) {
					matched = true

					break
				}
			}

			if !matched {
				continue
			}
		}

		out = append(out, row)
	}

	return out
}
```

- [ ] **Step 6: Add the column builder + width constant**

Add the constant to the `const ( ... )` block at the top of the file:

```go
const issueTableColWidthProject = 18
```

Add `buildIssueTableColumns` near `issueRowToTableRow`:

```go
// buildIssueTableColumns returns the bubbletea columns. The Project column
// appears only when rows span more than one project.
func buildIssueTableColumns(rows []store.IssueRow) []btable.Column {
	cols := []btable.Column{
		{Title: "Issue ID", Width: issueTableColWidthIssueID},
	}

	if len(uniqueProjects(rows)) > 1 {
		cols = append(cols, btable.Column{Title: "Project", Width: issueTableColWidthProject})
	}

	return append(cols,
		btable.Column{Title: "Title", Width: issueTableColWidthTitle},
		btable.Column{Title: "Branch", Width: issueTableColWidthBranch},
		btable.Column{Title: "Local Status", Width: issueTableColWidthLocalStatus},
		btable.Column{Title: "Tracker Status", Width: issueTableColWidthTrackerStatus},
		btable.Column{Title: "Created", Width: issueTableColWidthCreated},
	)
}
```

- [ ] **Step 7: Extend `issueTableModel` with project state**

Replace the `issueTableModel` struct (currently at lines ~289-295):

```go
type issueTableModel struct {
	table         btable.Model
	allRows       []store.IssueRow
	filter        textinput.Model
	filtering     bool
	statusFilter  string
	projects      []string // projects derived from allRows; static for the model's lifetime
	projectFilter string
}
```

Replace the `IssueTableModel` constructor at line ~191:

```go
func IssueTableModel(rows []store.IssueRow, initialStatus string) (tea.Model, error) {
	includeProj := len(uniqueProjects(rows)) > 1
	cols := buildIssueTableColumns(rows)

	status := initialStatus
	if status == "" {
		status = statusOpen
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(applyFilters(rows, status, "", projectAll, includeProj)),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64

	return &issueTableModel{
		table:         t,
		allRows:       rows,
		filter:        fi,
		statusFilter:  status,
		projects:      uniqueProjects(rows),
		projectFilter: projectAll,
	}, nil
}
```

- [ ] **Step 8: Update every `applyFilters` call site in `Update` and the existing tests**

There are three call sites inside the `Update` method's body. Replace each with the 5-arg call. Also update the existing 3-arg test calls to the 5-arg form.

In `/workspace/tui/issue.go`'s `Update` method, find and replace:

1. The escape-from-filtering branch (around line 307):
   ```go
   m.table.SetRows(applyFilters(m.allRows, m.statusFilter, "", m.projectFilter, len(m.projects) > 1))
   ```
2. The text-filter branch (around line 319):
   ```go
   m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, len(m.projects) > 1))
   ```
3. The tab-cycle branch (around line 329):
   ```go
   m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, len(m.projects) > 1))
   ```

In `/workspace/tui/issue_test.go`, update every existing call to `applyFilters` (in `TestApplyFilters_Open`, `_Closed`, `_All`, and any text-filter variants) to pass the 4th `projectAll` and 5th `false` arguments. Example:

```go
// before:
got := applyFilters(rows, "open", "")
// after:
got := applyFilters(rows, "open", "", projectAll, false)
```

Existing fixtures don't set `Project`, so `false` (no project column) is the right value.

- [ ] **Step 9: Bind `p` key to cycle the project filter**

In `/workspace/tui/issue.go`, find the `Update` method's outer `switch key.String()` (around line 324). Add a new case alongside the existing `tab`:

```go
case "p":
	m.projectFilter = nextProject(m.projectFilter, m.projects)
	includeProj := len(m.projects) > 1
	m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))

	return m, nil
```

Adjust the existing `tab` case in the same way to thread `includeProj`:

```go
case "tab":
	m.statusFilter = nextStatus(m.statusFilter)
	includeProj := len(m.projects) > 1
	m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))

	return m, nil
```

(Apply the same adjustment to the `esc` and text-filter call sites inside the `m.filtering` block.)

- [ ] **Step 10: Render a project tab strip**

Add a render helper near `renderStatusTabs`:

```go
func renderProjectTabs(current string, projects []string) string {
	if len(projects) == 0 {
		return ""
	}

	type tab struct {
		label string
		value string
	}

	tabs := make([]tab, 0, len(projects)+1)
	tabs = append(tabs, tab{"All", projectAll})
	for _, p := range projects {
		tabs = append(tabs, tab{p, p})
	}

	parts := make([]string, len(tabs))
	for i, t := range tabs {
		if t.value == current {
			parts[i] = activeTabStyle.Render("[ " + t.label + " ]")
		} else {
			parts[i] = inactiveTabStyle.Render(t.label)
		}
	}

	return strings.Join(parts, "  ")
}
```

Update the `View` method (around line 369):

```go
func (m *issueTableModel) View() string {
	tabs := renderStatusTabs(m.statusFilter)
	projTabs := renderProjectTabs(m.projectFilter, m.projects)

	hint := "Press / to filter · tab: status · q to quit"
	if len(m.projects) > 1 {
		hint = "Press / to filter · tab: status · p: project · q to quit"
	}

	view := m.table.View() + "\n\n" + tabs
	if projTabs != "" {
		view += "    " + projTabs
	}

	if m.filtering {
		return view + "    /" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return view + "    " + hint
}
```

- [ ] **Step 11: Run the new tests + full suite**

Run: `mise exec -- go test ./tui/... -run "TestApplyFilters_ByProject|TestApplyFilters_AllProjects|TestUniqueProjects|TestNextProject" -v`
Expected: all PASS.

Run: `mise exec -- go test ./...`
Expected: all PASS (existing `TestApplyFilters_Open`, `_Closed`, etc. should still pass after the signature update propagated in Step 7).

- [ ] **Step 12: Manual smoke test (optional)**

Run: `mise exec -- go build -o ./bin/git-zf . && ./bin/git-zf issue list`
Expected: opens the TUI table; press `p` to cycle through projects (only visible if multiple are loaded); press `tab` to cycle status; press `/` to text-filter.

- [ ] **Step 13: Commit (SKIP — user handles git)**

---

## Task 11: `tty.RenderIssueTable` — conditional Project column

**Files:**
- Modify: `/workspace/tty/issue.go`
- Modify: `/workspace/tty/issue_test.go` (create if missing — verify with `ls /workspace/tty/`)

- [ ] **Step 1: Verify whether `tty/issue_test.go` exists**

Run: `ls /workspace/tty/`
If `issue_test.go` is absent, you'll create it in Step 2. If it exists, append to it.

- [ ] **Step 2: Write the failing tests**

Create or append to `/workspace/tty/issue_test.go`:

```go
package tty

import (
	"bytes"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestRenderIssueTable_hidesProjectColumnWhenSingle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rows := []store.IssueRow{
		{IssueSlug: "1", Title: "a", Project: "octo/cat"},
		{IssueSlug: "2", Title: "b", Project: "octo/cat"},
	}

	RenderIssueTable(&buf, rows)
	out := buf.String()

	if strings.Contains(out, "PROJECT") {
		t.Errorf("expected no PROJECT header for single-project rows, got:\n%s", out)
	}
}

func TestRenderIssueTable_showsProjectColumnWhenMultiple(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	rows := []store.IssueRow{
		{IssueSlug: "1", Title: "a", Project: "octo/cat"},
		{IssueSlug: "2", Title: "b", Project: "octo/dog"},
	}

	RenderIssueTable(&buf, rows)
	out := buf.String()

	if !strings.Contains(out, "PROJECT") {
		t.Errorf("expected PROJECT header for multi-project rows, got:\n%s", out)
	}
	if !strings.Contains(out, "octo/cat") || !strings.Contains(out, "octo/dog") {
		t.Errorf("expected project values in output, got:\n%s", out)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./tty/... -run TestRenderIssueTable -v`
Expected: FAIL — `PROJECT` header is never emitted today.

- [ ] **Step 4: Update `RenderIssueTable` to conditionally emit the column**

Replace the body of `/workspace/tty/issue.go`:

```go
package tty

import (
	"fmt"
	"io"

	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

func RenderIssueTable(w io.Writer, rows []store.IssueRow) {
	includeProject := hasMultipleProjects(rows)

	headers := []string{"ISSUE ID"}
	if includeProject {
		headers = append(headers, "PROJECT")
	}
	headers = append(headers, "TITLE", "BRANCH", "LOCAL STATUS", "TRACKER STATUS", "CREATED")

	t := lgtable.New().
		Headers(headers...).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		cells := []string{r.IssueSlug}
		if includeProject {
			cells = append(cells, r.Project)
		}

		cells = append(cells,
			r.Title,
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			pkg.TrackerStatusOrNA(r.TrackerStatus),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		)

		t.Row(cells...)
	}

	fmt.Fprintln(w, t.Render())
}

func hasMultipleProjects(rows []store.IssueRow) bool {
	first := ""
	seen := false

	for _, r := range rows {
		if r.Project == "" {
			continue
		}
		if !seen {
			first = r.Project
			seen = true

			continue
		}
		if r.Project != first {
			return true
		}
	}

	return false
}
```

- [ ] **Step 5: Run the new tests + full suite**

Run: `mise exec -- go test ./tty/... -run TestRenderIssueTable -v && mise exec -- go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit (SKIP — user handles git)**

---

## Final verification

- [ ] **Step 1: Build the binary**

Run: `mise exec -- go build -o ./bin/git-zf .`
Expected: clean.

- [ ] **Step 2: Vet**

Run: `mise exec -- go vet ./...`
Expected: no warnings.

- [ ] **Step 3: golangci-lint (mandatory before declaring done)**

Run: `mise exec -- golangci-lint run ./...`
Expected: zero findings. If `golangci-lint` is not installed locally, note the gap and ask the user to run it before merging.

- [ ] **Step 4: Full test pass with race detector**

Run: `mise exec -- go test -race ./...`
Expected: all packages PASS, no race detector warnings.

- [ ] **Step 5: Smoke test the new adapter (manual, requires a real PAT)**

```bash
# Create a temporary config:
cat > /tmp/git-zf-test.json <<EOF
{
  "issue-tracker": {
    "type": "github",
    "url": "https://api.github.com",
    "token": "ghp_yourPATgoesHere",
    "projects": ["octocat/Hello-World"]
  }
}
EOF

# In a sandbox repo:
cp /tmp/git-zf-test.json .git-zf.json
./bin/git-zf issue list
```

Expected: lists open issues assigned to the authenticated user from `octocat/Hello-World`. Press `p` to cycle through projects (degenerate when only one project is loaded). Press `tab` for status, `/` for text search.

---

## Notes for the executing agent

- **Mandatory Go skills (project-local, in `.claude/skills/learned/`):** before writing or editing any Go in this plan, invoke every skill in `golang-mandatory.md` — at minimum `focused-functions`, `golang-linter-rules`, `golang-modern-go`, `golang-naming`, `golang-revive-rules`, `golang-data-structures`, `golang-testing`, `golang-security`. Their rules are baked into the code blocks; preserve them when transcribing.
- **Linter discipline (from `golang-linter-rules`):**
  - `nlreturn` — blank line before every `return` not the sole statement in its `{}` block. The plan's code respects this; preserve formatting when copying.
  - `wrapcheck` — every error from an external package wrapped via `fmt.Errorf("ctx: %w", err)`. The plan does this for `c.WithEnterpriseURLs`, `Issues.ListByAuthenticatedUser`, `Issues.Edit`, and `strconv.Atoi`.
  - `error-strings` — lowercase first letter, no trailing punctuation, no newlines.
  - `add-constant` — `statusOpen`, `statusClosed`, `projectAll`, `trackerType` extracted as constants. The repeated test fixture string `"a/b"` appears 4+ times across `tracker/github/github_test.go`; keep an eye on this — extract `const testRepo = "a/b"` if the linter complains.
- **Modern Go (from `golang-modern-go`):** plan uses `slices.Sort`, `slices.Equal`, `strings.Cut`, `t.Context()`. `exec.CommandContext` (n/a here — no subprocess work in this plan).
- **Revive thresholds (from `golang-revive-rules`):** `argument-limit ≤ 5`, `function-length ≤ 50/150`, `file-length-limit ≤ 500`, `line-length-limit ≤ 124`, `max-control-nesting ≤ 3`. The largest function written by this plan is `applyFilters` with 5 args (right at the limit) and ~30 lines — under all thresholds.
- **Toolchain:** always `mise exec -- go ...`. Plain `go ...` may pick up the wrong version.
- **No git operations:** the user handles all `git add`/`git commit`/`git push`. Skip all commit steps in this plan; report changes per-task and let the user commit.
- **`go-github` version compatibility:** this plan targets v85, but if Task 4's `go get` fetches a different major (because v85 isn't published yet at execution time), update **all** `v85` references throughout the plan and double-check method names: `Issues.ListByAuthenticatedUser` (introduced ~v50) and `gogithub.Ptr` (introduced in v74). Older versions use `Issues.List` and `gogithub.String` respectively.
