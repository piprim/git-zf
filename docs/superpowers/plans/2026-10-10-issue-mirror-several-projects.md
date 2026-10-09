# Issue Mirror Over Several Projects Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One repository mirrors several projects of one tracker: project-aware tracker calls, project-qualified issue slugs, and a project picker in `issue new`.

**Architecture:** The six per-project methods of `tracker.Tracker` take a near-slug `project` argument that the adapters resolve through the config they already hold. A record gains a `Project`, the branch chain gains a `project`, and a display ID is qualified (`other-42`) when the repository's linked records span more than one project, a flag computed from the records so every clone agrees. One helper, `issueflow.TrackerRef`, turns a branch into a project and a number for the close, review and prune tracker calls.

**Tech Stack:** Go (via `mise exec -- go`), cobra, huh, the chain plumbing in `git/`, the fake tracker in `tracker/fake`, `httptest` for the adapters.

**Spec:** `docs/superpowers/specs/2026-10-09-issue-mirror-several-projects-design.md`

## Global Constraints

- Every Go command runs as `mise exec -- go …`.
- Every test assertion sits in its own `t.Run("descriptive label", …)`, including single-assertion tests.
- `importRootTemplate` in `issue/mirror.go` is frozen. Never edit it. `TestChainRef_FixedRoot` in `git/chain_ref_test.go` must keep passing on its golden hash.
- Unknown op types and unknown op fields keep being skipped by every fold: an older binary reads refs written by a newer one.
- A near slug matches `^[a-z0-9][a-z0-9-]*$` and is lowercased at load (unchanged). It may contain dashes: `zf-2` is valid.
- The first `[[issue-tracker.projects]]` entry is the default project.
- A qualified display ID is `<near-slug>-<tracker-number>`, for example `other-42`.
- Per `CLAUDE.md`: run GitNexus `impact` on a symbol before editing it, and `detect_changes()` before each commit. Re-index with `node .gitnexus/run.cjs analyze` when the index is stale.
- Commit messages follow the repository's style: `feat(scope): …`, `fix(scope): …`, `docs: …`, `test(scope): …`, lower case, no period.

## Review Focus

1. **A near slug with a dash, such as `zf-2`.** `zf-2-42` must display, resolve and strip back to project `zf-2`, number `42`. Pinned in Task 5 (`DisplayID`, `FindByDisplayID`) and Task 8 (`TrackerRef`).
2. **A bare number that two projects share, typed by a user.** `issue show 42` with `zf-42` and `other-42` must fail with both qualified IDs listed, never pick one silently. Pinned in Task 5.
3. **A record whose project is not configured on this clone.** The reconcile must not export it to the default project; it warns and skips. Pinned in Task 6.
4. **`issue new --project` with the mirror off, or with an unknown slug.** Off: stored, nothing exported, no error. Unknown: an error naming the configured slugs. Pinned in Task 11.
5. **A tracker call with no project resolvable while two are configured.** The close must still complete, with one warning and no tracker write. Pinned in Task 10.

---

### Task 1: Config: default project, lookup, at least one project

**Files:**
- Modify: `config/config.go:95-140`
- Test: `config/config_test.go:364-495`

**Interfaces:**
- Produces:
  - `func (c *IssueTrackerConfig) DefaultProject() string` — near slug of the first entry, `""` with none.
  - `func (c *IssueTrackerConfig) Project(near string) (TrackerProject, bool)` — the entry with this near slug.
  - `func (c *IssueTrackerConfig) ProjectOrOnly(near string) (TrackerProject, error)` — the entry named by `near`, or the only configured one when `near == ""`; an error with zero entries, with several and `near == ""`, or with an unknown `near`.
  - `func (c *IssueTrackerConfig) NearSlugOf(far string) (string, bool)` — the near slug of the entry whose far slug is `far`.
  - `mirror = true` now needs **at least one** project.

- [ ] **Step 1: Write the failing tests**

In `config/config_test.go`, inside `TestLoadTrackerProjects`, delete the `"mirror with two projects"` entry from the `rejected` map, then add these subtests before the closing brace of the function:

```go
	t.Run("mirror with two projects loads, the first being the default", func(t *testing.T) {
		t.Parallel()

		cfg, err := load(t, `
[issue-tracker]
type = "forgejo"
mirror = true
[[issue-tracker.projects]]
near-slug = "zf"
far-slug = "o/a"
[[issue-tracker.projects]]
near-slug = "other"
far-slug = "o/b"
`)
		if err != nil || !cfg.IssueTracker.Mirror {
			t.Fatalf("Mirror = %v, err = %v", cfg != nil && cfg.IssueTracker.Mirror, err)
		}
		if got := cfg.IssueTracker.DefaultProject(); got != "zf" {
			t.Errorf("DefaultProject = %q, want zf", got)
		}
	})

	t.Run("DefaultProject is empty with no project", func(t *testing.T) {
		t.Parallel()

		var c config.IssueTrackerConfig
		if got := c.DefaultProject(); got != "" {
			t.Errorf("DefaultProject = %q, want empty", got)
		}
	})

	two := config.IssueTrackerConfig{Projects: []config.TrackerProject{
		{NearSlug: "zf", FarSlug: "o/a"}, {NearSlug: "other", FarSlug: "o/b"},
	}}
	one := config.IssueTrackerConfig{Projects: two.Projects[:1]}

	t.Run("Project finds an entry by near slug", func(t *testing.T) {
		t.Parallel()

		p, ok := two.Project("other")
		if !ok || p.FarSlug != "o/b" {
			t.Errorf("Project(other) = %+v, %v", p, ok)
		}
		if _, ok := two.Project("nope"); ok {
			t.Error("Project(nope) found something")
		}
	})

	t.Run("NearSlugOf maps a far slug back", func(t *testing.T) {
		t.Parallel()

		near, ok := two.NearSlugOf("o/b")
		if !ok || near != "other" {
			t.Errorf("NearSlugOf(o/b) = %q, %v", near, ok)
		}
		if _, ok := two.NearSlugOf("o/c"); ok {
			t.Error("NearSlugOf(o/c) found something")
		}
	})

	for name, tc := range map[string]struct {
		cfg     config.IssueTrackerConfig
		near    string
		wantFar string
		wantErr string
	}{
		"empty near with one project gives it":            {one, "", "o/a", ""},
		"a named project is returned":                     {two, "other", "o/b", ""},
		"empty near with two projects is refused":         {two, "", "", "none named"},
		"an unknown near slug is refused":                 {two, "nope", "", `"nope" is not configured`},
		"empty near with no project is refused":           {config.IssueTrackerConfig{}, "", "", "no project configured"},
	} {
		t.Run("ProjectOrOnly: "+name, func(t *testing.T) {
			t.Parallel()

			p, err := tc.cfg.ProjectOrOnly(tc.near)
			if tc.wantErr == "" {
				if err != nil || p.FarSlug != tc.wantFar {
					t.Errorf("ProjectOrOnly = %+v, %v", p, err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./config/... -run TestLoadTrackerProjects -v`
Expected: FAIL, compile error `c.DefaultProject undefined` (and friends).

- [ ] **Step 3: Implement**

In `config/config.go`, after `FarSlugs`, add:

```go
// DefaultProject is the near slug of the first configured project: the one a
// repo-born issue is exported to when none was picked. "" with no project.
func (c *IssueTrackerConfig) DefaultProject() string {
	if len(c.Projects) == 0 {
		return ""
	}

	return c.Projects[0].NearSlug
}

// Project returns the configured project with this near slug.
func (c *IssueTrackerConfig) Project(near string) (TrackerProject, bool) {
	for _, p := range c.Projects {
		if p.NearSlug == near {
			return p, true
		}
	}

	return TrackerProject{}, false
}

// NearSlugOf returns the near slug of the configured project the tracker
// calls far.
func (c *IssueTrackerConfig) NearSlugOf(far string) (string, bool) {
	for _, p := range c.Projects {
		if p.FarSlug == far {
			return p.NearSlug, true
		}
	}

	return "", false
}

// ProjectOrOnly returns the configured project named by near, or the only
// configured one when near is empty: what a per-project tracker call resolves
// its project with. With several projects, a call must name one.
func (c *IssueTrackerConfig) ProjectOrOnly(near string) (TrackerProject, error) {
	if near != "" {
		p, ok := c.Project(near)
		if !ok {
			return TrackerProject{}, fmt.Errorf("issue-tracker: project %q is not configured", near)
		}

		return p, nil
	}

	switch len(c.Projects) {
	case 0:
		return TrackerProject{}, errors.New("issue-tracker: no project configured ([[issue-tracker.projects]])")
	case 1:
		return c.Projects[0], nil
	default:
		return TrackerProject{}, fmt.Errorf(
			"issue-tracker: %d projects configured and none named: the branch or issue predates projects", len(c.Projects))
	}
}
```

In `normalize`, replace the mirror check:

```go
	if c.Mirror && (c.Type == "" || len(c.Projects) == 0) {
		return errors.New(
			"issue-tracker: mirror = true needs a tracker type and at least one [[issue-tracker.projects]] entry")
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./config/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): the mirror accepts several projects, the first is the default"
```

---

### Task 2: Tracker interface: a project parameter on the per-project calls

**Files:**
- Modify: `tracker/tracker.go:18-68`
- Modify: `tracker/forgejo/forgejo.go`, `tracker/github/github.go`, `tracker/redmine/redmine.go`
- Modify: `tracker/fake/fake.go` (signatures only; behaviour in Task 3)
- Modify: `issue/mirror.go:68,274,308,325`, `cmd/issueflow/tracker.go:42`, `cmd/review/tracker.go:37`, `cmd/branch/prune_tracker.go:62-67,108`
- Test: `tracker/forgejo/forgejo_test.go`, `tracker/github/github_test.go`, `tracker/redmine/mirror_test.go`, `tracker/redmine/redmine_test.go`, `tracker/fake/fake_test.go`, `cmd/branch/prune_tracker_test.go`, `issue/mirror_test.go`, `cmd/issue/close_mirror_e2e_test.go`

**Interfaces:**
- Consumes: `config.IssueTrackerConfig.ProjectOrOnly`, `NearSlugOf` (Task 1).
- Produces, on `tracker.Tracker`:
  ```go
  ListProjectIssues(ctx context.Context, project string) ([]Issue, error)
  CreateIssue(ctx context.Context, project, title, description string) (Issue, error)
  UpdateIssueStatus(ctx context.Context, project, issueID, statusName string) error
  IsIssueClosed(ctx context.Context, project, issueID string) (bool, error)
  AddComment(ctx context.Context, project, issueID, body string) error
  SetIssueOpen(ctx context.Context, project, issueID string, open bool) error
  ```
  `project` is a near slug; `""` means the only configured project, and is an error when several are configured. `Issue.Project` is the near slug whenever the far slug is a configured project.

- [ ] **Step 1: Write the failing adapter tests**

Add to `tracker/forgejo/forgejo_test.go`:

