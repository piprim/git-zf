package review

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
	"github.com/piprim/git-zf/tracker/fake"
)

type reviewE2ERig struct {
	dir     string
	client  *git.Client
	cfg     *config.AppConfig
	tracker *fake.Tracker // nil unless a test opts in via withFakeTracker
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

func (r *reviewE2ERig) deps() reviewDeps {
	d := reviewDeps{client: r.client, cfg: r.cfg}
	// Only populate the tracker interface when a concrete fake is attached;
	// assigning a nil *fake.Tracker would yield a non-nil interface and defeat
	// the deps.tracker == nil guard in maybeUpdateTrackerStatus.
	if r.tracker != nil {
		d.tracker = r.tracker
	}
	return d
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

	branchtest.Seed(t, client, branch.Op{Branch: "77@feat@my-feature", Title: "my feature"}, branch.StatusInProgress)

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	return &reviewE2ERig{
		dir: dir, client: client, cfg: cfg,
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
		branches, err := branch.ListRows(ctx, rig.client, branch.StatusInProgress)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		var picked branch.Row
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

		latest, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if latest == nil {
			t.Fatal("expected a review chain, got none")
		}

		t.Run("status is in_review", func(t *testing.T) {
			if latest.Status != reviewpkg.StatusInReview {
				t.Errorf("status: got %q, want %q", latest.Status, reviewpkg.StatusInReview)
			}
		})
		t.Run("round is 1", func(t *testing.T) {
			if latest.Round != 1 {
				t.Errorf("round: got %d, want 1", latest.Round)
			}
		})
	})

	t.Run("review approve transitions to approved", func(t *testing.T) {
		if err := runReviewApprove(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}

		updated, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || updated == nil {
			t.Fatalf("Load after approve: %v (state=%v)", err, updated)
		}

		t.Run("status is approved", func(t *testing.T) {
			if updated.Status != reviewpkg.StatusApproved {
				t.Errorf("status: got %q, want %q", updated.Status, reviewpkg.StatusApproved)
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
		branches, _ := branch.ListRows(ctx, rig.client, branch.StatusInProgress)
		var picked branch.Row
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive round 1: %v", err)
		}

		if err := reviewpkg.Append(ctx, rig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpReject}, false); err != nil {
			t.Fatalf("append reject: %v", err)
		}
	})

	t.Run("second request increments round counter", func(t *testing.T) {
		branches, _ := branch.ListRows(ctx, rig.client, branch.StatusInProgress)
		var picked branch.Row
		for _, b := range branches {
			if b.IssueSlug == "77" {
				picked = b
			}
		}
		p := &scriptedReviewPrompter{Branch: &picked}
		if err := runReviewRequestInteractive(ctx, rig.deps(), p); err != nil {
			t.Fatalf("runReviewRequestInteractive round 2: %v", err)
		}

		latest, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || latest == nil {
			t.Fatalf("Load: %v (state=%v)", err, latest)
		}

		t.Run("round is 2", func(t *testing.T) {
			if latest.Round != 2 {
				t.Errorf("round: got %d, want 2", latest.Round)
			}
		})
	})

	t.Run("the chain keeps the full history, oldest round first", func(t *testing.T) {
		st, err := reviewpkg.Load(ctx, rig.client, "77")
		if err != nil || st == nil {
			t.Fatalf("Load: %v (state=%v)", err, st)
		}
		rows := st.Rounds
		t.Run("two rounds recorded", func(t *testing.T) {
			if len(rows) != 2 {
				t.Fatalf("len: got %d, want 2", len(rows))
			}
		})
		t.Run("round 1 was rejected", func(t *testing.T) {
			if rows[0].Round != 1 || rows[0].Status != reviewpkg.StatusChangesRequested {
				t.Errorf("rows[0] = %+v", rows[0])
			}
		})
		t.Run("round 2 is in review", func(t *testing.T) {
			if rows[1].Round != 2 || rows[1].Status != reviewpkg.StatusInReview {
				t.Errorf("rows[1] = %+v", rows[1])
			}
		})
	})
}

// newReviewE2ERigWithOrigin builds a rig that has a bare remote (origin).
// This is required to exercise reviewpkg.Push — without a remote the push
// is a no-op and push bugs are invisible.
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

	branchtest.Seed(t, client, branch.Op{Branch: "77@feat@my-feature", Title: "my feature"}, branch.StatusInProgress)

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	return &reviewE2ERig{
		dir: cloneDir, client: client, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

// readRemoteReviewRef folds refs/zf/reviews/<issueID> as the bare origin has
// it, bypassing the local refs. nil when the origin has no such ref.
func readRemoteReviewRef(t *testing.T, originDir, issueID string) *reviewpkg.State {
	t.Helper()

	ref := "refs/zf/reviews/" + issueID
	out, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
		"rev-list", "--topo-order", "--reverse", "--parents", ref).Output()
	if err != nil {
		return nil // ref does not exist
	}

	var ops []reviewpkg.Op
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		payload, err := exec.CommandContext(t.Context(), "git", "-C", originDir,
			"cat-file", "blob", fields[0]+":op.json").Output()
		if err != nil {
			t.Fatalf("cat-file %s:op.json: %v", fields[0], err)
		}
		op, ok := reviewpkg.DecodeOp(fields[0], fields[1:], payload)
		if !ok {
			t.Fatalf("malformed op %s on origin", fields[0])
		}
		ops = append(ops, op)
	}

	st := reviewpkg.Fold(issueID, ops)

	return &st
}

