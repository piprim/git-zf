package branch

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func openTestBranchStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestBranchList(t *testing.T) {
	t.Parallel()

	t.Run("json output includes branch and issue fields", func(t *testing.T) {
		t.Parallel()

		s := openTestBranchStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&store.Issue{IDSlug: "ABC-42", Title: "Add OAuth login", StatusID: 1},
			&store.Branch{Name: "ABC-42@feat@add-oauth-login@550e8400", Type: "feat", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}

		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		var rows []store.BranchRow
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].IssueSlug != "ABC-42" {
			t.Errorf("IssueSlug = %q, want ABC-42", rows[0].IssueSlug)
		}
		if rows[0].BranchName != "ABC-42@feat@add-oauth-login@550e8400" {
			t.Errorf("BranchName = %q", rows[0].BranchName)
		}
		if string(rows[0].Status) != "in_progress" {
			t.Errorf("Status = %q, want in_progress", rows[0].Status)
		}
	})

	t.Run("json output is an empty array for an empty store", func(t *testing.T) {
		t.Parallel()

		s := openTestBranchStore(t)
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		var rows []store.BranchRow
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})

	t.Run("stdout output includes header and branch slug", func(t *testing.T) {
		t.Parallel()

		s := openTestBranchStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&store.Issue{IDSlug: "XY-1", Title: "Some feature", StatusID: 1},
			&store.Branch{Name: "XY-1@feat@some-feature@aabbccdd", Type: "feat", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}

		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "ISSUE ID") {
			t.Errorf("output missing ISSUE ID header: %q", out)
		}
		if !strings.Contains(out, "XY-1") {
			t.Errorf("output missing issue slug XY-1: %q", out)
		}
	})

	t.Run("stdout output says 'No branches found' for an empty store", func(t *testing.T) {
		t.Parallel()

		s := openTestBranchStore(t)
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, s, listFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(buf.String(), "No branches found.") {
			t.Errorf("expected 'No branches found.', got: %q", buf.String())
		}
	})
}