```go
// newTwoProjectAdapter serves handler for projects p0 = a/b and p1 = c/d.
func newTwoProjectAdapter(t *testing.T, handler http.HandlerFunc) *forgejoAdapter {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, ok := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: far("a/b", "c/d")}).(*forgejoAdapter)
	if !ok {
		t.Fatal("New did not return a *forgejoAdapter")
	}

	return a
}

func TestProjectCalls_TwoProjects(t *testing.T) {
	t.Parallel()

	var paths []string
	a := newTwoProjectAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues"):
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `[{"number": 7, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number": 57, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}`)
		default:
			fmt.Fprint(w, `{"number": 7, "state": "closed"}`)
		}
	})
	ctx := t.Context()

	t.Run("the listing names the second project and reports the near slug", func(t *testing.T) {
		got, err := a.ListProjectIssues(ctx, "p1")
		if err != nil || len(got) != 1 || got[0].Project != "p1" {
			t.Fatalf("ListProjectIssues = %+v, %v", got, err)
		}
		if paths[len(paths)-2] != "GET /api/v1/repos/c/d/issues" {
			t.Errorf("paths = %v", paths)
		}
	})
	t.Run("the creation goes to the named project", func(t *testing.T) {
		got, err := a.CreateIssue(ctx, "p1", "Bug", "")
		if err != nil || got.Project != "p1" || paths[len(paths)-1] != "POST /api/v1/repos/c/d/issues" {
			t.Errorf("CreateIssue = %+v, %v, last path %s", got, err, paths[len(paths)-1])
		}
	})
	t.Run("the per-issue calls name the project's repository", func(t *testing.T) {
		if _, err := a.IsIssueClosed(ctx, "p0", "7"); err != nil {
			t.Fatalf("IsIssueClosed: %v", err)
		}
		if paths[len(paths)-1] != "GET /api/v1/repos/a/b/issues/7" {
			t.Errorf("last path = %s", paths[len(paths)-1])
		}
	})
	t.Run("an empty project with two configured is refused before any request", func(t *testing.T) {
		n := len(paths)
		_, err := a.ListProjectIssues(ctx, "")
		if err == nil || !strings.Contains(err.Error(), "none named") || len(paths) != n {
			t.Errorf("err = %v, requests = %d", err, len(paths)-n)
		}
	})
	t.Run("an unknown project is refused", func(t *testing.T) {
		if err := a.AddComment(ctx, "nope", "7", "x"); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Errorf("err = %v", err)
		}
	})
}
```

Add to `tracker/github/github_test.go`:

```go
func TestProjectCalls_TwoProjects(t *testing.T) {
	t.Parallel()

	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues"):
			// No Link header: one page.
			fmt.Fprint(w, `[{"number": 7, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}]`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"number": 57, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}`)
		default:
			fmt.Fprint(w, `{"number": 7, "state": "closed"}`)
		}
	}))
	t.Cleanup(srv.Close)
	a := newTestAdapter(t, srv, []string{"a/b", "c/d"})
	ctx := t.Context()

	t.Run("the listing names the second project and reports the near slug", func(t *testing.T) {
		got, err := a.ListProjectIssues(ctx, "p1")
		if err != nil || len(got) != 1 || got[0].Project != "p1" || paths[len(paths)-1] != "GET /repos/c/d/issues" {
			t.Fatalf("ListProjectIssues = %+v, %v, paths = %v", got, err, paths)
		}
	})
	t.Run("the creation goes to the named project", func(t *testing.T) {
		got, err := a.CreateIssue(ctx, "p1", "Bug", "")
		if err != nil || got.Project != "p1" || paths[len(paths)-1] != "POST /repos/c/d/issues" {
			t.Errorf("CreateIssue = %+v, %v, last path %s", got, err, paths[len(paths)-1])
		}
	})
	t.Run("the per-issue calls name the project's repository", func(t *testing.T) {
		if _, err := a.IsIssueClosed(ctx, "p0", "7"); err != nil {
			t.Fatalf("IsIssueClosed: %v", err)
		}
		if paths[len(paths)-1] != "GET /repos/a/b/issues/7" {
			t.Errorf("last path = %s", paths[len(paths)-1])
		}
	})
	t.Run("an empty project with two configured is refused before any request", func(t *testing.T) {
		n := len(paths)
		_, err := a.ListProjectIssues(ctx, "")
		if err == nil || !strings.Contains(err.Error(), "none named") || len(paths) != n {
			t.Errorf("err = %v, requests = %d", err, len(paths)-n)
		}
	})
	t.Run("an unknown project is refused", func(t *testing.T) {
		if err := a.AddComment(ctx, "nope", "7", "x"); err == nil || !strings.Contains(err.Error(), "not configured") {
			t.Errorf("err = %v", err)
		}
	})
}
```

Add to `tracker/redmine/mirror_test.go`:

```go
func TestProjectCalls_TwoProjects(t *testing.T) {
	t.Parallel()

	var paths []string
	var sentProject string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost:
			var body struct {
				Issue struct {
					ProjectID string `json:"project_id"`
				} `json:"issue"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			sentProject = body.Issue.ProjectID
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"issue": {"id": 57, "subject": "Bug", "status": {"id": 1, "name": "New"}, "created_on": "2026-09-01T08:00:00Z"}}`)
		case strings.HasPrefix(r.URL.Path, "/projects/"):
			fmt.Fprint(w, `{"issues": [{"id": 9, "subject": "S", "status": {"id": 1, "name": "New"}, "created_on": "2026-09-01T08:00:00Z"}], "total_count": 1}`)
		default:
			fmt.Fprint(w, `{"issue": {"status": {"is_closed": true}}}`)
		}
	}))
	t.Cleanup(srv.Close)

	a, err := New(config.IssueTrackerConfig{URL: srv.URL, Token: "k", Projects: []config.TrackerProject{
		{NearSlug: "cpro", FarSlug: "cpro"}, {NearSlug: "other", FarSlug: "42"},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := t.Context()

	t.Run("the listing asks the named project and reports its near slug", func(t *testing.T) {
		got, err := a.ListProjectIssues(ctx, "other")
		if err != nil || len(got) != 1 || got[0].Project != "other" || paths[len(paths)-1] != "GET /projects/42/issues.json" {
			t.Errorf("got = %+v, %v, paths = %v", got, err, paths)
		}
	})
	t.Run("the creation sends the named project's far slug", func(t *testing.T) {
		if _, err := a.CreateIssue(ctx, "other", "Bug", ""); err != nil || sentProject != "42" {
			t.Errorf("err = %v, project_id = %q", err, sentProject)
		}
	})
	t.Run("a per-issue call ignores the project: numbers are global", func(t *testing.T) {
		closed, err := a.IsIssueClosed(ctx, "", "9")
		if err != nil || !closed || paths[len(paths)-1] != "GET /issues/9.json" {
			t.Errorf("closed = %v, %v, last path %s", closed, err, paths[len(paths)-1])
		}
	})
	t.Run("an empty project with two configured is refused by the listing", func(t *testing.T) {
		if _, err := a.ListProjectIssues(ctx, ""); err == nil || !strings.Contains(err.Error(), "none named") {
			t.Errorf("err = %v", err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/... 2>&1 | head -20`
Expected: FAIL, compile errors `too many arguments in call to a.ListProjectIssues`.

- [ ] **Step 3: Change the interface**

In `tracker/tracker.go`, replace the six methods:

```go
	// UpdateIssueStatus sets the status of issueID in project (a near slug;
	// "" is the only configured one, an error when several are).
	UpdateIssueStatus(ctx context.Context, project, issueID, statusName string) error
	// IsIssueClosed reports whether the tracker considers issueID closed.
	// Returns ErrIssueNotFound for missing-issue cases so callers can format
	// the warning distinctly from transport/auth failures.
	IsIssueClosed(ctx context.Context, project, issueID string) (bool, error)
	// AddComment posts body as a comment on issueID.
	AddComment(ctx context.Context, project, issueID, body string) error
	// ListProjectIssues retrieves every open issue of project, whoever it is
	// assigned to. It is the issue mirror's listing.
	ListProjectIssues(ctx context.Context, project string) ([]Issue, error)
	// CreateIssue creates an issue in project and returns it with the ID and
	// status the tracker gave it.
	CreateIssue(ctx context.Context, project, title, description string) (Issue, error)
	// SetIssueOpen reopens (open) or closes issueID, with whatever status the
	// tracker uses for that.
	SetIssueOpen(ctx context.Context, project, issueID string, open bool) error
```

Update the `Issue.Project` comment in the struct: `Project string // near slug of the configured project; the tracker's own name when none is configured`.

- [ ] **Step 4: Forgejo**

In `tracker/forgejo/forgejo.go`:

```go
// ownerRepo resolves the "owner/repo" of project (a near slug; "" is the
// only configured one) and returns its near slug with it.
func (a *forgejoAdapter) ownerRepo(project string) (owner, repo, near string, err error) {
	p, err := a.cfg.ProjectOrOnly(project)
	if err != nil {
		return "", "", "", fmt.Errorf("forgejo: %w", err)
	}

	owner, repo, ok := strings.Cut(p.FarSlug, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", "", fmt.Errorf("forgejo: invalid project %q (expected owner/repo)", p.FarSlug)
	}

	return owner, repo, p.NearSlug, nil
}

// projectIssuesPath returns "/repos/{owner}/{repo}/issues" and the near slug.
func (a *forgejoAdapter) projectIssuesPath(project string) (path, near string, err error) {
	owner, repo, near, err := a.ownerRepo(project)
	if err != nil {
		return "", "", err
	}

	return fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo)), near, nil
}

// issuePath builds "/repos/{owner}/{repo}/issues/{n}" for issueID in project.
func (a *forgejoAdapter) issuePath(project, issueID string) (string, error) {
	owner, repo, _, err := a.ownerRepo(project)
	if err != nil {
		return "", err
	}

	n, err := strconv.Atoi(issueID)
	if err != nil {
		return "", fmt.Errorf("forgejo: invalid issue id %q: %w", issueID, err)
	}

	return fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(owner), url.PathEscape(repo), n), nil
}
```

Then thread `project` through: `ListProjectIssues(ctx, project)` calls `a.projectIssuesPath(project)` and `toIssue(&issues[i], near)`; `CreateIssue(ctx, project, title, description)` likewise; `SetIssueOpen(ctx, project, issueID, open)` calls `a.UpdateIssueStatus(ctx, project, issueID, state)`; `UpdateIssueStatus(ctx, project, issueID, statusName)`, `IsIssueClosed(ctx, project, issueID)` and `AddComment(ctx, project, issueID, body)` call `a.issuePath(project, issueID)`. In `ListIssues`, replace the filter:

```go
		near, configured := a.cfg.NearSlugOf(proj)
		if len(a.cfg.Projects) > 0 && !configured {
			continue
		}

		out = append(out, a.toIssue(&issues[i], cmp.Or(near, proj)))
```

`toIssue`'s comment becomes "converts a Forgejo issue of project (a near slug, or the tracker's name when none is configured)". Drop the now unused `slices` import if nothing else uses it.

- [ ] **Step 5: GitHub**

In `tracker/github/github.go`:

```go
// ownerRepo resolves the "owner/repo" of project (a near slug; "" is the
// only configured one) and returns its near slug with it.
func (a *githubAdapter) ownerRepo(project string) (owner, repo, near string, err error) {
	p, err := a.cfg.ProjectOrOnly(project)
	if err != nil {
		return "", "", "", fmt.Errorf("github: %w", err)
	}

	owner, repo, ok := strings.Cut(p.FarSlug, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", "", fmt.Errorf("github: invalid project %q (expected owner/repo)", p.FarSlug)
	}

	return owner, repo, p.NearSlug, nil
}
```

`ListProjectIssues(ctx, project)` and `CreateIssue(ctx, project, title, description)` call `owner, repo, near, err := a.ownerRepo(project)` and `toIssue(iss, near)`. `UpdateIssueStatus(ctx, project, issueID, statusName)`, `IsIssueClosed(ctx, project, issueID)` and `AddComment(ctx, project, issueID, body)` call `owner, repo, _, err := a.ownerRepo(project)`. `SetIssueOpen(ctx, project, issueID, open)` passes `project` to `UpdateIssueStatus`. In `ListIssues`, the filter becomes:

```go
			proj := iss.GetRepository().GetFullName()
			near, configured := a.cfg.NearSlugOf(proj)
			if len(a.cfg.Projects) > 0 && !configured {
				continue
			}

			out = append(out, toIssue(iss, cmp.Or(near, proj)))
```

Add the `cmp` import and drop `slices` if unused.

- [ ] **Step 6: Redmine**

In `tracker/redmine/redmine.go`:

```go
// project resolves the configured project named by near ("" is the only
// configured one).
func (a *redmineAdapter) project(near string) (config.TrackerProject, error) {
	p, err := a.cfg.ProjectOrOnly(near)
	if err != nil {
		return config.TrackerProject{}, fmt.Errorf("redmine: %w", err)
	}

	return p, nil
}
```

`ListProjectIssues(ctx, project)`: `p, err := a.project(project)`; the path uses `p.FarSlug`, `toIssues(payload.Issues, p.NearSlug)`, the error names `p.FarSlug`. `CreateIssue(ctx, project, title, description)`: `ProjectID: p.FarSlug`, returns `toIssues(…, p.NearSlug)[0]`. `UpdateIssueStatus(ctx, _ string, issueID, statusNameOrID)`, `IsIssueClosed(ctx, _ string, issueID)`, `AddComment(ctx, _ string, issueID, body)`, `SetIssueOpen(ctx, _ string, issueID, open)`: the project is ignored, with the comment `// Redmine issue numbers are global: the project is not needed.` on each. `ListIssues` loops over `a.cfg.Projects` and calls `a.fetchIssues(ctx, "/projects/"+url.PathEscape(p.FarSlug)+"/issues.json?status_id=open&limit=100", p.NearSlug)`.

- [ ] **Step 7: Fake and callers, mechanically**

`tracker/fake/fake.go`: add the `project string` parameter to the six methods, unused for now (`_ string`). Task 3 gives it meaning.

Callers, passing the project they have or `""` where Task 10 will pass the real one:
- `issue/mirror.go`: `m.Tracker.ListProjectIssues(ctx, m.Project)`, `m.Tracker.CreateIssue(ctx, m.Project, rec.Title, rec.Description)`, `m.Tracker.IsIssueClosed(ctx, rec.Tracker.Project, rec.Tracker.ID)`, `m.Tracker.SetIssueOpen(ctx, rec.Tracker.Project, rec.Tracker.ID, r == StateOpen)`.
- `cmd/issueflow/tracker.go`: `t.UpdateIssueStatus(ctx, "", issueID, selected)` with the comment `// ponytail: "" is the only configured project; Task 10 of the several-projects plan passes the real one.`
- `cmd/review/tracker.go`: `deps.tracker.AddComment(ctx, "", issueSlug, body)`, same comment.
- `cmd/branch/prune_tracker.go`: `issueResolver.IsIssueClosed(ctx context.Context, project, issueID string) (bool, error)`; the call becomes `tr.IsIssueClosed(ctx, "", id)`, same comment.

Update every test caller: add `""` (or the project the test configures) as the new argument. Files: the three adapter test files (`newMirrorAdapter` and `newTestAdapterWithHandler` configure one project: pass `""`), `tracker/fake/fake_test.go`, `cmd/branch/prune_tracker_test.go` (`fakeIssueResolver.IsIssueClosed(_ context.Context, _, id string)`), `issue/mirror_test.go` and `cmd/issue/close_mirror_e2e_test.go` where they call the fake directly. In the adapter tests that assert `Project: "a/b"` or `"cpro"` on a returned issue, the expected value becomes the near slug: `"p0"` for the `far(...)` helper, `"cpro"` stays (near and far are equal in `newMirrorAdapter`).

