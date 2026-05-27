package issue

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// startTestRig bundles a real on-disk git repo (one initial commit on main)
// and a fake tracker so each E2E test sets up state in one line. Unlike the
// close-flow rig, no branch is pre-seeded — start CREATES the branch.
type startTestRig struct {
	dir     string
	client  *git.Client
	tracker *fake.Tracker
	cfg     *config.AppConfig
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

// runGitIn invokes `git` in dir with the given args and t.Fatals on failure.
// Free function so newStartRig can call it before the rig struct exists.
func runGitIn(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newStartRig(t *testing.T) *startTestRig {
	t.Helper()

	dir := t.TempDir()

	runGitIn(t, dir, "init", "-q", "-b", "main")
	runGitIn(t, dir, "config", "user.name", "Test User")
	runGitIn(t, dir, "config", "user.email", "test@test.com")
	runGitIn(t, dir, "config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}

	runGitIn(t, dir, "add", "base.txt")
	runGitIn(t, dir, "commit", "-m", "chore: init")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"
	cfg.CommitTypes = []config.CommitTypeOption{
		{Name: "feat"},
		{Name: "fix"},
	}

	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}

	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T, want *fake.Tracker", rawT)
	}

	return &startTestRig{
		dir: dir, client: client, tracker: fakeT, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

// deps returns a StartDeps wired with the rig's client + fake tracker.
// flags lets the test choose Variant / TrackerFirst per case.
func (r *startTestRig) deps(flags issuepkg.IssueStartFlags) StartDeps {
	return StartDeps{Client: r.client, Cfg: r.cfg, Tracker: r.tracker, Flags: flags}
}

// noTrackerDeps returns a StartDeps with deps.Tracker = nil (manual-only path).
func (r *startTestRig) noTrackerDeps(flags issuepkg.IssueStartFlags) StartDeps {
	return StartDeps{Client: r.client, Cfg: r.cfg, Tracker: nil, Flags: flags}
}

// runGit invokes `git` inside the rig's working tree and t.Fatals on failure.
// Shared by tests that need to pre-seed the repo (e.g. collision setup).
func (r *startTestRig) runGit(t *testing.T, args ...string) {
	t.Helper()

	runGitIn(t, r.dir, args...)
}

func TestRunIssueStart_BranchHappyPath_NoTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	pickedIssue := &issuepkg.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-2",
			Subject: "Add login form",
		},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: pickedIssue,
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-2@feat@add-login-form"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("stdout reports the switch", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte(wantBranch)) {
			t.Errorf("stdout = %q, want it to mention %q", got, wantBranch)
		}
	})

	t.Run("tracker was not called", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates len = %d, want 0 on no-tracker path", got)
		}
	})
}

func TestRunIssueStart_BranchHappyPath_WithTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: true, Variant: ""}

	rig.tracker.Issues = []tracker.Issue{
		{ID: "ABC-3", Subject: "Implement OAuth", TrackerType: "fake"},
	}

	pickedFromTracker := &issuepkg.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:          "ABC-3",
			Subject:     "Implement OAuth",
			TrackerType: "fake",
		},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: pickedFromTracker,
		UseWorktree:      false,
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-3@feat@implement-oauth"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("tracker received one UpdateIssueStatus call", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 1 {
			t.Fatalf("RecordedUpdates len = %d, want 1", got)
		}

		got := rig.tracker.RecordedUpdates[0]
		want := fake.Update{IssueID: "ABC-3", StatusName: "In Progress"}
		if got != want {
			t.Errorf("RecordedUpdates[0] = %+v, want %+v", got, want)
		}
	})
}

func TestRunIssueStart_WorktreeHappyPath(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	pickedIssue := &issuepkg.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-4",
			Subject: "Add metrics",
		},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser:   pickedIssue,
		UseWorktree:     true,
		ConfirmWorktree: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-4@feat@add-metrics"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("worktree directory exists", func(t *testing.T) {
		parent := filepath.Dir(rig.dir)
		repoName := filepath.Base(rig.dir)
		wantPath := filepath.Join(parent, repoName+"--"+wantBranch)
		if _, err := os.Stat(wantPath); err != nil {
			t.Errorf("worktree dir %q not found: %v", wantPath, err)
		}
	})

	t.Run("stdout mentions the cd hint", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte("Run 'cd")) {
			t.Errorf("stdout = %q, want it to contain the 'Run cd' hint", got)
		}
	})
}

func TestRunIssueStart_BranchUserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	picked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-5", Subject: "Add cache"},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: picked,
		UseWorktree:   false,
		ConfirmBranch: false, // operator declines
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("branch not created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-5@feat@add-cache")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if exists {
			t.Error("branch was created despite operator abort")
		}
	})

	t.Run("stdout shows Aborted message", func(t *testing.T) {
		if got := rig.stdout.String(); !bytes.Contains([]byte(got), []byte("Aborted.")) {
			t.Errorf("stdout = %q, want it to mention 'Aborted.'", got)
		}
	})
}