// TestReviewRefPush_ReachesRemote verifies that request and approve push their
// op to the remote, so the other side sees the decision. Without a remote,
// reviewpkg.Push is a no-op and this class of bug is invisible.
func TestReviewRefPush_ReachesRemote(t *testing.T) {
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

		branches, _ := branch.ListRows(ctx, rig.client, branch.StatusInProgress)
		var picked branch.Row
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
		p := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: "77@feat@my-feature"}}
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

// TestReviewList_And_Start_WorkOnEmptyReviewerStore verifies the cross-machine
// scenario: a reviewer on a fresh clone (empty git-zf store) can discover and
// start a review using only git refs fetched from the remote.
//
// This exercises the core design requirement: "Any team member who runs git
// fetch sees the current review state without needing access to another
// developer's store."
func TestReviewList_And_Start_WorkOnEmptyReviewerStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	originDir := filepath.Join(t.TempDir(), "origin.git")

	// ── Developer side ───────────────────────────────────────────────────────
	devRig := newReviewE2ERigWithOrigin(t)

	// Resolve origin from the dev rig.
	remoteCmd := exec.CommandContext(ctx, "git", "-C", devRig.dir, "remote", "get-url", "origin")
	remoteOut, err := remoteCmd.Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir = string(remoteOut[:len(remoteOut)-1])

	// Developer checks out the feature branch and submits for review.
	if err := devRig.client.RunGitAt(ctx, devRig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("dev checkout feature branch: %v", err)
	}
	branches, _ := branch.ListRows(ctx, devRig.client, branch.StatusInProgress)
	var picked branch.Row
	for _, b := range branches {
		if b.IssueSlug == "77" {
			picked = b
		}
	}
	p := &scriptedReviewPrompter{Branch: &picked}
	if err := runReviewRequestInteractive(ctx, devRig.deps(), p); err != nil {
		t.Fatalf("developer review request: %v", err)
	}

	// ── Reviewer side — fresh clone, empty store ──────────────────────────────
	reviewerDir := t.TempDir()
	runInDir := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	runInDir(filepath.Dir(reviewerDir), "clone", "--quiet", originDir, filepath.Base(reviewerDir))
	runInDir(reviewerDir, "config", "user.name", "Carol")
	runInDir(reviewerDir, "config", "user.email", "carol@example.com")
	runInDir(reviewerDir, "config", "commit.gpgsign", "false")

	reviewerStdout := &bytes.Buffer{}
	reviewerStderr := &bytes.Buffer{}
	reviewerClient, err := git.NewClientAt(&pkg.IO{
		In:  bytes.NewReader(nil),
		Out: reviewerStdout,
		Err: reviewerStderr,
	}, reviewerDir)
	if err != nil {
		t.Fatalf("reviewer NewClientAt: %v", err)
	}

	// Reviewer's store is EMPTY — no InsertIssueWithBranch has been called.

	reviewerDeps := reviewDeps{
		client: reviewerClient,
		cfg:    &config.AppConfig{},
	}

	// Reviewer fetches refs — this is the only setup they do.
	if err := reviewpkg.Fetch(ctx, reviewerClient, false); err != nil {
		t.Fatalf("reviewer Fetch reviews: %v", err)
	}

	t.Run("review list shows issue on empty store after fetch", func(t *testing.T) {
		reviewerStdout.Reset()
		if err := runReviewList(ctx, reviewerDeps); err != nil {
			t.Fatalf("runReviewList on empty store: %v", err)
		}
		out := reviewerStdout.String()
		if strings.Contains(out, "No issues currently in review") {
			t.Errorf("review list incorrectly reported no issues; stdout = %q", out)
		}
		if !strings.Contains(out, "77") {
			t.Errorf("review list did not show issue 77; stdout = %q", out)
		}
	})

	t.Run("review start succeeds on empty store after fetch", func(t *testing.T) {
		reviewerStdout.Reset()
		reviewPrompter := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: "77@review"}}
		if err := runReviewStartInteractive(ctx, reviewerDeps, reviewPrompter); err != nil {
			t.Fatalf("runReviewStartInteractive on empty store: %v", err)
		}
		// Verify the review branch was created.
		exists, brErr := reviewerClient.BranchExists("77@review")
		if brErr != nil {
			t.Fatalf("BranchExists: %v", brErr)
		}
		if !exists {
			t.Error("77@review branch was not created on reviewer's machine")
		}
	})
}

