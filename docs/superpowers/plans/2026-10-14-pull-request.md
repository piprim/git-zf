# Pull Requests on the Forge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open a pull request on GitHub or Forgejo once per branch (at the push step of `git zf commit`, or with `git zf branch pr`), and let `issue close` and `branch merge` merge a branch that has one through the forge.

**Architecture:** An optional tracker capability (`tracker.PullRequests`, discovered by type assertion like `SingleLabel`) implemented by the GitHub and Forgejo adapters and the fake. One core in `cmd/branch/pr.go` (`OpenPullRequest`) serves `branch pr` and the push proposal of `commit`. The merge engine (`cmd/mergeflow`) gets a forge path: strategy picker and confirm as today, then `MergePullRequest` and a fast-forward of the local target; `issue close` and `branch merge` enter it when the branch has an open pull request.

**Tech Stack:** Go 1.25 (via `mise exec -- go`), go-github v73, Forgejo API v1 over the adapter's JSON client, huh forms through `tui.Form` / `tui.NewForm`, real on-disk git repos and the in-process fake tracker in tests.

**Spec:** `docs/superpowers/specs/2026-10-14-pull-request-design.md`

## Global Constraints

- Every `Test*` function wraps each distinct assertion in a named `t.Run` (user's global rule).
- Run Go through mise: `mise exec -- go test ./...`, `mise exec -- go build ./...`.
- `MergeMethod` is a `tracker` type, never `commit.MergeStrategy`: the tracker package must not import `commit`.
- No draft pull requests, no reviewers, labels or assignees, no forge that is not the issue tracker (spec, "Out of scope").
- Every adapter returns `PullRequest` through `Clean()`; messages quote the caller's path only (Forgejo: the token must not reach an error string).
- Unknown forge errors come back with the forge's message, no retry.
- Forms are built with `tui.Form` (esc aborts with `huh.ErrUserAborted`) or `tui.NewForm`, never `huh.NewForm`.
- A subcommand offered by a menu reads its flags tolerantly (`pushflow.ReadFlags`, `cmdutil.StringFlag`): the branch menu runs it with the parent command.
- Commit after each task with a Conventional Commits message (`feat(tracker): …`, `test(…): …`).

## Review Focus

- A pull request opened from a fork with the same head ref: Forgejo's open listing includes it and close would merge someone else's pull request. Pinned in Task 4: `FindPullRequest` ignores a pull request whose `head.repo.full_name` is not the configured repo.
- The local base has commits of its own when the forge has merged: the fast-forward is refused. Expected: a warning, the close still reports the merge. Pinned in Task 9.
- `git zf branch pr` in a clone without a remote: nothing can reach the forge. Expected: a refusal naming the missing remote. Pinned in Task 7.
- `git zf branch pr master`: a pull request from the base onto itself. Expected: a refusal. Pinned in Task 7.
- A forge refusal carrying a message (Forgejo 405 "not mergeable"): the operator must read why. Pinned in Task 4 (the message reaches the error string).

---

### Task 1: The tracker capability

**Files:**
- Modify: `tracker/tracker.go`
- Test: `tracker/tracker_test.go`

**Interfaces:**
- Consumes: `internal/text.Line`, the existing `Tracker` interface.
- Produces: `tracker.PullRequest{Number, URL, Head, Base string}` with `Clean()`, `tracker.MergeMethod` (`MergeMethodMerge`, `MergeMethodSquash`, `MergeMethodRebase`), `tracker.ErrNoPullRequest`, `tracker.PullRequests` interface (`FindPullRequest`, `CreatePullRequest`, `MergePullRequest`), `tracker.PullRequestsOf(t Tracker) PullRequests`.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/tracker_test.go`:

```go
// prTracker is a stub with the pull request capability.
type prTracker struct{ stubTracker }

func (prTracker) FindPullRequest(_ context.Context, _ string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, tracker.ErrNoPullRequest
}

func (prTracker) CreatePullRequest(_ context.Context, _, _, _, _ string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, nil
}

func (prTracker) MergePullRequest(_ context.Context, _ string, _ tracker.MergeMethod) error { return nil }

func TestPullRequestsOf(t *testing.T) {
	t.Parallel()

	t.Run("nil and a tracker without the methods have no capability", func(t *testing.T) {
		t.Parallel()

		if tracker.PullRequestsOf(nil) != nil || tracker.PullRequestsOf(&stubTracker{}) != nil {
			t.Error("want nil")
		}
	})
	t.Run("a wrapped tracker hides the methods", func(t *testing.T) {
		t.Parallel()

		if tracker.PullRequestsOf(struct{ tracker.Tracker }{prTracker{}}) != nil {
			t.Error("want nil")
		}
	})
	t.Run("a tracker with the methods is returned", func(t *testing.T) {
		t.Parallel()

		if tracker.PullRequestsOf(prTracker{}) == nil {
			t.Error("want the capability")
		}
	})
}

func TestPullRequestClean(t *testing.T) {
	t.Parallel()

	got := tracker.PullRequest{Number: "7\n", URL: "https://x/pull/7\x1b[31m", Head: "feat\x07", Base: "main\r"}.Clean()

	t.Run("every field is one line without control characters", func(t *testing.T) {
		t.Parallel()

		if got.Number != "7" || got.URL != "https://x/pull/7[31m" || got.Head != "feat" || got.Base != "main" {
			t.Errorf("got %+v", got)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/ -run "TestPullRequestsOf|TestPullRequestClean" -v`
Expected: compile error, `undefined: tracker.PullRequest`.

- [ ] **Step 3: Write the capability**

Append to `tracker/tracker.go`:

```go
// PullRequest is one pull request of the forge, as it names things.
type PullRequest struct {
	Number string // the forge's number, "12"
	URL    string
	Head   string // branch name
	Base   string
}

// Clean drops the control characters, as Issue.Clean does.
func (p PullRequest) Clean() PullRequest {
	p.Number, p.URL, p.Head, p.Base = text.Line(p.Number), text.Line(p.URL), text.Line(p.Head), text.Line(p.Base)

	return p
}

// MergeMethod is how the forge merges a pull request.
type MergeMethod string

const (
	MergeMethodMerge  MergeMethod = "merge"
	MergeMethodSquash MergeMethod = "squash"
	MergeMethodRebase MergeMethod = "rebase"
)

// ErrNoPullRequest is returned by FindPullRequest when the branch has no open
// pull request.
var ErrNoPullRequest = errors.New("tracker: no open pull request")

// PullRequests is the optional capability of a tracker that is also the forge
// hosting the repository: GitHub and Forgejo. Discover it with PullRequestsOf.
type PullRequests interface {
	// FindPullRequest returns the open pull request whose head is branch.
	FindPullRequest(ctx context.Context, branch string) (PullRequest, error)
	// CreatePullRequest opens one and returns it with its number and URL.
	CreatePullRequest(ctx context.Context, head, base, title, body string) (PullRequest, error)
	// MergePullRequest merges number with method. A refusal of the forge
	// (checks failing, not mergeable) is an error carrying its message.
	MergePullRequest(ctx context.Context, number string, method MergeMethod) error
}

// PullRequestsOf returns t's capability, or nil when the tracker has none
// (or t is nil).
func PullRequestsOf(t Tracker) PullRequests {
	p, ok := t.(PullRequests)
	if !ok {
		return nil
	}

	return p
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/ -run "TestPullRequestsOf|TestPullRequestClean" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/tracker.go tracker/tracker_test.go
git commit -m "feat(tracker): optional pull request capability"
```

---

### Task 2: The fake tracker's pull requests

**Files:**
- Modify: `tracker/fake/fake.go`
- Test: `tracker/fake/fake_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces: fields `PullRequests []tracker.PullRequest`, `RecordedPullRequests []PullRequestCreate`, `RecordedMerges []Merge`, `MergeError error` on `fake.Tracker`; methods `FindPullRequest`, `CreatePullRequest`, `MergePullRequest`; types `fake.PullRequestCreate{Head, Base, Title, Body string}`, `fake.Merge{Number string; Method tracker.MergeMethod}`.

- [ ] **Step 1: Write the failing tests**

Append to `tracker/fake/fake_test.go`:

```go
func TestFake_PullRequests(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("the fake has the capability", func(t *testing.T) {
		t.Parallel()

		if tracker.PullRequestsOf(&Tracker{}) == nil {
			t.Error("want the capability")
		}
	})

	t.Run("FindPullRequest matches the head, ErrNoPullRequest otherwise", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feat", Base: "main"}}}
		if pr, err := ft.FindPullRequest(ctx, "feat"); err != nil || pr.Number != "7" {
			t.Errorf("FindPullRequest(feat) = %+v, %v", pr, err)
		}
		if _, err := ft.FindPullRequest(ctx, "other"); !errors.Is(err, tracker.ErrNoPullRequest) {
			t.Errorf("FindPullRequest(other) err = %v", err)
		}
	})

	t.Run("CreatePullRequest records, numbers and lists the pull request", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{PullRequests: []tracker.PullRequest{{Number: "3", Head: "a", Base: "main"}}}
		pr, err := ft.CreatePullRequest(ctx, "feat", "main", "Add thing", "Closes #1")
		if err != nil {
			t.Fatalf("CreatePullRequest: %v", err)
		}
		if pr.Number != "4" || pr.URL != "fake://pr/4" || pr.Head != "feat" || pr.Base != "main" {
			t.Errorf("pr = %+v", pr)
		}
		want := PullRequestCreate{Head: "feat", Base: "main", Title: "Add thing", Body: "Closes #1"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
		if found, err := ft.FindPullRequest(ctx, "feat"); err != nil || found.Number != "4" {
			t.Errorf("FindPullRequest after create = %+v, %v", found, err)
		}
	})

	t.Run("MergePullRequest records the method and removes the pull request", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feat", Base: "main"}}}
		if err := ft.MergePullRequest(ctx, "7", tracker.MergeMethodSquash); err != nil {
			t.Fatalf("MergePullRequest: %v", err)
		}
		if len(ft.RecordedMerges) != 1 || ft.RecordedMerges[0] != (Merge{Number: "7", Method: tracker.MergeMethodSquash}) {
			t.Errorf("RecordedMerges = %+v", ft.RecordedMerges)
		}
		if _, err := ft.FindPullRequest(ctx, "feat"); !errors.Is(err, tracker.ErrNoPullRequest) {
			t.Errorf("the merged pull request is still open: %v", err)
		}
	})

	t.Run("MergePullRequest of an unknown number is an error", func(t *testing.T) {
		t.Parallel()

		if err := (&Tracker{}).MergePullRequest(ctx, "9", tracker.MergeMethodMerge); err == nil {
			t.Error("want an error")
		}
	})

	t.Run("MergeError is the forge's refusal: nothing is recorded or removed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{
			PullRequests: []tracker.PullRequest{{Number: "7", Head: "feat", Base: "main"}},
			MergeError:   errors.New("Pull Request is not mergeable"),
		}
		err := ft.MergePullRequest(ctx, "7", tracker.MergeMethodMerge)
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Errorf("err = %v", err)
		}
		if len(ft.RecordedMerges) != 0 || len(ft.PullRequests) != 1 {
			t.Errorf("merges %+v, open %+v", ft.RecordedMerges, ft.PullRequests)
		}
	})
}
```

Add `"errors"` and `"strings"` to the test file's imports if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/fake/ -run TestFake_PullRequests -v`
Expected: compile error, `ft.FindPullRequest undefined`.

- [ ] **Step 3: Write the fake's methods**

In `tracker/fake/fake.go`, add to the `Tracker` struct (after `ListLabelsCalls int`):

```go
	// PullRequests are the forge's open pull requests; FindPullRequest
	// matches their Head. CreatePullRequest appends, MergePullRequest
	// removes.
	PullRequests         []tracker.PullRequest
	RecordedPullRequests []PullRequestCreate
	RecordedMerges       []Merge
	// MergeError, when non-nil, is returned by MergePullRequest: the forge
	// refuses the merge (checks failing, not mergeable).
	MergeError error
```

Add the record types after `Comment`:

```go
// PullRequestCreate captures one CreatePullRequest call.
type PullRequestCreate struct {
	Head, Base, Title, Body string
}

// Merge captures one MergePullRequest call.
type Merge struct {
	Number string
	Method tracker.MergeMethod
}
```

Append the methods:

```go
// FindPullRequest returns the open pull request whose head is branch.
func (t *Tracker) FindPullRequest(_ context.Context, branch string) (tracker.PullRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, pr := range t.PullRequests {
		if pr.Head == branch {
			return pr, nil
		}
	}

	return tracker.PullRequest{}, tracker.ErrNoPullRequest
}

// CreatePullRequest records the call and lists the pull request under the
// next free number.
func (t *Tracker) CreatePullRequest(_ context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := 1
	for _, pr := range t.PullRequests {
		if n, err := strconv.Atoi(pr.Number); err == nil && n >= next {
			next = n + 1
		}
	}
	num := strconv.Itoa(next)
	pr := tracker.PullRequest{Number: num, URL: "fake://pr/" + num, Head: head, Base: base}
	t.RecordedPullRequests = append(t.RecordedPullRequests, PullRequestCreate{Head: head, Base: base, Title: title, Body: body})
	t.PullRequests = append(t.PullRequests, pr)

	return pr, nil
}

// MergePullRequest records the merge and removes the pull request, or
// returns MergeError untouched.
func (t *Tracker) MergePullRequest(_ context.Context, number string, method tracker.MergeMethod) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.MergeError != nil {
		return t.MergeError
	}

	i := slices.IndexFunc(t.PullRequests, func(pr tracker.PullRequest) bool { return pr.Number == number })
	if i < 0 {
		return fmt.Errorf("fake: no open pull request #%s", number)
	}
	t.PullRequests = slices.Delete(t.PullRequests, i, i+1)
	t.RecordedMerges = append(t.RecordedMerges, Merge{Number: number, Method: method})

	return nil
}
```

`slices` and `fmt` are already imported by `fake.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/... -v -run "TestFake_PullRequests|TestPullRequestsOf"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker/fake/fake.go tracker/fake/fake_test.go
git commit -m "feat(tracker): the fake tracker has pull requests"
```

---

### Task 3: The GitHub adapter

**Files:**
- Create: `tracker/github/pullrequest.go`
- Test: `tracker/github/pullrequest_test.go`

**Interfaces:**
- Consumes: Task 1; `githubAdapter.ownerRepo()`, `newTestAdapter(t, srv, projects)` and `withProjects` from `github_test.go`.
- Produces: `*githubAdapter` satisfies `tracker.PullRequests`.

- [ ] **Step 1: Write the failing tests**

Create `tracker/github/pullrequest_test.go`:

```go
package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

func TestFindPullRequest(t *testing.T) {
	t.Parallel()

	t.Run("lists the open pull requests of the head and returns the first", func(t *testing.T) {
		t.Parallel()

		var gotHead, gotState string
		mux := http.NewServeMux()
		mux.HandleFunc("/repos/a/b/pulls", func(w http.ResponseWriter, r *http.Request) {
			gotHead, gotState = r.URL.Query().Get("head"), r.URL.Query().Get("state")
			fmt.Fprint(w, `[{"number":12,"html_url":"https://github.com/a/b/pull/12","head":{"ref":"feat"},"base":{"ref":"main"}}]`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		pr, err := newTestAdapter(t, srv, []string{"a/b"}).FindPullRequest(t.Context(), "feat")
		if err != nil {
			t.Fatalf("FindPullRequest: %v", err)
		}
		if gotHead != "a:feat" || gotState != "open" {
			t.Errorf("query head=%q state=%q", gotHead, gotState)
		}
		want := tracker.PullRequest{Number: "12", URL: "https://github.com/a/b/pull/12", Head: "feat", Base: "main"}
		if pr != want {
			t.Errorf("pr = %+v, want %+v", pr, want)
		}
	})

	t.Run("an empty listing is ErrNoPullRequest", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/repos/a/b/pulls", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })
		srv := httptest.NewServer(mux)
		defer srv.Close()

		if _, err := newTestAdapter(t, srv, []string{"a/b"}).FindPullRequest(t.Context(), "feat"); !errors.Is(err, tracker.ErrNoPullRequest) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCreatePullRequest(t *testing.T) {
	t.Parallel()

	var sent struct{ Title, Head, Base, Body string }
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/a/b/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)

			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number":13,"html_url":"https://github.com/a/b/pull/13","head":{"ref":"feat"},"base":{"ref":"main"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pr, err := newTestAdapter(t, srv, []string{"a/b"}).CreatePullRequest(t.Context(), "feat", "main", "Add thing", "Closes #1")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("CreatePullRequest: %v", err)
		}
	})
	t.Run("the head, base, title and body are sent", func(t *testing.T) {
		if sent.Head != "feat" || sent.Base != "main" || sent.Title != "Add thing" || sent.Body != "Closes #1" {
			t.Errorf("sent = %+v", sent)
		}
	})
	t.Run("the number and URL come back", func(t *testing.T) {
		if pr.Number != "13" || pr.URL != "https://github.com/a/b/pull/13" {
			t.Errorf("pr = %+v", pr)
		}
	})
}

func TestMergePullRequest(t *testing.T) {
	t.Parallel()

	t.Run("sends the merge method", func(t *testing.T) {
		t.Parallel()

		var sent struct {
			MergeMethod string `json:"merge_method"`
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/repos/a/b/pulls/12/merge", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				http.Error(w, "method", http.StatusMethodNotAllowed)

				return
			}
			_ = json.NewDecoder(r.Body).Decode(&sent)
			fmt.Fprint(w, `{"merged":true,"message":"Pull Request successfully merged"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		if err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "12", tracker.MergeMethodSquash); err != nil {
			t.Fatalf("MergePullRequest: %v", err)
		}
		if sent.MergeMethod != "squash" {
			t.Errorf("merge_method = %q", sent.MergeMethod)
		}
	})

	t.Run("a 405 comes back with the forge's message", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/repos/a/b/pulls/12/merge", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprint(w, `{"message":"Pull Request is not mergeable"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "12", tracker.MergeMethodMerge)
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a number that is not a number is an error", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.NewServeMux())
		defer srv.Close()

		if err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "x", tracker.MergeMethodMerge); err == nil {
			t.Error("want an error")
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/github/ -run "PullRequest" -v`
Expected: compile error, `FindPullRequest undefined`.

- [ ] **Step 3: Write the adapter methods**

Create `tracker/github/pullrequest.go`:

```go
package github

import (
	"context"
	"fmt"
	"strconv"

	gogithub "github.com/google/go-github/v73/github"

	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: GitHub is a forge.
var _ tracker.PullRequests = (*githubAdapter)(nil)

// FindPullRequest lists the open pull requests whose head is owner:branch
// and returns the first; GitHub allows one open pull request per head.
func (a *githubAdapter) FindPullRequest(ctx context.Context, branch string) (tracker.PullRequest, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	prs, _, err := a.client.PullRequests.List(ctx, owner, repo, &gogithub.PullRequestListOptions{
		State: statusOpen, Head: owner + ":" + branch, ListOptions: gogithub.ListOptions{PerPage: 1},
	})
	if err != nil {
		return tracker.PullRequest{}, fmt.Errorf("github: list pull requests of %q: %w", branch, err)
	}
	if len(prs) == 0 {
		return tracker.PullRequest{}, tracker.ErrNoPullRequest
	}

	return toPullRequest(prs[0]), nil
}

// CreatePullRequest opens a pull request of head against base.
func (a *githubAdapter) CreatePullRequest(ctx context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	pr, _, err := a.client.PullRequests.Create(ctx, owner, repo, &gogithub.NewPullRequest{
		Title: gogithub.Ptr(title), Head: gogithub.Ptr(head), Base: gogithub.Ptr(base), Body: gogithub.Ptr(body),
	})
	if err != nil {
		return tracker.PullRequest{}, fmt.Errorf("github: create pull request for %q: %w", head, err)
	}

	return toPullRequest(pr), nil
}

// MergePullRequest merges the pull request with method. GitHub answers 405
// when it is not mergeable and 409 when the head moved; go-github returns
// both with the API's message.
func (a *githubAdapter) MergePullRequest(ctx context.Context, number string, method tracker.MergeMethod) error {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return err
	}
	n, err := strconv.Atoi(number)
	if err != nil {
		return fmt.Errorf("github: invalid pull request number %q: %w", number, err)
	}

	res, _, err := a.client.PullRequests.Merge(ctx, owner, repo, n, "", &gogithub.PullRequestOptions{MergeMethod: string(method)})
	if err != nil {
		return fmt.Errorf("github: merge pull request #%d: %w", n, err)
	}
	if !res.GetMerged() {
		return fmt.Errorf("github: merge pull request #%d: %s", n, res.GetMessage())
	}

	return nil
}

func toPullRequest(pr *gogithub.PullRequest) tracker.PullRequest {
	return tracker.PullRequest{
		Number: strconv.Itoa(pr.GetNumber()),
		URL:    pr.GetHTMLURL(),
		Head:   pr.GetHead().GetRef(),
		Base:   pr.GetBase().GetRef(),
	}.Clean()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/github/ -v`
Expected: PASS, including the pre-existing tests.

- [ ] **Step 5: Commit**

```bash
git add tracker/github/pullrequest.go tracker/github/pullrequest_test.go
git commit -m "feat(github): pull requests: find, create, merge"
```

---

### Task 4: The Forgejo adapter

**Files:**
- Create: `tracker/forgejo/pullrequest.go`
- Modify: `tracker/forgejo/forgejo.go` (the `httpError` message)
- Test: `tracker/forgejo/pullrequest_test.go`

**Interfaces:**
- Consumes: Task 1; `forgejoAdapter.doJSON`, `ownerRepo`, `issuesPerPage`, `maxPages`, `statusOpen`; `newMirrorAdapter(t, handler)` from `forgejo_test.go`.
- Produces: `*forgejoAdapter` satisfies `tracker.PullRequests`; `httpError.Message` carries the API's `message` field.

- [ ] **Step 1: Write the failing tests**

Create `tracker/forgejo/pullrequest_test.go`:

```go
package forgejo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

func TestFindPullRequest(t *testing.T) {
	t.Parallel()

	t.Run("walks the open pull requests and matches the head ref", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/repos/a/b/pulls" || r.URL.Query().Get("state") != "open" {
				http.Error(w, "unexpected "+r.URL.String(), http.StatusBadRequest)

				return
			}
			switch r.URL.Query().Get("page") {
			case "1":
				fmt.Fprint(w, `[{"number":1,"html_url":"https://f/a/b/pulls/1","head":{"ref":"other","repo":{"full_name":"a/b"}},"base":{"ref":"main"}}]`)
			case "2":
				fmt.Fprint(w, `[{"number":2,"html_url":"https://f/a/b/pulls/2","head":{"ref":"feat","repo":{"full_name":"a/b"}},"base":{"ref":"main"}}]`)
			default:
				fmt.Fprint(w, `[]`)
			}
		})

		pr, err := a.FindPullRequest(t.Context(), "feat")
		if err != nil {
			t.Fatalf("FindPullRequest: %v", err)
		}
		want := tracker.PullRequest{Number: "2", URL: "https://f/a/b/pulls/2", Head: "feat", Base: "main"}
		if pr != want {
			t.Errorf("pr = %+v, want %+v", pr, want)
		}
	})

	t.Run("a pull request from a fork with the same head ref is ignored", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("page") == "1" {
				fmt.Fprint(w, `[{"number":5,"head":{"ref":"feat","repo":{"full_name":"fork/b"}},"base":{"ref":"main"}}]`)

				return
			}
			fmt.Fprint(w, `[]`)
		})

		if _, err := a.FindPullRequest(t.Context(), "feat"); !errors.Is(err, tracker.ErrNoPullRequest) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("no match is ErrNoPullRequest", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `[]`) })
		if _, err := a.FindPullRequest(t.Context(), "feat"); !errors.Is(err, tracker.ErrNoPullRequest) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestCreatePullRequest(t *testing.T) {
	t.Parallel()

	var sent struct{ Head, Base, Title, Body string }
	a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/a/b/pulls" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

			return
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"number":9,"html_url":"https://f/a/b/pulls/9","head":{"ref":"feat"},"base":{"ref":"main"}}`)
	})

	pr, err := a.CreatePullRequest(t.Context(), "feat", "main", "Add thing", "Closes #1")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("CreatePullRequest: %v", err)
		}
	})
	t.Run("the head, base, title and body are sent", func(t *testing.T) {
		if sent.Head != "feat" || sent.Base != "main" || sent.Title != "Add thing" || sent.Body != "Closes #1" {
			t.Errorf("sent = %+v", sent)
		}
	})
	t.Run("the number and URL come back", func(t *testing.T) {
		if pr.Number != "9" || pr.URL != "https://f/a/b/pulls/9" || pr.Head != "feat" || pr.Base != "main" {
			t.Errorf("pr = %+v", pr)
		}
	})
}

func TestMergePullRequest(t *testing.T) {
	t.Parallel()

	t.Run("posts the merge method", func(t *testing.T) {
		t.Parallel()

		var sent struct{ Do string }
		a := newMirrorAdapter(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/v1/repos/a/b/pulls/9/merge" {
				http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusBadRequest)

				return
			}
			_ = json.NewDecoder(r.Body).Decode(&sent)
			w.WriteHeader(http.StatusOK)
		})

		if err := a.MergePullRequest(t.Context(), "9", tracker.MergeMethodRebase); err != nil {
			t.Fatalf("MergePullRequest: %v", err)
		}
		if sent.Do != "rebase" {
			t.Errorf("Do = %q", sent.Do)
		}
	})

	t.Run("a 405 comes back with the forge's message", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprint(w, `{"message":"Please try again later"}`)
		})

		err := a.MergePullRequest(t.Context(), "9", tracker.MergeMethodMerge)
		if err == nil || !strings.Contains(err.Error(), "405") || !strings.Contains(err.Error(), "Please try again later") {
			t.Errorf("err = %v", err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./tracker/forgejo/ -run "PullRequest" -v`
