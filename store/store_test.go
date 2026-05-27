package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
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
	tables := []string{"statuses", "issues", "branches", "command_history"}
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

	t.Run("persists branch and issue rows", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		issue := Issue{IDSlug: "ABC-42", Title: "Add OAuth Login", StatusID: 1}
		branch := Branch{
			Name:     "ABC-42@feat@add-oauth-login@550e8400",
			Type:     "feat",
			StatusID: 1,
		}

		if err := s.InsertIssueWithBranch(t.Context(), &issue, &branch); err != nil {
			t.Fatalf("InsertIssueWithBranch: %v", err)
		}

		// Verify the branch row exists.
		var name string
		row := s.db.QueryRow("SELECT name FROM branches WHERE name = ?", "ABC-42@feat@add-oauth-login@550e8400")
		if err := row.Scan(&name); err != nil {
			t.Fatalf("branch not found: %v", err)
		}
		if name != "ABC-42@feat@add-oauth-login@550e8400" {
			t.Errorf("name = %q, want %q", name, "ABC-42@feat@add-oauth-login@550e8400")
		}

		// Verify the issue row exists.
		var idSlug string
		row = s.db.QueryRow("SELECT id_slug FROM issues WHERE id = (SELECT issue_id FROM branches WHERE name = ?)", "ABC-42@feat@add-oauth-login@550e8400")
		if err := row.Scan(&idSlug); err != nil {
			t.Fatalf("issue not found: %v", err)
		}
		if idSlug != "ABC-42" {
			t.Errorf("id_slug = %q, want %q", idSlug, "ABC-42")
		}
	})

	t.Run("stores NULL for tracker type when not set", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "TRK-1", Title: "Pre-tracker issue", StatusID: 1},
			&Branch{Name: "TRK-1@feat@pre-tracker@trk-uuid-1", Type: "feat", StatusID: 1},
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
	})

	t.Run("round-trips a non-nil tracker type", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		tt := "redmine"
		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "TRK-2", Title: "Tracker issue", StatusID: 1, TrackerType: &tt},
			&Branch{Name: "TRK-2@feat@tracker-issue@trk-uuid-2", Type: "feat", StatusID: 1},
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
	})
}

func TestListBranches(t *testing.T) {
	t.Parallel()

	t.Run("returns all branches regardless of status", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "A-1", Title: "First", StatusID: 1},
			&Branch{Name: "A-1@feat@first@uuid-1", Type: "feat", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "A-2", Title: "Second", StatusID: 1},
			&Branch{Name: "A-2@fix@second@uuid-2", Type: "fix", StatusID: 1},
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
	})

	t.Run("filters to in-progress branches only", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "B-1", Title: "In progress issue", StatusID: 1},
			&Branch{Name: "B-1@feat@in-progress@uuid-ip", Type: "feat", StatusID: 1},
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
	})

	t.Run("filters to merged branches only", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "C-1", Title: "Merged issue", StatusID: 1},
			&Branch{Name: "C-1@feat@merged@uuid-mg", Type: "feat", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}

		now := time.Now()
		if err := s.UpdateBranchStatus(t.Context(), "C-1@feat@merged@uuid-mg", 2, &now); err != nil {
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
	})

	t.Run("returns empty slice for an empty store", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		rows, err := s.ListBranches(t.Context(), BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})

	t.Run("returns branches ordered by created_at DESC", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		// Insert two branches with explicit created_at values to test DESC ordering.
		for _, row := range []struct {
			slug, name, tp, dt string
		}{
			{"D-1", "D-1@feat@old@uuid-old", "feat", "2025-01-01 00:00:00"},
			{"D-2", "D-2@feat@new@uuid-new", "feat", "2025-06-01 00:00:00"},
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
				`INSERT INTO branches (name, issue_id, type, status_id, created_at)
				 VALUES (?, ?, ?, 1, ?)`,
				row.name, issueID, row.tp, row.dt,
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
	})

	t.Run("populates BranchName field", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "U-1", Title: "branch name test", StatusID: 1},
			&Branch{Name: "U-1@feat@branch-name-test@deadbeef", Type: "feat", StatusID: 1},
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
		if rows[0].BranchName != "U-1@feat@branch-name-test@deadbeef" {
			t.Errorf("BranchName = %q, want %q", rows[0].BranchName, "U-1@feat@branch-name-test@deadbeef")
		}
	})

	t.Run("includes IssueID in the result row", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "X-1", Title: "IssueID test", StatusID: 1},
			&Branch{Name: "X-1@feat@issueid@uuid-x1", Type: "feat", StatusID: 1},
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
	})
}

