# Tracker Integration Design (Step 2b)

## Goal

Add pluggable issue-tracker support to `git cz issue start` and `git cz branch new`. The first adapter targets Redmine. When a tracker is configured the user can pick an issue from a live list instead of typing IDs manually, and optionally update the issue status to "In Progress" after branch creation.

## Architecture

Interface + registry pattern. The `tracker/` package defines the contract; adapters self-register via `init()`. No build tags — all adapters are compiled in by default and are dormant until `tracker.type` appears in `.git-zf.json`.

```
tracker/
  tracker.go          — Tracker interface, Issue, TrackerConfig, registry (Register / New)
  tracker_test.go     — registry unit tests
  redmine/
    redmine.go        — Redmine adapter wrapping github.com/mattn/go-redmine
    init.go           — func init() { tracker.Register("redmine", New) }
    redmine_test.go   — httptest fake-server tests

store/
  migrations/0002_add_tracker_type.sql
  store.go            — Issue gains TrackerType *string; InsertIssueWithBranch updated

tui/
  issue.go            — 4 new huh groups (toggle, picker, error note, status confirm)

cmd/
  issue.go            — extended issueStartRunE; loads tracker config via Viper
```

---

## Section 1: `tracker/` package

### Interface and models

```go
// tracker/tracker.go

type Issue struct {
    TrackerType string // "redmine", "plane", … — set by the adapter
    ID          string // matches store.Issue.IDSlug
    Subject     string
    Description string
    Status      string // human-readable: "New", "In Progress", …
}

type TrackerConfig struct {
    Type             string // "redmine"
    URL              string // base URL, no trailing slash
    Token            string // API key / personal access token
    InProgressStatus string // status name to set on start (default: "In Progress")
}

type Tracker interface {
    ListIssues(ctx context.Context) ([]Issue, error)
    // UpdateIssueStatus resolves statusName to the tracker's internal ID and
    // applies the update. Implementation is adapter-specific.
    UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
}
```

### Registry

```go
var registry = map[string]func(TrackerConfig) (Tracker, error){}

func Register(name string, fn func(TrackerConfig) (Tracker, error)) {
    registry[name] = fn
}

func New(cfg TrackerConfig) (Tracker, error) {
    fn, ok := registry[cfg.Type]
    if !ok {
        return nil, fmt.Errorf("unknown tracker type %q — is the adapter registered?", cfg.Type)
    }
    return fn(cfg)
}
```

---

## Section 2: Redmine adapter

Wraps `github.com/mattn/go-redmine`. The adapter fetches issues assigned to the authenticated user with an open status.

```go
// tracker/redmine/redmine.go

type redmineAdapter struct {
    client *redminelib.Client
    cfg    tracker.TrackerConfig
}

func New(cfg tracker.TrackerConfig) (tracker.Tracker, error) {
    c := redminelib.NewClient(cfg.URL, cfg.Token)
    return &redmineAdapter{client: c, cfg: cfg}, nil
}

// ListIssues fetches open issues assigned to the current user.
// Equivalent to GET /issues.json?assigned_to_id=me&status_id=open&limit=100
func (a *redmineAdapter) ListIssues(ctx context.Context) ([]tracker.Issue, error) { … }

// UpdateIssueStatus resolves statusName via GET /issue_statuses.json,
// then PUTs the matching status_id onto the issue.
func (a *redmineAdapter) UpdateIssueStatus(ctx context.Context, issueID, statusName string) error { … }
```

```go
// tracker/redmine/init.go
func init() { tracker.Register("redmine", New) }
```

`InProgressStatus` in `TrackerConfig` defaults to `"In Progress"` in `issueStartRunE` when the config field is empty.

Status ID resolution: `GET /issue_statuses.json` returns the full list; the adapter finds the entry whose `name` matches `statusName` (case-insensitive). If no match: return a descriptive error so the user can correct `in-progress-status` in their config.

---

## Section 3: Store migration

**`store/migrations/0002_add_tracker_type.sql`**

```sql
ALTER TABLE issues ADD COLUMN tracker_type TEXT DEFAULT NULL;
```

`NULL` = manually entered issue (no tracker). Non-null = tracker type string (e.g. `"redmine"`).

`store.Issue` updated:

```go
type Issue struct {
    ID          int64
    IDSlug      string
    Title       string
    StatusID    int64
    TrackerType *string // nil for manual; &"redmine" for tracker-sourced
}
```