Expected: compile error, `a.FindPullRequest undefined`.

- [ ] **Step 3: Carry the API's message on a non-2xx response**

In `tracker/forgejo/forgejo.go`, replace the `httpError` type and the non-2xx branch of `doJSON`:

```go
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&apiErr)

		return &httpError{Method: method, Path: path, StatusCode: resp.StatusCode, Message: text.Line(apiErr.Message)}
	}
```

```go
// httpError reports a non-2xx response, with the API's message when it sent
// one.
type httpError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *httpError) Error() string {
	s := fmt.Sprintf("%s %s: unexpected status %d", e.Method, e.Path, e.StatusCode)
	if e.Message != "" {
		s += ": " + e.Message
	}

	return s
}
```

Import `"github.com/piprim/git-zf/internal/text"` (and `"io"` if not already there). The existing tests assert on the status number and still pass.

- [ ] **Step 4: Write the adapter methods**

Create `tracker/forgejo/pullrequest.go`:

```go
package forgejo

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: Forgejo is a forge.
var _ tracker.PullRequests = (*forgejoAdapter)(nil)

// pullRequest is the subset of the Forgejo PullRequest object we consume.
type pullRequest struct {
	Number int `json:"number"`
	//nolint:tagliatelle // Forgejo wire format
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref  string      `json:"ref"`
		Repo *repository `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (a *forgejoAdapter) pullsPath() (path, project string, err error) {
	owner, repo, err := a.ownerRepo()
	if err != nil {
		return "", "", err
	}

	return "/repos/" + owner + "/" + repo + "/pulls", owner + "/" + repo, nil
}

