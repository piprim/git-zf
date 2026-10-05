package branch

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/piprim/git-zf/git"
)

// ErrLegacyBranch is returned for a branch ref that points at a JSON blob, the
// format of git-zf before branch refs were commit chains. Such a ref is not
// migrated: `git zf issue start` or `git zf issue track` replaces it.
var ErrLegacyBranch = errors.New("branch ref uses the old blob format")

// marshalOp fills the fields every op written by this clone shares (V, At,
// Author) and returns the op.json bytes.
func marshalOp(ctx context.Context, c *git.Client, op *Op) ([]byte, error) {
	author, _ := c.ConfigUser(ctx)
	op.V, op.At, op.Author = OpVersion, time.Now().UTC().Format(time.RFC3339), author

	// The author is "Name <email>": keep the angle brackets readable.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(op); err != nil {
		return nil, fmt.Errorf("marshal %s op: %w", op.Type, err)
	}

	return bytes.TrimSpace(buf.Bytes()), nil
}

// mergePayload is the op.json of the commit that joins two diverged chains.
func mergePayload(ctx context.Context, c *git.Client) ([]byte, error) {
	return marshalOp(ctx, c, &Op{Type: OpMerge})
}

// warnf prints a warning on the client's stderr, when it has one.
func warnf(c *git.Client, format string, args ...any) {
	if io := c.IO(); io != nil {
		fmt.Fprintf(io.Err, format+"\n", args...)
	}
}

// foldCommits decodes the commits of slug's chain and folds them. Malformed
// commits are skipped and named in State.Warnings.
func foldCommits(slug string, commits []git.ChainCommit) State {
	ops := make([]Op, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		op, ok := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: branches of %s: skipping malformed op %s", slug, commit.ID))
		}
		ops = append(ops, op)
	}

	st := Fold(slug, ops)
	st.Warnings = warnings

	return st
}

// Load reads the branch chain of issue slug and folds it. It returns
// (nil, nil) when the issue has no chain, and ErrLegacyBranch when the ref is
// a blob.
func Load(ctx context.Context, c *git.Client, slug string) (*State, error) {
	kind, err := c.ChainRefKind(ctx, git.BranchRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read branches of %s: %w", slug, err)
	}

	switch kind {
	case git.ChainAbsent:
		return nil, nil
	case git.ChainLegacy:
		return nil, fmt.Errorf("branches of %s: %w", slug, ErrLegacyBranch)
	}

	commits, err := c.ReadChainCommits(ctx, git.BranchRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read branches of %s: %w", slug, err)
	}

	st := foldCommits(slug, commits)

	return &st, nil
}

// List loads every local branch chain, in slug order. Refs in the old blob
// format are skipped and named in one warning; warnings also lists every
// malformed op met on the way.
func List(ctx context.Context, c *git.Client) (states []State, warnings []string, err error) {
	chains, legacy, err := c.ReadAllChains(ctx, git.BranchRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list branch refs: %w", err)
	}

	if len(legacy) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"WARN: %d branch ref(s) in the old blob format are ignored (%s): "+
				"run `git zf issue track` on each branch still in progress; see docs/branch-refs.md",
			len(legacy), strings.Join(legacy, ", ")))
	}

	states = make([]State, 0, len(chains))
	for _, slug := range slices.Sorted(maps.Keys(chains)) {
		st := foldCommits(slug, chains[slug])
		warnings = append(warnings, st.Warnings...)
		states = append(states, st)
	}

	return states, warnings, nil
}

// ListRows lists the tracked branches in status (StatusAll for every one),
// newest first. It reconciles first, so that chains a plain `git fetch`
// brought are listed without contacting the remote. Warnings go to the
// client's stderr.
func ListRows(ctx context.Context, c *git.Client, status string) ([]Row, error) {
	if err := Reconcile(ctx, c); err != nil {
		warnf(c, "warning: reconcile branch refs: %v", err)
	}

	states, warnings, err := List(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		warnf(c, "%s", w)
	}

	return Rows(states, status), nil
}

// Find returns the state and the entry of the tracked branch called name. Both
// are nil when name is not a git-zf branch name or the branch is not tracked;
// a ref in the old blob format counts as not tracked.
func Find(ctx context.Context, c *git.Client, name string) (*State, *Entry, error) {
	b, err := Parse(name)
	if err != nil {
		return nil, nil, nil //nolint:nilerr // not a git-zf branch name: not tracked
	}

	st, err := Load(ctx, c, b.IssueID())
	if (err == nil && st == nil) || errors.Is(err, ErrLegacyBranch) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	e := st.Entry(name)
	if e == nil {
		return nil, nil, nil
	}

	return st, e, nil
}

// appendOp writes op on the existing chain of slug.
func appendOp(ctx context.Context, c *git.Client, slug string, op *Op) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	if _, err := c.AppendChainCommit(ctx, git.BranchRefs, slug, payload, op.Type, false); err != nil {
		return fmt.Errorf("write %s op on branches of %s: %w", op.Type, slug, err)
	}

	return nil
}

