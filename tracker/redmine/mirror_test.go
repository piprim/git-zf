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
	t.Run("the created issue's number, status name and creation date are returned", func(t *testing.T) {
		if got.ID != "57" || got.Status != "New" || !got.CreatedAt.Equal(time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)) {
			t.Errorf("issue = %+v", got)
		}
	})
}

func TestCreateIssue_Errors(t *testing.T) {
	t.Parallel()

	t.Run("a rejected issue reports Redmine's message", func(t *testing.T) {
		t.Parallel()

		a := newMirrorAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"errors": ["Tracker cannot be blank"]}`)
		})

		_, err := a.CreateIssue(t.Context(), "Bug", "Steps")
		if err == nil || !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "Tracker cannot be blank") {
			t.Errorf("err = %v", err)
		}
	})

	for name, projects := range map[string][]config.TrackerProject{
		"no project":   nil,
		"two projects": {{NearSlug: "a", FarSlug: "a"}, {NearSlug: "b", FarSlug: "b"}},
	} {
		t.Run(name+" is refused by the project calls", func(t *testing.T) {
			t.Parallel()

			a, err := New(config.IssueTrackerConfig{URL: "http://redmine.invalid", Token: "k", Projects: projects})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := a.CreateIssue(t.Context(), "Bug", ""); err == nil || !strings.Contains(err.Error(), "exactly one project") {
				t.Errorf("CreateIssue err = %v", err)
			}
			if _, err := a.ListProjectIssues(t.Context()); err == nil || !strings.Contains(err.Error(), "exactly one project") {
				t.Errorf("ListProjectIssues err = %v", err)
			}
		})
	}
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
		"closing picks the first closed status": {false, 5},
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