// FindPullRequest walks the open pull requests of the repository and returns
// the one whose head is branch in this repository (the API has no head
// filter; a fork's pull request with the same ref is not ours).
func (a *forgejoAdapter) FindPullRequest(ctx context.Context, branch string) (tracker.PullRequest, error) {
	path, project, err := a.pullsPath()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	q := url.Values{}
	q.Set("state", statusOpen)
	q.Set("limit", strconv.Itoa(issuesPerPage))

	for page := 1; page <= maxPages; page++ {
		q.Set("page", strconv.Itoa(page))

		var batch []pullRequest
		if err := a.doJSON(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &batch); err != nil {
			return tracker.PullRequest{}, fmt.Errorf("forgejo: list pull requests: %w", err)
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			pr := &batch[i]
			if pr.Head.Ref == branch && (pr.Head.Repo == nil || pr.Head.Repo.FullName == project) {
				return toPullRequest(pr), nil
			}
		}
	}

	return tracker.PullRequest{}, tracker.ErrNoPullRequest
}

// CreatePullRequest opens a pull request of head against base.
func (a *forgejoAdapter) CreatePullRequest(ctx context.Context, head, base, title, body string) (tracker.PullRequest, error) {
	path, _, err := a.pullsPath()
	if err != nil {
		return tracker.PullRequest{}, err
	}

	req := struct {
		Head  string `json:"head"`
		Base  string `json:"base"`
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Head: head, Base: base, Title: title, Body: body}

	var created pullRequest
	if err := a.doJSON(ctx, http.MethodPost, path, req, &created); err != nil {
		return tracker.PullRequest{}, fmt.Errorf("forgejo: create pull request for %q: %w", head, err)
	}

	return toPullRequest(&created), nil
}

// MergePullRequest merges the pull request with method via
// POST /repos/{owner}/{repo}/pulls/{index}/merge. A refusal (405) carries the
// API's message.
func (a *forgejoAdapter) MergePullRequest(ctx context.Context, number string, method tracker.MergeMethod) error {
	path, _, err := a.pullsPath()
	if err != nil {
		return err
	}
	if _, err := strconv.Atoi(number); err != nil {
		return fmt.Errorf("forgejo: invalid pull request number %q: %w", number, err)
	}

	req := struct {
		Do string `json:"Do"` //nolint:tagliatelle // Forgejo wire format
	}{Do: string(method)}
	if err := a.doJSON(ctx, http.MethodPost, path+"/"+number+"/merge", req, nil); err != nil {
		return fmt.Errorf("forgejo: merge pull request #%s: %w", number, err)
	}

	return nil
}

func toPullRequest(pr *pullRequest) tracker.PullRequest {
	return tracker.PullRequest{
		Number: strconv.Itoa(pr.Number),
		URL:    pr.HTMLURL,
		Head:   pr.Head.Ref,
		Base:   pr.Base.Ref,
	}.Clean()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./tracker/forgejo/ -v`
Expected: PASS, including the pre-existing tests (the error strings still carry the status).

- [ ] **Step 6: Commit**

```bash
git add tracker/forgejo/pullrequest.go tracker/forgejo/pullrequest_test.go tracker/forgejo/forgejo.go
git commit -m "feat(forgejo): pull requests: find, create, merge"
```

---

### Task 5: The offer in the push proposal

**Files:**
- Modify: `cmd/pushflow/pushflow.go`
- Test: `cmd/pushflow/pushflow_test.go`

**Interfaces:**
- Consumes: the existing `Propose`, `Opts`, `fakePusher`, `yes`, `no`.
- Produces: `pushflow.PullRequestOffer{Open func(ctx context.Context) error}` and `Opts.PullRequest *PullRequestOffer`. `Open` runs after a successful push, never non-interactively; its error is a warning on the Err stream.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/pushflow/pushflow_test.go`:

```go
func TestPropose_PullRequest(t *testing.T) {
	t.Parallel()

	offer := func(calls *int, err error) *PullRequestOffer {
		return &PullRequestOffer{Open: func(context.Context) error { *calls++; return err }}
	}

	t.Run("runs after a confirmed push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		calls := 0
		if err := Propose(t.Context(), f, Opts{Branch: "b", PullRequest: offer(&calls, nil)}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || calls != 1 {
			t.Fatalf("pushed %v, offer calls %d", f.pushed, calls)
		}
	})

	t.Run("not after a declined push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		calls := 0
		if err := Propose(t.Context(), f, Opts{Branch: "b", PullRequest: offer(&calls, nil)}, no); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if calls != 0 {
			t.Fatalf("offer calls %d, want 0", calls)
		}
	})

	t.Run("not when nothing is to push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		f.dryOK = false
		calls := 0
		if err := Propose(t.Context(), f, Opts{Branch: "b", PullRequest: offer(&calls, nil)}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if calls != 0 {
			t.Fatalf("offer calls %d, want 0", calls)
		}
	})

	t.Run("not non-interactively, even with --push", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		calls := 0
		opts := Opts{Branch: "b", AutoConfirm: true, NonInteractive: true, PullRequest: offer(&calls, nil)}
		if err := Propose(t.Context(), f, opts, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if len(f.pushed) != 1 || calls != 0 {
			t.Fatalf("pushed %v, offer calls %d", f.pushed, calls)
		}
	})

	t.Run("a failing offer is a warning, the push stands", func(t *testing.T) {
		t.Parallel()
		f := newFake()
		calls := 0
		if err := Propose(t.Context(), f, Opts{Branch: "b", PullRequest: offer(&calls, errors.New("forge down"))}, yes); err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if !strings.Contains(f.out.String(), "warning: pull request: forge down") {
			t.Fatalf("output %q lacks the warning", f.out.String())
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/pushflow/ -run TestPropose_PullRequest -v`
Expected: compile error, `undefined: PullRequestOffer`.

- [ ] **Step 3: Add the offer**

In `cmd/pushflow/pushflow.go`, add after `Opts`:

```go
// PullRequestOffer, set on Opts, offers a pull request once the branch is
// pushed: Open looks the branch up on the forge, asks, and opens one. It is
// not run non-interactively. Its error is a warning: the push already
// happened.
type PullRequestOffer struct {
	Open func(ctx context.Context) error
}
```

Add to `Opts` (after `Parent string`):

```go
	// PullRequest, when set, is offered after a successful push.
	PullRequest *PullRequestOffer
```

At the end of `Propose`, replace the final `return nil` after the "Pushed" line:

```go
	fmt.Fprintf(c.IO().Out, "Pushed %q to %s.\n", opts.Branch, remote)

	if opts.PullRequest != nil && opts.PullRequest.Open != nil && !opts.NonInteractive {
		if err := opts.PullRequest.Open(ctx); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: pull request: %v\n", err)
		}
	}

	return nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/pushflow/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/pushflow/pushflow.go cmd/pushflow/pushflow_test.go
git commit -m "feat(pushflow): offer a pull request after the push"
```

---

### Task 6: The core: look up, ask, open

**Files:**
- Create: `cmd/branch/pr.go`, `tui/pullrequest.go`
- Test: `cmd/branch/pr_test.go`

**Interfaces:**
- Consumes: Task 1 and 2; `branch.Find`, `issueflow.ResolveParentBranch`, `issue.TrackerNumber`, `git.Client.DefaultBaseBranch`, `tui.Form`, `tui.NewForm`.
- Produces (package `cmd/branch`, imported by `cmd/commit` in Task 8 as `branchcmd`):
  - `type PullRequestDraft struct{ Title, Body, Base string }`
  - `type PullRequestForm func(ctx context.Context, branchName string, draft *PullRequestDraft) (open bool, err error)`
  - `func OpenPullRequest(ctx context.Context, out io.Writer, forge tracker.PullRequests, branchName string, draft PullRequestDraft, form PullRequestForm) error`
  - `func PullRequestDraftFor(ctx context.Context, c *git.Client, cfgBase, branchName string) PullRequestDraft`
  - `func HuhPullRequestForm() PullRequestForm` (the editable form), `func HuhPullRequestConfirm() PullRequestForm` (the yes/no question)
  - `tui.PullRequestForm(branchName string, title, body, base *string, confirmed *bool) *huh.Group`, `tui.PullRequestConfirm(branchName, base string, confirmed *bool) *huh.Group`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/branch/pr_test.go`:

```go
package branch

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// scriptedPullRequestForm answers the form with Open, after an optional edit
// of the draft, and keeps what it was shown.
type scriptedPullRequestForm struct {
	Open  bool
	Edit  func(*PullRequestDraft)
	Err   error
	Calls int
	Seen  PullRequestDraft
	Name  string
}

func (s *scriptedPullRequestForm) form() PullRequestForm {
	return func(_ context.Context, branchName string, draft *PullRequestDraft) (bool, error) {
		s.Calls++
		s.Name, s.Seen = branchName, *draft
		if s.Err != nil {
			return false, s.Err
		}
		if s.Edit != nil {
			s.Edit(draft)
		}

		return s.Open, nil
	}
}

func TestOpenPullRequest(t *testing.T) {
	t.Parallel()

	draft := PullRequestDraft{Title: "Add thing", Body: "Closes #1", Base: "master"}

	t.Run("yes opens the pull request with the draft and prints its URL", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		out := &bytes.Buffer{}
		form := &scriptedPullRequestForm{Open: true}
		if err := OpenPullRequest(t.Context(), out, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		want := fake.PullRequestCreate{Head: "feat-x", Base: "master", Title: "Add thing", Body: "Closes #1"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
		if form.Name != "feat-x" || form.Seen != draft {
			t.Errorf("form saw %q %+v", form.Name, form.Seen)
		}
		if !strings.Contains(out.String(), "fake://pr/1") {
			t.Errorf("output %q lacks the URL", out.String())
		}
	})

	t.Run("the form's edits are what is opened", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		form := &scriptedPullRequestForm{Open: true, Edit: func(d *PullRequestDraft) { d.Title, d.Base = "Better", "develop" }}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if got := ft.RecordedPullRequests[0]; got.Title != "Better" || got.Base != "develop" {
			t.Errorf("opened %+v", got)
		}
	})

	t.Run("no opens nothing", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, ft, "feat-x", draft, (&scriptedPullRequestForm{}).form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if len(ft.RecordedPullRequests) != 0 {
			t.Errorf("opened %+v", ft.RecordedPullRequests)
		}
	})

	t.Run("an open pull request skips the form and prints its URL", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", URL: "fake://pr/7", Head: "feat-x", Base: "master"}}}
		out := &bytes.Buffer{}
		form := &scriptedPullRequestForm{Open: true}
		if err := OpenPullRequest(t.Context(), out, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if form.Calls != 0 || len(ft.RecordedPullRequests) != 0 {
			t.Errorf("form calls %d, opened %+v", form.Calls, ft.RecordedPullRequests)
		}
		if !strings.Contains(out.String(), "#7") || !strings.Contains(out.String(), "fake://pr/7") {
			t.Errorf("output %q", out.String())
		}
	})

	t.Run("a form error is returned", func(t *testing.T) {
		t.Parallel()
		form := &scriptedPullRequestForm{Err: errors.New("boom")}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, &fake.Tracker{}, "feat-x", draft, form.form()); err == nil {
			t.Error("want an error")
		}
	})
}

