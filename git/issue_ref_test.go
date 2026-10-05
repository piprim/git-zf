package git

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func payloads(commits []ChainCommit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = string(c.Payload)
	}

	return out
}

func TestIssueRef_CreateAppendRead(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("the ref is named after the full ID of the root commit", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "refs/zf/issues/"+id).Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != id || len(id) != 40 {
			t.Errorf("ref points at %q, id is %q", got, id)
		}
	})

	second, err := client.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"type":"add_comment"}`), "add_comment", false)
	if err != nil {
		t.Fatalf("AppendChainCommit: %v", err)
	}

	t.Run("ChainTip returns the appended commit", func(t *testing.T) {
		tip, err := client.ChainTip(ctx, IssueRefs, id)
		if err != nil || tip != second {
			t.Errorf("ChainTip = %q, %v; want %q", tip, err, second)
		}
	})

	t.Run("ChainTip of an unknown issue is empty", func(t *testing.T) {
		tip, err := client.ChainTip(ctx, IssueRefs, "0000000000000000000000000000000000000001")
		if err != nil || tip != "" {
			t.Errorf("ChainTip = %q, %v; want empty", tip, err)
		}
	})

	t.Run("ReadChainCommits returns parents first with payloads and parent links", func(t *testing.T) {
		commits, err := client.ReadChainCommits(ctx, IssueRefs, id)
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		got := payloads(commits)
		if len(got) != 2 || got[0] != `{"type":"create"}` || got[1] != `{"type":"add_comment"}` {
			t.Fatalf("payloads = %q", got)
		}
		if commits[0].ID != id || len(commits[0].Parents) != 0 {
			t.Errorf("root = %+v", commits[0])
		}
		if commits[1].ID != second || len(commits[1].Parents) != 1 || commits[1].Parents[0] != id {
			t.Errorf("second = %+v", commits[1])
		}
	})

	t.Run("ListChainIDs lists the issue", func(t *testing.T) {
		ids, err := client.ListChainIDs(ctx, IssueRefs)
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Errorf("ListChainIDs = %v, %v", ids, err)
		}
	})

	t.Run("AppendChainCommit on an unknown issue fails with ErrIssueNotFound", func(t *testing.T) {
		_, err := client.AppendChainCommit(ctx, IssueRefs, "0000000000000000000000000000000000000001", []byte(`{}`), "x", false)
		if !errors.Is(err, ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("a payload with multi-byte characters and newlines round-trips", func(t *testing.T) {
		body := "{\"body\":\"é à ü\\nline two\"}\n\n"
		id2, err := client.CreateIssueRef(ctx, []byte(body), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		commits, err := client.ReadChainCommits(ctx, IssueRefs, id2)
		if err != nil || len(commits) != 1 || string(commits[0].Payload) != body {
			t.Errorf("payload = %q, %v", payloads(commits), err)
		}
	})
}

func TestIssueRef_Corrupt(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("a ref whose name is not a root of its chain is rejected", func(t *testing.T) {
		wrong := "1111111111111111111111111111111111111111"
		mustGit(t, dir, "update-ref", "refs/zf/issues/"+wrong, id)

		_, err := client.ReadChainCommits(ctx, IssueRefs, wrong)
		if !errors.Is(err, ErrIssueRefCorrupt) {
			t.Errorf("err = %v, want ErrIssueRefCorrupt", err)
		}
	})

	t.Run("a commit without op.json yields a nil payload and keeps the chain readable", func(t *testing.T) {
		// Append a commit carrying the empty tree.
		emptyTree := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "commit-tree", emptyTree, "-p", id, "-m", "junk").Output()
		if err != nil {
			t.Fatalf("commit-tree: %v", err)
		}
		junk := strings.TrimSpace(string(out))
		mustGit(t, dir, "update-ref", "refs/zf/issues/"+id, junk)

		if _, err := client.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"type":"add_label"}`), "add_label", false); err != nil {
			t.Fatalf("AppendChainCommit: %v", err)
		}

		commits, err := client.ReadChainCommits(ctx, IssueRefs, id)
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		if len(commits) != 3 || commits[1].Payload != nil || string(commits[2].Payload) != `{"type":"add_label"}` {
			t.Errorf("payloads = %q", payloads(commits))
		}
	})
}

func TestIssueRef_Signing(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	// A gpg program that always fails: a write succeeds only if git did not
	// try to sign.
	mustGit(t, dir, "config", "gpg.program", "false")

	t.Run("commit.gpgsign=false writes an unsigned commit", func(t *testing.T) {
		if _, err := client.CreateIssueRef(ctx, []byte(`{"n":1}`), "create"); err != nil {
			t.Errorf("CreateIssueRef: %v", err)
		}
	})

	t.Run("commit.gpgsign=true makes the write sign", func(t *testing.T) {
		mustGit(t, dir, "config", "commit.gpgsign", "true")

		if _, err := client.CreateIssueRef(ctx, []byte(`{"n":2}`), "create"); err == nil {
			t.Error("expected the write to fail because signing was attempted")
		}
	})
}

// ReadChainCommits must tell "no such issue" from a chain it cannot read.
func TestIssueRef_ReadErrors(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	t.Run("an unknown issue is ErrIssueNotFound", func(t *testing.T) {
		_, err := client.ReadChainCommits(ctx, IssueRefs, "0000000000000000000000000000000000000001")
		if !errors.Is(err, ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("a ref that points at a blob is an error, but not ErrIssueNotFound", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "hash-object", "-w", "--stdin").Output()
		if err != nil {
			t.Fatalf("hash-object: %v", err)
		}
		blob := strings.TrimSpace(string(out))
		mustGit(t, dir, "update-ref", "refs/zf/issues/"+blob, blob)

		_, err = client.ReadChainCommits(ctx, IssueRefs, blob)
		if err == nil || errors.Is(err, ErrIssueNotFound) {
			t.Errorf("err = %v, want a read error distinct from ErrIssueNotFound", err)
		}
	})
}

// CreateIssueRef writes payload as the root commit of a new issue chain and
// creates refs/zf/issues/<id>, where id is that commit's ID. Returns id.
// Test-only: production code calls WriteChainRoot and PublishChainRoot
// separately, so nothing is published before the branch exists.
func (c *Client) CreateIssueRef(ctx context.Context, payload []byte, message string) (string, error) {
	id, err := c.WriteChainRoot(ctx, payload, message, false)
	if err != nil {
		return "", err
	}

	if err := c.PublishChainRoot(ctx, IssueRefs, id, id); err != nil {
		return "", err
	}

	return id, nil
}
