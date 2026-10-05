package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

const chainOpFile = "op.json"

// ChainRefs names one family of commit chains: the local refs
// refs/zf/<name>/<id> and, for each remote, the tracking refs
// refs/remotes/<remote>/zf/<name>/<id>.
type ChainRefs struct {
	name string
	// idIsRoot is true when a chain's ID is the object ID of its root commit.
	idIsRoot bool
}

var (
	// IssueRefs are the issue chains. An issue's ID is its root commit.
	IssueRefs = ChainRefs{name: "issues", idIsRoot: true}
	// ReviewRefs are the review chains. A review's ID is its issue slug.
	ReviewRefs = ChainRefs{name: "reviews"}
	// BranchRefs are the branch chains. A chain's ID is its issue slug.
	BranchRefs = ChainRefs{name: "branches"}
)

func (n ChainRefs) prefix() string { return "refs/zf/" + n.name + "/" }

func (n ChainRefs) trackingPrefix(remote string) string {
	return "refs/remotes/" + remote + "/zf/" + n.name + "/"
}

// FetchRefspec is the refspec that fetches the chains of remote into their
// tracking namespace. Local chain refs are never the destination of a fetch.
func (n ChainRefs) FetchRefspec(remote string) string {
	return "+" + n.prefix() + "*:" + n.trackingPrefix(remote) + "*"
}

// What ChainRefKind finds under a chain's name.
const (
	ChainAbsent  = ""       // no ref
	ChainCommits = "chain"  // a commit chain
	ChainLegacy  = "legacy" // a non-commit object, written by an older git-zf
)

// ErrIssueRefCorrupt is returned by ReadChainCommits when a ref of a family
// whose IDs are root commits does not name a root of its chain.
var ErrIssueRefCorrupt = errors.New("issue ref does not name a root of its chain")

// ErrIssueNotFound is returned when the chain ref does not exist.
var ErrIssueNotFound = errors.New("issue not found")

// ChainCommit is one commit of a chain: its ID, its parents and the raw
// content of its op.json (nil when the commit has no such file).
type ChainCommit struct {
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

// writeChainCommit stores payload as op.json in a new commit with the given
// parents and returns the commit ID. It does not move any ref. The commit is
// signed when sign is true or when commit.gpgsign is true: `git commit-tree`
// ignores that setting on its own.
func (c *Client) writeChainCommit(
	ctx context.Context, payload []byte, message string, sign bool, parents ...string,
) (string, error) {
	blob, err := c.outputStdin(ctx, payload, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}

	tree, err := c.outputStdin(ctx, []byte("100644 blob "+blob+"\t"+chainOpFile+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	if !sign {
		configured, _ := c.output(ctx, "config", "--bool", "commit.gpgsign")
		sign = configured == "true"
	}
	if sign {
		args = append(args, "-S")
	}

	commit, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}

	return commit, nil
}

// WriteChainRoot writes payload as the root commit of a new chain and returns
// its ID. No ref is created: the chain does not exist for any command until
// PublishChainRoot is called, and an unpublished root is ordinary garbage for
// git.
func (c *Client) WriteChainRoot(ctx context.Context, payload []byte, message string, sign bool) (string, error) {
	return c.writeChainCommit(ctx, payload, message, sign)
}

// PublishChainRoot creates the ref of chain id pointing at commit, a root
// written by WriteChainRoot. It fails when the ref already exists.
func (c *Client) PublishChainRoot(ctx context.Context, ns ChainRefs, id, commit string) error {
	// The zero old-value makes update-ref fail if the ref already exists.
	if _, err := c.output(ctx, "update-ref", ns.prefix()+id, commit, ZeroHash.String()); err != nil {
		return fmt.Errorf("create %s ref: %w", ns.name, err)
	}

	return nil
}

// AppendChainCommit writes payload as a new commit on top of chain id and
// moves the ref to it with compare-and-swap. Returns the new commit ID.
func (c *Client) AppendChainCommit(
	ctx context.Context, ns ChainRefs, id string, payload []byte, message string, sign bool,
) (string, error) {
	tip, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return "", err
	}
	if tip == "" {
		return "", fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	commit, err := c.writeChainCommit(ctx, payload, message, sign, tip)
	if err != nil {
		return "", err
	}

	if _, err := c.output(ctx, "update-ref", ns.prefix()+id, commit, tip); err != nil {
		return "", fmt.Errorf("update %s ref: %w", ns.name, err)
	}

	return commit, nil
}

// ChainTip returns the object the ref of chain id points at, or "" when the
// ref does not exist.
func (c *Client) ChainTip(ctx context.Context, ns ChainRefs, id string) (string, error) {
	return c.refTip(ctx, ns.prefix()+id)
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

// ListChainIDs returns the IDs of all local refs of the family.
func (c *Client) ListChainIDs(ctx context.Context, ns ChainRefs) ([]string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(refname)", ns.prefix())
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", ns.prefix(), err)
	}

	ids := []string{}
	for _, name := range strings.Fields(out) {
		ids = append(ids, strings.TrimPrefix(name, ns.prefix()))
	}

	return ids, nil
}

// ChainRefKind tells what exists under the name of chain id: nothing
// (ChainAbsent), a commit chain (ChainCommits), or a non-commit object left by
// an older git-zf (ChainLegacy). A local chain wins over the tracking ref; a
// non-commit known only through the tracking ref is still ChainLegacy, so a
// fresh clone does not mistake it for "nothing".
func (c *Client) ChainRefKind(ctx context.Context, ns ChainRefs, id string) (string, error) {
	local := ns.prefix() + id
	args := []string{"for-each-ref", "--format=%(objecttype) %(refname)", local}

	tracking := ""
	if remote, _ := c.Remote(); remote != "" {
		tracking = ns.trackingPrefix(remote) + id
		args = append(args, tracking)
	}

	out, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", local, err)
	}

	types := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		if typ, name, ok := strings.Cut(line, " "); ok {
			types[name] = typ
		}
	}

	switch {
	case types[local] == "commit":
		return ChainCommits, nil
	case types[local] != "", tracking != "" && types[tracking] != "" && types[tracking] != "commit":
		return ChainLegacy, nil
	default:
		return ChainAbsent, nil
	}
}

