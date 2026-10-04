package git

import (
	"context"
	"fmt"
	"os/exec"
)

const reviewRefPrefix = "refs/zf/reviews/"

// ReviewRef is the JSON payload stored as a git blob at refs/zf/reviews/<IssueID>.
type ReviewRef struct {
	Status     string `json:"status"`
	Round      int    `json:"round"`
	FeatureSHA string `json:"feature_sha"`
	Reviewer   string `json:"reviewer,omitempty"`
	CreatedAt  string `json:"created_at"` // RFC3339
	// Comment is the reviewer's explanation when requesting changes.
	Comment string `json:"comment,omitempty"`
}

// WriteReviewRef atomically writes a ReviewRef as a git blob and updates the
// local ref refs/zf/reviews/<issueID> using CAS. oldSHA must be the current
// ref SHA — pass "" for the first write (no prior value). Returns the new SHA.
func (c *Client) WriteReviewRef(ctx context.Context, issueID string, ref ReviewRef, oldSHA string) (string, error) {
	return c.writeBlobRef(ctx, reviewRefPrefix+issueID, ref, oldSHA)
}

// ReadReviewRef reads the ReviewRef for issueID from the local ref store.
// Returns (nil, "", nil) when the ref does not exist.
// The returned currentSHA is suitable as oldSHA in the next WriteReviewRef call.
func (c *Client) ReadReviewRef(ctx context.Context, issueID string) (*ReviewRef, string, error) {
	var ref ReviewRef

	currentSHA, err := c.readBlobRef(ctx, reviewRefPrefix+issueID, &ref)
	if currentSHA == "" || err != nil {
		return nil, "", err
	}

	return &ref, currentSHA, nil
}

// FetchReviewRefs fetches refs/zf/reviews/* from the remote into the local ref
// namespace. No-op when no remote is configured.
func (c *Client) FetchReviewRefs(ctx context.Context) error {
	// --prune removes local refs/zf/reviews/* that no longer exist on the
	// remote (e.g. deleted when a sibling developer closed their issue).
	return c.fetchRefs(ctx, reviewRefPrefix, "--prune")
}

// FetchReviewRef fetches refs/zf/reviews/<issueID> from the remote into the
// local ref namespace. Silent — designed for use in pre-push hooks where
// interactive output would be confusing. No-op when no remote is configured.
// Errors are silently ignored (fail-open: the caller falls back to the local ref).
func (c *Client) FetchReviewRef(ctx context.Context, issueID string) {
	remote, err := c.Remote()
	if err != nil || remote == "" {
		return
	}

	root := c.root

	refName := reviewRefPrefix + issueID
	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"fetch", "--quiet", remote, refName+":"+refName)
	_ = cmd.Run()
}

// PushReviewRef pushes refs/zf/reviews/<issueID> to the remote using
// --force-with-lease to prevent overwriting a concurrently updated ref.
// Pass expectedOldSHA="" to allow any prior value (first push).
// No-op when no remote is configured.
func (c *Client) PushReviewRef(ctx context.Context, issueID, expectedOldSHA string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root := c.root

	refName := reviewRefPrefix + issueID
	lease := refName
	if expectedOldSHA != "" {
		lease = refName + ":" + expectedOldSHA
	}

	if err := c.runInteractive(ctx, root,
		"push", "--force-with-lease="+lease, remote, refName,
	); err != nil {
		return fmt.Errorf("push review ref %s: %w", issueID, err)
	}

	return nil
}

// ListReviewRefs returns all locally available review refs as a map of
// issueID → ReviewRef. Call FetchReviewRefs first to ensure the local
// namespace is up to date. Does not require the issue to exist in the store.
func (c *Client) ListReviewRefs(ctx context.Context) (map[string]*ReviewRef, error) {
	result := make(map[string]*ReviewRef)

	// A failing for-each-ref is reported as "no review refs", not as an error.
	_ = eachBlobRef(ctx, c, reviewRefPrefix, func(issueID string, ref *ReviewRef) {
		result[issueID] = ref
	})

	return result, nil
}

// DeleteReviewRef deletes refs/zf/reviews/<issueID> locally. If a remote is
// configured, also attempts to delete it there (best-effort; errors are ignored).
func (c *Client) DeleteReviewRef(ctx context.Context, issueID string) error {
	root := c.root

	refName := reviewRefPrefix + issueID

	// Delete local ref.
	delCmd := exec.CommandContext(ctx, "git", "-C", root, "update-ref", "-d", refName)
	if out, err := delCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete local review ref %s: %w: %s", refName, err, out)
	}

	// Delete remote ref best-effort.
	if remote, _ := c.Remote(); remote != "" {
		_ = c.runInteractive(ctx, root, "push", remote, "--delete", refName)
	}

	return nil
}