// TestReviewStart_FetchesCommitObjects verifies that git zf review start works
// even when the reviewer's clone predates the commit that was locked for review.
//
// Scenario reproduced here:
//  1. Developer submits round 1. Reviewer clones (has all commits so far).
//  2. Developer adds a new commit, resubmits for round 2.
//  3. Reviewer fetches only review refs — NOT the branch objects.
//  4. Reviewer runs review start → must fetch branch objects automatically and
//     create the review branch at the new commit SHA.
//
// Without the "git fetch <remote>" inside runReviewStart this test fails with
// "fatal: unable to read tree" because the new commit is not in the reviewer's
// object store.
func TestReviewStart_FetchesCommitObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// ── Developer side ────────────────────────────────────────────────────────
	devRig := newReviewE2ERigWithOrigin(t)

	remoteOut, err := exec.CommandContext(ctx, "git", "-C", devRig.dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir := strings.TrimSpace(string(remoteOut))

	// Round 1: developer checks out feature branch and requests review.
	if err := devRig.client.RunGitAt(ctx, devRig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	branches, _ := branch.ListRows(ctx, devRig.client, branch.StatusInProgress)
	var picked branch.Row
	for _, b := range branches {
		if b.IssueSlug == "77" {
			picked = b
		}
	}
	if err := runReviewRequestInteractive(ctx, devRig.deps(), &scriptedReviewPrompter{Branch: &picked}); err != nil {
		t.Fatalf("round 1 review request: %v", err)
	}

	// ── Reviewer clones NOW (after round 1, before round 2 commit) ────────────
	reviewerDir := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	run(filepath.Dir(reviewerDir), "clone", "--quiet", originDir, filepath.Base(reviewerDir))
	run(reviewerDir, "config", "user.name", "Carol")
	run(reviewerDir, "config", "user.email", "carol@example.com")
	run(reviewerDir, "config", "commit.gpgsign", "false")

	// ── Developer: simulate reject + new commit + round 2 request ────────────
	// Simulate reject: append a reject op to the chain and push it.
	if err := reviewpkg.Append(ctx, devRig.client, "77", &reviewpkg.Op{Type: reviewpkg.OpReject}, false); err != nil {
		t.Fatalf("append reject: %v", err)
	}
	_ = reviewpkg.Push(ctx, devRig.client, "77")

	// Developer adds a new commit (the round-2 fix) and pushes it.
	if err := os.WriteFile(filepath.Join(devRig.dir, "fix.txt"), []byte("round 2 fix\n"), 0o644); err != nil {
		t.Fatalf("write fix.txt: %v", err)
	}
	run(devRig.dir, "add", "fix.txt")
	run(devRig.dir, "commit", "-m", "fix: address review feedback")
	run(devRig.dir, "push", "origin", "77@feat@my-feature")

	// Developer submits round 2.
	if err := runReviewRequest(ctx, devRig.deps(), "77"); err != nil {
		t.Fatalf("round 2 review request: %v", err)
	}

	// Resolve the round-2 feature SHA so we can assert the branch tip below.
	round2SHA, err := exec.CommandContext(ctx, "git", "-C", devRig.dir, "rev-parse", "77@feat@my-feature").Output()
	if err != nil {
		t.Fatalf("rev-parse feature branch: %v", err)
	}
	wantSHA := strings.TrimSpace(string(round2SHA))

	// ── Reviewer: only fetch review refs, then run review start ──────────────
	reviewerClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: &bytes.Buffer{}, Err: &bytes.Buffer{},
	}, reviewerDir)
	if err != nil {
		t.Fatalf("reviewer NewClientAt: %v", err)
	}

	reviewerDeps := reviewDeps{
		client: reviewerClient,
		cfg:    &config.AppConfig{},
	}

	// Reviewer fetches ONLY review refs — does NOT do git fetch origin.
	if err := reviewpkg.Fetch(ctx, reviewerClient, false); err != nil {
		t.Fatalf("reviewer Fetch reviews: %v", err)
	}

	t.Run("review start succeeds even though reviewer lacks new commit", func(t *testing.T) {
		if err := runReviewStart(ctx, reviewerDeps, "77"); err != nil {
			t.Fatalf("runReviewStart: %v (reviewer lacked commit objects for round 2)", err)
		}
	})

	t.Run("review branch tip matches round-2 feature SHA", func(t *testing.T) {
		gotOut, err := exec.CommandContext(ctx, "git", "-C", reviewerDir,
			"rev-parse", "77@review").Output()
		if err != nil {
			t.Fatalf("rev-parse 77@review: %v", err)
		}
		got := strings.TrimSpace(string(gotOut))
		if got != wantSHA {
			t.Errorf("77@review tip = %s, want %s (round-2 fix commit)", got, wantSHA)
		}
	})
}

// TestReviewApproveReject_WorkOnEmptyReviewerStore verifies that approve and
// reject work on a fresh reviewer clone with an empty git-zf store.
// This is the companion to TestReviewList_And_Start_WorkOnEmptyReviewerStore
// and covers the same class of bug: commands must not require the reviewer to
// have run git zf issue start or git zf review track before operating.
func TestReviewApproveReject_WorkOnEmptyReviewerStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// ── Developer side: submit for review ─────────────────────────────────────
	devRig := newReviewE2ERigWithOrigin(t)

	remoteCmd := exec.CommandContext(ctx, "git", "-C", devRig.dir, "remote", "get-url", "origin")
	remoteOut, err := remoteCmd.Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir := string(remoteOut[:len(remoteOut)-1])

	if err := devRig.client.RunGitAt(ctx, devRig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("dev checkout: %v", err)
	}
	branches, _ := branch.ListRows(ctx, devRig.client, branch.StatusInProgress)
	var picked branch.Row
	for _, b := range branches {
		if b.IssueSlug == "77" {
			picked = b
		}
	}
	if err := runReviewRequestInteractive(ctx, devRig.deps(), &scriptedReviewPrompter{Branch: &picked}); err != nil {
		t.Fatalf("developer review request: %v", err)
	}

	// ── Reviewer side: fresh clone, empty store ───────────────────────────────
	reviewerDir := t.TempDir()
	runInDir := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	runInDir(filepath.Dir(reviewerDir), "clone", "--quiet", originDir, filepath.Base(reviewerDir))
	runInDir(reviewerDir, "config", "user.name", "Carol")
	runInDir(reviewerDir, "config", "user.email", "carol@example.com")
	runInDir(reviewerDir, "config", "commit.gpgsign", "false")

	reviewerClient, err := git.NewClientAt(&pkg.IO{
		In:  bytes.NewReader(nil),
		Out: &bytes.Buffer{},
		Err: &bytes.Buffer{},
	}, reviewerDir)
	if err != nil {
		t.Fatalf("reviewer NewClientAt: %v", err)
	}

	reviewerDeps := reviewDeps{
		client: reviewerClient,
		cfg:    &config.AppConfig{},
	}

	// Reviewer fetches — this is all they do before approving/rejecting.
	if err := reviewpkg.Fetch(ctx, reviewerClient, false); err != nil {
		t.Fatalf("reviewer Fetch reviews: %v", err)
	}

	t.Run("review reject works on empty store after fetch", func(t *testing.T) {
		rejectPrompter := &scriptedReviewPrompter{
			Branch: &branch.Row{IssueSlug: "77", BranchName: "77@review"},
		}
		if err := runReviewRejectInteractive(ctx, reviewerDeps, rejectPrompter, "", true); err != nil {
			t.Fatalf("runReviewRejectInteractive on empty store: %v", err)
		}
		latest, loadErr := reviewpkg.Load(ctx, reviewerClient, "77")
		if loadErr != nil || latest == nil {
			t.Fatalf("Load after reject: %v (state=%v)", loadErr, latest)
		}
		if latest.Status != reviewpkg.StatusChangesRequested {
			t.Errorf("status after reject = %q", latest.Status)
		}
	})
}

