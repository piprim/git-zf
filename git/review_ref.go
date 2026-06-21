package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

const reviewRefPrefix = "refs/zf/reviews/"

// ReviewRef is the JSON payload stored as a git blob at refs/zf/reviews/<IssueID>.
type ReviewRef struct {
	Status     string `json:"status"`
	Round      int    `json:"round"`
	FeatureSHA string `json:"feature_sha"`
	Reviewer   string `json:"reviewer,omitempty"`
	CreatedAt  string `json:"created_at"` // RFC3339
}

// WriteReviewRef atomically writes a ReviewRef as a git blob and updates the
// local ref refs/zf/reviews/<issueID> using CAS. oldSHA must be the current
// ref SHA — pass "" for the first write (no prior value). Returns the new SHA.
func (c *Client) WriteReviewRef(ctx context.Context, issueID string, ref ReviewRef, oldSHA string) (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	data, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("marshal review ref: %w", err)
	}

	// Write blob object.
	hashCmd := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "-w", "--stdin")
	hashCmd.Stdin = bytes.NewReader(data)
	out, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	newSHA := strings.TrimSpace(string(out))

	// Atomic CAS update of the ref.
	refName := reviewRefPrefix + issueID
	args := []string{"-C", root, "update-ref", refName, newSHA}
	if oldSHA != "" {
		args = append(args, oldSHA)
	}

	updateCmd := exec.CommandContext(ctx, "git", args...)
	if out, err := updateCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git update-ref (CAS): %w: %s", err, out)
	}

	return newSHA, nil
}

// ReadReviewRef reads the ReviewRef for issueID from the local ref store.
// Returns (nil, "", nil) when the ref does not exist.
// The returned currentSHA is suitable as oldSHA in the next WriteReviewRef call.
func (c *Client) ReadReviewRef(ctx context.Context, issueID string) (*ReviewRef, string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, "", fmt.Errorf("working tree root: %w", err)
	}

	refName := reviewRefPrefix + issueID

	// Resolve ref to SHA.
	showCmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--hash", refName)
	shaOut, err := showCmd.Output()
	if err != nil {
		// show-ref exits 1 when the ref does not exist — not an error.
		return nil, "", nil
	}
	currentSHA := strings.TrimSpace(string(shaOut))

	// Read blob contents.
	catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", currentSHA)
	blobOut, err := catCmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("git cat-file blob %s: %w", currentSHA, err)
	}

	var ref ReviewRef
	if err := json.Unmarshal(blobOut, &ref); err != nil {
		return nil, "", fmt.Errorf("unmarshal review ref: %w", err)
	}

	return &ref, currentSHA, nil
}

// FetchReviewRefs fetches refs/zf/reviews/* from the remote into the local ref
// namespace. No-op when no remote is configured.
func (c *Client) FetchReviewRefs(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	// --prune removes local refs/zf/reviews/* that no longer exist on the
	// remote (e.g. deleted when a sibling developer closed their issue).
	refspec := reviewRefPrefix + "*:" + reviewRefPrefix + "*"
	if err := c.runInteractive(ctx, root, "fetch", "--prune", remote, refspec); err != nil {
		return fmt.Errorf("fetch review refs: %w", err)
	}

	return nil
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

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return
	}

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

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

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
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	// List all refs under refs/zf/reviews/ with their SHA.
	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"for-each-ref", "--format=%(objectname) %(refname)", reviewRefPrefix)
	out, err := cmd.Output()
	if err != nil {
		// No refs exist yet — return empty map, not an error.
		return map[string]*ReviewRef{}, nil
	}

	result := make(map[string]*ReviewRef)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha := parts[0]
		refName := parts[1] // e.g. refs/zf/reviews/X.1

		issueID := strings.TrimPrefix(refName, reviewRefPrefix)
		if issueID == "" {
			continue
		}

		catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", sha)
		blobOut, catErr := catCmd.Output()
		if catErr != nil {
			continue // skip malformed ref
		}

		var ref ReviewRef
		if jsonErr := json.Unmarshal(blobOut, &ref); jsonErr != nil {
			continue
		}
		result[issueID] = &ref
	}

	return result, nil
}

// DeleteReviewRef deletes refs/zf/reviews/<issueID> locally. If a remote is
// configured, also attempts to delete it there (best-effort; errors are ignored).
func (c *Client) DeleteReviewRef(ctx context.Context, issueID string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

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