// legacyRef is what a branch ref in the old blob format carries that a new
// chain keeps.
type legacyRef struct {
	ParentSlug  string `json:"parent_slug"`
	TrackerType string `json:"tracker_type"`
	IssueID     string `json:"issue_id"`
}

// Start records that the branch op.Branch of issue slug is in progress. It
// fetches first (best-effort), so that an op is not written beside a chain the
// remote already has. Then:
//
//   - the chain does not know the branch: a start op is appended;
//   - the chain knows it as merged or closed: a set_status reopens it;
//   - the chain knows it as in progress: nothing is written;
//   - the issue has no chain: op becomes the chain's root;
//   - the ref is a blob left by an older git-zf: op becomes the root of a new
//     chain that replaces it, taking the blob's parent, tracker type and issue
//     ID where op has none. The root is written first, so a failure leaves the
//     blob in place. The blob is deleted on the remote only when the fetch saw
//     it there and the remote still holds it.
//
// Nothing is pushed.
func Start(ctx context.Context, c *git.Client, slug string, op *Op) error {
	if err := Fetch(ctx, c); err != nil {
		warnf(c, "warning: fetch branch refs: %v", err)
	}

	op.Type = OpStart

	kind, err := c.ChainRefKind(ctx, git.BranchRefs, slug)
	if err != nil {
		return fmt.Errorf("read branches of %s: %w", slug, err)
	}

	remoteBlob := ""

	switch kind {
	case git.ChainCommits:
		st, err := Load(ctx, c, slug)
		if err != nil {
			return err
		}

		switch e := st.Entry(op.Branch); {
		case e == nil:
			return appendOp(ctx, c, slug, op)
		case e.Status != StatusInProgress:
			return SetStatus(ctx, c, slug, op.Branch, StatusInProgress)
		}

		return nil
	case git.ChainLegacy:
		var content []byte
		if content, remoteBlob, err = c.LegacyBlob(ctx, git.BranchRefs, slug); err != nil {
			return fmt.Errorf("read legacy branch ref %s: %w", slug, err)
		}

		var old legacyRef
		_ = json.Unmarshal(content, &old) // an unreadable blob has nothing to carry over
		op.Parent = cmp.Or(op.Parent, old.ParentSlug)
		op.TrackerType = cmp.Or(op.TrackerType, old.TrackerType)
		op.IssueID = cmp.Or(op.IssueID, old.IssueID)
	}

	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	root, err := c.WriteChainRoot(ctx, payload, op.Type, false)
	if err != nil {
		return fmt.Errorf("write start op on branches of %s: %w", slug, err)
	}

	if kind == git.ChainLegacy {
		if err := c.DeleteChainRef(ctx, git.BranchRefs, slug, remoteBlob); err != nil {
			return fmt.Errorf("replace legacy branch ref %s: %w", slug, err)
		}
	}

	if err := c.PublishChainRoot(ctx, git.BranchRefs, slug, root); err != nil {
		return fmt.Errorf("create branch ref %s: %w", slug, err)
	}

	return nil
}

// SetStatus records the new status of branchName, a branch the chain of slug
// already knows. Nothing is pushed.
func SetStatus(ctx context.Context, c *git.Client, slug, branchName, status string) error {
	return appendOp(ctx, c, slug, &Op{Type: OpSetStatus, Branch: branchName, Status: status})
}

// Fetch fetches the remote's branch chains and reconciles the local ones with
// them, merging diverged chains. A local blob ref is replaced by the remote
// chain when the remote has one. No-op without a remote.
func Fetch(ctx context.Context, c *git.Client) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	return c.FetchChains(ctx, git.BranchRefs, payload, false) //nolint:wrapcheck // names the family already
}

// Reconcile brings the local chains up to date with what the last fetch saw
// on the remote, merging diverged chains. It does not contact the remote.
// No-op without a remote.
func Reconcile(ctx context.Context, c *git.Client) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	if _, err := c.ReconcileChainRefs(ctx, git.BranchRefs, payload); err != nil {
		return fmt.Errorf("reconcile branches: %w", err)
	}

	return nil
}

// Push pushes the branch chain of slug. A rejected push (someone pushed
// first) triggers one fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, slug string) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	err = c.PushChain(ctx, git.BranchRefs, slug, payload)
	if err == nil {
		return nil
	}

	// An older git-zf may have force-pushed a blob over the remote chain: no
	// push can succeed until that blob is gone.
	if _, remoteBlob, _ := c.LegacyBlob(ctx, git.BranchRefs, slug); remoteBlob != "" {
		remote, _ := c.Remote()

		return fmt.Errorf("%w\nthe remote holds this branch ref in the old blob format; delete it with: "+
			"git push %s --delete refs/zf/branches/%s", err, remote, slug)
	}

	return err //nolint:wrapcheck // names the family already
}

// Sync fetches and reconciles every branch chain, then pushes the ones the
// remote does not have yet: an op whose push failed earlier goes out here.
// No-op without a remote.
func Sync(ctx context.Context, c *git.Client) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	return c.SyncChains(ctx, git.BranchRefs, payload) //nolint:wrapcheck // names the family already
}
