package redmine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/redmine"
)

// newTestAdapterWithHandler starts an httptest.Server backed by handler and
// returns a tracker.Tracker whose base URL points at that server. The server
// is closed automatically via t.Cleanup.
func newTestAdapterWithHandler(t *testing.T, handler http.HandlerFunc) tracker.Tracker {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "test-key"})
	if err != nil {
		t.Fatalf("redmine.New: %v", err)
	}

	return a
}

func TestListIssues(t *testing.T) {
	t.Parallel()

	t.Run("returns issues with correct fields", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"issues":[{"id":1,"subject":"Fix login","description":"details","status":{"id":1,"name":"New"}}],"total_count":1,"offset":0,"limit":100}`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "test-key"})
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
	})

	t.Run("returns error on 401 unauthorized", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "bad-key"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = adapter.ListIssues(t.Context())
		if err == nil {
			t.Error("expected error on 401, got nil")
		}
	})

	t.Run("populates the Project field from the issue identifier", func(t *testing.T) {
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
	})

	for _, project := range []string{"cpro", "42"} {
		t.Run("asks project "+project+" for its issues", func(t *testing.T) {
			t.Parallel()

			mux := http.NewServeMux()
			mux.HandleFunc("GET /projects/{project}/issues.json", func(w http.ResponseWriter, r *http.Request) {
				if r.PathValue("project") != project {
					http.NotFound(w, r)

					return
				}

				w.Header().Set("Content-Type", "application/json")
				// real Redmine issue responses carry the project's id and name, never its identifier
				fmt.Fprint(w, `{"issues":[{"id":1,"subject":"keep","status":{"id":1,"name":"New"},"project":{"id":42,"name":"C Pro"}}],"total_count":1,"offset":0,"limit":100}`)
			})

			srv := httptest.NewServer(mux)
			defer srv.Close()

			adapter, err := redmine.New(config.IssueTrackerConfig{
				URL:      srv.URL,
				Token:    "k",
				Projects: []string{project},
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
			if issues[0].ID != "1" || issues[0].Project != project {
				t.Errorf("got issue %+v, want id=1 project=%s", issues[0], project)
			}
		})
	}

	t.Run("lists the open issues of a project whoever they are assigned to", func(t *testing.T) {
		t.Parallel()

		got := make(chan url.Values, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /projects/cpro/issues.json", func(w http.ResponseWriter, r *http.Request) {
			got <- r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"issues":[],"total_count":0,"offset":0,"limit":100}`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{
			URL:      srv.URL,
			Token:    "k",
			Projects: []string{"cpro"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if _, err := adapter.ListIssues(t.Context()); err != nil {
			t.Fatalf("ListIssues: %v", err)
		}

		q := <-got
		if q.Get("status_id") != "open" {
			t.Errorf("status_id = %q, want %q", q.Get("status_id"), "open")
		}
		if q.Has("assigned_to_id") {
			t.Errorf("assigned_to_id = %q, want it absent", q.Get("assigned_to_id"))
		}
	})

	t.Run("merges the issues of every configured project", func(t *testing.T) {
		t.Parallel()

		ids := map[string]int{"foo": 1, "bar": 2}
		mux := http.NewServeMux()
		mux.HandleFunc("GET /projects/{project}/issues.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"issues":[{"id":%d,"subject":"x","status":{"id":1,"name":"New"}}],"total_count":1,"offset":0,"limit":100}`,
				ids[r.PathValue("project")])
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{
			URL:      srv.URL,
			Token:    "k",
			Projects: []string{"foo", "bar"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		issues, err := adapter.ListIssues(t.Context())
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}
		if len(issues) != 2 {
			t.Fatalf("got %d issues, want 2", len(issues))
		}
		if issues[0].ID != "1" || issues[0].Project != "foo" || issues[1].ID != "2" || issues[1].Project != "bar" {
			t.Errorf("got %+v, want id=1 project=foo then id=2 project=bar", issues)
		}
	})

	t.Run("names the project whose request fails", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{
			URL:      srv.URL,
			Token:    "k",
			Projects: []string{"nope"},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		_, err = adapter.ListIssues(t.Context())
		if err == nil || !strings.Contains(err.Error(), `"nope"`) {
			t.Errorf("err = %v, want it to name project \"nope\"", err)
		}
	})

	t.Run("lists the issues assigned to the user across the tracker when no project is configured", func(t *testing.T) {
		t.Parallel()

		got := make(chan url.Values, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("GET /issues.json", func(w http.ResponseWriter, r *http.Request) {
			got <- r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"issues":[],"total_count":0,"offset":0,"limit":100}`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "k"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if _, err := adapter.ListIssues(t.Context()); err != nil {
			t.Fatalf("ListIssues: %v", err)
		}

		if q := <-got; q.Get("assigned_to_id") != "me" || q.Get("status_id") != "open" {
			t.Errorf("query = %v, want assigned_to_id=me and status_id=open", q)
		}
	})

	t.Run("falls back to project name when identifier is missing", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/issues.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// real Redmine issue responses include name but not identifier
			fmt.Fprint(w, `{"issues":[{"id":1,"subject":"x","status":{"id":1,"name":"New"},"project":{"id":7,"name":"My Project"}}],"total_count":1,"offset":0,"limit":100}`)
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
		if issues[0].Project != "My Project" {
			t.Errorf("Project = %q, want %q", issues[0].Project, "My Project")
		}
	})
}

func TestUpdateIssueStatus(t *testing.T) {
	t.Parallel()

	t.Run("updates issue status_id via PUT", func(t *testing.T) {
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

			var body struct {
				Issue struct {
					StatusID int `json:"status_id"`
				} `json:"issue"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)

				return
			}
			if body.Issue.StatusID != 2 {
				http.Error(w, fmt.Sprintf("expected status_id=2, got %d", body.Issue.StatusID), http.StatusBadRequest)

				return
			}

			w.WriteHeader(http.StatusOK)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "key"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err := adapter.UpdateIssueStatus(t.Context(), "42", "In Progress"); err != nil {
			t.Fatalf("UpdateIssueStatus: %v", err)
		}
	})

	t.Run("returns error when status name is not found", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/issue_statuses.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"issue_statuses":[{"id":1,"name":"New"}]}`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "key"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		err = adapter.UpdateIssueStatus(t.Context(), "42", "In Progress")
		if err == nil {
			t.Error("expected error for missing status, got nil")
		}
		if !strings.Contains(err.Error(), "In Progress") {
			t.Errorf("error should mention the status name, got: %v", err)
		}
	})
}

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

func TestAddComment(t *testing.T) {
	t.Parallel()

	t.Run("PUTs the body as issue notes", func(t *testing.T) {
		t.Parallel()

		var (
			hits   int
			method string
			path   string
			got    struct {
				Issue struct {
					Notes string `json:"notes"`
				} `json:"issue"`
			}
		)
		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, r *http.Request) {
			hits++
			method, path = r.Method, r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusNoContent)
		})

		if err := a.AddComment(t.Context(), "42", "needs tests"); err != nil {
			t.Fatalf("AddComment: %v", err)
		}

		if hits != 1 || method != http.MethodPut || path != "/issues/42.json" {
			t.Errorf("got %d hits, %s %s; want 1 hit, PUT /issues/42.json", hits, method, path)
		}

		if got.Issue.Notes != "needs tests" {
			t.Errorf(`notes = %q, want "needs tests"`, got.Issue.Notes)
		}
	})

	t.Run("surfaces a non-2xx response as an error", func(t *testing.T) {
		t.Parallel()

		a := newTestAdapterWithHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
		})

		if err := a.AddComment(t.Context(), "42", "x"); err == nil {
			t.Error("expected error on 422, got nil")
		}
	})
}
