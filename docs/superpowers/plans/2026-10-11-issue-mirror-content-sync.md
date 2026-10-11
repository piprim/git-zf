# Issue Mirror Content Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The record of a mirrored issue becomes a full copy of the tracker issue: title, description, labels and comments travel both ways, with `edit`, `label` and `comment` reaching the tracker at once.

**Architecture:** One snapshot op, `tracker_seen`, records what the tracker showed last; each field is resolved by comparing that baseline with the record and the tracker, the side that differs from the baseline having moved. The reconcile runs state, content, state again, then the snapshot, on one record at a time, and `Mirror.SyncOne` runs that same function for one record after `edit`, `label` or `comment`. The three adapters gain the calls the content needs; Redmine's status is its one label.

**Tech Stack:** Go (via `mise exec -- go`), cobra, huh, the chain plumbing in `git/`, the fake tracker in `tracker/fake`, `httptest` for the adapters, `go-github/v73` for GitHub.

**Spec:** `docs/superpowers/specs/2026-10-10-issue-mirror-content-sync-design.md`

## Global Constraints

- Every Go command runs as `mise exec -- go …`.
- Every test assertion sits in its own `t.Run("descriptive label", …)`, including single-assertion tests.
- `importRootTemplate` in `issue/mirror.go` is frozen. Never edit it. `TestChainRef_FixedRoot` in `git/chain_ref_test.go` must keep passing on its golden hash.
- Unknown op types and unknown op fields keep being skipped by every fold: an older binary reads refs written by a newer one.
- On a conflict on title or description, the repo wins, with one warning naming the issue, the field and the tracker's value that lost.
- Only a `SetLabels` that returns `tracker.ErrLabelRule` reverts the record's labels to the tracker's; any other error keeps the local change for the next run.
- On Redmine, `Issue.Labels` holds exactly one name, the status.
- Per `CLAUDE.md`: run GitNexus `impact` on a symbol before editing it, and `detect_changes()` before each commit. Re-index with `node .gitnexus/run.cjs analyze` when the index is stale.
- Commit messages follow the repository's style: `feat(scope): …`, `fix(scope): …`, `docs: …`, `test(scope): …`, lower case, no period.

## Review Focus

1. **A tracker comment whose body is empty, or whose author is missing.** Forgejo returns comments with an empty body for label events on some versions. An empty body must not become a record comment. Pinned in Task 3 (`ListComments` drops empty bodies) and Task 9 (the pull skips an empty body).
2. **A label name with a space, a comma or mixed case.** `cleanLabels` in `cmd/issue/record.go` already trims; the reconcile must compare names as the tracker returns them, byte for byte, and never split on commas. Pinned in Task 8 (a label `"needs triage"` round-trips).
3. **A description cleared on one side.** An empty description is a value like any other: tracker `""`, record `"Body"`, baseline `"Body"` is a pull that clears the record. Pinned in Task 7.
4. **A record whose tracker issue was deleted.** `GetIssue` returns `tracker.ErrIssueNotFound`; the record must be skipped with one warning and no op, as the state step does today. Pinned in Task 7.
5. **`issue label` with tokens on a single-label tracker that cannot be reached.** The tokens must be written unchecked and the next reconcile must settle them, never hang on the tracker. Pinned in Task 12.

---

### Task 1: Record: the three ops and their fold

**Files:**
- Modify: `issue/record.go`
- Modify: `docs/issue-refs.md:30-45`
- Test: `issue/record_test.go`

**Interfaces:**
- Produces:
  - `const OpTrackerSeen = "tracker_seen"`, `const OpLinkComment = "link_comment"`.
  - `Op` gains `Labels []string`, `Updated`, `TrackerCommentID`, `TrackerAuthor`, `TrackerAt`, `Comment string`.
  - `type TrackerSnapshot struct { Title, Description string; Labels []string; Updated time.Time }`; `Record.TrackerSeen *TrackerSnapshot`.
  - `Comment.TrackerID string`.

- [ ] **Step 1: Write the failing fold tests**

Append to `issue/record_test.go`:

```go
func TestFold_Content(t *testing.T) {
	t.Parallel()

	root := op("c0", OpCreate, "2026-10-01T10:00:00Z")
	root.TrackerType, root.Project, root.TrackerID = "forgejo", "zf", "42"

	t.Run("tracker_seen gives the snapshot with sorted labels and the updated time", func(t *testing.T) {
		t.Parallel()

		seen := op("c1", OpTrackerSeen, "2026-10-01T11:00:00Z", "c0")
		seen.Title, seen.Description, seen.Labels, seen.Updated = "T", "D", []string{"ui", "bug"}, "2026-10-01T10:30:00Z"

		got := Fold("c0", []Op{root, seen}).TrackerSeen
		want := &TrackerSnapshot{Title: "T", Description: "D", Labels: []string{"bug", "ui"},
			Updated: time.Date(2026, 10, 1, 10, 30, 0, 0, time.UTC)}
		if got == nil || got.Title != want.Title || got.Description != want.Description ||
			!slices.Equal(got.Labels, want.Labels) || !got.Updated.Equal(want.Updated) {
			t.Errorf("TrackerSeen = %+v, want %+v", got, want)
		}
	})

	t.Run("a tracker_seen without labels folds to an empty, non-nil list", func(t *testing.T) {
		t.Parallel()

		seen := op("c1", OpTrackerSeen, "2026-10-01T11:00:00Z", "c0")
		if got := Fold("c0", []Op{root, seen}).TrackerSeen; got == nil || got.Labels == nil || len(got.Labels) != 0 {
			t.Errorf("TrackerSeen = %+v", got)
		}
	})

	t.Run("no tracker_seen leaves TrackerSeen nil", func(t *testing.T) {
		t.Parallel()

		if got := Fold("c0", []Op{root}).TrackerSeen; got != nil {
			t.Errorf("TrackerSeen = %+v, want nil", got)
		}
	})

	t.Run("an imported comment shows the tracker author, time and ID", func(t *testing.T) {
		t.Parallel()

		c := op("c1", OpAddComment, "2026-10-02T09:00:00Z", "c0")
		c.Author, c.Body = "alice <a@test.com>", "From the web"
		c.TrackerCommentID, c.TrackerAuthor, c.TrackerAt = "77", "bob", "2026-10-01T15:00:00Z"

		got := Fold("c0", []Op{root, c}).Comments
		if len(got) != 1 || got[0].Author != "bob" || got[0].TrackerID != "77" || got[0].Body != "From the web" ||
			!got[0].At.Equal(time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)) {
			t.Errorf("Comments = %+v", got)
		}
	})

	t.Run("a second add_comment with the same tracker comment ID is dropped", func(t *testing.T) {
		t.Parallel()

		c1 := op("c1", OpAddComment, "2026-10-02T09:00:00Z", "c0")
		c1.Body, c1.TrackerCommentID = "From the web", "77"
		c2 := op("c2", OpAddComment, "2026-10-02T09:00:01Z", "c0")
		c2.Body, c2.TrackerCommentID = "From the web", "77"

		if got := Fold("c0", []Op{root, c1, c2}).Comments; len(got) != 1 || got[0].ID != "c1" {
			t.Errorf("Comments = %+v, want the first only", got)
		}
	})

	t.Run("link_comment sets the tracker ID of a local comment", func(t *testing.T) {
		t.Parallel()

		c := op("c1", OpAddComment, "2026-10-02T09:00:00Z", "c0")
		c.Author, c.Body = "alice <a@test.com>", "Local"
		link := op("c2", OpLinkComment, "2026-10-02T09:01:00Z", "c1")
		link.Comment, link.TrackerCommentID = "c1", "78"

		got := Fold("c0", []Op{root, c, link}).Comments
		if len(got) != 1 || got[0].TrackerID != "78" || got[0].Author != "alice <a@test.com>" {
			t.Errorf("Comments = %+v", got)
		}
	})

	t.Run("a link_comment naming no comment, or an already linked one, changes nothing", func(t *testing.T) {
		t.Parallel()

		c := op("c1", OpAddComment, "2026-10-02T09:00:00Z", "c0")
		c.Body, c.TrackerCommentID = "Imported", "77"
		stray := op("c2", OpLinkComment, "2026-10-02T09:01:00Z", "c1")
		stray.Comment, stray.TrackerCommentID = "nope", "1"
		again := op("c3", OpLinkComment, "2026-10-02T09:02:00Z", "c2")
		again.Comment, again.TrackerCommentID = "c1", "99"

		if got := Fold("c0", []Op{root, c, stray, again}).Comments; len(got) != 1 || got[0].TrackerID != "77" {
			t.Errorf("Comments = %+v", got)
		}
	})
}

func TestDecodeOp_ContentFields(t *testing.T) {
	t.Parallel()

	t.Run("the new fields are decoded and cleaned of control characters", func(t *testing.T) {
		t.Parallel()

		payload := `{"v":1,"type":"tracker_seen","at":"2026-10-01T11:00:00Z","title":"T\u001b[0m","labels":["a\tb"],"updated":"2026-10-01T10:30:00Z","tracker_comment_id":"7\u0007","tracker_author":"bob","tracker_at":"2026-10-01T15:00:00Z","comment":"c1"}`
		op, ok := DecodeOp("x", nil, []byte(payload))
		if !ok || op.Title != "T" || !slices.Equal(op.Labels, []string{"a b"}) || op.Updated != "2026-10-01T10:30:00Z" ||
			op.TrackerCommentID != "7" || op.TrackerAuthor != "bob" || op.TrackerAt != "2026-10-01T15:00:00Z" || op.Comment != "c1" {
			t.Errorf("op = %+v, ok = %v", op, ok)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestFold_Content|TestDecodeOp_ContentFields" -v`
Expected: compile error, `OpTrackerSeen`, `OpLinkComment`, `TrackerSeen`, `Labels` undefined.

- [ ] **Step 3: Implement**

In `issue/record.go`:

Add to the op type constants:

```go
	OpTrackerSeen    = "tracker_seen"
	OpLinkComment    = "link_comment"
```

Add to `Op`, after `Status`:

```go
	Labels  []string `json:"labels,omitempty"`  // tracker_seen: the tracker's labels
	Updated string   `json:"updated,omitempty"` // tracker_seen: the tracker's updated time, RFC 3339

	TrackerCommentID string `json:"tracker_comment_id,omitempty"` // add_comment (imported), link_comment
	TrackerAuthor    string `json:"tracker_author,omitempty"`     // add_comment (imported): the tracker login
	TrackerAt        string `json:"tracker_at,omitempty"`         // add_comment (imported): the tracker's time, RFC 3339
	Comment          string `json:"comment,omitempty"`            // link_comment: the op ID of the add_comment
```

In `DecodeOp`, after the line cleaning `TrackerType` … `Status`, add:

```go
	op.Updated, op.TrackerCommentID, op.TrackerAuthor, op.TrackerAt, op.Comment =
		text.Line(op.Updated), text.Line(op.TrackerCommentID), text.Line(op.TrackerAuthor), text.Line(op.TrackerAt), text.Line(op.Comment)
	for i := range op.Labels {
		op.Labels[i] = text.Line(op.Labels[i])
	}
```

Add `TrackerID` to `Comment`:

```go
// Comment is one add_comment op. Author and At are the tracker's when the
// comment was imported from it. TrackerID is the tracker's comment ID, ""
// for a comment the tracker has not got yet.
type Comment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	At        time.Time `json:"at"`
	Body      string    `json:"body"`
	TrackerID string    `json:"tracker_id,omitempty"`
}
```

Add the snapshot type before `Record`:

```go
// TrackerSnapshot is what the tracker showed the last time the mirror read
// the issue: the baseline the reconcile compares both sides with.
type TrackerSnapshot struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Labels      []string  `json:"labels"`
	Updated     time.Time `json:"updated"`
}
```

Add to `Record`, after `TrackerStatus`:

```go
	// TrackerSeen is the last tracker_seen op; nil when the chain has none.
	TrackerSeen *TrackerSnapshot `json:"tracker_seen"`
```

In `Fold`, add `seenComments := make(map[string]bool)` next to `labels`, replace the `OpAddComment` case and add two cases:

```go
		case OpAddComment:
			if op.TrackerCommentID != "" {
				if seenComments[op.TrackerCommentID] {
					continue
				}
				seenComments[op.TrackerCommentID] = true
			}
			c := Comment{ID: op.ID, Author: op.Author, At: chain.ParseAt(op.At), Body: op.Body, TrackerID: op.TrackerCommentID}
			if op.TrackerAuthor != "" {
				c.Author = op.TrackerAuthor
			}
			if op.TrackerAt != "" {
				c.At = chain.ParseAt(op.TrackerAt)
			}
			rec.Comments = append(rec.Comments, c)
		case OpLinkComment:
			for i := range rec.Comments {
				if rec.Comments[i].ID == op.Comment && rec.Comments[i].TrackerID == "" && op.TrackerCommentID != "" {
					rec.Comments[i].TrackerID = op.TrackerCommentID
					seenComments[op.TrackerCommentID] = true
				}
			}
		case OpTrackerSeen:
			seen := &TrackerSnapshot{Title: op.Title, Description: op.Description, Labels: slices.Clone(op.Labels)}
			if seen.Labels == nil {
				seen.Labels = []string{}
			}
			slices.Sort(seen.Labels)
			if op.Updated != "" {
				seen.Updated = chain.ParseAt(op.Updated)
			}
			rec.TrackerSeen = seen
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/ -run "TestFold|TestDecodeOp" -v`
Expected: PASS, the existing `TestFold` and `TestFold_Tracker` included.

- [ ] **Step 5: Document the ops**

In `docs/issue-refs.md`, in the ops table, replace the `add_comment` row and add two rows after `tracker_state`:

```markdown
| `add_comment` | `body`; for a comment imported from the tracker also `tracker_comment_id`, `tracker_author`, `tracker_at` | Adds a comment. Two ops with the same `tracker_comment_id` count once. |
| `link_comment` | `comment` (the op ID of an `add_comment`), `tracker_comment_id` | The tracker comment a local comment was posted as. |
| `tracker_seen` | `title`, `description`, `labels`, `updated` | What the tracker showed the last time the mirror read the issue. |
```

- [ ] **Step 6: Commit**

```bash
git add issue/record.go issue/record_test.go docs/issue-refs.md
git commit -m "feat(issue): tracker_seen, link_comment and the tracker fields of add_comment"
```

---

### Task 2: Tracker types: labels, updated time, comments, the label rule

**Files:**
- Modify: `tracker/tracker.go`
- Test: `tracker/tracker_test.go`

**Interfaces:**
- Produces:
  - `Issue.Labels []string`, `Issue.UpdatedAt time.Time`, `Issue.Closed bool`.
  - `type Comment struct { ID, Author, Body string; CreatedAt time.Time }` with `Clean() Comment`.
  - `var ErrLabelRule = errors.New("tracker: label set breaks a rule of the tracker")`.
  - `func SingleLabel(t Tracker) bool`: true when `t` implements `interface{ SingleLabel() bool }` and says so.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/tracker_test.go`:

```go
func TestIssueClean_Labels(t *testing.T) {
	t.Parallel()

	t.Run("labels lose their control characters", func(t *testing.T) {
		t.Parallel()

		got := Issue{Labels: []string{"a\tb", "ok"}}.Clean().Labels
		if !slices.Equal(got, []string{"a b", "ok"}) {
			t.Errorf("Labels = %q", got)
		}
	})
}

func TestCommentClean(t *testing.T) {
	t.Parallel()

	t.Run("author and ID are one line, the body keeps newlines", func(t *testing.T) {
		t.Parallel()

		got := Comment{ID: "7\x07", Author: "bob\n", Body: "a\nb\x1b[0m"}.Clean()
		if got.ID != "7" || got.Author != "bob " || got.Body != "a\nb" {
			t.Errorf("Clean = %+v", got)
		}
	})
}

type singleLabelTracker struct {
	Tracker
	single bool
}

func (s singleLabelTracker) SingleLabel() bool { return s.single }

func TestSingleLabel(t *testing.T) {
	t.Parallel()

	t.Run("a tracker without the method is not single-label", func(t *testing.T) {
		t.Parallel()

		if SingleLabel(nil) || SingleLabel(struct{ Tracker }{}) {
			t.Error("want false")
		}
	})
	t.Run("a tracker with the method answers for itself", func(t *testing.T) {
		t.Parallel()

		if !SingleLabel(singleLabelTracker{single: true}) || SingleLabel(singleLabelTracker{single: false}) {
			t.Error("want the method's answer")
		}
	})
}
```

Add `"slices"` to the test file's imports if missing. The exact `Author` expectation `"bob "` follows `text.Line`, which turns a newline into a space; check `internal/text/text_test.go` and adjust the literal if `Line` trims.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/ -run "TestIssueClean_Labels|TestCommentClean|TestSingleLabel" -v`
Expected: compile error, `Labels`, `Comment`, `SingleLabel` undefined.

- [ ] **Step 3: Implement**

In `tracker/tracker.go`, extend `Issue` and `Clean`, and add the new type, sentinel and helper:

