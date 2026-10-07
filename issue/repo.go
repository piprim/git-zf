package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
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

// Prepare writes the create op of a new issue and returns the issue's ID
// without making the issue exist: no ref points at it until Publish. It lets a
// caller know the ID (to name a branch) before committing to the issue. A
// prepared issue that is never published leaves nothing behind. in.Labels is
// not used: labels are ops on a published issue.
func Prepare(ctx context.Context, c *git.Client, in NewIssue) (string, error) {
	payload, err := marshalOp(ctx, c, &Op{
		Type: OpCreate, Title: in.Title, Description: in.Description, BranchType: in.BranchType,
	})
	if err != nil {
		return "", err
	}

	id, err := c.WriteChainRoot(ctx, payload, OpCreate, false)
	if err != nil {
		return "", fmt.Errorf("prepare issue: %w", err)
	}

	return id, nil
}

// Publish makes the issue prepared under id exist. Nothing is pushed.
func Publish(ctx context.Context, c *git.Client, id string) error {
	if err := c.PublishChainRoot(ctx, git.IssueRefs, id, id); err != nil {
		return fmt.Errorf("publish issue %s: %w", id, err)
	}

	return nil
}

// Create writes a new issue (a create op, then one add_label op per label)
// and returns its record. Nothing is pushed.
func Create(ctx context.Context, c *git.Client, in NewIssue) (Record, error) {
	id, err := Prepare(ctx, c, in)
	if err != nil {
		return Record{}, err
	}

	if err := Publish(ctx, c, id); err != nil {
		return Record{}, err
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

	if _, err := c.AppendChainCommit(ctx, git.IssueRefs, id, payload, op.Type, false); err != nil {
		return fmt.Errorf("write %s op on issue %s: %w", op.Type, id, err)
	}

	return nil
}

// fold decodes and folds the commits of chain id. ok is false when none of
// them is a root named id: the ref does not name its chain's root. Malformed
// commits are skipped and named in warnings and in Record.Warnings.
func fold(id string, commits []git.ChainCommit) (rec Record, warnings []string, ok bool) {
	ops := make([]Op, 0, len(commits))
	for _, commit := range commits {
		if commit.ID == id && len(commit.Parents) == 0 {
			ok = true
		}

		op, decoded := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !decoded {
			warnings = append(warnings, fmt.Sprintf("WARN: issue %s: skipping malformed op %s", id, commit.ID))
		}
		ops = append(ops, op)
	}

	if !ok {
		return Record{}, nil, false
	}

	rec = Fold(id, ops)
	rec.Warnings = warnings

	return rec, warnings, true
}

// Load reads the chain of issue id and folds it. Malformed commits are skipped
// and named in Record.Warnings.
func Load(ctx context.Context, c *git.Client, id string) (Record, error) {
	commits, err := c.ReadChainCommits(ctx, git.IssueRefs, id)
	if err != nil {
		return Record{}, fmt.Errorf("read issue %s: %w", id, err)
	}

	rec, _, ok := fold(id, commits)
	if !ok {
		return Record{}, fmt.Errorf("read issue %s: %w", id, git.ErrIssueRefCorrupt)
	}

	return rec, nil
}

// List loads every local issue, newest first, in three git processes whatever
// their number. A ref that is not a commit chain or does not name its chain's
// root is skipped; warnings names it, along with every malformed op met on
// the way.
func List(ctx context.Context, c *git.Client) (records []Record, warnings []string, err error) {
	chains, legacy, err := c.ReadAllChains(ctx, git.IssueRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list issues: %w", err)
	}

	for _, id := range legacy {
		warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: not a commit chain", id))
	}

	// Sorted IDs keep the order of issues created in the same second stable.
	ids := slices.Sorted(maps.Keys(chains))

	records = make([]Record, 0, len(chains))
	for _, id := range ids {
		rec, w, ok := fold(id, chains[id])
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: %v", id, git.ErrIssueRefCorrupt))

			continue
		}

		warnings = append(warnings, w...)
		records = append(records, rec)
	}

	slices.SortStableFunc(records, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return records, warnings, nil
}

// trackerNumberRe matches what GitHub, Forgejo and Redmine use as issue IDs.
var trackerNumberRe = regexp.MustCompile(`^[0-9]+$`)

// Resolve finds the issue designated by query: a full ID, the number of the
// tracker issue it is mirrored with, or a unique ID prefix of at least 4
// characters.
func Resolve(ctx context.Context, c *git.Client, query string) (Record, error) {
	ids, err := c.ListChainIDs(ctx, git.IssueRefs)
	if err != nil {
		return Record{}, fmt.Errorf("list issues: %w", err)
	}

	if slices.Contains(ids, query) {
		return Load(ctx, c, query)
	}

	// An exact tracker number wins over an ID prefix made of digits.
	// ponytail: loads every issue to find one number; keep an index of the
	// numbers if repositories with thousands of issues make this slow.
	if trackerNumberRe.MatchString(query) {
		records, _, err := List(ctx, c)
		if err != nil {
			return Record{}, err
		}
		for i := range records {
			if records[i].Tracker != nil && records[i].Tracker.ID == query {
				return records[i], nil
			}
		}
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
	if err := c.FetchChainRefs(ctx, git.IssueRefs, false); err != nil {
		return 0, fmt.Errorf("fetch issues: %w", err)
	}

	payload, err := marshalOp(ctx, c, &Op{Type: OpMerge})
	if err != nil {
		return 0, err
	}

	merged, err = c.ReconcileChainRefs(ctx, git.IssueRefs, payload)
	if err != nil {
		return merged, fmt.Errorf("reconcile issues: %w", err)
	}

	return merged, nil
}

// Push pushes issue id. See PushAll.
func Push(ctx context.Context, c *git.Client, id string) error {
	return PushAll(ctx, c, []string{id})
}

// PushAll pushes the issues ids in one git push. A rejected push (someone
// pushed first) triggers one fetch, merge and retry. No-op without a remote
// or without ids.
func PushAll(ctx context.Context, c *git.Client, ids []string) error {
	firstErr := c.PushChainRefs(ctx, git.IssueRefs, ids)
	if firstErr == nil {
		return nil
	}

	if _, err := Fetch(ctx, c); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushChainRefs(ctx, git.IssueRefs, ids); err != nil {
		return fmt.Errorf("after merge: %w", err)
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

	ids, err := c.ListChainIDs(ctx, git.IssueRefs)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}

	for _, id := range ids {
		pushed, err := c.ChainRefPushed(ctx, git.IssueRefs, id)
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