// LegacyBlob returns the content of the non-commit object an older git-zf left
// under the name of chain id: the local ref's, else the tracking ref's. content
// is nil when neither is a non-commit. remoteSHA is the object the tracking ref
// holds when that is a non-commit, "" otherwise: what DeleteChainRef may delete
// on the remote.
func (c *Client) LegacyBlob(ctx context.Context, ns ChainRefs, id string) (content []byte, remoteSHA string, err error) {
	local, err := c.chainTips(ctx, ns.prefix()+id)
	if err != nil {
		return nil, "", err
	}

	// chainTips keys by what follows the prefix: "" is the exact ref.
	sha := ""
	if tip, ok := local[""]; ok && !tip.commit {
		sha = tip.sha
	}

	if remote, _ := c.Remote(); remote != "" {
		tracked, err := c.chainTips(ctx, ns.trackingPrefix(remote)+id)
		if err != nil {
			return nil, "", err
		}
		if tip, ok := tracked[""]; ok && !tip.commit {
			remoteSHA = tip.sha
		}
	}

	if sha == "" {
		sha = remoteSHA
	}
	if sha == "" {
		return nil, "", nil
	}

	out, err := c.output(ctx, "cat-file", "-p", sha)
	if err != nil {
		return nil, remoteSHA, fmt.Errorf("cat-file %s: %w", sha, err)
	}

	return []byte(out), remoteSHA, nil
}

// DeleteChainRef removes the ref of chain id locally and its tracking ref.
// When remoteBlob is not empty it also deletes the ref on the remote, but only
// if the remote still holds that object (a lease): a chain another clone
// pushed in the meantime is left alone. The remote deletion is best-effort.
// Deleting a ref that does not exist is not an error.
func (c *Client) DeleteChainRef(ctx context.Context, ns ChainRefs, id, remoteBlob string) error {
	ref := ns.prefix() + id
	if _, err := c.output(ctx, "update-ref", "-d", ref); err != nil {
		return fmt.Errorf("delete %s: %w", ref, err)
	}

	remote, _ := c.Remote()
	if remote == "" {
		return nil
	}

	_, _ = c.output(ctx, "update-ref", "-d", ns.trackingPrefix(remote)+id)
	if remoteBlob != "" {
		_ = c.gitCmd(ctx, "push", "--quiet", "--force-with-lease="+ref+":"+remoteBlob, remote, ":"+ref).Run()
	}

	return nil
}