- [ ] **Step 8: Build and run the suites**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./tracker/... ./issue/... ./cmd/... 2>&1 | tail -30`
Expected: PASS everywhere, including the new `TestProjectCalls_TwoProjects` in the three adapters.

- [ ] **Step 9: Commit**

```bash
git add tracker/ issue/mirror.go issue/mirror_test.go cmd/issueflow/tracker.go cmd/review/tracker.go cmd/branch/prune_tracker.go cmd/branch/prune_tracker_test.go cmd/issue/close_mirror_e2e_test.go
git commit -m "feat(tracker): the per-project calls name their project by near slug"
```

---

### Task 3: Fake tracker: issues belong to a project

**Files:**
- Modify: `tracker/fake/fake.go`
- Test: `tracker/fake/fake_test.go`

**Interfaces:**
- Produces: `fake.Tracker.ListProjectIssues(ctx, project)` returns the issues whose `Project` equals `project`, every issue when `project == ""`. `CreateIssue` stamps `Project`. `fake.Create`, `fake.Open`, `fake.Update`, `fake.Comment` gain `Project string` (first field). `fake.Tracker.ListErrFor map[string]error` fails the listing of one project.

- [ ] **Step 1: Write the failing tests**

Add to `TestFake_Mirror` in `tracker/fake/fake_test.go`:

```go
	t.Run("the listing is filtered by project and empty means every project", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1", Project: "zf"}, {ID: "1", Project: "other"}, {ID: "2", Project: "zf"}}}
		other, _ := ft.ListProjectIssues(ctx, "other")
		all, _ := ft.ListProjectIssues(ctx, "")
		if len(other) != 1 || other[0].Project != "other" || len(all) != 3 {
			t.Errorf("other = %+v, all = %d", other, len(all))
		}
	})

	t.Run("a created issue carries its project and the creation records it", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{}
		iss, err := ft.CreateIssue(ctx, "other", "Bug", "")
		if err != nil || iss.Project != "other" {
			t.Fatalf("CreateIssue = %+v, %v", iss, err)
		}
		if len(ft.RecordedCreates) != 1 || ft.RecordedCreates[0] != (Create{Project: "other", Title: "Bug"}) {
			t.Errorf("RecordedCreates = %+v", ft.RecordedCreates)
		}
	})

	t.Run("the opens, updates and comments record their project", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "42", Project: "zf"}}}
		_ = ft.SetIssueOpen(ctx, "zf", "42", false)
		_ = ft.UpdateIssueStatus(ctx, "zf", "42", "New")
		_ = ft.AddComment(ctx, "zf", "42", "hi")
		if ft.RecordedOpens[0] != (Open{Project: "zf", IssueID: "42"}) ||
			ft.RecordedUpdates[0] != (Update{Project: "zf", IssueID: "42", StatusName: "New"}) ||
			ft.RecordedComments[0] != (Comment{Project: "zf", IssueID: "42", Body: "hi"}) {
			t.Errorf("opens = %+v, updates = %+v, comments = %+v", ft.RecordedOpens, ft.RecordedUpdates, ft.RecordedComments)
		}
	})

	t.Run("ListErrFor fails one project's listing only", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("down")
		ft := &Tracker{ListErrFor: map[string]error{"other": boom}}
		if _, err := ft.ListProjectIssues(ctx, "zf"); err != nil {
			t.Errorf("zf: %v", err)
		}
		if _, err := ft.ListProjectIssues(ctx, "other"); !errors.Is(err, boom) {
			t.Errorf("other: %v", err)
		}
	})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/fake/... -v 2>&1 | tail -20`
Expected: FAIL (`unknown field Project in struct literal`, `ft.ListErrFor undefined`).

- [ ] **Step 3: Implement**

In `tracker/fake/fake.go`:

```go
// Update captures one UpdateIssueStatus call.
type Update struct {
	Project    string
	IssueID    string
	StatusName string
}

// Comment captures one AddComment call.
type Comment struct {
	Project string
	IssueID string
	Body    string
}

// Create captures one CreateIssue call.
type Create struct {
	Project            string
	Title, Description string
}

// Open captures one SetIssueOpen call.
type Open struct {
	Project string
	IssueID string
	Open    bool
}
```

Add the field `ListErrFor map[string]error // fails the listing of one project` next to `ListErr`. Then:

```go
// ListProjectIssues returns a snapshot of the open issues of project; ""
// lists every project.
func (t *Tracker) ListProjectIssues(_ context.Context, project string) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ListProjectCalls++
	if t.ListErr != nil {
		return nil, t.ListErr
	}
	if err := t.ListErrFor[project]; err != nil {
		return nil, err
	}

	out := make([]tracker.Issue, 0, len(t.ProjectIssues))
	for _, iss := range t.ProjectIssues {
		if project == "" || iss.Project == project {
			out = append(out, iss)
		}
	}

	return out, nil
}
```

`CreateIssue(_ context.Context, project, title, description string)`: the created issue gets `Project: project`, the record is `Create{Project: project, Title: title, Description: description}`. `SetIssueOpen(_, project, issueID, open)` records `Open{Project: project, IssueID: issueID, Open: open}`. `UpdateIssueStatus(_, project, issueID, statusName)` records `Update{Project: project, IssueID: issueID, StatusName: statusName}`. `AddComment(_, project, issueID, body)` records `Comment{Project: project, IssueID: issueID, Body: body}`. `IsIssueClosed(_, _ string, issueID)` is unchanged: the fake keys its closed set by number.

- [ ] **Step 4: Seed a project on every fake issue the mirror tests use**

The mirror asks the fake for project `zf` (its only project until Task 6), and the fake now lists only issues of that project. Set `Project: "zf"` in the helpers: `trackerIssue` in `issue/mirror_test.go`, `fromTracker` in `cmd/issue/mirror_e2e_test.go`, the `ProjectIssues` literals in `cmd/issue/close_mirror_e2e_test.go` (`closeTrackerBranch`) and `cmd/issue/start_record_e2e_test.go` (`TestRunIssueStart_ImportedRecordStartsAsTrackerIssue`), and any inline issue in `TestReconcile_AwkwardTrackerInput` and `TestReconcile_StateTable`. Expected `fake.Create{...}` literals in `issue/mirror_test.go` and `cmd/issue/mirror_e2e_test.go` gain `Project: "zf"`.

- [ ] **Step 5: Run the suites**

Run: `mise exec -- go test ./tracker/fake/... ./issue/... ./cmd/issue/... 2>&1 | tail -20`
Expected: PASS. A test still failing on an empty listing is a fake issue without `Project: "zf"`: add it.

- [ ] **Step 6: Commit**

```bash
git add tracker/fake/ issue/mirror_test.go cmd/issue/mirror_e2e_test.go cmd/issue/close_mirror_e2e_test.go cmd/issue/start_record_e2e_test.go
git commit -m "feat(fake): the fake tracker's issues belong to a project"
```

---

### Task 4: A record knows its project

**Files:**
- Modify: `issue/record.go:104-130,160-167,181-188`, `issue/repo.go:37-62`
- Test: `issue/record_test.go` (`TestFold_Tracker`), `issue/repo_test.go` (`TestCreateLoadAppend`)

**Interfaces:**
- Produces: `Record.Project string` (near slug of the project the issue belongs to or is exported to; `""` means the default); `NewIssue.Project string`, written into the `create` op by `Prepare`.

- [ ] **Step 1: Write the failing tests**

In `TestFold_Tracker` of `issue/record_test.go` add:

```go
	t.Run("a repo-born create with a project sets Project and no link", func(t *testing.T) {
		t.Parallel()

		local := op("l0", OpCreate, "2026-10-01T10:00:00Z")
		local.Title, local.Project = "Local", "other"
		rec := Fold("l0", []Op{local})
		if rec.Project != "other" || rec.Tracker != nil {
			t.Errorf("Project = %q, Tracker = %+v", rec.Project, rec.Tracker)
		}
	})

	t.Run("an import root sets Project to its near slug", func(t *testing.T) {
		t.Parallel()

		if got := Fold("c0", []Op{root}).Project; got != "zf" {
			t.Errorf("Project = %q, want zf", got)
		}
	})

	t.Run("a taken link_tracker sets Project, a losing one does not", func(t *testing.T) {
		t.Parallel()

		first := link("l1", "57", "p0")
		second := link("l2", "58", "l1")
		second.Project = "other"
		rec := Fold("p0", []Op{plain, first, second})
		if rec.Project != "zf" {
			t.Errorf("Project = %q, want zf", rec.Project)
		}
	})
```

In `TestCreateLoadAppend` of `issue/repo_test.go` add a subtest:

```go
	t.Run("Create stores the project in the create op", func(t *testing.T) {
		rec, err := Create(ctx, c, NewIssue{Title: "With project", BranchType: "feat", Project: "other"})
		if err != nil || rec.Project != "other" {
			t.Errorf("Create = %+v, %v", rec, err)
		}
	})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/... -run "TestFold_Tracker|TestCreateLoadAppend" -v 2>&1 | tail -15`
Expected: FAIL (`rec.Project undefined`, `unknown field Project`).

- [ ] **Step 3: Implement**

In `issue/record.go`, add to `Record` after `CreatedAt`:

```go
	// Project is the near slug of the tracker project the issue belongs to
	// (imported or exported) or is to be exported to; "" means the default.
	Project string `json:"project"`
```

In `Fold`, `OpCreate` on the root: `rec.Project = op.Project` before the `TrackerID` check. In `OpLinkTracker`, the `rec.Tracker == nil` case also sets `rec.Project = op.Project`.

In `issue/repo.go`, add `Project string // near slug of the target tracker project; "" is the default` to `NewIssue`, and in `Prepare` write `Project: in.Project` into the `Op` literal.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/... -v 2>&1 | tail -15`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add issue/record.go issue/record_test.go issue/repo.go issue/repo_test.go
git commit -m "feat(issue): a record carries the project it belongs to"
```

---

### Task 5: Qualified display IDs and the lookup

**Files:**
- Modify: `issue/record.go:138-146`, `issue/repo.go:192-250`
- Modify: `tui/issue_record.go:77-95`
- Modify: `cmd/issue/record.go` (`recordPrompter.PickRecord`, `resolveRecord`, `runCloseByID`), `cmd/issue/show.go`, `cmd/issue/edit.go`, `cmd/issue/comment.go`, `cmd/issue/label.go`, `cmd/issue/new.go:102`, `cmd/issue/list.go:170-212`
- Modify: `issue/mirror.go:139-160,170-190`
- Modify: `cmd/issueflow/start.go:187-240,255-290`, `cmd/issueflow/start_prompter.go:35-39,147-162`
- Test: `issue/record_test.go`, `issue/repo_test.go`, `issue/mirror_test.go:603`, `cmd/issue/record_e2e_test.go`, `cmd/issue/start_prompter_test.go`, `cmd/issue/list_record_test.go`

**Interfaces:**
- Produces:
  - `func QualifiedID(project, number string, qualify bool) string` — `number`, or `project + "-" + number` when `qualify`.
  - `func LinkedProjects(records []Record) []string` — sorted distinct projects of the linked records.
  - `func Qualified(records []Record) bool` — `len(LinkedProjects(records)) > 1`.
  - `func (r *Record) DisplayID(qualify bool) string`.
  - `func FindByDisplayID(records []Record, qualify bool, query string) (*Record, error)` — the record whose display ID is `query`; else, for a query of digits, the single record linked to that number; an error listing the qualified IDs when several are; `nil, nil` when none.
  - `func Resolve(ctx, c, query) (Record, bool, error)` — the record and the `Qualified` flag.
  - `recordPrompter.PickRecord(ctx, records []issuepkg.Record, qualify bool) (string, error)`, `StartPrompter.PickIssueFromRepo(ctx, records, qualify bool)`, `tui.IssueRecordPicker(records, qualify, picked, offerNew)`.
  - `resolveRecord(ctx, client, p, args) (issuepkg.Record, bool, error)`.
  - `issue.Row.Project` is set on record rows; the list joins a branch by record ID, then `FindByDisplayID`.

- [ ] **Step 1: Write the failing fold-level tests**

Add to `issue/record_test.go`:

```go
func TestDisplayID(t *testing.T) {
	t.Parallel()

	born := Record{ID: strings.Repeat("a", 40), Tracker: &TrackerLink{Type: "fake", Project: "zf-2", ID: "42", Born: true}}
	exported := Record{ID: strings.Repeat("b", 40), Tracker: &TrackerLink{Type: "fake", Project: "zf-2", ID: "7"}}
	plain := Record{ID: strings.Repeat("c", 40)}

	for name, tc := range map[string]struct {
		rec     Record
		qualify bool
		want    string
	}{
		"a born record, unqualified, is its number":         {born, false, "42"},
		"a born record, qualified, is prefixed by its slug": {born, true, "zf-2-42"},
		"an exported record keeps its short hash":           {exported, true, strings.Repeat("b", 7)},
		"a plain record keeps its short hash":               {plain, true, strings.Repeat("c", 7)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.rec.DisplayID(tc.qualify); got != tc.want {
				t.Errorf("DisplayID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQualified(t *testing.T) {
	t.Parallel()

	link := func(project, id string, born bool) Record {
		return Record{ID: project + id, Tracker: &TrackerLink{Type: "fake", Project: project, ID: id, Born: born}}
	}

	for name, tc := range map[string]struct {
		records []Record
		want    bool
	}{
		"no record":                                  {nil, false},
		"unlinked records only":                      {[]Record{{ID: "x"}, {ID: "y", Project: "other"}}, false},
		"links of one project":                       {[]Record{link("zf", "1", true), link("zf", "2", false)}, false},
		"imports of two projects":                    {[]Record{link("zf", "1", true), link("other", "1", true)}, true},
		"an import and an export of two projects":    {[]Record{link("zf", "1", true), link("other", "1", false)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := Qualified(tc.records); got != tc.want {
				t.Errorf("Qualified = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("LinkedProjects is sorted and distinct", func(t *testing.T) {
		t.Parallel()

		got := LinkedProjects([]Record{link("zf", "1", true), link("other", "1", true), link("zf", "2", false)})
		if !slices.Equal(got, []string{"other", "zf"}) {
			t.Errorf("LinkedProjects = %v", got)
		}
	})
}

func TestFindByDisplayID(t *testing.T) {
	t.Parallel()

	zf := Record{ID: "a", Tracker: &TrackerLink{Type: "fake", Project: "zf", ID: "42", Born: true}}
	other := Record{ID: "b", Tracker: &TrackerLink{Type: "fake", Project: "other", ID: "42", Born: true}}
	only := Record{ID: "c", Tracker: &TrackerLink{Type: "fake", Project: "other", ID: "7"}}
	records := []Record{zf, other, only}

	t.Run("a qualified ID finds its record", func(t *testing.T) {
		t.Parallel()

		rec, err := FindByDisplayID(records, true, "other-42")
		if err != nil || rec == nil || rec.ID != "b" {
			t.Errorf("found %+v, %v", rec, err)
		}
	})
	t.Run("a bare number linked once finds its record, exported or not", func(t *testing.T) {
		t.Parallel()

		rec, err := FindByDisplayID(records, true, "7")
		if err != nil || rec == nil || rec.ID != "c" {
			t.Errorf("found %+v, %v", rec, err)
		}
	})
	t.Run("a bare number linked twice is an error listing the qualified IDs", func(t *testing.T) {
		t.Parallel()

		_, err := FindByDisplayID(records, true, "42")
		if err == nil || !strings.Contains(err.Error(), "zf-42") || !strings.Contains(err.Error(), "other-42") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unqualified, a bare number finds the only record with it", func(t *testing.T) {
		t.Parallel()

		rec, err := FindByDisplayID([]Record{zf, only}, false, "42")
		if err != nil || rec == nil || rec.ID != "a" {
			t.Errorf("found %+v, %v", rec, err)
		}
	})
	t.Run("nothing matching is nil, nil", func(t *testing.T) {
		t.Parallel()

		if rec, err := FindByDisplayID(records, true, "999"); rec != nil || err != nil {
			t.Errorf("found %+v, %v", rec, err)
		}
	})
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/... -run "TestDisplayID|TestQualified|TestFindByDisplayID" 2>&1 | tail -5`
Expected: FAIL, compile errors.

- [ ] **Step 3: Implement the issue package**

In `issue/record.go`, replace `DisplayID`:

```go
// QualifiedID is the display ID of tracker issue number in project: the
// number, prefixed with the near slug and a dash when qualify is set.
func QualifiedID(project, number string, qualify bool) string {
	if qualify && project != "" {
		return project + "-" + number
	}

	return number
}

// DisplayID is the ID shown to users and used in branch names: the tracker's
// number for an issue born in the tracker, qualified by its project when
// qualify is set (see Qualified), the short hash otherwise.
func (r *Record) DisplayID(qualify bool) string {
	if r.Tracker != nil && r.Tracker.Born {
		return QualifiedID(r.Tracker.Project, r.Tracker.ID, qualify)
	}

	return r.ShortID()
}

// LinkedProjects returns the sorted, distinct projects of the records
// mirrored with the tracker, imported or exported.
func LinkedProjects(records []Record) []string {
	var out []string
	for i := range records {
		if t := records[i].Tracker; t != nil && !slices.Contains(out, t.Project) {
			out = append(out, t.Project)
		}
	}
	slices.Sort(out)

	return out
}

// Qualified reports whether display IDs carry their project: the linked
// records span more than one project. A link is never removed, so once true
// it stays true on every clone that has fetched, whatever its config says.
func Qualified(records []Record) bool {
	return len(LinkedProjects(records)) > 1
}

// FindByDisplayID returns the record whose display ID is query, or, for a
// query made of digits, the one record linked to that tracker number. A
// number several records are linked to is an error that lists their
// qualified IDs. No match is nil, nil.
func FindByDisplayID(records []Record, qualify bool, query string) (*Record, error) {
	for i := range records {
		if records[i].DisplayID(qualify) == query {
			return &records[i], nil
		}
	}
	if !trackerNumberRe.MatchString(query) {
		return nil, nil
	}

	var matches []*Record
	for i := range records {
		if t := records[i].Tracker; t != nil && t.ID == query {
			matches = append(matches, &records[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	}

	names := make([]string, len(matches))
	for i, rec := range matches {
		names[i] = QualifiedID(rec.Tracker.Project, rec.Tracker.ID, true)
	}

	return nil, fmt.Errorf("issue %q is in several projects: %s", query, strings.Join(names, ", "))
}
```

Move `trackerNumberRe` from `repo.go` to `record.go` (next to the new function) and add the `fmt` and `strings` imports to `record.go`.

In `issue/repo.go`, replace `Resolve`:

```go
// Resolve finds the issue designated by query: a full ID, the display ID or
// tracker number of a mirrored issue (see FindByDisplayID), or a unique ID
// prefix of at least 4 characters. It reads every issue, and returns with
// the record whether display IDs are qualified in this repository.
func Resolve(ctx context.Context, c *git.Client, query string) (Record, bool, error) {
	records, _, err := List(ctx, c)
	if err != nil {
		return Record{}, false, err
	}
	qualify := Qualified(records)

	byID := make(map[string]*Record, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
	}
	if rec, ok := byID[query]; ok {
		return *rec, qualify, nil
	}

	rec, err := FindByDisplayID(records, qualify, query)
	if err != nil {
		return Record{}, qualify, err
	}
	if rec != nil {
		return *rec, qualify, nil
	}

	if len(query) < minPrefixLen {
		return Record{}, qualify, fmt.Errorf("issue %q: %w (use at least %d characters of the ID)",
			query, git.ErrIssueNotFound, minPrefixLen)
	}

	// The prefix is matched on every ref, including one List skipped as
	// corrupt: naming it in the ambiguity message is what tells the user it
	// is there.
	ids, err := c.ListChainIDs(ctx, git.IssueRefs)
	if err != nil {
		return Record{}, qualify, fmt.Errorf("list issues: %w", err)
	}

	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, query) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return Record{}, qualify, fmt.Errorf("issue %q: %w", query, git.ErrIssueNotFound)
	case 1:
		if rec, ok := byID[matches[0]]; ok {
			return *rec, qualify, nil
		}
		rec, err := Load(ctx, c, matches[0]) // a ref List skipped: Load says why
		return rec, qualify, err
	default:
		lines := make([]string, 0, len(matches))
		for _, id := range matches {
			title := "(unreadable)"
			if rec, ok := byID[id]; ok {
				title = rec.Title
			}
			lines = append(lines, "  "+id+"  "+title)
		}

		return Record{}, qualify, fmt.Errorf("issue ID %q is ambiguous:\n%s", query, strings.Join(lines, "\n"))
	}
}
```

Remove the `regexp` import from `repo.go` (`trackerNumberRe` moved to `record.go`). `TestListAndResolve` and `TestResolve_Ambiguous` keep their setup; their `Resolve` calls take the three return values (`got, _, err := Resolve(…)`).

- [ ] **Step 4: Update the callers**

`issue/mirror.go`: in `Reconcile`, after `List`, add `qualify := Qualified(records)`; give `index` and `syncRecords` a `qualify bool` parameter and use `rec.DisplayID(qualify)` in the warnings and in `duplicateOf`.

`tui/issue_record.go`: `func IssueRecordPicker(records []issue.Record, qualify bool, picked *string, offerNew bool) *huh.Group`, label `records[i].DisplayID(qualify)`.

`cmd/issue/record.go`:
- `recordPrompter.PickRecord(ctx context.Context, records []issuepkg.Record, qualify bool) (string, error)`; the huh implementation passes `qualify` to the picker.
- `resolveRecord` returns `(issuepkg.Record, bool, error)`: the `Resolve` path returns its flag; the picker path computes `qualify := issuepkg.Qualified(records)`, passes it to `PickRecord`, and returns it.
- `runCloseByID`: `rec, qualify, err := resolveRecord(…)`; every `rec.DisplayID()` becomes `rec.DisplayID(qualify)`. The in-progress check reads the chain under both slugs a born record may have had:

```go
	slugs := []string{rec.DisplayID(qualify)}
	if other := rec.DisplayID(!qualify); other != slugs[0] {
		slugs = append(slugs, other) // a branch started before the repository qualified its IDs
	}
	var inProgress []string
	for _, slug := range slugs {
		st, err := branch.Load(ctx, client, slug)
		if err != nil {
			return fmt.Errorf("read the branches of issue %s: %w", slugs[0], err)
		}
		if st == nil {
			continue
		}
		for i := range st.Entries {
			if st.Entries[i].Status == branch.StatusInProgress {
				inProgress = append(inProgress, st.Entries[i].Name)
			}
		}
	}
```

`cmd/issue/show.go`: `rec, qualify, err := resolveRecord(…)`, `renderRecord(w, &rec, qualify)` with `rec.DisplayID(qualify)`. `cmd/issue/edit.go`, `comment.go`, `label.go`: same three-value `resolveRecord` and `DisplayID(qualify)`. `cmd/issue/new.go:102`: `rec.ShortID()` (a record just created is repo-born: its display ID is its short hash).

`cmd/issue/list.go`, `mergeRepoIssues`: replace the `byDisplayID` map and the join with

```go
	qualify := issuepkg.Qualified(records)
	byID := make(map[string]*issuepkg.Record, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
	}

	out := rows
	started := make(map[string]bool, len(out))
	for i := range out {
		rec, ok := byID[out[i].Branch.IssueID]
		if !ok {
			// A branch started before the start op carried the full ID, or
			// before the repository qualified its display IDs, is joined by
			// its slug: the display ID, or the bare number when one record
			// has it. ponytail: a scan per branch; index the numbers if a
			// repository with thousands of issues makes the list slow.
			found, err := issuepkg.FindByDisplayID(records, qualify, out[i].IssueSlug)
			if err != nil || found == nil {
				continue
			}
			rec = found
		}
		started[rec.ID] = true
		out[i].Title = rec.Title
		out[i].Project = rec.Project
		…
```

and in the backlog loop set `IssueSlug: rec.DisplayID(qualify)` and `Project: rec.Project`.

`cmd/issueflow/start_prompter.go`: `PickIssueFromRepo(ctx context.Context, records []issuepkg.Record, qualify bool) (*issuepkg.Record, error)` on the interface and the huh implementation (`tui.IssueRecordPicker(records, qualify, &id, true)`). `cmd/issue/start_prompter_test.go`: the scripted method takes and ignores the flag.

`cmd/issueflow/start.go`, `getFromRepoOrUser`: after listing, `qualify := issue.Qualified(records)`; `p.PickIssueFromRepo(ctx, open, qualify)`; `checkRecordType(rec, allowedTypes, qualify)` and `issueFromRecord(rec, qualify)` take the flag and use `rec.DisplayID(qualify)`; the prepared new issue uses `got.ID, got.RecordID, got.RecordPending = rec.ShortID(), id, true`. `fromImportedRecord` keeps `src.ID` as the slug for now; Task 9 qualifies it.

- [ ] **Step 5: Write the command-level tests**

In `issue/mirror_test.go`, `TestResolve_TrackerNumber`: the calls become `rec, _, err := Resolve(…)`; add one subtest after reconciling a second fake with `Projects: []string{"other"}`… the mirror still takes one project until Task 6, so instead seed the second link by hand:

```go
	t.Run("a number in two projects is refused with the qualified IDs", func(t *testing.T) {
		twin, err := Create(ctx, c, NewIssue{Title: "Twin", BranchType: "fix"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := Append(ctx, c, twin.ID, &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "other", TrackerID: "42"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		_, qualify, err := Resolve(ctx, c, "42")
		if !qualify || err == nil || !strings.Contains(err.Error(), "zf-42") || !strings.Contains(err.Error(), "other-42") {
			t.Errorf("qualify = %v, err = %v", qualify, err)
		}
		if rec, _, err := Resolve(ctx, c, "zf-42"); err != nil || rec.Title != "From tracker" {
			t.Errorf("Resolve(zf-42) = %q, %v", rec.Title, err)
		}
	})
```

In `cmd/issue/list_record_test.go`, add:

```go
func TestBuildRows_JoinsAnOldBareBranchToItsQualifiedRecord(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	seedImported := func(project, number, title string) issuepkg.Record {
		t.Helper()
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{fromTracker(number, title, "open")}}
		ft.ProjectIssues[0].Project = project
		m := &issuepkg.Mirror{Tracker: ft, Type: "fake", Project: project}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec, _, err := issuepkg.Resolve(ctx, rig.client, project+"-"+number)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		return rec
	}
	zf := seedImported("zf", "42", "In zf")
	seedImported("other", "7", "In other")
	// Started when zf was the only project: a bare slug, no record ID.
	branchtest.Seed(t, rig.client, branch.Op{Branch: "42@feat@old", Title: "Old", TrackerType: "fake"}, branch.StatusInProgress)

	rows, err := buildRows(ctx, issueListInfra{stderr: &bytes.Buffer{}, client: rig.client}, "")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
	})
	t.Run("the old branch row carries its record and no backlog twin appears", func(t *testing.T) {
		var joined int
		for _, r := range rows {
			if r.Title == "In zf" {
				joined++
				if r.Branch == nil || r.Project != "zf" || r.IssueSlug != "42" {
					t.Errorf("row = %+v", r)
				}
			}
		}
		if joined != 1 {
			t.Errorf("rows with the zf issue = %d, want 1: %+v", joined, rows)
		}
		_ = zf
	})
	t.Run("the backlog record of the other project is qualified", func(t *testing.T) {
		if !slices.ContainsFunc(rows, func(r issuepkg.Row) bool { return r.IssueSlug == "other-7" && r.Project == "other" }) {
			t.Errorf("rows = %+v", rows)
		}
	})
}
```

Add the imports the file lacks: `tracker`, `tracker/fake`. (`Mirror.Project` is replaced by `Projects` in Task 6: that task updates this literal.)

- [ ] **Step 6: Build and run everything**

Run: `mise exec -- go build ./... && mise exec -- go test ./... 2>&1 | tail -30`
Expected: PASS. Every `DisplayID()` call without an argument is a compile error pointing at a caller to update; `TestMirror_ShowByTrackerNumber` and `TestRunShow` must still pass unchanged (one project: bare numbers).

- [ ] **Step 7: Commit**

```bash
git add issue/ tui/issue_record.go cmd/issue/ cmd/issueflow/
git commit -m "feat(issue): display IDs are qualified by project once the repository holds several"
```

---

### Task 6: The mirror over several projects

**Files:**
- Modify: `issue/mirror.go`
- Modify: `cmd/issue/record.go:139-148` (`mirrorOf`)
- Test: `issue/mirror_test.go`, `cmd/issue/mirror_e2e_test.go:38-70`, `cmd/issue/start_record_e2e_test.go:465`, `cmd/issue/list_record_test.go` (the literal of Task 5)

**Interfaces:**
- Produces:
  ```go
  type Mirror struct {
  	Tracker  tracker.Tracker
  	Type     string   // tracker type, as configured
  	Projects []string // near slugs, config order; the first is the default
  }
  func NewMirror(tc config.IssueTrackerConfig, t tracker.Tracker) *Mirror // nil when the mirror is off or t is nil
  ```
  A record is owned when its link has `Type` and a project in `Projects`. An open unlinked record is exported to `rec.Project`, to `Projects[0]` when empty, and skipped with a warning when its project is not in `Projects`.

- [ ] **Step 1: Write the failing tests**

In `issue/mirror_test.go`, change `newTestMirror` to `return &Mirror{Tracker: ft, Type: "fake", Projects: []string{"zf"}}` and add:

```go
func TestReconcile_TwoProjects(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	other42 := trackerIssue("42", "Other 42")
	other42.Project = "other"
	other7 := trackerIssue("7", "Other 7")
	other7.Project = "other"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "Zf 42"), other42, other7}}
	m := &Mirror{Tracker: ft, Type: "fake", Projects: []string{"zf", "other"}}

	toOther, err := Create(ctx, c, NewIssue{Title: "To other", BranchType: "fix", Project: "other"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	toDefault, err := Create(ctx, c, NewIssue{Title: "To default", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	elsewhere, err := Create(ctx, c, NewIssue{Title: "Elsewhere", BranchType: "fix", Project: "nope"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	foreign, err := Create(ctx, c, NewIssue{Title: "Foreign", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Append(ctx, c, foreign.ID, &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "third", TrackerID: "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, m, c)
	records, _, err := List(ctx, c)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byTitle := make(map[string]Record, len(records))
	for _, rec := range records {
		byTitle[rec.Title] = rec
	}

	t.Run("both projects are listed and imported", func(t *testing.T) {
		if ft.ListProjectCalls != 2 || res.Imported != 3 {
			t.Errorf("listings = %d, res = %+v", ft.ListProjectCalls, res)
		}
	})
	t.Run("the same number in two projects gives two records with different roots", func(t *testing.T) {
		a, b := byTitle["Zf 42"], byTitle["Other 42"]
		if a.ID == "" || b.ID == "" || a.ID == b.ID {
			t.Errorf("zf = %q, other = %q", a.ID, b.ID)
		}
		if a.DisplayID(true) != "zf-42" || b.DisplayID(true) != "other-42" {
			t.Errorf("display = %q, %q", a.DisplayID(true), b.DisplayID(true))
		}
	})
	t.Run("a record is exported to its own project, the default when it has none", func(t *testing.T) {
		want := []fake.Create{{Project: "other", Title: "To other"}, {Project: "zf", Title: "To default"}}
		got := slices.Clone(ft.RecordedCreates)
		slices.SortFunc(got, func(a, b fake.Create) int { return strings.Compare(a.Title, b.Title) })
		if res.Exported != 2 || !slices.Equal(got, want) {
			t.Errorf("exported = %d, creates = %+v", res.Exported, ft.RecordedCreates)
		}
		if rec := byTitle["To other"]; rec.Tracker == nil || rec.Tracker.Project != "other" {
			t.Errorf("link = %+v", rec.Tracker)
		}
		_, _ = toOther, toDefault
	})
	t.Run("a record of a project not configured here is warned about and not exported", func(t *testing.T) {
		if rec := byTitle["Elsewhere"]; rec.Tracker != nil {
			t.Errorf("link = %+v, want none", rec.Tracker)
		}
		if !slices.ContainsFunc(res.Warnings, func(w string) bool {
			return strings.Contains(w, elsewhere.ShortID()) && strings.Contains(w, "nope")
		}) {
			t.Errorf("warnings = %v", res.Warnings)
		}
	})
	t.Run("a record linked to a third project is left alone", func(t *testing.T) {
		if rec := byTitle["Foreign"]; rec.TrackerState != "" || len(ft.RecordedOpens) != 0 {
			t.Errorf("state = %q, opens = %+v", rec.TrackerState, ft.RecordedOpens)
		}
	})

	// The state table on a non-default project: closed in the tracker, the
	// record follows; closed in the repo, the tracker is told with its project.
	ft.CloseIssue("7")
	if err := Append(ctx, c, byTitle["Other 42"].ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	again := mustReconcile(t, m, c)
	records, _, _ = List(ctx, c)
	for _, rec := range records {
		byTitle[rec.Title] = rec
	}

	t.Run("an issue closed in the tracker closes its record", func(t *testing.T) {
		if rec := byTitle["Other 7"]; rec.State != StateClosed || rec.TrackerState != StateClosed || again.Pulled != 1 {
			t.Errorf("record = %+v, res = %+v", rec, again)
		}
	})
	t.Run("a record closed in the repo closes the tracker issue of its project", func(t *testing.T) {
		if again.Pushed != 1 || !slices.Contains(ft.RecordedOpens, fake.Open{Project: "other", IssueID: "42"}) {
			t.Errorf("res = %+v, opens = %+v", again, ft.RecordedOpens)
		}
	})
}

func TestReconcile_OneFailedListingWritesNothing(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{
		ProjectIssues: []tracker.Issue{trackerIssue("42", "Zf 42")},
		ListErrFor:    map[string]error{"other": errors.New("down")},
	}
	m := &Mirror{Tracker: ft, Type: "fake", Projects: []string{"zf", "other"}}

	_, err := m.Reconcile(ctx, c)
	records, _, _ := List(ctx, c)

	t.Run("the error names the project", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "other") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("the first project's issue was not imported", func(t *testing.T) {
		if len(records) != 0 {
			t.Errorf("records = %d, want 0", len(records))
		}
	})
}
```

In `cmd/issue/mirror_e2e_test.go`, `TestMirrorOf`: the assertion becomes `m == nil || m.Type != "fake" || !slices.Equal(m.Projects, []string{"zf"})`; add a subtest "mirror on with two projects lists both in config order".

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/... -run "TestReconcile_TwoProjects|TestReconcile_OneFailed" 2>&1 | tail -5`
Expected: FAIL (`unknown field Projects`).

- [ ] **Step 3: Implement**

In `issue/mirror.go`:

```go
// Mirror ties the issues of this repository to the projects of one tracker.
type Mirror struct {
	Tracker  tracker.Tracker
	Type     string   // tracker type, as configured
	Projects []string // near slugs, config order; the first is the default
}

// NewMirror returns the mirror the config describes over t, or nil when the
// mirror is off or there is no tracker. A nil Mirror is a no-op everywhere.
func NewMirror(tc config.IssueTrackerConfig, t tracker.Tracker) *Mirror {
	if !tc.Mirror || t == nil || len(tc.Projects) == 0 {
		return nil
	}

	projects := make([]string, len(tc.Projects))
	for i, p := range tc.Projects {
		projects[i] = p.NearSlug
	}

	return &Mirror{Tracker: t, Type: tc.Type, Projects: projects}
}

// issueKey names one tracker issue: two projects can share a number.
type issueKey struct{ project, id string }

// owns reports whether rec is mirrored with one of this mirror's projects.
func (m *Mirror) owns(rec *Record) bool {
	return rec.Tracker != nil && rec.Tracker.Type == m.Type && slices.Contains(m.Projects, rec.Tracker.Project)
}
```

`Reconcile`: list every project first,

```go
	var listed []tracker.Issue
	for _, project := range m.Projects {
		issues, err := m.Tracker.ListProjectIssues(ctx, project)
		if err != nil {
			return res, fmt.Errorf("list tracker issues of %s: %w", project, err)
		}
		for i := range issues {
			issues[i].Project = project // the adapter's near slug, pinned
		}
		listed = append(listed, issues...)
	}