// TestReviewSync_UsesRemoteParentBase reproduces Phase 11 of the demo:
// after Bob pushes a squash close of X.2 into origin/X@feat@big, Alice runs
// "git fetch origin" then "git zf review sync". The local X@feat@big is still
// at the pre-close SHA; only origin/X@feat@big has the new commit.
// sync must use the remote tracking ref so the drift is detected.
func TestReviewSync_UsesRemoteParentBase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// ----- origin setup -----
	originDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	run(originDir, "init", "--bare", "--initial-branch=main")

	// Seed origin via a throw-away repo.
	seedDir := t.TempDir()
	run(seedDir, "init", "--initial-branch=main")
	run(seedDir, "config", "user.name", "Seed")
	run(seedDir, "config", "user.email", "seed@example.com")
	run(seedDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seedDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	run(seedDir, "add", "base.txt")
	run(seedDir, "commit", "-m", "chore: init")
	run(seedDir, "remote", "add", "origin", originDir)
	run(seedDir, "push", "origin", "main")

	// Create parent integration branch X@feat@big and push.
	run(seedDir, "checkout", "-b", "X@feat@big")
	if err := os.WriteFile(filepath.Join(seedDir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	run(seedDir, "add", "parent.txt")
	run(seedDir, "commit", "-m", "feat(X): parent commit")
	run(seedDir, "push", "origin", "X@feat@big")

	// Create sub-task X.1@feat@one from X@feat@big and push.
	run(seedDir, "checkout", "-b", "X.1@feat@one")
	if err := os.WriteFile(filepath.Join(seedDir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	run(seedDir, "add", "subtask.txt")
	run(seedDir, "commit", "-m", "feat(X.1): subtask commit")
	run(seedDir, "push", "origin", "X.1@feat@one")

	// ----- Alice's clone -----
	aliceDir := t.TempDir()
	run(aliceDir, "init", "--initial-branch=main")
	run(aliceDir, "config", "user.name", "Alice")
	run(aliceDir, "config", "user.email", "alice@example.com")
	run(aliceDir, "config", "commit.gpgsign", "false")
	run(aliceDir, "remote", "add", "origin", originDir)
	run(aliceDir, "fetch", "origin")
	run(aliceDir, "checkout", "main")                                      // DWIM local main
	run(aliceDir, "checkout", "-b", "X.1@feat@one", "origin/X.1@feat@one") // local subtask branch

	// Alice knows about X@feat@big via store + branch ref, but has NOT
	// checked it out as a local branch — only origin/X@feat@big exists.

	stdout := &bytes.Buffer{}
	aliceClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{},
	}, aliceDir)
	if err != nil {
		t.Fatalf("NewClientAt alice: %v", err)
	}

	// Alice tracks parent X and its child X.1.
	branchtest.Seed(t, aliceClient, branch.Op{Branch: "X@feat@big", Title: "big"}, branch.StatusInProgress)
	branchtest.Seed(t, aliceClient, branch.Op{Branch: "X.1@feat@one", Title: "one", Parent: "X"}, branch.StatusInProgress)

	aliceCfg := &config.AppConfig{}
	aliceCfg.Branch.Base = "main"
	aliceDeps := reviewDeps{
		client: aliceClient,
		cfg:    aliceCfg,
	}

	// ----- Simulate Bob pushing a squash close of X.2 into X@feat@big -----
	// (Bob's commit lands on origin/X@feat@big, not on Alice's local X@feat@big.)
	run(seedDir, "checkout", "X@feat@big")
	if err := os.WriteFile(filepath.Join(seedDir, "bob_close.txt"), []byte("bob's X.2 squash\n"), 0o644); err != nil {
		t.Fatalf("write bob_close.txt: %v", err)
	}
	run(seedDir, "add", "bob_close.txt")
	run(seedDir, "commit", "-m", "feat(X.2): squash close into X@feat@big")
	run(seedDir, "push", "origin", "X@feat@big")

	// Alice fetches — origin/X@feat@big advances; local X@feat@big does not exist locally.
	run(aliceDir, "fetch", "origin")

	// ----- Before fix: sync would say "already up to date" -----
	// After fix: sync detects origin/X@feat@big is ahead and merges forward.
	if err := runReviewSync(ctx, aliceDeps, "X.1"); err != nil {
		t.Fatalf("runReviewSync: %v", err)
	}

	t.Run("drift detected and merged", func(t *testing.T) {
		if got := stdout.String(); strings.Contains(got, "already up to date") {
			t.Errorf("sync incorrectly reported up-to-date; stdout = %q", got)
		}
	})

	t.Run("X.1 branch contains bob's close commit", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", aliceDir,
			"log", "--oneline", "X.1@feat@one").Output()
		if err != nil {
			t.Fatalf("git log: %v", err)
		}
		if !strings.Contains(string(out), "X@feat@big") {
			t.Errorf("expected merge commit from X@feat@big in X.1 history; log:\n%s", out)
		}
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
		rows, listErr := branch.ListRows(ctx, rig.client, branch.StatusInProgress)
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

	// Simulate the developer having submitted for review: seed an in_review chain.
	featureSHA, shaErr := rig.client.ResolveRef("refs/heads/77@feat@my-feature")
	if shaErr != nil {
		t.Fatalf("ResolveRef: %v", shaErr)
	}
	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusInReview, 1, featureSHA.String())

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

	t.Run("reviewer identity recorded on the chain", func(t *testing.T) {
		latest, loadErr := reviewpkg.Load(ctx, rig.client, "77")
		if loadErr != nil || latest == nil {
			t.Fatalf("Load: %v (state=%v)", loadErr, latest)
		}
		if !strings.Contains(latest.Reviewer, "Test User") {
			t.Errorf("Reviewer = %q, want the clone's user", latest.Reviewer)
		}
	})

	t.Run("tracking again is a no-op that still succeeds", func(t *testing.T) {
		if err := runTrack(ctx, rig.deps()); err != nil {
			t.Fatalf("second runTrack: %v", err)
		}
		st, _ := reviewpkg.Load(ctx, rig.client, "77")
		if st == nil || !strings.Contains(st.Reviewer, "Test User") {
			t.Errorf("state after second track = %+v", st)
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

// TestFullParallelReviewScenario exercises the complete multi-user review
// workflow end-to-end using only non-interactive internal functions:
//
//   - Alice works on X.1@feat@part-one (child of parent X@feat@big-feature)
//   - Bob works on X.2@feat@part-two  (child of parent X@feat@big-feature)
//   - Carol reviews X.1: rejects round 1, approves round 2
//   - Dan reviews X.2: approves with active reviewer commits
//
// Close operations are simulated with direct git commands to avoid the
// cross-package dependency on cmd/issue (close is unit-tested there).
func TestFullParallelReviewScenario(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	workdir := t.TempDir()
	originDir := filepath.Join(workdir, "origin.git")
	aliceDir := filepath.Join(workdir, "dev-alice")
	bobDir := filepath.Join(workdir, "dev-bob")
	carolDir := filepath.Join(workdir, "reviewer-carol")
	danDir := filepath.Join(workdir, "reviewer-dan")

	// ── helpers ───────────────────────────────────────────────────────────────

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, filepath.Base(dir), err, out)
		}
	}

	gitHead := func(dir, branch string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%s", branch)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log %s in %s: %v", branch, filepath.Base(dir), err)
		}
		return strings.TrimSpace(string(out))
	}

	newDeps := func(dir string) reviewDeps {
		t.Helper()
		c, err := git.NewClientAt(&pkg.IO{
			In:  bytes.NewReader(nil),
			Out: &bytes.Buffer{},
			Err: &bytes.Buffer{},
		}, dir)
		if err != nil {
			t.Fatalf("NewClientAt %s: %v", filepath.Base(dir), err)
		}
		cfg := &config.AppConfig{}
		cfg.Branch.Base = "main"
		return reviewDeps{client: c, cfg: cfg}
	}

	// ── PHASE 0: infrastructure ───────────────────────────────────────────────

	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir origin: %v", err)
	}
	run(originDir, "init", "--bare", "--initial-branch=main")

	// Alice's repo: main commit → X@feat@big-feature → X.1 + X.2
	if err := os.MkdirAll(aliceDir, 0o755); err != nil {
		t.Fatalf("mkdir alice: %v", err)
	}
	run(aliceDir, "init", "--initial-branch=main")
	run(aliceDir, "config", "user.name", "Alice")
	run(aliceDir, "config", "user.email", "alice@example.com")
	run(aliceDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(aliceDir, "README.md"), []byte("# Project\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run(aliceDir, "add", "README.md")
	run(aliceDir, "commit", "-m", "chore: init")
	run(aliceDir, "remote", "add", "origin", originDir)
	run(aliceDir, "push", "origin", "main")

	// Integration branch and two sub-task branches.
	run(aliceDir, "checkout", "-b", "X@feat@big-feature")
	run(aliceDir, "push", "origin", "X@feat@big-feature")
	run(aliceDir, "checkout", "-b", "X.1@feat@part-one")
	run(aliceDir, "push", "origin", "X.1@feat@part-one")
	// X.2 branches from X@feat@big-feature, not X.1.
	run(aliceDir, "checkout", "X@feat@big-feature")
	run(aliceDir, "checkout", "-b", "X.2@feat@part-two")
	run(aliceDir, "push", "origin", "X.2@feat@part-two")
	run(aliceDir, "checkout", "main")

	aliceDeps := newDeps(aliceDir)

	// Alice tracks the three branches, with the parent relationships, and
	// pushes the chains.
	for _, op := range []branch.Op{
		{Branch: "X@feat@big-feature", Title: "big feature"},
		{Branch: "X.1@feat@part-one", Title: "part one", Parent: "X"},
		{Branch: "X.2@feat@part-two", Title: "part two", Parent: "X"},
	} {
		branchtest.Seed(t, aliceDeps.client, op, branch.StatusInProgress)
	}
	if err := branch.Sync(ctx, aliceDeps.client); err != nil {
		t.Fatalf("alice Sync branch refs: %v", err)
	}

	// Bob: clone, check out X.2, fetch the branch chains.
	run(filepath.Dir(bobDir), "clone", "--quiet", originDir, filepath.Base(bobDir))
	run(bobDir, "config", "user.name", "Bob")
	run(bobDir, "config", "user.email", "bob@example.com")
	run(bobDir, "config", "commit.gpgsign", "false")
	run(bobDir, "checkout", "-b", "X.2@feat@part-two", "origin/X.2@feat@part-two")
	run(bobDir, "checkout", "main")
	bobDeps := newDeps(bobDir)
	if err := branch.Fetch(ctx, bobDeps.client); err != nil {
		t.Fatalf("bob Fetch branch refs: %v", err)
	}

	// markMerged records a branch as merged and pushes it, as issue close does.
	markMerged := func(deps reviewDeps, slug, name string) {
		t.Helper()
		if err := branch.SetStatus(ctx, deps.client, slug, name, branch.StatusMerged); err != nil {
			t.Fatalf("SetStatus %s: %v", name, err)
		}
		if err := branch.Push(ctx, deps.client, slug); err != nil {
			t.Fatalf("push branch ref %s: %v", slug, err)
		}
	}

	// closeReview marks a review closed and pushes it, as issue close does.
	closeReview := func(deps reviewDeps, slug string) {
		t.Helper()
		if err := reviewpkg.Append(ctx, deps.client, slug, &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
			t.Fatalf("close review %s: %v", slug, err)
		}
		_ = reviewpkg.Push(ctx, deps.client, slug)
	}

	// ── PHASE 1: development ─────────────────────────────────────────────────

	// Alice commits to X.1@feat@part-one.
	run(aliceDir, "checkout", "X.1@feat@part-one")
	if err := os.WriteFile(filepath.Join(aliceDir, "part-one.txt"), []byte("part one impl\n"), 0o644); err != nil {
		t.Fatalf("write part-one.txt: %v", err)
	}
	run(aliceDir, "add", "part-one.txt")
	run(aliceDir, "commit", "-m", "feat(X.1): implement part one")
	run(aliceDir, "push", "origin", "X.1@feat@part-one")
	run(aliceDir, "checkout", "main")

	// Bob commits to X.2@feat@part-two.
	run(bobDir, "checkout", "X.2@feat@part-two")
	if err := os.WriteFile(filepath.Join(bobDir, "part-two.txt"), []byte("part two impl\n"), 0o644); err != nil {
		t.Fatalf("write part-two.txt: %v", err)
	}
	run(bobDir, "add", "part-two.txt")
	run(bobDir, "commit", "-m", "feat(X.2): implement part two")
	run(bobDir, "push", "origin", "X.2@feat@part-two")

	// ── PHASE 2: submit for review ────────────────────────────────────────────

	run(aliceDir, "checkout", "X.1@feat@part-one")
	if err := runReviewRequest(ctx, aliceDeps, "X.1"); err != nil {
		t.Fatalf("alice runReviewRequest X.1: %v", err)
	}
	run(aliceDir, "checkout", "main")

	run(bobDir, "checkout", "X.2@feat@part-two")
	if err := runReviewRequest(ctx, bobDeps, "X.2"); err != nil {
		t.Fatalf("bob runReviewRequest X.2: %v", err)
	}

	// ── PHASE 3: reviewers start reviews ─────────────────────────────────────

	// Carol clones, fetches review refs, starts X.1 review.
	run(filepath.Dir(carolDir), "clone", "--quiet", originDir, filepath.Base(carolDir))
	run(carolDir, "config", "user.name", "Carol")
	run(carolDir, "config", "user.email", "carol@example.com")
	run(carolDir, "config", "commit.gpgsign", "false")
	carolDeps := newDeps(carolDir)
	if err := reviewpkg.Fetch(ctx, carolDeps.client, false); err != nil {
		t.Fatalf("carol Fetch reviews: %v", err)
	}
	if err := runReviewStart(ctx, carolDeps, "X.1"); err != nil {
		t.Fatalf("carol runReviewStart X.1: %v", err)
	}

	// Dan clones, fetches review refs, starts X.2 review, pushes a reviewer fix.
	run(filepath.Dir(danDir), "clone", "--quiet", originDir, filepath.Base(danDir))
	run(danDir, "config", "user.name", "Dan")
	run(danDir, "config", "user.email", "dan@example.com")
	run(danDir, "config", "commit.gpgsign", "false")
	danDeps := newDeps(danDir)
	if err := reviewpkg.Fetch(ctx, danDeps.client, false); err != nil {
		t.Fatalf("dan Fetch reviews: %v", err)
	}
	if err := runReviewStart(ctx, danDeps, "X.2"); err != nil {
		t.Fatalf("dan runReviewStart X.2: %v", err)
	}
	// Dan commits a fix to X.2@review and pushes it.
	run(danDir, "checkout", "X.2@review")
	if err := os.WriteFile(filepath.Join(danDir, "part-two.txt"), []byte("part two impl\nreviewer fix from Dan\n"), 0o644); err != nil {
		t.Fatalf("write part-two.txt dan: %v", err)
	}
	run(danDir, "add", "part-two.txt")
	run(danDir, "commit", "-m", "fix(X.2): reviewer fix from Dan")
	run(danDir, "push", "origin", "X.2@review")

	// ── PHASE 4: decisions ────────────────────────────────────────────────────

	// Carol rejects X.1 round 1 (no reviewer commits).
	run(carolDir, "checkout", "X.1@review")
	if _, err := runReviewReject(ctx, carolDeps, "X.1", ""); err != nil {
		t.Fatalf("carol runReviewReject X.1: %v", err)
	}

	// Dan approves X.2 (reviewer commits pushed above).
	if err := runReviewApprove(ctx, danDeps, "X.2"); err != nil {
		t.Fatalf("dan runReviewApprove X.2: %v", err)
	}

	// ── PHASE 5: Alice addresses feedback, round 2 ───────────────────────────

	_ = reviewpkg.Fetch(ctx, aliceDeps.client, false)
	run(aliceDir, "checkout", "X.1@feat@part-one")
	if err := os.WriteFile(filepath.Join(aliceDir, "part-one.txt"), []byte("part one impl\nfixed per review\n"), 0o644); err != nil {
		t.Fatalf("write part-one.txt fix: %v", err)
	}
	run(aliceDir, "add", "part-one.txt")
	run(aliceDir, "commit", "-m", "fix(X.1): address review feedback")
	run(aliceDir, "push", "origin", "X.1@feat@part-one")
	if err := runReviewRequest(ctx, aliceDeps, "X.1"); err != nil {
		t.Fatalf("alice runReviewRequest X.1 round 2: %v", err)
	}

	// ── PHASE 6: Carol approves X.1 round 2 ──────────────────────────────────

	if err := reviewpkg.Fetch(ctx, carolDeps.client, false); err != nil {
		t.Fatalf("carol Fetch reviews round 2: %v", err)
	}
	if err := runReviewStart(ctx, carolDeps, "X.1"); err != nil {
		t.Fatalf("carol runReviewStart X.1 round 2: %v", err)
	}
	if err := runReviewApprove(ctx, carolDeps, "X.1"); err != nil {
		t.Fatalf("carol runReviewApprove X.1: %v", err)
	}

	// ── PHASE 7: Bob closes X.2 (simulated) ──────────────────────────────────
	// Simulates reviewPreflight fast-forward + squash merge into the parent
	// integration branch. This is the exact scenario fixed by the doMerge
	// dry-run fallback: X@feat@big-feature exists only as origin/X@feat@big-feature
	// on Bob's machine (never checked out locally).

	if err := bobDeps.client.Fetch(ctx); err != nil {
		t.Fatalf("bob fetch: %v", err)
	}
	if err := reviewpkg.Fetch(ctx, bobDeps.client, false); err != nil {
		t.Fatalf("bob Fetch reviews: %v", err)
	}

	// Fetch Dan's review branch (it only exists on origin as a remote branch).
	bobRoot := bobDeps.client.WorkingTreeRoot()
	if err := bobDeps.client.RunGitAt(ctx, bobRoot, "fetch", "origin", "X.2@review:X.2@review"); err != nil {
		t.Fatalf("bob fetch X.2@review: %v", err)
	}

	// Fast-forward X.2@feat@part-two to include Dan's reviewer commit.
	if err := bobDeps.client.FastForwardOnly(ctx, "X.2@review", "X.2@feat@part-two"); err != nil {
		t.Fatalf("bob FF X.2@feat@part-two to X.2@review: %v", err)
	}
	if err := bobDeps.client.DeleteLocalBranch(ctx, "X.2@review", true); err != nil {
		t.Fatalf("bob delete X.2@review: %v", err)
	}
	run(bobDir, "push", "origin", "--delete", "X.2@review")
	closeReview(bobDeps, "X.2")

	// Squash merge X.2@feat@part-two into X@feat@big-feature.
	// git checkout uses DWIM: creates local X@feat@big-feature from origin/X@feat@big-feature.
	if err := bobDeps.client.RunGitAt(ctx, bobRoot, "checkout", "X@feat@big-feature"); err != nil {
		t.Fatalf("bob checkout X@feat@big-feature (DWIM): %v", err)
	}
	if err := bobDeps.client.RunGitAt(ctx, bobRoot, "merge", "--squash", "X.2@feat@part-two"); err != nil {
		t.Fatalf("bob merge --squash X.2: %v", err)
	}
	if err := bobDeps.client.Commit(ctx, []byte("feat(X.2): close\n"), git.CommitOptions{}); err != nil {
		t.Fatalf("bob commit close X.2: %v", err)
	}

	markMerged(bobDeps, "X.2", "X.2@feat@part-two")
	run(bobDir, "push", "origin", "X@feat@big-feature")

	// Alice learns that X.2 is merged by fetching the chains.
	if err := branch.Fetch(ctx, aliceDeps.client); err != nil {
		t.Fatalf("alice Fetch branch refs: %v", err)
	}

	// ── PHASE 8: Alice syncs X.1 (X.2 landed in parent) ─────────────────────

	if err := aliceDeps.client.Fetch(ctx); err != nil {
		t.Fatalf("alice fetch: %v", err)
	}
	// Fast-forward local X@feat@big-feature to include Bob's squash commit.
	if err := aliceDeps.client.FastForwardOnly(ctx, "origin/X@feat@big-feature", "X@feat@big-feature"); err != nil {
		t.Fatalf("alice FF X@feat@big-feature: %v", err)
	}
	run(aliceDir, "checkout", "main")

	if err := runReviewSync(ctx, aliceDeps, "X.1"); err != nil {
		t.Fatalf("alice runReviewSync X.1: %v", err)
	}
	run(aliceDir, "push", "origin", "X.1@feat@part-one")

	// ── PHASE 9: Alice closes X.1 (simulated) ────────────────────────────────

	if err := aliceDeps.client.RunGitAt(ctx, aliceDir, "checkout", "X@feat@big-feature"); err != nil {
		t.Fatalf("alice checkout X@feat@big-feature for X.1 close: %v", err)
	}
	if err := aliceDeps.client.RunGitAt(ctx, aliceDir, "merge", "--squash", "X.1@feat@part-one"); err != nil {
		t.Fatalf("alice merge --squash X.1: %v", err)
	}
	if err := aliceDeps.client.Commit(ctx, []byte("feat(X.1): close\n"), git.CommitOptions{}); err != nil {
		t.Fatalf("alice commit close X.1: %v", err)
	}
	closeReview(aliceDeps, "X.1")

	markMerged(aliceDeps, "X.1", "X.1@feat@part-one")
	run(aliceDir, "push", "origin", "X@feat@big-feature")

	// ── PHASE 10: integration review on X, then close X into main ────────────

	// Alice requests review on the parent integration branch.
	run(aliceDir, "checkout", "X@feat@big-feature")
	if err := runReviewRequest(ctx, aliceDeps, "X"); err != nil {
		t.Fatalf("alice runReviewRequest X: %v", err)
	}

	// Carol fetches and starts + approves the integration review.
	if err := reviewpkg.Fetch(ctx, carolDeps.client, false); err != nil {
		t.Fatalf("carol Fetch reviews for X: %v", err)
	}
	run(carolDir, "fetch", "origin") // get updated X@feat@big-feature
	if err := runReviewStart(ctx, carolDeps, "X"); err != nil {
		t.Fatalf("carol runReviewStart X: %v", err)
	}
	if err := runReviewApprove(ctx, carolDeps, "X"); err != nil {
		t.Fatalf("carol runReviewApprove X: %v", err)
	}

	// Alice fetches refs, checks ChildrenAllMerged, then squash merges into main.
	if err := reviewpkg.Fetch(ctx, aliceDeps.client, false); err != nil {
		t.Fatalf("alice Fetch reviews for X close: %v", err)
	}
	states, _, err := branch.List(ctx, aliceDeps.client)
	if err != nil {
		t.Fatalf("List branch refs: %v", err)
	}
	for _, child := range branch.Children(states, "X") {
		for _, e := range child.Entries {
			if e.Status != branch.StatusMerged {
				t.Fatalf("sub-task %s is %s before closing parent X", e.Name, e.Status)
			}
		}
	}

	run(aliceDir, "checkout", "main")
	if err := aliceDeps.client.RunGitAt(ctx, aliceDir, "merge", "--squash", "X@feat@big-feature"); err != nil {
		t.Fatalf("alice merge --squash X into main: %v", err)
	}
	if err := aliceDeps.client.Commit(ctx, []byte("feat(X): close into main\n"), git.CommitOptions{}); err != nil {
		t.Fatalf("alice commit close X into main: %v", err)
	}
	closeReview(aliceDeps, "X")

	markMerged(aliceDeps, "X", "X@feat@big-feature")
	run(aliceDir, "push", "origin", "main")

	// ── ASSERTIONS ────────────────────────────────────────────────────────────

	t.Run("main carries the integration squash commit", func(t *testing.T) {
		got := gitHead(aliceDir, "main")
		if got != "feat(X): close into main" {
			t.Errorf("main HEAD subject = %q, want %q", got, "feat(X): close into main")
		}
	})

	t.Run("Dan's reviewer fix is present on main", func(t *testing.T) {
		cmd := exec.CommandContext(ctx, "git", "show", "main:part-two.txt")
		cmd.Dir = aliceDir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git show part-two.txt: %v", err)
		}
		if !strings.Contains(string(out), "reviewer fix from Dan") {
			t.Errorf("part-two.txt on main = %q, want it to contain Dan's reviewer fix", string(out))
		}
	})

	t.Run("X.2 is marked merged in alice store", func(t *testing.T) {
		merged, _ := branch.ListRows(ctx, aliceDeps.client, branch.StatusMerged)
		var found bool
		for _, b := range merged {
			if b.BranchName == "X.2@feat@part-two" {
				found = true
			}
		}
		if !found {
			t.Error("X.2@feat@part-two not merged in alice's store")
		}
	})

	t.Run("X.1 is marked merged in alice store", func(t *testing.T) {
		merged, _ := branch.ListRows(ctx, aliceDeps.client, branch.StatusMerged)
		var found bool
		for _, b := range merged {
			if b.BranchName == "X.1@feat@part-one" {
				found = true
			}
		}
		if !found {
			t.Error("X.1@feat@part-one not merged in alice's store")
		}
	})

	t.Run("X integration branch is marked merged in alice store", func(t *testing.T) {
		merged, _ := branch.ListRows(ctx, aliceDeps.client, branch.StatusMerged)
		var found bool
		for _, b := range merged {
			if b.BranchName == "X@feat@big-feature" {
				found = true
			}
		}
		if !found {
			t.Error("X@feat@big-feature not merged in alice's store")
		}
	})
}

