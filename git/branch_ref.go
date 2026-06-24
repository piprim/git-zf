package git

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
}

// WriteBranchRef writes a BranchRef as a git blob and updates the local ref
// refs/zf/branches/<issueSlug>. No CAS — branch metadata is write-once; an
// overwrite (e.g. re-running issue start) simply replaces the blob.
// Returns the new blob SHA.
func (c *Client) WriteBranchRef(ctx context.Context, issueSlug string, ref BranchRef) (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	data, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("marshal branch ref: %w", err)
	}

	hashCmd := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "-w", "--stdin")
	hashCmd.Stdin = bytes.NewReader(data)
	out, err := hashCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	newSHA := strings.TrimSpace(string(out))

	refName := branchRefPrefix + issueSlug
	updateCmd := exec.CommandContext(ctx, "git", "-C", root, "update-ref", refName, newSHA)
	if out, err := updateCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git update-ref: %w: %s", err, out)
	}

	return newSHA, nil
}

// ReadBranchRef reads the BranchRef for issueSlug from the local ref store.
// Returns (nil, nil) when the ref does not exist.
func (c *Client) ReadBranchRef(ctx context.Context, issueSlug string) (*BranchRef, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	refName := branchRefPrefix + issueSlug

	showCmd := exec.CommandContext(ctx, "git", "-C", root, "show-ref", "--verify", "--hash", refName)
	shaOut, err := showCmd.Output()
	if err != nil {
		return nil, nil // ref does not exist
	}
	sha := strings.TrimSpace(string(shaOut))

	catCmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", sha)
	blobOut, err := catCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git cat-file blob %s: %w", sha, err)
	}

	var ref BranchRef
	if err := json.Unmarshal(blobOut, &ref); err != nil {
		return nil, fmt.Errorf("unmarshal branch ref: %w", err)
	}

	return &ref, nil
}

// FetchBranchRefs fetches refs/zf/branches/* from the remote into the local
// ref namespace. No-op when no remote is configured.
func (c *Client) FetchBranchRefs(ctx context.Context) error {
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

	refspec := branchRefPrefix + "*:" + branchRefPrefix + "*"
	if err := c.runInteractive(ctx, root, "fetch", remote, refspec); err != nil {
		return fmt.Errorf("fetch branch refs: %w", err)
	}

	return nil
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

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	refName := branchRefPrefix + issueSlug
	if err := c.runInteractive(ctx, root, "push", "--force", remote, refName); err != nil {
		return fmt.Errorf("push branch ref %s: %w", issueSlug, err)
	}

	return nil
}
