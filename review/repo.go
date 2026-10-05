package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/piprim/git-zf/git"
)

// ErrLegacyReview is returned for a review ref that points at a JSON blob, the
// format of git-zf before reviews were commit chains. Such a review is not
// migrated: `git zf review request` replaces it.
var ErrLegacyReview = errors.New("review ref uses the old blob format")

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

// Load reads the review chain of slug and folds it. It returns (nil, nil) when
// the issue has no review, and ErrLegacyReview when the ref is a blob.
// Malformed commits are skipped and named in State.Warnings.
func Load(ctx context.Context, c *git.Client, slug string) (*State, error) {
	kind, err := c.ChainRefKind(ctx, git.ReviewRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read review %s: %w", slug, err)
	}

	switch kind {
	case git.ChainAbsent:
		return nil, nil
	case git.ChainLegacy:
		return nil, fmt.Errorf("review %s: %w", slug, ErrLegacyReview)
	}

	commits, err := c.ReadChainCommits(ctx, git.ReviewRefs, slug)
	if err != nil {
		return nil, fmt.Errorf("read review %s: %w", slug, err)
	}

	st := foldCommits(slug, commits)

	return &st, nil
}

// foldCommits decodes the commits of slug's chain and folds them. Malformed
// commits are skipped and named in State.Warnings.
func foldCommits(slug string, commits []git.ChainCommit) State {
	ops := make([]Op, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		op, ok := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: review %s: skipping malformed op %s", slug, commit.ID))
		}
		ops = append(ops, op)
	}

	st := Fold(slug, ops)
	st.Warnings = warnings

	return st
}

// List loads every local review, in slug order. A ref in the old blob format
// is skipped; warnings names it, along with every malformed op met on the way.
func List(ctx context.Context, c *git.Client) (states []State, warnings []string, err error) {
	chains, legacy, err := c.ReadAllChains(ctx, git.ReviewRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list reviews: %w", err)
	}

	for _, slug := range legacy {
		warnings = append(warnings, fmt.Sprintf(
			"WARN: skipping review ref %s: %v; run `git zf review request` to restart it, "+
				"or `git update-ref -d refs/zf/reviews/%s` to drop the local copy", slug, ErrLegacyReview, slug))
	}

	states = make([]State, 0, len(chains))
	for _, slug := range slices.Sorted(maps.Keys(chains)) {
		st := foldCommits(slug, chains[slug])
		warnings = append(warnings, st.Warnings...)
		states = append(states, st)
	}

	return states, warnings, nil
}

// Append writes op on the review chain of slug, creating the chain when the
// issue has no review yet. V, At and Author are filled in here. sign forces a
// signature even when commit.gpgsign is off. Nothing is pushed. Callers Sync
// first, so that an op is not written beside a chain the remote already has.
func Append(ctx context.Context, c *git.Client, slug string, op *Op, sign bool) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	kind, err := c.ChainRefKind(ctx, git.ReviewRefs, slug)
	if err != nil {
		return fmt.Errorf("read review %s: %w", slug, err)
	}

	switch kind {
	case git.ChainLegacy:
		return fmt.Errorf("review %s: %w", slug, ErrLegacyReview)
	case git.ChainAbsent:
		root, err := c.WriteChainRoot(ctx, payload, op.Type, sign)
		if err != nil {
			return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
		}
		if err := c.PublishChainRoot(ctx, git.ReviewRefs, slug, root); err != nil {
			return fmt.Errorf("create review %s: %w", slug, err)
		}

		return nil
	}

	if _, err := c.AppendChainCommit(ctx, git.ReviewRefs, slug, payload, op.Type, sign); err != nil {
		return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
	}

	return nil
}

// ReplaceLegacyWith replaces the blob review ref of slug by a new chain whose
// root is op. The root is written first: when that fails (a signing error) the
// blob is still in place, locally and on the remote. Then the blob ref is
// deleted locally, and on the remote when the last fetch saw the blob there and
// the remote still holds it, and the chain is published. Until the remote blob
// is gone a push of the chain is rejected. Nothing is pushed.
func ReplaceLegacyWith(ctx context.Context, c *git.Client, slug string, op *Op, sign bool) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	root, err := c.WriteChainRoot(ctx, payload, op.Type, sign)
	if err != nil {
		return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
	}

	// Only a blob seen on the remote is deleted there, and only if it is still
	// the one seen: a chain another clone pushed since is left alone.
	_, remoteBlob, err := c.LegacyBlob(ctx, git.ReviewRefs, slug)
	if err != nil {
		return fmt.Errorf("replace legacy review %s: %w", slug, err)
	}

	if err := c.DeleteChainRef(ctx, git.ReviewRefs, slug, remoteBlob); err != nil {
		return fmt.Errorf("replace legacy review %s: %w", slug, err)
	}

	if err := c.PublishChainRoot(ctx, git.ReviewRefs, slug, root); err != nil {
		return fmt.Errorf("create review %s: %w", slug, err)
	}

	return nil
}

// mergePayload is the op.json of the commit that joins two diverged chains.
func mergePayload(ctx context.Context, c *git.Client) ([]byte, error) {
	return marshalOp(ctx, c, &Op{Type: OpMerge})
}

// Fetch fetches the remote's review chains and reconciles the local ones with
// them, merging diverged chains. silent prints nothing, for use in hooks.
// No-op without a remote.
//
// A failed fetch still reconciles: a review lock must not go unseen for lack
// of network. The fetch error is returned after.
func Fetch(ctx context.Context, c *git.Client, silent bool) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	return c.FetchChains(ctx, git.ReviewRefs, payload, silent) //nolint:wrapcheck // names the family already
}

// Push pushes the review chain of slug. A rejected push (someone pushed
// first) triggers one fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, slug string) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	return c.PushChain(ctx, git.ReviewRefs, slug, payload) //nolint:wrapcheck // names the family already
}

// Sync fetches and reconciles every review, then pushes the chains the remote
// does not have yet: an op whose push failed earlier goes out here. No-op
// without a remote.
func Sync(ctx context.Context, c *git.Client) error {
	payload, err := mergePayload(ctx, c)
	if err != nil {
		return err
	}

	return c.SyncChains(ctx, git.ReviewRefs, payload) //nolint:wrapcheck // names the family already
}

// What SignatureState reports for an op commit.
const (
	SigVerified   = "verified"
	SigUnverified = "signed, not verified"
	SigNone       = "unsigned"
)

// SignatureState tells whether the op commit carries a signature git trusts
// (SigVerified), a signature git cannot vouch for (SigUnverified), or none
// (SigNone).
func SignatureState(ctx context.Context, c *git.Client, commit string) string {
	if c.VerifyCommit(ctx, commit) == nil {
		return SigVerified
	}
	if signed, _ := c.CommitSigned(ctx, commit); signed {
		return SigUnverified
	}

	return SigNone
}
