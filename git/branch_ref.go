package git

import (
	"context"
	"fmt"
)

const branchRefPrefix = "refs/zf/branches/"

// BranchRef is the JSON payload stored as a git blob at refs/zf/branches/<issueSlug>.
// It records the branch name and optional parent slug so any clone can resolve
// the merge target without querying the local SQLite store.
type BranchRef struct {
	IssueSlug  string `json:"issue_slug"`
	BranchName string `json:"branch_name"`
	ParentSlug string `json:"parent_slug,omitempty"`
	CreatedAt  string `json:"created_at"` // RFC3339
	Merged     bool   `json:"merged,omitempty"`
	// TrackerType records the tracker that created the issue ("" = manual). It
	// is the cross-machine source of truth for whether an issue is tracker-born:
	// stored in the git object (this blob) and fetched by every clone, so a
	// reviewer with an empty local store can still tell whether to offer a
	// tracker status update. omitempty keeps pre-existing refs backward-
	// compatible — an absent field unmarshals to "" (treated as "manual").
	TrackerType string `json:"tracker_type,omitempty"`
	// IssueID is the full ID of the repo issue (refs/zf/issues/<IssueID>) the
	// branch works on. The branch name carries only a 7-character short ID,
	// which may be ambiguous; this field is not. Empty when the issue has no
	// record in the repository.
	IssueID string `json:"issue_id,omitempty"`
}

// WriteBranchRef writes a BranchRef as a git blob and updates the local ref
// refs/zf/branches/<issueSlug>. No CAS — branch metadata is write-once; an
// overwrite (e.g. re-running issue start) simply replaces the blob.
// Returns the new blob SHA.
func (c *Client) WriteBranchRef(ctx context.Context, issueSlug string, ref BranchRef) (string, error) {
	return c.writeBlobRef(ctx, branchRefPrefix+issueSlug, ref, "")
}

// ReadBranchRef reads the BranchRef for issueSlug from the local ref store.
// Returns (nil, nil) when the ref does not exist.
func (c *Client) ReadBranchRef(ctx context.Context, issueSlug string) (*BranchRef, error) {
	var ref BranchRef

	sha, err := c.readBlobRef(ctx, branchRefPrefix+issueSlug, &ref)
	if sha == "" || err != nil {
		return nil, err
	}

	return &ref, nil
}

// FetchBranchRefs fetches refs/zf/branches/* from the remote into the local
// ref namespace. No-op when no remote is configured.
func (c *Client) FetchBranchRefs(ctx context.Context) error {
	return c.fetchRefs(ctx, branchRefPrefix)
}

// PushBranchRef pushes refs/zf/branches/<issueSlug> to the remote.
// No-op when no remote is configured. Uses --force because the ref points to a
// blob (not a commit) and git rejects non-force updates of blob refs; it is
// also needed when stamping Merged:true on an existing ref after close.
func (c *Client) PushBranchRef(ctx context.Context, issueSlug string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	root := c.root

	refName := branchRefPrefix + issueSlug
	if err := c.runInteractive(ctx, root, "push", "--force", remote, refName); err != nil {
		return fmt.Errorf("push branch ref %s: %w", issueSlug, err)
	}

	return nil
}

// ListBranchRefs returns all locally available branch refs (refs/zf/branches/*).
// Call FetchBranchRefs first to refresh the namespace from the remote. Returns
// an empty slice (not an error) when none exist; malformed blobs are skipped.
func (c *Client) ListBranchRefs(ctx context.Context) ([]BranchRef, error) {
	result := []BranchRef{}

	// A genuine git failure is returned: callers that want best-effort
	// behavior (CloseCandidates) degrade on this error.
	err := eachBlobRef(ctx, c, branchRefPrefix, func(_ string, ref *BranchRef) {
		result = append(result, *ref)
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}
