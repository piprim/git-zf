package review

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

type reviewE2ERig struct {
	dir    string
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (r *reviewE2ERig) deps() reviewDeps {
	return reviewDeps{client: r.client, store: r.store, cfg: r.cfg}
}

func newReviewE2ERig(t *testing.T) *reviewE2ERig {
	t.Helper()

	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@test.com")
	run("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")

	run("checkout", "-b", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	run("add", "feature.txt")
	run("commit", "-m", "feat: my feature")
	run("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	client, err := git.NewClientAt(&pkg.IO{
		In:  bytes.NewReader(nil),
		Out: stdout,
		Err: stderr,
	}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	gitDir := filepath.Join(dir, ".git")
	s, err := store.Open(t.Context(), gitDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "77", Title: "my feature", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "77@feat@my-feature", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	return &reviewE2ERig{
		dir: dir, client: client, store: s, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

func TestReviewLifecycle_RequestApprove(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	t.Run("review request locks branch and creates review record", func(t *testing.T) {
		// Checkout the feature branch (runReviewRequestInteractive reads current branch).
		if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
			t.Fatalf("checkout feature branch: %v", err)
		}

		// The scripted prompter pre-selects the feature branch for issue "77".
		branches, err := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		var picked store.BranchRow
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
				break
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive: %v", err)
		}

		latest, err := rig.store.GetLatestReview(ctx, "77")
		if err != nil {
			t.Fatalf("GetLatestReview: %v", err)
		}

		t.Run("review record exists", func(t *testing.T) {
			if latest == nil {
				t.Fatal("expected review row, got nil")
			}
		})
		t.Run("status is in_review", func(t *testing.T) {
			if latest.Status != store.ReviewStatusInReview {
				t.Errorf("status: got %q, want %q", latest.Status, store.ReviewStatusInReview)
			}
		})
		t.Run("round is 1", func(t *testing.T) {
			if latest.Round != 1 {
				t.Errorf("round: got %d, want 1", latest.Round)
			}
		})
	})

	t.Run("review approve transitions to approved", func(t *testing.T) {
		latest, err := rig.store.GetLatestReview(ctx, "77")
		if err != nil || latest == nil {
			t.Fatalf("GetLatestReview: %v (row=%v)", err, latest)
		}

		if err := rig.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusApproved, false); err != nil {
			t.Fatalf("UpdateReviewStatus: %v", err)
		}

		updated, err := rig.store.GetLatestReview(ctx, "77")
		if err != nil {
			t.Fatalf("GetLatestReview after approve: %v", err)
		}

		t.Run("status is approved", func(t *testing.T) {
			if updated.Status != store.ReviewStatusApproved {
				t.Errorf("status: got %q, want %q", updated.Status, store.ReviewStatusApproved)
			}
		})
	})

	t.Run("review command group exposes all subcommands", func(t *testing.T) {
		rv := New(rig.cfg)
		cmd := rv.GetRootCmd()

		if cmd.Use != "review" {
			t.Errorf("Use: got %q, want %q", cmd.Use, "review")
		}

		subNames := make(map[string]bool)
		for _, sub := range cmd.Commands() {
			subNames[sub.Use] = true
		}

		for _, want := range []string{
			"request", "start", "approve", "reject", "list", "status", "fetch", "sync",
		} {
			t.Run("has subcommand "+want, func(t *testing.T) {
				if !subNames[want] {
					t.Errorf("missing subcommand %q", want)
				}
			})
		}
	})
}

func TestReviewLifecycle_RequestReject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	t.Run("reject after request unlocks branch for next iteration", func(t *testing.T) {
		if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
			t.Fatalf("checkout feature branch: %v", err)
		}
		branches, _ := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		var picked store.BranchRow
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive round 1: %v", err)
		}

		latest, _ := rig.store.GetLatestReview(ctx, "77")
		if latest == nil {
			t.Fatal("review row not found after request")
		}

		if err := rig.store.UpdateReviewStatus(ctx, latest.ID, store.ReviewStatusChangesRequested, false); err != nil {
			t.Fatalf("UpdateReviewStatus to changes_requested: %v", err)
		}
	})

	t.Run("second request increments round counter", func(t *testing.T) {
		branches, _ := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		var picked store.BranchRow
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive round 2: %v", err)
		}

		latest, err := rig.store.GetLatestReview(ctx, "77")
		if err != nil {
			t.Fatalf("GetLatestReview: %v", err)
		}

		t.Run("round is 2", func(t *testing.T) {
			if latest.Round != 2 {
				t.Errorf("round: got %d, want 2", latest.Round)
			}
		})
	})

	t.Run("ListReviews returns full history ordered newest first", func(t *testing.T) {
		rows, err := rig.store.ListReviews(ctx, "77")
		if err != nil {
			t.Fatalf("ListReviews: %v", err)
		}
		t.Run("two rounds recorded", func(t *testing.T) {
			if len(rows) != 2 {
				t.Errorf("len: got %d, want 2", len(rows))
			}
		})
		t.Run("newest round first", func(t *testing.T) {
			if len(rows) > 0 && rows[0].Round != 2 {
				t.Errorf("rows[0].Round = %d, want 2", rows[0].Round)
			}
		})
	})
}

