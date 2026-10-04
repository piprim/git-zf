# GitHub Tracker Adapter Design

> **For agentic workers:** implement this spec via `superpowers:writing-plans` → `superpowers:subagent-driven-development`.

**Goal:** Add a GitHub adapter to the existing `tracker.Tracker` registry so `git zf issue *` commands work against GitHub repositories. Extend `IssueTrackerConfig` with an optional `Projects []string` filter and add a runtime project picker to the issues-list TUI. Both adapters (Redmine and GitHub) honour the new filter symmetrically.

---

## Architecture

```
config.IssueTrackerConfig{Type: "github", URL, Token, Projects}
       │
       ▼
tracker.New(cfg) ──► github.New(cfg) ──► githubAdapter{client: *github.Client, projects: set}
                                                │
                                                ▼
                                  client.Issues.ListByAuthenticatedUser(...)
                                                │
                                                ▼
                                  client-side filter on Projects
                                                │
                                                ▼
                                       []tracker.Issue
                                                │
                                                ▼
                          cmd/issue/list.go: TUI ─► IssueProjectFilter(...)
                                                │
                                                ▼
                                       filtered table render
```

Auth and HTTP are delegated to `github.com/google/go-github/v85`. The adapter's only direct responsibility is mapping the typed `*github.Issue` to `tracker.Issue` and applying the project filter.

---

## Files changed

| File | Change |
|------|--------|
| `config/config.go` | `IssueTrackerConfig.Projects []string` field |
| `config/config_test.go` | round-trip JSON unmarshal test for `Projects` |
| `tracker/tracker.go` | `Issue.Project string` field |
| `tracker/redmine/redmine.go` | populate `Issue.Project`; apply client-side `Projects` filter |
| `tracker/redmine/redmine_test.go` | `TestListIssues_projectsFilter` |
| `tracker/github/init.go` (new) | `tracker.Register("github", New)` |
| `tracker/github/github.go` (new) | `githubAdapter` implementing `Tracker` |
| `tracker/github/github_test.go` (new) | adapter tests via `httptest` |
| `tui/issue.go` (or new `tui/issue_project_filter.go`) | `IssueProjectFilter` |
| `tui/issue_test.go` | new tests for `IssueProjectFilter` |
| `cmd/issue/list.go` | wire `IssueProjectFilter`; show/hide project column |
| `cmd/issue/list_test.go` | filter wiring + column-toggle tests |
| `go.mod` / `go.sum` | add `github.com/google/go-github/v85` |

---

## Config

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

