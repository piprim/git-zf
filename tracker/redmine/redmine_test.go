package redmine_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker/redmine"
)

func TestListIssues_success(t *testing.T) {
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
}

func TestListIssues_authFailure(t *testing.T) {
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
}

func TestUpdateIssueStatus_success(t *testing.T) {
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
}

func TestUpdateIssueStatus_statusNotFound(t *testing.T) {
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
}

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

func TestListIssues_populatesProject_nameFallback(t *testing.T) {
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
}