`InsertIssueWithBranch` passes `TrackerType` through:

```sql
INSERT INTO issues (id_slug, title, status_id, tracker_type) VALUES (?, ?, ?, ?)
```

---

## Section 4: Config extension

`.git-zf.json` gains an optional `tracker` block:

```json
{
  "tracker": {
    "type": "redmine",
    "url": "https://redmine.example.com",
    "token": "abc123",
    "in-progress-status": "In Progress"
  }
}
```

`in-progress-status` is optional — defaults to `"In Progress"`. Loaded via Viper:

```go
cfg := tracker.TrackerConfig{
    Type:             viper.GetString("tracker.type"),
    URL:              viper.GetString("tracker.url"),
    Token:            viper.GetString("tracker.token"),
    InProgressStatus: viper.GetString("tracker.in-progress-status"),
}
if cfg.InProgressStatus == "" {
    cfg.InProgressStatus = "In Progress"
}
```

No tracker block (or empty `type`) → tracker feature disabled, manual flow only, no TUI changes.

---

## Section 5: TUI flow

### New huh groups in `tui/issue.go`

```go
// IssueTrackerToggle asks whether to fetch from the tracker.
// defaultFetch=true for issue start (tracker-first); false for branch new (manual-first).
func IssueTrackerToggle(useTracker *bool, trackerFirst bool, trackerType string) *huh.Group

// IssueTrackerPicker shows the live issue list and branch type selector.
func IssueTrackerPicker(issues []tracker.Issue, selected *tracker.Issue, types []string, branchType *string) *huh.Group

// IssueTrackerError shows an error note with a "Continue with manual input" button.
func IssueTrackerError(msg string) *huh.Group

// IssueUpdateStatusConfirm asks whether to update the issue status in the tracker.
func IssueUpdateStatusConfirm(issueID, statusName, trackerType string, confirmed *bool) *huh.Group
```

### Extended `issueStartRunE` flow

```
tracker configured?
  NO  → Step 2b (manual) → Step 3 → done
  YES →
    Step 1: IssueTrackerToggle (default YES for issue start, NO for branch new)
      useTracker=YES →
        fetch ListIssues()
          error → IssueTrackerError note → Step 2b
          ok    → Step 2a: IssueTrackerPicker
      useTracker=NO  → Step 2b (manual, unchanged)
    Step 3: IssueConfirm (unchanged)
    Step 4: IssueUpdateStatusConfirm  ← only when issue came from tracker
      YES → UpdateIssueStatus(); failure = non-fatal warning to stderr
      NO  → skip
```

`git cz branch new` uses the same flow with `defaultFetch=false` in Step 1 (manual-first per the Step 2b comment already in `branchNewRunE`).

---

## Section 6: Error handling

| Situation | Behaviour |
|---|---|
| No `tracker.type` in config | Skip toggle; go straight to manual form |
| Unknown tracker type | `tracker.New()` returns error surfaced by `issueStartRunE` |
| `ListIssues()` fails | `IssueTrackerError` TUI note + "Continue with manual input" → manual form |
| `UpdateIssueStatus()` fails | Non-fatal: `warning: could not update tracker status: <err>` to stderr |
| User picks issue, declines branch confirm | No branch, no status update |
| User declines status update (Step 4 = NO) | Branch created, tracker untouched — not an error |
| Status name not found in Redmine | `UpdateIssueStatus` returns descriptive error → warning only |

---

## Section 7: Testing

- **`tracker/tracker_test.go`** — `Register` + `New` happy path; unknown type error message
- **`tracker/redmine/redmine_test.go`** — `httptest.NewServer` fake Redmine API:
  - `ListIssues`: 200 with fixture JSON, 401 auth failure, network error
  - `UpdateIssueStatus`: status resolution, 200 success, status name not found
- **`store/store_test.go`** — migration: existing rows have `tracker_type = NULL`; insert with `TrackerType = &"redmine"` round-trips correctly
- **`cmd/issue.go`** — TUI interaction not unit-tested; covered by manual smoke tests

---

## Out of scope

- `git cz issue list` and `git cz issue close` (tracker-backed) — deferred to a follow-up
- Plane adapter — interface and registry are ready; adapter is a separate task
- Storing `Description` in the SQLite store — not needed yet
- Multiple trackers configured simultaneously
