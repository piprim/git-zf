package git

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// cloneOf clones originDir into a fresh directory and returns a client on it.
func cloneOf(t *testing.T, originDir, user string) (*Client, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), user)
	mustGit(t, filepath.Dir(dir), "clone", "-q", originDir, dir)
	mustGit(t, dir, "config", "user.name", user)
	mustGit(t, dir, "config", "user.email", user+"@test.com")
	mustGit(t, dir, "config", "commit.gpgsign", "false")

	c, err := NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func TestIssueRef_FetchReconcilePush(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("an unpushed issue is reported as not pushed", func(t *testing.T) {
		pushed, err := alice.ChainRefPushed(ctx, IssueRefs, id)
		if err != nil || pushed {
			t.Errorf("ChainRefPushed = %v, %v; want false", pushed, err)
		}
	})

	t.Run("push publishes the ref and marks it pushed", func(t *testing.T) {
		if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
			t.Fatalf("PushChainRef: %v", err)
		}
		pushed, err := alice.ChainRefPushed(ctx, IssueRefs, id)
		if err != nil || !pushed {
			t.Errorf("ChainRefPushed = %v, %v; want true", pushed, err)
		}
	})

	t.Run("fetch + reconcile creates the missing local ref on another clone", func(t *testing.T) {
		if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("FetchChainRefs: %v", err)
		}
		merged, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileChainRefs = %d, %v", merged, err)
		}
		tip, _ := bob.ChainTip(ctx, IssueRefs, id)
		if tip != id {
			t.Errorf("bob tip = %q, want %q", tip, id)
		}
	})

	t.Run("a local-only issue survives fetch + reconcile", func(t *testing.T) {
		local, err := bob.CreateIssueRef(ctx, []byte(`{"type":"create","n":"offline"}`), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("FetchChainRefs: %v", err)
		}
		if _, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge); err != nil {
			t.Fatalf("ReconcileChainRefs: %v", err)
		}
		if tip, _ := bob.ChainTip(ctx, IssueRefs, local); tip != local {
			t.Errorf("local-only issue lost: tip = %q", tip)
		}
	})

	aliceTip, err := alice.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"from":"alice"}`), "add_comment", false)
	if err != nil {
		t.Fatalf("alice append: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("alice push: %v", err)
	}

	t.Run("a clone that is behind is fast-forwarded", func(t *testing.T) {
		carol, _ := cloneOf(t, originDir, "carol")
		mustGit(t, carol.root, "update-ref", "refs/zf/issues/"+id, id)

		if err := carol.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("FetchChainRefs: %v", err)
		}
		merged, err := carol.ReconcileChainRefs(ctx, IssueRefs, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileChainRefs = %d, %v", merged, err)
		}
		if tip, _ := carol.ChainTip(ctx, IssueRefs, id); tip != aliceTip {
			t.Errorf("carol tip = %q, want %q", tip, aliceTip)
		}
	})

	bobTip, err := bob.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"from":"bob"}`), "add_comment", false)
	if err != nil {
		t.Fatalf("bob append: %v", err)
	}

	t.Run("a diverged push is rejected and nothing is overwritten", func(t *testing.T) {
		if err := bob.PushChainRef(ctx, IssueRefs, id); err == nil {
			t.Fatal("expected the non-fast-forward push to fail")
		}
		if err := alice.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("alice fetch: %v", err)
		}
		pushed, _ := alice.ChainRefPushed(ctx, IssueRefs, id)
		if !pushed {
			t.Error("origin no longer matches alice's tip")
		}
	})

	t.Run("reconcile merges the diverged chains and the push then succeeds", func(t *testing.T) {
		if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("FetchChainRefs: %v", err)
		}
		merged, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge)
		if err != nil || merged != 1 {
			t.Fatalf("ReconcileChainRefs = %d, %v; want 1 merge", merged, err)
		}

		commits, err := bob.ReadChainCommits(ctx, IssueRefs, id)
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		last := commits[len(commits)-1]
		if len(commits) != 4 || len(last.Parents) != 2 || string(last.Payload) != string(merge) {
			t.Fatalf("chain = %q, last parents = %v", payloads(commits), last.Parents)
		}
		if last.Parents[0] != bobTip || last.Parents[1] != aliceTip {
			t.Errorf("merge parents = %v, want [%s %s]", last.Parents, bobTip, aliceTip)
		}

		if err := bob.PushChainRef(ctx, IssueRefs, id); err != nil {
			t.Fatalf("PushChainRef after merge: %v", err)
		}
	})

	t.Run("a second reconcile is a no-op", func(t *testing.T) {
		merged, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge)
		if err != nil || merged != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v", merged, err)
		}
	})

	t.Run("a clone that is ahead is left alone", func(t *testing.T) {
		ahead, err := bob.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"from":"bob2"}`), "add_comment", false)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("FetchChainRefs: %v", err)
		}
		merged, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileChainRefs = %d, %v", merged, err)
		}
		if tip, _ := bob.ChainTip(ctx, IssueRefs, id); tip != ahead {
			t.Errorf("tip = %q, want %q", tip, ahead)
		}
	})
}

func TestIssueRef_NoRemote(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("fetch is a no-op", func(t *testing.T) {
		if err := client.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Errorf("FetchChainRefs: %v", err)
		}
	})

	t.Run("push is a no-op", func(t *testing.T) {
		if err := client.PushChainRef(ctx, IssueRefs, id); err != nil {
			t.Errorf("PushChainRef: %v", err)
		}
	})

	t.Run("the issue counts as pushed", func(t *testing.T) {
		pushed, err := client.ChainRefPushed(ctx, IssueRefs, id)
		if err != nil || !pushed {
			t.Errorf("ChainRefPushed = %v, %v", pushed, err)
		}
	})

	t.Run("reconcile with no tracking refs does nothing", func(t *testing.T) {
		merged, err := client.ReconcileChainRefs(ctx, IssueRefs, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v", merged, err)
		}
	})
}

// The first user of the feature fetches from a remote that has no
// refs/zf/issues/* at all.
func TestIssueRef_RemoteWithoutIssueRefs(t *testing.T) {
	t.Parallel()

	client, _, _ := newDiskRepoWithOrigin(t)
	ctx := t.Context()

	t.Run("fetch succeeds", func(t *testing.T) {
		if err := client.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Errorf("FetchChainRefs: %v", err)
		}
	})

	t.Run("reconcile has nothing to do", func(t *testing.T) {
		merged, err := client.ReconcileChainRefs(ctx, IssueRefs, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v", merged, err)
		}
	})

	t.Run("no issue is listed", func(t *testing.T) {
		ids, err := client.ListChainIDs(ctx, IssueRefs)
		if err != nil || len(ids) != 0 {
			t.Errorf("ListChainIDs = %v, %v", ids, err)
		}
	})
}

func TestPushChainRefs(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	ctx := t.Context()

	var ids []string
	for i := range 3 {
		root, err := alice.WriteChainRoot(ctx, []byte(fmt.Sprintf(`{"type":"create","n":%d}`, i)), "create", false)
		if err != nil {
			t.Fatalf("WriteChainRoot: %v", err)
		}
		if err := alice.PublishChainRoot(ctx, IssueRefs, root, root); err != nil {
			t.Fatalf("PublishChainRoot: %v", err)
		}
		ids = append(ids, root)
	}

	t.Run("no ids is a no-op", func(t *testing.T) {
		if err := alice.PushChainRefs(ctx, IssueRefs, nil); err != nil {
			t.Errorf("PushChainRefs(nil) = %v", err)
		}
	})

	err := alice.PushChainRefs(ctx, IssueRefs, ids)

	t.Run("one push sends every ref", func(t *testing.T) {
		if err != nil {
			t.Fatalf("PushChainRefs: %v", err)
		}
		if got := originRefs(t, originDir, "refs/zf/issues/"); len(got) != 3 {
			t.Errorf("origin has %d issue refs, want 3: %v", len(got), got)
		}
	})
	t.Run("the tracking refs are moved", func(t *testing.T) {
		for _, id := range ids {
			if pushed, err := alice.ChainRefPushed(ctx, IssueRefs, id); err != nil || !pushed {
				t.Errorf("ChainRefPushed(%s) = %v, %v", id, pushed, err)
			}
		}
	})
	t.Run("a duplicate id is not rejected", func(t *testing.T) {
		if err := alice.PushChainRefs(ctx, IssueRefs, []string{ids[0], ids[1], ids[0]}); err != nil {
			t.Errorf("PushChainRefs(duplicate) = %v", err)
		}
	})
	t.Run("an unknown id is an error", func(t *testing.T) {
		err := alice.PushChainRefs(ctx, IssueRefs, []string{"0000000000000000000000000000000000000000"})
		if !errors.Is(err, ErrIssueNotFound) {
			t.Errorf("PushChainRefs: err = %v, want ErrIssueNotFound", err)
		}
	})
	t.Run("a ref that moved on the remote is rejected", func(t *testing.T) {
		// Point origin's first ref at another chain's commit: alice's tip is
		// no longer a fast-forward of it.
		mustGit(t, originDir, "update-ref", "refs/zf/issues/"+ids[0], ids[1])
		if err := alice.PushChainRefs(ctx, IssueRefs, ids[:2]); err == nil {
			t.Error("PushChainRefs: want a rejection, got nil")
		}
	})
}

// A chain force-pushed under an issue ID it does not have as a root is not
// merged in: an issue's chain always has its ID as its root.
func TestIssueRef_ReconcileSkipsForeignChain(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	carol, carolDir := cloneOf(t, originDir, "carol")
	dave, _ := cloneOf(t, originDir, "dave")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
		t.Fatalf("bob fetch: %v", err)
	}
	if _, err := bob.ReconcileChainRefs(ctx, IssueRefs, merge); err != nil {
		t.Fatalf("bob reconcile: %v", err)
	}

	foreign, err := carol.CreateIssueRef(ctx, []byte(`{"type":"create","by":"carol"}`), "create")
	if err != nil {
		t.Fatalf("carol CreateIssueRef: %v", err)
	}
	mustGit(t, carolDir, "push", "-q", "--force", "origin", "refs/zf/issues/"+foreign+":refs/zf/issues/"+id)

	// A legitimate issue pushed alongside: the foreign chain must not block it.
	other, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create","n":2}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, other); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}

	for name, c := range map[string]*Client{"a clone with the issue": bob, "a clone without it": dave} {
		t.Run(name+" leaves the foreign chain out", func(t *testing.T) {
			if err := c.FetchChainRefs(ctx, IssueRefs, false); err != nil {
				t.Fatalf("fetch: %v", err)
			}
			before, _ := c.ChainTip(ctx, IssueRefs, id)

			merged, err := c.ReconcileChainRefs(ctx, IssueRefs, merge)
			if !errors.Is(err, ErrForeignChain) || merged != 0 {
				t.Fatalf("ReconcileChainRefs = %d, %v; want 0, ErrForeignChain", merged, err)
			}
			if tip, _ := c.ChainTip(ctx, IssueRefs, id); tip != before {
				t.Errorf("tip = %q, want %q (unchanged)", tip, before)
			}
			if tip, _ := c.ChainTip(ctx, IssueRefs, other); tip != other {
				t.Errorf("other issue tip = %q, want %q", tip, other)
			}
		})
	}
}

func TestIssueRef_RepairForeignChain(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	// setup pushes an issue from alice, then has carol force-push a foreign
	// chain over it on the origin, and fetches it into alice.
	setup := func(t *testing.T) (alice, carol *Client, carolDir, id, foreign string) {
		t.Helper()

		alice, _, originDir := newDiskRepoWithOrigin(t)
		carol, carolDir = cloneOf(t, originDir, "carol")

		id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
			t.Fatalf("PushChainRef: %v", err)
		}
		foreign, err = carol.CreateIssueRef(ctx, []byte(`{"type":"create","by":"carol"}`), "create")
		if err != nil {
			t.Fatalf("carol CreateIssueRef: %v", err)
		}
		mustGit(t, carolDir, "push", "-q", "--force", "origin", "refs/zf/issues/"+foreign+":refs/zf/issues/"+id)

		if err := alice.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if _, err := alice.ReconcileChainRefs(ctx, IssueRefs, merge); !errors.Is(err, ErrForeignChain) {
			t.Fatalf("ReconcileChainRefs err = %v, want ErrForeignChain", err)
		}

		return alice, carol, carolDir, id, foreign
	}

	t.Run("the local chain is pushed back and marked pushed", func(t *testing.T) {
		alice, _, _, id, _ := setup(t)

		ids, leftOut, err := alice.RepairForeignChainRefs(ctx, IssueRefs)
		if err != nil || len(ids) != 1 || ids[0] != id || leftOut != nil {
			t.Fatalf("RepairForeignChainRefs = %v, %v, %v; want [%s]", ids, leftOut, err, id)
		}
		if pushed, _ := alice.ChainRefPushed(ctx, IssueRefs, id); !pushed {
			t.Error("issue not marked pushed")
		}
		if _, err := alice.ReconcileChainRefs(ctx, IssueRefs, merge); err != nil {
			t.Errorf("reconcile after repair: %v", err)
		}
	})

	t.Run("a remote ref that moved since the fetch is not overwritten", func(t *testing.T) {
		alice, carol, carolDir, id, foreign := setup(t)

		moved, err := carol.output(ctx, "commit-tree", "-p", foreign, "-m", "moved", foreign+"^{tree}")
		if err != nil {
			t.Fatalf("commit-tree: %v", err)
		}
		mustGit(t, carolDir, "push", "-q", "--force", "origin", moved+":refs/zf/issues/"+id)

		if _, _, err := alice.RepairForeignChainRefs(ctx, IssueRefs); err == nil {
			t.Fatal("expected the leased push to be refused")
		}
		if got, _ := carol.output(ctx, "ls-remote", "origin", "refs/zf/issues/"+id); !strings.HasPrefix(got, moved) {
			t.Errorf("origin ref = %q, want %s", got, moved)
		}
	})

	t.Run("other families are never repaired", func(t *testing.T) {
		alice, _, _, _, _ := setup(t)

		if ids, _, err := alice.RepairForeignChainRefs(ctx, ReviewRefs); err != nil || ids != nil {
			t.Errorf("RepairForeignChainRefs(reviews) = %v, %v", ids, err)
		}
	})

	t.Run("a foreign chain the clone has no issue for is left out, named as junk", func(t *testing.T) {
		alice, _, carolDir, _, foreign := setup(t)
		mustGit(t, carolDir, "push", "-q", "origin", "refs/zf/issues/"+foreign+":refs/zf/issues/junk")
		if err := alice.FetchChainRefs(ctx, IssueRefs, true); err != nil {
			t.Fatalf("fetch: %v", err)
		}

		_, leftOut, err := alice.RepairForeignChainRefs(ctx, IssueRefs)
		if err != nil || len(leftOut) != 1 {
			t.Fatalf("RepairForeignChainRefs leftOut = %v, %v; want one line", leftOut, err)
		}
		if !strings.Contains(leftOut[0], "git push origin --delete refs/zf/issues/junk") {
			t.Errorf("leftOut = %q, want the delete command", leftOut[0])
		}
	})
}

// A clone on an older git-zf, which does not check the roots, merges a foreign
// chain into an issue and pushes the result: the real root is still in it, but
// not alone. It is neither merged in nor pushed over.
func TestIssueRef_ForeignChainMergedByOlderClone(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	carol, carolDir := cloneOf(t, originDir, "carol")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	for _, c := range []*Client{bob, carol} {
		if err := c.FetchChainRefs(ctx, IssueRefs, false); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if _, err := c.ReconcileChainRefs(ctx, IssueRefs, merge); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}

	// Carol's older git-zf: a merge of the issue with an unrelated chain,
	// pushed as a fast-forward.
	foreign, err := carol.CreateIssueRef(ctx, []byte(`{"type":"create","by":"carol"}`), "create")
	if err != nil {
		t.Fatalf("carol CreateIssueRef: %v", err)
	}
	mixed, err := carol.writeChainCommit(ctx, merge, "merge", false, id, foreign)
	if err != nil {
		t.Fatalf("writeChainCommit: %v", err)
	}
	mustGit(t, carolDir, "push", "-q", "origin", mixed+":refs/zf/issues/"+id)

	if err := bob.FetchChainRefs(ctx, IssueRefs, false); err != nil {
		t.Fatalf("bob fetch: %v", err)
	}
	merged, reconcileErr := bob.ReconcileChainRefs(ctx, IssueRefs, merge)

	t.Run("reconcile refuses the chain with two roots", func(t *testing.T) {
		if !errors.Is(reconcileErr, ErrForeignChain) || merged != 0 {
			t.Errorf("ReconcileChainRefs = %d, %v; want 0, ErrForeignChain", merged, reconcileErr)
		}
		if tip, _ := bob.ChainTip(ctx, IssueRefs, id); tip != id {
			t.Errorf("bob tip = %q, want %q (unchanged)", tip, id)
		}
	})

	t.Run("repair leaves it for a fix by hand, and says so", func(t *testing.T) {
		ids, leftOut, err := bob.RepairForeignChainRefs(ctx, IssueRefs)
		if err != nil || ids != nil || len(leftOut) != 1 || !strings.Contains(leftOut[0], "by hand") {
			t.Errorf("RepairForeignChainRefs = %v, %v, %v; want nothing repaired, one line", ids, leftOut, err)
		}
		if got, _ := carol.output(ctx, "ls-remote", "origin", "refs/zf/issues/"+id); !strings.HasPrefix(got, mixed) {
			t.Errorf("origin ref = %q, want %s", got, mixed)
		}
	})
}

func TestRemoteDown(t *testing.T) {
	t.Parallel()

	alice, cloneDir, originDir := newDiskRepoWithOrigin(t)
	ctx := t.Context()

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	// The remote dies (exit 128), then comes back: a process does not retry.
	mustGit(t, cloneDir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	first := alice.FetchChainRefs(ctx, IssueRefs, true)
	mustGit(t, cloneDir, "remote", "set-url", "origin", originDir)

	t.Run("the first failure is git's", func(t *testing.T) {
		if first == nil || errors.Is(first, ErrRemoteDown) {
			t.Fatalf("FetchChainRefs = %v, want git's error", first)
		}
	})
	t.Run("a later fetch does not run git", func(t *testing.T) {
		if err := alice.FetchChainRefs(ctx, IssueRefs, false); !errors.Is(err, ErrRemoteDown) {
			t.Errorf("FetchChainRefs = %v, want ErrRemoteDown", err)
		}
		if err := alice.Fetch(ctx); !errors.Is(err, ErrRemoteDown) {
			t.Errorf("Fetch = %v, want ErrRemoteDown", err)
		}
	})
	t.Run("a later push does not run git", func(t *testing.T) {
		if err := alice.PushChainRef(ctx, IssueRefs, id); !errors.Is(err, ErrRemoteDown) {
			t.Errorf("PushChainRef = %v, want ErrRemoteDown", err)
		}
		if err := alice.PushChainRefs(ctx, IssueRefs, []string{id}); !errors.Is(err, ErrRemoteDown) {
			t.Errorf("PushChainRefs = %v, want ErrRemoteDown", err)
		}
		if got := originRefs(t, originDir, "refs/zf/issues/"); len(got) != 0 {
			t.Errorf("origin has %v, want nothing pushed", got)
		}
	})
	t.Run("a new process tries again", func(t *testing.T) {
		fresh, err := NewClientAt(nil, cloneDir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		if err := fresh.PushChainRef(ctx, IssueRefs, id); err != nil {
			t.Errorf("PushChainRef = %v", err)
		}
	})
}

func TestRemoteDown_rejectedPushIsNotDown(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}
	if err := alice.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("PushChainRef: %v", err)
	}
	if err := bob.FetchChains(ctx, IssueRefs, []byte(`{"type":"merge"}`), true); err != nil {
		t.Fatalf("bob FetchChains: %v", err)
	}
	for _, c := range []*Client{alice, bob} {
		if _, err := c.AppendChainCommit(ctx, IssueRefs, id, []byte(`{"type":"comment"}`), "comment", false); err != nil {
			t.Fatalf("AppendChainRef: %v", err)
		}
	}
	if err := bob.PushChainRef(ctx, IssueRefs, id); err != nil {
		t.Fatalf("bob PushChainRef: %v", err)
	}

	rejected := alice.PushChainRef(ctx, IssueRefs, id)
	if rejected == nil || errors.Is(rejected, ErrRemoteDown) {
		t.Fatalf("PushChainRef = %v, want a rejection", rejected)
	}
	t.Run("the fetch after a rejection runs", func(t *testing.T) {
		if err := alice.FetchChainRefs(ctx, IssueRefs, true); err != nil {
			t.Errorf("FetchChainRefs = %v", err)
		}
	})
}