```go
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
	Project     string
	// Labels are the issue's labels as the tracker names them. On Redmine
	// it is one name: the status.
	Labels []string
	// CreatedAt is when the tracker says the issue was created; zero when the
	// call that built the Issue does not report it. UpdatedAt is the tracker's
	// time of the last change of any kind on the issue.
	CreatedAt time.Time
	UpdatedAt time.Time
	// Closed reports the tracker's open/closed state.
	Closed bool
}

func (i Issue) Clean() Issue {
	i.Subject, i.Status, i.Project = text.Line(i.Subject), text.Line(i.Status), text.Line(i.Project)
	i.Description = text.Clean(i.Description)
	i.Labels = slices.Clone(i.Labels)
	for k := range i.Labels {
		i.Labels[k] = text.Line(i.Labels[k])
	}

	return i
}

// Comment is one comment of a tracker issue.
type Comment struct {
	ID        string
	Author    string // the tracker login
	Body      string
	CreatedAt time.Time
}

// Clean drops the control characters, as Issue.Clean does.
func (c Comment) Clean() Comment {
	c.ID, c.Author, c.Body = text.Line(c.ID), text.Line(c.Author), text.Clean(c.Body)

	return c
}

// ErrLabelRule is returned by SetLabels when the set breaks a rule of the
// tracker (Redmine: exactly one name, a status), as opposed to a transport
// or auth failure. The mirror then takes the tracker's labels back.
var ErrLabelRule = errors.New("tracker: label set breaks a rule of the tracker")

// SingleLabel reports whether t allows exactly one label per issue, which is
// its status. Redmine does; a tracker without the optional method does not.
func SingleLabel(t Tracker) bool {
	s, ok := t.(interface{ SingleLabel() bool })

	return ok && s.SingleLabel()
}
```

Add `"slices"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/tracker.go tracker/tracker_test.go
git commit -m "feat(tracker): labels, updated time, comments and the label rule on the types"
```

---

### Task 3: Forgejo: the content calls

**Files:**
- Modify: `tracker/forgejo/forgejo.go`
- Test: `tracker/forgejo/forgejo_test.go`

**Interfaces:**
- Produces, on `*forgejoAdapter` (plain methods, not yet on the interface):
  - `GetIssue(ctx, issueID string) (tracker.Issue, error)`
  - `UpdateIssue(ctx, issueID, title, description string) error`
  - `SetLabels(ctx, issueID string, labels []string) error`
  - `ListLabels(ctx) ([]string, error)`
  - `ListComments(ctx, issueID string) ([]tracker.Comment, error)`
  - `toIssue` fills `Labels`, `UpdatedAt`, `Closed`.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/forgejo/forgejo_test.go`:

```go
const forgejoIssue42 = `{"number":42,"title":"T","body":"D","state":"closed","created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-02T09:00:00Z","labels":[{"id":1,"name":"bug"},{"id":2,"name":"ui"}]}`

func TestGetIssue(t *testing.T) {
	t.Parallel()

	t.Run("returns title, body, labels, updated time and the closed state", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/a/b/issues/42" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, forgejoIssue42)
		})

		iss, err := a.GetIssue(t.Context(), "42")
		if err != nil || iss.Subject != "T" || iss.Description != "D" || !iss.Closed ||
			!slices.Equal(iss.Labels, []string{"bug", "ui"}) ||
			!iss.UpdatedAt.Equal(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)) {
			t.Errorf("GetIssue = %+v, %v", iss, err)
		}
	})

	t.Run("a 404 is ErrIssueNotFound", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		if _, err := a.GetIssue(t.Context(), "42"); !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUpdateIssue(t *testing.T) {
	t.Parallel()

	t.Run("PATCHes title and body", func(t *testing.T) {
		t.Parallel()

		var got struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/repos/a/b/issues/42" {
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

				return
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			fmt.Fprint(w, forgejoIssue42)
		})

		if err := a.UpdateIssue(t.Context(), "42", "New T", "New D"); err != nil || got.Title != "New T" || got.Body != "New D" {
			t.Errorf("err = %v, sent %+v", err, got)
		}
	})
}

func TestListLabels(t *testing.T) {
	t.Parallel()

	t.Run("walks the pages of the repository's labels", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/a/b/labels" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			switch r.URL.Query().Get("page") {
			case "1":
				fmt.Fprint(w, `[{"id":1,"name":"bug"},{"id":2,"name":"ui"}]`)
			default:
				fmt.Fprint(w, `[]`)
			}
		})

		if got, err := a.ListLabels(t.Context()); err != nil || !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("ListLabels = %q, %v", got, err)
		}
	})
}

func TestSetLabels(t *testing.T) {
	t.Parallel()

	type server struct {
		created []string
		put     []int
	}

	newServer := func(t *testing.T) (*forgejoAdapter, *server) {
		t.Helper()

		s := &server{}
		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/a/b/labels":
				if r.URL.Query().Get("page") == "1" {
					fmt.Fprint(w, `[{"id":1,"name":"bug"}]`)
				} else {
					fmt.Fprint(w, `[]`)
				}
			case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/a/b/labels":
				var body struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				s.created = append(s.created, body.Name)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"id":%d,"name":%q}`, 10+len(s.created), body.Name)
			case r.Method == http.MethodPut && r.URL.Path == "/api/v1/repos/a/b/issues/42/labels":
				var body struct {
					Labels []int `json:"labels"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				s.put = body.Labels
				fmt.Fprint(w, `[]`)
			default:
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			}
		})

		return a, s
	}

	t.Run("creates the missing labels, then replaces the issue's set by ID", func(t *testing.T) {
		t.Parallel()

		a, s := newServer(t)
		if err := a.SetLabels(t.Context(), "42", []string{"bug", "urgent"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		if !slices.Equal(s.created, []string{"urgent"}) || !slices.Equal(s.put, []int{1, 11}) {
			t.Errorf("created %q, put %v", s.created, s.put)
		}
	})

	t.Run("an empty set clears the labels and creates nothing", func(t *testing.T) {
		t.Parallel()

		a, s := newServer(t)
		if err := a.SetLabels(t.Context(), "42", nil); err != nil || len(s.created) != 0 || len(s.put) != 0 {
			t.Errorf("err = %v, created %q, put %v", err, s.created, s.put)
		}
	})
}

func TestListComments(t *testing.T) {
	t.Parallel()

	t.Run("returns the comments oldest first and drops empty bodies", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/a/b/issues/42/comments" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)

				return
			}
			fmt.Fprint(w, `[{"id":7,"body":"First","user":{"login":"bob"},"created_at":"2026-09-01T09:00:00Z"},`+
				`{"id":8,"body":"","user":{"login":"bot"},"created_at":"2026-09-01T09:30:00Z"},`+
				`{"id":9,"body":"Second","user":{"login":"alice"},"created_at":"2026-09-01T10:00:00Z"}]`)
		})

		got, err := a.ListComments(t.Context(), "42")
		if err != nil || len(got) != 2 || got[0].ID != "7" || got[0].Author != "bob" || got[0].Body != "First" ||
			got[1].ID != "9" || !got[1].CreatedAt.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("ListComments = %+v, %v", got, err)
		}
	})
}

func TestListProjectIssues_Content(t *testing.T) {
	t.Parallel()

	t.Run("the listing carries labels, updated time and the open state", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)

				return
			}
			fmt.Fprint(w, `[`+strings.Replace(forgejoIssue42, `"closed"`, `"open"`, 1)+`]`)
		})

		got, err := a.ListProjectIssues(t.Context())
		if err != nil || len(got) != 1 || got[0].Closed || !slices.Equal(got[0].Labels, []string{"bug", "ui"}) || got[0].UpdatedAt.IsZero() {
			t.Errorf("ListProjectIssues = %+v, %v", got, err)
		}
	})
}
```

`newMirrorAdapter` (`forgejo_test.go:817`) serves project `a/b`; check its config and adjust the paths if it uses other names. Add `"errors"`, `"slices"`, `"strings"`, `"time"` to the imports as needed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/forgejo/ -run "TestGetIssue|TestUpdateIssue|TestListLabels|TestSetLabels|TestListComments|TestListProjectIssues_Content" -v`
Expected: compile error, the methods are undefined.

- [ ] **Step 3: Implement**

In `tracker/forgejo/forgejo.go`:

Extend the `issue` struct and `toIssue`:

```go
// label is the subset of the Forgejo Label object we consume.
type label struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type issue struct {
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	Body       string      `json:"body"`
	State      string      `json:"state"`
	Labels     []label     `json:"labels"`
	Repository *repository `json:"repository"`
	//nolint:tagliatelle // Forgejo wire format
	PullRequest *pullRequestRef `json:"pull_request"`
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
	//nolint:tagliatelle // Forgejo wire format
	UpdatedAt time.Time `json:"updated_at"`
}

// comment is the subset of the Forgejo Comment object we consume.
type comment struct {
	ID   int    `json:"id"`
	Body string `json:"body"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	//nolint:tagliatelle // Forgejo wire format
	CreatedAt time.Time `json:"created_at"`
}

func (a *forgejoAdapter) toIssue(iss *issue, project string) tracker.Issue {
	labels := make([]string, len(iss.Labels))
	for i, l := range iss.Labels {
		labels[i] = l.Name
	}

	return tracker.Issue{
		TrackerType: a.trackerType,
		ID:          strconv.Itoa(iss.Number),
		Subject:     iss.Title,
		Description: iss.Body,
		Status:      iss.State,
		Project:     project,
		Labels:      labels,
		CreatedAt:   iss.CreatedAt,
		UpdatedAt:   iss.UpdatedAt,
		Closed:      iss.State == statusClosed,
	}.Clean()
}
```

Rewrite `IsIssueClosed` over `GetIssue`, and add the new methods after `AddComment`:

```go
// GetIssue reads one issue via GET /repos/{owner}/{repo}/issues/{number}.
// HTTP 404 → tracker.ErrIssueNotFound; other failures are wrapped.
func (a *forgejoAdapter) GetIssue(ctx context.Context, issueID string) (tracker.Issue, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return tracker.Issue{}, err
	}
	_, project, err := a.projectIssuesPath()
	if err != nil {
		return tracker.Issue{}, err
	}

	var iss issue
	if err := a.doJSON(ctx, http.MethodGet, path, nil, &iss); err != nil {
		var he *httpError
		if errors.As(err, &he) && he.StatusCode == http.StatusNotFound {
			return tracker.Issue{}, tracker.ErrIssueNotFound
		}

		return tracker.Issue{}, fmt.Errorf("forgejo: get issue %s: %w", issueID, err)
	}

	return a.toIssue(&iss, project), nil
}

// IsIssueClosed asks Forgejo for the issue's state.
func (a *forgejoAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	iss, err := a.GetIssue(ctx, issueID)
	if err != nil {
		return false, err
	}

	return iss.Closed, nil
}

// UpdateIssue sets title and body via PATCH /repos/{owner}/{repo}/issues/{number}.
func (a *forgejoAdapter) UpdateIssue(ctx context.Context, issueID, title, description string) error {
	path, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	body := struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: title, Body: description}

	if err := a.doJSON(ctx, http.MethodPatch, path, body, nil); err != nil {
		return fmt.Errorf("forgejo: edit issue %s: %w", issueID, err)
	}

	return nil
}

// repoLabels lists the repository's labels, every page.
func (a *forgejoAdapter) repoLabels(ctx context.Context) (path string, labels []label, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", nil, err
	}
	path = fmt.Sprintf("/repos/%s/%s/labels", url.PathEscape(owner), url.PathEscape(repo))

	for page := 1; page <= maxPages; page++ {
		q := url.Values{"limit": {strconv.Itoa(issuesPerPage)}, "page": {strconv.Itoa(page)}}

		var batch []label
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return "", nil, fmt.Errorf("forgejo: list labels: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		labels = append(labels, batch...)
	}

	return path, labels, nil
}

// ListLabels returns the names of the repository's labels.
func (a *forgejoAdapter) ListLabels(ctx context.Context) ([]string, error) {
	_, labels, err := a.repoLabels(ctx)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}

	return names, nil
}

// defaultLabelColor is the colour of a label git-zf creates.
const defaultLabelColor = "#ededed"

// SetLabels replaces the issue's labels with names, creating the ones the
// repository lacks, via PUT /repos/{owner}/{repo}/issues/{number}/labels.
func (a *forgejoAdapter) SetLabels(ctx context.Context, issueID string, names []string) error {
	issuePath, err := a.issuePath(issueID)
	if err != nil {
		return err
	}

	labelsPath, labels, err := a.repoLabels(ctx)
	if err != nil {
		return err
	}
	byName := make(map[string]int, len(labels))
	for _, l := range labels {
		byName[l.Name] = l.ID
	}

	ids := make([]int, 0, len(names))
	for _, name := range names {
		id, ok := byName[name]
		if !ok {
			var created label
			body := struct {
				Name  string `json:"name"`
				Color string `json:"color"`
			}{Name: name, Color: defaultLabelColor}
			if err := a.doJSON(ctx, http.MethodPost, labelsPath, body, &created); err != nil {
				return fmt.Errorf("forgejo: create label %q: %w", name, err)
			}
			id = created.ID
		}
		ids = append(ids, id)
	}

	body := struct {
		Labels []int `json:"labels"`
	}{Labels: ids}
	if err := a.doJSON(ctx, http.MethodPut, issuePath+"/labels", body, nil); err != nil {
		return fmt.Errorf("forgejo: set labels of issue %s: %w", issueID, err)
	}

	return nil
}

// ListComments returns the issue's comments, oldest first, via
// GET /repos/{owner}/{repo}/issues/{number}/comments. A comment with an empty
// body (an event Forgejo lists as a comment) is dropped.
func (a *forgejoAdapter) ListComments(ctx context.Context, issueID string) ([]tracker.Comment, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return nil, err
	}

	var out []tracker.Comment
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"limit": {strconv.Itoa(issuesPerPage)}, "page": {strconv.Itoa(page)}}

		var batch []comment
		if err := a.doJSON(ctx, http.MethodGet, path+"/comments?"+q.Encode(), nil, &batch); err != nil {
			return nil, fmt.Errorf("forgejo: list comments of issue %s: %w", issueID, err)
		}
		if len(batch) == 0 {
			break
		}
		for _, c := range batch {
			if c.Body == "" {
				continue
			}
			author := ""
			if c.User != nil {
				author = c.User.Login
			}
			out = append(out, tracker.Comment{ID: strconv.Itoa(c.ID), Author: author, Body: c.Body, CreatedAt: c.CreatedAt}.Clean())
		}
	}

	slices.SortStableFunc(out, func(x, y tracker.Comment) int { return x.CreatedAt.Compare(y.CreatedAt) })

	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/forgejo/ -v`
Expected: PASS, the existing `TestIsIssueClosed` included.

- [ ] **Step 5: Commit**

```bash
git add tracker/forgejo/forgejo.go tracker/forgejo/forgejo_test.go
git commit -m "feat(forgejo): get, update, labels and comments of an issue"
```

---

### Task 4: GitHub: the content calls

**Files:**
- Modify: `tracker/github/github.go`
- Test: `tracker/github/github_test.go`

**Interfaces:**
- Produces, on `*githubAdapter`: the same five methods as Task 3, same signatures; `toIssue` fills `Labels`, `UpdatedAt`, `Closed`.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/github/github_test.go`. `newTestAdapterWithHandler` (`github_test.go:360`) serves project `a/b` against the handler; the go-github client sends REST paths under `/`.