func TestPullRequestDraftFor(t *testing.T) {
	t.Parallel()

	t.Run("an untracked branch: its name, the configured base, no body", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		got := PullRequestDraftFor(t.Context(), r.client, "master", "feat-x")
		if got != (PullRequestDraft{Title: "feat-x", Base: "master"}) {
			t.Errorf("draft = %+v", got)
		}
	})

	t.Run("a tracked branch: the issue title", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "ABC-1@feat@add-thing")
		branchtest.Seed(t, r.client, branch.Op{Branch: "ABC-1@feat@add-thing", Title: "Add thing"}, branch.StatusInProgress)
		got := PullRequestDraftFor(t.Context(), r.client, "master", "ABC-1@feat@add-thing")
		if got.Title != "Add thing" || got.Base != "master" || got.Body != "" {
			t.Errorf("draft = %+v", got)
		}
	})

	t.Run("without a configured base, the default base branch", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		if got := PullRequestDraftFor(t.Context(), r.client, "", "feat-x"); got.Base != "master" {
			t.Errorf("draft = %+v", got)
		}
	})
}
```

The "Closes #N" body needs a mirrored issue; it is pinned in Task 7's E2E, which has the mirror rig.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/branch/ -run "TestOpenPullRequest|TestPullRequestDraftFor" -v`
Expected: compile error, `undefined: PullRequestDraft`.

- [ ] **Step 3: Write the forms**

Create `tui/pullrequest.go`:

```go
package tui

import (
	"fmt"

	"github.com/charmbracelet/huh"
)

// PullRequestForm edits a pull request of branchName before it is opened:
// title, body, base, then a confirm. The caller seeds the values.
func PullRequestForm(branchName string, title, body, base *string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().Title("Title:").Value(title),
		huh.NewText().Title("Body:").Value(body),
		huh.NewInput().Title("Base branch:").Value(base),
		huh.NewConfirm().Title(fmt.Sprintf("Open a pull request for %q?", branchName)).
			Affirmative("Open").Negative("Skip").Value(confirmed),
	)
}

// PullRequestConfirm is the one-question offer of the push step.
func PullRequestConfirm(branchName, base string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().Title(fmt.Sprintf("Open a pull request for %q against %q?", branchName, base)).
			Affirmative("Open").Negative("Skip").Value(confirmed),
	)
}
```

- [ ] **Step 4: Write the core**

Create `cmd/branch/pr.go`:

```go
package branch

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)

// PullRequestDraft is what a pull request is opened with.
type PullRequestDraft struct {
	Title, Body, Base string
}

// PullRequestForm shows the draft of branchName's pull request and reports
// whether to open it. branch pr lets the operator edit the draft; the push
// step only asks. Esc is a no.
type PullRequestForm func(ctx context.Context, branchName string, draft *PullRequestDraft) (open bool, err error)

// OpenPullRequest prints the URL of branchName's open pull request, or runs
// form and opens one from the draft. It is the step behind branch pr and
// behind the push proposal of commit.
func OpenPullRequest(
	ctx context.Context, out io.Writer, forge tracker.PullRequests,
	branchName string, draft PullRequestDraft, form PullRequestForm,
) error {
	pr, err := forge.FindPullRequest(ctx, branchName)
	switch {
	case err == nil:
		fmt.Fprintf(out, "Pull request #%s is open for %q: %s\n", pr.Number, branchName, pr.URL)

		return nil
	case !errors.Is(err, tracker.ErrNoPullRequest):
		return fmt.Errorf("look up the pull request of %q: %w", branchName, err)
	}

	open, err := form(ctx, branchName, &draft)
	if err != nil {
		return err
	}
	if !open {
		return nil
	}

	pr, err = forge.CreatePullRequest(ctx, branchName, draft.Base, draft.Title, draft.Body)
	if err != nil {
		return fmt.Errorf("open a pull request for %q: %w", branchName, err)
	}
	fmt.Fprintf(out, "Opened pull request #%s for %q against %q: %s\n", pr.Number, branchName, pr.Base, pr.URL)

	return nil
}

// PullRequestDraftFor prefills from what the branch chain records: the issue
// title (else the branch name), the parent branch (else cfgBase, else the
// default base branch) and "Closes #N" when the issue has a tracker number.
func PullRequestDraftFor(ctx context.Context, c *git.Client, cfgBase, branchName string) PullRequestDraft {
	d := PullRequestDraft{Title: branchName, Base: cfgBase}
	if d.Base == "" {
		if b, err := c.DefaultBaseBranch(); err == nil {
			d.Base = b
		}
	}

	st, e, err := branch.Find(ctx, c, branchName)
	if err != nil || e == nil {
		return d
	}
	if st.Title != "" {
		d.Title = st.Title
	}
	if parent, err := issueflow.ResolveParentBranch(ctx, c, st.Slug, cfgBase); err == nil && parent != "" {
		d.Base = parent
	}
	if n := issue.TrackerNumber(ctx, c, st.IssueID); n != "" {
		d.Body = "Closes #" + n
	}

	return d
}

// HuhPullRequestForm is the production form of branch pr: the three fields
// and a confirm. Esc declines.
func HuhPullRequestForm() PullRequestForm {
	return func(ctx context.Context, branchName string, d *PullRequestDraft) (bool, error) {
		confirmed := true
		err := tui.Form(tui.PullRequestForm(branchName, &d.Title, &d.Body, &d.Base, &confirmed)).RunWithContext(ctx)
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("pull request form: %w", err)
		}

		return confirmed, nil
	}
}

// HuhPullRequestConfirm is the push step's offer: one question naming the
// branch and the base, no edit. Esc declines.
func HuhPullRequestConfirm() PullRequestForm {
	return func(ctx context.Context, branchName string, d *PullRequestDraft) (bool, error) {
		confirmed := true
		err := tui.Form(tui.PullRequestConfirm(branchName, d.Base, &confirmed)).RunWithContext(ctx)
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("pull request confirm: %w", err)
		}

		return confirmed, nil
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/branch/ -run "TestOpenPullRequest|TestPullRequestDraftFor" -v && mise exec -- go build ./...`
Expected: PASS, build OK.

- [ ] **Step 6: Commit**

```bash
git add cmd/branch/pr.go cmd/branch/pr_test.go tui/pullrequest.go
git commit -m "feat(branch): the open-pull-request step and its forms"
```

---

### Task 7: `git zf branch pr [branch]`

**Files:**
- Modify: `cmd/branch/pr.go` (the command), `cmd/branch/branch.go` (registration and menu)
- Test: `cmd/branch/pr_e2e_test.go`, `cmd/branch/branch_test.go`

**Interfaces:**
- Consumes: Task 6; `pushflow.AddFlags`, `ReadFlags`, `ResolveFlags`, `Propose`, `NewHuhConfirm`; `git.Client.PushDryRun`, `CurrentBranch`, `BranchExists`, `Remote`; `cmdutil.NewClientForCmd`, `cmdutil.StringFlag`; `tracker.New`, `tracker.PullRequestsOf`; the `mergeRig` of `merge_e2e_test.go` (`newMergeRig`, `addOrigin`, `addLocalBranch`, `git`, `gitOK`).
- Produces: `prDeps{client, cfg, forge, trackerType, push, noPush, yes, pushConfirm, form}`, `runPr(ctx, d prDeps, branchName string) error`, `Branch.prCmd()` in the branch menu.

- [ ] **Step 1: Write the failing tests**

Create `cmd/branch/pr_e2e_test.go`:

```go
package branch

import (
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// prRig is a merge rig with an origin, a pushed branch "feat-x" and a fake
// forge.
func newPrRig(t *testing.T) (*mergeRig, *fake.Tracker) {
	t.Helper()

	r := newMergeRig(t)
	r.addOrigin(t)
	r.addLocalBranch(t, "feat-x")
	r.git(t, "push", "-q", "origin", "feat-x")

	return r, &fake.Tracker{}
}

func (r *mergeRig) prDeps(ft *fake.Tracker, form *scriptedPullRequestForm) prDeps {
	var forge tracker.PullRequests
	if ft != nil {
		forge = ft
	}

	return prDeps{
		client: r.client, cfg: r.cfg, forge: forge, trackerType: "fake",
		pushConfirm: acceptPush, form: form.form(),
	}
}

func TestRunPr_OpensWithThePrefill(t *testing.T) {
	r, ft := newPrRig(t)
	form := &scriptedPullRequestForm{Open: true}

	err := runPr(t.Context(), r.prDeps(ft, form), "feat-x")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runPr: %v", err)
		}
	})
	t.Run("the pull request is opened from the branch name against the base", func(t *testing.T) {
		want := fake.PullRequestCreate{Head: "feat-x", Base: "master", Title: "feat-x"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
	})
	t.Run("the URL is printed", func(t *testing.T) {
		if !strings.Contains(r.stdout.String(), "fake://pr/1") {
			t.Errorf("stdout %q", r.stdout.String())
		}
	})
}

func TestRunPr_CurrentBranchByDefault(t *testing.T) {
	r, ft := newPrRig(t)
	r.git(t, "switch", "feat-x")
	r.rebuildClient(t)

	err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "")

	t.Run("the checked-out branch is used", func(t *testing.T) {
		if err != nil || len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0].Head != "feat-x" {
			t.Errorf("err %v, opened %+v", err, ft.RecordedPullRequests)
		}
	})
}

func TestRunPr_ExistingPullRequestPrintsItsURL(t *testing.T) {
	r, ft := newPrRig(t)
	ft.PullRequests = []tracker.PullRequest{{Number: "7", URL: "fake://pr/7", Head: "feat-x", Base: "master"}}
	form := &scriptedPullRequestForm{Open: true}

	err := runPr(t.Context(), r.prDeps(ft, form), "feat-x")

	t.Run("no form, nothing opened, the URL printed", func(t *testing.T) {
		if err != nil || form.Calls != 0 || len(ft.RecordedPullRequests) != 0 || !strings.Contains(r.stdout.String(), "fake://pr/7") {
			t.Errorf("err %v, calls %d, opened %+v, stdout %q", err, form.Calls, ft.RecordedPullRequests, r.stdout.String())
		}
	})
}

func TestRunPr_PushesFirst(t *testing.T) {
	r, ft := newPrRig(t)
	r.addLocalBranch(t, "feat-y") // not pushed

	err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "feat-y")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runPr: %v", err)
		}
	})
	t.Run("the branch is on origin", func(t *testing.T) {
		if !r.gitOK(t, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/feat-y") {
			t.Error("feat-y was not pushed")
		}
	})
	t.Run("the pull request is opened", func(t *testing.T) {
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0].Head != "feat-y" {
			t.Errorf("opened %+v", ft.RecordedPullRequests)
		}
	})
}

func TestRunPr_DeclinedPushAborts(t *testing.T) {
	r, ft := newPrRig(t)
	r.addLocalBranch(t, "feat-y")
	d := r.prDeps(ft, &scriptedPullRequestForm{Open: true})
	d.pushConfirm = declinePush

	err := runPr(t.Context(), d, "feat-y")

	t.Run("aborted, nothing opened", func(t *testing.T) {
		if err != nil || len(ft.RecordedPullRequests) != 0 || !strings.Contains(r.stdout.String(), "Aborted.") {
			t.Errorf("err %v, opened %+v, stdout %q", err, ft.RecordedPullRequests, r.stdout.String())
		}
	})
}

func TestRunPr_Refusals(t *testing.T) {
	t.Run("a tracker without pull requests", func(t *testing.T) {
		r, _ := newPrRig(t)
		err := runPr(t.Context(), r.prDeps(nil, &scriptedPullRequestForm{Open: true}), "feat-x")
		if err == nil || !strings.Contains(err.Error(), "no pull requests") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no tracker at all", func(t *testing.T) {
		r, _ := newPrRig(t)
		d := r.prDeps(nil, &scriptedPullRequestForm{Open: true})
		d.trackerType = ""
		err := runPr(t.Context(), d, "feat-x")
		if err == nil || !strings.Contains(err.Error(), "no issue tracker") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("--yes: a pull request needs a title", func(t *testing.T) {
		r, ft := newPrRig(t)
		d := r.prDeps(ft, &scriptedPullRequestForm{Open: true})
		d.yes = true
		err := runPr(t.Context(), d, "feat-x")
		if err == nil || !strings.Contains(err.Error(), "--yes") || len(ft.RecordedPullRequests) != 0 {
			t.Errorf("err = %v, opened %+v", err, ft.RecordedPullRequests)
		}
	})
	t.Run("the base branch itself", func(t *testing.T) {
		r, ft := newPrRig(t)
		err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "master")
		if err == nil || !strings.Contains(err.Error(), "base branch") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch that does not exist", func(t *testing.T) {
		r, ft := newPrRig(t)
		if err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "nope"); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("no remote", func(t *testing.T) {
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		err := runPr(t.Context(), r.prDeps(&fake.Tracker{}, &scriptedPullRequestForm{Open: true}), "feat-x")
		if err == nil || !strings.Contains(err.Error(), "remote") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRunPr_MirroredIssueClosesItsNumber(t *testing.T) {
	r, ft := newPrRig(t)
	ctx := t.Context()
	r.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}},
	}
	ft.ProjectIssues = []tracker.Issue{{
		TrackerType: "fake", ID: "42", Subject: "Add thing", Status: "open",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}
	if _, err := issueflow.MirrorOf(r.cfg, ft).Reconcile(ctx, r.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	records, _, err := issuepkg.List(ctx, r.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}
	r.addLocalBranch(t, "42@feat@add-thing")
	r.git(t, "push", "-q", "origin", "42@feat@add-thing")
	branchtest.Seed(t, r.client,
		branch.Op{Branch: "42@feat@add-thing", Title: "Add thing", TrackerType: "fake", IssueID: records[0].ID},
		branch.StatusInProgress)

	runErr := runPr(ctx, r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "42@feat@add-thing")

	t.Run("the title is the issue's and the body closes the tracker number", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runPr: %v", runErr)
		}
		want := fake.PullRequestCreate{Head: "42@feat@add-thing", Base: "master", Title: "Add thing", Body: "Closes #42"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
	})
}
```

