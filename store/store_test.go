package store

import (
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestOpen_createsMigrations(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	// Verify all three tables exist by querying sqlite_master.
	tables := []string{"statuses", "issues", "branches"}
	for _, tbl := range tables {
		var name string
		row := s.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q not found: %v", tbl, err)
		}
	}
}

func TestMigration_idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s1, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = s1.Close()

	s2, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	_ = s2.Close()
}

func TestInsertIssueWithBranch(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	issue := Issue{IDSlug: "ABC-42", Title: "Add OAuth Login", StatusID: 1}
	branch := Branch{
		UUID:     "550e8400",
		Name:     "ABC-42@feat@add-oauth-login@550e8400",
		Type:     "feat",
		StatusID: 1,
	}

	if err := s.InsertIssueWithBranch(t.Context(), &issue, &branch); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	// Verify the branch row exists.
	var name string
	row := s.db.QueryRow("SELECT name FROM branches WHERE uuid = ?", "550e8400")
	if err := row.Scan(&name); err != nil {
		t.Fatalf("branch not found: %v", err)
	}
	if name != "ABC-42@feat@add-oauth-login@550e8400" {
		t.Errorf("name = %q, want %q", name, "ABC-42@feat@add-oauth-login@550e8400")
	}

	// Verify the issue row exists.
	var idSlug string
	row = s.db.QueryRow("SELECT id_slug FROM issues WHERE id = (SELECT issue_id FROM branches WHERE uuid = ?)", "550e8400")
	if err := row.Scan(&idSlug); err != nil {
		t.Fatalf("issue not found: %v", err)
	}
	if idSlug != "ABC-42" {
		t.Errorf("id_slug = %q, want %q", idSlug, "ABC-42")
	}
}

