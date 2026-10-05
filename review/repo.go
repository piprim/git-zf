package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	return &st, nil
}

// List loads every local review, in slug order. An unreadable ref is skipped;
// warnings names it, along with every malformed op met on the way.
//
// ponytail: three git processes per review, closed ones included. Fine to a
// few hundred reviews; batch all chains through one `git log --stdin` if
// listing gets slow.
func List(ctx context.Context, c *git.Client) (states []State, warnings []string, err error) {
	slugs, err := c.ListChainIDs(ctx, git.ReviewRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("list reviews: %w", err)
	}

	states = make([]State, 0, len(slugs))
	for _, slug := range slugs {
		st, err := Load(ctx, c, slug)
		if errors.Is(err, ErrLegacyReview) {
			warnings = append(warnings, fmt.Sprintf(
				"WARN: skipping review ref %s: %v; run `git zf review request` to restart it, "+
					"or `git update-ref -d refs/zf/reviews/%s` to drop the local copy", slug, err, slug))

			continue
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping review ref %s: %v", slug, err))

			continue
		}
		if st == nil {
			continue
		}

		warnings = append(warnings, st.Warnings...)
		states = append(states, *st)
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
// deleted, locally and on the remote, and the chain published. Until the remote
// blob is gone a push of the chain is rejected. Nothing is pushed.
func ReplaceLegacyWith(ctx context.Context, c *git.Client, slug string, op *Op, sign bool) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	root, err := c.WriteChainRoot(ctx, payload, op.Type, sign)
	if err != nil {
		return fmt.Errorf("write %s op on review %s: %w", op.Type, slug, err)
	}

	if err := c.DeleteChainRef(ctx, git.ReviewRefs, slug); err != nil {
		return fmt.Errorf("replace legacy review %s: %w", slug, err)
	}

	if err := c.PublishChainRoot(ctx, git.ReviewRefs, slug, root); err != nil {
		return fmt.Errorf("create review %s: %w", slug, err)
	}

	return nil
}

// Fetch fetches the remote's review chains and reconciles the local ones with
// them, merging diverged chains. silent prints nothing, for use in hooks.
// No-op without a remote.
//
// A failed fetch still reconciles: the tracking refs may hold a chain that a
// plain `git fetch` brought and no git-zf command has loaded yet, and a review
// lock must not go unseen for lack of network. The fetch error is returned
// after.
func Fetch(ctx context.Context, c *git.Client, silent bool) error {
	fetchErr := c.FetchChainRefs(ctx, git.ReviewRefs, silent)
	if fetchErr != nil {
		fetchErr = fmt.Errorf("fetch reviews: %w", fetchErr)
	}

	payload, err := marshalOp(ctx, c, &Op{Type: OpMerge})
	if err != nil {
		return errors.Join(fetchErr, err)
	}

	if _, err := c.ReconcileChainRefs(ctx, git.ReviewRefs, payload); err != nil {
		return errors.Join(fetchErr, fmt.Errorf("reconcile reviews: %w", err))
	}

	return fetchErr
}

// Push pushes the review chain of slug. A rejected push (someone pushed
// first) triggers one fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, slug string) error {
	firstErr := c.PushChainRef(ctx, git.ReviewRefs, slug)
	if firstErr == nil {
		return nil
	}

	if err := Fetch(ctx, c, false); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushChainRef(ctx, git.ReviewRefs, slug); err != nil {
		return fmt.Errorf("push review %s after merge: %w", slug, err)
	}

	return nil
}

// Sync fetches and reconciles every review, then pushes the chains the remote
// does not have yet: an op whose push failed earlier goes out here. No-op
// without a remote.
func Sync(ctx context.Context, c *git.Client) error {
	if err := Fetch(ctx, c, false); err != nil {
		return err
	}

	slugs, err := c.UnpushedChainIDs(ctx, git.ReviewRefs)
	if err != nil {
		return fmt.Errorf("list unpushed reviews: %w", err)
	}

	var failed []error
	for _, slug := range slugs {
		if err := Push(ctx, c, slug); err != nil {
			failed = append(failed, err)
		}
	}

	return errors.Join(failed...)
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
