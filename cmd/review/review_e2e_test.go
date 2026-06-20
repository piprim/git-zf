package review

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestTrack_Developer_RegistersUnknownBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Create a feature branch with plain git (not via git zf issue start).
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "X.2@feat@part-two"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack: %v", err)
		}
	})

	t.Run("branch appears in store as in_progress", func(t *testing.T) {
		rows, listErr := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
		if listErr != nil {
			t.Fatalf("ListBranches: %v", listErr)
		}
		var found bool
		for _, r := range rows {
			if r.BranchName == "X.2@feat@part-two" {
				found = true
				break
			}
		}
		if !found {
			t.Error("branch not found in store as in_progress")
		}
	})

	t.Run("output mentions branch name", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "X.2@feat@part-two") {
			t.Errorf("stdout = %q, want it to mention branch name", out)
		}
	})
}

func TestTrack_Developer_IdempotentWhenAlreadyTracked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// The rig already has 77@feat@my-feature in the store; check it out.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("no error on duplicate", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack on already-tracked branch: %v", err)
		}
	})

	t.Run("output says already tracked", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "already tracked") {
			t.Errorf("stdout = %q, want 'already tracked'", out)
		}
	})
}

func TestTrack_Developer_ErrorOnUnrecognizedBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Create a branch with no git-zf naming convention.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "my-random-branch"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("returns error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error for unrecognized branch name, got nil")
		}
	})

	t.Run("error mentions naming convention", func(t *testing.T) {
		if !strings.Contains(err.Error(), "naming convention") {
			t.Errorf("error = %v, want mention of naming convention", err)
		}
	})
}

func TestTrack_Reviewer_RegistersReviewBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Simulate the developer having submitted for review: write an in_review ref.
	featureSHA, shaErr := rig.client.ResolveRef("refs/heads/77@feat@my-feature")
	if shaErr != nil {
		t.Fatalf("ResolveRef: %v", shaErr)
	}
	ref := git.ReviewRef{
		Status:     "in_review",
		Round:      1,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  "2026-06-20T10:00:00Z",
	}
	if _, err := rig.client.WriteReviewRef(ctx, "77", ref, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	// Reviewer creates branch manually (no git zf review start).
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "77@review"); err != nil {
		t.Fatalf("checkout review branch: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack reviewer path: %v", err)
		}
	})

	t.Run("review record exists in store", func(t *testing.T) {
		latest, getErr := rig.store.GetLatestReview(ctx, "77")
		if getErr != nil {
			t.Fatalf("GetLatestReview: %v", getErr)
		}
		if latest == nil {
			t.Fatal("expected review row, got nil")
		}
	})

	t.Run("reviewer identity recorded", func(t *testing.T) {
		latest, _ := rig.store.GetLatestReview(ctx, "77")
		if latest != nil && latest.Reviewer == "" {
			t.Error("reviewer identity not recorded")
		}
	})

	t.Run("output mentions registered", func(t *testing.T) {
		if out := rig.stdout.String(); !strings.Contains(out, "registered") {
			t.Errorf("stdout = %q, want 'registered'", out)
		}
	})
}

func TestTrack_Reviewer_ErrorWhenNoRefExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rig := newReviewE2ERig(t)

	// Checkout a review branch manually — but no review ref exists.
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "-b", "77@review"); err != nil {
		t.Fatalf("checkout: %v", err)
	}

	err := runTrack(ctx, rig.deps())

	t.Run("returns error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error when no review ref exists, got nil")
		}
	})
}
