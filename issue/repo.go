package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/piprim/git-zf/git"
)

// minPrefixLen is the shortest ID prefix Resolve accepts.
const minPrefixLen = 4

// marshalOp fills the fields every op written by this clone shares (V, At,
// Author) and returns the op.json bytes.
func marshalOp(ctx context.Context, c *git.Client, op *Op) ([]byte, error) {
	author, _ := c.ConfigUser(ctx)
	op.V, op.At, op.Author = OpVersion, time.Now().UTC().Format(time.RFC3339), author

	payload, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("marshal %s op: %w", op.Type, err)
	}

	return payload, nil
}

// NewIssue is the input of Create.
type NewIssue struct {
	Title       string
	Description string
	BranchType  string
	Labels      []string
}

// Create writes a new issue (a create op, then one add_label op per label)
// and returns its record. Nothing is pushed.
func Create(ctx context.Context, c *git.Client, in NewIssue) (Record, error) {
	payload, err := marshalOp(ctx, c, &Op{
		Type: OpCreate, Title: in.Title, Description: in.Description, BranchType: in.BranchType,
	})
	if err != nil {
		return Record{}, err
	}

	id, err := c.CreateIssueRef(ctx, payload, OpCreate)
	if err != nil {
		return Record{}, fmt.Errorf("create issue: %w", err)
	}

	for _, label := range in.Labels {
		if err := Append(ctx, c, id, &Op{Type: OpAddLabel, Value: label}); err != nil {
			return Record{}, err
		}
	}

	return Load(ctx, c, id)
}

// Append writes op on top of issue id. V, At and Author are filled in here.
// Nothing is pushed.
func Append(ctx context.Context, c *git.Client, id string, op *Op) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	if _, err := c.AppendIssueCommit(ctx, id, payload, op.Type); err != nil {
		return fmt.Errorf("write %s op on issue %s: %w", op.Type, id, err)
	}

	return nil
}

// Load reads the chain of issue id and folds it. Malformed commits are skipped
// and named in Record.Warnings.
func Load(ctx context.Context, c *git.Client, id string) (Record, error) {
	commits, err := c.ReadIssueCommits(ctx, id)
	if err != nil {
		return Record{}, fmt.Errorf("read issue %s: %w", id, err)
	}

	ops := make([]Op, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		op, ok := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: issue %s: skipping malformed op %s", id, commit.ID))
		}
		ops = append(ops, op)
	}

	rec := Fold(id, ops)
	rec.Warnings = warnings

	return rec, nil
}

// List loads every local issue, newest first. An unreadable ref is skipped;
// warnings names it, along with every malformed op met on the way.
func List(ctx context.Context, c *git.Client) (records []Record, warnings []string, err error) {
	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list issues: %w", err)
	}

	records = make([]Record, 0, len(ids))
	for _, id := range ids {
		rec, err := Load(ctx, c, id)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: %v", id, err))

			continue
		}

		warnings = append(warnings, rec.Warnings...)
		records = append(records, rec)
	}

	slices.SortStableFunc(records, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return records, warnings, nil
}

// Resolve finds the issue designated by query: a full ID, or a unique ID
// prefix of at least 4 characters.
func Resolve(ctx context.Context, c *git.Client, query string) (Record, error) {
	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("list issues: %w", err)
	}

	if slices.Contains(ids, query) {
		return Load(ctx, c, query)
	}

	if len(query) < minPrefixLen {
		return Record{}, fmt.Errorf("issue %q: %w (use at least %d characters of the ID)",
			query, git.ErrIssueNotFound, minPrefixLen)
	}

	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, query) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return Record{}, fmt.Errorf("issue %q: %w", query, git.ErrIssueNotFound)
	case 1:
		return Load(ctx, c, matches[0])
	default:
		lines := make([]string, 0, len(matches))
		for _, id := range matches {
			title := "(unreadable)"
			if rec, err := Load(ctx, c, id); err == nil {
				title = rec.Title
			}
			lines = append(lines, "  "+id+"  "+title)
		}

		return Record{}, fmt.Errorf("issue ID %q is ambiguous:\n%s", query, strings.Join(lines, "\n"))
	}
}

// Fetch fetches the remote's issue refs and reconciles the local ones with
// them, merging diverged chains. Returns the number of merges. No-op without
// a remote.
func Fetch(ctx context.Context, c *git.Client) (merged int, err error) {
	if err := c.FetchIssueRefs(ctx); err != nil {
		return 0, fmt.Errorf("fetch issues: %w", err)
	}

	payload, err := marshalOp(ctx, c, &Op{Type: OpMerge})
	if err != nil {
		return 0, err
	}

	merged, err = c.ReconcileIssueRefs(ctx, payload)
	if err != nil {
		return merged, fmt.Errorf("reconcile issues: %w", err)
	}

	return merged, nil
}

// Push pushes issue id. A rejected push (someone pushed first) triggers one
// fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, id string) error {
	firstErr := c.PushIssueRef(ctx, id)
	if firstErr == nil {
		return nil
	}

	if _, err := Fetch(ctx, c); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushIssueRef(ctx, id); err != nil {
		return fmt.Errorf("push issue %s after merge: %w", id, err)
	}

	return nil
}

// SyncResult summarizes one Sync.
type SyncResult struct {
	Merged int      // diverged chains merged
	Pushed int      // issues pushed
	Failed []string // one line per issue that could not be pushed
}

// Sync fetches and reconciles every issue, then pushes the ones the remote
// does not have yet. No-op without a remote.
func Sync(ctx context.Context, c *git.Client) (SyncResult, error) {
	var res SyncResult

	merged, err := Fetch(ctx, c)
	if err != nil {
		return res, err
	}
	res.Merged = merged

	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}

	for _, id := range ids {
		pushed, err := c.IssueRefPushed(ctx, id)
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", id, err))

			continue
		}
		if pushed {
			continue
		}

		if err := Push(ctx, c, id); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", id, err))

			continue
		}
		res.Pushed++
	}

	return res, nil
}
