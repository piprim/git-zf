package issue

import (
	"bytes"
	"strings"
	"testing"

	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

func TestRenderRecord_AuthorAndAssignees(t *testing.T) {
	t.Parallel()

	const pi = "Pi <pi@example.org>"
	render := func(rec issuepkg.Record) string {
		var out bytes.Buffer
		renderRecord(&out, &rec)

		return out.String()
	}
	seen := &issuepkg.TrackerSnapshot{Assignees: []string{"bob", "carol"}}

	t.Run("an issue created in git-zf shows its git identity", func(t *testing.T) {
		t.Parallel()

		if out := render(issuepkg.Record{ID: "abc", Author: pi}); !strings.Contains(out, "\nAuthor: "+pi+"\n") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("the tracker's assignees are all listed", func(t *testing.T) {
		t.Parallel()

		if out := render(issuepkg.Record{ID: "abc", TrackerSeen: seen}); !strings.Contains(out, "\nAssigned to: bob, carol\n") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("the tracker's assignees win over the local one", func(t *testing.T) {
		t.Parallel()

		out := render(issuepkg.Record{ID: "abc", Assignee: pi, TrackerSeen: seen})
		if !strings.Contains(out, "\nAssigned to: bob, carol\n") || strings.Contains(out, pi) {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("the local assignee when the tracker names nobody", func(t *testing.T) {
		t.Parallel()

		if out := render(issuepkg.Record{ID: "abc", Assignee: pi}); !strings.Contains(out, "\nAssigned to: "+pi+"\n") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("an unknown author and nobody assigned print no line", func(t *testing.T) {
		t.Parallel()

		if out := render(issuepkg.Record{ID: "abc"}); strings.Contains(out, "Author:") || strings.Contains(out, "Assigned to:") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("the author line sits between the state and the ID", func(t *testing.T) {
		t.Parallel()

		out := render(issuepkg.Record{ID: "abc", Author: pi})
		state, author, id := strings.Index(out, "State:"), strings.Index(out, "Author:"), strings.Index(out, "ID:")
		if state >= author || author >= id {
			t.Errorf("State at %d, Author at %d, ID at %d in %q", state, author, id, out)
		}
	})
}

func TestRenderTrackerIssue_AuthorAndAssignees(t *testing.T) {
	t.Parallel()

	render := func(iss tracker.Issue) string {
		var out bytes.Buffer
		renderTrackerIssue(&out, &iss, nil)

		return out.String()
	}

	t.Run("author and assignees are printed", func(t *testing.T) {
		t.Parallel()

		out := render(tracker.Issue{ID: "42", Author: "alice", Assignees: []string{"bob", "carol"}})
		if !strings.Contains(out, "\nAuthor: alice\n") || !strings.Contains(out, "\nAssigned to: bob, carol\n") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("neither line when the tracker names nobody", func(t *testing.T) {
		t.Parallel()

		if out := render(tracker.Issue{ID: "42"}); strings.Contains(out, "Author:") || strings.Contains(out, "Assigned to:") {
			t.Errorf("output = %q", out)
		}
	})
}

func TestMirror_ShowAuthorAndAssignee(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	iss := fromTracker("42", "A", "open")
	iss.Author, iss.Assignees = "alice", []string{"bob"}
	ft.ProjectIssues = []tracker.Issue{iss}
	if err := runSync(ctx, rig.client, m); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	rig.stdout.Reset()
	lists := ft.ListCalls

	err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{"42"}, false)
	out := rig.stdout.String()

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runShow: %v", err)
		}
	})
	t.Run("the tracker's author and assignee are shown", func(t *testing.T) {
		if !strings.Contains(out, "\nAuthor: alice\n") || !strings.Contains(out, "\nAssigned to: bob\n") {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("show does not ask the tracker", func(t *testing.T) {
		if ft.ListCalls != lists {
			t.Errorf("ListCalls %d → %d", lists, ft.ListCalls)
		}
	})
}