```go
const ghIssue42 = `{"number":42,"title":"T","body":"D","state":"closed","created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-02T09:00:00Z","labels":[{"name":"bug"},{"name":"ui"}]}`

func TestGetIssue(t *testing.T) {
	t.Parallel()

	t.Run("returns title, body, labels, updated time and the closed state", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/a/b/issues/42" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, ghIssue42)
		})

		iss, err := a.GetIssue(t.Context(), "42")
		if err != nil || iss.Subject != "T" || iss.Description != "D" || !iss.Closed ||
			!slices.Equal(iss.Labels, []string{"bug", "ui"}) ||
			!iss.UpdatedAt.Equal(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)) {
			t.Errorf("GetIssue = %+v, %v", iss, err)
		}
	})

	t.Run("a 404 is ErrIssueNotFound", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		if _, err := a.GetIssue(t.Context(), "42"); !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUpdateIssue(t *testing.T) {
	t.Parallel()

	t.Run("PATCHes title and body", func(t *testing.T) {
		t.Parallel()

		var got struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch || r.URL.Path != "/repos/a/b/issues/42" {
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

				return
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			fmt.Fprint(w, ghIssue42)
		})

		if err := a.UpdateIssue(t.Context(), "42", "New T", "New D"); err != nil || got.Title != "New T" || got.Body != "New D" {
			t.Errorf("err = %v, sent %+v", err, got)
		}
	})
}

func TestListLabels(t *testing.T) {
	t.Parallel()

	t.Run("lists the repository's labels", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/a/b/labels" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, `[{"name":"bug"},{"name":"ui"}]`)
		})

		if got, err := a.ListLabels(t.Context()); err != nil || !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("ListLabels = %q, %v", got, err)
		}
	})
}

func TestSetLabels(t *testing.T) {
	t.Parallel()

	t.Run("creates the missing labels, then replaces the issue's set by name", func(t *testing.T) {
		t.Parallel()

		var created, put []string
		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/repos/a/b/labels":
				fmt.Fprint(w, `[{"name":"bug"}]`)
			case r.Method == http.MethodPost && r.URL.Path == "/repos/a/b/labels":
				var body struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				created = append(created, body.Name)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"name":%q}`, body.Name)
			case r.Method == http.MethodPut && r.URL.Path == "/repos/a/b/issues/42/labels":
				var body struct {
					Labels []string `json:"labels"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				put = body.Labels
				fmt.Fprint(w, `[]`)
			default:
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)
			}
		})

		if err := a.SetLabels(t.Context(), "42", []string{"bug", "urgent"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		if !slices.Equal(created, []string{"urgent"}) || !slices.Equal(put, []string{"bug", "urgent"}) {
			t.Errorf("created %q, put %q", created, put)
		}
	})
}

func TestListComments(t *testing.T) {
	t.Parallel()

	t.Run("returns the comments oldest first and drops empty bodies", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/a/b/issues/42/comments" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, `[{"id":7,"body":"First","user":{"login":"bob"},"created_at":"2026-09-01T09:00:00Z"},`+
				`{"id":8,"body":"","user":{"login":"bot"},"created_at":"2026-09-01T09:30:00Z"},`+
				`{"id":9,"body":"Second","user":{"login":"alice"},"created_at":"2026-09-01T10:00:00Z"}]`)
		})

		got, err := a.ListComments(t.Context(), "42")
		if err != nil || len(got) != 2 || got[0].ID != "7" || got[0].Author != "bob" || got[0].Body != "First" ||
			got[1].ID != "9" || !got[1].CreatedAt.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("ListComments = %+v, %v", got, err)
		}
	})
}

func TestListProjectIssues_Content(t *testing.T) {
	t.Parallel()

	t.Run("the listing carries labels, updated time and the open state", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `[`+strings.Replace(ghIssue42, `"closed"`, `"open"`, 1)+`]`)
		})

		got, err := a.ListProjectIssues(t.Context())
		if err != nil || len(got) != 1 || got[0].Closed || !slices.Equal(got[0].Labels, []string{"bug", "ui"}) || got[0].UpdatedAt.IsZero() {
			t.Errorf("ListProjectIssues = %+v, %v", got, err)
		}
	})
}
```

Add `"errors"`, `"slices"`, `"strings"`, `"time"`, `"encoding/json"` to the imports as needed.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/ -run "TestGetIssue|TestUpdateIssue|TestListLabels|TestSetLabels|TestListComments|TestListProjectIssues_Content" -v`
Expected: compile error, the methods are undefined.

- [ ] **Step 3: Implement**

In `tracker/github/github.go`, extend `toIssue` and add the methods. `issueRef` factors the owner, repo and number resolution the per-issue calls repeat today.

```go
func toIssue(iss *gogithub.Issue, project string) tracker.Issue {
	labels := make([]string, len(iss.Labels))
	for i, l := range iss.Labels {
		labels[i] = l.GetName()
	}

	return tracker.Issue{
		TrackerType: trackerType,
		ID:          strconv.Itoa(iss.GetNumber()),
		Subject:     iss.GetTitle(),
		Description: iss.GetBody(),
		Status:      iss.GetState(),
		Project:     project,
		Labels:      labels,
		CreatedAt:   iss.GetCreatedAt().Time,
		UpdatedAt:   iss.GetUpdatedAt().Time,
		Closed:      iss.GetState() == statusClosed,
	}.Clean()
}

// issueRef resolves the configured repository and the issue number.
func (a *githubAdapter) issueRef(issueID string) (owner, repo string, n int, err error) {
	owner, repo, err = a.ownerRepo()
	if err != nil {
		return "", "", 0, err
	}
	n, err = strconv.Atoi(issueID)
	if err != nil {
		return "", "", 0, fmt.Errorf("github: invalid issue id %q: %w", issueID, err)
	}

	return owner, repo, n, nil
}

// GetIssue reads one issue. HTTP 404 → tracker.ErrIssueNotFound.
func (a *githubAdapter) GetIssue(ctx context.Context, issueID string) (tracker.Issue, error) {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return tracker.Issue{}, err
	}

	iss, resp, err := a.client.Issues.Get(ctx, owner, repo, n)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return tracker.Issue{}, tracker.ErrIssueNotFound
		}

		return tracker.Issue{}, fmt.Errorf("github: get issue %s: %w", issueID, err)
	}

	return toIssue(iss, owner+"/"+repo), nil
}

// UpdateIssue sets title and body.
func (a *githubAdapter) UpdateIssue(ctx context.Context, issueID, title, description string) error {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return err
	}

	_, _, err = a.client.Issues.Edit(ctx, owner, repo, n,
		&gogithub.IssueRequest{Title: gogithub.Ptr(title), Body: gogithub.Ptr(description)})
	if err != nil {
		return fmt.Errorf("github: edit issue %d: %w", n, err)
	}

	return nil
}

// ListLabels returns the names of the repository's labels, every page.
func (a *githubAdapter) ListLabels(ctx context.Context) ([]string, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return nil, err
	}

	var names []string
	opt := &gogithub.ListOptions{PerPage: issuesPerPage}
	for {
		page, resp, err := a.client.Issues.ListLabels(ctx, owner, repo, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list labels: %w", err)
		}
		for _, l := range page {
			names = append(names, l.GetName())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return names, nil
}

// defaultLabelColor is the colour of a label git-zf creates (GitHub wants it
// without the '#').
const defaultLabelColor = "ededed"

// SetLabels replaces the issue's labels with names, creating the ones the
// repository lacks.
func (a *githubAdapter) SetLabels(ctx context.Context, issueID string, names []string) error {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return err
	}

	existing, err := a.ListLabels(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if slices.Contains(existing, name) {
			continue
		}
		_, _, err := a.client.Issues.CreateLabel(ctx, owner, repo,
			&gogithub.Label{Name: gogithub.Ptr(name), Color: gogithub.Ptr(defaultLabelColor)})
		if err != nil {
			return fmt.Errorf("github: create label %q: %w", name, err)
		}
	}

	if names == nil {
		names = []string{}
	}
	if _, _, err := a.client.Issues.ReplaceLabelsForIssue(ctx, owner, repo, n, names); err != nil {
		return fmt.Errorf("github: set labels of issue %d: %w", n, err)
	}

	return nil
}

// ListComments returns the issue's comments, oldest first; a comment with an
// empty body is dropped.
func (a *githubAdapter) ListComments(ctx context.Context, issueID string) ([]tracker.Comment, error) {
	owner, repo, n, err := a.issueRef(issueID)
	if err != nil {
		return nil, err
	}

	var out []tracker.Comment
	opt := &gogithub.IssueListCommentsOptions{
		Sort: gogithub.Ptr("created"), Direction: gogithub.Ptr("asc"),
		ListOptions: gogithub.ListOptions{PerPage: issuesPerPage},
	}
	for {
		page, resp, err := a.client.Issues.ListComments(ctx, owner, repo, n, opt)
		if err != nil {
			return nil, fmt.Errorf("github: list comments of issue %d: %w", n, err)
		}
		for _, c := range page {
			if c.GetBody() == "" {
				continue
			}
			out = append(out, tracker.Comment{
				ID: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(),
				Body: c.GetBody(), CreatedAt: c.GetCreatedAt().Time,
			}.Clean())
		}
		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}

	return out, nil
}
```

Rewrite `IsIssueClosed` as `iss, err := a.GetIssue(ctx, issueID); return iss.Closed, err` and `AddComment`'s number parsing over `issueRef`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/github/github.go tracker/github/github_test.go
git commit -m "feat(github): get, update, labels and comments of an issue"
```

---

### Task 5: Redmine: the content calls, the status as the one label

**Files:**
- Modify: `tracker/redmine/redmine.go`
- Test: `tracker/redmine/mirror_test.go`

**Interfaces:**
- Produces, on `*redmineAdapter`: the same five methods; `SingleLabel() bool` returning true; `toIssues` fills `Labels` (one name, the status), `UpdatedAt`, `Closed`.
- `SetLabels` returns an error wrapping `tracker.ErrLabelRule` on zero names, several names, or a name that is not a status.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/redmine/mirror_test.go`. `newMirrorAdapter` serves project `cpro`.

```go
const redmineIssue42 = `{"issue":{"id":42,"subject":"T","description":"D","status":{"id":5,"name":"Closed","is_closed":true},"created_on":"2026-09-01T08:00:00Z","updated_on":"2026-09-02T09:00:00Z","journals":[{"id":7,"user":{"name":"Bob"},"notes":"First","created_on":"2026-09-01T09:00:00Z"},{"id":8,"user":{"name":"Bot"},"notes":"","created_on":"2026-09-01T09:30:00Z"},{"id":9,"user":{"name":"Alice"},"notes":"Second","created_on":"2026-09-01T10:00:00Z"}]}}`

const redmineStatuses = `{"issue_statuses":[{"id":1,"name":"New","is_closed":false},{"id":5,"name":"Closed","is_closed":true}]}`

func TestRedmineContent(t *testing.T) {
	t.Parallel()

	t.Run("GetIssue returns subject, description, the status as the one label, updated time and closed", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/issues/42.json" {
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, redmineIssue42)
		})

		iss, err := a.GetIssue(t.Context(), "42")
		if err != nil || iss.Subject != "T" || iss.Description != "D" || !iss.Closed || iss.Status != "Closed" ||
			!slices.Equal(iss.Labels, []string{"Closed"}) ||
			!iss.UpdatedAt.Equal(time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)) {
			t.Errorf("GetIssue = %+v, %v", iss, err)
		}
	})

	t.Run("GetIssue on a 404 is ErrIssueNotFound", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
		if _, err := a.GetIssue(t.Context(), "42"); !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("UpdateIssue PUTs subject and description only", func(t *testing.T) {
		t.Parallel()

		var got map[string]map[string]any
		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut || r.URL.Path != "/issues/42.json" {
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

				return
			}
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusNoContent)
		})

		err := a.UpdateIssue(t.Context(), "42", "New T", "New D")
		if err != nil || got["issue"]["subject"] != "New T" || got["issue"]["description"] != "New D" || len(got["issue"]) != 2 {
			t.Errorf("err = %v, sent %+v", err, got)
		}
	})

	t.Run("ListLabels is the status names and SingleLabel is true", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, redmineStatuses) })
		got, err := a.ListLabels(t.Context())
		if err != nil || !slices.Equal(got, []string{"New", "Closed"}) || !tracker.SingleLabel(a) {
			t.Errorf("ListLabels = %q, %v, SingleLabel = %v", got, err, tracker.SingleLabel(a))
		}
	})

	t.Run("SetLabels with one status name sets that status", func(t *testing.T) {
		t.Parallel()

		var statusID float64
		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/issue_statuses.json":
				fmt.Fprint(w, redmineStatuses)
			case r.Method == http.MethodPut && r.URL.Path == "/issues/42.json":
				var body map[string]map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				statusID, _ = body["issue"]["status_id"].(float64)
				w.WriteHeader(http.StatusNoContent)
			default:
				http.Error(w, "unexpected "+r.URL.Path, http.StatusBadRequest)
			}
		})

		if err := a.SetLabels(t.Context(), "42", []string{"Closed"}); err != nil || statusID != 5 {
			t.Errorf("err = %v, status_id = %v", err, statusID)
		}
	})

	for name, labels := range map[string][]string{
		"no name":            {},
		"two names":          {"New", "Closed"},
		"a name not a status": {"urgent"},
	} {
		t.Run("SetLabels with "+name+" is ErrLabelRule and PUTs nothing", func(t *testing.T) {
			t.Parallel()

			puts := 0
			a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					puts++
				}
				fmt.Fprint(w, redmineStatuses)
			})

			if err := a.SetLabels(t.Context(), "42", labels); !errors.Is(err, tracker.ErrLabelRule) || puts != 0 {
				t.Errorf("err = %v, puts = %d", err, puts)
			}
		})
	}

	t.Run("ListComments is the journals with notes, oldest first", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/issues/42.json" || r.URL.Query().Get("include") != "journals" {
				http.Error(w, "unexpected "+r.URL.String(), http.StatusBadRequest)

				return
			}
			fmt.Fprint(w, redmineIssue42)
		})

		got, err := a.ListComments(t.Context(), "42")
		if err != nil || len(got) != 2 || got[0].ID != "7" || got[0].Author != "Bob" || got[0].Body != "First" ||
			got[1].ID != "9" || !got[1].CreatedAt.Equal(time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)) {
			t.Errorf("ListComments = %+v, %v", got, err)
		}
	})

	t.Run("the project listing carries the status as label, updated time and open", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"issues":[{"id":42,"subject":"T","status":{"id":1,"name":"New","is_closed":false},"created_on":"2026-09-01T08:00:00Z","updated_on":"2026-09-02T09:00:00Z"}],"total_count":1}`)
		})

		got, err := a.ListProjectIssues(t.Context())
		if err != nil || len(got) != 1 || got[0].Closed || !slices.Equal(got[0].Labels, []string{"New"}) || got[0].UpdatedAt.IsZero() {
			t.Errorf("ListProjectIssues = %+v, %v", got, err)
		}
	})
}
```

Add `"errors"`, `"slices"` and `"github.com/piprim/git-zf/tracker"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/redmine/ -run TestRedmineContent -v`
Expected: compile error, the methods are undefined.

- [ ] **Step 3: Implement**

In `tracker/redmine/redmine.go`:

Extend `issue` and `toIssues`:

```go
// journal is one entry of a Redmine issue's journals: a comment when Notes
// is not empty.
type journal struct {
	ID   int `json:"id"`
	User *struct {
		Name string `json:"name"`
	} `json:"user"`
	Notes string `json:"notes"`
	//nolint:tagliatelle // Redmine wire format
	CreatedOn time.Time `json:"created_on"`
}

type issue struct {
	ID          int      `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Status      *status  `json:"status"`
	Project     *project `json:"project"`
	//nolint:tagliatelle // Redmine wire format
	CreatedOn time.Time `json:"created_on"`
	//nolint:tagliatelle // Redmine wire format
	UpdatedOn time.Time `json:"updated_on"`
	Journals  []journal `json:"journals"`
}
```

In `toIssues`, fill the new fields from `iss.Status`:

```go
		statusName, closed := "", false
		if iss.Status != nil {
			statusName, closed = iss.Status.Name, iss.Status.IsClosed
		}
		var labels []string
		if statusName != "" {
			labels = []string{statusName}
		}

		result = append(result, tracker.Issue{
			TrackerType: trackerType,
			ID:          strconv.Itoa(iss.ID),
			Subject:     iss.Subject,
			Description: iss.Description,
			Status:      statusName,
			Project:     cmp.Or(project, redmineProjectName(iss.Project)),
			Labels:      labels,
			CreatedAt:   iss.CreatedOn,
			UpdatedAt:   iss.UpdatedOn,
			Closed:      closed,
		}.Clean())
```

Add the methods, and rewrite `IsIssueClosed` over `getIssue`:

```go
// getIssue reads one issue; query is appended to the path ("?include=journals").
func (a *redmineAdapter) getIssue(ctx context.Context, issueID, query string) (issue, error) {
	var payload struct {
		Issue issue `json:"issue"`
	}
	code, err := a.getJSON(ctx, "/issues/"+url.PathEscape(issueID)+".json"+query, &payload)
	if code == http.StatusNotFound {
		return issue{}, tracker.ErrIssueNotFound
	}
	if err != nil {
		return issue{}, fmt.Errorf("redmine: get issue %s: %w", issueID, err)
	}

	return payload.Issue, nil
}

// GetIssue reads one issue. HTTP 404 → tracker.ErrIssueNotFound.
func (a *redmineAdapter) GetIssue(ctx context.Context, issueID string) (tracker.Issue, error) {
	iss, err := a.getIssue(ctx, issueID, "")
	if err != nil {
		return tracker.Issue{}, err
	}
	p, _ := a.project()

	return toIssues([]issue{iss}, p)[0], nil
}

// IsIssueClosed asks Redmine for the issue and reads status.is_closed.
func (a *redmineAdapter) IsIssueClosed(ctx context.Context, issueID string) (bool, error) {
	iss, err := a.GetIssue(ctx, issueID)
	if err != nil {
		return false, err
	}

	return iss.Closed, nil
}

// UpdateIssue PUTs subject and description only, the way setStatusID sends
// only the status: a fuller payload trips Redmine's validation.
func (a *redmineAdapter) UpdateIssue(ctx context.Context, issueID, title, description string) error {
	type fields struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	}
	type body struct {
		Issue fields `json:"issue"`
	}

	return a.putIssue(ctx, issueID, "content", body{Issue: fields{Subject: title, Description: description}})
}

// SingleLabel reports that a Redmine issue has one label: its status.
func (*redmineAdapter) SingleLabel() bool { return true }

// ListLabels returns the status names: on Redmine the status is the label.
func (a *redmineAdapter) ListLabels(ctx context.Context) ([]string, error) {
	return a.ListStatuses(ctx)
}

// SetLabels sets the status named by the one label. Zero or several names,
// or a name that is not a status, break the rule: tracker.ErrLabelRule.
func (a *redmineAdapter) SetLabels(ctx context.Context, issueID string, labels []string) error {
	if len(labels) != 1 {
		return fmt.Errorf("%w: redmine wants exactly one label, the status, got %d", tracker.ErrLabelRule, len(labels))
	}

	statuses, err := a.issueStatuses(ctx)
	if err != nil {
		return err
	}
	for _, s := range statuses {
		if strings.EqualFold(s.Name, labels[0]) {
			return a.setStatusID(ctx, issueID, s.ID)
		}
	}

	return fmt.Errorf("%w: %q is not a redmine status", tracker.ErrLabelRule, labels[0])
}

