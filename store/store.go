package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	"github.com/piprim/git-zf/internal/gitdir"
	_ "modernc.org/sqlite" // Import for side-effect: registers sqlite driver
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a SQLite database for branch and issue persistence.
type Store struct {
	db *sql.DB
}

// Issue is the SQLite persistence row for a work item — the durable record
// written once a branch exists. It is the last of three "Issue" shapes: a
// tracker.Issue (wire shape) becomes an issue.Issue (in-flow entity) and is
// finally persisted here. Unlike those, this carries a synthesised int64 PK, a
// normalised StatusID foreign key (not a status string), and a nil-able
// TrackerType to mark manual entries. See the doc on issue.Issue for the
// end-to-end flow.
type Issue struct {
	ID int64
	// tracker string ID: "ABC-42", "42", …
	IDSlug      string
	Title       string
	TrackerType *string // nil = manual entry; non-nil = tracker type (e.g. "redmine")
}

// Branch represents a tracked branch record. The branch's full git ref
// name is its primary key.
type Branch struct {
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
	BranchStatusClosed     BranchStatus = "closed"
	BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)

// StatusID* constants are the integer primary keys for the seeded rows in
// the statuses table (see migrations/).
const (
	StatusIDInProgress int64 = 1
	StatusIDMerged     int64 = 2
	StatusIDClosed     int64 = 3
)

// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
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
	// Labels and State are set for issues stored in the repository
	// (refs/zf/issues/*). State is "open" or "closed"; "" means the row has no
	// repo issue and its status is derived from the branch.
	Labels []string `json:"labels"`
	State  string   `json:"state"`
}

// CommandHistoryRow is one row from the command_history table.
type CommandHistoryRow struct {
	ID        int64
	Payload   json.RawMessage // raw JSON; caller unmarshals into the shape they need
	CreatedAt time.Time
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
		`INSERT INTO issues (id_slug, title, tracker_type) VALUES (?, ?, ?)`,
		issue.IDSlug, issue.Title, issue.TrackerType,
	)
	if err != nil {
		return fmt.Errorf("insert issue: %w", err)
	}

	issueID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO branches (name, issue_id, type, status_id) VALUES (?, ?, ?, ?)`,
		branch.Name, issueID, branch.Type, branch.StatusID,
	)
	if err != nil {
		return fmt.Errorf("insert branch: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}

// UpdateBranchStatus updates a branch's status. mergedAt must be non-nil
// when statusID == 2 (merged); the enforce_merged_at trigger rejects nil.
func (s *Store) UpdateBranchStatus(ctx context.Context, name string, statusID int64, mergedAt *time.Time) error {
	return s.execOne(ctx, "update branch status", fmt.Sprintf("branch with name %q", name),
		`UPDATE branches SET status_id = ?, merged_at = ? WHERE name = ?`,
		statusID, mergedAt, name)
}

// execOne runs a statement that must change at least one existing row. what
// names the operation in errors; missing names the row that was not found
// ("update branch status: no branch with name ...").
func (s *Store) execOne(ctx context.Context, what, missing, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("%s: no %s", what, missing)
	}

	return nil
}

// branchSelectBase is the shared SELECT + FROM/JOIN fragment for branch queries.
// Column order must match scanBranchRow.
const branchSelectBase = `
		SELECT i.id, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id`

// ListBranches returns all branches joined with their issue and status,
// ordered by created_at DESC. BranchStatusAll returns every row.
func (s *Store) ListBranches(ctx context.Context, status BranchStatus) ([]BranchRow, error) {
	q := branchSelectBase

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
		r, err := scanBranchRow(rows)
		if err != nil {
			return nil, err
		}

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

	q := branchSelectBase + `
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
		r, err := scanBranchRow(rows)
		if err != nil {
			return nil, err
		}

		if _, exists := result[r.IssueSlug]; !exists {
			result[r.IssueSlug] = r
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	return result, nil
}

// scanBranchRow scans one row from a branch query. Column order must match
// branchSelectBase: id, id_slug, title, name, type, status, created_at.
// created_at scans into a time.Time because the column is declared DATETIME:
// the driver parses the stored text (see TestStoredTimes).
func scanBranchRow(rows *sql.Rows) (BranchRow, error) {
	var r BranchRow

	if err := rows.Scan(
		&r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &r.CreatedAt,
	); err != nil {
		return r, fmt.Errorf("scan branch row: %w", err)
	}

	return r, nil
}

