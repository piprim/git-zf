package git

import (
	"context"
	"fmt"
	"strings"
)

// VerifyCommit reports whether sha carries a signature git trusts: nil when
// `git verify-commit` succeeds. Trust is git's own, the GPG keyring or
// gpg.ssh.allowedSignersFile.
func (c *Client) VerifyCommit(ctx context.Context, sha string) error {
	if _, err := c.output(ctx, "verify-commit", sha); err != nil {
		return fmt.Errorf("verify-commit %s: %w", sha, err)
	}

	return nil
}

// CommitSigned reports whether sha carries a signature at all, valid or not.
// It reads the commit header rather than `%G?`, which prints N for an SSH
// signature when gpg.ssh.allowedSignersFile is not configured.
func (c *Client) CommitSigned(ctx context.Context, sha string) (bool, error) {
	raw, err := c.output(ctx, "cat-file", "commit", sha)
	if err != nil {
		return false, fmt.Errorf("cat-file commit %s: %w", sha, err)
	}

	header, _, _ := strings.Cut(raw, "\n\n")
	for line := range strings.SplitSeq(header, "\n") {
		// "gpgsig" in a SHA-1 repository, "gpgsig-sha256" in a SHA-256 one.
		if strings.HasPrefix(line, "gpgsig") {
			return true, nil
		}
	}

	return false, nil
}

// CommitSigner returns who git says signed sha (`%GS`): the key's user ID for
// GPG, the allowed-signers principal for SSH. It is empty for an unsigned
// commit or a signature git cannot check.
func (c *Client) CommitSigner(ctx context.Context, sha string) (string, error) {
	out, err := c.output(ctx, "log", "-1", "--format=%GS", sha)
	if err != nil {
		return "", fmt.Errorf("read signer of %s: %w", sha, err)
	}

	return strings.TrimSpace(out), nil
}
