# Issue Tracker Mirror Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mirror the issues of one tracker project with the issues stored in the repository, and keep open/closed in sync both ways.

**Architecture:** The link lives in each issue's own chain under `refs/zf/issues/`. An imported issue has a deterministic root commit (same hash on every clone); a repo-born issue gets a `link_tracker` op once it is created in the tracker; a `tracker_state` op remembers the last tracker state seen, so a three-way comparison tells which side moved. One function, `(*issue.Mirror).Reconcile`, does import, export and state sync; `issue list` and the commands that create or close an issue call it.

**Tech Stack:** Go (run through `mise exec -- go …`), git plumbing (`hash-object`, `mktree`, `commit-tree`), `github.com/pelletier/go-toml` v1, `github.com/google/go-github/v73`, `net/http/httptest` for adapter tests.

**Spec:** `docs/superpowers/specs/2026-10-06-issue-tracker-mirror-design.md`. Read it before starting; this plan argues from it.

## Global Constraints

- Go commands run as `mise exec -- go …`, never bare `go`.
- Every distinct assertion in a test sits in its own named `t.Run`.
- **GitNexus, from the project `CLAUDE.md`:** before editing an existing function or method, run `impact({target: "<name>", direction: "upstream"})` and report the blast radius; stop and warn on HIGH or CRITICAL. Before every commit, run `detect_changes({scope: "staged"})`.
- Commits need the user's go-ahead in this repository. If a commit is declined, leave the work staged, say so, and continue with the next task.
- Unknown op types must keep being skipped by `Fold`: an older binary reads refs written by a newer one.
- The import root payload is frozen: `{"v":1,"type":"create","at":"%s","tracker_type":"%s","project":"%s","tracker_id":"%s"}`. Author and committer `git-zf <git-zf@localhost>`, message `create`, never signed.
- Golden hash for tracker type `forgejo`, project `zf`, number `42`, created `2026-10-01T10:00:00Z`: `619c562f9fa770a8ecd7cdee1f10f4ad168ef053`.
- Config keys are kebab-case: `near-slug`, `far-slug`, `mirror`.
- The near slug is lowercased at load and used lowercased everywhere.
- Mirror on = tracker type set, exactly one project, `mirror = true`. Mirror off = behaviour unchanged.
- A tracker failure is a warning on stderr; the command continues.
- Only `issue list`, `issue new`, `issue close <id>`, the merge close and `issue sync` reconcile. `show`, `edit`, `comment`, `label`, `start`, hooks and `commit` never do.
- Match the surrounding code: comment density, naming, error wrapping with `fmt.Errorf("…: %w", err)`.

## Review Focus

Inputs the spec implies but its test table does not name. Each has a test in the task that owns the code.

1. **A tracker title with quotes, newlines or non-ASCII text** is imported unchanged (it travels in a `set_title` op, never in the frozen template). Task 9.
2. **The listing returns the same issue twice** (an issue created while pages are walked): it is imported once and counted once. Task 9.
3. **A tracker issue ID outside `^[0-9A-Za-z_-]+$`**: that issue is skipped with a warning and the others are still imported. Task 9.
4. **A linked tracker issue was deleted** (`ErrIssueNotFound`): a warning on each run, no op written, the record keeps its state. Task 9.
5. **`mirror = true` but the tracker cannot be built** (no token, unknown type): the command works on local data with one warning. Task 10.
6. **An issue ref that does not name its chain's root** (a corrupt or hand-written ref): the batched listing skips it with a warning instead of failing every command. Task 8.

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `config/config.go` | modify | `TrackerProject`, `Mirror`, load-time normalisation |
| `config/default.toml` | modify | `mirror = false`, no `projects = []` |
| `tracker/tracker.go` | modify | `Issue.CreatedAt`, three interface methods |
| `tracker/{github,forgejo,redmine}/*.go` | modify | read `FarSlug`; the three calls |
| `tracker/redmine/mirror_test.go` | create | internal-package tests of the three calls |
| `tracker/fake/fake.go` | modify | stateful fake of the three calls |
| `git/chain_ref.go` | modify | `WriteFixedChainRoot` |
| `git/chain_ref_sync.go` | modify | `PushChainRefs`: one push for many chains |
| `issue/record.go` | modify | ops, `TrackerLink`, fold, `DisplayID` |
| `issue/mirror.go` | create | `Mirror`, `Reconcile` |
| `issue/repo.go` | modify | `List` in three git processes, `PushAll`, `Resolve` by tracker number |
| `issue/row.go` | modify | `Row.TrackerID`, the ID cell |
| `cmd/issue/record.go` | modify | `openMirror`, `mirrorOf`, `reconcileIssues` |
| `cmd/issue/{new,sync,list,show,close}.go` | modify | call sites |
| `cmd/issue/mirror_e2e_test.go` | create | command tests with the mirror on |
| `cmd/issue/close_mirror_e2e_test.go` | create | merge close with the mirror on |
| `cmd/issueflow/start.go` | modify | start from an imported record |
| `docs/issue-refs.md`, `README.md`, `CLAUDE.md` | modify | documentation |

---

### Task 1: Project config with near and far slugs

**Files:**
- Modify: `config/config.go` (`IssueTrackerConfig`, `Load`)
- Modify: `config/default.toml` (`[issue-tracker]` block)
- Modify: `tracker/github/github.go`, `tracker/forgejo/forgejo.go`, `tracker/redmine/redmine.go` (every read of `cfg.Projects`)
- Modify: `README.md` (the `[issue-tracker]` example near line 384 and the `projects` row of its table)
- Test: `config/config_test.go`, and the three adapter test files (mechanical)

**Interfaces:**
- Consumes: nothing.
- Produces: `config.TrackerProject{NearSlug, FarSlug string}`; `config.IssueTrackerConfig.Projects []TrackerProject`; `config.IssueTrackerConfig.Mirror bool`; `func (c IssueTrackerConfig) FarSlugs() []string`.

- [ ] **Step 1: Write the failing tests**

In `config/config_test.go`, replace the subtest `"reads projects list from a TOML config file"` (near line 116, it loads `projects = ["a/b", "c/d"]`) with this new test function, and delete the old subtest:

```go
func TestLoadTrackerProjects(t *testing.T) {
	t.Parallel()

	load := func(t *testing.T, blob string) (*config.AppConfig, error) {
		t.Helper()

		return config.Load(writeTOML(t, blob))
	}

	t.Run("reads near and far slugs and lowercases the near slug", func(t *testing.T) {
		t.Parallel()

		cfg, err := load(t, `
[issue-tracker]
type = "forgejo"

[[issue-tracker.projects]]
near-slug = "ZF"
far-slug = "Piprim/Git-ZF"
`)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []config.TrackerProject{{NearSlug: "zf", FarSlug: "Piprim/Git-ZF"}}
		if !slices.Equal(cfg.IssueTracker.Projects, want) {
			t.Errorf("Projects = %+v, want %+v", cfg.IssueTracker.Projects, want)
		}
	})

	t.Run("FarSlugs lists the tracker-side names", func(t *testing.T) {
		t.Parallel()

		c := config.IssueTrackerConfig{Projects: []config.TrackerProject{
			{NearSlug: "a", FarSlug: "o/a"}, {NearSlug: "b", FarSlug: "o/b"},
		}}
		if got := c.FarSlugs(); !slices.Equal(got, []string{"o/a", "o/b"}) {
			t.Errorf("FarSlugs = %v", got)
		}
	})

	t.Run("the old string form is rejected and the message shows the new form", func(t *testing.T) {
		t.Parallel()

		_, err := load(t, "[issue-tracker]\nprojects = [\"a/b\"]\n")
		if err == nil || !strings.Contains(err.Error(), "[[issue-tracker.projects]]") {
			t.Errorf("err = %v, want one naming [[issue-tracker.projects]]", err)
		}
	})

	t.Run("an empty projects array is the same as none", func(t *testing.T) {
		t.Parallel()

		cfg, err := load(t, "[issue-tracker]\nprojects = []\n")
		if err != nil || len(cfg.IssueTracker.Projects) != 0 {
			t.Errorf("Projects = %+v, err = %v", cfg.IssueTracker.Projects, err)
		}
	})

	rejected := map[string]string{
		"two near slugs differing only by case": `
[[issue-tracker.projects]]
near-slug = "zf"
far-slug = "o/a"
[[issue-tracker.projects]]
near-slug = "ZF"
far-slug = "o/b"
`,
		"a near slug with a space": `
[[issue-tracker.projects]]
near-slug = "my project"
far-slug = "o/a"
`,
		"an empty near slug": `
[[issue-tracker.projects]]
far-slug = "o/a"
`,
		"an empty far slug": `
[[issue-tracker.projects]]
near-slug = "zf"
`,
		"mirror without a project": `
[issue-tracker]
type = "forgejo"
mirror = true
`,
		"mirror with two projects": `
[issue-tracker]
type = "forgejo"
mirror = true
[[issue-tracker.projects]]
near-slug = "a"
far-slug = "o/a"
[[issue-tracker.projects]]
near-slug = "b"
far-slug = "o/b"
`,
		"mirror without a tracker type": `
[issue-tracker]
mirror = true
[[issue-tracker.projects]]
near-slug = "a"
far-slug = "o/a"
`,
	}
	for name, blob := range rejected {
		t.Run(name+" is rejected", func(t *testing.T) {
			t.Parallel()

			if _, err := load(t, blob); err == nil {
				t.Error("Load: want an error, got nil")
			}
		})
	}

	t.Run("mirror with a type and one project loads", func(t *testing.T) {
		t.Parallel()

		cfg, err := load(t, `
[issue-tracker]
type = "forgejo"
mirror = true
[[issue-tracker.projects]]
near-slug = "zf"
far-slug = "o/a"
`)
		if err != nil || !cfg.IssueTracker.Mirror {
			t.Errorf("Mirror = %v, err = %v", cfg != nil && cfg.IssueTracker.Mirror, err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./config/ -run TestLoadTrackerProjects -v`
Expected: build failure, `undefined: config.TrackerProject`.

- [ ] **Step 3: Implement the config**

In `config/config.go`, replace `IssueTrackerConfig` and add the helpers (add `regexp` and `strings` to the imports):

```go
// TrackerProject names one tracker project twice. NearSlug is the stable local
// name stored in the repository; Load lowercases it. FarSlug is what the
// tracker calls the project, passed to it as written.
type TrackerProject struct {
	NearSlug string `json:"near-slug" toml:"near-slug"`
	FarSlug  string `json:"far-slug"  toml:"far-slug"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type   string `json:"type"   toml:"type"`
	URL    string `json:"url"    toml:"url"`
	Token  string `json:"token"  toml:"token"`
	Mirror bool   `json:"mirror" toml:"mirror"`

	Projects []TrackerProject `json:"projects" toml:"projects"`
}

// FarSlugs returns the tracker-side names of the configured projects.
func (c IssueTrackerConfig) FarSlugs() []string {
	out := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		out[i] = p.FarSlug
	}

	return out
}

var nearSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// normalize lowercases the near slugs and checks the projects and the mirror
// switch.
func (c *IssueTrackerConfig) normalize() error {
	seen := make(map[string]bool, len(c.Projects))
	for i := range c.Projects {
		p := &c.Projects[i]
		p.NearSlug = strings.ToLower(p.NearSlug)

		switch {
		case !nearSlugRe.MatchString(p.NearSlug):
			return fmt.Errorf(
				"issue-tracker.projects: near-slug %q must be letters, digits and dashes, starting with a letter or a digit",
				p.NearSlug)
		case p.FarSlug == "":
			return fmt.Errorf("issue-tracker.projects: project %q has no far-slug", p.NearSlug)
		case seen[p.NearSlug]:
			return fmt.Errorf("issue-tracker.projects: near-slug %q is used twice", p.NearSlug)
		}
		seen[p.NearSlug] = true
	}

	if c.Mirror && (c.Type == "" || len(c.Projects) != 1) {
		return errors.New(
			"issue-tracker: mirror = true needs a tracker type and exactly one [[issue-tracker.projects]] entry")
	}

	return nil
}

const projectsKey = "issue-tracker.projects"

const oldProjectsHelp = `issue-tracker.projects is no longer a list of strings. Write one table per project:

    [[issue-tracker.projects]]
    near-slug = "myproject"     # stable local name
    far-slug  = "owner/repo"    # what the tracker calls it`

