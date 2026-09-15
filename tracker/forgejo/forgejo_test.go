package forgejo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("returns error when URL is empty", func(t *testing.T) {
		t.Parallel()

		_, err := New(config.IssueTrackerConfig{Token: "x"})
		if err == nil {
			t.Fatal("New with empty URL: expected error, got nil")
		}
	})

	t.Run("returns error when token is empty", func(t *testing.T) {
		t.Parallel()

		_, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org"})
		if err == nil {
			t.Fatal("New with empty Token: expected error, got nil")
		}
	})

	t.Run("returns a non-nil adapter with URL and token", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org", Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if a == nil {
			t.Fatal("New returned nil adapter")
		}
	})

	t.Run("appends /api/v1 to an instance root URL", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org/", Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fa, ok := a.(*forgejoAdapter)
		if !ok {
			t.Fatalf("New returned %T, want *forgejoAdapter", a)
		}

		if fa.apiBase != "https://codeberg.org/api/v1" {
			t.Errorf("apiBase = %q, want %q", fa.apiBase, "https://codeberg.org/api/v1")
		}
	})

	t.Run("strips userinfo from apiBase and keeps it for proxy basic auth", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://user:password@forgejo.example.org/", Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fa, ok := a.(*forgejoAdapter)
		if !ok {
			t.Fatalf("New returned %T, want *forgejoAdapter", a)
		}

		if fa.apiBase != "https://forgejo.example.org/api/v1" {
			t.Errorf("apiBase = %q, want %q", fa.apiBase, "https://forgejo.example.org/api/v1")
		}

		if fa.proxyAuth == nil || fa.proxyAuth.Username() != "user" {
			t.Errorf("proxyAuth = %v, want user=user", fa.proxyAuth)
		}

		if pw, _ := fa.proxyAuth.Password(); pw != "password" {
			t.Errorf("proxyAuth password = %q, want %q", pw, "password")
		}
	})

	t.Run("returns error for an unparsable URL", func(t *testing.T) {
		t.Parallel()

		if _, err := New(config.IssueTrackerConfig{URL: "https://bad url", Token: "x"}); err == nil {
			t.Fatal("expected error for unparsable URL, got nil")
		}
	})

	t.Run("tolerates a URL that already ends in /api/v1", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org/api/v1/", Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		fa, ok := a.(*forgejoAdapter)
		if !ok {
			t.Fatalf("New returned %T, want *forgejoAdapter", a)
		}

		if fa.apiBase != "https://codeberg.org/api/v1" {
			t.Errorf("apiBase = %q, want %q", fa.apiBase, "https://codeberg.org/api/v1")
		}
	})
}

func TestListStatuses(t *testing.T) {
	t.Parallel()

	t.Run("returns the static open/closed pair", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org", Token: "x"})
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
	})
}

// newTestAdapter returns an adapter whose API base points at srv.
func newTestAdapter(t *testing.T, srv *httptest.Server, cfg config.IssueTrackerConfig) tracker.Tracker {
	t.Helper()

	cfg.URL = srv.URL
	if cfg.Token == "" {
		cfg.Token = "test-token"
	}

	a, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return a
}

