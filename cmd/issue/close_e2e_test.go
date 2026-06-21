package issue

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// closeTestRig bundles a real on-disk git repo, a seeded store, and a fake
// tracker so each E2E test sets up state in one line.
type closeTestRig struct {
	dir     string
	client  *git.Client
	store   *store.Store
	tracker *fake.Tracker
	cfg     *config.AppConfig
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

// newCloseRig initialises a temp git repo with a "main" branch carrying one
// commit, a feature branch ABC-1@feat@add-thing one commit ahead of main, a
// store with the matching in-progress row, and a fake tracker.
//
// The returned rig embeds the IO buffers so tests can assert on stdout/stderr.
func newCloseRig(t *testing.T) *closeTestRig {
	t.Helper()

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	runGit("checkout", "-b", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "feat: add thing")
	runGit("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	trackerType := "fake"
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "Add thing", StatusID: store.StatusIDInProgress, TrackerType: &trackerType},
		&store.Branch{Name: "ABC-1@feat@add-thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"

	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T, want *fake.Tracker", rawT)
	}

	return &closeTestRig{
		dir: dir, client: client, store: s, tracker: fakeT, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

func (r *closeTestRig) deps() closeDeps {
	return closeDeps{client: r.client, store: r.store, cfg: r.cfg, tracker: r.tracker}
}

// pickedBranchRow returns the BranchRow the picker would have returned for
// the seeded branch. Used by every scriptedPrompter setup.
func (r *closeTestRig) pickedBranchRow() *store.BranchRow {
	return &store.BranchRow{
		IssueID:    1, // first row in the empty store
		IssueSlug:  "ABC-1",
		Title:      "Add thing",
		BranchName: "ABC-1@feat@add-thing",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}
}

// assertHeadSubject asserts the subject line of HEAD on branch matches want.
func assertHeadSubject(t *testing.T, dir, branch, want string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log %s: %v", branch, err)
	}
	got := string(bytes.TrimSpace(out))
	if got != want {
		t.Errorf("HEAD subject on %q = %q, want %q", branch, got, want)
	}
}

// assertBranchAbsent asserts that branch no longer exists locally.
func assertBranchAbsent(t *testing.T, c *git.Client, branch string) {
	t.Helper()

	exists, err := c.BranchExists(branch)
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Errorf("branch %q still exists; expected it to be deleted", branch)
	}
}

func TestClose_RebaseHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyRebase,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("main HEAD carries the new commit subject", func(t *testing.T) {
		// Rebase strategy lands the squashed merge directly on main via the
		// post-commit fast-forward.
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})

	t.Run("feature branch is deleted", func(t *testing.T) {
		// force=true for rebase.
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 || branches[0].BranchName != "ABC-1@feat@add-thing" {
			t.Errorf("expected one merged branch row for ABC-1@feat@add-thing, got %+v", branches)
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if got, want := len(rig.tracker.RecordedUpdates), 1; got != want {
			t.Fatalf("RecordedUpdates len = %d, want %d", got, want)
		}
		if rig.tracker.RecordedUpdates[0] != (fake.Update{IssueID: "ABC-1", StatusName: "Closed"}) {
			t.Errorf("RecordedUpdates[0] = %+v, want {ABC-1 Closed}", rig.tracker.RecordedUpdates[0])
		}
	})
}

func TestClose_SquashHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): squash-close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	// Squash strategy: the merge commit lands on the CURRENT branch (the
	// feature branch — MergeSquash stages, then Commit fires on whatever HEAD
	// is). We then delete the feature branch, so the merge commit lives only
	// on its reflog. No head-subject assertion — store/tracker state is what's
	// observable.

	t.Run("feature branch is deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 {
			t.Fatalf("expected one merged branch row, got %d (%+v)", len(branches), branches)
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 {
			t.Fatalf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
		}
	})
}