// ListComments returns the issue's journals that carry notes, oldest first.
func (a *redmineAdapter) ListComments(ctx context.Context, issueID string) ([]tracker.Comment, error) {
	iss, err := a.getIssue(ctx, issueID, "?include=journals")
	if err != nil {
		return nil, err
	}

	var out []tracker.Comment
	for _, j := range iss.Journals {
		if j.Notes == "" {
			continue
		}
		author := ""
		if j.User != nil {
			author = j.User.Name
		}
		out = append(out, tracker.Comment{ID: strconv.Itoa(j.ID), Author: author, Body: j.Notes, CreatedAt: j.CreatedOn}.Clean())
	}
	slices.SortStableFunc(out, func(x, y tracker.Comment) int { return x.CreatedAt.Compare(y.CreatedAt) })

	return out, nil
}
```

Add `"slices"` to the imports. The existing `IsIssueClosed` test keeps passing: the payload it serves decodes into `issue` the same way.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/redmine/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/redmine/redmine.go tracker/redmine/mirror_test.go
git commit -m "feat(redmine): get, update and comments of an issue; the status is its one label"
```

---

### Task 6: The interface, the fake, `AddComment` returns the comment

**Files:**
- Modify: `tracker/tracker.go:52-75`
- Modify: `tracker/fake/fake.go`
- Modify: `tracker/forgejo/forgejo.go`, `tracker/github/github.go`, `tracker/redmine/redmine.go` (`AddComment` only)
- Modify: `cmd/review/tracker.go:36`
- Test: `tracker/fake/fake_test.go`, the three adapters' `TestAddComment`

**Interfaces:**
- Produces, on `tracker.Tracker`:
  - `GetIssue(ctx, issueID string) (Issue, error)`
  - `UpdateIssue(ctx, issueID, title, description string) error`
  - `SetLabels(ctx, issueID string, labels []string) error`
  - `ListLabels(ctx) ([]string, error)`
  - `ListComments(ctx, issueID string) ([]Comment, error)`
  - `AddComment(ctx, issueID, body string) (Comment, error)` (changed)
- Produces, on `*fake.Tracker`:
  - fields `Labels []string` (the catalogue), `Comments map[string][]tracker.Comment`, `StatusIsLabel bool`, `RecordedEdits []Edit`, `RecordedLabelSets []LabelSet`, `GetErr error`.
  - `Edit(issueID string, fn func(*tracker.Issue))`: a web edit; bumps `UpdatedAt`.
  - `WebComment(issueID, author, body string) tracker.Comment`: a web comment; bumps `UpdatedAt`.
  - `RecordedComments` entries gain `ID`.
  - Every mutation bumps the issue's `UpdatedAt` by one second from `fakeEpoch` (deterministic).
  - `SingleLabel() bool` returns `StatusIsLabel`.

- [ ] **Step 1: Write the failing fake tests**

Append to `tracker/fake/fake_test.go`:

```go
func TestFake_Content(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("every mutation moves UpdatedAt forward", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1", Subject: "T"}}}
		before, _ := ft.GetIssue(ctx, "1")
		_ = ft.UpdateIssue(ctx, "1", "T2", "D")
		mid, _ := ft.GetIssue(ctx, "1")
		ft.Edit("1", func(iss *tracker.Issue) { iss.Subject = "T3" })
		after, _ := ft.GetIssue(ctx, "1")
		if !before.UpdatedAt.Before(mid.UpdatedAt) || !mid.UpdatedAt.Before(after.UpdatedAt) || after.Subject != "T3" {
			t.Errorf("UpdatedAt %v, %v, %v; subject %q", before.UpdatedAt, mid.UpdatedAt, after.UpdatedAt, after.Subject)
		}
	})

	t.Run("GetIssue of a closed issue reports Closed and the status closed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1", Status: "open"}}}
		ft.CloseIssue("1")
		if iss, err := ft.GetIssue(ctx, "1"); err != nil || !iss.Closed || iss.Status != "closed" {
			t.Errorf("GetIssue = %+v, %v", iss, err)
		}
	})

	t.Run("GetIssue honours Unknown and Errors like IsIssueClosed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{Unknown: map[string]bool{"9": true}, Errors: map[string]error{"8": errors.New("boom")}}
		if _, err := ft.GetIssue(ctx, "9"); !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
		if _, err := ft.GetIssue(ctx, "8"); err == nil || err.Error() != "boom" {
			t.Errorf("errors: err = %v", err)
		}
	})

	t.Run("SetLabels records the set, creates the missing names and lists them", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}, Labels: []string{"bug"}}
		if err := ft.SetLabels(ctx, "1", []string{"bug", "urgent"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		iss, _ := ft.GetIssue(ctx, "1")
		names, _ := ft.ListLabels(ctx)
		if !slices.Equal(iss.Labels, []string{"bug", "urgent"}) || !slices.Equal(names, []string{"bug", "urgent"}) ||
			len(ft.RecordedLabelSets) != 1 || !slices.Equal(ft.RecordedLabelSets[0].Labels, []string{"bug", "urgent"}) {
			t.Errorf("labels = %q, catalogue = %q, recorded = %+v", iss.Labels, names, ft.RecordedLabelSets)
		}
	})

	t.Run("AddComment returns the comment with an ID, lists it and records it", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}}
		c, err := ft.AddComment(ctx, "1", "Hello")
		listed, _ := ft.ListComments(ctx, "1")
		if err != nil || c.ID != "c1" || c.Body != "Hello" || len(listed) != 1 || listed[0].ID != "c1" ||
			len(ft.RecordedComments) != 1 || ft.RecordedComments[0].ID != "c1" {
			t.Errorf("comment = %+v, err = %v, listed = %+v, recorded = %+v", c, err, listed, ft.RecordedComments)
		}
	})

	t.Run("a web comment is listed but not recorded", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}}
		c := ft.WebComment("1", "bob", "From the web")
		listed, _ := ft.ListComments(ctx, "1")
		if c.Author != "bob" || len(listed) != 1 || len(ft.RecordedComments) != 0 {
			t.Errorf("comment = %+v, listed = %+v, recorded = %+v", c, listed, ft.RecordedComments)
		}
	})

	t.Run("with StatusIsLabel the status is the one label and SetLabels sets it", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{StatusIsLabel: true, Statuses: []string{"New", "Closed"}, ClosingStatuses: []string{"Closed"},
			ProjectIssues: []tracker.Issue{{ID: "1", Status: "New"}}}
		listed, _ := ft.ListProjectIssues(ctx)
		names, _ := ft.ListLabels(ctx)
		if !tracker.SingleLabel(ft) || !slices.Equal(listed[0].Labels, []string{"New"}) || !slices.Equal(names, []string{"New", "Closed"}) {
			t.Errorf("single = %v, labels = %q, names = %q", tracker.SingleLabel(ft), listed[0].Labels, names)
		}
		if err := ft.SetLabels(ctx, "1", []string{"Closed"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		if iss, _ := ft.GetIssue(ctx, "1"); !iss.Closed || !slices.Equal(iss.Labels, []string{"Closed"}) {
			t.Errorf("after: %+v", iss)
		}
	})

	t.Run("with StatusIsLabel a set that is not one status is ErrLabelRule", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{StatusIsLabel: true, Statuses: []string{"New"}, ProjectIssues: []tracker.Issue{{ID: "1", Status: "New"}}}
		if err := ft.SetLabels(ctx, "1", []string{"New", "urgent"}); !errors.Is(err, tracker.ErrLabelRule) {
			t.Errorf("two names: err = %v", err)
		}
		if err := ft.SetLabels(ctx, "1", []string{"urgent"}); !errors.Is(err, tracker.ErrLabelRule) {
			t.Errorf("not a status: err = %v", err)
		}
	})
}
```

Add `"slices"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/fake/ -run TestFake_Content -v`
Expected: compile error.

- [ ] **Step 3: Change the interface**

In `tracker/tracker.go`, replace the `AddComment` line of the interface and add the five methods:

```go
	// AddComment posts body as a comment on issueID and returns the comment
	// with the ID the tracker gave it.
	AddComment(ctx context.Context, issueID, body string) (Comment, error)
	// GetIssue reads one issue: title, description, labels, updated time and
	// state. ErrIssueNotFound when the tracker has no such issue.
	GetIssue(ctx context.Context, issueID string) (Issue, error)
	// UpdateIssue sets the title and the description.
	UpdateIssue(ctx context.Context, issueID, title, description string) error
	// SetLabels replaces the issue's labels, creating the names the tracker
	// lacks. A set the tracker refuses by rule is ErrLabelRule.
	SetLabels(ctx context.Context, issueID string, labels []string) error
	// ListLabels returns the label names the tracker project defines.
	ListLabels(ctx context.Context) ([]string, error)
	// ListComments returns the issue's comments, oldest first.
	ListComments(ctx context.Context, issueID string) ([]Comment, error)
```

- [ ] **Step 4: `AddComment` returns the comment in the three adapters**

Forgejo (`forgejo.go`): decode the response into `comment` and return it:

```go
func (a *forgejoAdapter) AddComment(ctx context.Context, issueID, body string) (tracker.Comment, error) {
	path, err := a.issuePath(issueID)
	if err != nil {
		return tracker.Comment{}, err
	}

	payload := struct {
		Body string `json:"body"`
	}{Body: body}

	var created comment
	if err := a.doJSON(ctx, http.MethodPost, path+"/comments", payload, &created); err != nil {
		return tracker.Comment{}, fmt.Errorf("forgejo: comment on issue %s: %w", issueID, err)
	}
	author := ""
	if created.User != nil {
		author = created.User.Login
	}

	return tracker.Comment{ID: strconv.Itoa(created.ID), Author: author, Body: created.Body, CreatedAt: created.CreatedAt}.Clean(), nil
}
```

GitHub (`github.go`): `c, _, err := a.client.Issues.CreateComment(…)`; on success return `tracker.Comment{ID: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(), Body: c.GetBody(), CreatedAt: c.GetCreatedAt().Time}.Clean()`.

Redmine (`redmine.go`): the PUT returns nothing, so read the journals back and take the last one with that body:

```go
func (a *redmineAdapter) AddComment(ctx context.Context, issueID, body string) (tracker.Comment, error) {
	type issueNotes struct {
		Notes string `json:"notes"`
	}
	type payload struct {
		Issue issueNotes `json:"issue"`
	}

	if err := a.putIssue(ctx, issueID, "comment", payload{Issue: issueNotes{Notes: body}}); err != nil {
		return tracker.Comment{}, err
	}

	comments, err := a.ListComments(ctx, issueID)
	if err != nil {
		return tracker.Comment{}, fmt.Errorf("redmine: read back comment on issue %s: %w", issueID, err)
	}
	for i := len(comments) - 1; i >= 0; i-- {
		if comments[i].Body == body {
			return comments[i], nil
		}
	}

	return tracker.Comment{}, fmt.Errorf("redmine: comment on issue %s posted but not found in its journals", issueID)
}
```

Update each adapter's `TestAddComment`: the Forgejo server already answers `{"id":1,"body":…}`, assert the returned `ID == "1"`; make the GitHub test server answer `{"id":5,"body":"needs tests","user":{"login":"me"}}` and assert `ID == "5"`; make the Redmine test serve `GET /issues/42.json?include=journals` with one journal `{"id":3,"notes":"needs tests","user":{"name":"Me"},"created_on":"2026-09-01T09:00:00Z"}` after the PUT and assert `ID == "3"`.

- [ ] **Step 5: The review caller**

In `cmd/review/tracker.go`, `addTrackerComment`:

```go
	if _, err := deps.tracker.AddComment(ctx, issueSlug, body); err != nil {
```

Fix the test doubles the same way: `grep -rn "AddComment(" cmd/ --include=*_test.go` finds them (`cmd/review`, and `cmd/issue/issue_test.go`).

- [ ] **Step 6: The fake**

In `tracker/fake/fake.go`, add the fields, the clock and the methods:

```go
// fakeEpoch is where the fake's clock starts; every mutation adds a second.
var fakeEpoch = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

type Tracker struct {
	// … existing fields …

	// Labels is the catalogue of the project's labels; SetLabels adds the
	// names it lacks. Comments holds each issue's comments by issue ID.
	Labels   []string
	Comments map[string][]tracker.Comment
	// StatusIsLabel makes the fake behave like Redmine: an issue's Labels is
	// its status, SetLabels sets the status or returns ErrLabelRule, and
	// SingleLabel is true.
	StatusIsLabel bool
	// GetErr, when non-nil, is returned by GetIssue.
	GetErr            error
	RecordedEdits     []Edit
	RecordedLabelSets []LabelSet

	ticks   int
	nextCID int
}

// Edit captures one UpdateIssue call.
type Edit struct {
	IssueID, Title, Description string
}

// LabelSet captures one SetLabels call.
type LabelSet struct {
	IssueID string
	Labels  []string
}

// Comment captures one AddComment call.
type Comment struct {
	IssueID string
	ID      string
	Body    string
}
```

```go
// now advances the fake's clock. The caller holds t.mu.
func (t *Tracker) now() time.Time {
	t.ticks++

	return fakeEpoch.Add(time.Duration(t.ticks) * time.Second)
}

// find returns the listed or shelved issue. The caller holds t.mu.
func (t *Tracker) find(issueID string) *tracker.Issue {
	for i := range t.ProjectIssues {
		if t.ProjectIssues[i].ID == issueID {
			return &t.ProjectIssues[i]
		}
	}
	if iss, ok := t.shelved[issueID]; ok {
		t.shelved[issueID] = iss

		return &iss // a copy; the caller stores it back with t.shelved[issueID] = *p
	}

	return nil
}

// view is the issue as the tracker shows it: with its status as the label
// when StatusIsLabel. The caller holds t.mu.
func (t *Tracker) view(iss tracker.Issue, closed bool) tracker.Issue {
	iss.Closed = closed
	if t.StatusIsLabel {
		iss.Labels = []string{iss.Status}
	}

	return iss
}

// SingleLabel reports StatusIsLabel.
func (t *Tracker) SingleLabel() bool { return t.StatusIsLabel }

// Edit applies fn to the issue the way a person edits it on the web: not
// recorded, UpdatedAt moves.
func (t *Tracker) Edit(issueID string, fn func(*tracker.Issue)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.mutate(issueID, fn)
}

// mutate applies fn to the listed or shelved issue and bumps UpdatedAt. The
// caller holds t.mu.
func (t *Tracker) mutate(issueID string, fn func(*tracker.Issue)) {
	for i := range t.ProjectIssues {
		if t.ProjectIssues[i].ID == issueID {
			fn(&t.ProjectIssues[i])
			t.ProjectIssues[i].UpdatedAt = t.now()

			return
		}
	}
	if iss, ok := t.shelved[issueID]; ok {
		fn(&iss)
		iss.UpdatedAt = t.now()
		t.shelved[issueID] = iss
	}
}

// GetIssue returns the issue, listed or closed, honouring Errors, Unknown
// and GetErr.
func (t *Tracker) GetIssue(_ context.Context, issueID string) (tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.GetErr != nil {
		return tracker.Issue{}, t.GetErr
	}
	if err, ok := t.Errors[issueID]; ok && err != nil {
		return tracker.Issue{}, err
	}
	if t.Unknown[issueID] {
		return tracker.Issue{}, tracker.ErrIssueNotFound
	}
	for _, iss := range t.ProjectIssues {
		if iss.ID == issueID {
			return t.view(iss, false), nil
		}
	}
	if iss, ok := t.shelved[issueID]; ok {
		return t.view(iss, true), nil
	}

	return tracker.Issue{}, tracker.ErrIssueNotFound
}

// UpdateIssue records the call and sets title and description.
func (t *Tracker) UpdateIssue(_ context.Context, issueID, title, description string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedEdits = append(t.RecordedEdits, Edit{IssueID: issueID, Title: title, Description: description})
	t.mutate(issueID, func(iss *tracker.Issue) { iss.Subject, iss.Description = title, description })

	return nil
}

// ListLabels returns the catalogue, or the statuses with StatusIsLabel.
func (t *Tracker) ListLabels(_ context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.StatusIsLabel {
		return slices.Clone(t.Statuses), nil
	}

	return slices.Clone(t.Labels), nil
}

// SetLabels records the call and sets the issue's labels, adding the names
// the catalogue lacks. With StatusIsLabel it sets the status named by the one
// label, or returns ErrLabelRule.
func (t *Tracker) SetLabels(ctx context.Context, issueID string, labels []string) error {
	t.mu.Lock()
	t.RecordedLabelSets = append(t.RecordedLabelSets, LabelSet{IssueID: issueID, Labels: slices.Clone(labels)})
	single := t.StatusIsLabel
	t.mu.Unlock()

	if single {
		if len(labels) != 1 {
			return fmt.Errorf("%w: fake wants exactly one label, got %d", tracker.ErrLabelRule, len(labels))
		}
		if !slices.Contains(t.Statuses, labels[0]) {
			return fmt.Errorf("%w: %q is not a status", tracker.ErrLabelRule, labels[0])
		}

		return t.UpdateIssueStatus(ctx, issueID, labels[0])
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, name := range labels {
		if !slices.Contains(t.Labels, name) {
			t.Labels = append(t.Labels, name)
		}
	}
	t.mutate(issueID, func(iss *tracker.Issue) { iss.Labels = slices.Clone(labels) })

	return nil
}

// ListComments returns the issue's comments, oldest first.
func (t *Tracker) ListComments(_ context.Context, issueID string) ([]tracker.Comment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	return slices.Clone(t.Comments[issueID]), nil
}

// AddComment records the call, lists the comment and returns it with its ID.
func (t *Tracker) AddComment(_ context.Context, issueID, body string) (tracker.Comment, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	c := t.addComment(issueID, "fake-user", body)
	t.RecordedComments = append(t.RecordedComments, Comment{IssueID: issueID, ID: c.ID, Body: body})

	return c, nil
}

// WebComment adds a comment the way a person does on the web: not recorded.
func (t *Tracker) WebComment(issueID, author, body string) tracker.Comment {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.addComment(issueID, author, body)
}

// addComment numbers the comment c1, c2, …, lists it and bumps the issue.
// The caller holds t.mu.
func (t *Tracker) addComment(issueID, author, body string) tracker.Comment {
	if t.Comments == nil {
		t.Comments = make(map[string][]tracker.Comment)
	}
	t.nextCID++
	c := tracker.Comment{ID: "c" + strconv.Itoa(t.nextCID), Author: author, Body: body, CreatedAt: t.now()}
	t.Comments[issueID] = append(t.Comments[issueID], c)
	t.mutate(issueID, func(*tracker.Issue) {})

	return c
}
```