Add to `cmd/branch/branch_test.go`:

```go
func TestPrCmd(t *testing.T) {
	root := New(&config.AppConfig{}).GetRootCmd()

	t.Run("branch pr is registered", func(t *testing.T) {
		if _, _, err := root.Find([]string{"pr"}); err != nil {
			t.Fatalf("find pr: %v", err)
		}
	})
	t.Run("pr's RunE on the root command tolerates the undefined flags", func(t *testing.T) {
		t.Chdir(t.TempDir())
		sub, _, _ := root.Find([]string{"pr"})
		err := sub.RunE(root, nil) // what the branch menu does
		if err == nil || strings.Contains(err.Error(), "flag accessed but not defined") {
			t.Fatalf("err = %v", err)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/branch/ -run "^TestRunPr_|^TestPrCmd" -v`
Expected: compile error, `undefined: prDeps`.

- [ ] **Step 3: Write the command**

Append to `cmd/branch/pr.go` (add imports `"github.com/piprim/git-zf/cmd/cmdutil"`, `"github.com/piprim/git-zf/cmd/pushflow"`, `"github.com/piprim/git-zf/config"`, `"github.com/spf13/cobra"`):

```go
// prDeps bundles what runPr needs; the E2E rig builds it directly.
type prDeps struct {
	client      *git.Client
	cfg         *config.AppConfig
	forge       tracker.PullRequests // nil ⇒ the tracker has no pull requests
	trackerType string               // "" ⇒ no tracker configured
	push, noPush, yes bool
	pushConfirm pushflow.ConfirmFunc
	form        PullRequestForm
}

func (b Branch) prCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr [branch]",
		Short: "Open a pull request for a branch on the forge",
		Long: `Open a pull request for the branch (the current one by default) on the
issue tracker's forge, GitHub or Forgejo. The branch is pushed first when it
is not on the remote. A branch that already has an open pull request gets its
URL printed.

The form is prefilled from the branch's issue: its title, its parent branch as
the base, and "Closes #N" when the issue is mirrored with the tracker.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
			if err != nil {
				return err //nolint:wrapcheck // already names the cause
			}

			d := prDeps{
				client: c, cfg: b.appConfig, trackerType: b.appConfig.IssueTracker.Type,
				pushConfirm: pushflow.NewHuhConfirm(), form: HuhPullRequestForm(),
			}
			// Tolerant readers: the branch menu runs this with the root command.
			d.push, d.noPush = pushflow.ReadFlags(cmd)
			d.yes = cmdutil.StringFlag(cmd, "yes") == "true"
			if d.trackerType != "" {
				t, err := tracker.New(b.appConfig.IssueTracker)
				if err != nil {
					return fmt.Errorf("init tracker: %w", err)
				}
				d.forge = tracker.PullRequestsOf(t)
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			return runPr(cmd.Context(), d, name)
		},
	}
	cmd.Flags().BoolP("yes", "y", false, "non-interactive: refused, a pull request needs a title")
	pushflow.AddFlags(cmd)

	return cmd
}

// runPr opens a pull request for branchName (the current branch when "").
func runPr(ctx context.Context, d prDeps, branchName string) error {
	switch {
	case d.trackerType == "":
		return errors.New("no issue tracker is configured: a pull request needs a forge (github or forgejo)")
	case d.forge == nil:
		return fmt.Errorf("the issue tracker %q has no pull requests", d.trackerType)
	case d.yes:
		return errors.New("a pull request needs a title: run without --yes")
	}

	if branchName == "" {
		cur, err := d.client.CurrentBranch()
		if err != nil || cur == "" || cur == "HEAD" {
			return errors.New("checkout a branch, or name one: git zf branch pr <branch>")
		}
		branchName = cur
	}
	if ok, err := d.client.BranchExists(branchName); err != nil || !ok {
		return fmt.Errorf("branch %q does not exist in this clone", branchName)
	}

	draft := PullRequestDraftFor(ctx, d.client, d.cfg.Branch.Base, branchName)
	if branchName == draft.Base {
		return fmt.Errorf("%q is the base branch: a pull request needs a feature branch", branchName)
	}

	remote, err := d.client.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return errors.New("this clone has no remote: a pull request needs the branch on the forge")
	}

	// The pull request needs the branch on the forge.
	if _, toPush, err := d.client.PushDryRun(ctx, branchName); err != nil {
		return fmt.Errorf("check %q against %s: %w", branchName, remote, err)
	} else if toPush {
		skip, auto, err := pushflow.ResolveFlags(d.push, d.noPush, true)
		if err != nil {
			return err //nolint:wrapcheck // pushflow already wraps
		}
		if skip {
			return fmt.Errorf("branch %q is not on %s yet: push it first", branchName, remote)
		}
		if err := pushflow.Propose(ctx, d.client, pushflow.Opts{Branch: branchName, AutoConfirm: auto}, d.pushConfirm); err != nil {
			return err //nolint:wrapcheck // pushflow already wraps
		}
		if _, still, _ := d.client.PushDryRun(ctx, branchName); still {
			fmt.Fprintln(d.client.IO().Out, "Aborted.")

			return nil
		}
	}

	return OpenPullRequest(ctx, d.client.IO().Out, d.forge, branchName, draft, d.form)
}
```

In `cmd/branch/branch.go`, register it in the menu order:

```go
	subs := []*cobra.Command{b.listCmd(), b.newCmd(), b.pruneCmd(), b.pruneTrackerCmd(), b.mergeCmd(), b.prCmd()}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/branch/ -run "^TestRunPr_|^TestPrCmd|^TestOpenPullRequest|^TestPullRequestDraftFor" -v`
Expected: PASS.

- [ ] **Step 5: Run the whole package**

Run: `mise exec -- go test ./cmd/branch/...`
Expected: PASS (the menu and the existing E2E suites are untouched).

- [ ] **Step 6: Commit**

```bash
git add cmd/branch/pr.go cmd/branch/pr_e2e_test.go cmd/branch/branch.go cmd/branch/branch_test.go
git commit -m "feat(branch): git zf branch pr opens a pull request on the forge"
```

---

### Task 8: The offer in `git zf commit`

**Files:**
- Create: `cmd/commit/pullrequest.go`
- Modify: `cmd/commit/commit.go` (`proposeCommitPush`)
- Test: `cmd/commit/pullrequest_test.go`

**Interfaces:**
- Consumes: Task 5 (`pushflow.PullRequestOffer`), Task 6 (`branchcmd.OpenPullRequest`, `PullRequestDraftFor`, `HuhPullRequestConfirm`, `PullRequestForm`), Task 1 and 2.
- Produces: `forgeOf(cfg *config.AppConfig, errOut io.Writer) tracker.PullRequests`, `pullRequestOffer(client *git.Client, cfg *config.AppConfig, forge tracker.PullRequests, branchName string, form branchcmd.PullRequestForm) *pushflow.PullRequestOffer`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/commit/pullrequest_test.go`:

```go
package commit

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	branchcmd "github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tracker/fake"
)

// newOfferRepo is a repo with master and a branch "feat" one commit ahead.
func newOfferRepo(t *testing.T) *git.Client {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("switch", "-c", "feat")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: x")

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func yesForm(context.Context, string, *branchcmd.PullRequestDraft) (bool, error) { return true, nil }

func TestPullRequestOffer(t *testing.T) {
	cfg := &config.AppConfig{}
	cfg.Branch.Base = "master"

	t.Run("no forge: no offer", func(t *testing.T) {
		if pullRequestOffer(newOfferRepo(t), cfg, nil, "feat", yesForm) != nil {
			t.Error("want nil")
		}
	})
	t.Run("the base branch: no offer", func(t *testing.T) {
		if pullRequestOffer(newOfferRepo(t), cfg, &fake.Tracker{}, "master", yesForm) != nil {
			t.Error("want nil")
		}
	})
	t.Run("a feature branch: yes opens the pull request from the prefill", func(t *testing.T) {
		ft := &fake.Tracker{}
		offer := pullRequestOffer(newOfferRepo(t), cfg, ft, "feat", yesForm)
		if offer == nil {
			t.Fatal("want an offer")
		}
		if err := offer.Open(t.Context()); err != nil {
			t.Fatalf("Open: %v", err)
		}
		want := fake.PullRequestCreate{Head: "feat", Base: "master", Title: "feat"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
	})
}

func TestForgeOf(t *testing.T) {
	t.Run("no tracker type: nil", func(t *testing.T) {
		if forgeOf(&config.AppConfig{}, &bytes.Buffer{}) != nil {
			t.Error("want nil")
		}
	})
	t.Run("the fake tracker is a forge", func(t *testing.T) {
		cfg := &config.AppConfig{}
		cfg.IssueTracker.Type = "fake"
		if forgeOf(cfg, &bytes.Buffer{}) == nil {
			t.Error("want a forge")
		}
	})
	t.Run("an unknown tracker type is a warning, nil", func(t *testing.T) {
		cfg := &config.AppConfig{}
		cfg.IssueTracker.Type = "nope"
		errOut := &bytes.Buffer{}
		if forgeOf(cfg, errOut) != nil || errOut.Len() == 0 {
			t.Errorf("forge %v, warning %q", forgeOf(cfg, errOut), errOut.String())
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/commit/ -run "TestPullRequestOffer|TestForgeOf" -v`
Expected: compile error, `undefined: pullRequestOffer`.