func TestUpdateBranchStatus_merged(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	issue := Issue{IDSlug: "ABC-1", Title: "Some issue", StatusID: 1}
	branch := Branch{Name: "ABC-1@fix@some-issue@aabbccdd", Type: "fix", StatusID: 1}
	if err := s.InsertIssueWithBranch(t.Context(), &issue, &branch); err != nil {
		t.Fatalf("InsertIssueWithBranch: %v", err)
	}

	// Updating to merged without merged_at must fail (trigger).
	if err := s.UpdateBranchStatus(t.Context(), "ABC-1@fix@some-issue@aabbccdd", 2, nil); err == nil {
		t.Error("expected error when merged_at is nil for merged status, got nil")
	}

	// Updating to merged with merged_at must succeed.
	now := time.Now()
	if err := s.UpdateBranchStatus(t.Context(), "ABC-1@fix@some-issue@aabbccdd", 2, &now); err != nil {
		t.Errorf("UpdateBranchStatus merged: %v", err)
	}

	var statusID int64
	row := s.db.QueryRow("SELECT status_id FROM branches WHERE name = ?", "ABC-1@fix@some-issue@aabbccdd")
	if err := row.Scan(&statusID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if statusID != 2 {
		t.Errorf("status_id = %d, want 2", statusID)
	}
}

func TestListBranchesByIssueSlugs(t *testing.T) {
	t.Parallel()

	t.Run("matches given slugs and ignores unknown ones", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "ABC-1", Title: "First", StatusID: 1},
			&Branch{Name: "ABC-1@feat@first@uuid-s1", Type: "feat", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}
		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "ABC-2", Title: "Second", StatusID: 1},
			&Branch{Name: "ABC-2@fix@second@uuid-s2", Type: "fix", StatusID: 1},
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
	})

	t.Run("returns empty map for nil input", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		result, err := s.ListBranchesByIssueSlugs(t.Context(), nil)
		if err != nil {
			t.Fatalf("ListBranchesByIssueSlugs: %v", err)
		}
		if len(result) != 0 {
			t.Errorf("got %d entries, want 0", len(result))
		}
	})
}

func TestDeleteBranch(t *testing.T) {
	t.Parallel()

	t.Run("removes the branch row from the store", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&Issue{IDSlug: "DEL-1", Title: "to delete", StatusID: 1},
			&Branch{Name: "DEL-1@fix@to-delete@cafebabe", Type: "fix", StatusID: 1},
		); err != nil {
			t.Fatalf("insert: %v", err)
		}

		if err := s.DeleteBranch(t.Context(), "DEL-1@fix@to-delete@cafebabe"); err != nil {
			t.Fatalf("DeleteBranch: %v", err)
		}

		rows, err := s.ListBranches(t.Context(), BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows after delete, want 0", len(rows))
		}
	})

	t.Run("returns error for a non-existent name", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.DeleteBranch(t.Context(), "no-such-branch@feat@missing@0000"); err == nil {
			t.Error("expected error for missing branch name, got nil")
		}
	})
}