Make `UpdateIssueStatus`, `SetIssueOpen`, `CloseIssue` and `ReopenIssue` go through `mutate` so that `UpdatedAt` moves, and `CreateIssue` set `UpdatedAt: t.now()` on the issue it adds. `setOpen(id, false)` sets `Status` to the first name of `ClosingStatuses` when there is one, else to `"closed"`, unless `UpdateIssueStatus` just set a closing name (it sets `Status` before calling `setOpen`). `setOpen(id, true)` keeps the shelved issue's status, except with `StatusIsLabel`, where it sets the first name of `Statuses` that is not in `ClosingStatuses` (Redmine reopens with its first open status). `ListProjectIssues` returns `t.view(iss, false)` for each issue. Delete the unused `find` helper if `mutate` covers every use. Run the existing mirror suites after this: a test that pinned the old `"closed"` name while `ClosingStatuses` was set must be updated to the closing name.

- [ ] **Step 7: Build and run every suite**

Run: `mise exec -- go build ./... && mise exec -- go test ./tracker/... ./cmd/review/... ./issue/... ./cmd/issue/...`
Expected: PASS. The compile errors name any caller of `AddComment` still ignoring the result.

- [ ] **Step 8: Commit**

```bash
git add tracker/ cmd/review/tracker.go cmd/review/*_test.go
git commit -m "feat(tracker): content calls on the interface, the fake and the adapters"
```

---

### Task 7: Mirror: the four steps, title and description, the snapshot

**Files:**
- Modify: `issue/mirror.go`
- Test: `issue/mirror_test.go`

**Interfaces:**
- Consumes: Task 1 (`OpTrackerSeen`, `TrackerSnapshot`, `Record.TrackerSeen`), Task 6 (`GetIssue`, `UpdateIssue`, `Issue.Closed`, `Issue.UpdatedAt`, fake `Edit`).
- Produces, in `issue/mirror.go` (unexported, used by Tasks 8 to 10):
  - `func (m *Mirror) syncRecord(ctx, c, rec *Record, listed *tracker.Issue, res *MirrorResult) (pulled, pushed, changed bool, err error)`: the four steps on one linked record.
  - `func (m *Mirror) syncContent(ctx, c, rec *Record, t *tracker.Issue, res *MirrorResult) (contentMove, error)` with `type contentMove struct{ pulled, pushed bool }`.
  - `func baseline(rec *Record) TrackerSnapshot`, `func contentSettled(rec *Record, t *tracker.Issue) bool`, `func resolveField(l, r, t string) (final string, pull, push, conflict bool)`, `func snapshotOf(t *tracker.Issue) TrackerSnapshot`, `func snapshotOp(s *TrackerSnapshot) *Op`.
  - `syncState` takes `t *tracker.Issue` (never nil) and updates `rec` in memory after it appends.
  - `export` returns the created `tracker.Issue`.

- [ ] **Step 1: Write the failing tests**

Append to `issue/mirror_test.go`:

```go
// countingTracker counts the per-issue reads the reconcile makes.
type countingTracker struct {
	*fake.Tracker
	gets int
}

func (c *countingTracker) GetIssue(ctx context.Context, id string) (tracker.Issue, error) {
	c.gets++

	return c.Tracker.GetIssue(ctx, id)
}

// importOne imports tracker issue 42 titled "A" with description "D" and
// returns the repo, the fake, the mirror and the record.
func importOne(t *testing.T) (*git.Client, *fake.Tracker, *Mirror, Record) {
	t.Helper()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "A")
	iss.Description = "D"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)

	return c, ft, m, onlyRecord(t, c)
}

func TestReconcile_TitleAndDescription(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name           string
		r, tr          string // what each side set the title to; "A" is the baseline
		want           string
		pulled, pushed int
		edits          int // UpdateIssue calls
		conflict       bool
	}{
		{"nobody moved", "A", "A", "A", 0, 0, 0, false},
		{"the tracker moved", "A", "B", "B", 1, 0, 0, false},
		{"the repo moved", "B", "A", "B", 0, 1, 1, false},
		{"both moved the same way", "B", "B", "B", 0, 0, 0, false},
		{"both moved apart: the repo wins", "B", "C", "B", 0, 1, 1, true},
	}

	for _, row := range rows {
		t.Run("title: "+row.name, func(t *testing.T) {
			t.Parallel()

			c, ft, m, rec := importOne(t)
			ctx := t.Context()
			if row.r != "A" {
				if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: row.r}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if row.tr != "A" {
				ft.Edit("42", func(iss *tracker.Issue) { iss.Subject = row.tr })
			}

			res := mustReconcile(t, m, c)
			got, _ := Load(ctx, c, rec.ID)
			onTracker, _ := ft.GetIssue(ctx, "42")

			if got.Title != row.want || onTracker.Subject != row.want {
				t.Errorf("repo title = %q, tracker title = %q, want both %q", got.Title, onTracker.Subject, row.want)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed || len(ft.RecordedEdits) != row.edits {
				t.Errorf("pulled/pushed/edits = %d/%d/%d, want %d/%d/%d", res.Pulled, res.Pushed, len(ft.RecordedEdits), row.pulled, row.pushed, row.edits)
			}
			if warned := len(res.Warnings) == 1 && strings.Contains(res.Warnings[0], "title"); warned != row.conflict {
				t.Errorf("warnings = %q, want a conflict warning: %v", res.Warnings, row.conflict)
			}
			if got.TrackerSeen == nil || got.TrackerSeen.Title != row.want || !got.TrackerSeen.Updated.Equal(onTracker.UpdatedAt) {
				t.Errorf("snapshot = %+v, tracker updated %v", got.TrackerSeen, onTracker.UpdatedAt)
			}
		})
	}

	t.Run("description cleared on the tracker clears the record", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.Edit("42", func(iss *tracker.Issue) { iss.Description = "" })
		res := mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if got.Description != "" || res.Pulled != 1 {
			t.Errorf("description = %q, res = %+v", got.Description, res)
		}
	})

	t.Run("description changed in the repo reaches the tracker with the unchanged title", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		if err := Append(t.Context(), c, rec.ID, &Op{Type: OpSetDescription, Value: "D2"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		if want := (fake.Edit{IssueID: "42", Title: "A", Description: "D2"}); len(ft.RecordedEdits) != 1 || ft.RecordedEdits[0] != want {
			t.Errorf("edits = %+v, want %+v", ft.RecordedEdits, want)
		}
	})
}

func TestReconcile_ContentSettles(t *testing.T) {
	t.Parallel()

	c, ft, _, rec := importOne(t)
	ct := &countingTracker{Tracker: ft}
	m := &Mirror{Tracker: ct, Type: "fake", Project: "zf"}
	ctx := t.Context()

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("a run with nothing changed writes no op, reads no issue and edits nothing", func(t *testing.T) {
		if opCount(t, c, rec.ID) != before || ct.gets != 0 || len(ft.RecordedEdits) != 0 || again.Pulled+again.Pushed != 0 {
			t.Errorf("ops %d → %d, gets %d, edits %d, res %+v", before, opCount(t, c, rec.ID), ct.gets, len(ft.RecordedEdits), again)
		}
	})

	if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: "B"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mustReconcile(t, m, c)
	gets := ct.gets
	afterPush := opCount(t, c, rec.ID)
	third := mustReconcile(t, m, c)

	t.Run("a push re-reads the issue once so that the run after it is settled", func(t *testing.T) {
		if gets != 1 || opCount(t, c, rec.ID) != afterPush || ct.gets != 1 || third.Pulled+third.Pushed != 0 {
			t.Errorf("gets after push %d, ops %d → %d, gets after third %d, third %+v", gets, afterPush, opCount(t, c, rec.ID), ct.gets, third)
		}
	})
}

func TestReconcile_ContentOfAClosedIssue(t *testing.T) {
	t.Parallel()

	c, ft, m, rec := importOne(t)
	ft.Edit("42", func(iss *tracker.Issue) { iss.Subject = "Closed with a new title" })
	ft.CloseIssue("42")
	res := mustReconcile(t, m, c)
	got, _ := Load(t.Context(), c, rec.ID)

	t.Run("an issue closed on the tracker still gets its last edit", func(t *testing.T) {
		if got.State != StateClosed || got.Title != "Closed with a new title" || res.Pulled != 1 {
			t.Errorf("record = %+v, res = %+v", got, res)
		}
	})
}

func TestReconcile_TrackerIssueGone(t *testing.T) {
	t.Parallel()

	c, ft, m, rec := importOne(t)
	ft.CloseIssue("42")
	ft.Unknown = map[string]bool{"42": true}
	before := opCount(t, c, rec.ID)
	res := mustReconcile(t, m, c)

	t.Run("a record whose tracker issue is gone is skipped with one warning", func(t *testing.T) {
		if len(res.Warnings) != 1 || opCount(t, c, rec.ID) != before {
			t.Errorf("warnings = %q, ops %d → %d", res.Warnings, before, opCount(t, c, rec.ID))
		}
	})
}

func TestReconcile_RecordWithoutSnapshot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	rec, err := Create(ctx, c, NewIssue{Title: "Local", Description: "Old", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A link written by the binary before this change: no tracker_seen.
	for _, op := range []*Op{
		{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "7"},
		{Type: OpTrackerState, Value: StateOpen, Status: "open"},
	} {
		if err := Append(ctx, c, rec.ID, op); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	iss := trackerIssue("7", "Local")
	iss.Description = "Edited on the web"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	res := mustReconcile(t, newTestMirror(ft), c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("the tracker's content is pulled wherever it differs, and a snapshot is written", func(t *testing.T) {
		if got.Description != "Edited on the web" || got.TrackerSeen == nil || res.Pulled != 1 || len(ft.RecordedEdits) != 0 {
			t.Errorf("record = %+v, res = %+v, edits = %+v", got, res, ft.RecordedEdits)
		}
	})
}

func TestReconcile_ExportWritesASnapshot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	if _, err := Create(t.Context(), c, NewIssue{Title: "Local", Description: "Steps", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ft := &fake.Tracker{}
	mustReconcile(t, newTestMirror(ft), c)
	rec := onlyRecord(t, c)

	t.Run("the snapshot is the created issue", func(t *testing.T) {
		if rec.TrackerSeen == nil || rec.TrackerSeen.Title != "Local" || rec.TrackerSeen.Description != "Steps" || rec.TrackerSeen.Updated.IsZero() {
			t.Errorf("snapshot = %+v", rec.TrackerSeen)
		}
	})
}
```

Add `"context"` to the imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestReconcile_TitleAndDescription|TestReconcile_ContentSettles|TestReconcile_ContentOfAClosedIssue|TestReconcile_TrackerIssueGone|TestReconcile_RecordWithoutSnapshot|TestReconcile_ExportWritesASnapshot" -v`
Expected: FAIL (the title stays, no snapshot, no edits).

- [ ] **Step 3: Implement**

In `issue/mirror.go`:

Replace `syncRecords` and `syncState`, and add the helpers. `importOps` gains the labels and the snapshot; `export` returns the created issue and writes the snapshot.

```go
// contentMove sums what the content steps did to one record.
type contentMove struct {
	pulled, pushed bool
}

// syncRecords exports the open records without a tracker issue and runs the
// four steps on the linked ones. It returns the IDs of the records it changed.
func (m *Mirror) syncRecords(
	ctx context.Context, c *git.Client, records []Record, open map[string]*tracker.Issue, res *MirrorResult,
) []string {
	var changed []string

	for i := range records {
		rec := &records[i]
		var listed *tracker.Issue

		switch {
		case rec.Tracker == nil && rec.State == StateOpen:
			created, err := m.export(ctx, c, rec)
			if err != nil {
				res.warnf("export issue %s: %v", rec.DisplayID(), err)

				continue
			}
			res.Exported++
			changed = append(changed, rec.ID)
			listed = &created
		case m.owns(rec):
			listed = open[rec.Tracker.ID]
		default:
			continue
		}

		pulled, pushed, moved, err := m.syncRecord(ctx, c, rec, listed, res)
		if err != nil {
			res.warnf("sync issue %s: %v", rec.DisplayID(), err)
		}
		if pulled {
			res.Pulled++
		}
		if pushed {
			res.Pushed++
		}
		if moved && listed != nil && rec.Tracker != nil && !slices.Contains(changed, rec.ID) {
			changed = append(changed, rec.ID)
		}
	}

	return changed
}

// syncRecord runs the four steps on one linked record: state, content, state
// again on the issue as re-read after a push, then the snapshot. listed is
// the issue in the open listing, nil when it is not there. changed reports
// whether an op was written, even when err is set.
func (m *Mirror) syncRecord(
	ctx context.Context, c *git.Client, rec *Record, listed *tracker.Issue, res *MirrorResult,
) (pulled, pushed, changed bool, err error) {
	t, err := m.trackerIssue(ctx, rec, listed)
	if err != nil || t == nil {
		return false, false, false, err
	}

	move, err := m.syncState(ctx, c, rec, t)
	if err != nil {
		return false, false, false, err
	}
	pulled, pushed, changed = move == movePulled, move == movePushed, move != moveNone
	if move == movePushed {
		if t, err = m.reread(ctx, rec); err != nil {
			return pulled, pushed, changed, err
		}
	}

	cm, err := m.syncContent(ctx, c, rec, t, res)
	pulled, pushed, changed = pulled || cm.pulled, pushed || cm.pushed, changed || cm.pulled || cm.pushed
	if err != nil {
		return pulled, pushed, changed, err
	}
	if cm.pushed {
		if t, err = m.reread(ctx, rec); err != nil {
			return pulled, pushed, changed, err
		}
		move, err = m.syncState(ctx, c, rec, t)
		if err != nil {
			return pulled, pushed, changed, err
		}
		pulled, changed = pulled || move == movePulled, changed || move != moveNone
	}

	snap := snapshotOf(t)
	if !sameSnapshot(rec.TrackerSeen, &snap) {
		if err := appendAll(ctx, c, rec.ID, snapshotOp(&snap)); err != nil {
			return pulled, pushed, changed, err
		}
		rec.TrackerSeen, changed = &snap, true
	}

	return pulled, pushed, changed, nil
}

// trackerIssue is the issue as the tracker shows it now: listed when it is
// in the open listing, read with one call otherwise. nil, nil for a record
// closed on both sides and not listed: nothing to compare.
func (m *Mirror) trackerIssue(ctx context.Context, rec *Record, listed *tracker.Issue) (*tracker.Issue, error) {
	if listed != nil {
		return listed, nil
	}
	if rec.State == StateClosed && cmp.Or(rec.TrackerState, StateOpen) == StateClosed {
		return nil, nil
	}

	return m.reread(ctx, rec)
}

// reread reads the record's tracker issue.
func (m *Mirror) reread(ctx context.Context, rec *Record) (*tracker.Issue, error) {
	iss, err := m.Tracker.GetIssue(ctx, rec.Tracker.ID)
	if err != nil {
		return nil, fmt.Errorf("read tracker issue %s: %w", rec.Tracker.ID, err)
	}

	return &iss, nil
}

// syncState compares three states of a linked record: l, the tracker state its
// chain last recorded (open when it recorded none); r, the repo state; t, the
// tracker state now. The side that differs from l moved, and the other one
// follows. When both differ from l they are equal: there is no conflict. The
// record is updated in memory with what was appended.
func (m *Mirror) syncState(ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue) (stateMove, error) {
	l, r := cmp.Or(rec.TrackerState, StateOpen), rec.State
	state, status := StateOpen, t.Status
	if t.Closed {
		state = StateClosed
		if status == "" {
			status = StateClosed
		}
	}

	seen := &Op{Type: OpTrackerState, Value: state, Status: status}
	note := func() { rec.TrackerState, rec.TrackerStatus = seen.Value, seen.Status }

	switch {
	case state != l && r != state:
		err := appendAll(ctx, c, rec.ID, &Op{Type: OpSetState, Value: state}, seen)
		if err == nil {
			rec.State = state
			note()
		}

		return movePulled, err
	case state != l:
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return moveNoted, err
	case r != state:
		if err := m.Tracker.SetIssueOpen(ctx, rec.Tracker.ID, r == StateOpen); err != nil {
			return moveNone, fmt.Errorf("set tracker issue %s %s: %w", rec.Tracker.ID, r, err)
		}
		// The status name a reopened issue got is read from the re-read.
		seen.Value, seen.Status = r, ""
		if r == StateClosed {
			seen.Status = StateClosed
		}
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return movePushed, err
	case rec.TrackerState == "" || status != rec.TrackerStatus:
		err := appendAll(ctx, c, rec.ID, seen)
		if err == nil {
			note()
		}

		return moveNoted, err
	}

	return moveNone, nil
}

// syncContent resolves title and description (and, in later tasks, labels and
// comments) between the baseline, the record and t. A push goes to the
// tracker before any pull op is written, so that a failed push leaves the
// chain as it was and the next run redoes the whole step.
func (m *Mirror) syncContent(
	ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue, res *MirrorResult,
) (contentMove, error) {
	var cm contentMove
	if contentSettled(rec, t) {
		return cm, nil
	}
	base := baseline(rec)

	title, pullTitle, pushTitle, conflictTitle := resolveField(base.Title, rec.Title, t.Subject)
	desc, pullDesc, pushDesc, conflictDesc := resolveField(base.Description, rec.Description, t.Description)
	if conflictTitle {
		res.warnf("issue %s: title changed on both sides; kept %q, the tracker had %q", rec.DisplayID(), rec.Title, t.Subject)
	}
	if conflictDesc {
		res.warnf("issue %s: description changed on both sides; kept the repo's, the tracker had %q", rec.DisplayID(), t.Description)
	}

	if pushTitle || pushDesc {
		if err := m.Tracker.UpdateIssue(ctx, rec.Tracker.ID, title, desc); err != nil {
			return cm, fmt.Errorf("update tracker issue %s: %w", rec.Tracker.ID, err)
		}
		cm.pushed = true
	}

	var ops []*Op
	if pullTitle {
		ops = append(ops, &Op{Type: OpSetTitle, Value: title})
	}
	if pullDesc {
		ops = append(ops, &Op{Type: OpSetDescription, Value: desc})
	}
	if len(ops) > 0 {
		if err := appendAll(ctx, c, rec.ID, ops...); err != nil {
			return cm, err
		}
		rec.Title, rec.Description, cm.pulled = title, desc, true
	}

	return cm, nil
}

// resolveField applies the rule of one field: l the baseline, r the record, t
// the tracker. The side that differs from l moved. Both sides moved apart is
// a conflict the record wins.
func resolveField(l, r, t string) (final string, pull, push, conflict bool) {
	switch {
	case t == l && r == l:
		return r, false, false, false
	case t != l && r == l:
		return t, true, false, false
	case r != l && t == l:
		return r, false, true, false
	case r == t:
		return r, false, false, false
	default:
		return r, false, true, true
	}
}

// baseline is the snapshot, or the record itself when the chain has none (a
// link written before snapshots existed): the tracker is then pulled
// wherever it differs.
func baseline(rec *Record) TrackerSnapshot {
	if rec.TrackerSeen != nil {
		return *rec.TrackerSeen
	}

	return TrackerSnapshot{Title: rec.Title, Description: rec.Description, Labels: slices.Clone(rec.Labels)}
}

// contentSettled reports that nothing moved on either side since the
// snapshot: the tracker's updated time is the snapshot's, the record equals
// the snapshot, and every comment is on the tracker.
func contentSettled(rec *Record, t *tracker.Issue) bool {
	s := rec.TrackerSeen
	if s == nil || !t.UpdatedAt.Equal(s.Updated) {
		return false
	}
	if rec.Title != s.Title || rec.Description != s.Description || !slices.Equal(rec.Labels, s.Labels) {
		return false
	}
	for _, c := range rec.Comments {
		if c.TrackerID == "" {
			return false
		}
	}

	return true
}

// snapshotOf is the snapshot of t: what the tracker shows.
func snapshotOf(t *tracker.Issue) TrackerSnapshot {
	labels := slices.Clone(t.Labels)
	if labels == nil {
		labels = []string{}
	}
	slices.Sort(labels)

	return TrackerSnapshot{Title: t.Subject, Description: t.Description, Labels: labels, Updated: t.UpdatedAt}
}

func sameSnapshot(a, b *TrackerSnapshot) bool {
	return a != nil && a.Title == b.Title && a.Description == b.Description &&
		slices.Equal(a.Labels, b.Labels) && a.Updated.Equal(b.Updated)
}

// snapshotOp is the tracker_seen op of s.
func snapshotOp(s *TrackerSnapshot) *Op {
	op := &Op{Type: OpTrackerSeen, Title: s.Title, Description: s.Description, Labels: s.Labels}
	if !s.Updated.IsZero() {
		op.Updated = s.Updated.UTC().Format(time.RFC3339Nano)
	}

	return op
}
```