- [ ] **Step 3: Write the wiring**

Create `cmd/commit/pullrequest.go`:

```go
package commit

import (
	"context"
	"fmt"
	"io"

	branchcmd "github.com/piprim/git-zf/cmd/branch"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// forgeOf builds the tracker and returns its pull request capability: nil
// without a tracker, without the capability, or when the tracker cannot be
// built (a warning: the commit and the push do not depend on it).
func forgeOf(cfg *config.AppConfig, errOut io.Writer) tracker.PullRequests {
	if cfg.IssueTracker.Type == "" {
		return nil
	}
	t, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		fmt.Fprintf(errOut, "warning: init tracker: %v\n", err)

		return nil
	}

	return tracker.PullRequestsOf(t)
}

// pullRequestOffer is the push step's offer for branchName: nil without a
// forge, or when the branch is the base itself. Open prefills from the
// branch chain and asks form, one yes/no question in production.
func pullRequestOffer(
	client *git.Client, cfg *config.AppConfig, forge tracker.PullRequests,
	branchName string, form branchcmd.PullRequestForm,
) *pushflow.PullRequestOffer {
	if forge == nil {
		return nil
	}
	base := cfg.Branch.Base
	if base == "" {
		base, _ = client.DefaultBaseBranch()
	}
	if branchName == base {
		return nil
	}

	return &pushflow.PullRequestOffer{Open: func(ctx context.Context) error {
		draft := branchcmd.PullRequestDraftFor(ctx, client, cfg.Branch.Base, branchName)

		return branchcmd.OpenPullRequest(ctx, client.IO().Out, forge, branchName, draft, form)
	}}
}
```

In `cmd/commit/commit.go`, `proposeCommitPush`, pass the offer:

```go
	return pushflow.Propose(cmd.Context(), client, pushflow.Opts{
		Branch:              branchName,
		Skip:                skip,
		AutoConfirm:         auto,
		NonInteractive:      yes,
		IncludeMergePreview: includeMerge,
		Parent:              parent,
		PullRequest: pullRequestOffer(client, cfg, forgeOf(cfg, client.IO().Err), branchName,
			branchcmd.HuhPullRequestConfirm()),
	}, pushflow.NewHuhConfirm())
```

Add the import `branchcmd "github.com/piprim/git-zf/cmd/branch"` to `commit.go`. The test file imports `tracker/fake` so the "fake" type is registered there.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/commit/... -v -run "TestPullRequestOffer|TestForgeOf" && mise exec -- go build ./... && mise exec -- go vet ./cmd/...`
Expected: PASS, build and vet OK (no import cycle: `cmd/branch` does not import `cmd/commit`).

- [ ] **Step 5: Commit**

```bash
git add cmd/commit/pullrequest.go cmd/commit/pullrequest_test.go cmd/commit/commit.go
git commit -m "feat(commit): offer a pull request after the push"
```

---

### Task 9: The forge path of the merge engine

**Files:**
- Modify: `cmd/mergeflow/mergeflow.go`
- Create: `cmd/mergeflow/forge.go`
- Test: `cmd/mergeflow/forge_test.go`

**Interfaces:**
- Consumes: Task 1 and 2; `git.Client.Remote`, `Fetch`, `FastForwardOnly`; the `engineRig` helpers of `mergeflow_test.go` (`initRepo`, `newEngineRig`, `gitRun`, `headSubject`, `statusPorcelain`, `scriptedMergePrompter`, `plainPrefill`).
- Produces: `mergeflow.ForgeMerge{Forge tracker.PullRequests; Number string}`, `Params.PullRequest *ForgeMerge`, `Result.ViaForge bool`, `methodOf(commit.MergeStrategy) tracker.MergeMethod`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/mergeflow/forge_test.go`:

```go
package mergeflow

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newForgeRig is newEngineRig with a bare origin holding master and feature,
// and the client rebuilt so it sees the remote. forgeMerged plays the forge:
// it moves origin/master to feature's tip, as a merge there would.
func newForgeRig(t *testing.T) (*engineRig, *bytes.Buffer) {
	t.Helper()

	rig := newEngineRig(t)
	origin := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "-b", "master", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	gitRun(t, rig.dir, "remote", "add", "origin", origin)
	gitRun(t, rig.dir, "push", "-q", "origin", "master", "feature")

	stderr := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: rig.stdout, Err: stderr}, rig.dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}
	rig.client = client

	return rig, stderr
}

func forgeMerged(t *testing.T, rig *engineRig) {
	t.Helper()

	gitRun(t, rig.dir, "push", "-q", "origin", "feature:master")
}

func TestRun_Forge(t *testing.T) {
	for _, tc := range []struct {
		strategy commit.MergeStrategy
		method   tracker.MergeMethod
	}{
		{commit.MergeStrategyClassic, tracker.MergeMethodMerge},
		{commit.MergeStrategySquash, tracker.MergeMethodSquash},
		{commit.MergeStrategyRebase, tracker.MergeMethodRebase},
	} {
		t.Run(string(tc.strategy)+" merges on the forge and fast-forwards master", func(t *testing.T) {
			rig, _ := newForgeRig(t)
			forgeMerged(t, rig)
			ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
			p := &scriptedMergePrompter{Strategy: tc.strategy, Confirm: true}

			res, err := Run(t.Context(), rig.client, Params{
				Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
			}, p, plainPrefill)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !res.ViaForge || res.Strategy != tc.strategy {
				t.Errorf("result = %+v", res)
			}
			if len(ft.RecordedMerges) != 1 || ft.RecordedMerges[0] != (fake.Merge{Number: "7", Method: tc.method}) {
				t.Errorf("RecordedMerges = %+v", ft.RecordedMerges)
			}
			if got := rig.headSubject(t, "master"); got != "feat: feature work" {
				t.Errorf("master head = %q, want the forge's merge", got)
			}
			if rig.statusPorcelain(t) != "" {
				t.Errorf("dirty tree: %q", rig.statusPorcelain(t))
			}
		})
	}

	t.Run("the forge's refusal is the error and nothing changes locally", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		ft := &fake.Tracker{
			PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}},
			MergeError:   errors.New("Pull Request is not mergeable"),
		}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: true}

		_, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Fatalf("err = %v", err)
		}
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Errorf("master head = %q, want untouched", got)
		}
	})

	t.Run("a declined confirm merges nothing on the forge", func(t *testing.T) {
		rig, _ := newForgeRig(t)
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategySquash, Confirm: false}

		res, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err != nil || !res.Aborted || len(ft.RecordedMerges) != 0 {
			t.Errorf("err %v, res %+v, merges %+v", err, res, ft.RecordedMerges)
		}
	})

	t.Run("a local master with its own commits is not fast-forwarded: a warning, the merge stands", func(t *testing.T) {
		rig, stderr := newForgeRig(t)
		forgeMerged(t, rig)
		writeFile(t, rig.dir, "local.txt", "local\n")
		gitRun(t, rig.dir, "add", "local.txt")
		gitRun(t, rig.dir, "commit", "-m", "chore: local only")
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feature", Base: "master"}}}
		p := &scriptedMergePrompter{Strategy: commit.MergeStrategyClassic, Confirm: true}

		res, err := Run(t.Context(), rig.client, Params{
			Source: "feature", Target: "master", PullRequest: &ForgeMerge{Forge: ft, Number: "7"},
		}, p, plainPrefill)
		if err != nil || !res.ViaForge {
			t.Fatalf("err %v, res %+v", err, res)
		}
		if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), "fast-forward") {
			t.Errorf("stderr %q lacks the fast-forward warning", stderr.String())
		}
		if got := rig.headSubject(t, "master"); got != "chore: local only" {
			t.Errorf("master head = %q, want the local commit kept", got)
		}
	})
}

func TestMethodOf(t *testing.T) {
	t.Parallel()

	t.Run("classic is merge, squash is squash, rebase is rebase", func(t *testing.T) {
		t.Parallel()
		if methodOf(commit.MergeStrategyClassic) != tracker.MergeMethodMerge ||
			methodOf(commit.MergeStrategySquash) != tracker.MergeMethodSquash ||
			methodOf(commit.MergeStrategyRebase) != tracker.MergeMethodRebase {
			t.Error("wrong mapping")
		}
	})
}
```

Note: the dry-run conflict check runs before the forge call. In the "local master" case, `local.txt` does not conflict with `base.txt`, so it passes.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/mergeflow/ -run "TestRun_Forge|TestMethodOf" -v`
Expected: compile error, `undefined: ForgeMerge`.

- [ ] **Step 3: Write the forge path**

Create `cmd/mergeflow/forge.go`:

```go
package mergeflow

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/tracker"
)

// ForgeMerge asks the forge to merge Source's open pull request instead of
// merging locally: the forge composes the commit, Target is then
// fast-forwarded onto the remote's.
type ForgeMerge struct {
	Forge  tracker.PullRequests
	Number string
}

// methodOf maps the picked strategy to the forge's merge method.
func methodOf(s commit.MergeStrategy) tracker.MergeMethod {
	switch s {
	case commit.MergeStrategySquash:
		return tracker.MergeMethodSquash
	case commit.MergeStrategyRebase:
		return tracker.MergeMethodRebase
	default:
		return tracker.MergeMethodMerge
	}
}

// forge merges on the forge, then fetches and fast-forwards the local target.
// A refusal of the forge is the error: nothing changed locally. A target that
// cannot be fast-forwarded (local commits of its own) is a warning: the forge
// merge is done.
func (r *run) forge(ctx context.Context, f *ForgeMerge, s commit.MergeStrategy) error {
	if err := f.Forge.MergePullRequest(ctx, f.Number, methodOf(s)); err != nil {
		return fmt.Errorf("merge pull request #%s on the forge: %w", f.Number, err)
	}
	fmt.Fprintf(r.client.IO().Out, "Pull request #%s merged on the forge (%s).\n", f.Number, s)

	remote, err := r.client.Remote()
	if err != nil || remote == "" {
		return nil
	}
	if err := r.client.Fetch(ctx); err != nil {
		fmt.Fprintf(r.client.IO().Err, "warning: fetch after the merge: %v\n", err)

		return nil
	}
	if err := r.client.FastForwardOnly(ctx, remote+"/"+r.target, r.target); err != nil {
		fmt.Fprintf(r.client.IO().Err,
			"warning: %q was not fast-forwarded to %s/%s (merge it yourself): %v\n", r.target, remote, r.target, err)
	}

	return nil
}
```

In `cmd/mergeflow/mergeflow.go`, add to `Params` (after `SourceClient`):

```go
	// PullRequest, when set, merges Source's pull request on the forge
	// instead of locally (see ForgeMerge). The dry-run, the strategy picker
	// and the confirm run as for a local merge; no commit message is composed.
	PullRequest *ForgeMerge
```

Add to `Result`:

```go
	ViaForge bool // the forge merged the pull request; Target was fast-forwarded
