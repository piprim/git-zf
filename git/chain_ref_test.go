package git

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
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

	t.Run("LegacyBlob reads the local blob and sees none on the remote before a fetch", func(t *testing.T) {
		content, remoteSHA, err := alice.LegacyBlob(ctx, ReviewRefs, "old")
		if err != nil || string(content) != `{"status":"in_review"}` || remoteSHA != "" {
			t.Errorf("LegacyBlob = %q, %q, %v", content, remoteSHA, err)
		}
	})

	t.Run("LegacyBlob falls back to the tracking blob", func(t *testing.T) {
		content, remoteSHA, err := bob.LegacyBlob(ctx, ReviewRefs, "old")
		if err != nil || string(content) != `{"status":"in_review"}` || remoteSHA != blob {
			t.Errorf("LegacyBlob = %q, %q, %v", content, remoteSHA, err)
		}
	})

	if err := alice.DeleteChainRef(ctx, ReviewRefs, "old", ""); err != nil {
		t.Fatalf("DeleteChainRef: %v", err)
	}

	t.Run("DeleteChainRef without a remote blob leaves the remote alone", func(t *testing.T) {
		if tip, _ := alice.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("local ref survives: %q", tip)
		}
		if refs := originRefs(t, originDir, "refs/zf/reviews/"); len(refs) != 1 {
			t.Errorf("origin has %v, want the blob ref", refs)
		}
	})

	mustGit(t, aliceDir, "update-ref", "refs/zf/reviews/old", blob)
	if err := alice.FetchChainRefs(ctx, ReviewRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	_, remoteBlob, err := alice.LegacyBlob(ctx, ReviewRefs, "old")
	if err != nil || remoteBlob != blob {
		t.Fatalf("LegacyBlob after fetch = %q, %v", remoteBlob, err)
	}
	if err := alice.DeleteChainRef(ctx, ReviewRefs, "old", remoteBlob); err != nil {
		t.Fatalf("DeleteChainRef: %v", err)
	}

	t.Run("DeleteChainRef removes the ref locally and the blob seen on the remote", func(t *testing.T) {
		if tip, _ := alice.ChainTip(ctx, ReviewRefs, "old"); tip != "" {
			t.Errorf("local ref survives: %q", tip)
		}
		if refs := originRefs(t, originDir, "refs/zf/reviews/"); len(refs) != 0 {
			t.Errorf("origin still has %v", refs)
		}
	})

	t.Run("DeleteChainRef of a missing ref is not an error", func(t *testing.T) {
		if err := alice.DeleteChainRef(ctx, ReviewRefs, "never-existed", ""); err != nil {
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

func TestChainRef_DeleteLeaseSparesAChainPushedSince(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, bobDir := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	blob, err := alice.outputStdin(ctx, []byte(`{"issue_slug":"77"}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/branches/77", blob)
	mustGit(t, aliceDir, "push", "-q", "origin", "refs/zf/branches/77")

	// bob sees the blob on the remote.
	if err := bob.FetchChainRefs(ctx, BranchRefs, false); err != nil {
		t.Fatalf("bob FetchChainRefs: %v", err)
	}
	_, seen, err := bob.LegacyBlob(ctx, BranchRefs, "77")
	if err != nil || seen != blob {
		t.Fatalf("bob LegacyBlob = %q, %v", seen, err)
	}

	// alice converts first: her chain replaces the blob on the remote.
	if err := alice.FetchChainRefs(ctx, BranchRefs, false); err != nil {
		t.Fatalf("alice FetchChainRefs: %v", err)
	}
	root, err := alice.WriteChainRoot(ctx, []byte(`{"type":"start"}`), "start", false)
	if err != nil {
		t.Fatalf("WriteChainRoot: %v", err)
	}
	if err := alice.DeleteChainRef(ctx, BranchRefs, "77", blob); err != nil {
		t.Fatalf("alice DeleteChainRef: %v", err)
	}
	if err := alice.PublishChainRoot(ctx, BranchRefs, "77", root); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}
	if err := alice.PushChainRef(ctx, BranchRefs, "77"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	// bob deletes with the blob he saw, which the remote no longer holds.
	mustGit(t, bobDir, "update-ref", "refs/zf/branches/77", blob)
	if err := bob.DeleteChainRef(ctx, BranchRefs, "77", seen); err != nil {
		t.Fatalf("bob DeleteChainRef: %v", err)
	}

	t.Run("the chain pushed in the meantime is still on the remote", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", originDir, "rev-parse", "refs/zf/branches/77").Output()
		if err != nil {
			t.Fatalf("rev-parse on origin: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != root {
			t.Errorf("origin ref = %q, want alice's root %q", got, root)
		}
	})
}

func TestChainRef_ReadAllChains(t *testing.T) {
	t.Parallel()

	alice, aliceDir, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	newChain := func(c *Client, id string, payloads ...string) {
		t.Helper()

		root, err := c.WriteChainRoot(ctx, []byte(payloads[0]), "op", false)
		if err != nil {
			t.Fatalf("WriteChainRoot: %v", err)
		}
		if err := c.PublishChainRoot(ctx, BranchRefs, id, root); err != nil {
			t.Fatalf("PublishChainRoot: %v", err)
		}
		for _, p := range payloads[1:] {
			if _, err := c.AppendChainCommit(ctx, BranchRefs, id, []byte(p), "op", false); err != nil {
				t.Fatalf("AppendChainCommit: %v", err)
			}
		}
	}

	// Two chains with no shared history, one of them with a merge commit: both
	// clones create the root of 42, then reconcile joins them.
	newChain(alice, "7", `{"n":1}`, `{"n":2}`, `{"n":3}`)
	newChain(alice, "42", `{"by":"alice"}`)
	if err := alice.PushChainRef(ctx, BranchRefs, "42"); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	newChain(bob, "42", `{"by":"bob"}`)
	if err := bob.FetchChainRefs(ctx, BranchRefs, false); err != nil {
		t.Fatalf("FetchChainRefs: %v", err)
	}
	if n, err := bob.ReconcileChainRefs(ctx, BranchRefs, []byte(`{"type":"merge"}`)); err != nil || n != 1 {
		t.Fatalf("ReconcileChainRefs = %d, %v", n, err)
	}
	newChain(bob, "7", `{"n":1}`, `{"n":2}`)

	blob, err := alice.outputStdin(ctx, []byte(`{}`), "hash-object", "-w", "--stdin")
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	mustGit(t, aliceDir, "update-ref", "refs/zf/branches/old", blob)

	for name, c := range map[string]*Client{"alice": alice, "bob": bob} {
		chains, _, err := c.ReadAllChains(ctx, BranchRefs)
		if err != nil {
			t.Fatalf("%s ReadAllChains: %v", name, err)
		}

		for _, id := range []string{"7", "42"} {
			t.Run(name+" reads chain "+id+" as ReadChainCommits does", func(t *testing.T) {
				want, err := c.ReadChainCommits(ctx, BranchRefs, id)
				if err != nil {
					t.Fatalf("ReadChainCommits: %v", err)
				}

				got := chains[id]
				if len(got) != len(want) {
					t.Fatalf("got %d commits, want %d", len(got), len(want))
				}

				// Both orders put parents first; compare as sets, then check that order.
				seen := map[string]bool{}
				for _, commit := range got {
					for _, p := range commit.Parents {
						if !seen[p] {
							t.Errorf("commit %s comes before its parent %s", commit.ID, p)
						}
					}
					seen[commit.ID] = true

					i := slices.IndexFunc(want, func(w ChainCommit) bool { return w.ID == commit.ID })
					if i < 0 || string(want[i].Payload) != string(commit.Payload) || !slices.Equal(want[i].Parents, commit.Parents) {
						t.Errorf("commit %s = %+v, not in ReadChainCommits output", commit.ID, commit)
					}
				}
			})
		}
	}

	t.Run("a merged chain holds both roots and the merge commit", func(t *testing.T) {
		chains, _, _ := bob.ReadAllChains(ctx, BranchRefs)
		if got := len(chains["42"]); got != 3 {
			t.Errorf("chain 42 has %d commits, want 3", got)
		}
	})

	t.Run("a blob ref is reported as legacy and has no chain", func(t *testing.T) {
		chains, legacy, _ := alice.ReadAllChains(ctx, BranchRefs)
		if !slices.Equal(legacy, []string{"old"}) {
			t.Errorf("legacy = %v", legacy)
		}
		if _, ok := chains["old"]; ok {
			t.Error("the blob ref has a chain")
		}
	})

	t.Run("a repository with no chain returns an empty map", func(t *testing.T) {
		solo, _ := newDiskRepo(t)
		chains, legacy, err := solo.ReadAllChains(ctx, BranchRefs)
		if err != nil || len(chains) != 0 || len(legacy) != 0 {
			t.Errorf("ReadAllChains = %v, %v, %v", chains, legacy, err)
		}
	})
}

func TestChainRef_FixedRoot(t *testing.T) {
	t.Parallel()

	const (
		payload = `{"v":1,"type":"create","at":"2026-10-01T10:00:00Z","tracker_type":"forgejo","project":"zf","tracker_id":"42"}`
		golden  = "619c562f9fa770a8ecd7cdee1f10f4ad168ef053"
	)
	when := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	ctx := t.Context()

	a, _ := newDiskRepo(t)
	b, dirB := newDiskRepo(t)

	// Everything an ordinary commit depends on differs in b.
	for key, value := range map[string]string{
		"user.name": "Somebody Else", "user.email": "else@example.org",
		"commit.gpgsign": "true", "i18n.commitEncoding": "ISO-8859-1",
	} {
		if out, err := exec.CommandContext(ctx, "git", "-C", dirB, "config", key, value).CombinedOutput(); err != nil {
			t.Fatalf("git config %s: %v\n%s", key, err, out)
		}
	}

	rootA, errA := a.WriteFixedChainRoot(ctx, []byte(payload), "create", when)
	rootB, errB := b.WriteFixedChainRoot(ctx, []byte(payload), "create", when)

	t.Run("it succeeds in both repositories", func(t *testing.T) {
		if errA != nil || errB != nil {
			t.Fatalf("WriteFixedChainRoot: %v / %v", errA, errB)
		}
	})
	t.Run("two repositories with different identities get the same commit", func(t *testing.T) {
		if rootA != rootB {
			t.Errorf("roots differ: %s vs %s", rootA, rootB)
		}
	})
	t.Run("the commit ID is the golden hash", func(t *testing.T) {
		if rootA != golden {
			t.Errorf("root = %s, want %s", rootA, golden)
		}
	})
	t.Run("the commit is unsigned and has no parent", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dirB, "cat-file", "-p", rootB).CombinedOutput()
		if err != nil {
			t.Fatalf("cat-file: %v\n%s", err, out)
		}
		if s := string(out); strings.Contains(s, "gpgsig") || strings.Contains(s, "parent ") {
			t.Errorf("commit =\n%s", s)
		}
	})
	t.Run("another date gives another commit", func(t *testing.T) {
		other, err := a.WriteFixedChainRoot(ctx, []byte(payload), "create", when.Add(time.Second))
		if err != nil || other == rootA {
			t.Errorf("other = %s (%v), want a different commit", other, err)
		}
	})
	t.Run("no ref is created", func(t *testing.T) {
		tip, err := a.ChainTip(ctx, IssueRefs, rootA)
		if err != nil || tip != "" {
			t.Errorf("tip = %q (%v), want none", tip, err)
		}
	})
}
