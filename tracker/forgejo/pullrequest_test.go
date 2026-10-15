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
