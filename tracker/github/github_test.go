package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/piprim/git-zf/config"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("returns error when token is empty", func(t *testing.T) {
		t.Parallel()

		_, err := New(config.IssueTrackerConfig{})
		if err == nil {
			t.Fatal("New with empty Token: expected error, got nil")
		}
	})

	t.Run("returns a non-nil adapter with a valid token", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if a == nil {
			t.Fatal("New returned nil adapter")
		}
	})

	t.Run("accepts an enterprise base URL", func(t *testing.T) {
		t.Parallel()

		_, err := New(config.IssueTrackerConfig{
			Token: "x",
			URL:   "https://github.example.com/api/v3/",
		})
		if err != nil {
			t.Fatalf("New with enterprise URL: %v", err)
		}
	})
}

// newTestAdapter returns a *githubAdapter whose BaseURL points at srv.
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

func TestListIssues(t *testing.T) {
	t.Parallel()

	t.Run("returns issues excluding pull requests", func(t *testing.T) {
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
	})

	t.Run("follows pagination link headers", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("/issues", func(w http.ResponseWriter, r *http.Request) {
			page := r.URL.Query().Get("page")
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
	})

	t.Run("filters issues to the configured project", func(t *testing.T) {
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
	})
}

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

func TestUpdateIssueStatus(t *testing.T) {
	t.Parallel()

	t.Run("sends PATCH to close an issue", func(t *testing.T) {
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
	})

	t.Run("sends PATCH to reopen an issue", func(t *testing.T) {
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
	})

	t.Run("returns error without calling server for unknown status", func(t *testing.T) {
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
	})

	t.Run("returns error when no projects are configured", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error("expected error when Projects is empty, got nil")
		}
	})

	t.Run("returns error when multiple projects are configured", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{Token: "x", Projects: []string{"a/b", "c/d"}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error("expected error when Projects has 2+ entries, got nil")
		}
	})

	t.Run("returns error for a malformed owner/repo project string", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{Token: "x", Projects: []string{"onlyone"}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error(`expected error when Projects[0] is not "owner/repo", got nil`)
		}
	})

	t.Run("returns error for a non-integer issue ID", func(t *testing.T) {
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
	})
}
