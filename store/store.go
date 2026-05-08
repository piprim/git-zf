package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	_ "modernc.org/sqlite" // Import for side-effect: registers sqlite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a SQLite database for branch and issue persistence.
type Store struct {
	db *sql.DB
}

// Issue represents a tracked issue record.
type Issue struct {
	ID int64
	// tracker string ID: "ABC-42", "42", …
	IDSlug      string
	Title       string
	StatusID    int64
	TrackerType *string // nil = manual entry; non-nil = tracker type (e.g. "redmine")
}

// Branch represents a tracked branch record.
type Branch struct {
	UUID      string
	Name      string
	IssueID   int64
	Type      string
	StatusID  int64
	CreatedAt time.Time
	MergedAt  *time.Time
}

// BranchStatus is the typed string representation of the statuses table.
type BranchStatus string

const (
	BranchStatusInProgress BranchStatus = "in_progress"
	BranchStatusMerged     BranchStatus = "merged"
	BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)

// StatusIDInProgress and StatusIDMerged are the integer primary keys for the
// seeded rows in the statuses table (see migrations/0001_initial.sql).
const (
	StatusIDInProgress int64 = 1
	StatusIDMerged     int64 = 2
)

// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
	UUID       string       `json:"uuid"`
	IssueID    int64        `json:"issue_id"`
	IssueSlug  string       `json:"issue_slug"`
	Title      string       `json:"title"`
	BranchName string       `json:"branch_name"`
	Type       string       `json:"type"`
	Status     BranchStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
}

// IssueRow is the unified display row for git zf issue list.
// It composes an issue identity with an optional local branch.
type IssueRow struct {
	IssueSlug     string     `json:"issue_slug"`
	Title         string     `json:"title"`
	Project       string     `json:"project"`        // tracker project / repo; empty when unknown
	TrackerStatus *string    `json:"tracker_status"` // nil → display "N.A."
	Branch        *BranchRow `json:"branch"`         // nil → not started locally
}

const dbName = "git-zf.db"

// Open opens (or creates) the SQLite database at dir/[dbName] and runs pending migrations.
func Open(ctx context.Context, dir string) (*Store, error) {
	path := filepath.Join(dir, dbName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	// Enable foreign keys.
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close db: %w", err)
	}

	return nil
}

// InsertIssueWithBranch inserts an issue and its linked branch in a single transaction.
func (s *Store) InsertIssueWithBranch(ctx context.Context, issue *Issue, branch *Branch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO issues (id_slug, title, status_id, tracker_type) VALUES (?, ?, ?, ?)`,
		issue.IDSlug, issue.Title, issue.StatusID, issue.TrackerType,
	)
	if err != nil {
		return fmt.Errorf("insert issue: %w", err)
	}

	issueID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO branches (uuid, name, issue_id, type, status_id) VALUES (?, ?, ?, ?, ?)`,
		branch.UUID, branch.Name, issueID, branch.Type, branch.StatusID,
	)
	if err != nil {
		return fmt.Errorf("insert branch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// UpdateBranchStatus updates a branch's status. mergedAt must be non-nil when statusID == 2 (merged).
func (s *Store) UpdateBranchStatus(ctx context.Context, uuid string, statusID int64, mergedAt *time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE branches SET status_id = ?, merged_at = ? WHERE uuid = ?`,
		statusID, mergedAt, uuid,
	)
	if err != nil {
		return fmt.Errorf("update branch status: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("update branch status: no branch with uuid %q", uuid)
	}

	return nil
}

// UpdateIssueStatus updates an issue's status.
func (s *Store) UpdateIssueStatus(ctx context.Context, issueID, statusID int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE issues SET status_id = ? WHERE id = ?`,
		statusID, issueID,
	)
	if err != nil {
		return fmt.Errorf("update issue status: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("update issue status: no issue with id %d", issueID)
	}

	return nil
}

// ListBranches returns all branches joined with their issue and status,
// ordered by created_at DESC. BranchStatusAll returns every row.
func (s *Store) ListBranches(ctx context.Context, status BranchStatus) ([]BranchRow, error) {
	q := `
		SELECT b.uuid, i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id`

	var args []any
	if status != BranchStatusAll {
		q += " WHERE st.name = ?"
		args = append(args, string(status))
	}

	q += " ORDER BY b.created_at DESC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list branches query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []BranchRow

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.UUID, &r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		result = append(result, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	if result == nil {
		result = []BranchRow{}
	}

	return result, nil
}

// ListBranchesByIssueSlugs returns a map[id_slug]BranchRow for the given slugs.
// Uses a single SELECT … WHERE i.id_slug IN (…). Returns an empty map for empty input.
func (s *Store) ListBranchesByIssueSlugs(ctx context.Context, slugs []string) (map[string]BranchRow, error) {
	if len(slugs) == 0 {
		return make(map[string]BranchRow), nil
	}

	q := `
SELECT b.uuid, i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
FROM branches b
JOIN issues i ON b.issue_id = i.id
JOIN statuses st ON b.status_id = st.id
WHERE i.id_slug IN (SELECT value FROM json_each(?))
ORDER BY b.created_at DESC`

	args, err := json.Marshal(slugs)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to json: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, q, string(args))
	if err != nil {
		return nil, fmt.Errorf("list branches by slugs query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]BranchRow)

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.UUID, &r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		if _, exists := result[r.IssueSlug]; !exists {
			result[r.IssueSlug] = r
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	return result, nil
}

// DeleteBranch removes the branch record identified by uuid.
func (s *Store) DeleteBranch(ctx context.Context, uuid string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM branches WHERE uuid = ?`, uuid)
	if err != nil {
		return fmt.Errorf("delete branch: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("delete branch: no branch with uuid %q", uuid)
	}

	return nil
}

// parseSQLiteTime parses the time string returned by modernc/sqlite for DATETIME columns.
// The driver may return either RFC3339 ("2006-01-02T15:04:05Z") or SQLite text
// ("2006-01-02 15:04:05") depending on whether the value originated from
// CURRENT_TIMESTAMP or a literal string INSERT.
func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unrecognised datetime format %q", s)
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}

	slices.Sort(names)

	for i, name := range names {
		if i < version {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration tx %s: %w", name, err)
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf("exec migration %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}

		// PRAGMA user_version does not support parameter binding; i is compile-time-bounded.
		// Executed after Commit because PRAGMA user_version is not transactional in SQLite.
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			return fmt.Errorf("bump user_version: %w", err)
		}
	}

	return nil
}
