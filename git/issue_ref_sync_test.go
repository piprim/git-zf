package git

import (
	"path/filepath"
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
		pushed, err := alice.IssueRefPushed(ctx, id)
		if err != nil || pushed {
			t.Errorf("IssueRefPushed = %v, %v; want false", pushed, err)
		}
	})

	t.Run("push publishes the ref and marks it pushed", func(t *testing.T) {
		if err := alice.PushIssueRef(ctx, id); err != nil {
			t.Fatalf("PushIssueRef: %v", err)
		}
		pushed, err := alice.IssueRefPushed(ctx, id)
		if err != nil || !pushed {
			t.Errorf("IssueRefPushed = %v, %v; want true", pushed, err)
		}
	})

	t.Run("fetch + reconcile creates the missing local ref on another clone", func(t *testing.T) {
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		tip, _ := bob.IssueTip(ctx, id)
		if tip != id {
			t.Errorf("bob tip = %q, want %q", tip, id)
		}
	})

	t.Run("a local-only issue survives fetch + reconcile", func(t *testing.T) {
		local, err := bob.CreateIssueRef(ctx, []byte(`{"type":"create","n":"offline"}`), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		if _, err := bob.ReconcileIssueRefs(ctx, merge); err != nil {
			t.Fatalf("ReconcileIssueRefs: %v", err)
		}
		if tip, _ := bob.IssueTip(ctx, local); tip != local {
			t.Errorf("local-only issue lost: tip = %q", tip)
		}
	})

	aliceTip, err := alice.AppendIssueCommit(ctx, id, []byte(`{"from":"alice"}`), "add_comment")
	if err != nil {
		t.Fatalf("alice append: %v", err)
	}
	if err := alice.PushIssueRef(ctx, id); err != nil {
		t.Fatalf("alice push: %v", err)
	}

	t.Run("a clone that is behind is fast-forwarded", func(t *testing.T) {
		carol, _ := cloneOf(t, originDir, "carol")
		mustGit(t, carol.root, "update-ref", "refs/zf/issues/"+id, id)

		if err := carol.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := carol.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		if tip, _ := carol.IssueTip(ctx, id); tip != aliceTip {
			t.Errorf("carol tip = %q, want %q", tip, aliceTip)
		}
	})

	bobTip, err := bob.AppendIssueCommit(ctx, id, []byte(`{"from":"bob"}`), "add_comment")
	if err != nil {
		t.Fatalf("bob append: %v", err)
	}

	t.Run("a diverged push is rejected and nothing is overwritten", func(t *testing.T) {
		if err := bob.PushIssueRef(ctx, id); err == nil {
			t.Fatal("expected the non-fast-forward push to fail")
		}
		if err := alice.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("alice fetch: %v", err)
		}
		pushed, _ := alice.IssueRefPushed(ctx, id)
		if !pushed {
			t.Error("origin no longer matches alice's tip")
		}
	})

	t.Run("reconcile merges the diverged chains and the push then succeeds", func(t *testing.T) {
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 1 {
			t.Fatalf("ReconcileIssueRefs = %d, %v; want 1 merge", merged, err)
		}

		commits, err := bob.ReadIssueCommits(ctx, id)
		if err != nil {
			t.Fatalf("ReadIssueCommits: %v", err)
		}
		last := commits[len(commits)-1]
		if len(commits) != 4 || len(last.Parents) != 2 || string(last.Payload) != string(merge) {
			t.Fatalf("chain = %q, last parents = %v", payloads(commits), last.Parents)
		}
		if last.Parents[0] != bobTip || last.Parents[1] != aliceTip {
			t.Errorf("merge parents = %v, want [%s %s]", last.Parents, bobTip, aliceTip)
		}

		if err := bob.PushIssueRef(ctx, id); err != nil {
			t.Fatalf("PushIssueRef after merge: %v", err)
		}
	})

	t.Run("a second reconcile is a no-op", func(t *testing.T) {
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
		}
	})

	t.Run("a clone that is ahead is left alone", func(t *testing.T) {
		ahead, err := bob.AppendIssueCommit(ctx, id, []byte(`{"from":"bob2"}`), "add_comment")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		if tip, _ := bob.IssueTip(ctx, id); tip != ahead {
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
		if err := client.FetchIssueRefs(ctx); err != nil {
			t.Errorf("FetchIssueRefs: %v", err)
		}
	})

	t.Run("push is a no-op", func(t *testing.T) {
		if err := client.PushIssueRef(ctx, id); err != nil {
			t.Errorf("PushIssueRef: %v", err)
		}
	})

	t.Run("the issue counts as pushed", func(t *testing.T) {
		pushed, err := client.IssueRefPushed(ctx, id)
		if err != nil || !pushed {
			t.Errorf("IssueRefPushed = %v, %v", pushed, err)
		}
	})

	t.Run("reconcile with no tracking refs does nothing", func(t *testing.T) {
		merged, err := client.ReconcileIssueRefs(ctx, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
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
		if err := client.FetchIssueRefs(ctx); err != nil {
			t.Errorf("FetchIssueRefs: %v", err)
		}
	})

	t.Run("reconcile has nothing to do", func(t *testing.T) {
		merged, err := client.ReconcileIssueRefs(ctx, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
		}
	})

	t.Run("no issue is listed", func(t *testing.T) {
		ids, err := client.ListIssueIDs(ctx)
		if err != nil || len(ids) != 0 {
			t.Errorf("ListIssueIDs = %v, %v", ids, err)
		}
	})
}