```

In `Run`, right after the `if !confirmed { ... }` block and before the `switch strategy`:

```go
	if p.PullRequest != nil {
		if err := r.forge(ctx, p.PullRequest, strategy); err != nil {
			return Result{Strategy: strategy}, err
		}

		return Result{Strategy: strategy, ViaForge: true}, nil
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/mergeflow/... -v`
Expected: PASS, the existing strategy tests included.

- [ ] **Step 5: Commit**

```bash
git add cmd/mergeflow/forge.go cmd/mergeflow/forge_test.go cmd/mergeflow/mergeflow.go
git commit -m "feat(mergeflow): merge a pull request on the forge"
```

---

### Task 10: `issue close` through the forge

**Files:**
- Create: `cmd/issueflow/forge.go`
- Modify: `cmd/issue/close.go` (`runClose`)
- Test: `cmd/issueflow/forge_test.go`, `cmd/issue/close_pr_e2e_test.go`

**Interfaces:**
- Consumes: Task 9; Task 1 and 2; `git.Client.Remote`, `Fetch`, `ResolveRef`, `IsAncestor`; the close rig (`newCloseRig`, `addOrigin`, `pickedBranchRow`, `mustRunGitAt`, `scriptedPrompter`, `rig.deps()`).
- Produces: `issueflow.ForgePullRequest(ctx, t tracker.Tracker, errOut io.Writer, branchName string) (tracker.PullRequests, *tracker.PullRequest)` and `issueflow.CheckPushed(ctx, c *git.Client, branchName string) error`.

- [ ] **Step 1: Write the failing tests for the helpers**

Create `cmd/issueflow/forge_test.go`:

```go
package issueflow

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
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newPushedRepo is a repo with an origin, master and "feat" pushed.
func newPushedRepo(t *testing.T) (*git.Client, string) {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "master")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("switch", "-c", "feat")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: x")
	run("switch", "master")
	origin := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "-b", "master", origin).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	run("remote", "add", "origin", origin)
	run("push", "-q", "origin", "master", "feat")

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func gitAt(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCheckPushed(t *testing.T) {
	t.Run("a branch at the remote's tip passes", func(t *testing.T) {
		c, _ := newPushedRepo(t)
		if err := CheckPushed(t.Context(), c, "feat"); err != nil {
			t.Errorf("CheckPushed: %v", err)
		}
	})
	t.Run("unpushed commits are refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "switch", "feat")
		gitAt(t, dir, "commit", "--allow-empty", "-m", "feat: more")
		err := CheckPushed(t.Context(), c, "feat")
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch behind the remote is refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "switch", "feat")
		gitAt(t, dir, "commit", "--allow-empty", "-m", "feat: more")
		gitAt(t, dir, "push", "-q", "origin", "feat")
		gitAt(t, dir, "reset", "-q", "--hard", "HEAD~1")
		err := CheckPushed(t.Context(), c, "feat")
		if err == nil || !strings.Contains(err.Error(), "behind") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch not on the remote is refused", func(t *testing.T) {
		c, dir := newPushedRepo(t)
		gitAt(t, dir, "branch", "other", "master")
		err := CheckPushed(t.Context(), c, "other")
		if err == nil || !strings.Contains(err.Error(), "push it first") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestForgePullRequest(t *testing.T) {
	t.Run("no capability: nil, no warning", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		forge, pr := ForgePullRequest(t.Context(), struct{ tracker.Tracker }{&fake.Tracker{}}, errOut, "feat")
		if forge != nil || pr != nil || errOut.Len() != 0 {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
	t.Run("no pull request: the forge, no pull request, no warning", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		forge, pr := ForgePullRequest(t.Context(), &fake.Tracker{}, errOut, "feat")
		if forge == nil || pr != nil || errOut.Len() != 0 {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
	t.Run("a pull request is returned", func(t *testing.T) {
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", Head: "feat", Base: "master"}}}
		forge, pr := ForgePullRequest(t.Context(), ft, &bytes.Buffer{}, "feat")
		if forge == nil || pr == nil || pr.Number != "7" {
			t.Errorf("forge %v, pr %+v", forge, pr)
		}
	})
	t.Run("a forge that cannot be asked is a warning, nil", func(t *testing.T) {
		errOut := &bytes.Buffer{}
		down := &downForge{Tracker: &fake.Tracker{}}
		forge, pr := ForgePullRequest(t.Context(), down, errOut, "feat")
		if forge != nil || pr != nil || !strings.Contains(errOut.String(), "warning") {
			t.Errorf("forge %v, pr %v, warning %q", forge, pr, errOut.String())
		}
	})
}

// downForge has the capability but every call fails.
type downForge struct{ tracker.Tracker }

func (downForge) FindPullRequest(context.Context, string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, errors.New("forge down")
}

func (downForge) CreatePullRequest(context.Context, string, string, string, string) (tracker.PullRequest, error) {
	return tracker.PullRequest{}, errors.New("forge down")
}

func (downForge) MergePullRequest(context.Context, string, tracker.MergeMethod) error {
	return errors.New("forge down")
}
```

Add `"context"` to the imports.

- [ ] **Step 2: Write the failing close E2E tests**

Create `cmd/issue/close_pr_e2e_test.go`:

```go
package issue

import (
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// prCloseRig is a close rig with an origin and an open pull request of the
// feature branch against main on the fake forge.
func newPrCloseRig(t *testing.T) *closeTestRig {
	t.Helper()

	rig := newCloseRig(t)
	rig.addOrigin(t)
	rig.tracker.PullRequests = []tracker.PullRequest{{
		Number: "7", URL: "fake://pr/7", Head: "ABC-1@feat@add-thing", Base: "main",
	}}

	return rig
}

// forgeMerged plays the forge: origin/main takes the feature's tip.
func forgeMerged(t *testing.T, rig *closeTestRig) {
	t.Helper()

	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@feat@add-thing:main")
}

func prPrompter(rig *closeTestRig, strategy commitpkg.MergeStrategy) *scriptedPrompter {
	return &scriptedPrompter{
		Branch: rig.pickedBranchRow(), Strategy: strategy, Confirm: true,
		Message: []byte("feat(thing): close ABC-1\n"), DeleteBranch: true,
	}
}

func TestClose_PullRequest_HappyPath(t *testing.T) {
	rig := newPrCloseRig(t)
	forgeMerged(t, rig)
	p := prPrompter(rig, commitpkg.MergeStrategySquash)

	err := runClose(t.Context(), rig.deps(), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("the forge merged with the picked strategy", func(t *testing.T) {
		if len(rig.tracker.RecordedMerges) != 1 || rig.tracker.RecordedMerges[0] != (fake.Merge{Number: "7", Method: tracker.MergeMethodSquash}) {
			t.Errorf("RecordedMerges = %+v", rig.tracker.RecordedMerges)
		}
	})
	t.Run("no commit message was composed, no base was picked", func(t *testing.T) {
		if p.CapturedPrefill != nil || p.BaseCalled {
			t.Errorf("prefill %v, base picked %v", p.CapturedPrefill, p.BaseCalled)
		}
	})
	t.Run("local main is the forge's main", func(t *testing.T) {
		if got := rig.headSubject(t, "main"); got != "feat: add thing" {
			t.Errorf("main head = %q", got)
		}
	})
	t.Run("the branch is recorded merged", func(t *testing.T) {
		if got := rig.statusOf(t, "ABC-1@feat@add-thing"); got != branch.StatusMerged {
			t.Errorf("status = %q", got)
		}
	})
	t.Run("the closing line names the pull request", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "pull request #7") {
			t.Errorf("stdout %q", rig.stdout.String())
		}
	})
}

func TestClose_PullRequest_BaseIsThePullRequests(t *testing.T) {
	rig := newPrCloseRig(t)
	forgeMerged(t, rig)
	rig.addBranch(t, "develop") // a second candidate: the picker would run

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategyClassic))

	t.Run("the pull request's base is used without the picker", func(t *testing.T) {
		if err != nil || !strings.Contains(rig.stdout.String(), `against "main"`) {
			t.Errorf("err %v, stdout %q", err, rig.stdout.String())
		}
	})
}

func TestClose_PullRequest_BaseFlagConflict(t *testing.T) {
	rig := newPrCloseRig(t)
	d := rig.deps()
	d.baseOverride = "develop"

	err := runClose(t.Context(), d, prPrompter(rig, commitpkg.MergeStrategyClassic))

	t.Run("refused before any merge", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "targets") || len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("err %v, merges %+v", err, rig.tracker.RecordedMerges)
		}
	})
}

func TestClose_PullRequest_UnpushedRefused(t *testing.T) {
	rig := newPrCloseRig(t)
	mustRunGitAt(t, rig.dir, "switch", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "commit", "--allow-empty", "-m", "feat: more")
	mustRunGitAt(t, rig.dir, "switch", "main")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("refused, nothing merged, branch still in progress", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Errorf("err = %v", err)
		}
		if len(rig.tracker.RecordedMerges) != 0 || rig.statusOf(t, "ABC-1@feat@add-thing") != branch.StatusInProgress {
			t.Errorf("merges %+v, status %q", rig.tracker.RecordedMerges, rig.statusOf(t, "ABC-1@feat@add-thing"))
		}
	})
}

func TestClose_PullRequest_BehindRefused(t *testing.T) {
	rig := newPrCloseRig(t)
	mustRunGitAt(t, rig.dir, "switch", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "commit", "--allow-empty", "-m", "feat: more")
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "reset", "-q", "--hard", "HEAD~1")
	mustRunGitAt(t, rig.dir, "switch", "main")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("refused with a fetch hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "behind") || len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("err %v, merges %+v", err, rig.tracker.RecordedMerges)
		}
	})
}

func TestClose_PullRequest_ForgeRefusal(t *testing.T) {
	rig := newPrCloseRig(t)
	rig.tracker.MergeError = errors.New("Pull Request is not mergeable")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("the forge's message is the error", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing changed: main untouched, branch in progress", func(t *testing.T) {
		if got := rig.headSubject(t, "main"); got != "chore: init" {
			t.Errorf("main head = %q", got)
		}
		if got := rig.statusOf(t, "ABC-1@feat@add-thing"); got != branch.StatusInProgress {
			t.Errorf("status = %q", got)
		}
	})
}

func TestClose_PullRequest_NoCapabilityMergesLocally(t *testing.T) {
	rig := newPrCloseRig(t)
	d := rig.deps()
	d.tracker = struct{ tracker.Tracker }{rig.tracker}

	err := runClose(t.Context(), d, prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("the local squash ran, the forge was not asked", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
		if got := rig.headSubject(t, "main"); got != "feat(thing): close ABC-1" {
			t.Errorf("main head = %q", got)
		}
		if len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("merges %+v", rig.tracker.RecordedMerges)
		}
	})
}