func TestListIssues(t *testing.T) {
	t.Parallel()

	t.Run("returns assigned open issues excluding pull requests", func(t *testing.T) {
		t.Parallel()

		var gotAuth string
		var gotQuery url.Values

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotQuery = r.URL.Query()

			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)

				return
			}

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[
				{"number": 42, "title": "Fix login", "body": "details", "state": "open",
				 "repository": {"full_name": "octocat/hello"}, "pull_request": null},
				{"number": 99, "title": "PR title", "body": "", "state": "open",
				 "repository": {"full_name": "octocat/hello"},
				 "pull_request": {"merged": false}}
			]`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{})

		issues, err := a.ListIssues(t.Context())
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}

		if len(issues) != 1 {
			t.Fatalf("got %d issues, want 1 (PR should be filtered)", len(issues))
		}

		got := issues[0]
		want := tracker.Issue{
			TrackerType: "forgejo", ID: "42", Subject: "Fix login",
			Description: "details", Status: "open", Project: "octocat/hello",
		}
		if got != want {
			t.Errorf("issue = %+v, want %+v", got, want)
		}

		if gotAuth != "token test-token" {
			t.Errorf(`Authorization = %q, want "token test-token"`, gotAuth)
		}

		for k, v := range map[string]string{"state": "open", "assigned": "true", "type": "issues"} {
			if gotQuery.Get(k) != v {
				t.Errorf("query %s = %q, want %q", k, gotQuery.Get(k), v)
			}
		}
	})

	t.Run("paginates until an empty page", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			switch r.URL.Query().Get("page") {
			case "1":
				fmt.Fprint(w, `[{"number":1,"title":"a","state":"open","repository":{"full_name":"o/r"}}]`)
			case "2":
				fmt.Fprint(w, `[{"number":2,"title":"b","state":"open","repository":{"full_name":"o/r"}}]`)
			case "3":
				fmt.Fprint(w, `[]`)
			default:
				http.Error(w, "unexpected page", http.StatusBadRequest)
			}
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{})

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

	// newBasicAuthAdapter returns an adapter whose URL carries user:password
	// userinfo, pointing at srv — the shape of a Forgejo instance behind an
	// HTTP Basic auth reverse-proxy gate.
	newBasicAuthAdapter := func(t *testing.T, srv *httptest.Server) tracker.Tracker {
		t.Helper()

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("parse server URL: %v", err)
		}

		u.User = url.UserPassword("gate-user", "gate-pass")

		a, err := New(config.IssueTrackerConfig{URL: u.String(), Token: "test-token"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		return a
	}

	t.Run("behind a basic-auth gate sends proxy credentials and the token as a query param", func(t *testing.T) {
		t.Parallel()

		var (
			gotUser, gotPass string
			gotBasicOK       bool
			gotAuthHeader    string
			gotTokenParam    string
		)

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPass, gotBasicOK = r.BasicAuth()
			gotAuthHeader = r.Header.Get("Authorization")
			gotTokenParam = r.URL.Query().Get("token")

			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[]`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newBasicAuthAdapter(t, srv)

		if _, err := a.ListIssues(t.Context()); err != nil {
			t.Fatalf("ListIssues: %v", err)
		}

		if !gotBasicOK || gotUser != "gate-user" || gotPass != "gate-pass" {
			t.Errorf("basic auth = (%q, %q, ok=%v), want (gate-user, gate-pass, true)", gotUser, gotPass, gotBasicOK)
		}

		if strings.HasPrefix(gotAuthHeader, "token ") {
			t.Errorf("Authorization = %q; must not carry the token when the header is needed for the gate", gotAuthHeader)
		}

		if gotTokenParam != "test-token" {
			t.Errorf("query token = %q, want %q", gotTokenParam, "test-token")
		}
	})

	t.Run("behind a basic-auth gate never leaks the token into error messages", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "", http.StatusUnauthorized)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newBasicAuthAdapter(t, srv)

		_, err := a.ListIssues(t.Context())
		if err == nil {
			t.Fatal("expected error on 401, got nil")
		}

		if strings.Contains(err.Error(), "test-token") {
			t.Errorf("error message leaks the token: %q", err.Error())
		}
	})

	t.Run("stops after maxPages when the server ignores page", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"number":1,"title":"a","state":"open","repository":{"full_name":"o/r"}}]`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{})

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		issues, err := a.ListIssues(ctx)
		if err != nil {
			t.Fatalf("ListIssues: %v (an unbounded loop would hit the context deadline)", err)
		}

		if len(issues) != maxPages {
			t.Errorf("got %d issues, want %d (one per page, capped)", len(issues), maxPages)
		}
	})

	t.Run("filters issues to the configured projects", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)

				return
			}

			fmt.Fprint(w, `[
				{"number": 1, "title": "keep", "state": "open", "repository": {"full_name": "a/b"}},
				{"number": 2, "title": "drop", "state": "open", "repository": {"full_name": "c/d"}}
			]`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

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

	t.Run("stamps the configured tracker type so the gitea alias is visible", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.URL.Query().Get("page") != "1" {
				fmt.Fprint(w, `[]`)

				return
			}

			fmt.Fprint(w, `[{"number":1,"title":"a","state":"open","repository":{"full_name":"o/r"}}]`)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Type: "gitea"})

		issues, err := a.ListIssues(t.Context())
		if err != nil {
			t.Fatalf("ListIssues: %v", err)
		}

		if len(issues) != 1 || issues[0].TrackerType != "gitea" {
			t.Errorf("issues = %+v, want one issue with TrackerType=gitea", issues)
		}
	})

	t.Run("returns an error on a non-200 response", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"token is required"}`, http.StatusUnauthorized)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{})

		if _, err := a.ListIssues(t.Context()); err == nil {
			t.Fatal("expected error on 401, got nil")
		}
	})
}