// ReadAllChains returns the commits of every local chain of the family, keyed
// by chain ID, parents before children, each with the content of its op.json.
// legacy lists, sorted, the IDs whose ref is not a commit (written by an older
// git-zf). Three git processes whatever the number of chains. It does not
// check that an ID names its chain's root: families with idIsRoot read one
// chain at a time through ReadChainCommits.
func (c *Client) ReadAllChains(
	ctx context.Context, ns ChainRefs,
) (chains map[string][]ChainCommit, legacy []string, err error) {
	tips, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return nil, nil, err
	}

	var shas []string
	for id, tip := range tips {
		if !tip.commit {
			legacy = append(legacy, id)

			continue
		}
		shas = append(shas, tip.sha)
	}
	slices.Sort(legacy)

	chains = make(map[string][]ChainCommit, len(tips))
	if len(shas) == 0 {
		return chains, legacy, nil
	}

	stdin := []byte(strings.Join(shas, "\n") + "\n")

	out, err := c.outputStdin(ctx, stdin, "rev-list", "--topo-order", "--reverse", "--parents", "--stdin")
	if err != nil {
		return nil, nil, fmt.Errorf("rev-list %s: %w", ns.prefix(), err)
	}

	// Every commit of every chain, parents before children.
	var (
		all   []ChainCommit
		specs []string
	)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		all = append(all, ChainCommit{ID: fields[0], Parents: fields[1:]})
		specs = append(specs, fields[0]+":"+chainOpFile)
	}

	cmd := c.gitCmd(ctx, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")

	raw, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("cat-file --batch: %w", gitStderr(err))
	}

	if err := readBatchPayloads(bufio.NewReader(bytes.NewReader(raw)), all); err != nil {
		return nil, nil, fmt.Errorf("parse cat-file output for %s: %w", ns.prefix(), err)
	}

	parents := make(map[string][]string, len(all))
	for i := range all {
		parents[all[i].ID] = all[i].Parents
	}

	for id, tip := range tips {
		if !tip.commit {
			continue
		}

		// The chain is what its tip reaches; all keeps the order.
		reach := reachable(tip.sha, parents)
		commits := make([]ChainCommit, 0, len(reach))
		for i := range all {
			if reach[all[i].ID] {
				commits = append(commits, all[i])
			}
		}
		chains[id] = commits
	}

	return chains, legacy, nil
}

// ReadChainCommits returns every commit of chain id, parents before children,
// each with the content of its op.json.
func (c *Client) ReadChainCommits(ctx context.Context, ns ChainRefs, id string) ([]ChainCommit, error) {
	ref := ns.prefix() + id

	out, err := c.output(ctx, "rev-list", "--topo-order", "--reverse", "--parents", ref)
	if err != nil {
		// Only on failure: tell "no such chain" from a chain that cannot be
		// read. Checking the ref first would cost one more git process per
		// chain on every listing.
		if tip, tipErr := c.ChainTip(ctx, ns, id); tipErr == nil && tip == "" {
			return nil, fmt.Errorf("%s: %w", id, ErrIssueNotFound)
		}

		return nil, fmt.Errorf("rev-list %s: %w", ref, err)
	}

	var (
		commits []ChainCommit
		specs   []string
		rooted  = !ns.idIsRoot
	)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		commits = append(commits, ChainCommit{ID: fields[0], Parents: fields[1:]})
		specs = append(specs, fields[0]+":"+chainOpFile)
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

// reachable returns tip and every commit it reaches through parents.
func reachable(tip string, parents map[string][]string) map[string]bool {
	reach := map[string]bool{tip: true}
	for queue := []string{tip}; len(queue) > 0; queue = queue[1:] {
		for _, p := range parents[queue[0]] {
			if !reach[p] {
				reach[p] = true
				queue = append(queue, p)
			}
		}
	}

	return reach
}

// readBatchPayloads parses `git cat-file --batch` output, one entry per commit
// in order. An entry is either "<oid> <type> <size>\n<content>\n" or
// "<spec> missing\n".
func readBatchPayloads(r *bufio.Reader, commits []ChainCommit) error {
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
