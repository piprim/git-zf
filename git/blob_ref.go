package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// writeBlobRef stores v as a JSON blob and points refName at it. A non-empty
// oldSHA makes the update a compare-and-swap: it fails unless refName still
// points at oldSHA. It returns the new blob SHA.
func (c *Client) writeBlobRef(ctx context.Context, refName string, v any, oldSHA string) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", refName, err)
	}

	sha, err := c.outputStdin(ctx, data, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}

	args := []string{"update-ref", refName, sha}
	if oldSHA != "" {
		args = append(args, oldSHA)
	}

	if _, err := c.output(ctx, args...); err != nil {
		return "", fmt.Errorf("git update-ref %s: %w", refName, err)
	}

	return sha, nil
}

// readBlobRef decodes the JSON blob refName points at into v and returns the
// blob SHA. The SHA is "" and v is left untouched when the ref does not exist.
func (c *Client) readBlobRef(ctx context.Context, refName string, v any) (string, error) {
	sha, err := c.output(ctx, "show-ref", "--verify", "--hash", refName)
	if err != nil {
		return "", nil //nolint:nilerr // show-ref fails when the ref does not exist
	}

	return sha, c.catBlob(ctx, sha, v)
}

// catBlob decodes the JSON blob sha into v.
func (c *Client) catBlob(ctx context.Context, sha string, v any) error {
	blob, err := c.output(ctx, "cat-file", "blob", sha)
	if err != nil {
		return fmt.Errorf("git cat-file blob %s: %w", sha, err)
	}

	if err := json.Unmarshal([]byte(blob), v); err != nil {
		return fmt.Errorf("unmarshal blob %s: %w", sha, err)
	}

	return nil
}

// eachBlobRef calls fn, in ref-name order, with every JSON blob ref under
// prefix: the ref name with prefix removed, and the decoded blob. A ref whose
// blob cannot be read or decoded is skipped. No ref under prefix is not an
// error.
func eachBlobRef[T any](ctx context.Context, c *Client, prefix string, fn func(name string, v *T)) error {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", prefix)
	if err != nil {
		return fmt.Errorf("for-each-ref %s: %w", prefix, err)
	}

	for line := range strings.SplitSeq(out, "\n") {
		sha, refName, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		var v T
		if c.catBlob(ctx, sha, &v) != nil {
			continue
		}

		fn(strings.TrimPrefix(refName, prefix), &v)
	}

	return nil
}

// fetchRefs fetches prefix* from the remote into the same local namespace.
// No-op when no remote is configured.
func (c *Client) fetchRefs(ctx context.Context, prefix string, flags ...string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	args := append(append([]string{"fetch"}, flags...), remote, prefix+"*:"+prefix+"*")
	if err := c.runInteractive(ctx, c.root, args...); err != nil {
		return fmt.Errorf("fetch %s*: %w", prefix, err)
	}

	return nil
}
