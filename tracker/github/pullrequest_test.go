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

	t.Run("sends the merge method and the head", func(t *testing.T) {
		t.Parallel()

		var sent struct {
			MergeMethod string `json:"merge_method"`
			SHA         string `json:"sha"`
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

		if err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "12", tracker.MergeMethodSquash, "abc123"); err != nil {
			t.Fatalf("MergePullRequest: %v", err)
		}
		if sent.MergeMethod != "squash" {
			t.Errorf("merge_method = %q", sent.MergeMethod)
		}
		if sent.SHA != "abc123" {
			t.Errorf("sha = %q", sent.SHA)
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

		err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "12", tracker.MergeMethodMerge, "abc123")
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("a number that is not a number is an error", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.NewServeMux())
		defer srv.Close()

		if err := newTestAdapter(t, srv, []string{"a/b"}).MergePullRequest(t.Context(), "x", tracker.MergeMethodMerge, "abc123"); err == nil {
			t.Error("want an error")
		}
	})
}