// unmarshalFile decodes one config file into cfg. The former string form of
// issue-tracker.projects is an error; an empty array, which older default
// configs wrote, is dropped: the decoder cannot turn it into a struct slice.
func unmarshalFile(b []byte, cfg *AppConfig) error {
	tree, err := toml.LoadBytes(b)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	if old, ok := tree.Get(projectsKey).([]any); ok {
		if len(old) > 0 {
			return errors.New(oldProjectsHelp)
		}
		if err := tree.Delete(projectsKey); err != nil {
			return fmt.Errorf("drop empty projects: %w", err)
		}
	}

	if err := tree.Unmarshal(cfg); err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	return nil
}
```

In `Load`, replace the file decode and add the check at the end:

```go
		if err := unmarshalFile(b, &cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
```

```go
	cfg.ProgName = ProgName

	if err := cfg.IssueTracker.normalize(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &cfg, nil
```

In `config/default.toml`, the `[issue-tracker]` block becomes:

```toml
[issue-tracker]
type = ""
url = ""
token = ""
mirror = false
```

(The `projects = []` line goes: the decoder rejects an empty array for a struct slice. An absent key is the same empty list.)

- [ ] **Step 4: Fix the adapters and their tests**

Run `mise exec -- go build ./... 2>&1 | head -40` and fix each error:

- `tracker/github/github.go`, in `ListIssues`: `!slices.Contains(a.cfg.Projects, proj)` → `!slices.Contains(a.cfg.FarSlugs(), proj)`. In `ownerRepo`: both `a.cfg.Projects[0]` → `a.cfg.Projects[0].FarSlug`.
- `tracker/forgejo/forgejo.go`: the same two changes in `ListIssues` and `ownerRepo`.
- `tracker/redmine/redmine.go`, in `ListIssues`: `for _, p := range a.cfg.Projects` → `for _, p := range a.cfg.FarSlugs()`.

In each of `tracker/github/github_test.go`, `tracker/forgejo/forgejo_test.go` and `tracker/redmine/redmine_test.go`, add this helper and replace every `Projects: []string{…}` with `Projects: far(…)` (in `github_test.go`, `newTestAdapter` keeps its `projects []string` parameter and passes `Projects: far(projects...)`):

```go
// far builds the projects config from tracker-side names.
func far(slugs ...string) []config.TrackerProject {
	out := make([]config.TrackerProject, len(slugs))
	for i, s := range slugs {
		out[i] = config.TrackerProject{NearSlug: fmt.Sprintf("p%d", i), FarSlug: s}
	}

	return out
}
```

Then `grep -rn "Projects" --include=*.go . | grep -v "UniqueProjects\|FarSlugs"` and fix any remaining reader.

- [ ] **Step 5: Update the README**

In `README.md`, the `[issue-tracker]` example: replace the `projects = ["owner/repo"]` line with

```toml
mirror = false                      # true: mirror this project's issues with the repository

[[issue-tracker.projects]]          # optional; one table per project
near-slug = "myproject"             # stable local name (lowercased), stored in the repository
far-slug  = "owner/repo"            # what the tracker calls it
```

and in the table below, replace the `projects` row's first sentence with: "Optional list of projects, one `[[issue-tracker.projects]]` table each. `far-slug` is the tracker's name for the project (Redmine: a slug or numeric ID; GitHub/Forgejo/Gitea: `"owner/repo"`); `near-slug` is a stable local name, so renaming the project in the tracker only means editing `far-slug`." Keep the rest of the row. Add a row: "`mirror` | `true` mirrors the project's issues with the issues stored in the repository. Needs a tracker type and exactly one project. See `docs/issue-refs.md`."

- [ ] **Step 6: Run the tests**

Run: `mise exec -- go build ./... && mise exec -- go test ./config/... ./tracker/... -v 2>&1 | tail -30`
Expected: PASS. `TestLoad` and `TestDefaultTOML_isValidTOML` must still pass: they prove `tree.Unmarshal` overlays files the way `toml.Unmarshal` did.

Run: `mise exec -- go test ./... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add config tracker README.md
git commit -m "feat(config): tracker projects carry a near and a far slug, mirror switch"
```

---

### Task 2: Fold the tracker link and the tracker state

**Files:**
- Modify: `issue/record.go`
- Test: `issue/record_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: constants `OpLinkTracker = "link_tracker"`, `OpTrackerState = "tracker_state"`; `Op` fields `TrackerType`, `Project`, `TrackerID`, `Status string`; `type TrackerLink struct{ Type, Project, ID string; Born bool }`; `Record` fields `Tracker *TrackerLink`, `TrackerState`, `TrackerStatus string`, `DuplicateTrackerIDs []string`; `DisplayID()` returns the tracker number for a tracker-born record.

- [ ] **Step 1: Write the failing tests**

Append to `issue/record_test.go`:

```go
func TestFold_Tracker(t *testing.T) {
	t.Parallel()

	root := op("c0", OpCreate, "2026-10-01T10:00:00Z")
	root.TrackerType, root.Project, root.TrackerID = "forgejo", "zf", "42"

	plain := op("p0", OpCreate, "2026-10-01T10:00:00Z")
	plain.Title = "Local"

	link := func(id, number string, parents ...string) Op {
		o := op(id, OpLinkTracker, "2026-10-01T11:00:00Z", parents...)
		o.TrackerType, o.Project, o.TrackerID = "forgejo", "zf", number

		return o
	}
	seen := func(id, value, status string, parents ...string) Op {
		o := op(id, OpTrackerState, "2026-10-01T12:00:00Z", parents...)
		o.Value, o.Status = value, status

		return o
	}

	t.Run("an import root links the record and marks it born in the tracker", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root})
		want := TrackerLink{Type: "forgejo", Project: "zf", ID: "42", Born: true}
		if rec.Tracker == nil || *rec.Tracker != want {
			t.Errorf("Tracker = %+v, want %+v", rec.Tracker, want)
		}
	})

	t.Run("a tracker-born record is displayed by its tracker number", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root})
		if got := rec.DisplayID(); got != "42" {
			t.Errorf("DisplayID = %q, want 42", got)
		}
	})

	t.Run("set_title after an import root gives the title", func(t *testing.T) {
		t.Parallel()

		title := op("c1", OpSetTitle, "2026-10-01T10:01:00Z", "c0")
		title.Value = "From tracker"
		if got := Fold("c0", []Op{root, title}).Title; got != "From tracker" {
			t.Errorf("Title = %q", got)
		}
	})

	t.Run("a record without a link has none", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain})
		if rec.Tracker != nil || rec.TrackerState != "" {
			t.Errorf("Tracker = %+v, TrackerState = %q", rec.Tracker, rec.TrackerState)
		}
	})

	t.Run("link_tracker links a repo-born record and keeps its short hash", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0")})
		want := TrackerLink{Type: "forgejo", Project: "zf", ID: "57"}
		if rec.Tracker == nil || *rec.Tracker != want {
			t.Errorf("Tracker = %+v, want %+v", rec.Tracker, want)
		}
		if got := rec.DisplayID(); got != "p0" {
			t.Errorf("DisplayID = %q, want p0", got)
		}
	})

	t.Run("the first link_tracker wins and the loser is recorded", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0"), link("p2", "58", "p1")})
		if rec.Tracker == nil || rec.Tracker.ID != "57" {
			t.Errorf("Tracker = %+v, want number 57", rec.Tracker)
		}
		if !slices.Equal(rec.DuplicateTrackerIDs, []string{"58"}) {
			t.Errorf("DuplicateTrackerIDs = %v, want [58]", rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a repeated link_tracker for the same number is not a duplicate", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0"), link("p2", "57", "p1")})
		if len(rec.DuplicateTrackerIDs) != 0 {
			t.Errorf("DuplicateTrackerIDs = %v, want none", rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a link_tracker on a tracker-born record is a duplicate", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, link("c1", "58", "c0")})
		if rec.Tracker.ID != "42" || !slices.Equal(rec.DuplicateTrackerIDs, []string{"58"}) {
			t.Errorf("Tracker = %+v, duplicates = %v", rec.Tracker, rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a link_tracker without a number is ignored", func(t *testing.T) {
		t.Parallel()

		if rec := Fold("p0", []Op{plain, link("p1", "", "p0")}); rec.Tracker != nil {
			t.Errorf("Tracker = %+v, want nil", rec.Tracker)
		}
	})

	t.Run("tracker_state records the state seen and the status name", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, seen("c1", StateClosed, "Rejected", "c0")})
		if rec.TrackerState != StateClosed || rec.TrackerStatus != "Rejected" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})

	t.Run("tracker_state does not change the issue state", func(t *testing.T) {
		t.Parallel()

		if got := Fold("c0", []Op{root, seen("c1", StateClosed, "", "c0")}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("a tracker_state with an unknown value is ignored", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, seen("c1", "resolved", "Resolved", "c0")})
		if rec.TrackerState != "" || rec.TrackerStatus != "" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run TestFold_Tracker -v`
Expected: build failure, `undefined: OpLinkTracker`.

- [ ] **Step 3: Implement**

In `issue/record.go`:

Add to the op-type constants:

```go
	OpLinkTracker    = "link_tracker"
	OpTrackerState   = "tracker_state"
```

Add to `Op`, after `Body`:

```go
	TrackerType string `json:"tracker_type,omitempty"` // create (imported issue), link_tracker
	Project     string `json:"project,omitempty"`      // create, link_tracker: the near slug
	TrackerID   string `json:"tracker_id,omitempty"`   // create, link_tracker: the tracker's issue number
	Status      string `json:"status,omitempty"`       // tracker_state: the tracker's status name
```

Add before `Record`, and add the fields to `Record` after `CreatedAt`:

```go
// TrackerLink names the tracker issue a record is mirrored with.
type TrackerLink struct {
	Type    string `json:"type"`    // tracker type, e.g. "forgejo"
	Project string `json:"project"` // near slug
	ID      string `json:"id"`      // the tracker's issue number
	// Born is true when the link comes from the create op: the issue was
	// imported from the tracker.
	Born bool `json:"born"`
}
```

```go
	// Tracker is nil for an issue that is not mirrored.
	Tracker *TrackerLink `json:"tracker"`
	// TrackerState is the last open/closed the chain saw on the tracker; ""
	// when it never saw one. TrackerStatus is the tracker's own status name.
	TrackerState  string `json:"tracker_state"`
	TrackerStatus string `json:"tracker_status"`
	// DuplicateTrackerIDs lists the tracker issues named by link_tracker ops
	// that lost to an earlier link.
	DuplicateTrackerIDs []string `json:"-"`
```

Replace `DisplayID`:

```go
// DisplayID is the ID shown to users and used in branch names: the tracker's
// number for an issue born in the tracker, the short hash otherwise.
func (r *Record) DisplayID() string {
	if r.Tracker != nil && r.Tracker.Born {
		return r.Tracker.ID
	}

	return r.ShortID()
}
```

In `Fold`, the `OpCreate` case becomes, and two cases are added after `OpAddComment`:

```go
		case OpCreate:
			rec.Title, rec.Description, rec.BranchType = op.Title, op.Description, op.BranchType
			if op.ID == id {
				rec.CreatedAt = chain.ParseAt(op.At)
				if op.TrackerID != "" {
					rec.Tracker = &TrackerLink{Type: op.TrackerType, Project: op.Project, ID: op.TrackerID, Born: true}
				}
			}
```

```go
		case OpLinkTracker:
			switch {
			case op.TrackerID == "":
			case rec.Tracker == nil:
				rec.Tracker = &TrackerLink{Type: op.TrackerType, Project: op.Project, ID: op.TrackerID}
			case rec.Tracker.ID != op.TrackerID && !slices.Contains(rec.DuplicateTrackerIDs, op.TrackerID):
				rec.DuplicateTrackerIDs = append(rec.DuplicateTrackerIDs, op.TrackerID)
			}
		case OpTrackerState:
			if op.Value == StateOpen || op.Value == StateClosed {
				rec.TrackerState, rec.TrackerStatus = op.Value, op.Status
			}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./issue/... -v 2>&1 | tail -30`
Expected: PASS, including the existing `TestFold`.

Run: `mise exec -- go test ./cmd/... 2>&1 | tail -20`
Expected: PASS. If a test compares the JSON of `issue show --json` byte for byte, add the three new keys (`tracker`, `tracker_state`, `tracker_status`) to its expectation.

- [ ] **Step 5: Commit**

```bash
git add issue/record.go issue/record_test.go
git commit -m "feat(issue): fold the tracker link and the last tracker state"
```

---

### Task 3: A root commit with a fixed identity

**Files:**
- Modify: `git/chain_ref.go` (next to `WriteChainRoot`)
- Test: `git/chain_ref_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func (c *Client) WriteFixedChainRoot(ctx context.Context, payload []byte, message string, when time.Time) (string, error)`.

- [ ] **Step 1: Write the failing test**

Append to `git/chain_ref_test.go` (add `os/exec`, `strings` and `time` to the imports if missing):

```go
func TestChainRef_FixedRoot(t *testing.T) {
	t.Parallel()

	const (
		payload = `{"v":1,"type":"create","at":"2026-10-01T10:00:00Z","tracker_type":"forgejo","project":"zf","tracker_id":"42"}`
		golden  = "619c562f9fa770a8ecd7cdee1f10f4ad168ef053"
	)
	when := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	ctx := t.Context()

	a, _ := newDiskRepo(t)
	b, dirB := newDiskRepo(t)

	// Everything an ordinary commit depends on differs in b.
	for key, value := range map[string]string{
		"user.name": "Somebody Else", "user.email": "else@example.org",
		"commit.gpgsign": "true", "i18n.commitEncoding": "ISO-8859-1",
	} {
		if out, err := exec.CommandContext(ctx, "git", "-C", dirB, "config", key, value).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", key, err, out)
		}
	}

	rootA, errA := a.WriteFixedChainRoot(ctx, []byte(payload), "create", when)
	rootB, errB := b.WriteFixedChainRoot(ctx, []byte(payload), "create", when)

	t.Run("it succeeds in both repositories", func(t *testing.T) {
		if errA != nil || errB != nil {
			t.Fatalf("WriteFixedChainRoot: %v / %v", errA, errB)
		}
	})
	t.Run("two repositories with different identities get the same commit", func(t *testing.T) {
		if rootA != rootB {
			t.Errorf("roots differ: %s vs %s", rootA, rootB)
		}
	})
	t.Run("the commit ID is the golden hash", func(t *testing.T) {
		if rootA != golden {
			t.Errorf("root = %s, want %s", rootA, golden)
		}
	})
	t.Run("the commit is unsigned and has no parent", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dirB, "cat-file", "-p", rootB).CombinedOutput()
		if err != nil {
			t.Fatalf("cat-file: %v\n%s", err, out)
		}
		if s := string(out); strings.Contains(s, "gpgsig") || strings.Contains(s, "parent ") {
			t.Errorf("commit =\n%s", s)
		}
	})
	t.Run("another date gives another commit", func(t *testing.T) {
		other, err := a.WriteFixedChainRoot(ctx, []byte(payload), "create", when.Add(time.Second))
		if err != nil || other == rootA {
			t.Errorf("other = %s (%v), want a different commit", other, err)
		}
	})
	t.Run("no ref is created", func(t *testing.T) {
		tip, err := a.ChainTip(ctx, IssueRefs, rootA)
		if err != nil || tip != "" {
			t.Errorf("tip = %q (%v), want none", tip, err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git/ -run TestChainRef_FixedRoot -v`
Expected: build failure, `a.WriteFixedChainRoot undefined`.

- [ ] **Step 3: Implement**

In `git/chain_ref.go`, extract the tree step of `writeChainCommit` (run `impact` on `writeChainCommit` first) and add the new function. Add `os` and `time` to the imports.

```go
// chainTree stores payload as op.json and returns the tree holding it.
func (c *Client) chainTree(ctx context.Context, payload []byte) (string, error) {
	blob, err := c.outputStdin(ctx, payload, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}

	tree, err := c.outputStdin(ctx, []byte("100644 blob "+blob+"\t"+chainOpFile+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	return tree, nil
}
```

`writeChainCommit` starts with `tree, err := c.chainTree(ctx, payload)` in place of its `hash-object` and `mktree` calls; the rest is unchanged.

```go
// The author and committer of every fixed chain root.
const (
	fixedRootName  = "git-zf"
	fixedRootEmail = "git-zf@localhost"
)

// WriteFixedChainRoot writes payload as a root commit whose ID depends only
// on payload, message and when: the author and committer are fixed, both dated
// when, and the commit is never signed. Two clones calling it with the same
// arguments get the same commit. It does not move any ref.
func (c *Client) WriteFixedChainRoot(
	ctx context.Context, payload []byte, message string, when time.Time,
) (string, error) {
	tree, err := c.chainTree(ctx, payload)
	if err != nil {
		return "", err
	}

	date := fmt.Sprintf("@%d +0000", when.Unix())
	// A configured i18n.commitEncoding would add an encoding header.
	cmd := c.gitCmd(ctx, "-c", "i18n.commitEncoding=UTF-8", "commit-tree", tree, "-m", message)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+fixedRootName, "GIT_AUTHOR_EMAIL="+fixedRootEmail, "GIT_AUTHOR_DATE="+date,
		"GIT_COMMITTER_NAME="+fixedRootName, "GIT_COMMITTER_EMAIL="+fixedRootEmail, "GIT_COMMITTER_DATE="+date)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", gitStderr(err))
	}

	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./git/... -run "TestChainRef|TestIssueRef_" -v 2>&1 | tail -30`
Expected: PASS. If the golden hash differs, the code deviates from the frozen format in Global Constraints; fix the code, never the constant.

- [ ] **Step 5: Commit**

```bash
git add git/chain_ref.go git/chain_ref_test.go
git commit -m "feat(git): write a chain root with a fixed identity"
```

---

### Task 4: GitHub adapter, the three mirror calls

**Files:**
- Modify: `tracker/tracker.go` (`Issue`)
- Modify: `tracker/github/github.go`
- Test: `tracker/github/github_test.go`

**Interfaces:**
- Consumes: `config.TrackerProject` (Task 1).
- Produces: `tracker.Issue.CreatedAt time.Time`; on `*githubAdapter`: `ListProjectIssues(ctx) ([]tracker.Issue, error)`, `CreateIssue(ctx, title, description string) (tracker.Issue, error)`, `SetIssueOpen(ctx, issueID string, open bool) error`. They are not on the `tracker.Tracker` interface yet (Task 7).

- [ ] **Step 1: Write the failing tests**

Append to `tracker/github/github_test.go` (add `encoding/json` and `time` to the imports if missing):

```go
func TestListProjectIssues(t *testing.T) {
	t.Parallel()

	a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/a/b/issues" || r.URL.Query().Get("state") != "open" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusBadRequest)

			return
		}
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `[{"number": 9, "title": "Second page", "state": "open", "created_at": "2026-09-03T08:00:00Z"}]`)

			return
		}
		w.Header().Set("Link", `<http://`+r.Host+`/repos/a/b/issues?state=open&page=2>; rel="next"`)
		fmt.Fprint(w, `[
			{"number": 7, "title": "Bug", "body": "Steps", "state": "open", "created_at": "2026-09-01T08:00:00Z"},
			{"number": 8, "title": "A pull request", "state": "open", "pull_request": {"url": "x"}, "created_at": "2026-09-02T08:00:00Z"}
		]`)
	})

	got, err := a.ListProjectIssues(t.Context())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("ListProjectIssues: %v", err)
		}
	})
	t.Run("pull requests are dropped and every page is read", func(t *testing.T) {
		if len(got) != 2 || got[0].ID != "7" || got[1].ID != "9" {
			t.Fatalf("issues = %+v", got)
		}
	})
	t.Run("an issue carries its fields and creation date", func(t *testing.T) {
		want := tracker.Issue{
			TrackerType: "github", ID: "7", Subject: "Bug", Description: "Steps", Status: "open", Project: "a/b",
			CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
		}
		if len(got) == 0 || !got[0].CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("CreatedAt = %v", got)
		}
		got[0].CreatedAt = want.CreatedAt
		if got[0] != want {
			t.Errorf("issue = %+v, want %+v", got[0], want)
		}
	})
}

func TestCreateIssue(t *testing.T) {
	t.Parallel()

	var sent struct{ Title, Body string }
	a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/a/b/issues" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number": 57, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}`)
	})

	got, err := a.CreateIssue(t.Context(), "Bug", "Steps")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	})
	t.Run("the title and description are sent", func(t *testing.T) {
		if sent.Title != "Bug" || sent.Body != "Steps" {
			t.Errorf("sent = %+v", sent)
		}
	})
	t.Run("the created issue's number and status are returned", func(t *testing.T) {
		if got.ID != "57" || got.Status != "open" {
			t.Errorf("issue = %+v", got)
		}
	})
}

func TestSetIssueOpen(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		open bool
		want string
	}{"closing sends closed": {false, "closed"}, "reopening sends open": {true, "open"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var sent struct{ State string }
			a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch || r.URL.Path != "/repos/a/b/issues/57" {
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

					return
				}
				_ = json.NewDecoder(r.Body).Decode(&sent)
				fmt.Fprint(w, `{"number": 57}`)
			})

			if err := a.SetIssueOpen(t.Context(), "57", tc.open); err != nil {
				t.Fatalf("SetIssueOpen: %v", err)
			}
			if sent.State != tc.want {
				t.Errorf("state sent = %q, want %q", sent.State, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/ -run "TestListProjectIssues|TestCreateIssue|TestSetIssueOpen" -v`