`importOps` adds the labels and the snapshot:

```go
// importOps are the ops that follow the root of an imported issue.
func importOps(iss *tracker.Issue) []*Op {
	ops := []*Op{{Type: OpSetTitle, Value: iss.Subject}}
	if iss.Description != "" {
		ops = append(ops, &Op{Type: OpSetDescription, Value: iss.Description})
	}
	for _, l := range iss.Labels {
		ops = append(ops, &Op{Type: OpAddLabel, Value: l})
	}
	snap := snapshotOf(iss)

	return append(ops, &Op{Type: OpTrackerState, Value: StateOpen, Status: iss.Status}, snapshotOp(&snap))
}
```

`export` returns the issue and writes the snapshot, and links the record in memory:

```go
// export creates the tracker issue of a record that has none, links them and
// returns the created issue, so that the caller syncs the record against it
// at once.
func (m *Mirror) export(ctx context.Context, c *git.Client, rec *Record) (tracker.Issue, error) {
	created, err := m.Tracker.CreateIssue(ctx, rec.Title, rec.Description)
	if err != nil {
		return tracker.Issue{}, fmt.Errorf("create tracker issue: %w", err)
	}

	snap := snapshotOf(&created)
	err = appendAll(ctx, c, rec.ID,
		&Op{Type: OpLinkTracker, TrackerType: m.Type, Project: m.Project, TrackerID: created.ID},
		&Op{Type: OpTrackerState, Value: StateOpen, Status: created.Status},
		snapshotOp(&snap))
	if err != nil {
		// The next run exports again: the issue named here is the orphan.
		return tracker.Issue{}, fmt.Errorf("link tracker issue %s (created, now orphaned): %w", created.ID, err)
	}
	rec.Tracker = &TrackerLink{Type: m.Type, Project: m.Project, ID: created.ID}
	rec.TrackerState, rec.TrackerStatus, rec.TrackerSeen = StateOpen, created.Status, &snap

	return created, nil
}
```

Add `"slices"` and `"time"` to the imports if missing. `TestReconcile_StateTable`, `TestReconcile_StatusNameChange` and `TestReconcile_PushedReopenSettles` keep passing: a tracker close now reads status `closed` from the fake's `GetIssue`, as `IsIssueClosed` gave before.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/ -run "TestReconcile|TestFold" -v`
Expected: PASS, every existing reconcile test included.

- [ ] **Step 5: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go
git commit -m "feat(issue): the mirror syncs title and description with a snapshot baseline"
```

---

### Task 8: Mirror: labels, and the revert on a rule rejection

**Files:**
- Modify: `issue/mirror.go` (`syncContent`)
- Test: `issue/mirror_test.go`

**Interfaces:**
- Consumes: Task 7 (`syncContent`, `baseline`), Task 6 (`SetLabels`, `tracker.ErrLabelRule`, fake `Edit`, `RecordedLabelSets`, `StatusIsLabel`).
- Produces: `func resolveLabels(l, r, t []string) (final []string, pull []*Op, push bool)`.

- [ ] **Step 1: Write the failing tests**

Append to `issue/mirror_test.go`:

```go
func TestReconcile_Labels(t *testing.T) {
	t.Parallel()

	// The baseline is {bug}. Each row names what each side did to it.
	rows := []struct {
		name   string
		r, tr  []string
		want   []string
		pulled int
		pushed int
		sets   int // SetLabels calls
	}{
		{"nobody moved", []string{"bug"}, []string{"bug"}, []string{"bug"}, 0, 0, 0},
		{"the tracker added one", []string{"bug"}, []string{"bug", "ui"}, []string{"bug", "ui"}, 1, 0, 0},
		{"the tracker removed one", []string{"bug"}, nil, []string{}, 1, 0, 0},
		{"the repo added one", []string{"bug", "needs triage"}, []string{"bug"}, []string{"bug", "needs triage"}, 0, 1, 1},
		{"the repo removed one", nil, []string{"bug"}, []string{}, 0, 1, 1},
		{"both added the same one", []string{"bug", "ui"}, []string{"bug", "ui"}, []string{"bug", "ui"}, 0, 0, 0},
		{"each side added its own", []string{"bug", "ui"}, []string{"bug", "doc"}, []string{"bug", "doc", "ui"}, 1, 1, 1},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			c := newRepo(t, "alice", "")
			ctx := t.Context()
			iss := trackerIssue("42", "A")
			iss.Labels = []string{"bug"}
			ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}, Labels: []string{"bug", "ui", "doc"}}
			m := newTestMirror(ft)
			mustReconcile(t, m, c)
			rec := onlyRecord(t, c)

			for _, op := range diffLabelsForTest(rec.Labels, row.r) {
				if err := Append(ctx, c, rec.ID, op); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if !slices.Equal(row.tr, []string{"bug"}) {
				ft.Edit("42", func(iss *tracker.Issue) { iss.Labels = slices.Clone(row.tr) })
			}

			res := mustReconcile(t, m, c)
			got, _ := Load(ctx, c, rec.ID)
			onTracker, _ := ft.GetIssue(ctx, "42")
			sorted := slices.Clone(onTracker.Labels)
			slices.Sort(sorted)

			if !slices.Equal(got.Labels, row.want) || !slices.Equal(sorted, row.want) {
				t.Errorf("repo labels = %q, tracker labels = %q, want both %q", got.Labels, sorted, row.want)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed || len(ft.RecordedLabelSets) != row.sets {
				t.Errorf("pulled/pushed/sets = %d/%d/%d, want %d/%d/%d", res.Pulled, res.Pushed, len(ft.RecordedLabelSets), row.pulled, row.pushed, row.sets)
			}
			if got.TrackerSeen == nil || !slices.Equal(got.TrackerSeen.Labels, row.want) {
				t.Errorf("snapshot labels = %q, want %q", got.TrackerSeen, row.want)
			}
		})
	}
}

// diffLabelsForTest is the add/remove ops that take labels from have to want.
func diffLabelsForTest(have, want []string) []*Op {
	var ops []*Op
	for _, l := range have {
		if !slices.Contains(want, l) {
			ops = append(ops, &Op{Type: OpRemoveLabel, Value: l})
		}
	}
	for _, l := range want {
		if !slices.Contains(have, l) {
			ops = append(ops, &Op{Type: OpAddLabel, Value: l})
		}
	}

	return ops
}