func TestRunIssueStart_VariantOnCollision(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	rig.runGit(t, "branch", "ABC-6@feat@add-search", "main")

	picked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-6", Subject: "Add search"},
	}

	variantBranch, err := rebuildVariantBranch(picked, "spike")
	if err != nil {
		t.Fatalf("rebuildVariantBranch: %v", err)
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser:  picked,
		ConflictBranch: variantBranch,
		UseWorktree:    false,
		ConfirmBranch:  true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("variant branch exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-6@feat@add-search@spike")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Error("variant branch ABC-6@feat@add-search@spike was not created")
		}
	})

	t.Run("original branch is untouched", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-6@feat@add-search")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Error("pre-existing branch ABC-6@feat@add-search was deleted unexpectedly")
		}
	})
}

func TestRunIssueStart_AbortOnCollision(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	rig.runGit(t, "branch", "ABC-7@feat@add-export", "main")

	picked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-7", Subject: "Add export"},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: picked,
		ConflictAbort: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("no new branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-7@feat@add-export@spike")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if exists {
			t.Error("variant branch was unexpectedly created on abort path")
		}
	})

	t.Run("tracker untouched", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0 on abort path", got)
		}
	})
}

func TestRunIssueStart_TrackerListErrorFallsBackToManual(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: true, Variant: ""}

	// Fake tracker starts with empty Issues — that triggers the "no open issues" fallback.

	manualPicked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-8", Subject: "Manual fallback"},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:    true, // operator accepts the toggle
		IssueFromUser: manualPicked,
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("NotifyTrackerError was called once", func(t *testing.T) {
		if got := prompter.TrackerErrorNotifications; got != 1 {
			t.Errorf("TrackerErrorNotifications = %d, want 1", got)
		}
	})

	t.Run("manual fallback branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-8@feat@manual-fallback")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Error("manual fallback branch was not created")
		}
	})
}

func TestRunIssueStart_NoTrackerStatusUpdate(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: true, Variant: ""}

	rig.tracker.Issues = []tracker.Issue{
		{ID: "ABC-9", Subject: "Add lints", TrackerType: "fake"},
	}

	pickedFromTracker := &issuepkg.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:          "ABC-9",
			Subject:     "Add lints",
			TrackerType: "fake",
		},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: pickedFromTracker,
		UseWorktree:      false,
		ConfirmBranch:    true,
		TrackerStatus:    "", // empty = skip
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("branch was still created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-9@feat@add-lints")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}

		if !exists {
			t.Error("branch was not created when tracker status was skipped")
		}
	})

	t.Run("tracker received no UpdateIssueStatus calls", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0 when TrackerStatus is empty", got)
		}
	})
}

func TestRunIssueStart_UseWorktreeConfigOverride(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)

	// Override the worktree toggle via config. PickUseWorktree must NOT be
	// invoked when cfg.Branch.UseWorktree is non-nil.
	override := true
	rig.cfg.Branch.UseWorktree = &override

	flags := issuepkg.IssueStartFlags{TrackerFirst: false, Variant: ""}

	picked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-10", Subject: "Worktree override"},
	}

	// Tripwire: PickUseWorktree returning an error would surface here. The
	// scripted prompter's UseWorktree=false would route to createBranchFlow if
	// the toggle were consulted — instead we expect it to be skipped entirely
	// and the override (true) to drive the worktree flow.
	prompter := &scriptedStartPrompter{
		IssueFromUser:   picked,
		UseWorktree:     false, // intentionally opposite of the override
		UseWorktreeErr:  errors.New("PickUseWorktree should not be called when override is set"),
		ConfirmWorktree: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-10@feat@worktree-override"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("worktree directory exists (override forced worktree path)", func(t *testing.T) {
		parent := filepath.Dir(rig.dir)
		repoName := filepath.Base(rig.dir)
		wantPath := filepath.Join(parent, repoName+"--"+wantBranch)
		if _, err := os.Stat(wantPath); err != nil {
			t.Errorf("worktree dir %q not found: %v", wantPath, err)
		}
	})
}

func TestRunIssueStart_DeclinesTrackerTogglesToManual(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{TrackerFirst: true, Variant: ""}

	// Tracker is configured (rig.cfg.IssueTracker.Type = "fake") but the
	// operator declines the toggle. pickIssue should fall through to
	// issue.GetFromUser → prompter.PickIssueFromUser.
	manualPicked := &issuepkg.Issue{
		Type:  "feat",
		Issue: tracker.Issue{ID: "ABC-11", Subject: "Manual choice"},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:    false, // operator declines the tracker toggle
		IssueFromUser: manualPicked,
		// Tripwire: if pickIssue mis-routes to GetFromTracker, this error fires.
		IssueFromTrackerErr: errors.New("PickIssueFromTracker should not be called when UseTracker=false"),
		UseWorktree:         false,
		ConfirmBranch:       true,
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("manual branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-11@feat@manual-choice")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("ABC-11@feat@manual-choice was not created")
		}
	})

	t.Run("tracker received no UpdateIssueStatus calls", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0 when tracker toggle declined", got)
		}
	})
}