func TestListBranches_all(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "A-1", Title: "First", StatusID: 1},
		&Branch{UUID: "uuid-1", Name: "A-1@feat@first@uuid-1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "A-2", Title: "Second", StatusID: 1},
		&Branch{UUID: "uuid-2", Name: "A-2@fix@second@uuid-2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestListBranches_filterInProgress(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "B-1", Title: "In progress issue", StatusID: 1},
		&Branch{UUID: "uuid-ip", Name: "B-1@feat@in-progress@uuid-ip", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert in_progress: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != BranchStatusInProgress {
		t.Errorf("Status = %q, want %q", rows[0].Status, BranchStatusInProgress)
	}
}

func TestListBranches_filterMerged(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "C-1", Title: "Merged issue", StatusID: 1},
		&Branch{UUID: "uuid-mg", Name: "C-1@feat@merged@uuid-mg", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	now := time.Now()
	if err := s.UpdateBranchStatus(t.Context(), "uuid-mg", 2, &now); err != nil {
		t.Fatalf("UpdateBranchStatus: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != BranchStatusMerged {
		t.Errorf("Status = %q, want %q", rows[0].Status, BranchStatusMerged)
	}
}

func TestListBranches_empty(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestListBranches_order(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	// Insert two branches with explicit created_at values to test DESC ordering.
	for _, row := range []struct {
		uuid, slug, name, tp, dt string
	}{
		{"uuid-old", "D-1", "D-1@feat@old@uuid-old", "feat", "2025-01-01 00:00:00"},
		{"uuid-new", "D-2", "D-2@feat@new@uuid-new", "feat", "2025-06-01 00:00:00"},
	} {
		if _, err := s.db.ExecContext(t.Context(),
			`INSERT INTO issues (id_slug, title, status_id) VALUES (?, 'T', 1)`, row.slug,
		); err != nil {
			t.Fatalf("insert issue: %v", err)
		}

		var issueID int64
		if err := s.db.QueryRowContext(t.Context(),
			"SELECT last_insert_rowid()",
		).Scan(&issueID); err != nil {
			t.Fatalf("last insert id: %v", err)
		}

		if _, err := s.db.ExecContext(t.Context(),
			`INSERT INTO branches (uuid, name, issue_id, type, status_id, created_at)
			 VALUES (?, ?, ?, ?, 1, ?)`,
			row.uuid, row.name, issueID, row.tp, row.dt,
		); err != nil {
			t.Fatalf("insert branch: %v", err)
		}
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].IssueSlug != "D-2" {
		t.Errorf("first row (newest) = %q, want %q", rows[0].IssueSlug, "D-2")
	}
	if rows[1].IssueSlug != "D-1" {
		t.Errorf("second row (oldest) = %q, want %q", rows[1].IssueSlug, "D-1")
	}
}

func TestUpdateBranchStatus_merged(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	issue := Issue{IDSlug: "ABC-1", Title: "Some issue", StatusID: 1}
	branch := Branch{UUID: "aabbccdd", Name: "ABC-1@fix@some-issue@aabbccdd", Type: "fix", StatusID: 1}
	if err := s.InsertIssueWithBranch(t.Context(), &issue, &branch); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	// Updating to merged without merged_at must fail (trigger).
	if err := s.UpdateBranchStatus(t.Context(), "aabbccdd", 2, nil); err == nil {
		t.Error("expected error when merged_at is nil for merged status, got nil")
	}

	// Updating to merged with merged_at must succeed.
	now := time.Now()
	if err := s.UpdateBranchStatus(t.Context(), "aabbccdd", 2, &now); err != nil {
		t.Errorf("UpdateBranchStatus merged: %v", err)
	}

	var statusID int64
	row := s.db.QueryRow("SELECT status_id FROM branches WHERE uuid = ?", "aabbccdd")
	if err := row.Scan(&statusID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if statusID != 2 {
		t.Errorf("status_id = %d, want 2", statusID)
	}
}

func TestListBranches_uuidPopulated(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "U-1", Title: "uuid test", StatusID: 1},
		&Branch{UUID: "deadbeef", Name: "U-1@feat@uuid-test@deadbeef", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].UUID != "deadbeef" {
		t.Errorf("UUID = %q, want %q", rows[0].UUID, "deadbeef")
	}
}

func TestDeleteBranch(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "DEL-1", Title: "to delete", StatusID: 1},
		&Branch{UUID: "cafebabe", Name: "DEL-1@fix@to-delete@cafebabe", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := s.DeleteBranch(t.Context(), "cafebabe"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows after delete, want 0", len(rows))
	}
}

func TestDeleteBranch_notFound(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.DeleteBranch(t.Context(), "nonexistent"); err == nil {
		t.Error("expected error for missing uuid, got nil")
	}
}

func TestInsertIssueWithBranch_trackerTypeNilStoresNull(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "TRK-1", Title: "Pre-tracker issue", StatusID: 1},
		&Branch{UUID: "trk-uuid-1", Name: "TRK-1@feat@pre-tracker@trk-uuid-1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var trackerType *string
	row := s.db.QueryRow("SELECT tracker_type FROM issues WHERE id_slug = ?", "TRK-1")
	if err := row.Scan(&trackerType); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if trackerType != nil {
		t.Errorf("tracker_type = %v, want nil (NULL)", trackerType)
	}
}

func TestInsertIssueWithBranch_trackerTypeRoundTrip(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	tt := "redmine"
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "TRK-2", Title: "Tracker issue", StatusID: 1, TrackerType: &tt},
		&Branch{UUID: "trk-uuid-2", Name: "TRK-2@feat@tracker-issue@trk-uuid-2", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var got *string
	row := s.db.QueryRow("SELECT tracker_type FROM issues WHERE id_slug = ?", "TRK-2")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got == nil {
		t.Fatal("tracker_type is nil, want non-nil")
	}
	if *got != "redmine" {
		t.Errorf("tracker_type = %q, want %q", *got, "redmine")
	}
}

func TestListBranchesByIssueSlugs_match(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "ABC-1", Title: "First", StatusID: 1},
		&Branch{UUID: "uuid-s1", Name: "ABC-1@feat@first@uuid-s1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "ABC-2", Title: "Second", StatusID: 1},
		&Branch{UUID: "uuid-s2", Name: "ABC-2@fix@second@uuid-s2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := s.ListBranchesByIssueSlugs(t.Context(), []string{"ABC-1", "ABC-2", "ABC-99"})
	if err != nil {
		t.Fatalf("ListBranchesByIssueSlugs: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d entries, want 2", len(result))
	}
	if b, ok := result["ABC-1"]; !ok {
		t.Error("missing ABC-1")
	} else if b.BranchName != "ABC-1@feat@first@uuid-s1" {
		t.Errorf("BranchName = %q, want %q", b.BranchName, "ABC-1@feat@first@uuid-s1")
	}
	if _, ok := result["ABC-2"]; !ok {
		t.Error("missing ABC-2")
	}
	if _, ok := result["ABC-99"]; ok {
		t.Error("unexpected ABC-99 in result")
	}
}

func TestListBranchesByIssueSlugs_emptyInput(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	result, err := s.ListBranchesByIssueSlugs(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListBranchesByIssueSlugs: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d entries, want 0", len(result))
	}
}

func TestListBranches_includesIssueID(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "X-1", Title: "IssueID test", StatusID: 1},
		&Branch{UUID: "uuid-x1", Name: "X-1@feat@issueid@uuid-x1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	if len(rows) == 0 {
		t.Fatal("expected at least one branch row")
	}

	if rows[0].IssueID <= 0 {
		t.Errorf("IssueID = %d, want > 0", rows[0].IssueID)
	}
}