func TestClose_ClassicHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyClassic,
		Confirm:       true,
		Message:       []byte("Merge ABC-1 into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false, // exercise the "don't delete" path
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("main HEAD carries the merge commit", func(t *testing.T) {
		// Classic strategy: merge --no-ff lands the merge commit directly on main.
		assertHeadSubject(t, rig.dir, "main", "Merge ABC-1 into main")
	})

	t.Run("feature branch is retained", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("feature branch was deleted; expected it to remain (DeleteBranch=false)")
		}
	})

	t.Run("store records branch as merged", func(t *testing.T) {
		branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(branches) != 1 {
			t.Fatalf("expected one merged branch row, got %d", len(branches))
		}
	})

	t.Run("tracker recorded one UpdateIssueStatus call", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 {
			t.Errorf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
		}
	})
}

func TestClose_NoInProgressBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Bare repo init — no seeded branch row.
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	// PickBranch should never be called — but if runClose mis-routes, fail loudly.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no branches exist")}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the empty-list message", func(t *testing.T) {
		if got := stdout.String(); !strings.Contains(got, "No in-progress branches") {
			t.Errorf("stdout = %q, want it to contain 'No in-progress branches'", got)
		}
	})
}

func TestClose_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:   rig.pickedBranchRow(),
		Strategy: StrategyRebase,
		Confirm:  false, // operator declines
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("stdout shows the Aborted message", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
			t.Errorf("stdout = %q, want it to contain 'Aborted.'", got)
		}
	})

	t.Run("feature branch is preserved", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("feature branch was deleted on user-abort path")
		}
	})

	t.Run("tracker recorded no updates", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 0 {
			t.Errorf("RecordedUpdates = %+v, want empty on abort", rig.tracker.RecordedUpdates)
		}
	})
}

func TestClose_PrefillHasFixFooter(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Give the config a footer item so IssueHint.Prefill can populate it.
	rig.cfg.CommitMessage.Items = []config.CommitItem{
		{Name: "subject", Required: true},
		{Name: "footer"},
	}

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyRebase,
		Confirm:       true,
		Message:       []byte("feat: close ABC-1\n\nCloses #ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("ComposeMessage prefill has footer Closes #ABC-1", func(t *testing.T) {
		got, ok := prompter.CapturedPrefill["footer"]
		if !ok {
			t.Error("prefill missing 'footer' key")
		} else if got != "Closes #ABC-1" {
			t.Errorf("prefill[footer] = %q, want %q", got, "Closes #ABC-1")
		}
	})
}

func TestClose_ConflictAborts(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Create a conflicting change on main before calling runClose.
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Touch the same file on main with a different content, creating a dry-run conflict.
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("main-conflict\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt on main: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "chore: conflict")

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
		// Strategy/Message irrelevant — the dry-run fails before either is asked.
	}

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("runClose returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("runClose: expected an error from the dry-run conflict, got nil")
		}
	})

	t.Run("error mentions merge conflicts", func(t *testing.T) {
		if err == nil {
			t.Skip("no error returned (covered by prior subtest)")
		}
		if !strings.Contains(err.Error(), "merge conflicts") {
			t.Errorf("error = %v, want it to mention 'merge conflicts'", err)
		}
	})

	t.Run("stdout lists the conflicting files", func(t *testing.T) {
		if got := rig.stdout.String(); !strings.Contains(got, "Conflicts detected") {
			t.Errorf("stdout = %q, want it to mention 'Conflicts detected'", got)
		}
	})
}