**Filter semantics** — uniform across adapters:
- `Projects` empty/nil → adapter lists all assigned issues (existing behaviour for Redmine; GitHub's "assigned across all accessible repos").
- `Projects` non-empty → adapter calls the same cross-project endpoint, then filters client-side keeping only issues whose `Project` is in the configured set.

**Per-tracker meaning of a `Projects` entry**:
- Redmine: project slug or numeric ID (matches `Issue.Project`, which the adapter populates from the API response's `project.identifier`).
- GitHub: `"owner/repo"` matching `Issue.Project` (populated from `repository.full_name`).

Backwards compatibility: an unset `Projects` keeps existing Redmine behaviour. Existing JSON configs continue to load with the new field defaulted to `nil`.

---

## Tracker contract additions

```go
type Issue struct {
    TrackerType string
    ID          string
    Subject     string
    Description string
    Status      string
    Project     string // NEW. Redmine: project slug. GitHub: "owner/repo".
}
```

The `Tracker` interface is unchanged; existing methods keep their signatures.

---

## GitHub adapter

### Package layout

`tracker/github/{init.go, github.go, github_test.go}`. `init.go`:

```go
package github

import "github.com/piprim/git-zf/tracker"

//nolint:gochecknoinits // Register pattern needs it
func init() {
    tracker.Register(trackerType, New)
}
```

The constant `trackerType = "github"` lives in `github.go`.

### Construction

```go
type githubAdapter struct {
    client   *github.Client
    cfg      config.IssueTrackerConfig
    projects map[string]struct{} // lookup set built from cfg.Projects
}

const trackerType = "github"

func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
    if cfg.Token == "" {
        return nil, errors.New("github: Token is required")
    }

    c := github.NewClient(nil).WithAuthToken(cfg.Token)
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
```

### `ListIssues`

Uses `client.Issues.ListByAuthenticatedUser` (the cross-repo "assigned" endpoint). Paginates via `*Response.NextPage`. Drops PRs (`iss.IsPullRequest()`). Applies the client-side `Projects` filter when configured.

```go
func (a *githubAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) {
    opt := &github.IssueListOptions{
        Filter:      "assigned",
        State:       "open",
        ListOptions: github.ListOptions{PerPage: 100},
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

### `ListStatuses`

GitHub issues have only two states (`open`, `closed`). No API call needed.

```go
func (a *githubAdapter) ListStatuses(_ context.Context) ([]string, error) {
    return []string{statusOpen, statusClosed}, nil
}

const (
    statusOpen   = "open"
    statusClosed = "closed"
)
```

### `UpdateIssueStatus`

GitHub does not have a project-agnostic update endpoint — `PATCH /repos/{owner}/{repo}/issues/{number}` requires the repo. To keep the contract simple, the adapter requires **exactly one** entry in `cfg.Projects` and uses it as the target repo.

```go
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

    _, _, err = a.client.Issues.Edit(ctx, owner, repo, n, &github.IssueRequest{State: github.Ptr(state)})
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

`state_reason` (`completed` / `not_planned` / `reopened`) is left to GitHub's default. Adding fine-grained reason support is out of scope for v1.

---

## Redmine adapter changes

The existing `redmineAdapter` is extended (not rewritten):

1. Add a `Project` substruct to the existing `issue` JSON struct in `tracker/redmine/redmine.go`:

   ```go
   type project struct {
       ID         int    `json:"id"`
       Identifier string `json:"identifier"`
   }

   type issue struct {
       // ...existing fields
       Project *project `json:"project"`
   }
   ```

2. In `ListIssues`, populate `tracker.Issue.Project` as: `iss.Project.Identifier` if non-empty, otherwise `strconv.Itoa(iss.Project.ID)`, otherwise `""` (when the API omits the project entirely).
3. Apply a client-side `Projects` filter symmetric to the GitHub adapter (drop issues whose `Project` is not in `cfg.Projects` when the slice is non-empty). The match is exact-string against the populated `tracker.Issue.Project`, so users who configure numeric IDs match issues without a slug, and vice versa.

No change to the request URL — keep the cross-project listing as today.

---

## TUI: project filter

### `IssueProjectFilter`

Lives in `tui/` next to `IssueStatusFilter`. Same shape: a single-select picker that returns the chosen value or empty string for "all".

```go
// IssueProjectFilter prompts the user to pick a project to filter the issue
// list by. projects is the deduplicated, sorted list of project names found
// in the loaded issues. current is the previously selected value (empty for
// first prompt or for "all"). Returns the selected project, or "" for "all".
//
// When len(projects) <= 1, no prompt is shown and "" is returned.
func IssueProjectFilter(projects []string, current string) (string, error)
```

Implementation: `huh.NewSelect[string]()` with `["all"] + projects` as options, default value = `current`. The "all" option maps to the empty string. Returns early with `""` when there's nothing meaningful to filter.

### Wiring in `cmd/issue/list.go`

After `tracker.ListIssues` returns:

1. Build `projectsInResult := sortedUniqueProjects(issues)`.
2. In TUI mode (no `--json`, no `--stdout`), call `IssueProjectFilter(projectsInResult, "")`. The skip-on-single-project rule is enforced inside the filter function.
3. If a non-empty project is returned, drop issues whose `Project != selection`.
4. Render the (possibly-filtered) table.

`--json` and `--stdout` paths are not affected — they always emit the unfiltered list.

### Issue table column

`renderIssueTable` gains a `Project` column, placed immediately after the `ID` column (so the natural reading order is "project, then id, then subject, then status, ..."). The column is rendered only when `projectsInResult` contains more than one distinct project — single-project views stay compact.

---

## Error handling

| Situation | Behaviour |
|-----------|-----------|
| `cfg.Token` empty | `New` returns `errors.New("github: Token is required")` |
| `cfg.URL` non-empty, non-default, invalid for `WithEnterpriseURLs` | wrapped error from go-github |
| `Issues.ListByAuthenticatedUser` returns non-2xx (rate limit, auth) | wrapped error: `"github: list issues: <go-github error>"` |
| `cfg.Projects` empty AND `UpdateIssueStatus` called | error: `"github: UpdateIssueStatus requires exactly one project configured"` |
| `cfg.Projects` has 2+ entries AND `UpdateIssueStatus` called | same error as above |
| `cfg.Projects[0]` not in `owner/repo` form | error: `"github: invalid project %q (expected owner/repo)"` |
| `statusName` is neither `"open"` nor `"closed"` | error: `"github: unknown status %q (want \"open\" or \"closed\")"` |
| `issueID` not parseable as int | wrapped error: `"github: invalid issue id %q: <strconv error>"` |
| `Issues.Edit` returns error | wrapped error: `"github: edit issue %d: <go-github error>"` |

PR responses returned by the issues endpoint are silently filtered out (`iss.IsPullRequest()`).

---

## Testing

### `tracker/github/github_test.go`

Internal test package (`package github`) so the adapter's unexported `client` field is accessible. Tests use `httptest.NewServer` and override `adapter.client.BaseURL` (a `*url.URL` in go-github) directly:

```go
srv := httptest.NewServer(mux)
defer srv.Close()

a, err := New(config.IssueTrackerConfig{Token: "test"})
// ...
ga := a.(*githubAdapter)
u, _ := url.Parse(srv.URL + "/")
ga.client.BaseURL = u
```

Internal test access avoids the URL-validation constraints of `WithEnterpriseURLs`, which expects a fully-formed enterprise base URL.

| Test | Scenario |
|------|----------|
| `TestNew_missingToken` | `cfg.Token=""` → constructor returns error |
| `TestListIssues_success` | server returns 1 issue + 1 PR (`pull_request != nil`); expect 1 `tracker.Issue`, PR filtered, fields populated |
| `TestListIssues_pagination` | server returns 2 pages via `Link: <...>; rel="next"`; expect concatenated result |
| `TestListIssues_projectFilter` | `cfg.Projects=["a/b"]`; server returns issues from `a/b` and `c/d`; expect only the `a/b` issue |
| `TestListStatuses` | no HTTP call; expect `["open", "closed"]` |
| `TestUpdateIssueStatus_close` | `cfg.Projects=["a/b"]`, `statusName="closed"`; expect PATCH `/repos/a/b/issues/42` body `{"state":"closed"}` |
| `TestUpdateIssueStatus_open` | symmetric; body `{"state":"open"}` |
| `TestUpdateIssueStatus_unknownStatus` | `statusName="WIP"` → returns error; no HTTP call (verify via test server hit counter) |
| `TestUpdateIssueStatus_zeroProjects` | `cfg.Projects=[]` → returns error; no HTTP call |
| `TestUpdateIssueStatus_multipleProjects` | `cfg.Projects=["a/b","c/d"]` → returns error; no HTTP call |
| `TestUpdateIssueStatus_invalidProject` | `cfg.Projects=["onlyone"]` → returns error before HTTP |
| `TestUpdateIssueStatus_invalidIssueID` | `issueID="not-an-int"` → returns error; no HTTP call |

Each test sets `client.BaseURL = srv.URL + "/"` after `New` to redirect API calls to the test server. Tests run with `t.Parallel()`.

### `tracker/redmine/redmine_test.go`

Add `TestListIssues_projectsFilter`: `cfg.Projects=["foo"]`; server returns 2 issues (one in project "foo", one in "bar"); expect only the "foo" issue.

Add `TestListIssues_populatesProject`: server returns 1 issue with `project.identifier="myproj"`; expect `tracker.Issue.Project == "myproj"`.

### `tui/issue_test.go`

- `TestIssueProjectFilter_skipsWhenSingleProject` — `projects=["a/b"]`, `current=""` → returns `""` without invoking the form.
- `TestIssueProjectFilter_skipsWhenEmpty` — `projects=[]` → returns `""`.
- `TestIssueProjectFilter_preservesSelected` — pass `current="a/b"`; verify the picker is initialised with that value (mirrors the existing `IssueStatusFilter_preservesSelected` test).

### `cmd/issue/list_test.go`

Extend the existing fake-tracker tests:

- `TestRunIssueList_filtersByProject` — fake tracker returns issues from two projects; the wired `IssueProjectFilter` is replaced with a stub returning a fixed selection; expect the rendered table contains only the selected project's issues.
- `TestRenderIssueTable_hidesProjectColumnWhenSingle` — all rows share one project → column not present.
- `TestRenderIssueTable_showsProjectColumnWhenMultiple` — rows span multiple projects → column present.

### `config/config_test.go`

Add `TestLoad_projects`: a config blob with `"projects": ["a/b","c/d"]` round-trips to `cfg.IssueTracker.Projects == ["a/b","c/d"]`.

---

## Out of scope (deferred)

- GitHub state_reason support (`completed` / `not_planned` / `reopened`).
- GitHub Projects v2 (kanban boards) integration.
- GitHub App / OAuth flow — PAT only.
- Auto-detecting `Projects` from the local git remote.
- Multi-tracker config (one tracker per config remains the constraint).
- Rate-limit handling beyond go-github's defaults.
- Migration tooling for existing Redmine configs (the `Projects` field is purely additive and defaults to nil).