```

then key `open`, `linked`, `bare` and `duplicateOf` by `issueKey{iss.Project, iss.ID}` (and `issueKey{rec.Tracker.Project, rec.Tracker.ID}` on the record side). `importIssue` validates and writes `iss.Project` in place of `m.Project`. `healImport` takes the keyed `bare` map. In `syncRecords`:

```go
		case rec.Tracker == nil && rec.State == StateOpen:
			project := cmp.Or(rec.Project, m.Projects[0])
			if !slices.Contains(m.Projects, project) {
				res.warnf("issue %s belongs to project %q, which is not configured here: not exported", rec.DisplayID(qualify), project)

				continue
			}
			if err := m.export(ctx, c, rec, project); err != nil {
```

`export(ctx, c, rec, project)` calls `CreateIssue(ctx, project, …)` and writes `Project: project` in the link op. `syncState` looks up `open[issueKey{rec.Tracker.Project, rec.Tracker.ID}]`. Add `"github.com/piprim/git-zf/config"` to the imports.

`cmd/issue/record.go`, `mirrorOf`: `return issuepkg.NewMirror(cfg.IssueTracker, t)`; keep the function and its doc comment (the commands and tests call it).

Update the literals: `cmd/issue/start_record_e2e_test.go` (`Projects: []string{"zf"}`), `cmd/issue/list_record_test.go` (`Projects: []string{project}`).

- [ ] **Step 4: Run the suites**

Run: `mise exec -- go test ./issue/... ./cmd/issue/... 2>&1 | tail -20`
Expected: PASS, including `TestReconcile_StateTable` and `TestReconcile_TwoClonesImportOffline` unchanged.

- [ ] **Step 5: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go cmd/issue/record.go cmd/issue/mirror_e2e_test.go cmd/issue/start_record_e2e_test.go cmd/issue/list_record_test.go
git commit -m "feat(issue): the mirror reconciles every configured project"
```

---

### Task 7: The branch chain records the project

**Files:**
- Modify: `branch/record.go:36-70,86-95,128-140,160-190`
- Test: `branch/record_test.go:40-75,110-136,258-310`

**Interfaces:**
- Produces: `branch.Op.Project string` (`json:"project,omitempty"`, start: near slug of the tracker project), `branch.State.Project`, `branch.Row.Project` (`json:"project,omitempty"`). First non-empty value wins, like `TrackerType`.

- [ ] **Step 1: Write the failing tests**

In `branch/record_test.go`: in `TestFold`, set `root.Project = "zf"` next to the other root fields and extend the first-value table with `{"project", func(o *Op, v string) { o.Project = v }, func(s State) string { return s.Project }}`; in the "start adds an in-progress entry" subtest add `|| st.Project != "zf"` to the issue-fields check. In `TestRows` add:

```go
	t.Run("a row carries the chain's project", func(t *testing.T) {
		t.Parallel()

		st := Fold("zf-42", []Op{func() Op { o := startOp("s1", t0, "zf-42@feat@x"); o.Project = "zf"; return o }()})
		rows := Rows([]State{st}, StatusAll)
		if len(rows) != 1 || rows[0].Project != "zf" {
			t.Errorf("rows = %+v", rows)
		}
	})
```

In `TestDecodeOp`, add:

```go
	t.Run("the project is decoded and cleaned of control characters", func(t *testing.T) {
		t.Parallel()

		op, ok := DecodeOp("x", nil, []byte(`{"v":1,"type":"start","at":"`+t0+`","branch":"zf-42@feat@x","project":"zf\u0007"}`))
		if !ok || op.Project != "zf" {
			t.Errorf("op = %+v, ok = %v", op, ok)
		}
	})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./branch/... -run "TestFold|TestRows|TestDecodeOp" 2>&1 | tail -5`
Expected: FAIL (`o.Project undefined`).

- [ ] **Step 3: Implement**

In `branch/record.go`: add `Project string \`json:"project,omitempty"\` // start: near slug of the tracker project, "" before projects were recorded` to `Op` after `TrackerType`; clean it in `DecodeOp` (`op.Project = text.Line(op.Project)`); add `Project string // near slug of the tracker project, "" when unknown` to `State` after `TrackerType`; in `apply`, `st.Project = cmp.Or(st.Project, op.Project)`; add `Project string \`json:"project,omitempty"\`` to `Row` and copy it in `Rows`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./branch/... 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add branch/record.go branch/record_test.go
git commit -m "feat(branch): the start op records the tracker project"
```

---

### Task 8: TrackerRef and a project-aware status update

**Files:**
- Create: `cmd/issueflow/trackerref.go`
- Modify: `cmd/issueflow/tracker.go`
- Test: `cmd/issueflow/trackerref_test.go` (new)

**Interfaces:**
- Consumes: `issue.FindByDisplayID`, `issue.Qualified`, `issue.List` (Task 5), `branch.State.Project` (Task 7).
- Produces:
  ```go
  // TrackerRef returns the tracker project and number of the issue a branch
  // slug names, in this order: the project the branch chain recorded, the
  // number being the slug without its "<project>-" prefix; else the record
  // whose display ID is the slug; else "" and the slug, which makes the
  // adapter use the only configured project or refuse.
  func TrackerRef(chainProject, slug string, records []issue.Record) (project, number string)
  // LoadTrackerRef reads the branch chain and the records of the repository,
  // then resolves like TrackerRef. A chain or listing that cannot be read is
  // a warning, after which the slug is returned as the number.
  func LoadTrackerRef(ctx context.Context, c *git.Client, slug string) (project, number string)
  func ApplyTrackerStatus(ctx, t, errW, project, issueID, trackerType string, pick …)
  ```

- [ ] **Step 1: Write the failing tests**

Create `cmd/issueflow/trackerref_test.go`:

```go
package issueflow

import (
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/issue"
)

func TestTrackerRef(t *testing.T) {
	t.Parallel()

	records := []issue.Record{
		{ID: "a", Tracker: &issue.TrackerLink{Type: "fake", Project: "zf", ID: "42", Born: true}},
		{ID: "b", Tracker: &issue.TrackerLink{Type: "fake", Project: "other", ID: "42", Born: true}},
		{ID: "c", Tracker: &issue.TrackerLink{Type: "fake", Project: "other", ID: "7"}},
	}

	for name, tc := range map[string]struct {
		chainProject, slug  string
		wantProject, wantID string
	}{
		"the chain's project wins and the prefix is stripped":     {"other", "other-42", "other", "42"},
		"the chain's project with a bare slug":                    {"zf", "42", "zf", "42"},
		"a chain project with a dash strips its own prefix only":  {"zf-2", "zf-2-42", "zf-2", "42"},
		"no chain project: the record whose display ID matches":   {"", "other-42", "other", "42"},
		"no chain project: a number one record is linked to":      {"", "7", "other", "7"},
		"no chain project and an ambiguous number: the slug only": {"", "42", "", "42"},
		"nothing known: the slug only":                            {"", "ABC-9", "", "ABC-9"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			project, id := TrackerRef(tc.chainProject, tc.slug, records)
			if project != tc.wantProject || id != tc.wantID {
				t.Errorf("TrackerRef = %q, %q, want %q, %q", project, id, tc.wantProject, tc.wantID)
			}
		})
	}
}