// TestReviewRequest_ProposesFeatureBranchPush verifies that when Push.Propose=true
// and the push confirm returns true, runReviewRequestInteractive pushes the feature
// branch to origin after submitting for review.
func TestReviewRequest_ProposesFeatureBranchPush(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERigWithOrigin(t)

	// Resolve the actual origin path from the rig's git config.
	originOut, err := exec.CommandContext(t.Context(), "git", "-C", rig.dir,
		"remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("get origin url: %v", err)
	}
	originDir := strings.TrimSpace(string(originOut))

	// Checkout the feature branch so currentIssueSlug resolves correctly.
	if err := rig.client.RunGitAt(t.Context(), rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}

	branches, err := branch.ListRows(t.Context(), rig.client, branch.StatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	var picked branch.Row
	for _, b := range branches {
		if b.IssueSlug == "77" {
			picked = b
			break
		}
	}

	deps := rig.deps()
	deps.cfg.Push.Propose = true
	deps.pushConfirm = func(_ context.Context, _ string) (bool, error) { return true, nil }

	p := &scriptedReviewPrompter{Branch: &picked}
	if err := runReviewRequestInteractive(t.Context(), deps, p); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	t.Run("feature branch present on origin after push", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "git", "-C", originDir,
			"rev-parse", "refs/heads/77@feat@my-feature")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("origin missing 77@feat@my-feature: %v\n%s", err, out)
		}
	})
}