// DeleteBranch removes the branch record identified by name.
func (s *Store) DeleteBranch(ctx context.Context, name string) error {
	return s.execOne(ctx, "delete branch", fmt.Sprintf("branch with name %q", name),
		`DELETE FROM branches WHERE name = ?`, name)
}

// OpenRepo opens the local store inside the current git repository's common
// .git directory. Resolves it via gitdir.Common() so regular repos, submodules
// (where <worktree>/.git is a gitlink file), and linked worktrees all share one
// store: inside a linked worktree the per-worktree dir (.git/worktrees/<name>)
// would otherwise hold a separate, empty database.
func OpenRepo(ctx context.Context) (*Store, error) {
	d, err := gitdir.Common()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	s, err := Open(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
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

// InsertCommandHistory records one completed form submission.
// payload is marshalled to JSON internally; it must be JSON-serialisable.
func (s *Store) InsertCommandHistory(ctx context.Context, command string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO command_history (command, payload) VALUES (?, ?)`,
		command, string(data),
	)
	if err != nil {
		return fmt.Errorf("insert command history: %w", err)
	}

	return nil
}

// InsertIssueRelation records a parent-child relationship between two issue slugs.
// Silently succeeds if the relation already exists (idempotent).
func (s *Store) InsertIssueRelation(ctx context.Context, parentSlug, childSlug string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO issue_relations (parent_issue_slug, child_issue_slug) VALUES (?, ?)`,
		parentSlug, childSlug,
	)
	if err != nil {
		return fmt.Errorf("insert issue relation: %w", err)
	}
	return nil
}

// GetParentIssue returns the parent issue slug for childSlug, or "" if it has no parent.
func (s *Store) GetParentIssue(ctx context.Context, childSlug string) (string, error) {
	var parent string
	err := s.db.QueryRowContext(ctx,
		`SELECT parent_issue_slug FROM issue_relations WHERE child_issue_slug = ?`,
		childSlug,
	).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get parent issue: %w", err)
	}
	return parent, nil
}

// ListChildIssues returns the slugs of all direct children of parentSlug.
func (s *Store) ListChildIssues(ctx context.Context, parentSlug string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT child_issue_slug FROM issue_relations WHERE parent_issue_slug = ?`,
		parentSlug,
	)
	if err != nil {
		return nil, fmt.Errorf("list children query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("scan child slug: %w", err)
		}
		result = append(result, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate children: %w", err)
	}
	return result, nil
}

// ChildrenAllMerged reports whether every branch of every child issue of
// parentSlug has BranchStatusMerged. A child with no branches (not yet started)
// is treated as not merged. Returns true when parentSlug has no children.
//
// Uses a COUNT query per child to handle issues with multiple branches
// (e.g. created with --variant): all branches must be merged, not just one.
func (s *Store) ChildrenAllMerged(ctx context.Context, parentSlug string) (bool, error) {
	children, err := s.ListChildIssues(ctx, parentSlug)
	if err != nil {
		return false, err
	}
	if len(children) == 0 {
		return true, nil
	}

	for _, child := range children {
		var total, nonMerged int
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*),
			        COALESCE(SUM(CASE WHEN st.name != 'merged' THEN 1 ELSE 0 END), 0)
			 FROM branches b
			 JOIN issues i ON b.issue_id = i.id
			 JOIN statuses st ON b.status_id = st.id
			 WHERE i.id_slug = ?`,
			child,
		).Scan(&total, &nonMerged)
		if err != nil {
			return false, fmt.Errorf("check child %q branches: %w", child, err)
		}
		if total == 0 || nonMerged > 0 {
			return false, nil
		}
	}
	return true, nil
}

// ListCommandHistory returns the most recent limit entries for command, newest-first.
func (s *Store) ListCommandHistory(ctx context.Context, command string, limit int) ([]CommandHistoryRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, payload, created_at FROM command_history
		 WHERE command = ? ORDER BY created_at DESC, id DESC LIMIT ?`,
		command, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list command history query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []CommandHistoryRow

	for rows.Next() {
		var r CommandHistoryRow
		var payloadStr string

		if err := rows.Scan(&r.ID, &payloadStr, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan command history row: %w", err)
		}

		r.Payload = json.RawMessage(payloadStr)
		result = append(result, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate command history: %w", err)
	}

	if result == nil {
		result = []CommandHistoryRow{}
	}

	return result, nil
}