func TestLoadTrackerRef(t *testing.T) {
	t.Parallel()

	c, _ := newChainRepo(t, "")
	ctx := t.Context()
	branchtest.Seed(t, c, branch.Op{Branch: "other-42@feat@x", TrackerType: "fake", Project: "other"}, branch.StatusInProgress)
	rec, err := issue.Create(ctx, c, issue.NewIssue{Title: "Old", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issue.Append(ctx, c, rec.ID, &issue.Op{Type: issue.OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "9"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	branchtest.Seed(t, c, branch.Op{Branch: "9@feat@old", TrackerType: "fake"}, branch.StatusInProgress)

	t.Run("a chain with a project answers from the chain", func(t *testing.T) {
		if p, id := LoadTrackerRef(ctx, c, "other-42"); p != "other" || id != "42" {
			t.Errorf("= %q, %q", p, id)
		}
	})
	t.Run("a chain without a project answers from the record", func(t *testing.T) {
		if p, id := LoadTrackerRef(ctx, c, "9"); p != "zf" || id != "9" {
			t.Errorf("= %q, %q", p, id)
		}
	})
	t.Run("an unknown slug comes back bare", func(t *testing.T) {
		if p, id := LoadTrackerRef(ctx, c, "nope"); p != "" || id != "nope" {
			t.Errorf("= %q, %q", p, id)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issueflow/... -run "TrackerRef" 2>&1 | tail -5`
Expected: FAIL (`undefined: TrackerRef`).

- [ ] **Step 3: Implement**

Create `cmd/issueflow/trackerref.go`:

```go
package issueflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
)

// TrackerRef returns the tracker project and number of the issue a branch
// slug names, in this order: the project the branch chain recorded, the
// number being the slug without its "<project>-" prefix; else the record
// whose display ID is the slug (see issue.FindByDisplayID); else "" and the
// slug, which makes the adapter use the only configured project or refuse.
func TrackerRef(chainProject, slug string, records []issue.Record) (project, number string) {
	if chainProject != "" {
		return chainProject, strings.TrimPrefix(slug, chainProject+"-")
	}

	rec, err := issue.FindByDisplayID(records, issue.Qualified(records), slug)
	if err == nil && rec != nil && rec.Tracker != nil {
		return rec.Tracker.Project, rec.Tracker.ID
	}

	return "", slug
}

// LoadTrackerRef reads the branch chain of slug and the repository's issues,
// then resolves like TrackerRef. What cannot be read is a warning: the slug
// then comes back as the number.
func LoadTrackerRef(ctx context.Context, c *git.Client, slug string) (project, number string) {
	chainProject := ""
	if st, err := branch.Load(ctx, c, slug); err != nil {
		fmt.Fprintf(c.IO().Err, "warning: read branch ref: %v\n", err)
	} else if st != nil {
		chainProject = st.Project
	}

	var records []issue.Record
	if chainProject == "" {
		var err error
		if records, _, err = issue.List(ctx, c); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: list issues: %v\n", err)
		}
	}

	return TrackerRef(chainProject, slug, records)
}
```

In `cmd/issueflow/tracker.go`, `ApplyTrackerStatus` takes `project` before `issueID` and calls `t.UpdateIssueStatus(ctx, project, issueID, selected)`; drop the ponytail comment of Task 2. Update its callers to pass `""` for now: `cmd/issueflow/start.go` (`createFlow`), `cmd/issue/close.go:724`, `cmd/review/tracker.go` (`applyTrackerStatus`). Tasks 9 and 10 pass the real project.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/issueflow/... 2>&1 | tail -5`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/issueflow/trackerref.go cmd/issueflow/trackerref_test.go cmd/issueflow/tracker.go cmd/issueflow/start.go cmd/issue/close.go cmd/review/tracker.go
git commit -m "feat(issueflow): resolve a branch to its tracker project and number"
```

---

### Task 9: The start flow names qualified branches and records the project

**Files:**
- Modify: `issue/issue.go:28-40`
- Modify: `cmd/issueflow/start.go` (`StartDeps`, `BuildStartDeps`, `pickIssue`, `getFromRepoOrUser`, `fromImportedRecord`, `getFromTracker`, `createFlow`, `prepareBranch`)
- Modify: `cmd/issueflow/start_prompter.go:131-145`
- Test: `cmd/issue/start_record_e2e_test.go`, `cmd/issue/start_e2e_test.go`

**Interfaces:**
- Consumes: `issue.NewMirror`, `Mirror.Projects`, `Mirror.Reconcile` (Task 6); `issue.QualifiedID`, `issue.Qualified`, `issue.LinkedProjects` (Task 5); `branch.Op.Project` (Task 7); `ApplyTrackerStatus(…, project, issueID, …)` (Task 8).
- Produces: `issue.Issue.Slug string` (names the branch; `""` means `ID`); `StartDeps.Mirror *issue.Mirror` (nil when off); `BuildStartDeps` sets it. The huh tracker picker copies `Project` into the picked issue.

- [ ] **Step 1: Write the failing tests**

In `cmd/issue/start_e2e_test.go` add:

```go
// A repository whose issues span two projects: a live pick in the second
// project names its branch with the qualified slug and records the project.
func TestRunIssueStart_LivePickInASecondProjectIsQualified(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.cfg.IssueTracker.Projects = []config.TrackerProject{{NearSlug: "zf", FarSlug: "o/a"}, {NearSlug: "other", FarSlug: "o/b"}}
	rig.cfg.IssueTracker.Mirror = true
	rig.tracker.ProjectIssues = []tracker.Issue{
		fromTracker("1", "In zf", "open"), fromTracker("9", "Add lints", "open"),
	}
	rig.tracker.ProjectIssues[0].Project, rig.tracker.ProjectIssues[1].Project = "zf", "other"
	rig.tracker.Issues = []tracker.Issue{{ID: "9", Subject: "Add lints", TrackerType: "fake", Project: "other"}}

	deps := rig.deps(issuepkg.IssueStartFlags{TrackerFirst: true})
	deps.Mirror = issuepkg.NewMirror(rig.cfg.IssueTracker, rig.tracker)
	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "9", Subject: "Add lints", TrackerType: "fake", Project: "other"}},
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	runErr := issueflow.RunIssueStart(ctx, deps, prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("RunIssueStart: %v", runErr)
		}
	})
	t.Run("the reconcile ran first and imported both projects", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 2 || !issuepkg.Qualified(records) {
			t.Errorf("records = %d, qualified = %v", len(records), issuepkg.Qualified(records))
		}
	})
	t.Run("the branch is named with the qualified slug", func(t *testing.T) {
		exists, err := rig.client.BranchExists("other-9@feat@add-lints")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the chain records the project and the tracker origin", func(t *testing.T) {
		st, err := branch.Load(ctx, rig.client, "other-9")
		if err != nil || st == nil || st.Project != "other" || st.TrackerType != "fake" {
			t.Errorf("state = %+v, %v", st, err)
		}
	})
	t.Run("the status update names the project and the bare number", func(t *testing.T) {
		want := []fake.Update{{Project: "other", IssueID: "9", StatusName: "In Progress"}}
		if !slices.Equal(rig.tracker.RecordedUpdates, want) {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}

// A project that already has a mirrored issue needs no import: the start
// does not touch the tracker listing.
func TestRunIssueStart_LivePickInAMirroredProjectDoesNotReconcile(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.cfg.IssueTracker.Projects = []config.TrackerProject{{NearSlug: "zf", FarSlug: "o/a"}, {NearSlug: "other", FarSlug: "o/b"}}
	rig.cfg.IssueTracker.Mirror = true
	rig.tracker.ProjectIssues = []tracker.Issue{fromTracker("1", "In zf", "open"), fromTracker("9", "Add lints", "open")}
	rig.tracker.ProjectIssues[0].Project, rig.tracker.ProjectIssues[1].Project = "zf", "other"
	rig.tracker.Issues = []tracker.Issue{{ID: "9", Subject: "Add lints", TrackerType: "fake", Project: "other"}}
	deps := rig.deps(issuepkg.IssueStartFlags{TrackerFirst: true})
	deps.Mirror = issuepkg.NewMirror(rig.cfg.IssueTracker, rig.tracker)
	if _, err := deps.Mirror.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rig.tracker.ListProjectCalls = 0

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "9", Subject: "Add lints", TrackerType: "fake", Project: "other"}},
		ConfirmBranch:    true,
	}
	runErr := issueflow.RunIssueStart(ctx, deps, prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("RunIssueStart: %v", runErr)
		}
	})
	t.Run("the tracker was not listed again", func(t *testing.T) {
		if rig.tracker.ListProjectCalls != 0 {
			t.Errorf("listings = %d, want 0", rig.tracker.ListProjectCalls)
		}
	})
	t.Run("the branch is still qualified", func(t *testing.T) {
		exists, err := rig.client.BranchExists("other-9@feat@add-lints")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
}
```

(`fromTracker` lives in `mirror_e2e_test.go` of the same package.) Add the imports the file lacks (`config`, `fake`, `slices`).

In `cmd/issue/start_record_e2e_test.go`, add after `TestRunIssueStart_ImportedRecordStartsAsTrackerIssue`:

```go
func TestRunIssueStart_ImportedRecordOfASecondProjectIsQualified(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.tracker.Issues = nil // empty list ⇒ NotifyTrackerError ⇒ repo picker
	zf, other := fromTracker("42", "In zf", "open"), fromTracker("42", "In other", "open")
	zf.Project, other.Project = "zf", "other"
	source := &fake.Tracker{ProjectIssues: []tracker.Issue{zf, other}}
	m := &issuepkg.Mirror{Tracker: source, Type: "fake", Projects: []string{"zf", "other"}}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	imported, _, err := issuepkg.Resolve(ctx, rig.client, "other-42")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromRepo:    &imported,
		IssueFromTracker: &issuepkg.Issue{Type: "fix"},
		ConfirmBranch:    true,
	}
	runErr := issueflow.RunIssueStart(ctx, rig.deps(issuepkg.IssueStartFlags{TrackerFirst: true}), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("RunIssueStart: %v", runErr)
		}
	})
	t.Run("the branch and its chain use the qualified slug", func(t *testing.T) {
		exists, err := rig.client.BranchExists("other-42@fix@in-other")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
		st, err := branch.Load(ctx, rig.client, "other-42")
		if err != nil || st == nil || st.Project != "other" || st.IssueID != imported.ID {
			t.Errorf("state = %+v, %v", st, err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "TestRunIssueStart_LivePick|TestRunIssueStart_ImportedRecordOfASecond" 2>&1 | tail -8`
Expected: FAIL (`deps.Mirror undefined`, or a branch named `9@feat@add-lints`).

- [ ] **Step 3: Implement**

`issue/issue.go`: add to `Issue`:

```go
	// Slug names the branch (refs/zf/branches/<Slug>, "<Slug>@<type>@…"):
	// the display ID of the issue, qualified by its project when the
	// repository's issues span several. "" means ID.
	Slug string
```

`cmd/issueflow/start.go`:

1. `StartDeps` gains `Mirror *issue.Mirror // nil when the mirror is off`. `BuildStartDeps` sets `deps.Mirror = issue.NewMirror(cfg.IssueTracker, t)` after building the tracker.
2. Add:

```go
// startRecords reads the repository's issues, fetched first, and returns
// them with the Qualified flag.
func startRecords(ctx context.Context, deps StartDeps) ([]issue.Record, bool) {
	c := deps.Client
	errW := c.IO().Err

	if _, err := issue.Fetch(ctx, c); err != nil {
		fmt.Fprintf(errW, "warning: could not fetch issues, using local data: %v\n", err)
	}

	records, warnings, err := issue.List(ctx, c)
	if err != nil {
		fmt.Fprintf(errW, "warning: list repo issues: %v\n", err)

		return nil, false
	}
	for _, w := range warnings {
		fmt.Fprintln(errW, w)
	}

	return records, issue.Qualified(records)
}

// importNewProject reconciles when the mirror covers several projects and no
// record is linked to project yet: the first start on a new project imports
// its issues, so the Qualified flag flips before the branch is named and
// every clone names the issue alike. It runs once per project, and never
// with one project or for a project that already has a mirrored issue. It
// returns the records, read again after the import.
func importNewProject(ctx context.Context, deps StartDeps, project string, records []issue.Record) []issue.Record {
	m := deps.Mirror
	if m == nil || len(m.Projects) < 2 || project == "" || slices.Contains(issue.LinkedProjects(records), project) {
		return records
	}

	c := deps.Client
	errW := c.IO().Err
	res, err := m.Reconcile(ctx, c)
	if err != nil {
		fmt.Fprintf(errW, "warning: tracker mirror: %v\n", err)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(errW, w)
	}

	again, _, err := issue.List(ctx, c)
	if err != nil {
		fmt.Fprintf(errW, "warning: list repo issues: %v\n", err)

		return records
	}

	return again
}
```

3. `pickIssue`: call `records, qualify := startRecords(ctx, deps)` first; `manual` becomes `getFromRepoOrUser(ctx, deps.Client, prompter, allowedBranchTypes, records, qualify)`. After `getFromTracker` returns, a live pick (a tracker issue with no record) gets its slug once the new-project import ran:

```go
	got, err := getFromTracker(ctx, prompter, deps.Tracker, allowedBranchTypes, manual)
	if err != nil {
		return nil, fmt.Errorf("issue from tracker: %w", err)
	}
	if got != nil && got.TrackerType != "" && got.RecordID == "" {
		records = importNewProject(ctx, deps, got.Project, records)
		got.Slug = issue.QualifiedID(got.Project, got.ID, issue.Qualified(records))
	}

	return got, nil
```

4. `getFromRepoOrUser(ctx, c, p, allowedTypes, records, qualify)`: remove its own fetch and list; the rest is unchanged.
5. `fromImportedRecord`: `src` gets `Project: rec.Tracker.Project`; after the pick, `got.Issue, got.RecordID, got.Slug = src, rec.ID, rec.DisplayID(qualify)` (add the `qualify bool` parameter and pass it from `getFromRepoOrUser`).
6. `getFromTracker` keeps its signature; `pickIssue` sets the slug (item 3).
7. `prepareBranch`: `branch.New(cmp.Or(picked.Slug, picked.ID), picked.Type, picked.Subject, deps.Flags.Variant)` (import `cmp`).
8. `createFlow`: the branch op gains `Project: picked.Project` inside the `trackerType != ""` case:

```go
	trackerType, project := "", ""
	if picked.TrackerType != "" {
		trackerType, project = deps.Cfg.IssueTracker.Type, picked.Project
	}
	op := &branch.Op{
		Branch: b.Name(), BranchType: b.Type(), Title: picked.Subject,
		Parent: deps.Flags.ParentIssueSlug, TrackerType: trackerType, Project: project, IssueID: picked.RecordID,
	}
```

and `ApplyTrackerStatus(ctx, deps.Tracker, deps.Client.IO().Err, picked.Project, picked.ID, deps.Cfg.IssueTracker.Type, prompter.PickTrackerStatus)`.

`cmd/issueflow/start_prompter.go`, `PickIssueFromTracker`: add `got.Project = pickedIssue.Project`.

- [ ] **Step 4: Run the suites**

Run: `mise exec -- go test ./cmd/issueflow/... ./cmd/issue/... ./cmd/branch/... 2>&1 | tail -20`
Expected: PASS. The existing start tests keep bare slugs (one project).

- [ ] **Step 5: Commit**

```bash
git add issue/issue.go cmd/issueflow/ cmd/issue/start_e2e_test.go cmd/issue/start_record_e2e_test.go
git commit -m "feat(issueflow): a branch of a second project gets a qualified slug and its chain the project"
```

---

### Task 10: Close, review and prune call the tracker with the project

**Files:**
- Modify: `cmd/issue/close.go:700-748`
- Modify: `cmd/review/tracker.go`
- Modify: `cmd/branch/prune_tracker.go:62-67,78-130,330-360`
- Test: `cmd/issue/close_mirror_e2e_test.go`, `cmd/review/tracker_e2e_test.go`, `cmd/branch/prune_tracker_test.go`

**Interfaces:**
- Consumes: `issueflow.TrackerRef`, `issueflow.LoadTrackerRef`, `ApplyTrackerStatus(…, project, issueID, …)` (Task 8); `branch.Row.Project` (Task 7).
- Produces: `runDiscoverTracker(ctx, w, pr, tr, refOf func(id string, row *branch.Row) (project, number string), trackedByName, base)`.

- [ ] **Step 1: Write the failing tests**

`cmd/issue/close_mirror_e2e_test.go`: every expected `fake.Update{IssueID: "ABC-1", …}` in the file gains `Project: "zf"` (the chain has no project; the imported record gives it). Add the `config`, `errors` and `strings` imports, then add:

```go
// The chain names the project: the status update uses it and strips the
// prefix from the slug.
func TestClose_Mirror_ChainProjectReachesTheTracker(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()
	mirrorOn(rig)
	rig.tracker.ClosingStatuses = []string{"Closed"}
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", TrackerType: "fake", Project: "other"})

	prompter := &scriptedPrompter{
		Branch: rig.pickedBranchRow(), Strategy: commitpkg.MergeStrategySquash, Confirm: true,
		Message: []byte("feat(thing): close\n"), TrackerStatus: "Closed", DeleteBranch: true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the update carries the chain's project", func(t *testing.T) {
		want := []fake.Update{{Project: "other", IssueID: "ABC-1", StatusName: "Closed"}}
		if !slices.Equal(rig.tracker.RecordedUpdates, want) {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}

// Review Focus 5: two projects, a branch that names none and has no record.
// The close completes; the tracker call is skipped with a warning.
func TestClose_Mirror_UnresolvableProjectIsAWarning(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()
	rig.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a"}, {NearSlug: "other", FarSlug: "b"}},
	}
	rig.tracker.StatusErr = errors.New("2 projects configured and none named")
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", TrackerType: "fake"})

	prompter := &scriptedPrompter{
		Branch: rig.pickedBranchRow(), Strategy: commitpkg.MergeStrategySquash, Confirm: true,
		Message: []byte("feat(thing): close\n"), TrackerStatus: "Closed", DeleteBranch: true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("the close completes", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("one warning names the failed update", func(t *testing.T) {
		if got := rig.stderr.String(); !strings.Contains(got, "warning: update tracker status") || !strings.Contains(got, "none named") {
			t.Errorf("stderr = %q", got)
		}
	})
}
```

The fake needs `StatusErr error // returned by UpdateIssueStatus when non-nil` (add it in `tracker/fake/fake.go`, checked first in `UpdateIssueStatus`): the fake accepts any project, so the refusal a real adapter gives is injected.

`cmd/review/tracker_e2e_test.go`: add after `assertOneUpdate`:

```go
func TestReviewTracker_ChainProjectReachesTheTracker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)
	fakeT := withFakeTracker(t, rig)
	branchtest.Amend(t, rig.client, branch.Op{Branch: "77@feat@my-feature", TrackerType: "fake", Project: "zf"})

	picked := inProgressBranchRow(t, rig, "77")
	p := &scriptedReviewPrompter{Branch: picked, TrackerStatus: "In Progress"}
	if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
		t.Fatalf("review request: %v", err)
	}

	t.Run("the update names the chain's project", func(t *testing.T) {
		want := fake.Update{Project: "zf", IssueID: "77", StatusName: "In Progress"}
		if len(fakeT.RecordedUpdates) != 1 || fakeT.RecordedUpdates[0] != want {
			t.Errorf("updates = %+v", fakeT.RecordedUpdates)
		}
	})
}
```

`cmd/branch/prune_tracker_test.go`: `fakeIssueResolver` records what it is asked:

```go
type fakeIssueResolver struct {
	closed  map[string]bool
	errs    map[string]error
	unknown map[string]bool
	asked   []string // "<project>/<id>" per call
}

func (f *fakeIssueResolver) IsIssueClosed(_ context.Context, project, id string) (bool, error) {
	f.asked = append(f.asked, project+"/"+id)
	…
```

Every `runDiscoverTracker(…)` call in the file gains `bareRef` after `tr`, with `func bareRef(id string, _ *branch.Row) (string, string) { return "", id }` defined in the test file. Add to `TestRunDiscoverTracker`:

```go
	t.Run("a qualified slug reaches the resolver as project and number", func(t *testing.T) {
		pr := &fakeTrackerPruner{base: "master", locals: []string{"master", "other-42@feat@x"}}
		tr := &fakeIssueResolver{closed: map[string]bool{"42": true}}
		rows := map[string]*branch.Row{"other-42@feat@x": {IssueSlug: "other-42", BranchName: "other-42@feat@x", Project: "other"}}
		refOf := func(id string, row *branch.Row) (string, string) {
			return issueflow.TrackerRef(row.Project, id, nil)
		}

		result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, refOf, rows, "master")
		if err != nil || len(result.Candidates) != 1 || result.Candidates[0].IssueID != "other-42" {
			t.Fatalf("result = %+v, %v", result, err)
		}
		if !slices.Equal(tr.asked, []string{"other/42"}) {
			t.Errorf("asked = %v", tr.asked)
		}
	})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "TestClose_Mirror" ./cmd/review/... -run "ChainProject" ./cmd/branch/... -run "TestRunDiscoverTracker" 2>&1 | tail -12`
Expected: FAIL (wrong `Project` in the updates, `too many arguments to runDiscoverTracker`).

- [ ] **Step 3: Implement**

`cmd/issue/close.go`, `updateClosedStatus`:

```go
	if tracked {
		project, number := issueflow.LoadTrackerRef(ctx, deps.client, picked.IssueSlug)
		issueflow.ApplyTrackerStatus(
			ctx, deps.tracker, deps.client.IO().Err, project, number,
			deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
	}
```

`cmd/review/tracker.go`:

```go
func applyTrackerStatus(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, issueSlug string) {
	project, number := issueflow.LoadTrackerRef(ctx, deps.client, issueSlug)
	issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err,
		project, number, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
}

func addTrackerComment(ctx context.Context, deps reviewDeps, issueSlug string, round int, reason string) {
	body := fmt.Sprintf("Changes requested (review round %d):\n\n%s", round, reason)
	project, number := issueflow.LoadTrackerRef(ctx, deps.client, issueSlug)
	if err := deps.tracker.AddComment(ctx, project, number, body); err != nil {
```

`cmd/branch/prune_tracker.go`: `runDiscoverTracker` gains `refOf func(id string, row *branch.Row) (project, number string)` after `tr`; in the loop, `project, number := refOf(id, trackedByName[name])` and `tr.IsIssueClosed(ctx, project, number)`; the candidate keeps `IssueID: id` (the slug, for display). In `runPruneTracker`, after `trackedByName` is built:

```go
	// One listing for the run: the records answer for the branches whose
	// chain predates the project field.
	if _, err := issue.Fetch(ctx, client); err != nil && !errors.Is(err, git.ErrForeignChain) {
		fmt.Fprintf(client.IO().Err, "warning: could not fetch issues, using local data: %v\n", err)
	}
	records, _, err := issue.List(ctx, client)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: list issues: %v\n", err)
	}
	refOf := func(id string, row *branch.Row) (string, string) {
		chainProject := ""
		if row != nil {
			chainProject = row.Project
		}

		return issueflow.TrackerRef(chainProject, id, records)
	}
	result, err := runDiscoverTracker(ctx, w, pr, tr, refOf, trackedByName, base)
```

Import `issue` and `issueflow` in that file. Remove the three ponytail comments left by Task 2.

- [ ] **Step 4: Run the suites**

Run: `mise exec -- go test ./cmd/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_mirror_e2e_test.go cmd/review/tracker.go cmd/review/tracker_e2e_test.go cmd/branch/prune_tracker.go cmd/branch/prune_tracker_test.go tracker/fake/fake.go
git commit -m "feat: close, review and prune name the tracker project of a branch"
```

---

### Task 11: `issue new` picks a project; `issue show` prints it

**Files:**
- Modify: `cmd/issue/new.go`, `cmd/issue/record.go:22-50`, `cmd/issue/show.go:68-80`
- Modify: `tui/issue_record.go:38-57`
- Test: `cmd/issue/record_e2e_test.go` (`scriptedRecordPrompter`), `cmd/issue/mirror_e2e_test.go`

**Interfaces:**
- Consumes: `config.IssueTrackerConfig.DefaultProject`, `Project(near)` (Task 1); `NewIssue.Project` (Task 4); `Mirror.Projects` (Task 6).
- Produces: `recordPrompter.NewIssue(ctx, allowedTypes, projects []string, in *issuepkg.NewIssue) error` (`projects` empty: no select); `tui.IssueNewForm(title, branchType, description, labels, project *string, allowedBranchTypes, projects []string)`; the `--project` flag.

- [ ] **Step 1: Write the failing tests**

`cmd/issue/record_e2e_test.go`: `scriptedRecordPrompter` gains `ProjectsOffered []string` and its `NewIssue(_ context.Context, _ []string, projects []string, in *issuepkg.NewIssue) error` stores `s.ProjectsOffered = projects` before copying `s.New`.

Add to `cmd/issue/mirror_e2e_test.go`:

```go
func newTwoProjectMirrorRig(t *testing.T) (*recordRig, *fake.Tracker, *issuepkg.Mirror) {
	t.Helper()

	rig, ft, _ := newMirrorRig(t)
	rig.cfg.IssueTracker.Projects = append(rig.cfg.IssueTracker.Projects, config.TrackerProject{NearSlug: "other", FarSlug: "piprim/other"})

	return rig, ft, mirrorOf(rig.cfg, ft)
}

func TestMirror_NewWithAProject(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("--project exports to that project", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newTwoProjectMirrorRig(t)
		err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Elsewhere", Project: "other"}, nil, m)
		rec := rig.onlyRecord(t)
		if err != nil || rec.Project != "other" || len(ft.RecordedCreates) != 1 || ft.RecordedCreates[0].Project != "other" {
			t.Errorf("err = %v, record = %+v, creates = %+v", err, rec, ft.RecordedCreates)
		}
	})
	t.Run("no project goes to the first configured one", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newTwoProjectMirrorRig(t)
		err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Default"}, nil, m)
		if err != nil || rig.onlyRecord(t).Project != "zf" || ft.RecordedCreates[0].Project != "zf" {
			t.Errorf("err = %v, record = %+v, creates = %+v", err, rig.onlyRecord(t), ft.RecordedCreates)
		}
	})
	t.Run("the form offers the projects with the mirror on and several configured", func(t *testing.T) {
		t.Parallel()

		rig, _, m := newTwoProjectMirrorRig(t)
		p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form", Project: "other"}}
		if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{}, p, m); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if !slices.Equal(p.ProjectsOffered, []string{"zf", "other"}) || rig.onlyRecord(t).Project != "other" {
			t.Errorf("offered = %v, record = %+v", p.ProjectsOffered, rig.onlyRecord(t))
		}
	})
	t.Run("the form offers no project with one configured", func(t *testing.T) {
		t.Parallel()

		rig, _, m := newMirrorRig(t)
		p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form"}}
		if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{}, p, m); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if len(p.ProjectsOffered) != 0 {
			t.Errorf("offered = %v, want none", p.ProjectsOffered)
		}
	})
	t.Run("an unknown project is refused naming the configured ones", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newTwoProjectMirrorRig(t)
		err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Nope", Project: "nope"}, nil, m)
		if err == nil || !strings.Contains(err.Error(), "zf, other") || len(ft.RecordedCreates) != 0 {
			t.Errorf("err = %v, creates = %+v", err, ft.RecordedCreates)
		}
	})
	t.Run("Review Focus 4: with the mirror off the project is stored and nothing is exported", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		rig.cfg.IssueTracker.Projects = []config.TrackerProject{{NearSlug: "zf", FarSlug: "a"}, {NearSlug: "other", FarSlug: "b"}}
		err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Off", Project: "other"}, nil, nil)
		if err != nil || rig.onlyRecord(t).Project != "other" {
			t.Errorf("err = %v, record = %+v", err, rig.onlyRecord(t))
		}
	})
}

func TestMirror_ShowPrintsTheProject(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local", Project: "other"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rig.stdout.Reset()

	err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, nil, false)

	t.Run("an unlinked record shows its project", func(t *testing.T) {
		if err != nil || !strings.Contains(rig.stdout.String(), "Project: other\n") {
			t.Errorf("stdout = %q, err = %v", rig.stdout.String(), err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "TestMirror_NewWithAProject|TestMirror_ShowPrintsTheProject" 2>&1 | tail -8`
Expected: FAIL (`ProjectsOffered` unknown, or the record's project empty).

- [ ] **Step 3: Implement**

`tui/issue_record.go`:

```go
// IssueNewForm is the form of `issue new`. labels is one comma-separated
// line. projects, when it has several entries, adds a select of the tracker
// project the issue is exported to; project then receives the pick.
func IssueNewForm(title, branchType, description, labels, project *string, allowedBranchTypes, projects []string) *huh.Group {
	fields := []huh.Field{
		huh.NewInput().Title("Title:").Placeholder("Short description of the issue").Validate(requiredText).Value(title),
		huh.NewSelect[string]().Title("Type:").Options(branchTypeOptions(allowedBranchTypes)...).Value(branchType),
	}
	if len(projects) > 1 {
		opts := make([]huh.Option[string], len(projects))
		for i, p := range projects {
			opts[i] = huh.NewOption(p, p)
		}
		fields = append(fields, huh.NewSelect[string]().Title("Project:").Options(opts...).Value(project))
	}
	fields = append(fields,
		huh.NewText().Title("Description:").Value(description),
		huh.NewInput().Title("Labels (comma-separated, optional):").Value(labels),
	)

	return huh.NewGroup(fields...)
}
```

`cmd/issue/record.go`: `recordPrompter.NewIssue(ctx context.Context, allowedTypes, projects []string, in *issuepkg.NewIssue) error`; the huh implementation passes `&in.Project` and `projects` to the form (the select preselects `in.Project`, which `runNew` sets to the default before opening the form).

`cmd/issue/new.go`:
- the flag: `f.StringVar(&in.Project, "project", "", "tracker project to export the issue to, by near slug (default: the first configured)")`, and `"project"` in the `passed` list.
- `runNew`, before the form:

```go
	tc := cfg.IssueTracker
	in.Project = cmp.Or(in.Project, tc.DefaultProject())

	var projects []string
	if tc.Mirror && len(tc.Projects) > 1 {
		for _, p := range tc.Projects {
			projects = append(projects, p.NearSlug)
		}
	}
	if p != nil {
		if err := p.NewIssue(ctx, types, projects, &in); err != nil {
```

- after the title check, the validation:

```go
	if in.Project != "" {
		if _, ok := tc.Project(in.Project); !ok {
			names := make([]string, len(tc.Projects))
			for i, p := range tc.Projects {
				names[i] = p.NearSlug
			}

			return fmt.Errorf("unknown project %q (want one of: %s)", in.Project, strings.Join(names, ", "))
		}
	}
```

`cmd/issue/show.go`, `renderRecord`: before the `Tracker:` line, `if rec.Tracker == nil && rec.Project != "" { fmt.Fprintf(w, "Project: %s\n", rec.Project) }`.

- [ ] **Step 4: Run the suites**

Run: `mise exec -- go test ./cmd/issue/... ./tui/... 2>&1 | tail -10`
Expected: PASS. `TestRunNew_Form` and the scripted prompter compile with the new parameter.

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/new.go cmd/issue/record.go cmd/issue/show.go cmd/issue/record_e2e_test.go cmd/issue/mirror_e2e_test.go tui/issue_record.go
git commit -m "feat(issue): issue new picks the tracker project, issue show prints it"
```

---

### Task 12: Documentation

**Files:**
- Modify: `docs/issue-refs.md:23-50,87-140`, `README.md:380-402`, `CLAUDE.md` (section "Testing the tracker mirror"), `ROADMAP.md:5-9`

- [ ] **Step 1: `docs/issue-refs.md`**

In the ops table, the `create` row reads: `` `title`, `description`, `branch_type`, `project` (near slug of the target tracker project, "" for the default); for an issue imported from the tracker also `tracker_type`, `tracker_id` ``. Replace the first paragraph of "Tracker mirror" with: "With `mirror = true` under `[issue-tracker]` and one or more `[[issue-tracker.projects]]` entries, the issues of those tracker projects and the issues of the repository are one set:". Add after "Identity of an imported issue":

```markdown
### Several projects

Each configured project is listed and imported on its own; two projects can
share an issue number, and the near slug in the root keeps the two issues
apart. A repo-born issue is exported to the project its `create` op names,
or to the first configured project when it names none. An issue whose project
is not configured on this clone is not exported, with a warning.
```

Replace "Display" with:

```markdown
### Display

An imported issue is shown, and names its branches, by its tracker number.
Once the repository's mirrored issues span more than one project, every
imported issue is qualified by its project: `other-42`. That flag is read
from the issue refs, never from the clone's config, so every clone switches
at the same moment, the first reconcile that mirrors an issue of a second
project, and never switches back. A branch started before that moment keeps
its bare slug: `issue list` joins it to its record through the record ID on
its chain, or by its bare number when one record has it. A repo-born issue
keeps its short hash; `issue list` shows `a1b2c3d (#57)` once it is
exported. `issue show 57` finds either; a number shared by two projects is
refused with both qualified IDs.

The branch chain records the project of a tracker-born branch (`project` on
the start op). The close status picker, the review comment and `branch
prune-tracker` take the project from the chain, else from the record the
slug names, else let the adapter use the only configured project.
```

Add to "Limits": "- A second branch on an issue is checked by exact name, so `42@feat@x` from before a second project and `zf-42@feat@x` after can coexist on one issue."

- [ ] **Step 2: `README.md`**

In the config example, the comment on `mirror` becomes `# true: mirror the projects' issues with the repository`, and add a second `[[issue-tracker.projects]]` table (`near-slug = "other"`, `far-slug = "owner/other"`) with the comment `# the first entry is the default project`. In the table: the `projects` row drops "**Exactly one entry is required** to update issue status on GitHub/Forgejo/Gitea, whose issue endpoints are scoped to one repository." and gains "The first entry is the default project: where `issue new` exports an issue when `--project` was not given, and the one a branch started before projects were recorded belongs to. Do not reorder entries once issues were exported." The `mirror` row reads: "`true` mirrors the projects' issues with the issues stored in the repository. Needs a tracker type and at least one project. With several projects, imported issues are named `<near-slug>-<number>` (`other-42`) in branches and listings once the repository holds issues of two projects. See `docs/issue-refs.md`." Under the `issue new` description (search `**\`issue new\`**`), add: "`--project <near-slug>` picks the tracker project the issue is exported to; the form asks when several are configured."

- [ ] **Step 3: `CLAUDE.md`**

In "Testing the tracker mirror", after the bullet list, add:

```markdown
With several projects, the mirror lists each one (`Mirror.Projects`, config
order, the first being the default) and display IDs are qualified
(`other-42`) once the linked records span two projects
(`issue.Qualified`). The two-project cases live in `TestReconcile_TwoProjects`,
`TestMirror_NewWithAProject`, `TestRunIssueStart_LivePickInASecondProjectIsQualified`
and `TestClose_Mirror_ChainProjectReachesTheTracker`. A branch is turned into
a tracker project and number by `issueflow.TrackerRef` (`cmd/issueflow/trackerref_test.go`):
the chain's project, else the record whose display ID is the slug, else the
slug alone. Seed a fake issue with its `Project`: the fake lists per project.

    mise exec -- go test ./issue/... -run "TestReconcile_TwoProjects|TestQualified|TestFindByDisplayID" -v
    mise exec -- go test ./cmd/issueflow/... -run "TrackerRef" -v
```

- [ ] **Step 4: `ROADMAP.md`**

Replace item 1.a with: `a. ~~Several projects~~ — shipped. Left out: a project picker in the \`issue start\` form (the default project is used); the "second branch on the same issue" check compares exact names, so a bare and a qualified branch can coexist on one issue.`

- [ ] **Step 5: Run everything once more and commit**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./... 2>&1 | tail -15`
Expected: PASS.

```bash
git add docs/issue-refs.md README.md CLAUDE.md ROADMAP.md
git commit -m "docs: the issue mirror over several projects"
```
