package git

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// originRefs lists the ref names the bare origin has under prefix.
func originRefs(t *testing.T, originDir, prefix string) []string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"for-each-ref", "--format=%(refname)", prefix).Output()
	if err != nil {
		t.Fatalf("for-each-ref on origin: %v", err)
	}

	return strings.Fields(string(out))
}

func TestChainRef_ReviewNamespace(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	root, err := client.WriteChainRoot(ctx, []byte(`{"type":"request"}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := client.PublishChainRoot(ctx, ReviewRefs, "ABC-1", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}
	tip, err := client.AppendChainCommit(ctx, ReviewRefs, "ABC-1", []byte(`{"type":"approve"}`), "approve", false)
	if err != nil {
		t.Fatalf("AppendChainCommit: %v", err)
	}

	t.Run("the ref is named after the slug and points at the tip", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "refs/zf/reviews/ABC-1").Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != tip {
			t.Errorf("ref points at %q, want %q", got, tip)
		}
	})

	t.Run("ReadChainCommits does not require the name to be the root commit", func(t *testing.T) {
		commits, err := client.ReadChainCommits(ctx, ReviewRefs, "ABC-1")
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		if len(commits) != 2 || commits[0].ID != root || commits[1].ID != tip {
			t.Errorf("commits = %+v", commits)
		}
	})

	t.Run("ListChainIDs lists only its own namespace", func(t *testing.T) {
		reviews, err := client.ListChainIDs(ctx, ReviewRefs)
		if err != nil || !slices.Equal(reviews, []string{"ABC-1"}) {
			t.Errorf("reviews = %v, %v", reviews, err)
		}
		issues, err := client.ListChainIDs(ctx, IssueRefs)
		if err != nil || len(issues) != 0 {
			t.Errorf("issues = %v, %v", issues, err)
		}
	})

	t.Run("publishing a root over an existing chain fails", func(t *testing.T) {
		if err := client.PublishChainRoot(ctx, ReviewRefs, "ABC-1", root); err == nil {
			t.Error("PublishChainRoot succeeded on an existing ref")
		}
	})
}

func TestChainRef_TrackingNamespace(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, bobDir := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}

	t.Run("push records the tip under refs/remotes/origin/zf/issues/", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", aliceDir, "rev-parse", "refs/remotes/origin/zf/issues/"+id).Output()
		if err != nil || strings.TrimSpace(string(out)) != id {
			t.Errorf("tracking ref = %q, %v; want %q", out, err, id)
		}
	})

	t.Run("fetch writes the tracking ref and no local ref", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", bobDir, "rev-parse", "refs/remotes/origin/zf/issues/"+id).Output()
		if err != nil || strings.TrimSpace(string(out)) != id {
			t.Errorf("tracking ref = %q, %v; want %q", out, err, id)
		}
		if tip, _ := bob.ChainTip(ctx, IssueRefs, id); tip != "" {
			t.Errorf("fetch created the local ref: %q", tip)
		}
	})

	t.Run("nothing is written under the old refs/zf/remote/ prefix", func(t *testing.T) {
		for _, c := range []*Client{alice, bob} {
			out, err := c.output(ctx, "for-each-ref", "refs/zf/remote/")
			if err != nil || out != "" {
				t.Errorf("refs/zf/remote/ = %q, %v", out, err)
			}
		}
	})

	t.Run("FetchRefspec maps the local namespace to the tracking one", func(t *testing.T) {
		want := "+refs/zf/reviews/*:refs/remotes/up/zf/reviews/*"
		if got := ReviewRefs.FetchRefspec("up"); got != want {
			t.Errorf("FetchRefspec = %q, want %q", got, want)
		}
	})
}

func TestChainRefKind(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	kind := func(c *Client, id string) string {
		t.Helper()
		k, err := c.ChainRefKind(ctx, ReviewRefs, id)
		if err != nil {
			t.Fatalf("ChainRefKind(%s): %v", id, err)
		}

		return k
	}

	t.Run("no ref is ChainAbsent", func(t *testing.T) {
		if got := kind(alice, "none"); got != ChainAbsent {
			t.Errorf("kind = %q", got)
		}
	})

	root, err := alice.WriteChainRoot(ctx, []byte(`{}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := alice.PublishChainRoot(ctx, ReviewRefs, "42", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	t.Run("a commit ref is ChainCommits", func(t *testing.T) {
		if got := kind(alice, "42"); got != ChainCommits {
			t.Errorf("kind = %q", got)
		}
	})

	blob, err := alice.outputStdin(ctx, []byte(`{"status":"in_review"}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/reviews/old", blob)

	t.Run("a local blob ref is ChainLegacy", func(t *testing.T) {
		if got := kind(alice, "old"); got != ChainLegacy {
			t.Errorf("kind = %q", got)
		}
	})

	mustGit(t, aliceDir, "push", "-q", "origin", "refs/zf/reviews/old")
	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}

	t.Run("a blob known only through the tracking ref is ChainLegacy", func(t *testing.T) {
		if got := kind(bob, "old"); got != ChainLegacy {
			t.Errorf("kind = %q", got)
		}
	})
}

func TestChainRef_LegacyBlobs(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, bobDir := cloneOf(t, originDir, "bob")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	blob, err := alice.outputStdin(ctx, []byte(`{"status":"in_review"}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/reviews/old", blob)
	mustGit(t, aliceDir, "push", "-q", "origin", "refs/zf/reviews/old")

	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	merged, err := bob.ReconcileChainRefs(ctx, ReviewRefs, merge)

	t.Run("reconcile skips a blob tracking ref", func(t *testing.T) {
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileChainRefs = %d, %v", merged, err)
		}
		if tip, _ := bob.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("reconcile imported the blob: %q", tip)
		}
	})

	// bob still holds the blob locally, as a clone upgraded in place does.
	mustGit(t, bobDir, "update-ref", "refs/zf/reviews/old", blob)

	if err := alice.DeleteChainRef(ctx, ReviewRefs, "old"); err != nil {
		t.Fatalf("DeleteChainRef: %v", err)
	}

	t.Run("DeleteChainRef removes the ref locally and on the remote", func(t *testing.T) {
		if tip, _ := alice.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("local ref survives: %q", tip)
		}
		if refs := originRefs(t, originDir, "refs/zf/reviews/"); len(refs) != 0 {
			t.Errorf("origin still has %v", refs)
		}
	})

	t.Run("DeleteChainRef of a missing ref is not an error", func(t *testing.T) {
		if err := alice.DeleteChainRef(ctx, ReviewRefs, "never-existed"); err != nil {
			t.Errorf("DeleteChainRef: %v", err)
		}
	})

	root, err := alice.WriteChainRoot(ctx, []byte(`{"type":"request"}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := alice.PublishChainRoot(ctx, ReviewRefs, "old", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}
	if err := alice.PushChainRef(ctx, ReviewRefs, "old"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	if err := bob.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	_, err = bob.ReconcileChainRefs(ctx, ReviewRefs, merge)

	t.Run("reconcile replaces a local blob with the remote chain", func(t *testing.T) {
		if err != nil {
			t.Fatalf("ReconcileChainRefs: %v", err)
		}
		if tip, _ := bob.ChainTip(ctx, ReviewRefs, "old"); tip != root {
			t.Errorf("bob tip = %q, want %q", tip, root)
		}
	})
}

func TestChainRef_UnpushedAndNoRemote(t *testing.T) {
	t.Parallel()

	alice, _, _ := newDiskRepoWithOrigin(t)
	solo, _ := newDiskRepo(t)
	ctx := t.Context()

	for _, id := range []string{"10", "42"} {
		root, err := alice.WriteChainRoot(ctx, []byte(`{}`), "request", false)
		if err != nil {
			t.Fatalf("WriteChainRoot: %v", err)
		}
		if err := alice.PublishChainRoot(ctx, ReviewRefs, id, root); err != nil {
			t.Fatalf("PublishChainRoot: %v", err)
		}
	}
	if err := alice.PushChainRef(ctx, ReviewRefs, "10"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	t.Run("UnpushedChainIDs lists the chains the remote lacks", func(t *testing.T) {
		ids, err := alice.UnpushedChainIDs(ctx, ReviewRefs)
		if err != nil || !slices.Equal(ids, []string{"42"}) {
			t.Errorf("UnpushedChainIDs = %v, %v", ids, err)
		}
	})

	root, err := solo.WriteChainRoot(ctx, []byte(`{}`), "request", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := solo.PublishChainRoot(ctx, ReviewRefs, "42", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	t.Run("without a remote, fetch, reconcile and push are no-ops", func(t *testing.T) {
		if err := solo.FetchChainRefs(ctx, ReviewRefs, true); err != nil {
			t.Errorf("FetchChainRefs: %v", err)
		}
		if n, err := solo.ReconcileChainRefs(ctx, ReviewRefs, []byte(`{}`)); err != nil || n != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v", n, err)
		}
		if err := solo.PushChainRef(ctx, ReviewRefs, "42"); err != nil {
			t.Errorf("PushChainRef: %v", err)
		}
	})

	t.Run("without a remote, nothing is unpushed and the kind is still read", func(t *testing.T) {
		if ids, err := solo.UnpushedChainIDs(ctx, ReviewRefs); err != nil || len(ids) != 0 {
			t.Errorf("UnpushedChainIDs = %v, %v", ids, err)
		}
		if k, err := solo.ChainRefKind(ctx, ReviewRefs, "42"); err != nil || k != ChainCommits {
			t.Errorf("ChainRefKind = %q, %v", k, err)
		}
	})
}
