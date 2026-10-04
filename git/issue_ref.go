package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	issueRefPrefix = "refs/zf/issues/"
	issueOpFile    = "op.json"
)

// ErrIssueRefCorrupt is returned by ReadIssueCommits when refs/zf/issues/<id>
// does not name a root commit of its own chain.
var ErrIssueRefCorrupt = errors.New("issue ref does not name a root of its chain")

// ErrIssueNotFound is returned when refs/zf/issues/<id> does not exist.
var ErrIssueNotFound = errors.New("issue not found")

// IssueCommit is one commit of an issue chain: its ID, its parents and the raw
// content of its op.json (nil when the commit has no such file).
type IssueCommit struct {
	ID      string
	Parents []string
	Payload []byte
}

// outputStdin is output with stdin fed to the command.
func (c *Client) outputStdin(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := c.gitCmd(ctx, args...)
	cmd.Stdin = bytes.NewReader(stdin)

	out, err := cmd.Output()
	if err != nil {
		return "", gitStderr(err)
	}

	return strings.TrimSpace(string(out)), nil
}

// writeIssueCommit stores payload as op.json in a new commit with the given
// parents and returns the commit ID. It does not move any ref. The commit is
// signed when commit.gpgsign is true: `git commit-tree` ignores that setting
// on its own.
func (c *Client) writeIssueCommit(ctx context.Context, payload []byte, message string, parents ...string) (string, error) {
	blob, err := c.outputStdin(ctx, payload, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}

	tree, err := c.outputStdin(ctx, []byte("100644 blob "+blob+"\t"+issueOpFile+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	if sign, _ := c.output(ctx, "config", "--bool", "commit.gpgsign"); sign == "true" {
		args = append(args, "-S")
	}

	commit, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}

	return commit, nil
}

// WriteIssueRoot writes payload as the root commit of a new issue chain and
// returns its ID, which is the issue's ID. No ref is created: the issue does
// not exist for any command until PublishIssueRoot is called, and an
// unpublished root is ordinary garbage for git.
func (c *Client) WriteIssueRoot(ctx context.Context, payload []byte, message string) (string, error) {
	return c.writeIssueCommit(ctx, payload, message)
}

// PublishIssueRoot creates refs/zf/issues/<id> pointing at the root commit id
// written by WriteIssueRoot.
func (c *Client) PublishIssueRoot(ctx context.Context, id string) error {
	// The zero old-value makes update-ref fail if the ref already exists.
	if _, err := c.output(ctx, "update-ref", issueRefPrefix+id, id, ZeroHash.String()); err != nil {
		return fmt.Errorf("create issue ref: %w", err)
	}

	return nil
}

// AppendIssueCommit writes payload as a new commit on top of issue id and
// moves the ref to it with compare-and-swap. Returns the new commit ID.
func (c *Client) AppendIssueCommit(ctx context.Context, id string, payload []byte, message string) (string, error) {
	tip, err := c.IssueTip(ctx, id)
	if err != nil {
		return "", err
	}
	if tip == "" {
		return "", fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	commit, err := c.writeIssueCommit(ctx, payload, message, tip)
	if err != nil {
		return "", err
	}

	if _, err := c.output(ctx, "update-ref", issueRefPrefix+id, commit, tip); err != nil {
		return "", fmt.Errorf("update issue ref: %w", err)
	}

	return commit, nil
}

// IssueTip returns the commit refs/zf/issues/<id> points at, or "" when the
// ref does not exist.
func (c *Client) IssueTip(ctx context.Context, id string) (string, error) {
	return c.refTip(ctx, issueRefPrefix+id)
}

func (c *Client) refTip(ctx context.Context, ref string) (string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", ref)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", ref, err)
	}

	// for-each-ref also matches refs *under* ref; keep the exact name only.
	for _, line := range strings.Split(out, "\n") {
		if tip, name, ok := strings.Cut(line, " "); ok && name == ref {
			return tip, nil
		}
	}

	return "", nil
}

// ListIssueIDs returns the IDs of all local issue refs.
func (c *Client) ListIssueIDs(ctx context.Context) ([]string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(refname)", issueRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", issueRefPrefix, err)
	}

	ids := []string{}
	for _, name := range strings.Fields(out) {
		ids = append(ids, strings.TrimPrefix(name, issueRefPrefix))
	}

	return ids, nil
}

// ReadIssueCommits returns every commit of issue id, parents before children,
// each with the content of its op.json.
func (c *Client) ReadIssueCommits(ctx context.Context, id string) ([]IssueCommit, error) {
	ref := issueRefPrefix + id

	out, err := c.output(ctx, "rev-list", "--topo-order", "--reverse", "--parents", ref)
	if err != nil {
		// Only on failure: tell "no such issue" from a chain that cannot be
		// read. Checking the ref first would cost one more git process per
		// issue on every listing.
		if tip, tipErr := c.IssueTip(ctx, id); tipErr == nil && tip == "" {
			return nil, fmt.Errorf("%s: %w", id, ErrIssueNotFound)
		}

		return nil, fmt.Errorf("rev-list %s: %w", ref, err)
	}

	var (
		commits []IssueCommit
		specs   []string
		rooted  bool
	)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		commits = append(commits, IssueCommit{ID: fields[0], Parents: fields[1:]})
		specs = append(specs, fields[0]+":"+issueOpFile)
		if len(fields) == 1 && fields[0] == id {
			rooted = true
		}
	}

	if !rooted {
		return nil, fmt.Errorf("%s: %w", ref, ErrIssueRefCorrupt)
	}

	cmd := c.gitCmd(ctx, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")

	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cat-file --batch: %w", gitStderr(err))
	}

	if err := readBatchPayloads(bufio.NewReader(bytes.NewReader(raw)), commits); err != nil {
		return nil, fmt.Errorf("parse cat-file output for %s: %w", ref, err)
	}

	return commits, nil
}

// readBatchPayloads parses `git cat-file --batch` output, one entry per commit
// in order. An entry is either "<oid> <type> <size>\n<content>\n" or
// "<spec> missing\n".
func readBatchPayloads(r *bufio.Reader, commits []IssueCommit) error {
	const headerFields = 3 // "<oid> <type> <size>"

	for i := range commits {
		header, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read header: %w", err)
		}

		fields := strings.Fields(header)
		if len(fields) != headerFields {
			continue // "<spec> missing": the commit has no op.json
		}

		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("object size %q: %w", fields[2], err)
		}

		content := make([]byte, size)
		if _, err := io.ReadFull(r, content); err != nil {
			return fmt.Errorf("read content: %w", err)
		}
		if _, err := r.Discard(1); err != nil { // trailing newline
			return fmt.Errorf("read content terminator: %w", err)
		}

		if fields[1] == "blob" {
			commits[i].Payload = content
		}
	}

	return nil
}