func TestInsertCommandHistory(t *testing.T) {
	t.Parallel()

	t.Run("persists payload and retrieves it on list", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		payload := map[string]any{"type": "feat", "scope": "auth", "subject": "add OAuth"}

		if err := s.InsertCommandHistory(t.Context(), "commit", payload); err != nil {
			t.Fatalf("InsertCommandHistory: %v", err)
		}

		rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}

		var got map[string]any
		if err := json.Unmarshal(rows[0].Payload, &got); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got["type"] != "feat" {
			t.Errorf(`type = %q, want "feat"`, got["type"])
		}
		if got["scope"] != "auth" {
			t.Errorf(`scope = %q, want "auth"`, got["scope"])
		}
		if got["subject"] != "add OAuth" {
			t.Errorf(`subject = %q, want "add OAuth"`, got["subject"])
		}
	})
}

func TestListCommandHistory(t *testing.T) {
	t.Parallel()

	t.Run("returns entries newest first (by id desc)", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		for _, subj := range []string{"first", "second", "third"} {
			if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"subject": subj}); err != nil {
				t.Fatalf("insert %s: %v", subj, err)
			}
		}

		rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}

		var last map[string]any
		if err := json.Unmarshal(rows[0].Payload, &last); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		// ORDER BY created_at DESC, id DESC: in fast in-process tests all rows share the same
		// CURRENT_TIMESTAMP second, so id (AUTOINCREMENT) is the effective tiebreaker.
		// Inserting "first", "second", "third" gives "third" the highest id → it appears first.
		if last["subject"] != "third" {
			t.Errorf(`rows[0].subject = %q, want "third" (most recent insertion)`, last["subject"])
		}
	})

	t.Run("respects the limit parameter", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		for i := range 5 {
			if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"n": i}); err != nil {
				t.Fatalf("insert %d: %v", i, err)
			}
		}

		rows, err := s.ListCommandHistory(t.Context(), "commit", 3)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("got %d rows, want 3", len(rows))
		}
	})

	t.Run("isolates entries by command name", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"a": 1}); err != nil {
			t.Fatalf("insert commit: %v", err)
		}
		if err := s.InsertCommandHistory(t.Context(), "branch", map[string]any{"b": 2}); err != nil {
			t.Fatalf("insert branch: %v", err)
		}

		rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("got %d rows, want 1 (branch entry must not appear)", len(rows))
		}
	})

	t.Run("returns empty slice when history is empty", func(t *testing.T) {
		t.Parallel()

		s := openTestStore(t)

		rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
		if err != nil {
			t.Fatalf("ListCommandHistory: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d rows, want 0", len(rows))
		}
	})
}

func TestMigration0004BranchesNamePK(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()

	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	// PRAGMA user_version must equal the number of migration files.
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	t.Run("user_version matches migration count", func(t *testing.T) {
		entries, err := fs.ReadDir(migrationsFS, "migrations")
		if err != nil {
			t.Fatalf("read migrations dir: %v", err)
		}

		wantVersion := len(entries)
		if version != wantVersion {
			t.Errorf("user_version = %d, want %d", version, wantVersion)
		}
	})

	// The branches table must NOT have a uuid column and MUST have name as PK.
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(branches)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var hasUUID, namePK bool
	for rows.Next() {
		var (
			cid     int
			cname   string
			ctype   string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &cname, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if cname == "uuid" {
			hasUUID = true
		}
		if cname == "name" && pk == 1 {
			namePK = true
		}
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	if hasUUID {
		t.Error("uuid column is still present after migration 0004")
	}
	if !namePK {
		t.Error("name should be PRIMARY KEY after migration 0004")
	}

	// enforce_merged_at trigger must still be in place.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO issues (id_slug, title, status_id) VALUES (?, ?, ?)`,
		"ABC-1", "t", StatusIDInProgress,
	); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO branches (name, issue_id, type, status_id) VALUES (?, (SELECT id FROM issues WHERE id_slug = ?), ?, ?)`,
		"ABC-1@feat@x", "ABC-1", "feat", StatusIDInProgress,
	); err != nil {
		t.Fatalf("insert branch: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE branches SET status_id = ? WHERE name = ?`,
		StatusIDMerged, "ABC-1@feat@x",
	); err == nil {
		t.Error("expected enforce_merged_at to reject UPDATE without merged_at")
	}
}