// A branch reviewed with git-zf gets the reviewer's commits merged in by the
// review preflight, which puts it ahead of the remote. With a pull request,
// close pushes them itself before asking the forge: the forge must merge
// the reviewed branch, and a "push it first" here would cost a second run.
func TestClose_PullRequest_ReviewedBranchIsPushedFirst(t *testing.T) {
	rig := newPrCloseRig(t)
	// The reviewer fixed the branch on ABC-1@review and pushed it.
	mustRunGitAt(t, rig.dir, "checkout", "-q", "-b", "ABC-1@review", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("feature\nreviewer fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRunGitAt(t, rig.dir, "add", "feature.txt")
	mustRunGitAt(t, rig.dir, "commit", "-q", "-m", "fix: reviewer fix")
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@review")
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")
	featureSHA, _ := rig.client.ResolveRef("refs/heads/ABC-1@feat@add-thing")
	reviewtest.Seed(t, rig.client, "ABC-1", reviewpkg.StatusApproved, 1, featureSHA.String())
	p := prPrompter(rig, commitpkg.MergeStrategyClassic)
	p.DeleteBranch = false // keep the remote branch to inspect it

	err := runClose(t.Context(), rig.deps(), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("the reviewer's commit reached the remote branch before the forge merged", func(t *testing.T) {
		mustRunGitAt(t, rig.dir, "fetch", "-q", "origin")
		if got := rig.headSubject(t, "origin/ABC-1@feat@add-thing"); got != "fix: reviewer fix" {
			t.Errorf("origin feature head = %q", got)
		}
		if len(rig.tracker.RecordedMerges) != 1 {
			t.Errorf("merges %+v", rig.tracker.RecordedMerges)
		}
	})
}
```

This test needs `"os"`, `"path/filepath"`, `reviewpkg "github.com/piprim/git-zf/review"` and `"github.com/piprim/git-zf/review/reviewtest"` in the test file's imports. `reviewPreflight` returns a non-nil cleanup only when it incorporated a review branch (`cmd/issue/close.go`, the `return func(ctx context.Context)` inside the review path), which is the signal the wiring below uses.

If the close rig lacks `headSubject`, `statusOf` or `addBranch`, add them next to `addOrigin` in `close_e2e_test.go` (check first, `grep -n "func (r \*closeTestRig)" cmd/issue/close_e2e_test.go`):

```go
func (r *closeTestRig) headSubject(t *testing.T, ref string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", ref)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log %s: %v\n%s", ref, err, out)
	}

	return strings.TrimSpace(string(out))
}

func (r *closeTestRig) statusOf(t *testing.T, branchName string) string {
	t.Helper()

	_, e, err := branch.Find(t.Context(), r.client, branchName)
	if err != nil {
		t.Fatalf("Find(%s): %v", branchName, err)
	}
	if e == nil {
		return ""
	}

	return e.Status
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issueflow/ -run "TestCheckPushed|TestForgePullRequest" -v; mise exec -- go test ./cmd/issue/ -run "^TestClose_PullRequest" -v`
Expected: compile errors, `undefined: CheckPushed`; the close tests fail (the merge runs locally, no merge recorded).

- [ ] **Step 4: Write the helpers**

Create `cmd/issueflow/forge.go`:

```go
package issueflow

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
)

// ForgePullRequest returns the tracker's forge and branchName's open pull
// request on it. Without the capability, or without a pull request, the pull
// request is nil and the caller merges locally. A forge that cannot be asked
// is a warning and the same nil: the merge then runs locally, as it did
// before the forge existed.
func ForgePullRequest(
	ctx context.Context, t tracker.Tracker, errOut io.Writer, branchName string,
) (tracker.PullRequests, *tracker.PullRequest) {
	forge := tracker.PullRequestsOf(t)
	if forge == nil {
		return nil, nil
	}

	pr, err := forge.FindPullRequest(ctx, branchName)
	switch {
	case errors.Is(err, tracker.ErrNoPullRequest):
		return forge, nil
	case err != nil:
		fmt.Fprintf(errOut, "warning: pull request of %q not looked up, merging locally: %v\n", branchName, err)

		return nil, nil
	}

	return forge, &pr
}

// CheckPushed reports whether branchName's local tip is the remote's, after
// a fetch: the forge merges the branch it sees, so a close through it must
// not merge something else. Ahead, behind and absent are errors that say
// what to do.
func CheckPushed(ctx context.Context, c *git.Client, branchName string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return fmt.Errorf("branch %q has a pull request but this clone has no remote", branchName)
	}
	if err := c.Fetch(ctx); err != nil {
		return err //nolint:wrapcheck // names the remote
	}

	local, err := c.ResolveRef("refs/heads/" + branchName)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", branchName, err)
	}
	remoteRef := "refs/remotes/" + remote + "/" + branchName
	remoteTip, err := c.ResolveRef(remoteRef)
	if err != nil {
		return fmt.Errorf("branch %q is not on %s: push it first", branchName, remote)
	}
	if local == remoteTip {
		return nil
	}
	if ahead, _ := c.IsAncestor(ctx, remoteRef, "refs/heads/"+branchName); ahead {
		return fmt.Errorf("branch %q has unpushed commits: push it first", branchName)
	}

	return fmt.Errorf("branch %q is behind %s/%s: fetch and merge first", branchName, remote, branchName)
}
```

- [ ] **Step 5: Wire the close flow**

In `cmd/issue/close.go`, `runClose`, after the `reviewPreflight` call and its deferred cleanup, replace the base resolution block (from the "Smart-default merge target" comment through `chooseMergeTarget`) with:

```go
	// A branch with an open pull request is merged by the forge, against the
	// pull request's base: no target picker. The branch must be the one the
	// forge shows.
	forge, pr := issueflow.ForgePullRequest(ctx, deps.tracker, deps.client.IO().Err, picked.BranchName)

	var base string
	var forgeMerge *mergeflow.ForgeMerge
	if pr != nil {
		if deps.baseOverride != "" && deps.baseOverride != pr.Base {
			return fmt.Errorf("pull request #%s targets %q, not %q", pr.Number, pr.Base, deps.baseOverride)
		}
		// The review preflight merged reviewer commits into the branch: the
		// forge must see them before it merges. The push adds nothing the
		// remote lacks (they live on its @review branch), so it is not asked.
		if reviewCleanup != nil {
			if err := deps.client.PushBranch(ctx, picked.BranchName); err != nil {
				return fmt.Errorf("push reviewer commits of %q: %w", picked.BranchName, err)
			}
		}
		if err := issueflow.CheckPushed(ctx, deps.client, picked.BranchName); err != nil {
			return err //nolint:wrapcheck // says what to do
		}
		fmt.Fprintf(deps.client.IO().Out, "Pull request #%s %s against %q\n", pr.Number, pr.URL, pr.Base)
		base = pr.Base
		forgeMerge = &mergeflow.ForgeMerge{Forge: forge, Number: pr.Number}
	} else {
		// Smart-default merge target, pre-selected in the picker: the configured
		// base (or DefaultBaseBranch), redirected to the parent integration branch
		// when the picked issue has a parent.
		base, err = issueflow.ResolveParentBranch(ctx, deps.client, picked.IssueSlug, deps.cfg.Branch.Base)
		if err != nil {
			return err
		}

		base, err = chooseMergeTarget(ctx, deps, picked, base, prompter)
		if err != nil {
			return err
		}
	}
```

Pass it to the engine:

```go
	res, err := mergeflow.Run(ctx, deps.client, mergeflow.Params{
		Source:             picked.BranchName,
		Target:             base,
		SourceMaterialized: createdBranch,
		SourceClient:       srcClient,
		PullRequest:        forgeMerge,
	}, prompter, prefill)
```

Replace the closing line:

```go
	if res.ViaForge {
		fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q by pull request #%s and closed.\n",
			picked.BranchName, base, pr.Number)
	} else {
		fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issueflow/ -run "TestCheckPushed|TestForgePullRequest" -v && mise exec -- go test ./cmd/issue/ -run "^TestClose_" -v`
Expected: PASS, the whole close suite included (a rig without a pull request on the fake takes the local path as before).

- [ ] **Step 7: Commit**

```bash
git add cmd/issueflow/forge.go cmd/issueflow/forge_test.go cmd/issue/close.go cmd/issue/close_pr_e2e_test.go cmd/issue/close_e2e_test.go
git commit -m "feat(issue): close merges a branch with a pull request on the forge"
```

---

### Task 11: `branch merge` through the forge, and the docs

**Files:**
- Modify: `cmd/branch/merge.go`, `CLAUDE.md`
- Test: `cmd/branch/merge_e2e_test.go`

**Interfaces:**
- Consumes: Task 9 and 10 (`issueflow.ForgePullRequest`, `CheckPushed`, `mergeflow.ForgeMerge`); `mergeRig`.
- Produces: `mergeDeps.tracker tracker.Tracker`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/branch/merge_e2e_test.go`:

```go
func TestRunMerge_PullRequest(t *testing.T) {
	r := newMergeRig(t)
	r.addOrigin(t)
	r.addLocalBranch(t, "feat-x")
	r.git(t, "push", "-q", "origin", "feat-x")
	r.git(t, "push", "-q", "origin", "feat-x:master") // the forge merged
	ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "3", URL: "fake://pr/3", Head: "feat-x", Base: "master"}}}
	d := r.deps(declinePush)
	d.tracker = ft
	p := &scriptedMergePrompter{Source: SourceBranch{Name: "feat-x"}, Strategy: commit.MergeStrategyRebase, Confirm: true}

	err := runMerge(t.Context(), d, p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("the forge merged with the picked strategy", func(t *testing.T) {
		if len(ft.RecordedMerges) != 1 || ft.RecordedMerges[0] != (fake.Merge{Number: "3", Method: tracker.MergeMethodRebase}) {
			t.Errorf("RecordedMerges = %+v", ft.RecordedMerges)
		}
	})
	t.Run("master is the forge's", func(t *testing.T) {
		if got := r.headSubject(t, "master"); got != "feat: feat-x" {
			t.Errorf("master head = %q", got)
		}
	})
	t.Run("the closing line names the pull request", func(t *testing.T) {
		if !strings.Contains(r.stdout.String(), "pull request #3") {
			t.Errorf("stdout %q", r.stdout.String())
		}
	})
}

func TestRunMerge_PullRequest_BaseIsNotTheCurrentBranch(t *testing.T) {
	r := newMergeRig(t)
	r.addOrigin(t)
	r.addLocalBranch(t, "feat-x")
	r.git(t, "push", "-q", "origin", "feat-x")
	ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "3", Head: "feat-x", Base: "develop"}}}
	d := r.deps(declinePush)
	d.tracker = ft

	err := runMerge(t.Context(), d, &scriptedMergePrompter{Source: SourceBranch{Name: "feat-x"}, Strategy: commit.MergeStrategyRebase, Confirm: true})

	t.Run("refused: the pull request targets another branch", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "develop") || len(ft.RecordedMerges) != 0 {
			t.Errorf("err %v, merges %+v", err, ft.RecordedMerges)
		}
	})
}
```

Add the imports `"github.com/piprim/git-zf/tracker"` and `"github.com/piprim/git-zf/tracker/fake"` to the test file (and `"strings"`, `"github.com/piprim/git-zf/commit"` if missing).

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/branch/ -run "^TestRunMerge_PullRequest" -v`
Expected: compile error, `d.tracker undefined`.

- [ ] **Step 3: Wire the merge command**

In `cmd/branch/merge.go`:

Add to `mergeDeps`:

```go
	tracker tracker.Tracker // nil ⇒ no forge
```

In `mergeRunE`, build it (import `"github.com/piprim/git-zf/tracker"`):

```go
	d := mergeDeps{
		client: c, cfg: b.appConfig,
		push: push, noPush: noPush, pushConfirm: pushflow.NewHuhConfirm(),
	}
	if b.appConfig.IssueTracker.Type != "" {
		t, err := tracker.New(b.appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(c.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			d.tracker = t
		}
	}
```

In `runMerge`, after the materialize-rollback `defer` and before `mergeflow.SourceTree`:

```go
	// A source with an open pull request is merged by the forge; the pull
	// request must target the current branch and the source must be the one
	// the forge shows.
	forge, pr := issueflow.ForgePullRequest(ctx, d.tracker, d.client.IO().Err, source.Name)
	var forgeMerge *mergeflow.ForgeMerge
	if pr != nil {
		if pr.Base != target {
			return fmt.Errorf("pull request #%s targets %q, not the current branch %q", pr.Number, pr.Base, target)
		}
		if err := issueflow.CheckPushed(ctx, d.client, source.Name); err != nil {
			return err //nolint:wrapcheck // says what to do
		}
		fmt.Fprintf(d.client.IO().Out, "Pull request #%s %s against %q\n", pr.Number, pr.URL, pr.Base)
		forgeMerge = &mergeflow.ForgeMerge{Forge: forge, Number: pr.Number}
	}
```

Pass `PullRequest: forgeMerge` in the `mergeflow.Params` literal, and replace the closing line:

```go
	if res.ViaForge {
		fmt.Fprintf(d.client.IO().Out, "Branch %q merged into %q by pull request #%s.\n", source.Name, target, pr.Number)
	} else {
		fmt.Fprintf(d.client.IO().Out, "Branch %q merged into %q.\n", source.Name, target)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/branch/... -v -run "^TestRunMerge_"`
Expected: PASS, the existing merge suite included.

- [ ] **Step 5: Document the test commands**

In `CLAUDE.md`, after the "### Testing the merge flow" section, add:

```markdown
### Testing the pull requests

On GitHub and Forgejo the tracker is also the forge: `tracker.PullRequests`
(`tracker/tracker.go`, discovered with `tracker.PullRequestsOf`) finds, opens
and merges a pull request. `git zf commit` offers one after a push of a
feature branch (one yes/no question, `pushflow.PullRequestOffer`), `git zf
branch pr [branch]` opens one with an editable form (`cmd/branch/pr.go`,
`OpenPullRequest` is the shared step), and `issue close` / `branch merge`
merge a branch that has an open pull request on the forge
(`mergeflow.ForgeMerge`): strategy picker and confirm as today, then the
forge merges and the local base is fast-forwarded. The branch must be at the
remote's tip (`issueflow.CheckPushed`). The fake tracker plays the forge:
`PullRequests`, `RecordedPullRequests`, `RecordedMerges`, `MergeError`; wrap
it in `struct{ tracker.Tracker }{fake}` for a tracker without the capability.

    mise exec -- go test ./tracker/... -run "PullRequest" -v
    mise exec -- go test ./cmd/pushflow/... -run "PullRequest" -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunPr_|^TestPrCmd|^TestOpenPullRequest|^TestPullRequestDraftFor|^TestRunMerge_PullRequest" -v
    mise exec -- go test ./cmd/commit/... -run "TestPullRequestOffer|TestForgeOf" -v
    mise exec -- go test ./cmd/mergeflow/... -run "TestRun_Forge|TestMethodOf" -v
    mise exec -- go test ./cmd/issueflow/... -run "TestCheckPushed|TestForgePullRequest" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_PullRequest" -v

A fork's pull request with the same head ref is not the branch's (Forgejo
filters on `head.repo.full_name`; GitHub's head filter is `owner:branch`).
```

- [ ] **Step 6: Run everything**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: build, vet and every package PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/branch/merge.go cmd/branch/merge_e2e_test.go CLAUDE.md
git commit -m "feat(branch): merge merges a branch with a pull request on the forge"
```