Expected: build failure, `a.ListProjectIssues undefined`.

- [ ] **Step 3: Implement**

In `tracker/tracker.go`, add to `Issue` (and `time` to the imports):

```go
	// CreatedAt is when the tracker says the issue was created; zero when the
	// call that built the Issue does not report it.
	CreatedAt time.Time
```

In `tracker/github/github.go`, after `ListIssues`:

```go
// toIssue converts a GitHub issue of project (an "owner/repo").
func toIssue(iss *gogithub.Issue, project string) tracker.Issue {
	return tracker.Issue{
		TrackerType: trackerType,
		ID:          strconv.Itoa(iss.GetNumber()),
		Subject:     iss.GetTitle(),
		Description: iss.GetBody(),
		Status:      iss.GetState(),
		Project:     project,
		CreatedAt:   iss.GetCreatedAt().Time,
	}
}

// ListProjectIssues fetches every open issue of the configured repository,
// whoever it is assigned to, page after page, and drops pull requests.
func (a *githubAdapter) ListProjectIssues(ctx context.Context) ([]tracker.Issue, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return nil, err
	}

	opt := &gogithub.IssueListByRepoOptions{
		State:       statusOpen,
		ListOptions: gogithub.ListOptions{PerPage: issuesPerPage},
	}

	var out []tracker.Issue

	for {
		page, resp, err := a.client.Issues.ListByRepo(ctx, owner, repo, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list issues of %s/%s: %w", owner, repo, err)
		}

		for _, iss := range page {
			if !iss.IsPullRequest() {
				out = append(out, toIssue(iss, owner+"/"+repo))
			}
		}

		if resp.NextPage == 0 {
			break
		}

		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}

// CreateIssue creates an issue in the configured repository.
func (a *githubAdapter) CreateIssue(ctx context.Context, title, description string) (tracker.Issue, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.Issue{}, err
	}

	iss, _, err := a.client.Issues.Create(ctx, owner, repo,
		&gogithub.IssueRequest{Title: gogithub.Ptr(title), Body: gogithub.Ptr(description)})
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("github: create issue: %w", err)
	}

	return toIssue(iss, owner+"/"+repo), nil
}

// SetIssueOpen reopens (open) or closes the issue.
func (a *githubAdapter) SetIssueOpen(ctx context.Context, issueID string, open bool) error {
	state := statusClosed
	if open {
		state = statusOpen
	}

	return a.UpdateIssueStatus(ctx, issueID, state)
}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./tracker/github/... -v 2>&1 | tail -30`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/tracker.go tracker/github
git commit -m "feat(github): list a project's issues, create an issue, open or close one"
```

---

### Task 5: Forgejo adapter, the three mirror calls

**Files:**
- Modify: `tracker/forgejo/forgejo.go`
- Test: `tracker/forgejo/forgejo_test.go`

**Interfaces:**
- Consumes: `tracker.Issue.CreatedAt` (Task 4), `far(...)` test helper (Task 1).
- Produces: on `*forgejoAdapter`: `ListProjectIssues`, `CreateIssue`, `SetIssueOpen`, same signatures as Task 4.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/forgejo/forgejo_test.go`:

```go
// newMirrorAdapter returns the concrete adapter for project a/b, served by handler.
func newMirrorAdapter(t *testing.T, handler http.HandlerFunc) *forgejoAdapter {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, ok := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: far("a/b")}).(*forgejoAdapter)
	if !ok {
		t.Fatal("New did not return a *forgejoAdapter")
	}

	return a
}

func TestListProjectIssues(t *testing.T) {
	t.Parallel()

	a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/a/b/issues" ||
			q.Get("state") != "open" || q.Get("type") != "issues" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.String(), http.StatusBadRequest)

			return
		}
		switch q.Get("page") {
		case "1":
			fmt.Fprint(w, `[
				{"number": 7, "title": "Bug", "body": "Steps", "state": "open", "created_at": "2026-09-01T10:00:00+02:00"},
				{"number": 8, "title": "A pull request", "state": "open", "pull_request": {}, "created_at": "2026-09-02T08:00:00Z"}
			]`)
		case "2":
			fmt.Fprint(w, `[{"number": 9, "title": "Second page", "state": "open", "created_at": "2026-09-03T08:00:00Z"}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	})

	got, err := a.ListProjectIssues(t.Context())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("ListProjectIssues: %v", err)
		}
	})
	t.Run("pull requests are dropped and pages are walked until an empty one", func(t *testing.T) {
		if len(got) != 2 || got[0].ID != "7" || got[1].ID != "9" {
			t.Fatalf("issues = %+v", got)
		}
	})
	t.Run("an issue carries its fields and creation date", func(t *testing.T) {
		if len(got) == 0 {
			t.Fatal("no issue")
		}
		iss := got[0]
		if iss.Subject != "Bug" || iss.Description != "Steps" || iss.Status != "open" || iss.Project != "a/b" {
			t.Errorf("issue = %+v", iss)
		}
		if want := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC); !iss.CreatedAt.Equal(want) {
			t.Errorf("CreatedAt = %v, want %v", iss.CreatedAt, want)
		}
	})
}

func TestCreateIssue(t *testing.T) {
	t.Parallel()

	var sent struct{ Title, Body string }
	a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/a/b/issues" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number": 57, "title": "Bug", "state": "open", "created_at": "2026-09-01T08:00:00Z"}`)
	})

	got, err := a.CreateIssue(t.Context(), "Bug", "Steps")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	})
	t.Run("the title and description are sent", func(t *testing.T) {
		if sent.Title != "Bug" || sent.Body != "Steps" {
			t.Errorf("sent = %+v", sent)
		}
	})
	t.Run("the created issue's number and status are returned", func(t *testing.T) {
		if got.ID != "57" || got.Status != "open" {
			t.Errorf("issue = %+v", got)
		}
	})
}

func TestSetIssueOpen(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		open bool
		want string
	}{"closing sends closed": {false, "closed"}, "reopening sends open": {true, "open"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var sent struct{ State string }
			a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/repos/a/b/issues/57" {
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

					return
				}
				_ = json.NewDecoder(r.Body).Decode(&sent)
				fmt.Fprint(w, `{"number": 57}`)
			})

			if err := a.SetIssueOpen(t.Context(), "57", tc.open); err != nil {
				t.Fatalf("SetIssueOpen: %v", err)
			}
			if sent.State != tc.want {
				t.Errorf("state sent = %q, want %q", sent.State, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/forgejo/ -run "TestListProjectIssues|TestCreateIssue|TestSetIssueOpen" -v`
Expected: build failure, `a.ListProjectIssues undefined`.

- [ ] **Step 3: Implement**

In `tracker/forgejo/forgejo.go`, add to the `issue` struct (and `time` to the imports):

```go
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
```

After `ListIssues`:

```go
// toIssue converts a Forgejo issue of project (an "owner/repo").
func (a *forgejoAdapter) toIssue(iss *issue, project string) tracker.Issue {
	return tracker.Issue{
		TrackerType: a.trackerType,
		ID:          strconv.Itoa(iss.Number),
		Subject:     iss.Title,
		Description: iss.Body,
		Status:      iss.State,
		Project:     project,
		CreatedAt:   iss.CreatedAt,
	}
}

// projectIssuesPath returns "/repos/{owner}/{repo}/issues" and the project name.
func (a *forgejoAdapter) projectIssuesPath() (path, project string, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", "", err
	}

	return fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo)), owner + "/" + repo, nil
}

// ListProjectIssues fetches every open issue of the configured repository,
// whoever it is assigned to. Like ListIssues it walks pages until an empty
// one or maxPages, and drops pull requests.
func (a *forgejoAdapter) ListProjectIssues(ctx context.Context) ([]tracker.Issue, error) {
	path, project, err := a.projectIssuesPath()
	if err != nil {
		return nil, err
	}

	var out []tracker.Issue

	for page := 1; page <= maxPages; page++ {
		q := url.Values{
			"state": {statusOpen},
			"type":  {"issues"},
			"limit": {strconv.Itoa(issuesPerPage)},
			"page":  {strconv.Itoa(page)},
		}

		var batch []issue
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return nil, fmt.Errorf("forgejo: list issues of %s: %w", project, err)
		}

		if len(batch) == 0 {
			break
		}

		for i := range batch {
			if batch[i].PullRequest == nil {
				out = append(out, a.toIssue(&batch[i], project))
			}
		}
	}

	return out, nil
}

// CreateIssue creates an issue in the configured repository.
func (a *forgejoAdapter) CreateIssue(ctx context.Context, title, description string) (tracker.Issue, error) {
	path, project, err := a.projectIssuesPath()
	if err != nil {
		return tracker.Issue{}, err
	}

	body := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: title, Body: description}

	var created issue
	if err := a.doJSON(ctx, http.MethodPost, path, body, &created); err != nil {
		return tracker.Issue{}, fmt.Errorf("forgejo: create issue: %w", err)
	}

	return a.toIssue(&created, project), nil
}

// SetIssueOpen reopens (open) or closes the issue.
func (a *forgejoAdapter) SetIssueOpen(ctx context.Context, issueID string, open bool) error {
	state := statusClosed
	if open {
		state = statusOpen
	}

	return a.UpdateIssueStatus(ctx, issueID, state)
}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./tracker/forgejo/... -v 2>&1 | tail -30`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/forgejo
git commit -m "feat(forgejo): list a project's issues, create an issue, open or close one"
```

---

### Task 6: Redmine adapter, the three mirror calls

**Files:**
- Modify: `tracker/redmine/redmine.go`
- Create: `tracker/redmine/mirror_test.go` (package `redmine`: the existing test file is the external package `redmine_test` and cannot reach the concrete adapter)

**Interfaces:**
- Consumes: `tracker.Issue.CreatedAt` (Task 4), `config.TrackerProject` (Task 1).
- Produces: on `*redmineAdapter`: `ListProjectIssues`, `CreateIssue`, `SetIssueOpen`, same signatures as Task 4.

- [ ] **Step 1: Write the failing tests**

Create `tracker/redmine/mirror_test.go`:

```go
package redmine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
)

// newMirrorAdapter returns the concrete adapter for project "cpro", served by handler.
func newMirrorAdapter(t *testing.T, handler http.HandlerFunc) *redmineAdapter {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, err := New(config.IssueTrackerConfig{
		URL: srv.URL, Token: "test-key",
		Projects: []config.TrackerProject{{NearSlug: "cpro", FarSlug: "cpro"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ra, ok := a.(*redmineAdapter)
	if !ok {
		t.Fatal("New did not return a *redmineAdapter")
	}

	return ra
}

func TestListProjectIssues(t *testing.T) {
	t.Parallel()

	// 101 open issues: one more than a page.
	page := func(from, to int) string {
		items := make([]string, 0, to-from)
		for id := from; id < to; id++ {
			items = append(items, fmt.Sprintf(
				`{"id": %d, "subject": "Issue %d", "description": "Body", "status": {"id": 2, "name": "In Progress"}, "created_on": "2026-09-01T08:00:00Z"}`,
				id, id))
		}

		return `{"issues": [` + strings.Join(items, ",") + `], "total_count": 101}`
	}

	a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/projects/cpro/issues.json" || q.Get("status_id") != "open" {
			http.Error(w, "unexpected "+r.URL.String(), http.StatusBadRequest)

			return
		}
		if q.Get("offset") == "100" {
			fmt.Fprint(w, page(101, 102))

			return
		}
		fmt.Fprint(w, page(1, 101))
	})

	got, err := a.ListProjectIssues(t.Context())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("ListProjectIssues: %v", err)
		}
	})
	t.Run("every page is read", func(t *testing.T) {
		if len(got) != 101 || got[100].ID != "101" {
			t.Fatalf("got %d issues", len(got))
		}
	})
	t.Run("an issue carries its status name, project and creation date", func(t *testing.T) {
		if len(got) == 0 {
			t.Fatal("no issue")
		}
		iss := got[0]
		if iss.Subject != "Issue 1" || iss.Status != "In Progress" || iss.Project != "cpro" {
			t.Errorf("issue = %+v", iss)
		}
		if want := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC); !iss.CreatedAt.Equal(want) {
			t.Errorf("CreatedAt = %v, want %v", iss.CreatedAt, want)
		}
	})
}

func TestCreateIssue(t *testing.T) {
	t.Parallel()

	var sent struct {
		Issue struct {
			ProjectID   string `json:"project_id"`
			Subject     string `json:"subject"`
			Description string `json:"description"`
		} `json:"issue"`
	}
	a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/issues.json" || r.Header.Get("X-Redmine-API-Key") != "test-key" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"issue": {"id": 57, "subject": "Bug", "status": {"id": 1, "name": "New"}, "created_on": "2026-09-01T08:00:00Z"}}`)
	})

	got, err := a.CreateIssue(t.Context(), "Bug", "Steps")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("CreateIssue: %v", err)
		}
	})
	t.Run("the project, title and description are sent", func(t *testing.T) {
		if sent.Issue.ProjectID != "cpro" || sent.Issue.Subject != "Bug" || sent.Issue.Description != "Steps" {
			t.Errorf("sent = %+v", sent.Issue)
		}
	})
	t.Run("the created issue's number and status name are returned", func(t *testing.T) {
		if got.ID != "57" || got.Status != "New" {
			t.Errorf("issue = %+v", got)
		}
	})
}

func TestSetIssueOpen(t *testing.T) {
	t.Parallel()

	const statuses = `{"issue_statuses": [
		{"id": 1, "name": "New"}, {"id": 2, "name": "In Progress"},
		{"id": 5, "name": "Closed", "is_closed": true}, {"id": 6, "name": "Rejected", "is_closed": true}
	]}`

	for name, tc := range map[string]struct {
		open bool
		want int
	}{
		"closing picks the first closed status":    {false, 5},
		"reopening picks the first open status": {true, 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var sent struct {
				Issue struct {
					StatusID int `json:"status_id"`
				} `json:"issue"`
			}
			a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/issue_statuses.json":
					fmt.Fprint(w, statuses)
				case r.Method == http.MethodPut && r.URL.Path == "/issues/57.json":
					_ = json.NewDecoder(r.Body).Decode(&sent)
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
				}
			})

			if err := a.SetIssueOpen(t.Context(), "57", tc.open); err != nil {
				t.Fatalf("SetIssueOpen: %v", err)
			}
			if sent.Issue.StatusID != tc.want {
				t.Errorf("status_id sent = %d, want %d", sent.Issue.StatusID, tc.want)
			}
		})
	}

	t.Run("no closed status in the tracker is an error", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"issue_statuses": [{"id": 1, "name": "New"}]}`)
		})
		if err := a.SetIssueOpen(t.Context(), "57", false); err == nil {
			t.Error("SetIssueOpen: want an error, got nil")
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/redmine/ -run "TestListProjectIssues|TestCreateIssue|TestSetIssueOpen" -v`
Expected: build failure, `a.ListProjectIssues undefined`.

- [ ] **Step 3: Implement**

In `tracker/redmine/redmine.go` (add `time` to the imports; run `impact` on `fetchIssues` and `UpdateIssueStatus` first):

`status` and `issue` gain a field each:

```go
type status struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	//nolint:tagliatelle // Redmine wire format
	IsClosed bool `json:"is_closed"`
}
```

```go
	//nolint:tagliatelle // Redmine wire format
	CreatedOn time.Time `json:"created_on"`
```

Extract the conversion loop of `fetchIssues` into `toIssues` and call it from there (`return toIssues(payload.Issues, project), nil`):

```go
// toIssues converts Redmine issues. Every issue is reported under project;
// when project is empty, the name comes from the issue itself.
func toIssues(issues []issue, project string) []tracker.Issue {
	result := make([]tracker.Issue, 0, len(issues))
	for _, iss := range issues {
		statusName := ""
		if iss.Status != nil {
			statusName = iss.Status.Name
		}

		result = append(result, tracker.Issue{
			TrackerType: trackerType,
			ID:          strconv.Itoa(iss.ID),
			Subject:     iss.Subject,
			Description: iss.Description,
			Status:      statusName,
			Project:     cmp.Or(project, redmineProjectName(iss.Project)),
			CreatedAt:   iss.CreatedOn,
		})
	}

	return result
}
```