func TestReconcile_LabelRuleRejection(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	iss := trackerIssue("42", "A")
	iss.Status, iss.Labels = "New", []string{"New"}
	ft := &fake.Tracker{StatusIsLabel: true, Statuses: []string{"New", "Closed"}, ClosingStatuses: []string{"Closed"},
		ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	rec := onlyRecord(t, c)
	if err := Append(ctx, c, rec.ID, &Op{Type: OpAddLabel, Value: "urgent"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, m, c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("the record takes the tracker's labels back, with one warning naming the dropped name", func(t *testing.T) {
		if !slices.Equal(got.Labels, []string{"New"}) || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "urgent") {
			t.Errorf("labels = %q, warnings = %q", got.Labels, res.Warnings)
		}
	})

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("the run after is quiet", func(t *testing.T) {
		if len(again.Warnings) != 0 || opCount(t, c, rec.ID) != before {
			t.Errorf("warnings = %q, ops %d → %d", again.Warnings, before, opCount(t, c, rec.ID))
		}
	})
}

func TestReconcile_LabelTransportFailureKeepsTheChange(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	rec := onlyRecord(t, c)
	if err := Append(ctx, c, rec.ID, &Op{Type: OpAddLabel, Value: "urgent"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	ft.SetLabelsErr = errors.New("502")
	res := mustReconcile(t, m, c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("a transport failure is a warning and the local label stays", func(t *testing.T) {
		if !slices.Equal(got.Labels, []string{"urgent"}) || len(res.Warnings) != 1 {
			t.Errorf("labels = %q, warnings = %q", got.Labels, res.Warnings)
		}
	})

	ft.SetLabelsErr = nil
	mustReconcile(t, m, c)
	onTracker, _ := ft.GetIssue(ctx, "42")

	t.Run("the next run pushes it", func(t *testing.T) {
		if !slices.Equal(onTracker.Labels, []string{"urgent"}) {
			t.Errorf("tracker labels = %q", onTracker.Labels)
		}
	})
}
```

Add `SetLabelsErr error` to the fake: `SetLabels` returns it first when non-nil (one line at the top of the method, in `tracker/fake/fake.go`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestReconcile_Labels|TestReconcile_LabelRuleRejection|TestReconcile_LabelTransportFailureKeepsTheChange" -v`
Expected: FAIL (labels are not synced).

- [ ] **Step 3: Implement**

In `issue/mirror.go`, add `resolveLabels` and the labels step in `syncContent`, between the title/description push and the pull ops:

```go
// resolveLabels applies the rule to every name of l, r and t: the side that
// differs from the baseline moved. It returns the final set, the add/remove
// ops that take the record to it, and whether the tracker must be set.
func resolveLabels(l, r, t []string) (final []string, pull []*Op, push bool) {
	names := slices.Concat(l, r, t)
	slices.Sort(names)
	names = slices.Compact(names)

	for _, name := range names {
		inL, inR, inT := slices.Contains(l, name), slices.Contains(r, name), slices.Contains(t, name)
		switch {
		case inT && !inL && !inR:
			pull = append(pull, &Op{Type: OpAddLabel, Value: name})
			final = append(final, name)
		case !inT && inL && inR:
			pull = append(pull, &Op{Type: OpRemoveLabel, Value: name})
		case inR && !inL && !inT, !inR && inL && inT:
			push = true
			if inR {
				final = append(final, name)
			}
		case inR:
			final = append(final, name)
		}
	}
	if final == nil {
		final = []string{}
	}

	return final, pull, push
}
```

In `syncContent`, after the title/description push block:

```go
	labels, pullLabels, pushLabels := resolveLabels(base.Labels, rec.Labels, t.Labels)
	if pushLabels {
		err := m.Tracker.SetLabels(ctx, rec.Tracker.ID, labels)
		switch {
		case errors.Is(err, tracker.ErrLabelRule):
			// The record's labels cannot exist on the tracker: take the
			// tracker's back, once, and say which names are dropped.
			var dropped []string
			for _, name := range rec.Labels {
				if !slices.Contains(t.Labels, name) {
					dropped = append(dropped, name)
				}
			}
			res.warnf("issue %s: labels %q dropped, %v", rec.DisplayID(), dropped, err)
			labels, pullLabels, pushLabels = resolveLabels(rec.Labels, rec.Labels, t.Labels)
		case err != nil:
			return cm, fmt.Errorf("set labels of tracker issue %s: %w", rec.Tracker.ID, err)
		default:
			cm.pushed = true
		}
	}
```

Then extend the pull block: `ops = append(ops, pullLabels...)` before the `len(ops) > 0` check, and set `rec.Labels = labels` next to `rec.Title, rec.Description = title, desc`. `resolveLabels(rec.Labels, rec.Labels, t.Labels)` with the record as baseline yields exactly the ops that make the record equal the tracker.

Add `"errors"` to the imports. The push of title/description and the push of labels are independent: a failed `UpdateIssue` returns before the labels; a failed `SetLabels` returns before the pull ops, so the title pushed is re-read as equal on the next run and nothing is pushed twice.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/ ./tracker/fake/ -run "TestReconcile|TestFake" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go tracker/fake/fake.go
git commit -m "feat(issue): the mirror syncs labels and takes the tracker's back on a rule rejection"
```

---

### Task 9: Mirror: comments, both ways

**Files:**
- Modify: `issue/mirror.go` (`syncContent`, `importIssue`, `healImport`)
- Test: `issue/mirror_test.go`

**Interfaces:**
- Consumes: Task 1 (`OpLinkComment`, the tracker fields of `add_comment`, `Comment.TrackerID`), Task 6 (`ListComments`, `AddComment` returning the comment, fake `Comment`, `RecordedComments`).
- Produces: `func (m *Mirror) pullComments(ctx, c, rec *Record, t *tracker.Issue) (pulled bool, err error)`, `func (m *Mirror) pushComments(ctx, c, rec *Record) (pushed bool, err error)`.

- [ ] **Step 1: Write the failing tests**

Append to `issue/mirror_test.go`:

```go
func TestReconcile_Comments(t *testing.T) {
	t.Parallel()

	t.Run("an import brings the tracker's comments with their author, time and ID", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
		web := ft.WebComment("42", "bob", "From the web")
		mustReconcile(t, newTestMirror(ft), c)
		rec := onlyRecord(t, c)
		if len(rec.Comments) != 1 || rec.Comments[0].Author != "bob" || rec.Comments[0].TrackerID != web.ID ||
			rec.Comments[0].Body != "From the web" || !rec.Comments[0].At.Equal(web.CreatedAt) {
			t.Errorf("comments = %+v", rec.Comments)
		}
	})

	t.Run("a tracker comment made after the import is pulled, once", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.WebComment("42", "bob", "Later")
		res := mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if len(got.Comments) != 1 || got.Comments[0].Body != "Later" || res.Pulled != 1 {
			t.Errorf("comments = %+v, res = %+v", got.Comments, res)
		}
		before := opCount(t, c, rec.ID)
		mustReconcile(t, m, c)
		if opCount(t, c, rec.ID) != before {
			t.Errorf("ops %d → %d on the run after", before, opCount(t, c, rec.ID))
		}
	})

	t.Run("a repo comment is posted and linked, and not pulled back", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "From the repo"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		res := mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if len(ft.RecordedComments) != 1 || ft.RecordedComments[0].Body != "From the repo" || res.Pushed != 1 {
			t.Errorf("posted = %+v, res = %+v", ft.RecordedComments, res)
		}
		if len(got.Comments) != 1 || got.Comments[0].TrackerID != ft.RecordedComments[0].ID {
			t.Errorf("comments = %+v", got.Comments)
		}
		mustReconcile(t, m, c)
		got, _ = Load(ctx, c, rec.ID)
		if len(got.Comments) != 1 || len(ft.RecordedComments) != 1 {
			t.Errorf("after a second run: comments = %+v, posted = %+v", got.Comments, ft.RecordedComments)
		}
	})

	t.Run("an empty tracker comment is not pulled", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.WebComment("42", "bot", "")
		mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if len(got.Comments) != 0 {
			t.Errorf("comments = %+v", got.Comments)
		}
	})

	t.Run("two clones pulling the same comment offline fold to one", func(t *testing.T) {
		t.Parallel()

		origin := newOrigin(t)
		a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
		ctx := t.Context()
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
		mustReconcile(t, newTestMirror(ft), a)
		if _, err := Fetch(ctx, b); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		ft.WebComment("42", "bob", "Seen by both")
		mustReconcile(t, newTestMirror(ft), a)
		mustReconcile(t, newTestMirror(ft), b)
		if _, err := Fetch(ctx, a); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if ra := onlyRecord(t, a); len(ra.Comments) != 1 {
			t.Errorf("comments = %+v", ra.Comments)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestReconcile_Comments" -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `issue/mirror.go`:

```go
// pullComments appends the tracker's comments the record lacks, oldest
// first, when the tracker moved since the snapshot. An empty body is skipped.
func (m *Mirror) pullComments(ctx context.Context, c *git.Client, rec *Record, t *tracker.Issue) (bool, error) {
	if rec.TrackerSeen != nil && t.UpdatedAt.Equal(rec.TrackerSeen.Updated) {
		return false, nil
	}

	comments, err := m.Tracker.ListComments(ctx, rec.Tracker.ID)
	if err != nil {
		return false, fmt.Errorf("list comments of tracker issue %s: %w", rec.Tracker.ID, err)
	}
	known := make(map[string]bool, len(rec.Comments))
	for _, rc := range rec.Comments {
		known[rc.TrackerID] = true
	}

	var ops []*Op
	for _, tc := range comments {
		if tc.Body == "" || known[tc.ID] {
			continue
		}
		ops = append(ops, &Op{
			Type: OpAddComment, Body: tc.Body, TrackerCommentID: tc.ID, TrackerAuthor: tc.Author,
			TrackerAt: tc.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
		rec.Comments = append(rec.Comments, Comment{Author: tc.Author, At: tc.CreatedAt, Body: tc.Body, TrackerID: tc.ID})
	}
	if len(ops) == 0 {
		return false, nil
	}

	return true, appendAll(ctx, c, rec.ID, ops...)
}

// pushComments posts every record comment the tracker has not got and links
// each one with the ID it was given.
func (m *Mirror) pushComments(ctx context.Context, c *git.Client, rec *Record) (bool, error) {
	pushed := false
	for i := range rec.Comments {
		rc := &rec.Comments[i]
		if rc.TrackerID != "" {
			continue
		}
		posted, err := m.Tracker.AddComment(ctx, rec.Tracker.ID, rc.Body)
		if err != nil {
			return pushed, fmt.Errorf("post comment on tracker issue %s: %w", rec.Tracker.ID, err)
		}
		pushed = true
		if err := appendAll(ctx, c, rec.ID, &Op{Type: OpLinkComment, Comment: rc.ID, TrackerCommentID: posted.ID}); err != nil {
			// The next run posts it again: the comment named here is the orphan.
			return pushed, fmt.Errorf("link comment %s (posted on tracker issue %s as %s, now orphaned): %w",
				rc.ID, rec.Tracker.ID, posted.ID, err)
		}
		rc.TrackerID = posted.ID
	}

	return pushed, nil
}
```

In `syncContent`, after the pull-ops block (title, description, labels are written), add the comment steps, pull first so that a posted comment is never pulled back as a new one:

```go
	pulledComments, err := m.pullComments(ctx, c, rec, t)
	cm.pulled = cm.pulled || pulledComments
	if err != nil {
		return cm, err
	}
	pushedComments, err := m.pushComments(ctx, c, rec)
	cm.pushed = cm.pushed || pushedComments
	if err != nil {
		return cm, err
	}
```

`importIssue` and `healImport` pull the comments of the issue they import: after `appendAll(ctx, c, id, importOps(iss)...)`, load nothing, build the record in memory and call `pullComments` with a zero snapshot so that the listing is read:

```go
// importContent writes the ops that follow the root of an imported issue and
// pulls its comments. A failure to list the comments is not an error: the
// snapshot written here has no updated time, so the next run reads them.
func (m *Mirror) importContent(ctx context.Context, c *git.Client, id string, iss *tracker.Issue, warn func(string, ...any)) error {
	ops := importOps(iss)
	snapshot := ops[len(ops)-1]

	comments, err := m.Tracker.ListComments(ctx, iss.ID)
	if err != nil {
		warn("import comments of tracker issue %s: %v (they come with the next run)", iss.ID, err)
		snapshot.Updated = ""
	}
	if err := appendAll(ctx, c, id, ops[:len(ops)-1]...); err != nil {
		return err
	}
	for _, tc := range comments {
		if tc.Body == "" {
			continue
		}
		op := &Op{Type: OpAddComment, Body: tc.Body, TrackerCommentID: tc.ID, TrackerAuthor: tc.Author,
			TrackerAt: tc.CreatedAt.UTC().Format(time.RFC3339Nano)}
		if err := appendAll(ctx, c, id, op); err != nil {
			return err
		}
	}

	return appendAll(ctx, c, id, snapshot)
}
```

Replace the two calls of `appendAll(ctx, c, <id>, importOps(iss)...)` with `m.importContent(ctx, c, <id>, iss, warn)`. `importIssue` becomes `(m *Mirror) importIssue(ctx, c, iss *tracker.Issue, warn func(string, ...any)) (string, error)` and `Reconcile` passes `res.warnf`; `healImport` becomes a method, `(m *Mirror) healImport(ctx, c, bare, iss, warn)`, with the same body otherwise.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/ -run "TestReconcile" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go
git commit -m "feat(issue): the mirror syncs comments both ways"
```

---

### Task 10: Mirror: `SyncOne`, and state, content, state on a single-label tracker

**Files:**
- Modify: `issue/mirror.go`
- Test: `issue/mirror_test.go`

**Interfaces:**
- Consumes: Task 7 (`syncRecord`), Task 8, Task 9.
- Produces: `func (m *Mirror) SyncOne(ctx context.Context, c *git.Client, id string) (MirrorResult, error)`: the four steps on one record, then `Push`. A nil `*Mirror` is a no-op. A record the mirror does not own is a no-op.

- [ ] **Step 1: Write the failing tests**

Append to `issue/mirror_test.go`:

```go
func TestSyncOne(t *testing.T) {
	t.Parallel()

	t.Run("a nil mirror does nothing", func(t *testing.T) {
		t.Parallel()

		var m *Mirror
		if res, err := m.SyncOne(t.Context(), newRepo(t, "alice", ""), "x"); err != nil || res.Pushed != 0 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})

	t.Run("pushes the record's change to the tracker without a listing", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		listings := ft.ListProjectCalls
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: "B"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		res, err := m.SyncOne(ctx, c, rec.ID)
		onTracker, _ := ft.GetIssue(ctx, "42")
		if err != nil || res.Pushed != 1 || onTracker.Subject != "B" || ft.ListProjectCalls != listings {
			t.Errorf("res = %+v, err = %v, tracker title = %q, listings %d → %d", res, err, onTracker.Subject, listings, ft.ListProjectCalls)
		}
	})

	t.Run("a record without a link is left alone", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ctx := t.Context()
		rec, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ft := &fake.Tracker{}
		if res, err := newTestMirror(ft).SyncOne(ctx, c, rec.ID); err != nil || res.Exported != 0 || len(ft.RecordedCreates) != 0 {
			t.Errorf("res = %+v, err = %v, creates = %+v", res, err, ft.RecordedCreates)
		}
	})

	t.Run("a tracker failure is returned, the record untouched", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.GetErr = errors.New("502")
		before := opCount(t, c, rec.ID)
		if _, err := m.SyncOne(t.Context(), c, rec.ID); err == nil || opCount(t, c, rec.ID) != before {
			t.Errorf("err = %v, ops %d → %d", err, before, opCount(t, c, rec.ID))
		}
	})
}

// newStatusIsLabelMirror imports issue 42 in status "New" on a fake that
// behaves like Redmine.
func newStatusIsLabelMirror(t *testing.T) (*git.Client, *fake.Tracker, *Mirror, Record) {
	t.Helper()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "A")
	iss.Status = "New"
	ft := &fake.Tracker{StatusIsLabel: true, Statuses: []string{"New", "Resolved", "Closed"},
		ClosingStatuses: []string{"Closed"}, ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)

	return c, ft, m, onlyRecord(t, c)
}

func TestReconcile_StatusIsLabel(t *testing.T) {
	t.Parallel()

	t.Run("the imported record carries the status as its one label", func(t *testing.T) {
		t.Parallel()

		_, _, _, rec := newStatusIsLabelMirror(t)
		if !slices.Equal(rec.Labels, []string{"New"}) {
			t.Errorf("labels = %q", rec.Labels)
		}
	})

	t.Run("a close in the repo gives the record the closing status as label in one run", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if !closed || !slices.Equal(got.Labels, []string{"Closed"}) || got.State != StateClosed {
			t.Errorf("tracker closed = %v, record = %+v", closed, got)
		}
		if len(ft.RecordedLabelSets) != 0 {
			t.Errorf("SetLabels was called: %+v", ft.RecordedLabelSets)
		}
	})

	t.Run("a closing status set as a label closes the record in the same run", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		for _, op := range []*Op{{Type: OpRemoveLabel, Value: "New"}, {Type: OpAddLabel, Value: "Closed"}} {
			if err := Append(ctx, c, rec.ID, op); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if !closed || got.State != StateClosed || !slices.Equal(got.Labels, []string{"Closed"}) || got.TrackerStatus != "Closed" {
			t.Errorf("tracker closed = %v, record = %+v", closed, got)
		}
	})

	t.Run("a status picked on the tracker is pulled as the label", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		if err := ft.UpdateIssueStatus(ctx, "42", "Resolved"); err != nil {
			t.Fatalf("UpdateIssueStatus: %v", err)
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if !slices.Equal(got.Labels, []string{"Resolved"}) || got.TrackerStatus != "Resolved" {
			t.Errorf("record = %+v", got)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./issue/ -run "TestSyncOne|TestReconcile_StatusIsLabel" -v`
Expected: `SyncOne` undefined; the status-as-label rows fail or leave the record open.

- [ ] **Step 3: Implement**

In `issue/mirror.go`:

```go
// SyncOne runs the four steps on one record and pushes it: what `edit`,
// `label` and `comment` call after their push, so that the tracker gets the
// change at once. The issue is read with one call, no listing. A nil Mirror,
// or a record the mirror does not own, is a no-op.
func (m *Mirror) SyncOne(ctx context.Context, c *git.Client, id string) (MirrorResult, error) {
	var res MirrorResult
	if m == nil {
		return res, nil
	}

	rec, err := Load(ctx, c, id)
	if err != nil {
		return res, err
	}
	if !m.owns(&rec) {
		return res, nil
	}

	pulled, pushed, changed, err := m.syncRecord(ctx, c, &rec, nil, &res)
	if pulled {
		res.Pulled++
	}
	if pushed {
		res.Pushed++
	}
	if changed {
		if perr := Push(ctx, c, id); perr != nil {
			res.warnf("issue saved locally but not pushed (run `git zf issue sync` later): %v", perr)
		}
	}

	return res, err
}
```

`syncRecord` with `listed == nil` reads the issue through `trackerIssue`: a record closed on both sides is a no-op, as in the reconcile. The three `StatusIsLabel` rows are what Task 7's state, content, state order was built for; if a row fails, check that `syncState` updates `rec.State` in memory on a pull and that `syncRecord` re-reads after the content push before the second state pass.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./issue/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add issue/mirror.go issue/mirror_test.go
git commit -m "feat(issue): SyncOne, and the status as label settles in one run"
```

---

### Task 11: `issue edit` and `issue comment` reach the tracker at once

**Files:**
- Modify: `cmd/issue/record.go` (new helper `syncOne`)
- Modify: `cmd/issue/edit.go:58-116`
- Modify: `cmd/issue/comment.go:36-72`
- Test: `cmd/issue/mirror_e2e_test.go`, and the existing calls of `runEdit` / `runComment` in `cmd/issue/record_ops_e2e_test.go`

**Interfaces:**
- Consumes: Task 10 (`Mirror.SyncOne`).
- Produces:
  - `func syncOne(ctx context.Context, client *git.Client, m *issuepkg.Mirror, id string)` in `cmd/issue/record.go`: runs `SyncOne`, prints its warnings and a failure as a warning.
  - `runEdit(ctx, client, p, args, flags, m *issuepkg.Mirror)`, `runComment(ctx, client, p, args, message, m *issuepkg.Mirror)`.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/issue/mirror_e2e_test.go`:

```go
// importedRecord imports tracker issue 42 into the rig and returns its record.
func importedRecord(t *testing.T, rig *recordRig, ft *fake.Tracker, m *issuepkg.Mirror) issuepkg.Record {
	t.Helper()

	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "open")}
	if _, err := m.Reconcile(t.Context(), rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	return rig.onlyRecord(t)
}

func TestMirror_EditReachesTheTrackerAtOnce(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)
	title := "B"

	err := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &title}, m)
	onTracker, _ := ft.GetIssue(ctx, "42")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runEdit: %v", err)
		}
	})
	t.Run("the tracker has the new title, with no project listing", func(t *testing.T) {
		if onTracker.Subject != "B" || ft.ListProjectCalls != 1 {
			t.Errorf("tracker title = %q, listings = %d", onTracker.Subject, ft.ListProjectCalls)
		}
	})
	t.Run("the record is settled: a reconcile writes nothing", func(t *testing.T) {
		res, _ := m.Reconcile(ctx, rig.client)
		if res.Pulled+res.Pushed != 0 {
			t.Errorf("res = %+v", res)
		}
	})
}

func TestMirror_CommentReachesTheTrackerAtOnce(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)

	err := runComment(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, "Posted", m)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runComment: %v", err)
		}
	})
	t.Run("the comment is posted and linked", func(t *testing.T) {
		if len(ft.RecordedComments) != 1 || ft.RecordedComments[0].Body != "Posted" ||
			len(got.Comments) != 1 || got.Comments[0].TrackerID != ft.RecordedComments[0].ID {
			t.Errorf("posted = %+v, comments = %+v", ft.RecordedComments, got.Comments)
		}
	})
}

func TestMirror_ShowPrintsATrackerComment(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "open")}
	ft.WebComment("42", "bob", "From the web")
	if _, err := m.Reconcile(t.Context(), rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec := rig.onlyRecord(t)

	var out bytes.Buffer
	renderRecord(&out, &rec)

	t.Run("the comment shows its tracker author", func(t *testing.T) {
		if !strings.Contains(out.String(), "bob") || !strings.Contains(out.String(), "From the web") {
			t.Errorf("output = %q", out.String())
		}
	})
}