// newReviewE2ERigWithOrigin builds a rig that has a bare remote (origin).
// This is required to exercise PushReviewRef — without a remote the push
// is a no-op and lease-correctness bugs are invisible.
func newReviewE2ERigWithOrigin(t *testing.T) *reviewE2ERig {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	cloneDir := t.TempDir()

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// Create bare origin.
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}
	run(originDir, "init", "--bare", "--initial-branch=main")

	// Seed the clone from scratch (no intermediate seed repo needed).
	run(cloneDir, "init", "--initial-branch=main")
	run(cloneDir, "config", "user.name", "Test User")
	run(cloneDir, "config", "user.email", "test@test.com")
	run(cloneDir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(cloneDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	run(cloneDir, "add", "base.txt")
	run(cloneDir, "commit", "-m", "chore: init")
	run(cloneDir, "remote", "add", "origin", originDir)
	run(cloneDir, "push", "origin", "main")

	// Create feature branch and push it.
	run(cloneDir, "checkout", "-b", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(cloneDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	run(cloneDir, "add", "feature.txt")
	run(cloneDir, "commit", "-m", "feat: my feature")
	run(cloneDir, "push", "origin", "77@feat@my-feature")
	run(cloneDir, "checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	client, err := git.NewClientAt(&pkg.IO{
		In:  bytes.NewReader(nil),
		Out: stdout,
		Err: stderr,
	}, cloneDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	gitDir := filepath.Join(cloneDir, ".git")
	s, err := store.Open(t.Context(), gitDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "77", Title: "my feature", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "77@feat@my-feature", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	return &reviewE2ERig{
		dir: cloneDir, client: client, store: s, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

// readRemoteReviewRef reads refs/zf/reviews/<issueID> from the bare origin
// directory by shelling out to git cat-file, bypassing the local ref store.
func readRemoteReviewRef(t *testing.T, originDir, issueID string) *git.ReviewRef {
	t.Helper()

	refName := "refs/zf/reviews/" + issueID

	// Resolve ref to SHA in the bare repo.
	shaCmd := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"show-ref", "--verify", "--hash", refName)
	shaOut, err := shaCmd.Output()
	if err != nil {
		return nil // ref does not exist
	}
	sha := string(shaOut[:len(shaOut)-1]) // trim newline

	// Read blob.
	blobCmd := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"cat-file", "blob", sha)
	blobOut, err := blobCmd.Output()
	if err != nil {
		t.Fatalf("cat-file blob %s: %v", sha, err)
	}

	var ref git.ReviewRef
	if err := json.Unmarshal(blobOut, &ref); err != nil {
		t.Fatalf("unmarshal remote review ref: %v", err)
	}
	return &ref
}

// TestReviewRefPush_LeaseCorrectness verifies that the --force-with-lease SHA
// passed to PushReviewRef is the remote's current value (not the new local SHA).
// Without a remote, PushReviewRef is a no-op and this class of bug is invisible.
func TestReviewRefPush_LeaseCorrectness(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	originDir := filepath.Join(t.TempDir(), "origin.git")

	// We need originDir before the rig creates it, so build rig first then
	// capture the origin path from the rig's remote config.
	rig := newReviewE2ERigWithOrigin(t)

	// Resolve the actual origin path from the rig's git config.
	remoteCmd := exec.CommandContext(ctx, "git", "-C", rig.dir,
		"remote", "get-url", "origin")
	remoteOut, err := remoteCmd.Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir = string(remoteOut[:len(remoteOut)-1]) // trim newline

	t.Run("review request pushes in_review ref to remote", func(t *testing.T) {
		if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
			t.Fatalf("checkout feature branch: %v", err)
		}

		branches, _ := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		var picked store.BranchRow
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive: %v", err)
		}

		t.Run("remote ref exists", func(t *testing.T) {
			ref := readRemoteReviewRef(t, originDir, "77")
			if ref == nil {
				t.Fatal("refs/zf/reviews/77 not found on remote after request")
			}
		})
		t.Run("remote ref status is in_review", func(t *testing.T) {
			ref := readRemoteReviewRef(t, originDir, "77")
			if ref != nil && ref.Status != "in_review" {
				t.Errorf("remote ref status: got %q, want %q", ref.Status, "in_review")
			}
		})
	})

	t.Run("review approve pushes approved ref to remote", func(t *testing.T) {
		latest, err := rig.store.GetLatestReview(ctx, "77")
		if err != nil || latest == nil {
			t.Fatalf("GetLatestReview: %v (row=%v)", err, latest)
		}

		p := &scriptedReviewPrompter{Branch: &store.BranchRow{IssueSlug: "77", BranchName: "77@feat@my-feature"}}
		if err := runReviewApproveInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewApproveInteractive: %v", err)
		}

		t.Run("remote ref exists after approve", func(t *testing.T) {
			ref := readRemoteReviewRef(t, originDir, "77")
			if ref == nil {
				t.Fatal("refs/zf/reviews/77 not found on remote after approve")
			}
		})
		t.Run("remote ref status is approved", func(t *testing.T) {
			ref := readRemoteReviewRef(t, originDir, "77")
			if ref != nil && ref.Status != "approved" {
				t.Errorf("remote ref status: got %q, want %q", ref.Status, "approved")
			}
		})
	})
}