Extract the PUT of `UpdateIssueStatus` into `setStatusID`; `UpdateIssueStatus` ends with `return a.setStatusID(ctx, issueID, statusID)` and loses its two local types:

```go
// setStatusID PUTs only the status_id: a minimal payload, because sending
// every issue field (category_id:0 among them) triggers Redmine validation
// errors on issues with no category assigned.
func (a *redmineAdapter) setStatusID(ctx context.Context, issueID string, statusID int) error {
	type issueUpdate struct {
		//nolint:tagliatelle // Redmine need it
		StatusID int `json:"status_id"`
	}
	type body struct {
		Issue issueUpdate `json:"issue"`
	}

	return a.putIssue(ctx, issueID, "status", body{Issue: issueUpdate{StatusID: statusID}})
}
```

Add:

```go
const projectPageSize = 100

// project returns the single configured project.
func (a *redmineAdapter) project() (string, error) {
	if len(a.cfg.Projects) != 1 {
		return "", fmt.Errorf("redmine: exactly one project must be configured (got %d)", len(a.cfg.Projects))
	}

	return a.cfg.Projects[0].FarSlug, nil
}

// ListProjectIssues fetches every open issue of the configured project,
// whoever it is assigned to, walking offset until total_count.
func (a *redmineAdapter) ListProjectIssues(ctx context.Context) ([]tracker.Issue, error) {
	p, err := a.project()
	if err != nil {
		return nil, err
	}

	var out []tracker.Issue

	for offset := 0; ; offset += projectPageSize {
		path := fmt.Sprintf("/projects/%s/issues.json?status_id=open&limit=%d&offset=%d",
			url.PathEscape(p), projectPageSize, offset)

		var payload issuesResponse
		if _, err := a.getJSON(ctx, path, &payload); err != nil {
			return nil, fmt.Errorf("redmine: list issues of %q: %w", p, err)
		}

		out = append(out, toIssues(payload.Issues, p)...)

		if len(payload.Issues) == 0 || offset+len(payload.Issues) >= payload.TotalCount {
			break
		}
	}

	return out, nil
}

// CreateIssue creates an issue in the configured project via POST /issues.json.
func (a *redmineAdapter) CreateIssue(ctx context.Context, title, description string) (tracker.Issue, error) {
	p, err := a.project()
	if err != nil {
		return tracker.Issue{}, err
	}

	type newIssue struct {
		//nolint:tagliatelle // Redmine wire format
		ProjectID   string `json:"project_id"`
		Subject     string `json:"subject"`
		Description string `json:"description"`
	}

	buf, err := json.Marshal(struct {
		Issue newIssue `json:"issue"`
	}{newIssue{ProjectID: p, Subject: title, Description: description}})
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("redmine: marshal new issue: %w", err)
	}

	endpoint := strings.TrimRight(a.cfg.URL, "/") + "/issues.json"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("redmine: build create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", a.cfg.Token)

	resp, err := a.http.Do(req)
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("redmine: create issue: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return tracker.Issue{}, fmt.Errorf("redmine: create issue: unexpected HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Issue issue `json:"issue"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return tracker.Issue{}, fmt.Errorf("redmine: decode created issue: %w", err)
	}

	return toIssues([]issue{payload.Issue}, p)[0], nil
}

// SetIssueOpen closes the issue with the tracker's first closed status, or
// reopens it with its first status that is not closed: Redmine has no fixed
// status names.
func (a *redmineAdapter) SetIssueOpen(ctx context.Context, issueID string, open bool) error {
	statuses, err := a.issueStatuses(ctx)
	if err != nil {
		return err
	}

	for _, s := range statuses {
		if s.IsClosed != open {
			return a.setStatusID(ctx, issueID, s.ID)
		}
	}

	return fmt.Errorf("redmine: no status with is_closed=%t to set on issue %s", !open, issueID)
}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./tracker/redmine/... -v 2>&1 | tail -30`
Expected: PASS, including the existing `TestListIssues` and `TestUpdateIssueStatus`.

- [ ] **Step 5: Commit**

```bash
git add tracker/redmine
git commit -m "feat(redmine): list a project's issues, create an issue, open or close one"
```

---

### Task 7: The tracker interface and a stateful fake

**Files:**
- Modify: `tracker/tracker.go` (`Tracker`)
- Modify: `tracker/fake/fake.go`
- Create: `tracker/fake/fake_test.go`

**Interfaces:**
- Consumes: the three methods of Tasks 4 to 6.
- Produces: `tracker.Tracker` gains `ListProjectIssues(ctx) ([]Issue, error)`, `CreateIssue(ctx, title, description string) (Issue, error)`, `SetIssueOpen(ctx, issueID string, open bool) error`. On `*fake.Tracker`: fields `ProjectIssues []tracker.Issue`, `ListErr`, `CreateErr error`, `ListProjectCalls int`, `RecordedCreates []fake.Create`, `RecordedOpens []fake.Open`, `ClosingStatuses []string`; methods `CloseIssue(id string)`, `ReopenIssue(id string)`; types `fake.Create{Title, Description string}`, `fake.Open{IssueID string; Open bool}`. The zero value `&fake.Tracker{}` is usable.

- [ ] **Step 1: Write the failing test**

Create `tracker/fake/fake_test.go`:

```go
package fake

import (
	"errors"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

var _ tracker.Tracker = (*Tracker)(nil)

func TestFake_Mirror(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("a created issue is numbered, recorded and listed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{}
		iss, err := ft.CreateIssue(ctx, "Bug", "Steps")
		if err != nil || iss.ID != "1" || iss.Status != "open" {
			t.Fatalf("CreateIssue = %+v, %v", iss, err)
		}
		if len(ft.RecordedCreates) != 1 || ft.RecordedCreates[0] != (Create{Title: "Bug", Description: "Steps"}) {
			t.Errorf("RecordedCreates = %+v", ft.RecordedCreates)
		}
		listed, _ := ft.ListProjectIssues(ctx)
		if len(listed) != 1 || listed[0].ID != "1" || ft.ListProjectCalls != 1 {
			t.Errorf("listed = %+v, calls = %d", listed, ft.ListProjectCalls)
		}
	})

	t.Run("closing unlists the issue and reopening lists it again", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "42", Subject: "A"}}}
		if err := ft.SetIssueOpen(ctx, "42", false); err != nil {
			t.Fatalf("SetIssueOpen: %v", err)
		}
		listed, _ := ft.ListProjectIssues(ctx)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if len(listed) != 0 || !closed {
			t.Errorf("after close: listed = %+v, closed = %v", listed, closed)
		}

		ft.ReopenIssue("42")
		listed, _ = ft.ListProjectIssues(ctx)
		closed, _ = ft.IsIssueClosed(ctx, "42")
		if len(listed) != 1 || closed {
			t.Errorf("after reopen: listed = %+v, closed = %v", listed, closed)
		}
	})

	t.Run("SetIssueOpen is recorded, CloseIssue is not", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "42"}, {ID: "43"}}}
		_ = ft.SetIssueOpen(ctx, "42", false)
		ft.CloseIssue("43")
		if len(ft.RecordedOpens) != 1 || ft.RecordedOpens[0] != (Open{IssueID: "42"}) {
			t.Errorf("RecordedOpens = %+v", ft.RecordedOpens)
		}
	})

	t.Run("a closing status closes the issue, another one renames its status", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{
			ProjectIssues:   []tracker.Issue{{ID: "42", Status: "New"}, {ID: "43", Status: "New"}},
			ClosingStatuses: []string{"Closed"},
		}
		_ = ft.UpdateIssueStatus(ctx, "42", "Closed")
		_ = ft.UpdateIssueStatus(ctx, "43", "In Progress")

		listed, _ := ft.ListProjectIssues(ctx)
		if len(listed) != 1 || listed[0].ID != "43" || listed[0].Status != "In Progress" {
			t.Errorf("listed = %+v", listed)
		}
	})

	t.Run("ListErr and CreateErr are returned", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("boom")
		ft := &Tracker{ListErr: boom, CreateErr: boom}
		if _, err := ft.ListProjectIssues(ctx); !errors.Is(err, boom) {
			t.Errorf("ListProjectIssues err = %v", err)
		}
		if _, err := ft.CreateIssue(ctx, "x", ""); !errors.Is(err, boom) {
			t.Errorf("CreateIssue err = %v", err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./tracker/fake/ -v`
Expected: build failure, `unknown field ProjectIssues`.

- [ ] **Step 3: Implement the fake**

In `tracker/fake/fake.go` (add `slices`, `strconv` and `time` to the imports), add to the `Tracker` struct:

```go
	// ProjectIssues is what ListProjectIssues returns: the project's open
	// issues. CreateIssue, SetIssueOpen, CloseIssue and ReopenIssue keep it
	// and Closed in step, so the fake behaves like one tracker over time.
	ProjectIssues []tracker.Issue
	// ListErr and CreateErr, when non-nil, are returned by ListProjectIssues
	// and CreateIssue.
	ListErr, CreateErr error
	// ListProjectCalls counts the ListProjectIssues calls.
	ListProjectCalls int
	RecordedCreates  []Create
	RecordedOpens    []Open
	// ClosingStatuses are the status names that close an issue when
	// UpdateIssueStatus applies them. Any other name becomes the issue's
	// listed status.
	ClosingStatuses []string

	nextID  int
	shelved map[string]tracker.Issue // closed issues, by ID
```

Add the types and methods:

```go
// Create captures one CreateIssue call.
type Create struct {
	Title, Description string
}

// Open captures one SetIssueOpen call.
type Open struct {
	IssueID string
	Open    bool
}

// ListProjectIssues returns a snapshot of the open issues.
func (t *Tracker) ListProjectIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ListProjectCalls++
	if t.ListErr != nil {
		return nil, t.ListErr
	}

	return slices.Clone(t.ProjectIssues), nil
}

// CreateIssue records the call and adds an open issue numbered 1, 2, 3….
func (t *Tracker) CreateIssue(_ context.Context, title, description string) (tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.CreateErr != nil {
		return tracker.Issue{}, t.CreateErr
	}

	t.nextID++
	iss := tracker.Issue{
		TrackerType: "fake", ID: strconv.Itoa(t.nextID), Subject: title, Description: description,
		Status: "open", CreatedAt: time.Now().UTC(),
	}
	t.RecordedCreates = append(t.RecordedCreates, Create{Title: title, Description: description})
	t.ProjectIssues = append(t.ProjectIssues, iss)

	return iss, nil
}

// SetIssueOpen records the call and opens or closes the issue.
func (t *Tracker) SetIssueOpen(_ context.Context, issueID string, open bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedOpens = append(t.RecordedOpens, Open{IssueID: issueID, Open: open})
	t.setOpen(issueID, open)

	return nil
}

// CloseIssue closes the issue the way a person does in the tracker's own UI:
// nothing is recorded.
func (t *Tracker) CloseIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, false)
}

// ReopenIssue is the counterpart of CloseIssue.
func (t *Tracker) ReopenIssue(issueID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.setOpen(issueID, true)
}

// setOpen moves the issue between ProjectIssues and the closed shelf. The
// caller holds t.mu.
func (t *Tracker) setOpen(issueID string, open bool) {
	if t.Closed == nil {
		t.Closed = make(map[string]bool)
	}
	if t.shelved == nil {
		t.shelved = make(map[string]tracker.Issue)
	}

	t.Closed[issueID] = !open

	i := slices.IndexFunc(t.ProjectIssues, func(iss tracker.Issue) bool { return iss.ID == issueID })
	switch {
	case !open && i >= 0:
		t.shelved[issueID] = t.ProjectIssues[i]
		t.ProjectIssues = slices.Delete(t.ProjectIssues, i, i+1)
	case open && i < 0:
		if iss, ok := t.shelved[issueID]; ok {
			t.ProjectIssues = append(t.ProjectIssues, iss)
			delete(t.shelved, issueID)
		}
	}
}
```

`UpdateIssueStatus` keeps recording, then applies the status (run `impact` on it first):

```go
	t.RecordedUpdates = append(t.RecordedUpdates, Update{IssueID: issueID, StatusName: statusName})

	if slices.Contains(t.ClosingStatuses, statusName) {
		t.setOpen(issueID, false)

		return nil
	}

	for i := range t.ProjectIssues {
		if t.ProjectIssues[i].ID == issueID {
			t.ProjectIssues[i].Status = statusName
		}
	}

	return nil
```

- [ ] **Step 4: Extend the interface**

In `tracker/tracker.go`, add to `Tracker`:

```go
	// ListProjectIssues retrieves every open issue of the single configured
	// project, whoever it is assigned to. It is the issue mirror's listing.
	ListProjectIssues(ctx context.Context) ([]Issue, error)
	// CreateIssue creates an issue in the single configured project and
	// returns it with the ID and status the tracker gave it.
	CreateIssue(ctx context.Context, title, description string) (Issue, error)
	// SetIssueOpen reopens (open) or closes issueID, with whatever status the
	// tracker uses for that.
	SetIssueOpen(ctx context.Context, issueID string, open bool) error
```

- [ ] **Step 5: Run the tests**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./tracker/... ./cmd/... 2>&1 | tail -20`
Expected: PASS. A build error naming another type that implements `tracker.Tracker` in a test (a local stub) means that stub needs the three methods; give it the fake's bodies or embed `*fake.Tracker`.

- [ ] **Step 6: Commit**

```bash
git add tracker/tracker.go tracker/fake
git commit -m "feat(tracker): the mirror calls join the interface, the fake keeps state"
```

---

### Task 8: Issues read in three git processes, pushed in one

A reconcile loads every issue and pushes every issue it changed. Today `List`
runs two or three git processes per issue (`Load`) and `Push` one `git push`
per issue. This task gives issues what branches and reviews already have.

**Files:**
- Modify: `git/chain_ref_sync.go` (after `PushChainRef`)
- Modify: `issue/repo.go` (`List`, `Load`, `Push`)
- Test: `git/issue_ref_sync_test.go`, `issue/repo_test.go`

**Interfaces:**
- Consumes: existing `ReadAllChains`, `chainTips`, `runInteractive`, `outputStdin`, `Fetch`.
- Produces: `func (c *Client) PushChainRefs(ctx context.Context, ns ChainRefs, ids []string) error`; `func issue.PushAll(ctx context.Context, c *git.Client, ids []string) error`; `List` reads all chains at once and skips, with a warning, a ref that does not name its chain's root.

- [ ] **Step 1: Write the failing git test**

Append to `git/issue_ref_sync_test.go` (add `fmt` to the imports if missing):

```go
func TestPushChainRefs(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	ctx := t.Context()

	var ids []string
	for i := range 3 {
		root, err := alice.WriteChainRoot(ctx, []byte(fmt.Sprintf(`{"type":"create","n":%d}`, i)), "create", false)
		if err != nil {
			t.Fatalf("WriteChainRoot: %v", err)
		}
		if err := alice.PublishChainRoot(ctx, IssueRefs, root, root); err != nil {
			t.Fatalf("PublishChainRoot: %v", err)
		}
		ids = append(ids, root)
	}

	t.Run("no ids is a no-op", func(t *testing.T) {
		if err := alice.PushChainRefs(ctx, IssueRefs, nil); err != nil {
			t.Errorf("PushChainRefs(nil) = %v", err)
		}
	})

	err := alice.PushChainRefs(ctx, IssueRefs, ids)

	t.Run("one push sends every ref", func(t *testing.T) {
		if err != nil {
			t.Fatalf("PushChainRefs: %v", err)
		}
		if got := originRefs(t, originDir, "refs/zf/issues/"); len(got) != 3 {
			t.Errorf("origin has %d issue refs, want 3: %v", len(got), got)
		}
	})
	t.Run("the tracking refs are moved", func(t *testing.T) {
		for _, id := range ids {
			if pushed, err := alice.ChainRefPushed(ctx, IssueRefs, id); err != nil || !pushed {
				t.Errorf("ChainRefPushed(%s) = %v, %v", id, pushed, err)
			}
		}
	})
	t.Run("an unknown id is an error", func(t *testing.T) {
		if err := alice.PushChainRefs(ctx, IssueRefs, []string{"0000000000000000000000000000000000000000"}); err == nil {
			t.Error("PushChainRefs: want an error, got nil")
		}
	})
	t.Run("a ref that moved on the remote is rejected", func(t *testing.T) {
		// Point origin's first ref at another chain's commit: alice's tip is
		// no longer a fast-forward of it.
		mustGit(t, originDir, "update-ref", "refs/zf/issues/"+ids[0], ids[1])
		if err := alice.PushChainRefs(ctx, IssueRefs, ids[:2]); err == nil {
			t.Error("PushChainRefs: want a rejection, got nil")
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git/ -run TestPushChainRefs -v`
Expected: build failure, `alice.PushChainRefs undefined`.