func TestMirror_EditWithTheTrackerDown(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)
	ft.GetErr = errors.New("502")
	title := "B"

	err := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &title}, m)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("the edit is saved and the tracker failure is one warning", func(t *testing.T) {
		if err != nil || got.Title != "B" || !strings.Contains(rig.stderr.String(), "warning") {
			t.Errorf("err = %v, title = %q, stderr = %q", err, got.Title, rig.stderr.String())
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/ -run "TestMirror_Edit|TestMirror_Comment" -v`
Expected: compile error, `runEdit` takes five arguments.

- [ ] **Step 3: Implement**

In `cmd/issue/record.go`, next to `reconcileIssues`:

```go
// syncOne gives the tracker one record's change at once, after the record
// was pushed. A failure is a warning: the next reconcile retries from the
// chain. A nil m is a no-op.
func syncOne(ctx context.Context, client *git.Client, m *issuepkg.Mirror, id string) {
	res, err := m.SyncOne(ctx, client, id)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: tracker mirror: %v\n", err)
	}
	printWarnings(client.IO().Err, res.Warnings)
}
```

In `cmd/issue/edit.go`: `runEdit` gains `m *issuepkg.Mirror` as its last parameter and calls `syncOne(ctx, client, m, rec.ID)` right after `pushIssue`; `editRunE` passes `openMirror(i.appConfig, client.IO().Err)`.

In `cmd/issue/comment.go`: the same for `runComment` and `commentRunE`.

Fix the existing callers in `cmd/issue/record_ops_e2e_test.go` by passing `nil`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/ -run "TestMirror_|TestRunEdit|TestRunComment" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/record.go cmd/issue/edit.go cmd/issue/comment.go cmd/issue/mirror_e2e_test.go cmd/issue/record_ops_e2e_test.go
git commit -m "feat(issue): edit and comment reach the tracker at once"
```

---

### Task 12: `issue label`: the picker over the tracker's labels, the single pick, the sync

**Files:**
- Modify: `cmd/issue/label.go`
- Modify: `cmd/issue/record.go:23-34` (`recordPrompter`), `:79-87` (`huhRecordPrompter.LabelChanges`)
- Modify: `tui/issue_record.go:105-114`
- Modify: `cmd/issue/record_e2e_test.go` (`scriptedRecordPrompter`)
- Test: `cmd/issue/record_ops_e2e_test.go` (`TestRunLabel`), `cmd/issue/mirror_e2e_test.go`

**Interfaces:**
- Consumes: Task 6 (`ListLabels`, `tracker.SingleLabel`), Task 11 (`syncOne`).
- Produces:
  - `recordPrompter.LabelChanges(ctx, choices, current []string, single bool) ([]string, error)`: the final set. `choices == nil` means the text line of today, applied to `current`.
  - `func applyLabelChanges(current []string, ops []issuepkg.Op) []string`, `func diffLabels(current, final []string) []issuepkg.Op` in `cmd/issue/label.go`.
  - `runLabel(ctx, client, p, args, m *issuepkg.Mirror)`.
  - `tui.IssueLabelPicker(choices, current []string, selected *[]string, extra *string) *huh.Group`, `tui.IssueStatusPicker(choices []string, current string, picked *string) *huh.Group`.

- [ ] **Step 1: Write the failing tests**

In `cmd/issue/record_e2e_test.go`, change the scripted prompter: replace the `Labels string` field by

```go
	// LabelSet is what LabelChanges returns; LabelOffered records what it was
	// offered.
	LabelSet     []string
	LabelOffered struct {
		Choices, Current []string
		Single           bool
	}
```

and the method by

```go
func (s *scriptedRecordPrompter) LabelChanges(_ context.Context, choices, current []string, single bool) ([]string, error) {
	s.LabelOffered.Choices, s.LabelOffered.Current, s.LabelOffered.Single = choices, current, single

	return s.LabelSet, s.Err
}
```

In `TestRunLabel` (`record_ops_e2e_test.go`), the subtest "without tokens the form supplies them" becomes:

```go
	t.Run("without tokens the form supplies the final set", func(t *testing.T) {
		p := &scriptedRecordPrompter{LabelSet: []string{"bug", "p1"}}
		if err := runLabel(ctx, rig.client, p, []string{rec.ID}, nil); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if got := labels(t); !slices.Equal(got, []string{"bug", "p1"}) {
			t.Errorf("labels = %v", got)
		}
		if p.LabelOffered.Choices != nil || !slices.Equal(p.LabelOffered.Current, []string{"bug", "ui"}) || p.LabelOffered.Single {
			t.Errorf("offered = %+v, want no choices, the current labels, not single", p.LabelOffered)
		}
	})
```

Every other `runLabel(…)` call in that file gets a trailing `nil`.

Append to `cmd/issue/mirror_e2e_test.go`:

```go
func TestMirror_LabelPicker(t *testing.T) {
	t.Parallel()

	t.Run("the form is offered the tracker's labels united with the issue's, the issue's as current", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.Labels = []string{"bug", "ui"}
		rec := importedRecord(t, rig, ft, m)
		if err := issuepkg.Append(ctx, rig.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddLabel, Value: "local"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		p := &scriptedRecordPrompter{LabelSet: []string{"local", "ui"}}

		if err := runLabel(ctx, rig.client, p, []string{rec.ID}, m); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if !slices.Equal(p.LabelOffered.Choices, []string{"bug", "local", "ui"}) || !slices.Equal(p.LabelOffered.Current, []string{"local"}) || p.LabelOffered.Single {
			t.Errorf("offered = %+v", p.LabelOffered)
		}
		onTracker, _ := ft.GetIssue(ctx, "42")
		sorted := slices.Clone(onTracker.Labels)
		slices.Sort(sorted)
		if !slices.Equal(sorted, []string{"local", "ui"}) {
			t.Errorf("tracker labels = %q, want local and ui at once", onTracker.Labels)
		}
	})

	t.Run("on a single-label tracker the form is a single pick among the statuses", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses, ft.ClosingStatuses = true, []string{"New", "Resolved"}, []string{"Closed"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)
		p := &scriptedRecordPrompter{LabelSet: []string{"Resolved"}}

		if err := runLabel(ctx, rig.client, p, []string{rec.ID}, m); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if !p.LabelOffered.Single || !slices.Equal(p.LabelOffered.Choices, []string{"New", "Resolved"}) {
			t.Errorf("offered = %+v", p.LabelOffered)
		}
		if onTracker, _ := ft.GetIssue(ctx, "42"); onTracker.Status != "Resolved" {
			t.Errorf("tracker status = %q", onTracker.Status)
		}
	})

	t.Run("on a single-label tracker tokens that leave two labels are refused before any op", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses = true, []string{"New", "Resolved"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)

		err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID, "+urgent"}, m)
		got, _ := issuepkg.Load(ctx, rig.client, rec.ID)
		if err == nil || !slices.Equal(got.Labels, []string{"New"}) {
			t.Errorf("err = %v, labels = %q", err, got.Labels)
		}
	})

	t.Run("with the tracker down the text form is used and tokens are written unchecked", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses = true, []string{"New"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)
		ft.ListLabelsErr, ft.GetErr = errors.New("502"), errors.New("502")

		err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID, "+urgent"}, m)
		got, _ := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || !slices.Equal(got.Labels, []string{"New", "urgent"}) {
			t.Errorf("err = %v, labels = %q", err, got.Labels)
		}
	})
}
```

Add `ListLabelsErr error` to the fake, returned first by `ListLabels` when non-nil.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/ -run "TestRunLabel|TestMirror_LabelPicker" -v`
Expected: compile errors on the prompter and `runLabel`.

- [ ] **Step 3: Implement the forms**

In `tui/issue_record.go`, keep `IssueLabelInput` and add:

```go
// IssueLabelPicker is the `issue label` form when the tracker's labels are
// known: a multi-select over choices, current preselected, plus a line for
// names not in the list.
func IssueLabelPicker(choices, current []string, selected *[]string, extra *string) *huh.Group {
	opts := make([]huh.Option[string], len(choices))
	for i, name := range choices {
		opts[i] = huh.NewOption(name, name).Selected(slices.Contains(current, name))
	}

	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Labels:").
			Description("space/x toggles, enter confirms").
			Options(opts...).
			Value(selected),
		huh.NewInput().
			Title("New labels (space separated, optional):").
			Value(extra),
	)
}

// IssueStatusPicker is the `issue label` form on a tracker whose one label
// is the status: a single pick.
func IssueStatusPicker(choices []string, current string, picked *string) *huh.Group {
	opts := make([]huh.Option[string], len(choices))
	for i, name := range choices {
		opts[i] = huh.NewOption(name, name).Selected(name == current)
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Status:").
			Options(opts...).
			Value(picked),
	)
}
```

- [ ] **Step 4: Implement the prompter and the command**

In `cmd/issue/record.go`, the interface line becomes:

```go
	// LabelChanges returns the issue's final labels. choices are the labels
	// the tracker knows (nil: unknown, the text line applies to current);
	// single means one label only, the status.
	LabelChanges(ctx context.Context, choices, current []string, single bool) ([]string, error)
```

and `huhRecordPrompter.LabelChanges`:

```go
func (huhRecordPrompter) LabelChanges(ctx context.Context, choices, current []string, single bool) ([]string, error) {
	switch {
	case choices == nil:
		var changes string
		if err := huh.NewForm(tui.IssueLabelInput(&changes)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("label form: %w", err)
		}
		ops, err := parseLabelChanges(strings.Fields(changes))
		if err != nil {
			return nil, err
		}

		return applyLabelChanges(current, ops), nil
	case single:
		picked := ""
		if len(current) > 0 {
			picked = current[0]
		}
		if err := huh.NewForm(tui.IssueStatusPicker(choices, picked, &picked)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("status form: %w", err)
		}

		return []string{picked}, nil
	default:
		selected, extra := slices.Clone(current), ""
		if err := huh.NewForm(tui.IssueLabelPicker(choices, current, &selected, &extra)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("label form: %w", err)
		}

		return cleanLabels(append(selected, strings.Fields(extra)...)), nil
	}
}
```

In `cmd/issue/label.go`, add the two set helpers and rewrite `runLabel`:

```go
// applyLabelChanges applies add/remove ops to current and returns the sorted
// result.
func applyLabelChanges(current []string, ops []issuepkg.Op) []string {
	set := make(map[string]bool, len(current))
	for _, l := range current {
		set[l] = true
	}
	for _, op := range ops {
		if op.Type == issuepkg.OpAddLabel {
			set[op.Value] = true
		} else {
			delete(set, op.Value)
		}
	}
	final := make([]string, 0, len(set))
	for l := range set {
		final = append(final, l)
	}
	slices.Sort(final)

	return final
}

// diffLabels is the ops that take current to final.
func diffLabels(current, final []string) []issuepkg.Op {
	var ops []issuepkg.Op
	for _, l := range current {
		if !slices.Contains(final, l) {
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpRemoveLabel, Value: l})
		}
	}
	for _, l := range final {
		if !slices.Contains(current, l) {
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpAddLabel, Value: l})
		}
	}

	return ops
}

// labelChoices asks the tracker for its labels when the mirror is on. nil
// when the mirror is off or the tracker does not answer: the form is then
// the text line and the tokens are not checked.
func labelChoices(ctx context.Context, client *git.Client, m *issuepkg.Mirror, current []string) (choices []string, single bool) {
	if m == nil {
		return nil, false
	}
	names, err := m.Tracker.ListLabels(ctx)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: could not list the tracker's labels: %v\n", err)

		return nil, false
	}
	choices = cleanLabels(append(names, current...))
	single = tracker.SingleLabel(m.Tracker)
	if !single {
		slices.Sort(choices)
	}

	return choices, single
}

func runLabel(ctx context.Context, client *git.Client, p recordPrompter, args []string, m *issuepkg.Mirror) error {
	fetchIssues(ctx, client)

	var idArgs, tokens []string
	if len(args) > 0 {
		idArgs, tokens = args[:1], args[1:]
	}

	rec, err := resolveRecord(ctx, client, p, idArgs)
	if err != nil {
		return err
	}
	choices, single := labelChoices(ctx, client, m, rec.Labels)

	var final []string
	if len(tokens) == 0 {
		final, err = p.LabelChanges(ctx, choices, rec.Labels, single)
		if err != nil {
			return fmt.Errorf("label form: %w", err)
		}
	} else {
		changes, err := parseLabelChanges(tokens)
		if err != nil {
			return err
		}
		final = applyLabelChanges(rec.Labels, changes)
	}
	if single && choices != nil && (len(final) != 1 || !slices.Contains(choices, final[0])) {
		return fmt.Errorf("this tracker's one label is the status: want exactly one of %s, got %q", strings.Join(choices, ", "), final)
	}

	ops := diffLabels(rec.Labels, final)
	if len(ops) == 0 {
		return fmt.Errorf("no label change given for issue %s", rec.DisplayID())
	}

	for i := range ops {
		if err := issuepkg.Append(ctx, client, rec.ID, &ops[i]); err != nil {
			return fmt.Errorf("update labels: %w", err)
		}
	}

	pushIssue(ctx, client, rec.ID)
	syncOne(ctx, client, m, rec.ID)

	updated, err := issuepkg.Load(ctx, client, rec.ID)
	if err != nil {
		return fmt.Errorf("reload issue: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issue %s labels: %s\n", updated.DisplayID(), strings.Join(updated.Labels, ", "))

	return nil
}
```

`cleanLabels` (`record.go:100`) trims and deduplicates; check whether it sorts, and keep the status order of `ListLabels` for the single pick (the `!single` sort above assumes it does not). `labelRunE` passes `openMirror(i.appConfig, client.IO().Err)`. Add `"slices"` and `"github.com/piprim/git-zf/tracker"` to the imports. Update the `Long` help of the command: with the mirror on, the form lists the tracker's labels, and on Redmine the one label is the status.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/ ./tui/ ./tracker/fake/ -v 2>&1 | grep -v "^=== RUN"`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/label.go cmd/issue/record.go cmd/issue/record_e2e_test.go cmd/issue/record_ops_e2e_test.go cmd/issue/mirror_e2e_test.go tui/issue_record.go tracker/fake/fake.go
git commit -m "feat(issue): the label form offers the tracker's labels, one pick on redmine"
```

---

### Task 13: The close flow on a single-label tracker, and the documentation

**Files:**
- Test: `cmd/issue/close_mirror_e2e_test.go`
- Modify: `README.md:379-405`, `docs/issue-refs.md:85-130`, `CLAUDE.md:110-140`, `ROADMAP.md:4-6`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write the failing close-flow test**

Append to `cmd/issue/close_mirror_e2e_test.go`:

```go
// closeStatusIsLabel is closeTrackerBorn on a fake that behaves like
// Redmine: the status is the one label.
func closeStatusIsLabel(t *testing.T, status string) (*closeTestRig, issuepkg.Record, error) {
	t.Helper()

	rig := newCloseRig(t)
	ctx := t.Context()
	m := mirrorOn(rig)
	rig.tracker.StatusIsLabel = true
	rig.tracker.Statuses = []string{"New", "Resolved", "Closed"}
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

func TestClose_Mirror_StatusIsLabel(t *testing.T) {
	t.Parallel()

	t.Run("a closing status picked at close becomes the record's label in one run", func(t *testing.T) {
		t.Parallel()

		_, rec, err := closeStatusIsLabel(t, "Closed")
		if err != nil || rec.State != issuepkg.StateClosed || !slices.Equal(rec.Labels, []string{"Closed"}) {
			t.Errorf("err = %v, record = %+v", err, rec)
		}
	})

	t.Run("a status that leaves the issue open is the label and the record stays open", func(t *testing.T) {
		t.Parallel()

		_, rec, err := closeStatusIsLabel(t, "Resolved")
		if err != nil || rec.State != issuepkg.StateOpen || !slices.Equal(rec.Labels, []string{"Resolved"}) {
			t.Errorf("err = %v, record = %+v", err, rec)
		}
	})
}
```

Run: `mise exec -- go test ./cmd/issue/ -run "TestClose_Mirror_StatusIsLabel" -v`
Expected: PASS already if Tasks 7 to 10 are right (the close flow reconciles after the status picker); a failure here means the reconcile's second state pass or the label pull is wrong, fix it in `issue/mirror.go`.

- [ ] **Step 2: README**

In `README.md`, after the `mirror` row of the tracker table, add a paragraph:

```markdown
**With the mirror on**, title, description, labels and comments travel both ways: a web edit shows up in `issue show`, and `issue edit`, `issue label` and `issue comment` reach the tracker at once. When both sides changed a title or a description between two syncs, the repo wins and one warning names the tracker's value. A label the tracker lacks is created there. `issue label` offers the tracker's labels. On Redmine, which has no labels, the status is the one label: `issue label +Resolved -New` is a status change, and the form is a single pick. After upgrading to this version, run `git zf issue sync` once before editing: the first run pulls the tracker's content wherever it differs from the repo.
```

- [ ] **Step 3: `docs/issue-refs.md`**

In the "Tracker mirror" section, change the third bullet and the sentence after the list:

```markdown
- title, description, labels, comments and open/closed travel both ways.

`git zf issue list`, `issue new`, `issue close <id>`, `issue close` (merge)
and `issue sync` reconcile the whole project. `edit`, `comment` and `label`
sync the one issue they changed. `show` only reads the refs.
```

And add, after "Which side moved":

```markdown
Content follows the same rule with `tracker_seen`, the snapshot of the
tracker's title, description, labels and updated time: a field that differs
from the snapshot moved. A title or a description changed on both sides to
different values is kept as the repo has it, with a warning. A comment is
append-only: one pulled from the tracker carries the tracker's comment ID,
author and time on its `add_comment`; one posted from the repo gets a
`link_comment` with the ID the tracker gave it. An issue whose updated time
is the snapshot's and whose record equals the snapshot costs no call beyond
the listing.
```

- [ ] **Step 4: `CLAUDE.md`**

In "Testing the tracker mirror", extend the list and the commands:

```markdown
- `issue/mirror_test.go` — the content: `TestReconcile_TitleAndDescription`,
  `TestReconcile_Labels`, `TestReconcile_LabelRuleRejection`,
  `TestReconcile_Comments`, `TestReconcile_StatusIsLabel`, `TestSyncOne`.

    mise exec -- go test ./issue/... -run "TestReconcile_(TitleAndDescription|Labels|LabelRuleRejection|Comments|StatusIsLabel)|TestSyncOne" -v
    mise exec -- go test ./cmd/issue/... -run "^TestMirror_(Edit|Comment|LabelPicker)|^TestClose_Mirror_StatusIsLabel" -v

The fake's `Edit` and `Comment` play the tracker's web UI (not recorded, the
issue's `UpdatedAt` moves); `StatusIsLabel` makes it behave like Redmine. A
reconcile runs state, content, state again, then writes `tracker_seen`; a
`SetLabels` that returns `tracker.ErrLabelRule` makes the record take the
tracker's labels back, any other error keeps the local change.
```

- [ ] **Step 5: ROADMAP**

In `ROADMAP.md`, item 1.b becomes `b. [done] Sync of title, description, labels and comments. Spec: docs/superpowers/specs/2026-10-10-issue-mirror-content-sync-design.md`.

- [ ] **Step 6: Run everything and commit**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: PASS.

```bash
git add cmd/issue/close_mirror_e2e_test.go README.md docs/issue-refs.md CLAUDE.md ROADMAP.md
git commit -m "docs: the issue mirror syncs content; close on a single-label tracker tested"
```