func TestUpdateIssueStatus(t *testing.T) {
	t.Parallel()

	// patchRecorder serves PATCH /api/v1/repos/a/b/issues/42, capturing the
	// decoded body and counting hits.
	type patchRecorder struct {
		hits int
		body struct {
			State string `json:"state"`
		}
	}

	newPatchServer := func(t *testing.T) (*httptest.Server, *patchRecorder) {
		t.Helper()

		rec := &patchRecorder{}
		mux := http.NewServeMux()
		mux.HandleFunc("PATCH /api/v1/repos/a/b/issues/42", func(w http.ResponseWriter, r *http.Request) {
			rec.hits++
			_ = json.NewDecoder(r.Body).Decode(&rec.body)
			w.WriteHeader(http.StatusCreated) // Forgejo answers 201 on edit
			fmt.Fprint(w, `{"number":42,"state":"`+rec.body.State+`"}`)
		})

		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		return srv, rec
	}

	t.Run("sends PATCH to close an issue", func(t *testing.T) {
		t.Parallel()

		srv, rec := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err != nil {
			t.Fatalf("UpdateIssueStatus: %v", err)
		}

		if rec.hits != 1 {
			t.Errorf("server hits = %d, want 1", rec.hits)
		}

		if rec.body.State != "closed" {
			t.Errorf(`state = %q, want "closed"`, rec.body.State)
		}
	})

	t.Run("sends PATCH to reopen an issue", func(t *testing.T) {
		t.Parallel()

		srv, rec := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "open"); err != nil {
			t.Fatalf("UpdateIssueStatus: %v", err)
		}

		if rec.body.State != "open" {
			t.Errorf(`state = %q, want "open"`, rec.body.State)
		}
	})

	t.Run("returns error without calling server for unknown status", func(t *testing.T) {
		t.Parallel()

		srv, rec := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "WIP"); err == nil {
			t.Fatal("expected error for unknown status, got nil")
		}

		if rec.hits != 0 {
			t.Errorf("server hits = %d, want 0 (no HTTP call should be made)", rec.hits)
		}
	})

	t.Run("returns error when no projects are configured", func(t *testing.T) {
		t.Parallel()

		srv, rec := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{})

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error("expected error when Projects is empty, got nil")
		}

		if rec.hits != 0 {
			t.Errorf("server hits = %d, want 0", rec.hits)
		}
	})

	t.Run("returns error when multiple projects are configured", func(t *testing.T) {
		t.Parallel()

		srv, _ := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b", "c/d"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error("expected error when Projects has 2+ entries, got nil")
		}
	})

	t.Run("returns error for a malformed owner/repo project string", func(t *testing.T) {
		t.Parallel()

		srv, _ := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"onlyone"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Error(`expected error when Projects[0] is not "owner/repo", got nil`)
		}
	})

	t.Run("returns error for a non-integer issue ID", func(t *testing.T) {
		t.Parallel()

		srv, rec := newPatchServer(t)
		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

		if err := a.UpdateIssueStatus(t.Context(), "not-a-number", "closed"); err == nil {
			t.Fatal("expected error for non-integer issue id, got nil")
		}

		if rec.hits != 0 {
			t.Errorf("server hits = %d, want 0", rec.hits)
		}
	})

	t.Run("wraps a non-2xx response as an error", func(t *testing.T) {
		t.Parallel()

		mux := http.NewServeMux()
		mux.HandleFunc("PATCH /api/v1/repos/a/b/issues/42", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
		})

		srv := httptest.NewServer(mux)
		defer srv.Close()

		a := newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})

		if err := a.UpdateIssueStatus(t.Context(), "42", "closed"); err == nil {
			t.Fatal("expected error on 403, got nil")
		}
	})
}

func TestIsIssueClosed(t *testing.T) {
	t.Parallel()

	// newGetServer serves GET /api/v1/repos/a/b/issues/42 with handler and
	// returns an adapter configured for project a/b.
	newGetAdapter := func(t *testing.T, handler http.HandlerFunc) tracker.Tracker {
		t.Helper()

		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/repos/a/b/issues/42", handler)

		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)

		return newTestAdapter(t, srv, config.IssueTrackerConfig{Projects: []string{"a/b"}})
	}

	t.Run("returns true for closed issue", func(t *testing.T) {
		t.Parallel()

		a := newGetAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"number":42,"state":"closed"}`)
		})

		closed, err := a.IsIssueClosed(t.Context(), "42")
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if !closed {
			t.Fatal("want closed=true")
		}
	})

	t.Run("returns false for open issue", func(t *testing.T) {
		t.Parallel()

		a := newGetAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"number":42,"state":"open"}`)
		})

		closed, err := a.IsIssueClosed(t.Context(), "42")
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if closed {
			t.Fatal("want closed=false")
		}
	})

	t.Run("returns ErrIssueNotFound on 404", func(t *testing.T) {
		t.Parallel()

		a := newGetAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		})

		_, err := a.IsIssueClosed(t.Context(), "42")
		if !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Fatalf("got %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("wraps other transport errors", func(t *testing.T) {
		t.Parallel()

		a := newGetAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `boom`, http.StatusInternalServerError)
		})

		_, err := a.IsIssueClosed(t.Context(), "42")
		if err == nil || errors.Is(err, tracker.ErrIssueNotFound) {
			t.Fatalf("want wrapped non-404 error, got %v", err)
		}
	})

	t.Run("returns error when no projects are configured", func(t *testing.T) {
		t.Parallel()

		a, err := New(config.IssueTrackerConfig{URL: "https://codeberg.org", Token: "x"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if _, err := a.IsIssueClosed(t.Context(), "42"); err == nil {
			t.Error("expected error when Projects is empty, got nil")
		}
	})
}

func TestRegistration(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{"forgejo", "gitea"} {
		t.Run("tracker.New resolves type "+typ, func(t *testing.T) {
			t.Parallel()

			a, err := tracker.New(config.IssueTrackerConfig{Type: typ, URL: "https://example.org", Token: "x"})
			if err != nil {
				t.Fatalf("tracker.New(%q): %v", typ, err)
			}

			if _, ok := a.(*forgejoAdapter); !ok {
				t.Fatalf("tracker.New(%q) returned %T, want *forgejoAdapter", typ, a)
			}
		})
	}
}