// seedReviewRef writes a git review ref so reviewPreflight (which reads the
// ref as authoritative) can see the correct status in tests.
func seedReviewRef(t *testing.T, rig *closeTestRig, issueSlug string, status store.ReviewStatus, round int) {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", rig.dir, "rev-parse", "ABC-1@feat@add-thing").Output()
	if err != nil {
		t.Fatalf("rev-parse feature branch: %v", err)
	}
	ref := git.ReviewRef{
		Status:     string(status),
		Round:      round,
		FeatureSHA: strings.TrimSpace(string(out)),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := rig.client.WriteReviewRef(t.Context(), issueSlug, ref, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
}

func TestClose_ReviewPreflight(t *testing.T) {
	t.Parallel()

	t.Run("blocked when branch is in_review", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		if _, err := rig.store.InsertReview(t.Context(), "ABC-1", "alice"); err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		seedReviewRef(t, rig, "ABC-1", store.ReviewStatusInReview, 1)

		p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
		err := runClose(t.Context(), rig.deps(), p)

		t.Run("returns error", func(t *testing.T) {
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
		t.Run("error wraps ErrBranchLockedForReview", func(t *testing.T) {
			if !errors.Is(err, ErrBranchLockedForReview) {
				t.Errorf("expected ErrBranchLockedForReview, got: %v", err)
			}
		})
	})

	t.Run("blocked when changes_requested", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		row, err := rig.store.InsertReview(t.Context(), "ABC-1", "alice")
		if err != nil {
			t.Fatalf("InsertReview: %v", err)
		}
		if err := rig.store.UpdateReviewStatus(t.Context(), row.ID, store.ReviewStatusChangesRequested, false); err != nil {
			t.Fatalf("UpdateReviewStatus: %v", err)
		}
		seedReviewRef(t, rig, "ABC-1", store.ReviewStatusChangesRequested, 1)

		p := &scriptedPrompter{Branch: rig.pickedBranchRow()}
		err = runClose(t.Context(), rig.deps(), p)

		t.Run("returns error", func(t *testing.T) {
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
		t.Run("error wraps ErrReviewChangesRequested", func(t *testing.T) {
			if !errors.Is(err, ErrReviewChangesRequested) {
				t.Errorf("expected ErrReviewChangesRequested, got: %v", err)
			}
		})
	})
}

// TestClose_ReviewPreflight_IncorporatesRemoteOnlyReviewerCommits verifies
// Phase 10: Dan pushed reviewer commits to origin/X.2@review but Bob never
// checked out X.2@review locally. reviewPreflight must still detect the commits
// (via the remote tracking ref) and fast-forward the feature branch.
func TestClose_ReviewPreflight_IncorporatesRemoteOnlyReviewerCommits(t *testing.T) {
	t.Parallel()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	bobDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}

	// ----- origin -----
	if err := os.MkdirAll(originDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	runIn(originDir, "init", "--bare", "--initial-branch=main")

	seedDir := t.TempDir()
	runIn(seedDir, "init", "--initial-branch=main")
	runIn(seedDir, "config", "user.name", "Seed")
	runIn(seedDir, "config", "user.email", "seed@example.com")
	runIn(seedDir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seedDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "base.txt")
	runIn(seedDir, "commit", "-m", "chore: init")
	runIn(seedDir, "remote", "add", "origin", originDir)
	runIn(seedDir, "push", "origin", "main")

	// Feature branch ABC-1@feat@thing
	runIn(seedDir, "checkout", "-b", "ABC-1@feat@thing")
	if err := os.WriteFile(filepath.Join(seedDir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "feat: implement")
	runIn(seedDir, "push", "origin", "ABC-1@feat@thing")

	// Reviewer creates ABC-1@review and pushes a fix.
	runIn(seedDir, "checkout", "-b", "ABC-1@review")
	if err := os.WriteFile(filepath.Join(seedDir, "feature.txt"), []byte("feature\nreviewer fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runIn(seedDir, "add", "feature.txt")
	runIn(seedDir, "commit", "-m", "fix: reviewer fix")
	runIn(seedDir, "push", "origin", "ABC-1@review")
	runIn(seedDir, "checkout", "main")

	// ----- Bob's clone: never checks out ABC-1@review locally -----
	runIn(bobDir, "init", "--initial-branch=main")
	runIn(bobDir, "config", "user.name", "Bob")
	runIn(bobDir, "config", "user.email", "bob@example.com")
	runIn(bobDir, "config", "commit.gpgsign", "false")
	runIn(bobDir, "remote", "add", "origin", originDir)
	runIn(bobDir, "fetch", "origin")
	runIn(bobDir, "checkout", "main")
	runIn(bobDir, "checkout", "-b", "ABC-1@feat@thing", "origin/ABC-1@feat@thing")
	// ABC-1@review deliberately NOT checked out locally.

	stdout := &bytes.Buffer{}
	bobClient, err := git.NewClientAt(&pkg.IO{
		In: bytes.NewReader(nil), Out: stdout, Err: &bytes.Buffer{},
	}, bobDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	bobStore, err := store.Open(t.Context(), filepath.Join(bobDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = bobStore.Close() })

	if err := bobStore.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "thing", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "ABC-1@feat@thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Write approved review ref and push it to origin — FetchReviewRefs uses
	// --prune, so any ref not on the remote would be deleted before the close reads it.
	featureSHA, _ := bobClient.ResolveRef("refs/heads/ABC-1@feat@thing")
	reviewRef := git.ReviewRef{
		Status: "approved", Round: 1,
		FeatureSHA: featureSHA.String(),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := bobClient.WriteReviewRef(t.Context(), "ABC-1", reviewRef, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
	// Push the ref so --prune doesn't remove it from local on the next fetch.
	runIn(bobDir, "push", "--force", "origin", "refs/zf/reviews/ABC-1")

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	deps := closeDeps{client: bobClient, store: bobStore, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID: 1, IssueSlug: "ABC-1",
		BranchName: "ABC-1@feat@thing", Type: "feat",
		Status: store.BranchStatusInProgress,
	}
	prompter := &scriptedPrompter{
		Branch: pickedRow, Strategy: StrategySquash, Confirm: true,
		Message: []byte("feat: close\n"), DeleteBranch: false,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("reviewer fix incorporated into feature branch", func(t *testing.T) {
		out, _ := exec.CommandContext(t.Context(), "git", "-C", bobDir,
			"log", "--oneline", "ABC-1@feat@thing").Output()
		if !strings.Contains(string(out), "reviewer fix") {
			t.Errorf("reviewer commit not in feature branch history; log:\n%s", out)
		}
	})

	t.Run("stdout says incorporating reviewer commits", func(t *testing.T) {
		if !strings.Contains(stdout.String(), "Incorporating") {
			t.Errorf("stdout = %q — expected 'Incorporating' message", stdout.String())
		}
	})
}

// TestClose_SubtaskDryRunFallsBackToRemoteBase verifies that the initial
// merge dry-run in doMerge succeeds when the base branch (a parent integration
// branch) exists only as a remote tracking ref (origin/<base>) and was never
// checked out locally. This is the failure mode from Phase 10 of the review
// E2E scenario: Bob closes X.2 but never checked out X@feat@big locally.
func TestClose_SubtaskDryRunFallsBackToRemoteBase(t *testing.T) {
	t.Parallel()

	originDir := t.TempDir()
	bobDir := t.TempDir()

	runIn := func(dir string, args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// ----- origin repo -----
	runIn(originDir, "init", "-q", "-b", "main")
	runIn(originDir, "config", "user.name", "Alice")
	runIn(originDir, "config", "user.email", "alice@example.com")
	runIn(originDir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(originDir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runIn(originDir, "add", "base.txt")
	runIn(originDir, "commit", "-m", "chore: init")

	// X@feat@big — parent integration branch.
	runIn(originDir, "checkout", "-b", "X@feat@big")
	if err := os.WriteFile(filepath.Join(originDir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runIn(originDir, "add", "parent.txt")
	runIn(originDir, "commit", "-m", "feat(X): parent")

	// X.2@feat@two — sub-task branched from X@feat@big.
	runIn(originDir, "checkout", "-b", "X.2@feat@two")
	if err := os.WriteFile(filepath.Join(originDir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	runIn(originDir, "add", "subtask.txt")
	runIn(originDir, "commit", "-m", "feat(X.2): subtask")
	runIn(originDir, "checkout", "main")

	// ----- Bob's repo: init, add remote, fetch, check out only the subtask -----
	runIn(bobDir, "init", "-q", "-b", "main")
	runIn(bobDir, "config", "user.name", "Bob")
	runIn(bobDir, "config", "user.email", "bob@example.com")
	runIn(bobDir, "config", "commit.gpgsign", "false")
	runIn(bobDir, "remote", "add", "origin", originDir)
	runIn(bobDir, "fetch", "origin")
	// DWIM: creates local main tracking origin/main.
	runIn(bobDir, "checkout", "main")
	// DWIM: creates local X.2@feat@two tracking origin/X.2@feat@two.
	runIn(bobDir, "checkout", "-b", "X.2@feat@two", "origin/X.2@feat@two")
	// Return to main; X@feat@big is intentionally NOT checked out locally.
	runIn(bobDir, "checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, bobDir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	// Write branch refs locally so parentSlug resolves without a remote fetch.
	parentBR := git.BranchRef{
		IssueSlug:  "X",
		BranchName: "X@feat@big",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X", parentBR); err != nil {
		t.Fatalf("WriteBranchRef X: %v", err)
	}
	childBR := git.BranchRef{
		IssueSlug:  "X.2",
		BranchName: "X.2@feat@two",
		ParentSlug: "X",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X.2", childBR); err != nil {
		t.Fatalf("WriteBranchRef X.2: %v", err)
	}

	s, err := store.Open(t.Context(), bobDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.2", Title: "part-two", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X.2@feat@two", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	deps := closeDeps{client: client, store: s, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "X.2",
		Title:      "part-two",
		BranchName: "X.2@feat@two",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:       pickedRow,
		Strategy:     StrategySquash,
		Confirm:      true,
		Message:      []byte("feat(X.2): close\n"),
		DeleteBranch: true,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("squash commit landed on parent integration branch", func(t *testing.T) {
		// X@feat@big is created locally by git checkout DWIM during MergeSquash.
		assertHeadSubject(t, bobDir, "X@feat@big", "feat(X.2): close")
	})

	t.Run("main is unchanged", func(t *testing.T) {
		assertHeadSubject(t, bobDir, "main", "chore: init")
	})
}

// TestClose_CrossMachine_UsesParentBranchRef verifies that close correctly
// merges a sub-task into its parent integration branch even when the local
// SQLite store has no parent-child relation record (cross-machine scenario:
// a developer who fetched and checked out the branch without running issue start).
func TestClose_CrossMachine_UsesParentBranchRef(t *testing.T) {
	t.Parallel()

	// Build a repo that looks like Bob's machine:
	//   - main branch with one commit
	//   - X@feat@big-feature (parent integration branch), one commit ahead of main
	//   - X.1@feat@part-one (sub-task), one commit ahead of X@feat@big-feature
	//   - refs/zf/branches/X    → {branch_name: "X@feat@big-feature"}
	//   - refs/zf/branches/X.1  → {branch_name: "X.1@feat@part-one", parent_slug: "X"}
	//   - SQLite store: only X.1 branch row (no issue_relations record)

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Bob")
	runGit("config", "user.email", "bob@example.com")
	runGit("config", "commit.gpgsign", "false")

	// main commit
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	// X@feat@big-feature
	runGit("checkout", "-b", "X@feat@big-feature")
	if err := os.WriteFile(filepath.Join(dir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runGit("add", "parent.txt")
	runGit("commit", "-m", "feat(X): parent commit")

	// X.1@feat@part-one (branched from X@feat@big-feature)
	runGit("checkout", "-b", "X.1@feat@part-one")
	if err := os.WriteFile(filepath.Join(dir, "subtask.txt"), []byte("subtask\n"), 0o644); err != nil {
		t.Fatalf("write subtask.txt: %v", err)
	}
	runGit("add", "subtask.txt")
	runGit("commit", "-m", "feat(X.1): subtask commit")

	runGit("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	// Write branch refs (as Alice would have pushed them; Bob fetched them).
	parentRef := git.BranchRef{
		IssueSlug:  "X",
		BranchName: "X@feat@big-feature",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X", parentRef); err != nil {
		t.Fatalf("WriteBranchRef X: %v", err)
	}

	childRef := git.BranchRef{
		IssueSlug:  "X.1",
		BranchName: "X.1@feat@part-one",
		ParentSlug: "X",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteBranchRef(t.Context(), "X.1", childRef); err != nil {
		t.Fatalf("WriteBranchRef X.1: %v", err)
	}

	// Store: only X.1 branch row; no issue_relations.
	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.1", Title: "part-one", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X.1@feat@part-one", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	// Seed review ref so reviewPreflight passes (approved, no reviewer commits).
	reviewRef := git.ReviewRef{
		Status: "approved",
		Round:  1,
		// FeatureSHA is not checked by reviewPreflight for approved status
		// when no X.1@review branch exists.
		FeatureSHA: "ignored",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := client.WriteReviewRef(t.Context(), "X.1", reviewRef, ""); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	pickedRow := &store.BranchRow{
		IssueID:    1,
		IssueSlug:  "X.1",
		Title:      "part-one",
		BranchName: "X.1@feat@part-one",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:       pickedRow,
		Strategy:     StrategySquash,
		Confirm:      true,
		Message:      []byte("feat(X.1): close\n"),
		DeleteBranch: true,
	}

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("merged into parent branch not main", func(t *testing.T) {
		// X@feat@big-feature should carry the squash commit; main should not.
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "X@feat@big-feature")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log X@feat@big-feature: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "feat(X.1): close" {
			t.Errorf("X@feat@big-feature HEAD subject = %q, want %q", got, "feat(X.1): close")
		}
	})

	t.Run("main is unchanged", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "git", "log", "-1", "--format=%s", "main")
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log main: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if got != "chore: init" {
			t.Errorf("main HEAD = %q, expected it to be unchanged (%q)", got, "chore: init")
		}
	})
}

// TestClose_ParentClose_ReconcilesMergedChildFromRef covers Phase 12 of the
// demo: Alice tries to close parent X, but her local store still has X.2 as
// in_progress (Bob closed it in his clone). The branch ref for X.2 has
// Merged=true (written by Bob's close). reconcileChildrenFromRefs should
// update Alice's store so ChildrenAllMerged passes.
func TestClose_ParentClose_ReconcilesMergedChildFromRef(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t) // main + ABC-1@feat@add-thing

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Rename the seeded branch to simulate the parent integration branch X.
	// Create child X.1 already merged, and child X.2 in_progress (sibling's close
	// not yet reflected in this store).
	runGit("checkout", "-b", "X@feat@big")
	if err := os.WriteFile(filepath.Join(rig.dir, "parent.txt"), []byte("parent\n"), 0o644); err != nil {
		t.Fatalf("write parent.txt: %v", err)
	}
	runGit("add", "parent.txt")
	runGit("commit", "-m", "feat(X): parent")
	runGit("checkout", "main")

	// Re-seed store: parent X, and two children X.1 (merged) and X.2 (in_progress).
	trackerType := "fake"
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X", Title: "big", StatusID: store.StatusIDInProgress, TrackerType: &trackerType},
		&store.Branch{Name: "X@feat@big", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X: %v", err)
	}
	now := time.Now()
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.1", Title: "one", StatusID: store.StatusIDMerged},
		&store.Branch{Name: "X.1@feat@one", Type: "feat", StatusID: store.StatusIDMerged, MergedAt: &now},
	); err != nil {
		t.Fatalf("seed X.1: %v", err)
	}
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "X.2", Title: "two", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "X.2@feat@two", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed X.2: %v", err)
	}
	// Record parent-child relations.
	if err := rig.store.InsertIssueRelation(t.Context(), "X", "X.1"); err != nil {
		t.Fatalf("relation X→X.1: %v", err)
	}
	if err := rig.store.InsertIssueRelation(t.Context(), "X", "X.2"); err != nil {
		t.Fatalf("relation X→X.2: %v", err)
	}

	// Simulate Bob having closed X.2 in his clone: write a branch ref with Merged=true.
	mergedRef := git.BranchRef{
		IssueSlug:  "X.2",
		BranchName: "X.2@feat@two",
		ParentSlug: "X",
		CreatedAt:  now.UTC().Format(time.RFC3339),
		Merged:     true,
	}
	if _, err := rig.client.WriteBranchRef(t.Context(), "X.2", mergedRef); err != nil {
		t.Fatalf("WriteBranchRef X.2: %v", err)
	}

	// Update the store config: X@feat@big should merge into main.
	rig.cfg.Branch.Base = "main"

	xRow := &store.BranchRow{
		IssueID:    2, // second InsertIssueWithBranch call
		IssueSlug:  "X",
		Title:      "big",
		BranchName: "X@feat@big",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}

	prompter := &scriptedPrompter{
		Branch:        xRow,
		Strategy:      StrategySquash,
		Confirm:       true,
		Message:       []byte("feat(X): close into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("parent X merged into main", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(X): close into main")
	})

	t.Run("X.2 reconciled as merged in store", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		var found bool
		for _, b := range merged {
			if b.BranchName == "X.2@feat@two" {
				found = true
			}
		}
		if !found {
			t.Error("X.2@feat@two not reconciled as merged in store")
		}
	})
}