- [ ] **Step 3: Implement `PushChainRefs`**

In `git/chain_ref_sync.go`, after `PushChainRef`:

```go
// PushChainRefs pushes the refs of chains ids in one git push, fast-forward
// only like PushChainRef, then moves their tracking refs in one update-ref.
// A rejected ref fails the call; the other refs may have gone through, and the
// next push reports them up to date. No-op without a remote or without ids.
func (c *Client) PushChainRefs(ctx context.Context, ns ChainRefs, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	tips, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return err
	}

	args := []string{"push", "--quiet", remote}
	var updates strings.Builder
	for _, id := range ids {
		tip, ok := tips[id]
		if !ok || !tip.commit {
			return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
		}

		ref := ns.prefix() + id
		args = append(args, ref+":"+ref)
		fmt.Fprintf(&updates, "update %s%s %s\n", ns.trackingPrefix(remote), id, tip.sha)
	}

	if err := c.runInteractive(ctx, c.root, args...); err != nil {
		return fmt.Errorf("push %d %s refs: %w", len(ids), ns.name, err)
	}

	if _, err := c.outputStdin(ctx, []byte(updates.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("update tracking refs: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run the git tests**

Run: `mise exec -- go test ./git/... -run "TestPushChainRefs|TestPushFetchSync|TestChainRef" -v 2>&1 | tail -30`
Expected: PASS.

- [ ] **Step 5: Write the failing issue tests**

Append to `issue/repo_test.go`:

```go
func TestPushAll(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
	ctx := t.Context()

	first, err := Create(ctx, a, NewIssue{Title: "First", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := Create(ctx, a, NewIssue{Title: "Second", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("both issues reach the remote in one push", func(t *testing.T) {
		if err := PushAll(ctx, a, []string{first.ID, second.ID}); err != nil {
			t.Fatalf("PushAll: %v", err)
		}
		if _, err := Fetch(ctx, b); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if records, _, _ := List(ctx, b); len(records) != 2 {
			t.Errorf("bob has %d issues, want 2", len(records))
		}
	})

	t.Run("a rejected push is merged and retried", func(t *testing.T) {
		if err := Append(ctx, b, first.ID, &Op{Type: OpAddComment, Body: "from bob"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := Push(ctx, b, first.ID); err != nil {
			t.Fatalf("Push: %v", err)
		}
		for _, id := range []string{first.ID, second.ID} {
			if err := Append(ctx, a, id, &Op{Type: OpAddComment, Body: "from alice"}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}

		if err := PushAll(ctx, a, []string{first.ID, second.ID}); err != nil {
			t.Fatalf("PushAll after a divergence: %v", err)
		}
		rec, err := Load(ctx, a, first.ID)
		if err != nil || len(rec.Comments) != 2 {
			t.Errorf("alice's first issue has %d comments (%v), want both", len(rec.Comments), err)
		}
	})

	t.Run("no ids and no remote are no-ops", func(t *testing.T) {
		alone := newRepo(t, "carol", "")
		rec, err := Create(ctx, alone, NewIssue{Title: "Alone", BranchType: "fix"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := PushAll(ctx, alone, nil); err != nil {
			t.Errorf("PushAll(nil) = %v", err)
		}
		if err := PushAll(ctx, alone, []string{rec.ID}); err != nil {
			t.Errorf("PushAll without a remote = %v", err)
		}
	})
}

// Review Focus 6.
func TestList_SkipsARefThatIsNotARoot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	good, err := Create(ctx, c, NewIssue{Title: "Good", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Append(ctx, c, good.ID, &Op{Type: OpAddComment, Body: "second commit"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	tip, err := c.ChainTip(ctx, git.IssueRefs, good.ID)
	if err != nil {
		t.Fatalf("ChainTip: %v", err)
	}
	// A ref named after a commit that is not its chain's root.
	if err := c.PublishChainRoot(ctx, git.IssueRefs, tip, tip); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	records, warnings, err := List(ctx, c)

	t.Run("the good issue is listed", func(t *testing.T) {
		if err != nil || len(records) != 1 || records[0].ID != good.ID {
			t.Errorf("records = %+v, err = %v", records, err)
		}
	})
	t.Run("the bad ref is a warning naming it", func(t *testing.T) {
		if len(warnings) != 1 || !strings.Contains(warnings[0], tip) {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("Load refuses the bad ref", func(t *testing.T) {
		if _, err := Load(ctx, c, tip); !errors.Is(err, git.ErrIssueRefCorrupt) {
			t.Errorf("Load = %v, want ErrIssueRefCorrupt", err)
		}
	})
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestPushAll|TestList_SkipsARefThatIsNotARoot" -v`
Expected: build failure, `undefined: PushAll`.

- [ ] **Step 7: Implement in `issue/repo.go`**

Run `impact` on `List`, `Load` and `Push` first (every command reads issues through them).

Replace `Load` and `List`, and add `fold`:

```go
// fold decodes and folds the commits of chain id. ok is false when none of
// them is a root named id: the ref does not name its chain's root. Malformed
// commits are skipped and named in warnings and in Record.Warnings.
func fold(id string, commits []git.ChainCommit) (rec Record, warnings []string, ok bool) {
	ops := make([]Op, 0, len(commits))
	for _, commit := range commits {
		if commit.ID == id && len(commit.Parents) == 0 {
			ok = true
		}

		op, decoded := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !decoded {
			warnings = append(warnings, fmt.Sprintf("WARN: issue %s: skipping malformed op %s", id, commit.ID))
		}
		ops = append(ops, op)
	}

	if !ok {
		return Record{}, nil, false
	}

	rec = Fold(id, ops)
	rec.Warnings = warnings

	return rec, warnings, true
}

// Load reads the chain of issue id and folds it. Malformed commits are skipped
// and named in Record.Warnings.
func Load(ctx context.Context, c *git.Client, id string) (Record, error) {
	commits, err := c.ReadChainCommits(ctx, git.IssueRefs, id)
	if err != nil {
		return Record{}, fmt.Errorf("read issue %s: %w", id, err)
	}

	rec, _, ok := fold(id, commits)
	if !ok {
		return Record{}, fmt.Errorf("read issue %s: %w", id, git.ErrIssueRefCorrupt)
	}

	return rec, nil
}

// List loads every local issue, newest first, in three git processes whatever
// their number. A ref that is not a commit chain or does not name its chain's
// root is skipped; warnings names it, along with every malformed op met on
// the way.
func List(ctx context.Context, c *git.Client) (records []Record, warnings []string, err error) {
	chains, legacy, err := c.ReadAllChains(ctx, git.IssueRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list issues: %w", err)
	}

	for _, id := range legacy {
		warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: not a commit chain", id))
	}

	// Sorted IDs keep the order of issues created in the same second stable.
	ids := slices.Sorted(maps.Keys(chains))

	records = make([]Record, 0, len(chains))
	for _, id := range ids {
		rec, w, ok := fold(id, chains[id])
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: %v", id, git.ErrIssueRefCorrupt))

			continue
		}

		warnings = append(warnings, w...)
		records = append(records, rec)
	}

	slices.SortStableFunc(records, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return records, warnings, nil
}
```

(Add `maps` to the imports.)

Replace `Push` and add `PushAll`:

```go
// Push pushes issue id. See PushAll.
func Push(ctx context.Context, c *git.Client, id string) error {
	return PushAll(ctx, c, []string{id})
}

// PushAll pushes the issues ids in one git push. A rejected push (someone
// pushed first) triggers one fetch, merge and retry. No-op without a remote
// or without ids.
func PushAll(ctx context.Context, c *git.Client, ids []string) error {
	firstErr := c.PushChainRefs(ctx, git.IssueRefs, ids)
	if firstErr == nil {
		return nil
	}

	if _, err := Fetch(ctx, c); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushChainRefs(ctx, git.IssueRefs, ids); err != nil {
		return fmt.Errorf("push %d issue(s) after merge: %w", len(ids), err)
	}

	return nil
}
```

- [ ] **Step 8: Run the tests**

Run: `mise exec -- go test ./issue/... ./cmd/issue/... 2>&1 | tail -30`
Expected: PASS. A test that matched the old "push issue %s after merge" wording needs the new one.

- [ ] **Step 9: Commit**

```bash
git add git/chain_ref_sync.go git/issue_ref_sync_test.go issue/repo.go issue/repo_test.go
git commit -m "perf(issue): list every issue in three git processes, push many in one"
```

---

### Task 9: Reconcile

**Files:**
- Create: `issue/mirror.go`
- Modify: `issue/repo.go` (`Resolve`)
- Test: `issue/mirror_test.go` (create)

**Interfaces:**
- Consumes: `WriteFixedChainRoot` (Task 3); the fold of Task 2; `tracker.Tracker` with the three calls and `*fake.Tracker` (Task 7); `PushAll` and the batched `List` (Task 8); existing `Append`, `Create`, `Load`.
- Produces:
  - `type Mirror struct{ Tracker tracker.Tracker; Type, Project string }`
  - `func (m *Mirror) Reconcile(ctx context.Context, c *git.Client) (MirrorResult, error)`: a nil `*Mirror` is a no-op.
  - `type MirrorResult struct{ Imported, Exported, Pulled, Pushed int; Warnings []string }`
  - `Resolve` finds a record by its exact tracker number.

- [ ] **Step 1: Write the failing tests**

Create `issue/mirror_test.go`:

```go
package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

var mirrorCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func newTestMirror(ft *fake.Tracker) *Mirror {
	return &Mirror{Tracker: ft, Type: "fake", Project: "zf"}
}

func trackerIssue(id, title string) tracker.Issue {
	return tracker.Issue{TrackerType: "fake", ID: id, Subject: title, Status: "open", CreatedAt: mirrorCreated}
}

func mustReconcile(t *testing.T, m *Mirror, c *git.Client) MirrorResult {
	t.Helper()

	res, err := m.Reconcile(t.Context(), c)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	return res
}

// onlyRecord returns the single issue of the repository.
func onlyRecord(t *testing.T, c *git.Client) Record {
	t.Helper()

	records, _, err := List(t.Context(), c)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}

	return records[0]
}

func opCount(t *testing.T, c *git.Client, id string) int {
	t.Helper()

	commits, err := c.ReadChainCommits(t.Context(), git.IssueRefs, id)
	if err != nil {
		t.Fatalf("ReadChainCommits: %v", err)
	}

	return len(commits)
}

func TestReconcile_NilMirror(t *testing.T) {
	t.Parallel()

	var m *Mirror
	res, err := m.Reconcile(t.Context(), newRepo(t, "alice", ""))

	t.Run("a nil mirror does nothing", func(t *testing.T) {
		if err != nil || res.Imported+res.Exported+res.Pulled+res.Pushed != 0 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})
}

func TestReconcile_Import(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "From tracker")
	iss.Description, iss.Status = "Body", "In Progress"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)

	res := mustReconcile(t, m, c)
	rec := onlyRecord(t, c)

	t.Run("one issue is imported", func(t *testing.T) {
		if res.Imported != 1 || res.Exported != 0 {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("the record carries the tracker's title, description and date", func(t *testing.T) {
		if rec.Title != "From tracker" || rec.Description != "Body" || !rec.CreatedAt.Equal(mirrorCreated) {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the record is open, linked and born in the tracker", func(t *testing.T) {
		want := TrackerLink{Type: "fake", Project: "zf", ID: "42", Born: true}
		if rec.State != StateOpen || rec.Tracker == nil || *rec.Tracker != want || rec.DisplayID() != "42" {
			t.Errorf("state = %q, tracker = %+v", rec.State, rec.Tracker)
		}
	})
	t.Run("the tracker state and status name are recorded", func(t *testing.T) {
		if rec.TrackerState != StateOpen || rec.TrackerStatus != "In Progress" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if again.Imported != 0 || opCount(t, c, rec.ID) != before || len(ft.RecordedCreates)+len(ft.RecordedOpens) != 0 {
			t.Errorf("again = %+v, ops %d → %d, tracker writes %d", again, before, opCount(t, c, rec.ID),
				len(ft.RecordedCreates)+len(ft.RecordedOpens))
		}
	})
}

func TestReconcile_ImportRootIsDeterministic(t *testing.T) {
	t.Parallel()

	// The same issue, its date written with sub-seconds in another zone.
	shifted := trackerIssue("42", "From tracker")
	shifted.CreatedAt = mirrorCreated.Add(300 * time.Millisecond).In(time.FixedZone("CEST", 2*3600))

	a, b := newRepo(t, "alice", ""), newRepo(t, "bob", "")
	mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}), a)
	mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{shifted}}), b)

	t.Run("two clones give the issue the same ID", func(t *testing.T) {
		if ida, idb := onlyRecord(t, a).ID, onlyRecord(t, b).ID; ida != idb {
			t.Errorf("IDs differ: %s vs %s", ida, idb)
		}
	})
}

func TestReconcile_Export(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{}
	m := newTestMirror(ft)

	created, err := Create(ctx, c, NewIssue{Title: "Local bug", Description: "Steps", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	closed, err := Create(ctx, c, NewIssue{Title: "Old", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Append(ctx, c, closed.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, m, c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the open record is created in the tracker", func(t *testing.T) {
		want := []fake.Create{{Title: "Local bug", Description: "Steps"}}
		if res.Exported != 1 || !slices.Equal(ft.RecordedCreates, want) {
			t.Errorf("res = %+v, creates = %+v", res, ft.RecordedCreates)
		}
	})
	t.Run("the record is linked and keeps its short hash", func(t *testing.T) {
		want := TrackerLink{Type: "fake", Project: "zf", ID: "1"}
		if rec.Tracker == nil || *rec.Tracker != want || rec.DisplayID() != rec.ShortID() {
			t.Errorf("tracker = %+v, display = %q", rec.Tracker, rec.DisplayID())
		}
	})
	t.Run("a record closed before it was exported stays local", func(t *testing.T) {
		if got, _ := Load(ctx, c, closed.ID); got.Tracker != nil {
			t.Errorf("tracker = %+v, want nil", got.Tracker)
		}
	})

	before := opCount(t, c, created.ID)
	again := mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if again.Exported != 0 || again.Imported != 0 || len(ft.RecordedCreates) != 1 || opCount(t, c, created.ID) != before {
			t.Errorf("again = %+v, creates = %d, ops %d → %d", again, len(ft.RecordedCreates), before, opCount(t, c, created.ID))
		}
	})
}

func TestReconcile_StateTable(t *testing.T) {
	t.Parallel()

	const o, cl = StateOpen, StateClosed

	rows := []struct {
		name          string
		l, r, tr      string // last recorded, repo, tracker
		want          string // the state both sides end in
		pulled        int
		pushed        int
		wantTrackerOp []fake.Open
	}{
		{"open open open: nobody moved", o, o, o, o, 0, 0, nil},
		{"open open closed: the tracker closed", o, o, cl, cl, 1, 0, nil},
		{"open closed open: the repo closed", o, cl, o, cl, 0, 1, []fake.Open{{IssueID: "42", Open: false}}},
		{"open closed closed: both closed", o, cl, cl, cl, 0, 0, nil},
		{"closed closed closed: nobody moved", cl, cl, cl, cl, 0, 0, nil},
		{"closed closed open: the tracker reopened", cl, cl, o, o, 1, 0, nil},
		{"closed open closed: the repo reopened", cl, o, cl, o, 0, 1, []fake.Open{{IssueID: "42", Open: true}}},
		{"closed open open: both reopened", cl, o, o, o, 0, 0, nil},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			c := newRepo(t, "alice", "")
			ctx := t.Context()
			ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
			m := newTestMirror(ft)

			// Reach the row: import (open/open/open), settle on closed if
			// the row starts there, then move each side away from l.
			mustReconcile(t, m, c)
			id := onlyRecord(t, c).ID
			if row.l == cl {
				ft.CloseIssue("42")
				mustReconcile(t, m, c)
			}
			if row.r != row.l {
				if err := Append(ctx, c, id, &Op{Type: OpSetState, Value: row.r}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if row.tr != row.l {
				if row.tr == cl {
					ft.CloseIssue("42")
				} else {
					ft.ReopenIssue("42")
				}
			}

			res := mustReconcile(t, m, c)
			rec, _ := Load(ctx, c, id)
			trackerClosed, _ := ft.IsIssueClosed(ctx, "42")

			if rec.State != row.want || rec.TrackerState != row.want {
				t.Errorf("repo state = %q, recorded tracker state = %q, want both %q", rec.State, rec.TrackerState, row.want)
			}
			if trackerClosed != (row.want == cl) {
				t.Errorf("tracker closed = %v, want %v", trackerClosed, row.want == cl)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed {
				t.Errorf("pulled/pushed = %d/%d, want %d/%d", res.Pulled, res.Pushed, row.pulled, row.pushed)
			}
			if !slices.Equal(ft.RecordedOpens, row.wantTrackerOp) {
				t.Errorf("tracker writes = %+v, want %+v", ft.RecordedOpens, row.wantTrackerOp)
			}
		})
	}
}

func TestReconcile_LinkWithoutTrackerState(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("7", "Linked")}}

	created, err := Create(ctx, c, NewIssue{Title: "Linked", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "7"}
	if err := Append(ctx, c, created.ID, link); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, newTestMirror(ft), c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the missing state counts as open and is recorded", func(t *testing.T) {
		if rec.TrackerState != StateOpen || rec.State != StateOpen {
			t.Errorf("TrackerState = %q, State = %q", rec.TrackerState, rec.State)
		}
	})
	t.Run("nothing is imported, exported or written to the tracker", func(t *testing.T) {
		if res.Imported+res.Exported != 0 || len(ft.RecordedOpens)+len(ft.RecordedCreates) != 0 {
			t.Errorf("res = %+v, opens = %+v", res, ft.RecordedOpens)
		}
	})
}

func TestReconcile_TwoClonesImportOffline(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}

	// Neither clone has the other's refs when it imports.
	mustReconcile(t, newTestMirror(ft), a)
	mustReconcile(t, newTestMirror(ft), b)
	if _, err := Fetch(ctx, a); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	ra, rb := onlyRecord(t, a), onlyRecord(t, b)

	t.Run("both clones hold one and the same issue", func(t *testing.T) {
		if ra.ID != rb.ID {
			t.Errorf("IDs differ: %s vs %s", ra.ID, rb.ID)
		}
	})
	t.Run("the merged issue folds to the tracker's title and an open state", func(t *testing.T) {
		if ra.Title != "From tracker" || ra.State != StateOpen || ra.TrackerState != StateOpen {
			t.Errorf("record = %+v", ra)
		}
	})
}

func TestReconcile_DuplicateLink(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("1", "Exported"), trackerIssue("2", "Exported")}}

	created, err := Create(ctx, c, NewIssue{Title: "Exported", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, number := range []string{"1", "2"} {
		link := &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: number}
		if err := Append(ctx, c, created.ID, link); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	res := mustReconcile(t, newTestMirror(ft), c)

	t.Run("the duplicate tracker issue is not imported", func(t *testing.T) {
		if res.Imported != 0 || onlyRecord(t, c).ID != created.ID {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("a warning names the duplicate", func(t *testing.T) {
		if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "tracker issue 2 duplicates") }) {
			t.Errorf("warnings = %v", res.Warnings)
		}
	})
}

func TestReconcile_TrackerDown(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	boom := errors.New("connection refused")
	ft := &fake.Tracker{ListErr: boom}

	created, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := opCount(t, c, created.ID)

	_, err = newTestMirror(ft).Reconcile(ctx, c)

	t.Run("the listing error is returned", func(t *testing.T) {
		if !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing was written on either side", func(t *testing.T) {
		if opCount(t, c, created.ID) != before || len(ft.RecordedCreates) != 0 {
			t.Errorf("ops %d → %d, creates = %d", before, opCount(t, c, created.ID), len(ft.RecordedCreates))
		}
	})
}

func TestReconcile_ExportFailureIsAWarning(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{CreateErr: errors.New("403 forbidden")}

	created, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := newTestMirror(ft).Reconcile(ctx, c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the run succeeds with a warning", func(t *testing.T) {
		if err != nil || res.Exported != 0 || len(res.Warnings) != 1 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("the record stays unlinked for the next run", func(t *testing.T) {
		if rec.Tracker != nil {
			t.Errorf("tracker = %+v", rec.Tracker)
		}
	})
}

// Review Focus 1 to 4.
func TestReconcile_AwkwardTrackerInput(t *testing.T) {
	t.Parallel()

	t.Run("a title with quotes, a newline and non-ASCII text is imported unchanged", func(t *testing.T) {
		t.Parallel()

		const title = "Le \"login\" échoue\nsur 日本語 {\"v\":2}"
		c := newRepo(t, "alice", "")
		mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", title)}}), c)
		if got := onlyRecord(t, c).Title; got != title {
			t.Errorf("Title = %q, want %q", got, title)
		}
	})

	t.Run("an issue listed twice is imported once", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		twice := []tracker.Issue{trackerIssue("42", "Twice"), trackerIssue("42", "Twice")}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: twice}), c)
		if res.Imported != 1 || onlyRecord(t, c).Title != "Twice" {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("an issue ID with a space is skipped with a warning and the others are imported", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		listed := []tracker.Issue{trackerIssue("PROJ 12", "Bad"), trackerIssue("42", "Good")}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: listed}), c)
		if res.Imported != 1 || len(res.Warnings) != 1 || onlyRecord(t, c).Title != "Good" {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("an issue without a creation date is skipped with a warning", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		undated := trackerIssue("42", "Undated")
		undated.CreatedAt = time.Time{}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{undated}}), c)
		if res.Imported != 0 || len(res.Warnings) != 1 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("a linked issue deleted from the tracker warns and writes nothing", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "Doomed")}}
		m := newTestMirror(ft)
		mustReconcile(t, m, c)
		rec := onlyRecord(t, c)
		before := opCount(t, c, rec.ID)

		ft.ProjectIssues = nil
		ft.Unknown = map[string]bool{"42": true}
		res := mustReconcile(t, m, c)

		if len(res.Warnings) != 1 || opCount(t, c, rec.ID) != before || onlyRecord(t, c).State != StateOpen {
			t.Errorf("res = %+v, ops %d → %d", res, before, opCount(t, c, rec.ID))
		}
	})
}

func TestResolve_TrackerNumber(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
	m := newTestMirror(ft)

	local, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustReconcile(t, m, c) // imports 42, exports Local as 1

	t.Run("an imported issue resolves by its tracker number", func(t *testing.T) {
		rec, err := Resolve(ctx, c, "42")
		if err != nil || rec.Title != "From tracker" {
			t.Errorf("Resolve(42) = %q, %v", rec.Title, err)
		}
	})
	t.Run("an exported issue resolves by its tracker number", func(t *testing.T) {
		rec, err := Resolve(ctx, c, "1")
		if err != nil || rec.ID != local.ID {
			t.Errorf("Resolve(1) = %q, %v", rec.ID, err)
		}
	})
	t.Run("a number no issue is linked to is not found", func(t *testing.T) {
		if _, err := Resolve(ctx, c, "999"); !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestReconcile|TestResolve_TrackerNumber" -v`
Expected: build failure, `undefined: Mirror`.

- [ ] **Step 3: Implement `issue/mirror.go`**

```go
package issue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// importRootTemplate is the op.json of an imported issue's root commit: at,
// tracker type, near slug, tracker number. FROZEN: the ID of every imported
// issue is the hash of a commit holding exactly these bytes, so that two
// clones importing the same tracker issue get the same chain. Changing one
// character makes every clone import its issues a second time.
const importRootTemplate = `{"v":1,"type":"create","at":"%s","tracker_type":"%s","project":"%s","tracker_id":"%s"}`

// plainTokenRe is what the template accepts unescaped.
var plainTokenRe = regexp.MustCompile(`^[0-9A-Za-z_-]+$`)

// Mirror ties the issues of this repository to one tracker project.
type Mirror struct {
	Tracker tracker.Tracker
	Type    string // tracker type, as configured
	Project string // near slug
}

// MirrorResult summarizes one Reconcile.
type MirrorResult struct {
	Imported, Exported int
	Pulled, Pushed     int      // state changes: tracker → repo, repo → tracker
	Warnings           []string // one line per issue that could not be handled
}

type stateMove int

const (
	moveNone   stateMove = iota // no op written
	moveNoted                   // only the tracker state seen was recorded
	movePulled                  // the tracker moved, the repo followed
	movePushed                  // the repo moved, the tracker followed
)

// owns reports whether rec is mirrored with this tracker project.
func (m *Mirror) owns(rec *Record) bool {
	return rec.Tracker != nil && rec.Tracker.Type == m.Type && rec.Tracker.Project == m.Project
}

// Reconcile mirrors the issues with the tracker project: it imports the
// tracker's open issues that have no record, creates a tracker issue for each
// open record that has none, syncs open/closed both ways, and pushes the
// records it changed in one push. It reads every issue in three git processes
// and calls the tracker once for the listing, plus once per issue that closed
// there since the last run. The caller fetches the issue refs first. A failure to
// list the tracker ends the run before anything is written; a failure on one
// issue is a line in MirrorResult.Warnings. A nil Mirror is a no-op.
func (m *Mirror) Reconcile(ctx context.Context, c *git.Client) (MirrorResult, error) {
	var res MirrorResult
	if m == nil {
		return res, nil
	}

	listed, err := m.Tracker.ListProjectIssues(ctx)
	if err != nil {
		return res, fmt.Errorf("list tracker issues: %w", err)
	}

	records, warnings, err := List(ctx, c)
	if err != nil {
		return res, err
	}
	res.Warnings = warnings

	warn := func(format string, args ...any) {
		res.Warnings = append(res.Warnings, "WARN: "+fmt.Sprintf(format, args...))
	}

	open := make(map[string]*tracker.Issue, len(listed))
	for i := range listed {
		open[listed[i].ID] = &listed[i]
	}

	linked := make(map[string]bool, len(records)) // tracker numbers a record links to
	duplicateOf := make(map[string]string)        // losing tracker number → its record
	for i := range records {
		rec := &records[i]
		if !m.owns(rec) {
			continue
		}
		linked[rec.Tracker.ID] = true
		for _, number := range rec.DuplicateTrackerIDs {
			duplicateOf[number] = rec.DisplayID()
		}
	}

	var changed []string

	for i := range listed {
		iss := &listed[i]
		if linked[iss.ID] {
			continue
		}
		linked[iss.ID] = true // a listing may name an issue twice

		if owner, dup := duplicateOf[iss.ID]; dup {
			warn("tracker issue %s duplicates the one linked to issue %s: close it in the tracker", iss.ID, owner)

			continue
		}

		id, err := m.importIssue(ctx, c, iss)
		if err != nil {
			warn("import tracker issue %s: %v", iss.ID, err)

			continue
		}
		res.Imported++
		changed = append(changed, id)
	}

	for i := range records {
		rec := &records[i]

		switch {
		case rec.Tracker == nil && rec.State == StateOpen:
			if err := m.export(ctx, c, rec); err != nil {
				warn("export issue %s: %v", rec.DisplayID(), err)

				continue
			}
			res.Exported++
			changed = append(changed, rec.ID)
		case m.owns(rec):
			move, err := m.syncState(ctx, c, rec, open[rec.Tracker.ID])
			if err != nil {
				warn("sync issue %s: %v", rec.DisplayID(), err)

				continue
			}

			switch move {
			case movePulled:
				res.Pulled++
			case movePushed:
				res.Pushed++
			case moveNone, moveNoted:
			}
			if move != moveNone {
				changed = append(changed, rec.ID)
			}
		}
	}

	// One push for every record touched; one line, not one per issue, when
	// the remote is off.
	if err := PushAll(ctx, c, changed); err != nil {
		warn("issues saved locally but not pushed (run `git zf issue sync` later): %v", err)
	}

	return res, nil
}

// importIssue writes the chain of a tracker issue that has no record and
// returns its ID.
func (m *Mirror) importIssue(ctx context.Context, c *git.Client, iss *tracker.Issue) (string, error) {
	if !plainTokenRe.MatchString(iss.ID) || !plainTokenRe.MatchString(m.Type) || !plainTokenRe.MatchString(m.Project) {
		return "", fmt.Errorf("unsupported characters in %q, %q or %q", iss.ID, m.Type, m.Project)
	}
	if iss.CreatedAt.IsZero() {
		return "", errors.New("the tracker reports no creation date")
	}

	at := iss.CreatedAt.UTC().Truncate(time.Second)
	payload := fmt.Sprintf(importRootTemplate, at.Format(time.RFC3339), m.Type, m.Project, iss.ID)

	id, err := c.WriteFixedChainRoot(ctx, []byte(payload), OpCreate, at)
	if err != nil {
		return "", fmt.Errorf("write root: %w", err)
	}

	tip, err := c.ChainTip(ctx, git.IssueRefs, id)
	if err != nil {
		return "", fmt.Errorf("read ref: %w", err)
	}
	if tip != "" {
		return id, nil // the chain is already here
	}

	if err := c.PublishChainRoot(ctx, git.IssueRefs, id, id); err != nil {
		return "", fmt.Errorf("publish: %w", err)
	}

	ops := []*Op{{Type: OpSetTitle, Value: iss.Subject}}
	if iss.Description != "" {
		ops = append(ops, &Op{Type: OpSetDescription, Value: iss.Description})
	}
	ops = append(ops, &Op{Type: OpTrackerState, Value: StateOpen, Status: iss.Status})

	return id, appendAll(ctx, c, id, ops...)
}

// export creates the tracker issue of a record that has none and links them.
func (m *Mirror) export(ctx context.Context, c *git.Client, rec *Record) error {
	created, err := m.Tracker.CreateIssue(ctx, rec.Title, rec.Description)
	if err != nil {
		return fmt.Errorf("create tracker issue: %w", err)
	}

	return appendAll(ctx, c, rec.ID,
		&Op{Type: OpLinkTracker, TrackerType: m.Type, Project: m.Project, TrackerID: created.ID},
		&Op{Type: OpTrackerState, Value: StateOpen, Status: created.Status})
}

// syncState compares three states of a linked record: l, the tracker state its
// chain last recorded (open when it recorded none); r, the repo state; t, the
// tracker state now. The side that differs from l moved, and the other one
// follows. When both differ from l they are equal: there is no conflict.
// listed is the issue in the open listing, nil when it is not there.
func (m *Mirror) syncState(
	ctx context.Context, c *git.Client, rec *Record, listed *tracker.Issue,
) (stateMove, error) {
	l, r := cmp.Or(rec.TrackerState, StateOpen), rec.State
	t, status := StateOpen, rec.TrackerStatus

	switch {
	case listed != nil:
		status = listed.Status
	case r == StateClosed && l == StateClosed:
		// Closed on both sides and still not listed: no need to ask.
		t = StateClosed
	default:
		closed, err := m.Tracker.IsIssueClosed(ctx, rec.Tracker.ID)
		if err != nil {
			return moveNone, fmt.Errorf("read tracker issue %s: %w", rec.Tracker.ID, err)
		}
		if closed {
			t, status = StateClosed, StateClosed
		}
	}

	seen := &Op{Type: OpTrackerState, Value: t, Status: status}

	switch {
	case t != l && r != t:
		return movePulled, appendAll(ctx, c, rec.ID, &Op{Type: OpSetState, Value: t}, seen)
	case t != l:
		return moveNoted, appendAll(ctx, c, rec.ID, seen)
	case r != t:
		if err := m.Tracker.SetIssueOpen(ctx, rec.Tracker.ID, r == StateOpen); err != nil {
			return moveNone, fmt.Errorf("set tracker issue %s %s: %w", rec.Tracker.ID, r, err)
		}
		// The status name a reopened issue got is read from the next listing.
		seen.Value, seen.Status = r, ""
		if r == StateClosed {
			seen.Status = StateClosed
		}

		return movePushed, appendAll(ctx, c, rec.ID, seen)
	case rec.TrackerState == "" || status != rec.TrackerStatus:
		return moveNoted, appendAll(ctx, c, rec.ID, seen)
	}

	return moveNone, nil
}

// appendAll appends ops to issue id, in order.
func appendAll(ctx context.Context, c *git.Client, id string, ops ...*Op) error {
	for _, op := range ops {
		if err := Append(ctx, c, id, op); err != nil {
			return err
		}
	}

	return nil
}
```

- [ ] **Step 4: Implement `Resolve` by tracker number**

In `issue/repo.go` (run `impact` on `Resolve` first; add `regexp` to the imports), add the variable and insert the block right after the exact-ID check (`if slices.Contains(ids, query) { return Load(ctx, c, query) }`):

```go
// trackerNumberRe matches what GitHub, Forgejo and Redmine use as issue IDs.
var trackerNumberRe = regexp.MustCompile(`^[0-9]+$`)
```

```go
	// An exact tracker number wins over an ID prefix made of digits.
	// ponytail: loads every issue to find one number; keep an index of the
	// numbers if repositories with thousands of issues make this slow.
	if trackerNumberRe.MatchString(query) {
		records, _, err := List(ctx, c)
		if err != nil {
			return Record{}, err
		}
		for i := range records {
			if records[i].Tracker != nil && records[i].Tracker.ID == query {
				return records[i], nil
			}
		}
	}
```

Update the doc comment of `Resolve`: "Resolve finds the issue designated by query: a full ID, the number of the tracker issue it is mirrored with, or a unique ID prefix of at least 4 characters."

- [ ] **Step 5: Run the tests**

Run: `mise exec -- go test ./issue/... -v 2>&1 | tail -60`
Expected: PASS, every `TestReconcile_*` subtest and the existing `TestListAndResolve`, `TestResolve_Ambiguous`.

- [ ] **Step 6: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go issue/repo.go
git commit -m "feat(issue): reconcile the repository's issues with a tracker project"
```

---

### Task 10: Commands reconcile

**Files:**
- Modify: `cmd/issue/record.go` (helpers, `runCloseByID`, `closeRepoIssue`)
- Modify: `cmd/issue/new.go`, `cmd/issue/sync.go`, `cmd/issue/list.go`, `cmd/issue/show.go`, `cmd/issue/close.go`
- Modify: `issue/row.go`
- Create: `cmd/issue/mirror_e2e_test.go`
- Test (mechanical): every existing caller of `runNew`, `runSync`, `runCloseByID`, `closeRepoIssue`

**Interfaces:**
- Consumes: `issuepkg.Mirror`, `MirrorResult` (Task 9); `config.IssueTrackerConfig.Mirror`, `Projects` (Task 1); `*fake.Tracker` (Task 7).
- Produces, in package `cmd/issue`:
  - `func openMirror(cfg *config.AppConfig, errW io.Writer) *issuepkg.Mirror`
  - `func mirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issuepkg.Mirror`
  - `func reconcileIssues(ctx context.Context, client *git.Client, m *issuepkg.Mirror) issuepkg.MirrorResult`
  - `runNew(ctx, client, cfg, in, p, m)`, `runSync(ctx, client, m)`, `runCloseByID(ctx, client, query, m)`, `closeRepoIssue(ctx, client, ref, m)`: each gains a trailing `m *issuepkg.Mirror`.
  - `issueListInfra.mirror *issuepkg.Mirror`
  - `issuepkg.Row.TrackerID string` (JSON `tracker_id`)

- [ ] **Step 1: Write the failing tests**

Create `cmd/issue/mirror_e2e_test.go`:

```go
package issue

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newMirrorRig is a recordRig with the mirror on, backed by a fake tracker.
func newMirrorRig(t *testing.T) (*recordRig, *fake.Tracker, *issuepkg.Mirror) {
	t.Helper()

	rig := newRecordRig(t, "alice", "")
	rig.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "piprim/git-zf"}},
	}
	ft := &fake.Tracker{}

	return rig, ft, mirrorOf(rig.cfg, ft)
}

func fromTracker(id, title, status string) tracker.Issue {
	return tracker.Issue{
		TrackerType: "fake", ID: id, Subject: title, Status: status,
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}
}

func TestMirrorOf(t *testing.T) {
	t.Parallel()

	on := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "fake", Mirror: true, Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}
	off := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "fake", Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}

	t.Run("mirror on gives a mirror named by the tracker type and the near slug", func(t *testing.T) {
		t.Parallel()

		m := mirrorOf(on, &fake.Tracker{})
		if m == nil || m.Type != "fake" || m.Project != "zf" {
			t.Errorf("mirror = %+v", m)
		}
	})
	t.Run("mirror off gives nil", func(t *testing.T) {
		t.Parallel()

		if m := mirrorOf(off, &fake.Tracker{}); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
	t.Run("no tracker gives nil", func(t *testing.T) {
		t.Parallel()

		if m := mirrorOf(on, nil); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
}

// Review Focus 5.
func TestOpenMirror_TrackerCannotBeBuilt(t *testing.T) {
	t.Parallel()

	cfg := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "no-such-tracker", Mirror: true, Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}
	var errW bytes.Buffer

	m := openMirror(cfg, &errW)

	t.Run("the mirror is off", func(t *testing.T) {
		if m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
	t.Run("one warning says why", func(t *testing.T) {
		if got := errW.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "warning: issue mirror off") {
			t.Errorf("stderr = %q", got)
		}
	})
}

func TestMirror_NewCreatesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()

	err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug", Description: "Steps"}, nil, m)
	rec := rig.onlyRecord(t)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the tracker issue is created at once", func(t *testing.T) {
		want := []fake.Create{{Title: "Local bug", Description: "Steps"}}
		if !slices.Equal(ft.RecordedCreates, want) {
			t.Errorf("creates = %+v", ft.RecordedCreates)
		}
	})
	t.Run("the record is linked to it", func(t *testing.T) {
		if rec.Tracker == nil || rec.Tracker.ID != "1" || rec.Tracker.Born {
			t.Errorf("tracker = %+v", rec.Tracker)
		}
	})
}

func TestMirror_NewSurvivesATrackerFailure(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ft.ListErr = errors.New("connection refused")

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, m)

	t.Run("the issue is created locally", func(t *testing.T) {
		if err != nil || rig.onlyRecord(t).Title != "Local bug" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("the failure is a warning", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "warning: tracker mirror: ") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestMirror_ListReadsTheRepository(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "In Progress")}
	ft.Issues = []tracker.Issue{{TrackerType: "fake", ID: "99", Subject: "Assigned to me"}}
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	local := rig.onlyRecord(t)

	infra := issueListInfra{tracker: ft, mirror: m, stderr: rig.stderr, client: rig.client}
	rows, err := buildRows(ctx, infra, "")
	bySlug := make(map[string]issuepkg.Row, len(rows))
	for _, r := range rows {
		bySlug[r.IssueSlug] = r
	}

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
	})
	t.Run("the tracker's project issue is listed under its number with its status name", func(t *testing.T) {
		row, ok := bySlug["42"]
		if !ok || row.Title != "From tracker" || row.TrackerStatus == nil || *row.TrackerStatus != "In Progress" {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
	})
	t.Run("the repo-born issue shows its short hash and its new tracker number", func(t *testing.T) {
		row, ok := bySlug[local.ShortID()]
		if !ok || row.TrackerID != "1" {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
		if cell := issuepkg.RowCells(&row, false)[0]; cell != local.ShortID()+" (#1)" {
			t.Errorf("ID cell = %q", cell)
		}
	})
	t.Run("the assigned-to-me listing is not used", func(t *testing.T) {
		if _, ok := bySlug["99"]; ok || len(rows) != 2 {
			t.Errorf("rows = %+v", rows)
		}
	})
}

func TestMirror_ListWithTheMirrorOffIsUnchanged(t *testing.T) {
	t.Parallel()

	rig, ft, _ := newMirrorRig(t)
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	ft.Issues = []tracker.Issue{{TrackerType: "fake", ID: "99", Subject: "Assigned to me", Status: "open"}}

	rows, err := buildRows(t.Context(), issueListInfra{tracker: ft, stderr: rig.stderr, client: rig.client}, "")

	t.Run("only the assigned-to-me listing is shown and nothing is imported", func(t *testing.T) {
		if err != nil || len(rows) != 1 || rows[0].IssueSlug != "99" || ft.ListProjectCalls != 0 {
			t.Errorf("rows = %+v, err = %v, project listings = %d", rows, err, ft.ListProjectCalls)
		}
	})
}

func TestMirror_CloseByIDClosesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Duplicate"}, nil, m); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	err := runCloseByID(ctx, rig.client, rig.onlyRecord(t).ID, m)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runCloseByID: %v", err)
		}
	})
	t.Run("the tracker issue is closed at once", func(t *testing.T) {
		if !slices.Equal(ft.RecordedOpens, []fake.Open{{IssueID: "1", Open: false}}) {
			t.Errorf("tracker writes = %+v", ft.RecordedOpens)
		}
	})
}

func TestMirror_SyncPrintsTheCounts(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rig.stdout.Reset()

	err := runSync(ctx, rig.client, m)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runSync: %v", err)
		}
	})
	t.Run("the mirror line counts imports and exports", func(t *testing.T) {
		want := "Tracker mirror: 1 imported, 1 exported, 0 pulled, 0 pushed.\n"
		if !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})
	t.Run("without a mirror the line is absent", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runSync(ctx, rig.client, nil); err != nil || strings.Contains(rig.stdout.String(), "Tracker mirror") {
			t.Errorf("stdout = %q, err = %v", rig.stdout.String(), err)
		}
	})
}

func TestMirror_ShowByTrackerNumber(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	reconcileIssues(ctx, rig.client, m)
	calls := ft.ListProjectCalls
	rig.stdout.Reset()

	err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{"42"}, false)
	out := rig.stdout.String()

	t.Run("the issue is found by its tracker number", func(t *testing.T) {
		if err != nil || !strings.HasPrefix(out, "42  From tracker\n") {
			t.Errorf("stdout = %q, err = %v", out, err)
		}
	})
	t.Run("the link is printed", func(t *testing.T) {
		if !strings.Contains(out, "Tracker: fake zf #42\n") {
			t.Errorf("stdout = %q", out)
		}
	})
	t.Run("show does not call the tracker", func(t *testing.T) {
		if ft.ListProjectCalls != calls {
			t.Errorf("project listings = %d, want %d", ft.ListProjectCalls, calls)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/ -run "TestMirror|TestOpenMirror" -v`
Expected: build failure, `undefined: mirrorOf`.

- [ ] **Step 3: Implement the helpers**

In `cmd/issue/record.go` (add `github.com/piprim/git-zf/tracker` to the imports), after `pushIssue`:

```go
// mirrorOf returns the issue mirror over an already built tracker, or nil
// when the mirror is off or there is no tracker.
func mirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issuepkg.Mirror {
	tc := cfg.IssueTracker
	if !tc.Mirror || t == nil || len(tc.Projects) != 1 {
		return nil
	}

	return &issuepkg.Mirror{Tracker: t, Type: tc.Type, Project: tc.Projects[0].NearSlug}
}

// openMirror builds the tracker and returns the issue mirror, or nil when the
// mirror is off. A tracker that cannot be built is a warning: the command
// then works on local data.
func openMirror(cfg *config.AppConfig, errW io.Writer) *issuepkg.Mirror {
	if !cfg.IssueTracker.Mirror {
		return nil
	}

	t, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		fmt.Fprintf(errW, "warning: issue mirror off, could not initialize tracker: %v\n", err)

		return nil
	}

	return mirrorOf(cfg, t)
}

// reconcileIssues mirrors the issues with the tracker. A failure is a
// warning: the next run reconciles from the chains. A nil m is a no-op.
func reconcileIssues(ctx context.Context, client *git.Client, m *issuepkg.Mirror) issuepkg.MirrorResult {
	res, err := m.Reconcile(ctx, client)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: tracker mirror: %v\n", err)
	}
	printWarnings(client.IO().Err, res.Warnings)

	return res
}
```

- [ ] **Step 4: Wire `new`, `close <id>`, `sync`**

Run `impact` on `runNew`, `runSync`, `runCloseByID`, `closeRepoIssue` first.

`cmd/issue/new.go`: `runNew` gains a last parameter `m *issuepkg.Mirror`; after `pushIssue(ctx, client, rec.ID)` add `reconcileIssues(ctx, client, m)`. In `newRunE`: `return runNew(cmd.Context(), client, i.appConfig, in, p, openMirror(i.appConfig, client.IO().Err))`.

`cmd/issue/record.go`:
- `runCloseByID(ctx, client, query string, m *issuepkg.Mirror)`: after `pushIssue(ctx, client, rec.ID)` add `reconcileIssues(ctx, client, m)`.
- `closeRepoIssue(ctx, client, ref *branch.State, m *issuepkg.Mirror)`: after `pushIssue(ctx, client, id)` add `reconcileIssues(ctx, client, m)`. Extend its doc comment: "With a mirror, the tracker issue is then closed by the reconcile."

`cmd/issue/close.go`:
- line 184: `return runCloseByID(cmd.Context(), client, id, openMirror(i.appConfig, client.IO().Err))`.
- in `updateClosedStatus`: `closeRepoIssue(ctx, deps.client, existing, mirrorOf(deps.cfg, deps.tracker))`. (Task 11 reorders this function; here only the argument is added.)

`cmd/issue/sync.go`:

```go
func (i Issue) syncRunE(cmd *cobra.Command) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runSync(cmd.Context(), client, openMirror(i.appConfig, client.IO().Err))
}

func runSync(ctx context.Context, client *git.Client, m *issuepkg.Mirror) error {
	res, err := issuepkg.Sync(ctx, client)
	if err != nil {
		return fmt.Errorf("sync issues: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issues synced: %d merged, %d pushed.\n", res.Merged, res.Pushed)

	if m != nil {
		mr := reconcileIssues(ctx, client, m)
		fmt.Fprintf(client.IO().Out, "Tracker mirror: %d imported, %d exported, %d pulled, %d pushed.\n",
			mr.Imported, mr.Exported, mr.Pulled, mr.Pushed)
	}

	for _, line := range res.Failed {
		fmt.Fprintf(client.IO().Err, "WARN: not pushed: %s\n", line)
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d issue(s) could not be pushed", len(res.Failed))
	}

	return nil
}
```

Extend the `Long` of the sync command with: "With `issue-tracker.mirror` on, it then mirrors the issues with the tracker project: imports, exports, and open/closed both ways."

Fix every existing test call: `runNew(…, p)` → `runNew(…, p, nil)`, `runSync(ctx, client)` → `runSync(ctx, client, nil)`, `runCloseByID(ctx, client, q)` → `runCloseByID(ctx, client, q, nil)`. `mise exec -- go vet ./cmd/issue/` lists them.

- [ ] **Step 5: Wire `list` and the row**

Run `impact` on `buildRows`, `mergeRepoIssues` and `RowCells` first. `tui/issue.go:259` builds its table from `RowCells`: check that it does not read the first cell back as an issue ID (it searches cells as text; if a later line parses cell 0, leave the cell alone and render the number in the title instead, and say so).

`issue/row.go`: add to `Row` after `State`:

```go
	// TrackerID is the tracker's number for a repo-born issue that was
	// exported; "" otherwise (a tracker-born issue has it as IssueSlug).
	TrackerID string `json:"tracker_id"`
```

and in `RowCells` replace `cells := []string{r.IssueSlug}` with:

```go
	id := r.IssueSlug
	if r.TrackerID != "" {
		id += " (#" + r.TrackerID + ")"
	}
	cells := []string{id}
```

`cmd/issue/list.go` (add `cmp` to the imports):

`issueListInfra` gains:

```go
	// mirror is non-nil when the issues are mirrored with the tracker: the
	// list then reads the repository, never the assigned-to-me listing.
	mirror *issuepkg.Mirror
```

In `issueListRunE`, the `infra` literal gains `mirror: mirrorOf(ir.appConfig, t),`.

In `buildRows`, the condition becomes `if infra.tracker != nil && infra.mirror == nil {`.

In `mergeRepoIssues`, after `fetchIssues(ctx, infra.client)` add `reconcileIssues(ctx, infra.client, infra.mirror)`, and replace the two places that set the row's status from the record:

```go
		out[i].Labels, out[i].State, out[i].TrackerStatus = rec.Labels, rec.State, trackerStatusOf(rec)
		out[i].TrackerID = exportedNumber(rec)
```

```go
		out = append(out, issuepkg.Row{
			IssueSlug: rec.DisplayID(), Title: rec.Title,
			Labels: rec.Labels, State: rec.State, TrackerStatus: trackerStatusOf(rec),
			TrackerID: exportedNumber(rec),
		})
```

```go
// trackerStatusOf is the status shown for a repo issue: the tracker's status
// name when the issue is mirrored, its open/closed state otherwise.
func trackerStatusOf(rec *issuepkg.Record) *string {
	s := cmp.Or(rec.TrackerStatus, rec.State)

	return &s
}

// exportedNumber is the tracker number of a repo-born issue that was
// exported, "" otherwise.
func exportedNumber(rec *issuepkg.Record) string {
	if rec.Tracker == nil || rec.Tracker.Born {
		return ""
	}

	return rec.Tracker.ID
}
```

- [ ] **Step 6: Print the link in `show`**

In `cmd/issue/show.go`, `renderRecord`, after the `ID:` line:

```go
	if t := rec.Tracker; t != nil {
		fmt.Fprintf(w, "Tracker: %s %s #%s\n", t.Type, t.Project, t.ID)
	}
```

Update the `Long` of the show command: "`<id>` is the full ID, the number of the tracker issue it is mirrored with, or a unique prefix of at least 4 characters."

- [ ] **Step 7: Run the tests**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./cmd/issue/... ./issue/... ./tty/... ./tui/... 2>&1 | tail -30`
Expected: PASS. A list test that compares JSON rows byte for byte needs `"tracker_id":""` added.

- [ ] **Step 8: Commit**

```bash
git add cmd/issue issue/row.go
git commit -m "feat(issue): list, new, close <id> and sync mirror the issues with the tracker"
```

---

### Task 11: Merge close and start with mirrored issues

**Files:**
- Modify: `cmd/issue/close.go` (`updateClosedStatus`)
- Modify: `cmd/issueflow/start.go` (`getFromRepoOrUser`, new `fromImportedRecord`)
- Create: `cmd/issue/close_mirror_e2e_test.go`
- Test: `cmd/issue/start_record_e2e_test.go`

**Interfaces:**
- Consumes: `mirrorOf`, `reconcileIssues`, `fetchIssues`, `closeRepoIssue(ctx, client, ref, m)` (Task 10); `fake.Tracker.ClosingStatuses` (Task 7); `Record.Tracker.Born` (Task 2).
- Produces: no new exported name. Behaviour: a tracker-born branch never closes its record directly; an imported record starts like a tracker issue.

- [ ] **Step 1: Write the failing close tests**

Create `cmd/issue/close_mirror_e2e_test.go`:

```go
package issue

import (
	"slices"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// mirrorOn turns the mirror on for a close rig and returns it.
func mirrorOn(rig *closeTestRig) *issuepkg.Mirror {
	rig.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "piprim/git-zf"}},
	}

	return mirrorOf(rig.cfg, rig.tracker)
}

// closeTrackerBorn closes the rig's branch ABC-1, born from tracker issue
// ABC-1 and mirrored, picking status in the status picker.
func closeTrackerBorn(t *testing.T, status string) (*closeTestRig, issuepkg.Record, error) {
	t.Helper()

	rig := newCloseRig(t)
	ctx := t.Context()
	m := mirrorOn(rig)
	rig.tracker.ClosingStatuses = []string{"Closed"}
	rig.tracker.ProjectIssues = []tracker.Issue{{
		TrackerType: "fake", ID: "ABC-1", Subject: "Add thing", Status: "New",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec, err := issuepkg.Resolve(ctx, rig.client, importedID(t, rig))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", TrackerType: "fake", IssueID: rec.ID})

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: status,
		DeleteBranch:  true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)

	got, err := issuepkg.Load(ctx, rig.client, rec.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return rig, got, runErr
}

// importedID returns the full ID of the rig's only issue.
func importedID(t *testing.T, rig *closeTestRig) string {
	t.Helper()

	records, _, err := issuepkg.List(t.Context(), rig.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}

	return records[0].ID
}

func TestClose_Mirror_TrackerBornFollowsTheTracker(t *testing.T) {
	t.Parallel()

	rig, rec, runErr := closeTrackerBorn(t, "Closed")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the picked status is applied to the tracker", func(t *testing.T) {
		want := []fake.Update{{IssueID: "ABC-1", StatusName: "Closed"}}
		if !slices.Equal(rig.tracker.RecordedUpdates, want) {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
	t.Run("the record follows the tracker and is closed", func(t *testing.T) {
		if rec.State != issuepkg.StateClosed || rec.TrackerState != issuepkg.StateClosed {
			t.Errorf("State = %q, TrackerState = %q", rec.State, rec.TrackerState)
		}
	})
	t.Run("the mirror did not close the tracker issue itself", func(t *testing.T) {
		if len(rig.tracker.RecordedOpens) != 0 {
			t.Errorf("tracker writes = %+v", rig.tracker.RecordedOpens)
		}
	})
}

// On Redmine, "Resolved" is not a closed status: the mirror must not force a
// close over the status the operator picked.
func TestClose_Mirror_TrackerBornKeepsAnOpenStatus(t *testing.T) {
	t.Parallel()

	rig, rec, runErr := closeTrackerBorn(t, "In Progress")

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the record stays open with the picked status name", func(t *testing.T) {
		if rec.State != issuepkg.StateOpen || rec.TrackerStatus != "In Progress" {
			t.Errorf("State = %q, TrackerStatus = %q", rec.State, rec.TrackerStatus)
		}
	})
	t.Run("the tracker issue is not closed", func(t *testing.T) {
		closed, _ := rig.tracker.IsIssueClosed(t.Context(), "ABC-1")
		if closed || len(rig.tracker.RecordedOpens) != 0 {
			t.Errorf("closed = %v, tracker writes = %+v", closed, rig.tracker.RecordedOpens)
		}
	})
}

func TestClose_Mirror_RepoBornClosesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()
	m := mirrorOn(rig)

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Add thing", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.Reconcile(ctx, rig.client); err != nil { // exports it as tracker issue 1
		t.Fatalf("Reconcile: %v", err)
	}
	branchtest.Amend(t, rig.client, branch.Op{Branch: "ABC-1@feat@add-thing", IssueID: rec.ID})

	prompter := &scriptedPrompter{
		Branch:       rig.pickedBranchRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): close ABC-1\n"),
		DeleteBranch: true,
	}
	runErr := runClose(ctx, rig.deps(), prompter)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the record is closed", func(t *testing.T) {
		if got.State != issuepkg.StateClosed {
			t.Errorf("State = %q", got.State)
		}
	})
	t.Run("the tracker issue is closed by the mirror", func(t *testing.T) {
		if !slices.Equal(rig.tracker.RecordedOpens, []fake.Open{{IssueID: "1", Open: false}}) {
			t.Errorf("tracker writes = %+v", rig.tracker.RecordedOpens)
		}
	})
	t.Run("no status picker ran", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 0 {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/ -run "^TestClose_Mirror" -v`
Expected: FAIL. `TrackerBornFollowsTheTracker` fails on "the mirror did not close the tracker issue itself" or on the record state: today `closeRepoIssue` closes the record directly and nothing reconciles after the picker.

- [ ] **Step 3: Implement the merge-close rule**

In `cmd/issue/close.go`, run `impact` on `updateClosedStatus`, then replace its tail (from `existing, _ := branch.Load(…)` to the end of the function) with:

```go
	existing, _ := branch.Load(ctx, deps.client, picked.IssueSlug)
	mirror := mirrorOf(deps.cfg, deps.tracker)

	// A manual or repo-born issue (TrackerType == "") must not prompt even
	// when a tracker is configured: its record is closed directly, and with a
	// mirror the reconcile closes the tracker issue.
	if existing == nil || existing.TrackerType == "" {
		closeRepoIssue(ctx, deps.client, existing, mirror)

		return
	}

	// A tracker-born issue gets the status the operator picks. Its record, if
	// it has one, is never closed directly: it follows the tracker, so that a
	// status that leaves the issue open (Redmine's "Resolved") is not
	// overridden by a forced close.
	issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err, picked.IssueSlug, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)

	if mirror != nil {
		fetchIssues(ctx, deps.client)
		reconcileIssues(ctx, deps.client, mirror)
	}
```

Update the function's doc comment: add "With the issue mirror on, the record and the tracker issue are reconciled afterwards."

- [ ] **Step 4: Run the close tests**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v 2>&1 | tail -40`
Expected: PASS, the three new tests and the whole existing close suite (it is the regression net: with the mirror off, a branch never has both a tracker type and a repo issue ID, so the reordering changes nothing).

- [ ] **Step 5: Write the failing start test**

Append to `cmd/issue/start_record_e2e_test.go` (add `time` and `github.com/piprim/git-zf/tracker/fake` to the imports if missing):

```go
// An issue imported by the mirror has no branch type. Started from the repo
// picker, it asks the type with the tracker form and gets the branch a live
// tracker listing would have created.
func TestRunIssueStart_ImportedRecordStartsAsTrackerIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.tracker.Issues = nil // empty list ⇒ NotifyTrackerError ⇒ repo picker

	source := &fake.Tracker{ProjectIssues: []tracker.Issue{{
		TrackerType: "fake", ID: "42", Subject: "From tracker", Status: "open",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}}
	m := &issuepkg.Mirror{Tracker: source, Type: "fake", Project: "zf"}
	if _, err := m.Reconcile(ctx, rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	records, _, _ := issuepkg.List(ctx, rig.client)
	if len(records) != 1 {
		t.Fatalf("repo has %d issues, want 1", len(records))
	}
	imported := records[0]

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
	t.Run("the branch is named after the tracker number and the picked type", func(t *testing.T) {
		const want = "42@fix@from-tracker"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
	t.Run("the branch chain records the tracker origin and the issue ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, "42")
		if err != nil || ref == nil || ref.TrackerType != rig.cfg.IssueTracker.Type || ref.IssueID != imported.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("no second issue is created", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 1 {
			t.Errorf("repo has %d issues, want 1", len(records))
		}
	})
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/ -run TestRunIssueStart_ImportedRecordStartsAsTrackerIssue -v`
Expected: FAIL with `issue 42 has type "", which is not one of this clone's commit types`.

- [ ] **Step 7: Implement the start path**

In `cmd/issueflow/start.go` (run `impact` on `getFromRepoOrUser` first), in `getFromRepoOrUser`, the `if rec != nil {` block becomes:

```go
		if rec != nil {
			if rec.Tracker != nil && rec.Tracker.Born {
				return fromImportedRecord(ctx, p, rec, allowedTypes)
			}

			if err := checkRecordType(rec, allowedTypes); err != nil {
				return nil, err
			}

			return issueFromRecord(rec), nil
		}
```

and add after `issueFromRecord`:

```go
// fromImportedRecord converts an issue imported from the tracker. Its record
// has no branch type (the tracker has none), so the type is asked with the
// form a live tracker listing uses, offered this one issue. The result starts
// like a tracker issue: the tracker number names the branch and the branch
// chain records the tracker origin; RecordID ties it to the record.
func fromImportedRecord(
	ctx context.Context, p StartPrompter, rec *issue.Record, allowedTypes []string,
) (*issue.Issue, error) {
	src := tracker.Issue{
		TrackerType: rec.Tracker.Type, ID: rec.Tracker.ID, Subject: rec.Title, Description: rec.Description,
	}

	got, err := p.PickIssueFromTracker(ctx, []tracker.Issue{src}, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("tracker issue form: %w", err)
	}
	if got == nil {
		return nil, nil
	}

	got.Issue, got.RecordID = src, rec.ID

	return got, nil
}
```

(`getFromRepoOrUser` already returns a nil issue with a nil error when the operator aborts; if the linter rejects `return nil, nil` here, follow the form the function's other aborting return uses.)

- [ ] **Step 8: Run the start tests**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_" -v 2>&1 | tail -40`
Expected: PASS, the new test and the existing suite.

- [ ] **Step 9: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_mirror_e2e_test.go cmd/issueflow/start.go cmd/issue/start_record_e2e_test.go
git commit -m "feat(issue): merge close and start follow the mirror's origin rule"
```

---

### Task 12: Documentation and full verification

**Files:**
- Modify: `docs/issue-refs.md`
- Modify: `CLAUDE.md` (before `### Testing the branch chains`)
- Modify: `ROADMAP.md` (already holds the entries; check the wording)

**Interfaces:**
- Consumes: everything above.
- Produces: nothing code depends on.

- [ ] **Step 1: Document the mirror in `docs/issue-refs.md`**

In the `## Ops` section, add the two op types to the list in the file's own format: `link_tracker` (`tracker_type`, `project`, `tracker_id`: the tracker issue a repo-born issue was created as; the first one wins) and `tracker_state` (`value` `open`/`closed`, `status`: the last tracker state the chain saw). Note that `create` may carry `tracker_type`, `project`, `tracker_id` for an issue imported from the tracker.

Add at the end of the file:

```markdown
## Tracker mirror

With `mirror = true` under `[issue-tracker]` and exactly one
`[[issue-tracker.projects]]` entry, the issues of that tracker project and the
issues of the repository are one set:

- every open tracker issue is imported as an issue ref;
- every open repo-born issue is created in the tracker;
- open/closed travels both ways.

`git zf issue list`, `issue new`, `issue close <id>`, `issue close` (merge)
and `issue sync` reconcile. `show`, `edit`, `comment` and `label` only read
the refs: they see what another clone reconciled.

### Identity of an imported issue

The root commit of an imported issue is built from four values only: the
tracker type, the project's near slug, the tracker's issue number and its
creation date. Author, committer and dates are fixed and the commit is never
signed, so two clones importing the same tracker issue write the same commit
and therefore the same ref. Title and description follow as ordinary ops.

The near slug is part of the identity; the far slug is not. Renaming the
project in the tracker means editing `far-slug`. Changing `near-slug` imports
every issue again under new IDs.

### Which side moved

Each reconcile compares the repo state, the tracker state, and the tracker
state the chain last recorded (`tracker_state`). The side that differs from
the recorded one moved, and the other follows. With two values, both sides
moving means they agree.

### Display

An imported issue is shown, and names its branches, by its tracker number. A
repo-born issue keeps its short hash; `issue list` shows `a1b2c3d (#57)` once
it is exported. `issue show 57` finds either.

### Limits

- Two clones exporting the same issue at the same moment create two tracker
  issues. One link wins; the other issue is reported on each reconcile until
  it is closed in the tracker.
- The project's open issues are listed in full on each reconcile.
- Issues already closed in the tracker, or closed in the repository before
  the mirror was on, are not mirrored.
```

- [ ] **Step 2: Add the testing section to `CLAUDE.md`**

Insert before `### Testing the branch chains`:

```markdown
### Testing the tracker mirror

With `[issue-tracker] mirror = true`, the repo issues are mirrored with one
tracker project (`issue.Mirror`, `issue/mirror.go`). It is tested at four
levels, on real on-disk repos and the fake tracker:

- `issue/record_test.go` — `TestFold_Tracker`: the link and the tracker state.
- `git/chain_ref_test.go` — `TestChainRef_FixedRoot`: the deterministic root,
  pinned by a golden hash.
- `issue/mirror_test.go` — `Reconcile`: import, export, the eight rows of the
  state table (`TestReconcile_StateTable`), two clones importing offline.
- `cmd/issue/mirror_e2e_test.go`, `close_mirror_e2e_test.go` — the commands.

    mise exec -- go test ./issue/... -run "TestReconcile|TestFold_Tracker|TestResolve_TrackerNumber" -v
    mise exec -- go test ./git/... -run "TestChainRef_FixedRoot" -v
    mise exec -- go test ./cmd/issue/... -run "^TestMirror|^TestClose_Mirror" -v

The fake tracker is stateful for these calls: `CreateIssue` lists the issue,
`SetIssueOpen` / `CloseIssue` / `ReopenIssue` move it in and out of the open
listing. Use `CloseIssue` for "someone closed it in the tracker's UI": it is
not recorded in `RecordedOpens`.

`importRootTemplate` (`issue/mirror.go`) is frozen: the ID of every imported
issue is the hash of a commit holding those bytes. Never edit it; if
`TestChainRef_FixedRoot` fails on the golden hash, the code is wrong. Only
`issue list` and the commands that create an issue or change its state
reconcile; a new read-only command must not.

`issue.List` reads every issue with `ReadAllChains` (three git processes) and
`issue.PushAll` pushes many issues in one `git push` (`PushChainRefs`); a loop
of `Load` or `Push` over many issues is the thing to avoid.

    mise exec -- go test ./git/... -run "TestPushChainRefs" -v
    mise exec -- go test ./issue/... -run "TestPushAll|TestList_" -v
```

- [ ] **Step 3: Check the roadmap**

`ROADMAP.md` already has the "Issue mirror" entry with its three left-out items and the author/assignee entry marked "Comes after the issue mirror". Change "Issue mirror, repo <-> tracker." to "Issue mirror, repo <-> tracker (first version shipped)." and keep the three sub-items.

- [ ] **Step 4: Full verification**

Run:

```bash
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test ./... 2>&1 | tail -30
mise exec -- golangci-lint run --new-from-rev main ./... 2>&1 | tail -30
```

Expected: build and vet clean, every package `ok`, no new lint finding.

Then run the GitNexus regression check: `detect_changes({scope: "compare", base_ref: "main"})`. Expected: the changed symbols are the ones this plan names; report any execution flow outside issues, trackers and config.

- [ ] **Step 5: Try it by hand**

```bash
make
d=$(mktemp -d) && git -C "$d" init -q -b main && git -C "$d" commit -q --allow-empty -m init
cat > "$d/.git/.git-zf.toml" <<'EOF'
[issue-tracker]
type = "forgejo"
mirror = true
projects = ["owner/repo"]
EOF
(cd "$d" && /home/pi/code/pi/git-zf/bin/git-zf issue list --stdout); echo "exit=$?"
```

Expected: a non-zero exit and the message that shows the `[[issue-tracker.projects]]` form. Remove `$d` afterwards.

- [ ] **Step 6: Commit**

```bash
git add docs/issue-refs.md CLAUDE.md ROADMAP.md
git commit -m "docs: the issue tracker mirror"
```
